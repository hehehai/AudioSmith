// Package filter has IIR biquads (designs from the RBJ "Audio EQ Cookbook"
// formulas), the ITU-R BS.1770 K-weighting pair at any sample rate, and
// Kaiser-windowed sinc FIR design for resampling and oversampling.
package filter

import (
	"math"
	"math/cmplx"
)

// Biquad holds normalised coefficients (a0 = 1) of
// y[n] = b0 x[n] + b1 x[n−1] + b2 x[n−2] − a1 y[n−1] − a2 y[n−2].
type Biquad struct {
	B0, B1, B2, A1, A2 float64
}

func norm(b0, b1, b2, a0, a1, a2 float64) Biquad {
	return Biquad{b0 / a0, b1 / a0, b2 / a0, a1 / a0, a2 / a0}
}

func rbj(rate int, f, q float64) (cosw, alpha float64) {
	w := 2 * math.Pi * f / float64(rate)
	return math.Cos(w), math.Sin(w) / (2 * q)
}

// HighPass is a 12 dB/octave high-pass at f Hz (q 0.7071 is Butterworth).
func HighPass(rate int, f, q float64) Biquad {
	c, al := rbj(rate, f, q)
	return norm((1+c)/2, -(1 + c), (1+c)/2, 1+al, -2*c, 1-al)
}

// LowPass is a 12 dB/octave low-pass at f Hz.
func LowPass(rate int, f, q float64) Biquad {
	c, al := rbj(rate, f, q)
	return norm((1-c)/2, 1-c, (1-c)/2, 1+al, -2*c, 1-al)
}

// BandPass is a constant 0 dB peak gain band-pass centred on f Hz.
func BandPass(rate int, f, q float64) Biquad {
	c, al := rbj(rate, f, q)
	return norm(al, 0, -al, 1+al, -2*c, 1-al)
}

// Peaking boosts or cuts gainDB around f Hz with bandwidth set by q.
func Peaking(rate int, f, q, gainDB float64) Biquad {
	c, al := rbj(rate, f, q)
	a := math.Pow(10, gainDB/40)
	return norm(1+al*a, -2*c, 1-al*a, 1+al/a, -2*c, 1-al/a)
}

// LowShelf changes gainDB below f Hz (q 0.7071 is the steepest monotonic slope).
func LowShelf(rate int, f, q, gainDB float64) Biquad {
	c, al := rbj(rate, f, q)
	a := math.Pow(10, gainDB/40)
	sa := 2 * math.Sqrt(a) * al
	return norm(a*((a+1)-(a-1)*c+sa), 2*a*((a-1)-(a+1)*c), a*((a+1)-(a-1)*c-sa),
		(a+1)+(a-1)*c+sa, -2*((a-1)+(a+1)*c), (a+1)+(a-1)*c-sa)
}

// HighShelf changes gainDB above f Hz.
func HighShelf(rate int, f, q, gainDB float64) Biquad {
	c, al := rbj(rate, f, q)
	a := math.Pow(10, gainDB/40)
	sa := 2 * math.Sqrt(a) * al
	return norm(a*((a+1)+(a-1)*c+sa), -2*a*((a-1)+(a+1)*c), a*((a+1)+(a-1)*c-sa),
		(a+1)-(a-1)*c+sa, 2*((a-1)-(a+1)*c), (a+1)-(a-1)*c-sa)
}

// KWeighting returns the two BS.1770-4 K-weighting stages (the head
// "pre-filter" high shelf, then the RLB high-pass) for any sample rate,
// from the analogue prototype parameters the 48 kHz coefficients of the
// Recommendation were derived from (bilinear transform with prewarping).
// At 48 kHz they equal the Recommendation's tables to ~1e-14.
func KWeighting(rate int) (shelf, highpass Biquad) {
	fs := float64(rate)
	{
		const f0, g, q = 1681.974450955533, 3.999843853973347, 0.7071752369554196
		k := math.Tan(math.Pi * f0 / fs)
		vh := math.Pow(10, g/20)
		vb := math.Pow(vh, 0.4996667741545416)
		a0 := 1 + k/q + k*k
		shelf = Biquad{
			B0: (vh + vb*k/q + k*k) / a0,
			B1: 2 * (k*k - vh) / a0,
			B2: (vh - vb*k/q + k*k) / a0,
			A1: 2 * (k*k - 1) / a0,
			A2: (1 - k/q + k*k) / a0,
		}
	}
	{
		const f0, q = 38.13547087602444, 0.5003270373238773
		k := math.Tan(math.Pi * f0 / fs)
		a0 := 1 + k/q + k*k
		highpass = Biquad{B0: 1, B1: -2, B2: 1, A1: 2 * (k*k - 1) / a0, A2: (1 - k/q + k*k) / a0}
	}
	return shelf, highpass
}

// Response returns the complex frequency response at f Hz.
func (b Biquad) Response(rate int, f float64) complex128 {
	z := cmplx.Exp(complex(0, -2*math.Pi*f/float64(rate)))
	num := complex(b.B0, 0) + complex(b.B1, 0)*z + complex(b.B2, 0)*z*z
	den := 1 + complex(b.A1, 0)*z + complex(b.A2, 0)*z*z
	return num / den
}

// GainDB returns the magnitude response at f Hz in dB.
func (b Biquad) GainDB(rate int, f float64) float64 {
	return 20 * math.Log10(cmplx.Abs(b.Response(rate, f)))
}

// Chain is a cascade of biquads over interleaved multichannel audio, with
// per-channel state (transposed direct form II, float64).
type Chain struct {
	stages   []Biquad
	channels int
	z        []float64 // [channel][stage][2]
}

// NewChain returns a cascade of stages for channels channels.
func NewChain(channels int, stages ...Biquad) *Chain {
	return &Chain{stages: stages, channels: channels, z: make([]float64, channels*len(stages)*2)}
}

// Reset clears the filter state.
func (c *Chain) Reset() { clear(c.z) }

// Process filters interleaved samples in place.
func (c *Chain) Process(samples []float32) {
	ns, nc := len(c.stages), c.channels
	for i := 0; i+nc <= len(samples); i += nc {
		for ch := range nc {
			x := float64(samples[i+ch])
			z := c.z[ch*ns*2:]
			for s, q := range c.stages {
				y := q.B0*x + z[2*s]
				z[2*s] = q.B1*x - q.A1*y + z[2*s+1]
				z[2*s+1] = q.B2*x - q.A2*y
				x = y
			}
			samples[i+ch] = float32(x)
		}
	}
}

// Process64 filters interleaved float64 samples in place.
func (c *Chain) Process64(samples []float64) {
	ns, nc := len(c.stages), c.channels
	for i := 0; i+nc <= len(samples); i += nc {
		for ch := range nc {
			x := samples[i+ch]
			z := c.z[ch*ns*2:]
			for s, q := range c.stages {
				y := q.B0*x + z[2*s]
				z[2*s] = q.B1*x - q.A1*y + z[2*s+1]
				z[2*s+1] = q.B2*x - q.A2*y
				x = y
			}
			samples[i+ch] = x
		}
	}
}

// besselI0 is the zeroth-order modified Bessel function of the first kind.
func besselI0(x float64) float64 {
	sum, term := 1.0, 1.0
	y := x * x / 4
	for k := 1; k < 64; k++ {
		term *= y / float64(k*k)
		sum += term
		if term < sum*1e-17 {
			break
		}
	}
	return sum
}

// Kaiser returns the Kaiser window w(x) for x in [−1, 1] (0 outside).
func Kaiser(x, beta float64) float64 {
	if x < -1 || x > 1 {
		return 0
	}
	return besselI0(beta*math.Sqrt(1-x*x)) / besselI0(beta)
}

// KaiserBeta returns the β giving about atten dB of stopband attenuation.
func KaiserBeta(atten float64) float64 {
	switch {
	case atten > 50:
		return 0.1102 * (atten - 8.7)
	case atten >= 21:
		return 0.5842*math.Pow(atten-21, 0.4) + 0.07886*(atten-21)
	}
	return 0
}

// Sinc is sin(πx)/(πx).
func Sinc(x float64) float64 {
	if x == 0 {
		return 1
	}
	return math.Sin(math.Pi*x) / (math.Pi * x)
}

// LowpassFIR designs a linear-phase low-pass of taps taps with cutoff
// (−6 dB point) at cutoff cycles per sample (0 < cutoff < 0.5), Kaiser
// window β, and DC gain gain.
func LowpassFIR(taps int, cutoff, beta, gain float64) []float64 {
	h := make([]float64, taps)
	mid := float64(taps-1) / 2
	var sum float64
	for i := range h {
		t := float64(i) - mid
		h[i] = 2 * cutoff * Sinc(2*cutoff*t) * Kaiser(t/(mid+1), beta)
		sum += h[i]
	}
	for i := range h {
		h[i] *= gain / sum
	}
	return h
}
