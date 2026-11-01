// Package edit assembles takes and long renders: concatenation with
// crossfades or gaps (a streaming Joiner for renders too long for memory),
// silence for [pause] tags, edge-silence trimming, gap policy between
// sentences, lines, paragraphs and chapters, and shortening of long
// internal pauses.
package edit

import (
	"errors"
	"math"

	"github.com/hehehai/AudioSmith/internal/dsp"
)

// DefaultCrossfade is the crossfade between chunks of one text: 50 ms.
const DefaultCrossfade = 0.050

// Join says how a segment attaches to the audio before it. A Gap (silence)
// takes precedence over a Crossfade (overlap).
type Join struct {
	Gap       float64   `json:"gap,omitempty"`       // seconds of silence before the segment
	Crossfade float64   `json:"crossfade,omitempty"` // seconds of overlap with the previous audio
	Curve     dsp.Curve `json:"curve,omitempty"`     // crossfade shape (default equal power)
}

// Span is a segment's place in the output, in frames [Start, End).
type Span struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Range converts s to seconds at rate.
func (s Span) Range(rate int) dsp.Range {
	return dsp.Range{Start: dsp.SecondsAt(s.Start, rate), End: dsp.SecondsAt(s.End, rate)}
}

// Joiner appends segments into a stream. It holds back up to the maximum
// crossfade of output, so memory stays constant however long the render.
// Not safe for concurrent use.
type Joiner struct {
	rate, ch int
	hold     int // frames held back
	tail     []float32
	emitted  int // frames passed to sink
	sink     func([]float32) error
	err      error
	closed   bool
}

// ErrFormat reports a segment that is not whole frames.
var ErrFormat = errors.New("edit: segment is not a whole number of frames")

// NewJoiner returns a joiner of rate/channels audio passing finished
// samples to sink (which must not keep the slice). maxCrossfade (seconds)
// bounds every Join's crossfade.
func NewJoiner(rate, channels int, maxCrossfade float64, sink func([]float32) error) *Joiner {
	return &Joiner{rate: rate, ch: channels, hold: max(dsp.FramesAt(maxCrossfade, rate), 0), sink: sink}
}

// Frames returns the output length so far.
func (j *Joiner) Frames() int { return j.emitted + len(j.tail)/j.ch }

// Append adds seg (interleaved) after join and returns where it landed.
// With a crossfade the segment starts inside the previous audio.
func (j *Joiner) Append(seg []float32, join Join) (Span, error) {
	if j.err != nil {
		return Span{}, j.err
	}
	if j.closed {
		return Span{}, errors.New("edit: append after close")
	}
	if len(seg)%j.ch != 0 {
		return Span{}, ErrFormat
	}
	if join.Gap > 0 {
		j.tail = append(j.tail, make([]float32, dsp.FramesAt(join.Gap, j.rate)*j.ch)...)
	}
	n := len(seg) / j.ch
	c := 0
	if join.Gap <= 0 && join.Crossfade > 0 {
		c = min(dsp.FramesAt(join.Crossfade, j.rate), j.hold, len(j.tail)/j.ch, n)
	}
	start := j.Frames() - c
	if c > 0 {
		t := j.tail[len(j.tail)-c*j.ch:]
		for i := range c {
			x := (float64(i) + 0.5) / float64(c)
			gin := float32(join.Curve.FadeGain(x))
			gout := float32(join.Curve.FadeGain(1 - x))
			for k := range j.ch {
				t[i*j.ch+k] = t[i*j.ch+k]*gout + seg[i*j.ch+k]*gin
			}
		}
	}
	j.tail = append(j.tail, seg[c*j.ch:]...)
	span := Span{start, start + n}
	return span, j.drain(j.hold)
}

// Silence appends seconds of silence (a [pause]).
func (j *Joiner) Silence(seconds float64) (Span, error) {
	start := j.Frames()
	_, err := j.Append(nil, Join{Gap: seconds})
	return Span{start, j.Frames()}, err
}

// drain emits all but keep frames of the tail.
func (j *Joiner) drain(keep int) error {
	if n := len(j.tail)/j.ch - keep; n > 0 {
		if err := j.sink(j.tail[:n*j.ch]); err != nil {
			j.err = err
			return err
		}
		j.emitted += n
		j.tail = append(j.tail[:0], j.tail[n*j.ch:]...)
	}
	return nil
}

// Close emits the held-back tail.
func (j *Joiner) Close() error {
	if j.closed {
		return j.err
	}
	j.closed = true
	if j.err != nil {
		return j.err
	}
	return j.drain(0)
}

// Concat joins segments (all of rate/channels); joins[i] applies before
// segs[i] (joins may be nil or shorter: missing joins are plain butt
// joins). It returns the result and each segment's span.
func Concat(rate, channels int, segs [][]float32, joins []Join) (dsp.Buffer, []Span, error) {
	var maxC float64
	for _, jn := range joins {
		maxC = math.Max(maxC, jn.Crossfade)
	}
	out := dsp.Buffer{Rate: rate, Channels: channels}
	j := NewJoiner(rate, channels, maxC, func(s []float32) error {
		out.Samples = append(out.Samples, s...)
		return nil
	})
	spans := make([]Span, len(segs))
	for i, s := range segs {
		var jn Join
		if i < len(joins) {
			jn = joins[i]
		}
		sp, err := j.Append(s, jn)
		if err != nil {
			return out, spans, err
		}
		spans[i] = sp
	}
	return out, spans, j.Close()
}

// Boundary is the kind of break between two pieces of a script.
type Boundary int

const (
	Chunk     Boundary = iota // inside one sentence/line split for the engine
	Sentence                  // between sentences of a line
	Line                      // between lines (Stories rows, dialogue turns)
	Paragraph                 // between paragraphs
	Chapter                   // between chapters
)

// GapPolicy maps boundaries to joins (seconds). The zero Chunk value means
// "crossfade" (Crossfade, default DefaultCrossfade).
type GapPolicy struct {
	Crossfade float64 `json:"crossfade"` // Chunk joins
	Sentence  float64 `json:"sentence"`
	Line      float64 `json:"line"`
	Paragraph float64 `json:"paragraph"`
	Chapter   float64 `json:"chapter"`
}

// DefaultGaps: 50 ms crossfade inside sentences, 0.25 s between sentences,
// 0.4 s between lines, 0.8 s between paragraphs, 2 s between chapters.
var DefaultGaps = GapPolicy{Crossfade: DefaultCrossfade, Sentence: 0.25, Line: 0.4, Paragraph: 0.8, Chapter: 2}

// Join returns the join for boundary b.
func (g GapPolicy) Join(b Boundary) Join {
	switch b {
	case Sentence:
		return Join{Gap: g.Sentence}
	case Line:
		return Join{Gap: g.Line}
	case Paragraph:
		return Join{Gap: g.Paragraph}
	case Chapter:
		return Join{Gap: g.Chapter}
	}
	c := g.Crossfade
	if c == 0 {
		c = DefaultCrossfade
	}
	return Join{Crossfade: c}
}
