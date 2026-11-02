package edit

import (
	"math"

	"github.com/hehehai/AudioSmith/internal/dsp"
)

// TrimScanner finds TrimRange over audio fed in chunks, so a long file is
// never held whole.
type TrimScanner struct {
	o           TrimOptions
	rate, ch, w int
	thr         float64
	k           int     // index of the window being filled
	e           float64 // its energy so far
	n           int     // its frames so far
	frames      int
	first, last int
}

// NewTrimScanner starts a scan of audio at rate with channels channels.
func NewTrimScanner(rate, channels int, o TrimOptions) *TrimScanner {
	o = o.norm()
	return &TrimScanner{o: o, rate: rate, ch: channels, w: max(dsp.FramesAt(o.Window, rate), 1),
		thr: math.Pow(10, o.ThresholdDB/10), first: -1, last: -1}
}

// Write feeds interleaved samples (whole frames).
func (s *TrimScanner) Write(samples []float32) {
	for i := 0; i+s.ch <= len(samples); i += s.ch {
		for _, v := range samples[i : i+s.ch] {
			s.e += float64(v) * float64(v)
		}
		s.n++
		s.frames++
		if s.n == s.w {
			s.close()
		}
	}
}

// close ends the window being filled.
func (s *TrimScanner) close() {
	if s.n > 0 && s.e/float64(s.n*s.ch) >= s.thr {
		if s.first < 0 {
			s.first = s.k
		}
		s.last = s.k
	}
	s.k++
	s.e, s.n = 0, 0
}

// FadeFrames is the fade Trim applies at a cut, in frames.
func (s *TrimScanner) FadeFrames() int { return dsp.FramesAt(s.o.Fade, s.rate) }

// Range returns TrimRange of everything written. Call it once, at the end.
func (s *TrimScanner) Range() (start, end int) {
	s.close() // the last, shorter window
	if s.first < 0 {
		return 0, 0
	}
	keep := dsp.FramesAt(s.o.Keep, s.rate)
	return max(s.first*s.w-keep, 0), min((s.last+1)*s.w+keep, s.frames)
}
