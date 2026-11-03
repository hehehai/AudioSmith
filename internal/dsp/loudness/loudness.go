// Package loudness measures loudness per ITU-R BS.1770-4 and EBU R128
// (EBU Tech 3341 meter, Tech 3342 loudness range) and normalises audio to
// delivery targets (ACX, podcast, broadcast) under a true-peak ceiling.
//
// Measurement: K-weighting (filter.KWeighting, any sample rate), channel
// weights 1 for L/R/C and mono, 1.41 for surrounds, 0 for LFE; mean square
// accumulated per 100 ms sub-block. Momentary = last 400 ms, short-term =
// last 3 s (both over whole sub-blocks; shorter programmes use what there
// is). Integrated = 400 ms blocks every 100 ms, absolute gate −70 LUFS,
// relative gate −10 LU. Loudness range = 10th to 95th percentile of the 3 s
// short-term values every 100 ms after a −70 LUFS absolute and −20 LU
// relative gate. True peak = filter.TruePeak (4× at 48 kHz, up to 8× at
// lower rates). A trailing partial 100 ms sub-block is not counted.
//
// Mono is counted once (BS.1770): a mono file reads 3 LU quieter than the
// same signal on both stereo channels. Options.DualMono counts it twice,
// as some podcast tools do.
package loudness

import (
	"math"
	"slices"

	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/filter"
)

const (
	// AbsoluteGate is the BS.1770 absolute gate in LUFS.
	AbsoluteGate = -70.0
	// Floor is what Stats reports for silence (JSON has no −Inf).
	Floor = -120.0
)

// Options tune a Meter.
type Options struct {
	SkipTruePeak bool      // do not oversample (true peak then equals sample peak)
	DualMono     bool      // mono counts as two channels (+3.01 LU)
	Weights      []float64 // per-channel weights, overriding the BS.1770 defaults
}

// Weights returns the BS.1770 channel weights for a channel count: 5 is
// L R C Ls Rs, 6 is L R C LFE Ls Rs; other layouts weigh every channel 1.
func Weights(channels int) []float64 {
	w := make([]float64, channels)
	for i := range w {
		w[i] = 1
	}
	switch channels {
	case 5:
		w[3], w[4] = 1.41, 1.41
	case 6:
		w[3], w[4], w[5] = 0, 1.41, 1.41
	}
	return w
}

type sub struct {
	sum float64 // Σ over frames of Σ_c w_c·y_c²
	n   int
}

// Meter measures a stream. Write chunks, read values at any time.
// Not safe for concurrent use.
type Meter struct {
	rate, ch int
	w        []float64
	kw       *filter.Chain
	tp       *filter.TruePeak
	subs     []sub
	cur      sub
	frames   int64 // frames written
	nextEdge int64 // frame index ending the current sub-block
	sp, tpk  []float64
	scratch  []float64
	tpbuf    []float32
	th       []float32
}

// NewMeter returns a meter for channels channels at rate.
func NewMeter(rate, channels int, opt Options) *Meter {
	m := &Meter{rate: rate, ch: channels, w: Weights(channels), sp: make([]float64, channels), tpk: make([]float64, channels), th: make([]float32, channels)}
	if len(opt.Weights) == channels {
		m.w = slices.Clone(opt.Weights)
	}
	if opt.DualMono && channels == 1 {
		m.w[0] = 2
	}
	s, h := filter.KWeighting(rate)
	m.kw = filter.NewChain(channels, s, h)
	if !opt.SkipTruePeak {
		m.tp = filter.NewTruePeak(rate, channels)
	}
	m.nextEdge = m.edge(1)
	return m
}

// edge returns the frame index where sub-block k−1 ends (k·rate/10, rounded).
func (m *Meter) edge(k int64) int64 { return (k*int64(m.rate) + 5) / 10 }

// Write measures interleaved samples (whole frames).
func (m *Meter) Write(samples []float32) {
	n := len(samples) / m.ch
	samples = samples[:n*m.ch]
	if cap(m.scratch) < len(samples) {
		m.scratch = make([]float64, len(samples))
	}
	y := m.scratch[:len(samples)]
	for i, v := range samples {
		y[i] = float64(v)
		a := math.Abs(y[i])
		if c := i % m.ch; a > m.sp[c] {
			m.sp[c] = a
		}
	}
	m.kw.Process64(y)
	for i := range n {
		var e float64
		for c, wc := range m.w {
			v := y[i*m.ch+c]
			e += wc * v * v
		}
		m.cur.sum += e
		m.cur.n++
		m.frames++
		if m.frames == m.nextEdge {
			m.subs = append(m.subs, m.cur)
			m.cur = sub{}
			m.nextEdge = m.edge(int64(len(m.subs)) + 1)
		}
	}
	if m.tp != nil {
		// Only frames that could raise a channel's maximum need oversampling.
		for c, v := range m.tpk {
			m.th[c] = float32(v)
		}
		m.tp.SetThreshold(m.th)
		m.tpbuf = m.tp.Process(m.tpbuf[:0], samples)
		m.maxTP(m.tpbuf)
	}
}

func (m *Meter) maxTP(p []float32) {
	for i, v := range p {
		if c := i % m.ch; float64(v) > m.tpk[c] {
			m.tpk[c] = float64(v)
		}
	}
}

// Reset forgets everything measured.
func (m *Meter) Reset() {
	m.kw.Reset()
	if m.tp != nil {
		m.tp.Reset()
	}
	m.subs, m.cur, m.frames = m.subs[:0], sub{}, 0
	m.nextEdge = m.edge(1)
	clear(m.sp)
	clear(m.tpk)
}

// Seconds returns the duration written.
func (m *Meter) Seconds() float64 { return float64(m.frames) / float64(m.rate) }

// lufs converts a weighted mean square to LUFS.
func lufs(z float64) float64 {
	if z <= 0 {
		return math.Inf(-1)
	}
	return -0.691 + 10*math.Log10(z)
}

// window returns the loudness of sub-blocks [i, i+k).
func (m *Meter) window(i, k int) float64 {
	var s float64
	var n int
	for _, b := range m.subs[i : i+k] {
		s += b.sum
		n += b.n
	}
	if n == 0 {
		return math.Inf(-1)
	}
	return lufs(s / float64(n))
}

func (m *Meter) last(k int) float64 {
	if len(m.subs) == 0 {
		return math.Inf(-1)
	}
	k = min(k, len(m.subs))
	return m.window(len(m.subs)-k, k)
}

// Momentary returns the loudness of the last 400 ms, in LUFS.
func (m *Meter) Momentary() float64 { return m.last(4) }

// ShortTerm returns the loudness of the last 3 s, in LUFS.
func (m *Meter) ShortTerm() float64 { return m.last(30) }

// series returns the loudness of every k-sub-block window, one per 100 ms;
// value i covers sub-blocks [i, i+k), ending at (i+k)/10 s.
func (m *Meter) series(k int) []float64 {
	if len(m.subs) < k {
		return nil
	}
	out := make([]float64, len(m.subs)-k+1)
	var s float64
	var n int
	for j := range k {
		s += m.subs[j].sum
		n += m.subs[j].n
	}
	for i := range out {
		if i > 0 {
			s += m.subs[i+k-1].sum - m.subs[i-1].sum
			n += m.subs[i+k-1].n - m.subs[i-1].n
		}
		out[i] = lufs(max(s, 0) / float64(n))
	}
	return out
}

// MomentarySeries returns the momentary loudness every 100 ms: value i is
// the 400 ms ending at (i+4)·0.1 s.
func (m *Meter) MomentarySeries() []float64 { return m.series(4) }

// ShortTermSeries returns the short-term loudness every 100 ms: value i is
// the 3 s ending at (i+30)·0.1 s.
func (m *Meter) ShortTermSeries() []float64 { return m.series(30) }

// Integrated returns the gated integrated loudness in LUFS (−Inf if no
// 400 ms block passes the absolute gate).
func (m *Meter) Integrated() float64 {
	blocks := m.series(4)
	return gated(blocks, -10)
}

// gated returns the power mean of blocks above the absolute gate and
// rel LU below the power mean of those.
func gated(blocks []float64, rel float64) float64 {
	pm := func(min float64) float64 {
		var s float64
		var n int
		for _, l := range blocks {
			if l > min {
				s += math.Pow(10, (l+0.691)/10)
				n++
			}
		}
		if n == 0 {
			return math.Inf(-1)
		}
		return lufs(s / float64(n))
	}
	abs := pm(AbsoluteGate)
	if math.IsInf(abs, -1) {
		return abs
	}
	return pm(math.Max(abs+rel, AbsoluteGate))
}

// Range returns the loudness range (LRA) in LU and its low and high
// percentiles in LUFS (0 and −Inf when under 3 s or silent).
func (m *Meter) Range() (lra, low, high float64) {
	st := m.series(30)
	abs := gated(st, -1e9) // power mean above the absolute gate only
	if math.IsInf(abs, -1) {
		return 0, math.Inf(-1), math.Inf(-1)
	}
	gate := math.Max(abs-20, AbsoluteGate)
	var v []float64
	for _, l := range st {
		if l > gate {
			v = append(v, l)
		}
	}
	slices.Sort(v)
	pct := func(p float64) float64 { return v[int(math.Round(float64(len(v)-1)*p))] }
	low, high = pct(0.10), pct(0.95)
	return high - low, low, high
}

// SamplePeak returns the largest absolute sample value in dBFS.
func (m *Meter) SamplePeak() float64 { return dsp.DB(slices.Max(m.sp)) }

// TruePeak returns the largest true-peak value in dBTP, including the
// frames still in the detector's look-ahead.
func (m *Meter) TruePeak() float64 {
	if m.tp == nil {
		return m.SamplePeak()
	}
	t := m.tp.Clone()
	tail := t.Flush(nil)
	top := slices.Max(m.tpk)
	for _, v := range tail {
		top = max(top, float64(v))
	}
	return dsp.DB(max(top, slices.Max(m.sp)))
}

// Stats are a programme's loudness figures. Silence reads Floor.
type Stats struct {
	Integrated   float64 `json:"integrated"`   // LUFS
	Range        float64 `json:"range"`        // LRA, LU
	RangeLow     float64 `json:"rangeLow"`     // LUFS (10th percentile)
	RangeHigh    float64 `json:"rangeHigh"`    // LUFS (95th percentile)
	MomentaryMax float64 `json:"momentaryMax"` // LUFS
	ShortTermMax float64 `json:"shortTermMax"` // LUFS
	SamplePeak   float64 `json:"samplePeak"`   // dBFS
	TruePeak     float64 `json:"truePeak"`     // dBTP
	Seconds      float64 `json:"seconds"`
}

func floor(v float64) float64 {
	if math.IsNaN(v) || v < Floor {
		return Floor
	}
	return v
}

func maxOf(v []float64) float64 {
	if len(v) == 0 {
		return math.Inf(-1)
	}
	return slices.Max(v)
}

// Stats returns all figures so far.
func (m *Meter) Stats() Stats {
	lra, lo, hi := m.Range()
	mom := m.series(4)
	if len(mom) == 0 {
		mom = []float64{m.Momentary()}
	}
	st := m.series(30)
	if len(st) == 0 {
		st = []float64{m.ShortTerm()}
	}
	return Stats{
		Integrated:   floor(m.Integrated()),
		Range:        lra,
		RangeLow:     floor(lo),
		RangeHigh:    floor(hi),
		MomentaryMax: floor(maxOf(mom)),
		ShortTermMax: floor(maxOf(st)),
		SamplePeak:   floor(m.SamplePeak()),
		TruePeak:     floor(m.TruePeak()),
		Seconds:      m.Seconds(),
	}
}

// Measure returns the Stats of a whole buffer (true peak included).
func Measure(b dsp.Buffer) Stats { return MeasureWith(b, Options{}) }

// MeasureWith is Measure with meter options.
func MeasureWith(b dsp.Buffer, o Options) Stats {
	m := NewMeter(b.Rate, b.Channels, o)
	writeChunked(m, b)
	return m.Stats()
}

// writeChunked feeds b to m a chunk at a time: the meter's scratch and
// oversampling buffers stay small instead of growing to the whole
// buffer (code review 11-02: measuring 10 min of stereo allocated 4 GB).
func writeChunked(m *Meter, b dsp.Buffer) {
	const chunk = 16384
	step := chunk * max(b.Channels, 1)
	for i := 0; i < len(b.Samples); i += step {
		m.Write(b.Samples[i:min(i+step, len(b.Samples))])
	}
}

// Integrated returns the integrated loudness of a buffer (no true peak work).
func Integrated(b dsp.Buffer) float64 { return IntegratedWith(b, Options{}) }

// IntegratedWith is Integrated with meter options (SkipTruePeak is implied).
func IntegratedWith(b dsp.Buffer, o Options) float64 {
	o.SkipTruePeak = true
	m := NewMeter(b.Rate, b.Channels, o)
	writeChunked(m, b)
	return m.Integrated()
}
