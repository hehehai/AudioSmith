package analysis

import (
	"math"
	"slices"

	"github.com/hehehai/AudioSmith/internal/dsp"
)

// Onset is a point to snap a timeline to.
type Onset struct {
	Time     float64 `json:"time"`
	Strength float64 `json:"strength"` // 0–1, relative to the strongest
	Kind     string  `json:"kind"`     // "speech" (a speech segment starts) or "flux" (a new sound inside speech)
}

// OnsetOptions tune Onsets; zero values take the defaults.
type OnsetOptions struct {
	Hop        float64 // s; default 0.005
	MinGap     float64 // minimum distance between onsets, s; default 0.05
	Delta      float64 // a flux peak must exceed its local mean by Delta × the 95th percentile; default 0.2
	GateDB     float64 // no onsets where the signal is under this (dBFS RMS); default −50
	VAD        VADOptions
	FluxOnly   bool // skip speech-segment onsets
	SpeechOnly bool // skip flux onsets
}

func (o OnsetOptions) norm() OnsetOptions {
	if o.Hop <= 0 {
		o.Hop = 0.005
	}
	if o.MinGap <= 0 {
		o.MinGap = 0.05
	}
	if o.Delta <= 0 {
		o.Delta = 0.2
	}
	if o.GateDB == 0 {
		o.GateDB = -50
	}
	return o
}

// Flux returns the spectral-flux onset detection function: the half-wave
// rectified increase of log-compressed magnitude spectra (Hann windows of
// ~25 ms, rounded up to a power of two) every hop seconds. Value i is the
// change into the window ending at i·hop; its time is the window centre,
// where the flux of a sudden sound peaks.
func Flux(b dsp.Buffer, hop float64) Contour {
	m := mono(b)
	n := nextPow2(max(dsp.FramesAt(0.025, m.Rate), 64))
	h := max(dsp.FramesAt(hop, m.Rate), 1)
	frames := 0
	if m.Frames() > 0 {
		frames = (m.Frames()+h-1)/h + 1
	}
	f := newFFT(n)
	w := hann(n)
	buf := make([]complex128, n)
	prev := make([]float64, n/2+1)
	cur := make([]float64, n/2+1)
	out := make([]float64, frames)
	for i := range frames {
		end := i * h
		for k := range n {
			j := end - n + k
			v := 0.0
			if j >= 0 && j < m.Frames() {
				v = float64(m.Samples[j])
			}
			buf[k] = complex(v*w[k], 0)
		}
		f.transform(buf)
		var s float64
		for k := range cur {
			cur[k] = math.Log1p(100 * math.Hypot(real(buf[k]), imag(buf[k])))
			if i > 0 {
				s += max(cur[k]-prev[k], 0)
			}
		}
		out[i] = s
		prev, cur = cur, prev
	}
	return Contour{Start: -float64(n) / 2 / float64(m.Rate), Hop: float64(h) / float64(m.Rate), Values: out}
}

// Onsets returns speech-segment starts (from VAD) and spectral-flux peaks
// inside speech, sorted by time, at least MinGap apart (a speech onset wins
// over a nearby flux onset).
func Onsets(b dsp.Buffer, o OnsetOptions) []Onset {
	o = o.norm()
	var out []Onset
	if !o.FluxOnly {
		for _, s := range VAD(b, o.VAD) {
			out = append(out, Onset{Time: speechStart(b, s.Start), Strength: 1, Kind: "speech"})
		}
	}
	if !o.SpeechOnly {
		fl := Flux(b, o.Hop)
		gate := gateMask(b, fl, o.GateDB)
		v := fl.Values
		scale := Percentile(v, 0.95)
		top := slices.Max(append([]float64{0}, v...))
		if scale > 0 {
			pre := max(int(math.Round(o.MinGap/fl.Hop)), 1)
			avg := max(int(math.Round(0.1/fl.Hop)), 1)
			last := math.Inf(-1)
			for i := range v {
				if !gate[i] {
					continue
				}
				lo, hi := max(i-pre, 0), min(i+pre+1, len(v))
				if v[i] < slices.Max(v[lo:hi]) || v[i] <= 0 {
					continue
				}
				alo, ahi := max(i-avg, 0), min(i+avg+1, len(v))
				var mean float64
				for _, x := range v[alo:ahi] {
					mean += x
				}
				mean /= float64(ahi - alo)
				t := max(fl.Time(i), 0)
				if v[i] < mean+o.Delta*scale || t-last < o.MinGap {
					continue
				}
				out = append(out, Onset{Time: t, Strength: v[i] / top, Kind: "flux"})
				last = t
			}
		}
	}
	slices.SortStableFunc(out, func(a, b Onset) int {
		switch {
		case a.Time < b.Time:
			return -1
		case a.Time > b.Time:
			return 1
		case a.Kind == "speech" && b.Kind != "speech":
			return -1
		}
		return 0
	})
	// Enforce MinGap, preferring speech onsets.
	var kept []Onset
	for _, on := range out {
		if n := len(kept); n > 0 && on.Time-kept[n-1].Time < o.MinGap {
			if on.Kind == "speech" && kept[n-1].Kind != "speech" {
				kept[n-1] = on
			}
			continue
		}
		kept = append(kept, on)
	}
	return kept
}

// gateMask marks flux frames whose surrounding 20 ms is above gateDB.
func gateMask(b dsp.Buffer, fl Contour, gateDB float64) []bool {
	m := mono(b)
	win := max(dsp.FramesAt(0.02, m.Rate), 1)
	thr := math.Pow(10, gateDB/10)
	pre := make([]float64, m.Frames()+1)
	for i, v := range m.Samples {
		pre[i+1] = pre[i] + float64(v)*float64(v)
	}
	out := make([]bool, len(fl.Values))
	for i := range out {
		c := dsp.FramesAt(fl.Time(i), m.Rate)
		lo, hi := max(c, 0), min(c+win, m.Frames())
		if hi > lo {
			out[i] = (pre[hi]-pre[lo])/float64(hi-lo) >= thr
		}
	}
	return out
}

// speechStart refines a VAD start: the first 5 ms block within ±60 ms whose
// level reaches 20 dB under the segment's next 100 ms peak block.
func speechStart(b dsp.Buffer, t float64) float64 {
	m := mono(b)
	blk := max(dsp.FramesAt(0.005, m.Rate), 1)
	lo := max(dsp.FramesAt(t-0.06, m.Rate), 0)
	hi := min(dsp.FramesAt(t+0.1, m.Rate), m.Frames())
	if hi-lo < 2*blk {
		return t
	}
	db := FrameDB(m.Samples[lo:hi], blk, blk)
	peak := slices.Max(db)
	for i, v := range db {
		if v >= peak-20 {
			return float64(lo+i*blk) / float64(m.Rate)
		}
	}
	return t
}
