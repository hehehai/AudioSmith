// Package dynamics has a feed-forward compressor (RMS or peak detector,
// soft knee, attack/release, makeup gain) and a look-ahead brick-wall
// limiter that can hold a true-peak ceiling. Both stream over interleaved
// float32 chunks, are channel-linked (one gain for all channels), and keep
// their state between calls.
package dynamics

import (
	"math"

	"github.com/hehehai/AudioSmith/internal/dsp/filter"
)

// coef returns the one-pole coefficient reaching 1−1/e of a step in ms.
func coef(rate int, ms float64) float64 {
	if ms <= 0 {
		return 0
	}
	return math.Exp(-1 / (ms / 1000 * float64(rate)))
}

func db(x float64) float64 {
	if x <= 1e-20 {
		return -400
	}
	return 10 * math.Log10(x) // x is a power
}

// CompressorParams configure a Compressor. Zero values pick defaults where noted.
type CompressorParams struct {
	ThresholdDB float64 // level above which gain is reduced (dBFS, RMS of a sine reads its peak level)
	Ratio       float64 // ≥ 1; e.g. 3 for 3:1 (default 1, no compression)
	KneeDB      float64 // soft-knee width in dB (0 = hard knee)
	AttackMs    float64 // gain-reduction attack time constant
	ReleaseMs   float64 // gain-reduction release time constant
	RMSMs       float64 // RMS detector window (time constant); 0 = peak detector
	MakeupDB    float64 // gain added after compression
}

// Compressor reduces dynamic range.
type Compressor struct {
	p           CompressorParams
	ch          int
	rmsC, attC  float64
	relC        float64
	ms          float64 // detector mean square
	gr          float64 // current gain reduction, dB (≤ 0)
	makeup      float32
	reductionDB float64 // last block's maximum reduction, for meters
}

// NewCompressor returns a compressor for channels channels at rate.
func NewCompressor(rate, channels int, p CompressorParams) *Compressor {
	if p.Ratio < 1 {
		p.Ratio = 1
	}
	return &Compressor{
		p: p, ch: channels,
		rmsC: coef(rate, p.RMSMs), attC: coef(rate, p.AttackMs), relC: coef(rate, p.ReleaseMs),
		makeup: float32(math.Pow(10, p.MakeupDB/20)),
	}
}

// curve returns the static gain change (dB, ≤ 0) for a detector level in dB.
func (c *Compressor) curve(level float64) float64 {
	t, r, k := c.p.ThresholdDB, c.p.Ratio, c.p.KneeDB
	over := level - t
	switch {
	case k > 0 && 2*math.Abs(over) <= k:
		x := over + k/2
		return (1/r - 1) * x * x / (2 * k)
	case over > 0:
		return (1/r - 1) * over
	}
	return 0
}

// StaticGainDB returns the steady-state gain (with makeup) for a constant
// input level in dB, for tests and UI curves.
func (c *Compressor) StaticGainDB(levelDB float64) float64 {
	return c.curve(levelDB) + c.p.MakeupDB
}

// MaxReductionDB returns the deepest gain reduction (dB, ≤ 0) of the last Process call.
func (c *Compressor) MaxReductionDB() float64 { return c.reductionDB }

// Process compresses interleaved samples in place.
func (c *Compressor) Process(samples []float32) {
	c.reductionDB = 0
	if c.p.Ratio == 1 {
		if c.makeup != 1 {
			for i := range samples {
				samples[i] *= c.makeup
			}
		}
		return
	}
	for i := 0; i+c.ch <= len(samples); i += c.ch {
		fr := samples[i : i+c.ch]
		// Linked detector: the loudest channel's power.
		var p float64
		for _, v := range fr {
			p = max(p, float64(v)*float64(v))
		}
		var level float64
		if c.p.RMSMs > 0 {
			c.ms = c.rmsC*c.ms + (1-c.rmsC)*p
			// ×2 so a sine's RMS reads at its peak level, like a peak meter.
			level = db(2 * c.ms)
		} else {
			level = db(p)
		}
		target := c.curve(level)
		if target < c.gr {
			c.gr = c.attC*c.gr + (1-c.attC)*target
		} else {
			c.gr = c.relC*c.gr + (1-c.relC)*target
		}
		c.reductionDB = min(c.reductionDB, c.gr)
		g := float32(math.Pow(10, c.gr/20)) * c.makeup
		for j := range fr {
			fr[j] *= g
		}
	}
}

// LimiterParams configure a Limiter.
type LimiterParams struct {
	CeilingDB   float64 // output ceiling (dBFS, or dBTP with TruePeak)
	LookaheadMs float64 // default 5 ms
	ReleaseMs   float64 // default 80 ms
	TruePeak    bool    // detect inter-sample peaks (oversampled) instead of sample peaks
}

// Limiter is a look-ahead brick-wall limiter. For each frame j it needs
// gain r[j] = min(1, ceiling/peak[j]); it takes h[j] = min(r[j−d..j]) over
// the look-ahead d, smooths h with an instant-attack one-pole release (never
// above h), and applies to frame k the mean of the smoothed values over
// [k, k+d]. Every value in that mean is ≤ r[k], so the output never passes
// the ceiling, and the gain ramps over d frames instead of jumping.
// Audio is delayed by Latency() frames; Flush returns the tail.
type Limiter struct {
	ch   int
	ceil float64
	d    int     // look-ahead frames
	relC float64 // release coefficient
	tp   *filter.TruePeak

	fifo   []float32 // delayed input frames, from head on
	head   int
	minq   []int64 // monotonic deque for the sliding minimum
	minv   []float64
	n      int64 // r values pushed
	hr     float64
	box    []float64 // last d+1 smoothed values
	bsum   float64
	bw     int
	tpbuf  []float32
	maxRed float64
}

// NewLimiter returns a limiter for channels channels at rate.
func NewLimiter(rate, channels int, p LimiterParams) *Limiter {
	if p.LookaheadMs <= 0 {
		p.LookaheadMs = 5
	}
	if p.ReleaseMs <= 0 {
		p.ReleaseMs = 80
	}
	l := &Limiter{ch: channels, ceil: math.Pow(10, p.CeilingDB/20), relC: coef(rate, p.ReleaseMs)}
	l.d = max(int(math.Round(p.LookaheadMs/1000*float64(rate))), 1)
	if p.TruePeak {
		l.tp = filter.NewTruePeak(rate, channels)
		// Peaks under the ceiling need no gain: skip oversampling them.
		th := make([]float32, channels)
		for i := range th {
			th[i] = float32(l.ceil)
		}
		l.tp.SetThreshold(th)
	}
	l.Reset()
	return l
}

// Latency returns the delay in frames between input and output.
func (l *Limiter) Latency() int {
	if l.tp != nil {
		return l.d + l.tp.Delay()
	}
	return l.d
}

// Reset clears all state.
func (l *Limiter) Reset() {
	l.fifo, l.head = l.fifo[:0], 0
	l.minq, l.minv = l.minq[:0], l.minv[:0]
	l.n, l.hr, l.bw = 0, 1, 0
	l.box = make([]float64, l.d+1)
	for i := range l.box {
		l.box[i] = 1
	}
	l.bsum = float64(l.d + 1)
	if l.tp != nil {
		l.tp.Reset()
	}
}

// MaxReductionDB returns the deepest gain reduction (dB, ≤ 0) since the
// last call, and resets it.
func (l *Limiter) MaxReductionDB() float64 {
	r := 0.0
	if l.maxRed > 0 {
		r = 20 * math.Log10(max(1-l.maxRed, 1e-10))
	}
	l.maxRed = 0
	return r
}

// Process appends the limited output that in makes available to dst and
// returns it. Output lags input by Latency() frames until Flush.
func (l *Limiter) Process(dst, in []float32) []float32 {
	n := len(in) / l.ch
	l.fifo = append(l.fifo, in[:n*l.ch]...)
	var peaks []float32
	if l.tp != nil {
		l.tpbuf = l.tp.Process(l.tpbuf[:0], in[:n*l.ch])
		peaks = l.tpbuf
	} else {
		peaks = in[:n*l.ch]
	}
	return l.push(dst, peaks)
}

// push feeds per-frame peaks (interleaved, abs taken here) in frame order.
func (l *Limiter) push(dst []float32, peaks []float32) []float32 {
	for i := 0; i+l.ch <= len(peaks); i += l.ch {
		var peak float64
		for _, v := range peaks[i : i+l.ch] {
			peak = max(peak, math.Abs(float64(v)))
		}
		dst = l.step(dst, peak)
	}
	// Compact the FIFO now and then.
	if l.head > 1<<15 {
		k := copy(l.fifo, l.fifo[l.head:])
		l.fifo, l.head = l.fifo[:k], 0
	}
	return dst
}

func (l *Limiter) step(dst []float32, peak float64) []float32 {
	r := 1.0
	if peak > l.ceil {
		r = l.ceil / peak
	}
	j := l.n
	l.n++
	for len(l.minv) > 0 && l.minv[len(l.minv)-1] >= r {
		l.minv, l.minq = l.minv[:len(l.minv)-1], l.minq[:len(l.minq)-1]
	}
	l.minv, l.minq = append(l.minv, r), append(l.minq, j)
	for l.minq[0] < j-int64(l.d) {
		l.minv, l.minq = l.minv[1:], l.minq[1:]
	}
	h := l.minv[0]
	if h < l.hr {
		l.hr = h
	} else {
		l.hr = l.relC*l.hr + (1-l.relC)*h
		l.hr = min(l.hr, h)
	}
	l.bsum += l.hr - l.box[l.bw]
	l.box[l.bw] = l.hr
	l.bw = (l.bw + 1) % len(l.box)
	if j < int64(l.d) {
		return dst
	}
	g := min(l.bsum/float64(len(l.box)), 1)
	l.maxRed = max(l.maxRed, 1-g)
	gf := float32(g)
	fr := l.fifo[l.head : l.head+l.ch]
	l.head += l.ch
	for _, v := range fr {
		v *= gf
		if l.tp == nil {
			// Rounding of the averaged gain must not poke above the ceiling.
			c := float32(l.ceil)
			v = min(max(v, -c), c)
		}
		dst = append(dst, v)
	}
	return dst
}

// Flush appends the remaining Latency() frames of output and resets.
func (l *Limiter) Flush(dst []float32) []float32 {
	if l.tp != nil {
		dst = l.push(dst, l.tp.Flush(l.tpbuf[:0]))
	}
	for range l.d {
		dst = l.step(dst, 0)
	}
	l.Reset()
	return dst
}

// Buffer limits whole interleaved samples, returning output of the same length.
func (l *Limiter) Buffer(samples []float32) []float32 {
	out := make([]float32, 0, len(samples))
	out = l.Process(out, samples)
	return l.Flush(out)
}
