// Package analysis measures speech for dubbing and prosody: an energy VAD
// (the fallback; the ML VAD lives in the engine), speech onsets for
// timeline snapping (spectral flux + segment starts), a YIN pitch contour
// with summary statistics, a loudness contour, a speaking-rate estimate
// from syllable nuclei, and a per-line prosody summary compared against a
// speaker baseline ("Prosody Mirror").
//
// Inputs of any channel count are mixed to mono first. Times are seconds.
package analysis

import (
	"math"
	"slices"

	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/filter"
	"github.com/hehehai/AudioSmith/internal/dsp/resample"
)

// mono returns b as one channel.
func mono(b dsp.Buffer) dsp.Buffer { return dsp.Remix(b, 1) }

// at returns b mono at rate (resampling only when needed).
func at(b dsp.Buffer, rate int) dsp.Buffer {
	return resample.Buffer(mono(b), rate, resample.Fast)
}

// Contour is a series sampled every Hop seconds; value i is centred at
// Start + i·Hop.
type Contour struct {
	Start  float64   `json:"start"`
	Hop    float64   `json:"hop"`
	Values []float64 `json:"values"`
}

// Time returns the centre time of value i.
func (c Contour) Time(i int) float64 { return c.Start + float64(i)*c.Hop }

// FrameDB returns the RMS level (dBFS, floor −120) of windows of win
// frames every hop frames; window i is centred at i·hop + win/2.
func FrameDB(x []float32, win, hop int) []float64 {
	if len(x) < win {
		win = len(x)
	}
	if win == 0 {
		return nil
	}
	n := (len(x)-win)/hop + 1
	out := make([]float64, n)
	pre := make([]float64, len(x)+1)
	for i, v := range x {
		pre[i+1] = pre[i] + float64(v)*float64(v)
	}
	for i := range out {
		e := (pre[i*hop+win] - pre[i*hop]) / float64(win)
		out[i] = max(10*math.Log10(max(e, 1e-12)), -120)
	}
	return out
}

// Percentile is v's p-quantile (0–1, nearest rank); NaN for none.
func Percentile(v []float64, p float64) float64 {
	if len(v) == 0 {
		return math.NaN()
	}
	s := slices.Clone(v)
	slices.Sort(s)
	return s[int(math.Round(p*float64(len(s)-1)))]
}

func median(v []float64) float64 { return Percentile(v, 0.5) }

// VADOptions tune the energy VAD; zero values take the defaults.
type VADOptions struct {
	Frame     float64 // analysis window, s; default 0.02
	Hop       float64 // s; default 0.01
	MarginDB  float64 // speech is this far above the noise floor; default 12 dB
	MinDB     float64 // and at least this loud (dBFS RMS); default −55
	Hangover  float64 // speech continues this long after energy drops, s; default 0.15
	MinGap    float64 // shorter silences are bridged, s; default 0.25
	MinSpeech float64 // shorter bursts are dropped, s; default 0.08
}

func (o VADOptions) norm() VADOptions {
	def := func(v *float64, d float64) {
		if *v == 0 {
			*v = d
		}
	}
	def(&o.Frame, 0.02)
	def(&o.Hop, 0.01)
	def(&o.MarginDB, 12)
	def(&o.MinDB, -55)
	def(&o.Hangover, 0.15)
	def(&o.MinGap, 0.25)
	def(&o.MinSpeech, 0.08)
	return o
}

// VAD returns the speech segments of b by frame energy: frames more than
// MarginDB above the noise floor (the 10th percentile frame level) and above
// MinDB are speech; segments get a hangover, gaps under MinGap are bridged
// and bursts under MinSpeech dropped.
func VAD(b dsp.Buffer, o VADOptions) []dsp.Range {
	o = o.norm()
	m := mono(b)
	win := max(dsp.FramesAt(o.Frame, m.Rate), 1)
	hop := max(dsp.FramesAt(o.Hop, m.Rate), 1)
	db := FrameDB(m.Samples, win, hop)
	if len(db) == 0 {
		return nil
	}
	floor := Percentile(db, 0.1)
	thr := max(floor+o.MarginDB, o.MinDB)
	var segs []dsp.Range
	in := false
	var start float64
	for i, v := range db {
		t := float64(i*hop) / float64(m.Rate)
		switch {
		case v >= thr && !in:
			in, start = true, t
		case v < thr && in:
			in = false
			segs = append(segs, dsp.Range{Start: start, End: t + float64(win)/float64(m.Rate)})
		}
	}
	if in {
		segs = append(segs, dsp.Range{Start: start, End: m.Seconds()})
	}
	// Hangover, bridging, minimum length.
	var out []dsp.Range
	for _, s := range segs {
		s.End = min(s.End+o.Hangover, m.Seconds())
		if n := len(out); n > 0 && s.Start-out[n-1].End < o.MinGap {
			out[n-1].End = max(out[n-1].End, s.End)
			continue
		}
		out = append(out, s)
	}
	out = slices.DeleteFunc(out, func(r dsp.Range) bool { return r.Len() < o.MinSpeech })
	return out
}

// SpeechSeconds returns the total length of segs.
func SpeechSeconds(segs []dsp.Range) float64 {
	var s float64
	for _, r := range segs {
		s += r.Len()
	}
	return s
}

// LoudnessContour returns the K-weighted loudness (LUFS, BS.1770 weighting
// without gating, floor −120) of windows of win seconds every hop seconds
// (defaults 0.4 s and 0.1 s: the momentary loudness).
func LoudnessContour(b dsp.Buffer, win, hop float64) Contour {
	if win <= 0 {
		win = 0.4
	}
	if hop <= 0 {
		hop = 0.1
	}
	m := mono(b)
	k := kweight(m)
	w := max(dsp.FramesAt(win, m.Rate), 1)
	h := max(dsp.FramesAt(hop, m.Rate), 1)
	db := FrameDB(k, w, h)
	for i := range db {
		if db[i] > -120 {
			db[i] -= 0.691
		}
	}
	return Contour{Start: float64(w) / 2 / float64(m.Rate), Hop: float64(h) / float64(m.Rate), Values: db}
}

// kweight returns a K-weighted copy of mono m.
func kweight(m dsp.Buffer) []float32 {
	s, h := filter.KWeighting(m.Rate)
	out := slices.Clone(m.Samples)
	filter.NewChain(1, s, h).Process(out)
	return out
}
