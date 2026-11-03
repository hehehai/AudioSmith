package filter

import (
	"math"
	"slices"
)

// TruePeakFactor returns the oversampling factor used for true-peak
// detection at rate: enough to reach ≥ 192 kHz, between 1 and 8
// (48 kHz → 4×, the BS.1770-4 Annex 2 figure; 44.1 kHz → 5×; 24 kHz → 8×;
// 96 kHz → 2×; ≥ 192 kHz → 1×, i.e. sample peak).
func TruePeakFactor(rate int) int {
	return min(max((192000+rate-1)/rate, 1), 8)
}

// TruePeak finds inter-sample peaks: it upsamples each channel by
// TruePeakFactor with a Kaiser-windowed sinc interpolator (48 taps per
// phase, β 8, cutoff at the input Nyquist, so phase 0 is the input itself)
// and reports, per input sample, the largest magnitude among the sample and
// the interpolated points after it. It streams with Delay() frames of latency.
type TruePeak struct {
	l, half, taps, ch int
	table             []float32 // phases 1..l-1, taps each
	buf               [][]float32
	pending           int // frames in buf not yet reported
	l1                float32
	thresh            []float32
	stage             []float32
}

const tpHalf = 24

// NewTruePeak returns a detector for channels channels at rate.
func NewTruePeak(rate, channels int) *TruePeak {
	t := &TruePeak{l: TruePeakFactor(rate), half: tpHalf, taps: 2 * tpHalf, ch: channels}
	t.table = make([]float32, max(t.l-1, 0)*t.taps)
	for p := 1; p < t.l; p++ {
		frac := float64(p) / float64(t.l)
		row := t.table[(p-1)*t.taps : p*t.taps]
		var sum float64
		w := make([]float64, t.taps)
		for i := range t.taps {
			x := float64(i-t.half+1) - frac
			w[i] = Sinc(x) * Kaiser(x/float64(t.half), 8)
			sum += w[i]
		}
		var l1 float32
		for i := range w {
			row[i] = float32(w[i] / sum)
			l1 += float32(math.Abs(w[i] / sum))
		}
		t.l1 = max(t.l1, l1)
	}
	t.Reset()
	return t
}

// SetThreshold lets the detector skip work: for channel c, frames whose
// true peak provably cannot exceed th[c] (by the interpolator's L1 bound
// over the neighbourhood) are reported as their sample magnitude instead.
// Values above th[c] stay exact. nil (the default) computes every frame.
// A meter tracking a running maximum passes that maximum; a limiter passes
// its ceiling.
func (t *TruePeak) SetThreshold(th []float32) { t.thresh = th }

// Factor returns the oversampling factor.
func (t *TruePeak) Factor() int { return t.l }

// Delay returns the latency in frames: Process reports frame n once frame
// n+Delay() has been given.
func (t *TruePeak) Delay() int { return t.half }

// Reset clears the history.
func (t *TruePeak) Reset() {
	t.buf = make([][]float32, t.ch)
	for c := range t.buf {
		t.buf[c] = make([]float32, t.half-1, 4096)
	}
	t.pending = 0
}

// Process consumes interleaved frames and appends to dst, interleaved, the
// peak magnitude of every frame that has become reportable.
func (t *TruePeak) Process(dst, in []float32) []float32 {
	n := len(in) / t.ch
	for c := range t.ch {
		b := t.buf[c]
		for i := range n {
			b = append(b, in[i*t.ch+c])
		}
		t.buf[c] = b
	}
	t.pending += n
	return t.emit(dst, false)
}

// Flush reports the last Delay() frames (as if followed by silence).
func (t *TruePeak) Flush(dst []float32) []float32 {
	for c := range t.ch {
		t.buf[c] = append(t.buf[c], make([]float32, t.half)...)
	}
	dst = t.emit(dst, true)
	t.Reset()
	return dst
}

func (t *TruePeak) emit(dst []float32, flush bool) []float32 {
	// buf[c][k] holds frame base+k; the oldest pending frame sits at index
	// len - pending (before flush padding) and needs half frames after it.
	pad := 0
	if flush {
		pad = t.half
	}
	first := len(t.buf[0]) - pad - t.pending // index of the oldest unreported frame
	ready := t.pending
	if !flush {
		ready = t.pending - t.half
	}
	const block = 32
	for k0 := 0; k0 < ready; k0 += block {
		k1 := min(k0+block, ready)
		for c := range t.ch {
			b := t.buf[c]
			if t.thresh != nil {
				// Bound every interpolated value in the block.
				var m float32
				for _, v := range b[first+k0-t.half+1 : first+k1+t.half] {
					m = max(m, float32(math.Abs(float64(v))))
				}
				if m*t.l1 <= t.thresh[c] {
					for k := k0; k < k1; k++ {
						t.stage = append(t.stage, float32(math.Abs(float64(b[first+k]))))
					}
					continue
				}
			}
			for k := k0; k < k1; k++ {
				idx := first + k
				m := float32(math.Abs(float64(b[idx])))
				win := b[idx-t.half+1 : idx+t.half+1]
				for p := 1; p < t.l; p++ {
					s := Dot(win, t.table[(p-1)*t.taps:p*t.taps])
					if s < 0 {
						s = -s
					}
					m = max(m, s)
				}
				t.stage = append(t.stage, m)
			}
		}
		dst = t.interleave(dst, k1-k0)
	}
	if ready > 0 {
		t.pending -= ready
	}
	// Keep only the history the next frames need.
	keep := t.pending + pad + t.half - 1
	if drop := len(t.buf[0]) - keep; drop > 8192 && !flush {
		for c := range t.ch {
			m := copy(t.buf[c], t.buf[c][drop:])
			t.buf[c] = t.buf[c][:m]
		}
	}
	return dst
}

// Dot returns Σ x[i]·h[i] over len(x) (h must be at least as long).
func Dot(x, h []float32) float32 {
	h = h[:len(x)]
	var s0, s1, s2, s3 float32
	i := 0
	for ; i+4 <= len(x); i += 4 {
		s0 += x[i] * h[i]
		s1 += x[i+1] * h[i+1]
		s2 += x[i+2] * h[i+2]
		s3 += x[i+3] * h[i+3]
	}
	for ; i < len(x); i++ {
		s0 += x[i] * h[i]
	}
	return (s0 + s1) + (s2 + s3)
}

// interleave appends the staged block (channel-major, n frames) to dst
// interleaved.
func (t *TruePeak) interleave(dst []float32, n int) []float32 {
	// slices.Grow, not append(dst, make(…)...): with -race (or -N) the
	// compiler does not elide that make, a new slice every block.
	base := len(dst)
	dst = slices.Grow(dst, n*t.ch)[:base+n*t.ch]
	for c := range t.ch {
		for k := range n {
			dst[base+k*t.ch+c] = t.stage[c*n+k]
		}
	}
	t.stage = t.stage[:0]
	return dst
}

// Clone returns an independent copy of the detector's state.
func (t *TruePeak) Clone() *TruePeak {
	c := *t
	c.stage = nil
	c.buf = make([][]float32, len(t.buf))
	for i, b := range t.buf {
		c.buf[i] = append(make([]float32, 0, len(b)+t.half), b...)
	}
	return &c
}
