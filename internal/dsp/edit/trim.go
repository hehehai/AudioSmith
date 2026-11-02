package edit

import (
	"math"

	"github.com/hehehai/AudioSmith/internal/dsp"
)

// TrimOptions configure silence detection for trimming.
type TrimOptions struct {
	ThresholdDB float64 // window RMS (dBFS, all channels) under which audio is silence; default −50
	Window      float64 // detection window, seconds; default 0.010
	Keep        float64 // silence kept next to the sound, seconds; default 0.030
	Fade        float64 // fade at each cut, seconds; default 0.005 (never longer than Keep)
}

func (o TrimOptions) norm() TrimOptions {
	if o.ThresholdDB == 0 {
		o.ThresholdDB = -50
	}
	if o.Window <= 0 {
		o.Window = 0.010
	}
	if o.Keep < 0 {
		o.Keep = 0
	} else if o.Keep == 0 {
		o.Keep = 0.030
	}
	if o.Fade < 0 {
		o.Fade = 0
	} else if o.Fade == 0 {
		o.Fade = 0.005
	}
	o.Fade = min(o.Fade, o.Keep)
	return o
}

// activity returns, per window of w frames (the last may be shorter),
// whether its RMS is at or above thr (a linear mean square).
func activity(b dsp.Buffer, w int, thr float64) []bool {
	n := b.Frames()
	out := make([]bool, (n+w-1)/w)
	for k := range out {
		lo, hi := k*w, min((k+1)*w, n)
		var e float64
		for _, v := range b.Samples[lo*b.Channels : hi*b.Channels] {
			e += float64(v) * float64(v)
		}
		out[k] = e/float64((hi-lo)*b.Channels) >= thr
	}
	return out
}

// TrimRange returns the frames [start, end) of b to keep: from Keep before
// the first window above the threshold to Keep after the last. All-silent
// audio gives start == end.
func TrimRange(b dsp.Buffer, o TrimOptions) (start, end int) {
	sc := NewTrimScanner(b.Rate, b.Channels, o)
	sc.Write(b.Samples)
	return sc.Range()
}

// Trim returns a copy of b without its leading and trailing silence,
// faded at the cuts that removed audio.
func Trim(b dsp.Buffer, o TrimOptions) dsp.Buffer {
	o = o.norm()
	s, e := TrimRange(b, o)
	out := b.Slice(s, e).Clone()
	f := dsp.FramesAt(o.Fade, b.Rate)
	if s > 0 {
		dsp.FadeIn(out, f, dsp.EqualPower)
	}
	if e < b.Frames() {
		dsp.FadeOut(out, f, dsp.EqualPower)
	}
	return out
}

// Pause is a stretch of internal silence, in frames.
type Pause = Span

// Pauses returns the silent stretches between sounds that last at least
// minPause seconds (leading and trailing silence excluded).
func Pauses(b dsp.Buffer, minPause float64, o TrimOptions) []Pause {
	o = o.norm()
	w := max(dsp.FramesAt(o.Window, b.Rate), 1)
	act := activity(b, w, math.Pow(10, o.ThresholdDB/10))
	minW := int(math.Ceil(minPause * float64(b.Rate) / float64(w)))
	var out []Pause
	seen := false
	run := -1
	for k, a := range act {
		switch {
		case a:
			if seen && run >= 0 && k-run >= minW {
				out = append(out, Pause{run * w, k * w})
			}
			seen, run = true, -1
		case run < 0:
			run = k
		}
	}
	return out
}

// LimitPauses shortens every internal pause longer than maxPause seconds
// to maxPause, cutting from its middle with a short crossfade, and returns
// the new audio and the removed spans (in input frames). Use it to tighten
// TTS output that pauses too long, or to fit a dubbing slot.
func LimitPauses(b dsp.Buffer, maxPause float64, o TrimOptions) (dsp.Buffer, []Span) {
	o = o.norm()
	maxF := dsp.FramesAt(maxPause, b.Rate)
	cf := min(o.Fade, maxPause/4)
	c := dsp.FramesAt(cf, b.Rate)
	var cuts []Span
	for _, p := range Pauses(b, maxPause, o) {
		// Keep maxF+c frames of the pause; the crossfade overlaps c of them.
		keep := maxF + c
		if p.End-p.Start <= keep {
			continue
		}
		s := p.Start + keep/2
		cuts = append(cuts, Span{s, s + (p.End - p.Start - keep)})
	}
	if len(cuts) == 0 {
		return b.Clone(), nil
	}
	var segs [][]float32
	prev := 0
	for _, c := range cuts {
		segs = append(segs, b.Slice(prev, c.Start).Samples)
		prev = c.End
	}
	segs = append(segs, b.Slice(prev, b.Frames()).Samples)
	joins := make([]Join, len(segs))
	for i := range joins {
		joins[i] = Join{Crossfade: cf}
	}
	out, _, _ := Concat(b.Rate, b.Channels, segs, joins)
	return out, cuts
}
