// Package refcheck checks and tidies voice references: the clips clone and
// dialogue models copy a voice from (thread #83, after #81's study,
// research/voice-reference-quality.md). Models copy a reference's noise,
// loudness, intonation and pace into what they say, so a reference is
// measured before it is used:
//
//   - Measure: length, speech, an SNR estimate (speech level − the noise
//     floor between words), loudness and clipping, leading and trailing
//     silence, pauses, the F0 spread (semitones) and, given the
//     transcript's size, the speaking rate; the transcript's error rate is
//     the caller's (it needs a recognizer);
//   - Report.Hints and Report.Grade turn the numbers into codes with #81's
//     thresholds; Report.Noisy says when to denoise (only then: denoising
//     a clean synthetic reference does no good);
//   - Tidy trims the edges to 0.15 s, shortens pauses over 0.7 s to 0.5 s
//     and evens the loudness out to −18 LUFS with true peaks ≤ −1.5 dBTP;
//   - PickWindow finds the best 10–14 s of a longer recording;
//   - Pick chooses among rendered candidates (designed voices' references,
//     portable copies).
//
// Speech regions come from the caller (Silero VAD in the engine) or from
// an energy VAD here (Speech). Results are codes and numbers, never UI
// strings.
package refcheck

import (
	"math"
	"slices"

	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/analysis"
	"github.com/hehehai/AudioSmith/internal/dsp/loudness"
)

// Thresholds (#81 §4.1).
const (
	MinSpeech      = 1.0  // seconds of speech below which a clip is refused
	ShortSpeech    = 5.0  // below: too short for any model
	DialogueSpeech = 8.0  // below: short for a dialogue model (8–15 s is best)
	NoisySNR       = 30.0 // dB: below, denoise
	VeryNoisySNR   = 15.0 // dB: below, record again
	ClipLimit      = 0.001
	FlatRange      = 6.0 // semitones (F0 10th–90th percentile)
	SlowRateCJK    = 3.5 // characters per second (Chinese, Japanese)
	SlowRateWords  = 2.0 // words per second (other languages)
	MismatchCER    = 0.10
	MinPause       = 0.15  // seconds: shorter silences are not pauses
	LongPause      = 0.7   // seconds: Tidy shortens longer pauses
	maxSNR         = 60.0  // digital silence: the estimate is capped
	quietest       = -65.0 // dBFS: quieter frames are never speech
)

// Use is what a reference is for: dialogue models want longer, livelier
// references than clone models.
type Use int

const (
	UseClone Use = iota
	UseDialogue
)

// Options are what Measure knows besides the audio.
type Options struct {
	// Speech are the speech regions (seconds); nil: Speech finds them.
	Speech []dsp.Range
	// Units is the transcript's size: characters for unspaced scripts
	// (CJK true), else words; 0 = no transcript (no rate).
	Units int
	CJK   bool
	// CER is the recognizer's error rate against the transcript; < 0 =
	// not known.
	CER float64
}

// Report is a reference's measures.
type Report struct {
	Seconds      float64 `json:"seconds"`
	Speech       float64 `json:"speech"`   // seconds of speech
	Lead         float64 `json:"lead"`     // silence before the first word
	Trail        float64 `json:"trail"`    // and after the last
	Pauses       int     `json:"pauses"`   // silences ≥ MinPause between words
	PausesPer10  float64 `json:"pauses10"` // per 10 s of the clip without its edges
	LongestPause float64 `json:"longestPause"`
	LUFS         float64 `json:"lufs"`
	PeakDB       float64 `json:"peakDb"`
	Clipped      float64 `json:"clipped"` // fraction of samples at full scale
	SNR          float64 `json:"snr"`     // dB, at most 60
	NoiseFloor   float64 `json:"noiseFloor"`
	F0           float64 `json:"f0"`      // median Hz; 0 = no voiced frames
	F0Range      float64 `json:"f0Range"` // semitones, 10th–90th percentile
	Rate         float64 `json:"rate"`    // units per second without the edges; 0 = unknown
	CJK          bool    `json:"cjk"`
	CER          float64 `json:"cer"` // < 0 = unknown
}

// Body is the clip without its leading and trailing silence, seconds.
func (r Report) Body() float64 { return max(r.Seconds-r.Lead-r.Trail, 0) }

// Noisy reports whether the reference should be denoised.
func (r Report) Noisy() bool { return r.Speech > 0 && r.SNR < NoisySNR }

// Measure measures a reference.
func Measure(b dsp.Buffer, o Options) Report {
	m := dsp.Remix(b, 1)
	r := Report{Seconds: m.Seconds(), CJK: o.CJK, CER: o.CER, LUFS: loudness.Floor, PeakDB: loudness.Floor}
	if m.Frames() == 0 || m.Rate <= 0 {
		return r
	}
	peak := float64(dsp.Peak(m.Samples))
	if peak > 0 {
		r.PeakDB = dsp.DB(peak)
	}
	clipped := 0
	for _, v := range m.Samples {
		if math.Abs(float64(v)) >= 0.999 {
			clipped++
		}
	}
	r.Clipped = float64(clipped) / float64(len(m.Samples))
	r.LUFS = max(loudness.Integrated(m), loudness.Floor)
	speech := o.Speech
	if speech == nil {
		speech = Speech(m)
	}
	speech = clean(speech, r.Seconds)
	r.Speech = 0
	for _, s := range speech {
		r.Speech += s.Len()
	}
	if len(speech) == 0 {
		r.Lead, r.SNR = r.Seconds, 0
		return r
	}
	r.Lead, r.Trail = speech[0].Start, r.Seconds-speech[len(speech)-1].End
	for i := 1; i < len(speech); i++ {
		if g := speech[i].Start - speech[i-1].End; g >= MinPause {
			r.Pauses++
			r.LongestPause = max(r.LongestPause, g)
		}
	}
	if body := r.Body(); body > 0 {
		r.PausesPer10 = 10 * float64(r.Pauses) / body
		if o.Units > 0 {
			r.Rate = float64(o.Units) / body
		}
	}
	r.SNR, r.NoiseFloor = snr(m, speech)
	p := analysis.Pitch(m, analysis.PitchOptions{MinHz: 70, MaxHz: 600}).Stats()
	r.F0, r.F0Range = p.MedianHz, p.RangeST
	return r
}

// clean sorts regions, clips them to the clip and merges overlaps.
func clean(rs []dsp.Range, total float64) []dsp.Range {
	out := make([]dsp.Range, 0, len(rs))
	for _, s := range rs {
		s.Start, s.End = max(s.Start, 0), min(s.End, total)
		if s.End > s.Start {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b dsp.Range) int { return cmpFloat(a.Start, b.Start) })
	merged := out[:0]
	for _, s := range out {
		if n := len(merged); n > 0 && s.Start <= merged[n-1].End {
			merged[n-1].End = max(merged[n-1].End, s.End)
			continue
		}
		merged = append(merged, s)
	}
	return merged
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Frames of the level analysis: 20 ms windows every 10 ms.
const (
	frameWin = 0.02
	frameHop = 0.01
)

// levels are the frames' RMS levels (dBFS, floor −120) of mono m.
func levels(m dsp.Buffer) []float64 {
	return analysis.FrameDB(m.Samples, max(dsp.FramesAt(frameWin, m.Rate), 1), max(dsp.FramesAt(frameHop, m.Rate), 1))
}

// snr estimates the signal-to-noise ratio: the speech frames' level (90th
// percentile) over the noise floor (10th percentile of the frames between
// speech, or the 5th of all frames when there are few), capped at 60 dB.
func snr(m dsp.Buffer, speech []dsp.Range) (snr, floor float64) {
	db := levels(m)
	var sp, quiet []float64
	for i, v := range db {
		t := float64(i)*frameHop + frameWin/2
		if inside(speech, t) {
			sp = append(sp, v)
		} else {
			quiet = append(quiet, v)
		}
	}
	if len(sp) == 0 {
		return 0, analysis.Percentile(db, 0.05)
	}
	level := analysis.Percentile(sp, 0.9)
	if len(quiet) >= 20 {
		floor = analysis.Percentile(quiet, 0.1)
	} else {
		floor = analysis.Percentile(db, 0.05)
	}
	floor = max(floor, level-maxSNR)
	return level - floor, floor
}

func inside(rs []dsp.Range, t float64) bool {
	for _, r := range rs {
		if t >= r.Start && t < r.End {
			return true
		}
	}
	return false
}

// Speech finds the speech of b by energy (#81's prototype): 10 ms frames
// above max(loudest − 35 dB, floor + 12 dB) are speech (the floor + 12 dB
// at most loudest − 6 dB, so loud noise does not hide all of it; never
// under −65 dBFS); gaps under 80 ms
// are filled and bursts under 50 ms dropped. Pauses of 0.15 s and more
// stay, so they can be counted (analysis.VAD bridges them).
func Speech(b dsp.Buffer) []dsp.Range {
	m := dsp.Remix(b, 1)
	if m.Frames() == 0 || m.Rate <= 0 {
		return nil
	}
	db := levels(m)
	// The loudest frames are speech even over loud noise.
	top := analysis.Percentile(db, 0.98)
	thr := max(top-35, min(analysis.Percentile(db, 0.05)+12, top-6), quietest)
	on := make([]bool, len(db))
	for i, v := range db {
		on[i] = v > thr
	}
	setRuns(on, false, 8) // fill gaps < 80 ms
	setRuns(on, true, 5)  // drop bursts < 50 ms
	var out []dsp.Range
	total := m.Seconds()
	for i := 0; i < len(on); {
		j := i
		for j < len(on) && on[j] == on[i] {
			j++
		}
		if on[i] {
			out = append(out, dsp.Range{Start: float64(i) * frameHop, End: min(float64(j)*frameHop+frameWin-frameHop, total)})
		}
		i = j
	}
	return out
}

// setRuns flips the inner runs of value v shorter than n frames (edge runs
// of silence stay: they are the clip's edges).
func setRuns(on []bool, v bool, n int) {
	for i := 0; i < len(on); {
		j := i
		for j < len(on) && on[j] == on[i] {
			j++
		}
		edge := i == 0 || j == len(on)
		if on[i] == v && j-i < n && !(edge && !v) {
			for k := i; k < j; k++ {
				on[k] = !v
			}
		}
		i = j
	}
}
