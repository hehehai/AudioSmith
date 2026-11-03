package dub

import (
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/analysis"
)

// AssignSpeakers gives each segment the speaker whose turns overlap it
// most, splitting a segment with timed words where the speaker changes
// for at least two words and half a second. Speakers are relabelled S1,
// S2… in order of their first line. Without turns everyone is S1.
func (p *Project) AssignSpeakers(turns []Turn) {
	var out []Segment
	for _, s := range p.Segments {
		if len(turns) == 0 {
			s.Speaker = "S1"
			out = append(out, s)
			continue
		}
		out = append(out, p.splitByTurns(s, turns)...)
	}
	// Relabel in order of appearance.
	labels := map[string]string{}
	for i := range out {
		sp := out[i].Speaker
		if sp == "" {
			sp = "?"
		}
		l, ok := labels[sp]
		if !ok {
			l = "S" + strconv.Itoa(len(labels)+1)
			labels[sp] = l
		}
		out[i].Speaker = l
	}
	p.Segments = out
}

// speakerAt is the speaker whose turns overlap start–end most.
func speakerAt(turns []Turn, start, end float64) string {
	ov := map[string]float64{}
	best, bestOv := "", 0.0
	for _, t := range turns {
		o := min(end, t.End) - max(start, t.Start)
		if o > 0 {
			ov[t.Speaker] += o
			if ov[t.Speaker] > bestOv {
				best, bestOv = t.Speaker, ov[t.Speaker]
			}
		}
	}
	if best == "" {
		// No overlap: the nearest turn.
		dist := math.Inf(1)
		mid := (start + end) / 2
		for _, t := range turns {
			d := math.Min(math.Abs(mid-t.Start), math.Abs(mid-t.End))
			if d < dist {
				best, dist = t.Speaker, d
			}
		}
	}
	return best
}

func (p *Project) splitByTurns(s Segment, turns []Turn) []Segment {
	s.Speaker = speakerAt(turns, s.Start, s.End)
	if len(s.Words) < 4 {
		return []Segment{s}
	}
	// Runs of words by speaker.
	type run struct {
		speaker  string
		from, to int // words
	}
	var runs []run
	for i, w := range s.Words {
		sp := speakerAt(turns, w.Start, max(w.End, w.Start+0.01))
		if n := len(runs); n > 0 && runs[n-1].speaker == sp {
			runs[n-1].to = i + 1
			continue
		}
		runs = append(runs, run{sp, i, i + 1})
	}
	// Runs too short to be a turn join the one before (or after).
	for i := 0; i < len(runs); {
		r := runs[i]
		short := r.to-r.from < 2 || s.Words[r.to-1].End-s.Words[r.from].Start < 0.5
		if short && len(runs) > 1 {
			if i > 0 {
				runs[i-1].to = r.to
			} else {
				runs[i+1].from = r.from
			}
			runs = slices.Delete(runs, i, i+1)
			// Neighbours may now share a speaker.
			for j := 1; j < len(runs); {
				if runs[j].speaker == runs[j-1].speaker {
					runs[j-1].to = runs[j].to
					runs = slices.Delete(runs, j, j+1)
					continue
				}
				j++
			}
			i = 0
			continue
		}
		i++
	}
	if len(runs) < 2 {
		return []Segment{s}
	}
	var out []Segment
	for k, r := range runs {
		seg := s
		if k > 0 {
			seg.ID = p.newID()
		}
		seg.Speaker = r.speaker
		seg.Words = slices.Clone(s.Words[r.from:r.to])
		seg.Start = s.Words[r.from].Start
		if k == 0 {
			seg.Start = s.Start
		}
		seg.End = s.Words[r.to-1].End
		if k == len(runs)-1 {
			seg.End = s.End
		}
		var b strings.Builder
		for _, w := range seg.Words {
			b.WriteString(joinSep(b.String(), w.Text))
			b.WriteString(w.Text)
		}
		seg.Text = strings.TrimSpace(b.String())
		seg.Lines, seg.Parts = nil, nil
		out = append(out, seg)
	}
	return out
}

// joinSep is the separator before word w after text so far.
func joinSep(so, w string) string {
	if so == "" || hasUnspaced(w) || strings.HasSuffix(so, " ") {
		return ""
	}
	return " "
}

// --- clone references -----------------------------------------------------------

// Reference windows, seconds.
const (
	RefMin   = 5.0
	RefMax   = 15.0
	RefIdeal = 10.0
	// RefFloor: a speaker with less speech than this gets no reference
	// (their lines use the default voice unless cast).
	RefFloor = 3.0
)

// RefCandidate is a window of a speaker's speech.
type RefCandidate struct {
	Speaker    string
	Start, End float64
	Text       string
	Score      float64
}

// Audio is audio read a window at a time (a *wav.File; a dsp.Buffer as
// BufferAudio), so a long recording is never read whole.
type Audio interface {
	Seconds() float64
	// ReadSeconds reads [from, to), clamped to the audio.
	ReadSeconds(from, to float64) (dsp.Buffer, error)
}

// BufferAudio is a Buffer as Audio.
type BufferAudio struct{ dsp.Buffer }

// ReadSeconds slices [from, to), clamped.
func (b BufferAudio) ReadSeconds(from, to float64) (dsp.Buffer, error) {
	return b.Slice(dsp.FramesAt(from, b.Rate), dsp.FramesAt(to, b.Rate)), nil
}

// PickReferences picks each speaker's best clone reference from audio
// (their voices, separated if possible): runs of their consecutive lines
// (gaps under 0.8 s, nobody else speaking over them), cut to at most 15 s
// and preferably 5 s or more, scored by how much of the window is speech,
// how steady and how loud it is, and how close it is to 10 s. Speakers
// with under 3 s of speech get none. Only the candidate windows are read
// (code review 1103 F9).
func PickReferences(segs []Segment, audio Audio) (map[string]RefCandidate, error) {
	out := map[string]RefCandidate{}
	if audio.Seconds() <= 0 {
		return out, nil
	}
	var readErr error
	scoreWindow := func(start, end float64) float64 {
		if readErr != nil {
			return 0
		}
		w, err := audio.ReadSeconds(start, end)
		if err != nil {
			readErr = err
			return 0
		}
		return scoreAudio(w)
	}
	bySpeaker := map[string][]int{}
	for i, s := range segs {
		bySpeaker[s.Speaker] = append(bySpeaker[s.Speaker], i)
	}
	for sp, idx := range bySpeaker {
		var cands []RefCandidate
		total := 0.0
		for _, i := range idx {
			total += segs[i].Duration()
		}
		if total < RefFloor {
			continue
		}
		// Runs of consecutive lines.
		for k := 0; k < len(idx); {
			start := segs[idx[k]].Start
			end := segs[idx[k]].End
			texts := []string{segs[idx[k]].Text}
			j := k + 1
			for ; j < len(idx); j++ {
				s := segs[idx[j]]
				if s.Start-end > 0.8 || s.End-start > RefMax || overlapsOther(segs, sp, end, s.Start) {
					break
				}
				end = s.End
				texts = append(texts, s.Text)
			}
			// A lone long line is cut to 15 s.
			if end-start > RefMax {
				end = start + RefMax
			}
			c := RefCandidate{Speaker: sp, Start: start, End: end, Text: joinTexts(texts)}
			if !overlapsOther(segs, sp, start, end) {
				c.Score = scoreWindow(start, end)
				cands = append(cands, c)
			}
			k = j
		}
		if len(cands) == 0 {
			// Every run overlaps someone: take the longest line anyway.
			best := idx[0]
			for _, i := range idx {
				if segs[i].Duration() > segs[best].Duration() {
					best = i
				}
			}
			s := segs[best]
			c := RefCandidate{Speaker: sp, Start: s.Start, End: min(s.End, s.Start+RefMax), Text: s.Text}
			c.Score = scoreWindow(c.Start, c.End) * 0.5
			cands = append(cands, c)
		}
		slices.SortStableFunc(cands, func(a, b RefCandidate) int {
			switch {
			case a.Score > b.Score:
				return -1
			case a.Score < b.Score:
				return 1
			}
			return 0
		})
		out[sp] = cands[0]
	}
	if readErr != nil {
		return nil, readErr
	}
	return out, nil
}

func joinTexts(ts []string) string {
	out := ""
	for _, t := range ts {
		out = joinText(out, t)
	}
	return out
}

// overlapsOther reports whether another speaker speaks during start–end.
func overlapsOther(segs []Segment, speaker string, start, end float64) bool {
	for _, s := range segs {
		if s.Speaker != speaker && min(end, s.End)-max(start, s.Start) > 0.05 {
			return true
		}
	}
	return false
}

// scoreAudio rates a window of audio as a clone reference
// (0–1).
func scoreAudio(w dsp.Buffer) float64 {
	if w.Frames() < w.Rate/2 {
		return 0
	}
	length := w.Seconds()
	segs := analysis.VAD(w, analysis.VADOptions{})
	speech := analysis.SpeechSeconds(segs) / length
	// Level: RMS of the speech, best around −20 dBFS.
	var e float64
	var n int
	ch := max(w.Channels, 1)
	for _, r := range segs {
		a := min(dsp.FramesAt(r.Start, w.Rate), w.Frames())
		b := min(dsp.FramesAt(r.End, w.Rate), w.Frames())
		for i := a * ch; i < b*ch; i++ {
			v := float64(w.Samples[i])
			e += v * v
			n++
		}
	}
	if n == 0 {
		// Continuous speech without a pause leaves the energy VAD no
		// floor to measure against: count the whole window if it is loud.
		for _, v := range w.Samples {
			e += float64(v) * float64(v)
		}
		n = len(w.Samples)
		if 10*math.Log10(e/float64(max(n, 1))+1e-12) > -45 {
			speech = 0.9
		}
	}
	level := 0.0
	if n > 0 {
		db := 10 * math.Log10(e/float64(n)+1e-12)
		level = math.Exp(-math.Pow((db+20)/18, 2))
	}
	// Steadiness: loudness spread over speech.
	lc := analysis.LoudnessContour(w, 0.4, 0.2)
	var vals []float64
	for _, v := range lc.Values {
		if v > -60 {
			vals = append(vals, v)
		}
	}
	steady := 1.0
	if len(vals) > 1 {
		var mean, vs float64
		for _, v := range vals {
			mean += v
		}
		mean /= float64(len(vals))
		for _, v := range vals {
			vs += (v - mean) * (v - mean)
		}
		std := math.Sqrt(vs / float64(len(vals)))
		steady = math.Exp(-std / 8)
	}
	// Length: 1 at 10 s, lower towards 5 and 15, low under 5.
	lenScore := math.Exp(-math.Pow((length-RefIdeal)/6, 2))
	if length < RefMin {
		lenScore *= length / RefMin
	}
	return round3(speech * (0.4 + 0.6*level) * (0.5 + 0.5*steady) * lenScore)
}
