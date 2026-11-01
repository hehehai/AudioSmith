package analysis

import (
	"math"

	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/loudness"
)

// RateOptions tune syllable-nucleus detection; zero values take the defaults.
type RateOptions struct {
	MinDipDB   float64 // a nucleus must stand this far above the dip before it; default 2 dB
	BelowTopDB float64 // nuclei are within this of the loudest frame; default 25 dB
	Voiced     bool    // keep only nuclei where Pitch finds voicing (slower, more robust on noise)
}

// Nuclei returns the times of syllable nuclei: peaks of a smoothed
// intensity contour (50 ms windows, 10 ms hop) that are above the median
// intensity of the speech, within BelowTopDB of the maximum, and separated
// from the previous nucleus by a dip of at least MinDipDB (after de Jong &
// Wempe 2009, without Praat).
func Nuclei(b dsp.Buffer, o RateOptions) []float64 {
	if o.MinDipDB <= 0 {
		o.MinDipDB = 2
	}
	if o.BelowTopDB <= 0 {
		o.BelowTopDB = 25
	}
	m := mono(b)
	win := max(dsp.FramesAt(0.05, m.Rate), 1)
	hop := max(dsp.FramesAt(0.01, m.Rate), 1)
	db := FrameDB(m.Samples, win, hop)
	if len(db) < 3 {
		return nil
	}
	// Smooth with a 3-point average.
	sm := make([]float64, len(db))
	for i := range db {
		lo, hi := max(i-1, 0), min(i+2, len(db))
		var s float64
		for _, v := range db[lo:hi] {
			s += v
		}
		sm[i] = s / float64(hi-lo)
	}
	top := Percentile(sm, 0.99)
	var speech []float64
	for _, v := range sm {
		if v > top-o.BelowTopDB {
			speech = append(speech, v)
		}
	}
	thr := math.Max(median(speech), top-o.BelowTopDB)
	t := func(i int) float64 { return (float64(i*hop) + float64(win)/2) / float64(m.Rate) }
	var pc PitchContour
	if o.Voiced {
		pc = Pitch(b, PitchOptions{})
	}
	voicedAt := func(sec float64) bool {
		if !o.Voiced {
			return true
		}
		for _, d := range []float64{-0.02, 0, 0.02} {
			i := int(math.Round((sec + d - pc.Start) / pc.Hop))
			if i >= 0 && i < len(pc.Values) && pc.Values[i] > 0 {
				return true
			}
		}
		return false
	}
	var peaks []int
	dip := math.Inf(1) // lowest value since the last accepted peak
	for i := 1; i+1 < len(sm); i++ {
		dip = min(dip, sm[i])
		if sm[i] < thr || sm[i] < sm[i-1] || sm[i] < sm[i+1] {
			continue
		}
		if n := len(peaks); n > 0 && sm[i]-dip < o.MinDipDB {
			// Not separated from the previous peak: keep the higher one.
			if sm[i] > sm[peaks[n-1]] {
				peaks[n-1] = i
			}
			continue
		}
		peaks = append(peaks, i)
		dip = sm[i]
	}
	var out []float64
	for _, p := range peaks {
		if s := t(p); voicedAt(s) {
			out = append(out, s)
		}
	}
	return out
}

// SpeakingRate returns syllables per second of speech (the articulation
// rate: nuclei over VAD speech time) and the nucleus count.
func SpeakingRate(b dsp.Buffer, o RateOptions) (rate float64, syllables int) {
	n := Nuclei(b, o)
	sp := SpeechSeconds(VAD(b, VADOptions{}))
	if sp <= 0 {
		return 0, len(n)
	}
	return float64(len(n)) / sp, len(n)
}

// Prosody summarises how a line is spoken.
type Prosody struct {
	Seconds       float64    `json:"seconds"`
	SpeechSeconds float64    `json:"speechSeconds"`
	Pitch         PitchStats `json:"pitch"`
	Loudness      float64    `json:"loudness"`    // LUFS over speech (floor −120)
	LoudnessVar   float64    `json:"loudnessVar"` // std of momentary loudness over speech, LU
	Syllables     int        `json:"syllables"`
	Rate          float64    `json:"rate"` // syllables per second of speech
}

// Analyze measures b's prosody.
func Analyze(b dsp.Buffer) Prosody {
	p := Prosody{Seconds: b.Seconds()}
	segs := VAD(b, VADOptions{})
	p.SpeechSeconds = SpeechSeconds(segs)
	p.Pitch = Pitch(b, PitchOptions{}).Stats()
	p.Rate, p.Syllables = SpeakingRate(b, RateOptions{})
	// Loudness over the speech only: momentary values inside segments.
	lc := LoudnessContour(b, 0.4, 0.1)
	var vals []float64
	for i, v := range lc.Values {
		t := lc.Time(i)
		for _, s := range segs {
			if t >= s.Start && t < s.End && v > -70 {
				vals = append(vals, v)
				break
			}
		}
	}
	p.Loudness = loudness.Floor
	if len(vals) > 0 {
		var pw float64
		for _, v := range vals {
			pw += math.Pow(10, v/10)
		}
		p.Loudness = 10 * math.Log10(pw/float64(len(vals)))
		var mean, vs float64
		for _, v := range vals {
			mean += v
		}
		mean /= float64(len(vals))
		for _, v := range vals {
			vs += (v - mean) * (v - mean)
		}
		p.LoudnessVar = math.Sqrt(vs / float64(len(vals)))
	}
	return p
}

// Delta is a line's prosody relative to a speaker baseline.
type Delta struct {
	PitchST    float64  `json:"pitchSt"`    // median pitch difference, semitones
	Movement   float64  `json:"movement"`   // pitch std ratio (line / baseline)
	LoudnessLU float64  `json:"loudnessLu"` // loudness difference
	RateRatio  float64  `json:"rateRatio"`  // speaking-rate ratio
	Voicing    float64  `json:"voicing"`    // voiced-fraction difference
	Directions []string `json:"directions"` // codes: higher lower louder softer faster slower animated flat whispered
}

// Compare describes line against base. Directions use thresholds of
// ±2 semitones, ±3 LU, rate ×1.15 / ×0.87, pitch movement ×1.4 / ×0.7, and
// "whispered" for a mostly unvoiced line (voicing < 0.3 where the baseline
// is > 0.5) that is also quieter.
func Compare(line, base Prosody) Delta {
	d := Delta{Directions: []string{}}
	if line.Pitch.MedianHz > 0 && base.Pitch.MedianHz > 0 {
		d.PitchST = 12 * math.Log2(line.Pitch.MedianHz/base.Pitch.MedianHz)
	}
	if base.Pitch.StdST > 0 {
		d.Movement = line.Pitch.StdST / base.Pitch.StdST
	}
	if line.Loudness > loudness.Floor && base.Loudness > loudness.Floor {
		d.LoudnessLU = line.Loudness - base.Loudness
	}
	if base.Rate > 0 {
		d.RateRatio = line.Rate / base.Rate
	}
	d.Voicing = line.Pitch.Voiced - base.Pitch.Voiced
	add := func(c string) { d.Directions = append(d.Directions, c) }
	whisper := line.Pitch.Voiced < 0.3 && base.Pitch.Voiced > 0.5 && d.LoudnessLU < 0
	switch {
	case whisper:
		add("whispered")
	case d.PitchST >= 2:
		add("higher")
	case d.PitchST <= -2 && line.Pitch.MedianHz > 0:
		add("lower")
	}
	switch {
	case d.LoudnessLU >= 3:
		add("louder")
	case d.LoudnessLU <= -3 && !whisper:
		add("softer")
	}
	switch {
	case d.RateRatio >= 1.15:
		add("faster")
	case d.RateRatio > 0 && d.RateRatio <= 0.87:
		add("slower")
	}
	switch {
	case d.Movement >= 1.4:
		add("animated")
	case d.Movement > 0 && d.Movement <= 0.7 && !whisper:
		add("flat")
	}
	return d
}

// Baseline merges several lines' prosody into a speaker baseline
// (speech-time-weighted means; loudness power-averaged).
func Baseline(lines []Prosody) Prosody {
	var b Prosody
	var w, pw float64
	var hz, std, voiced, rate float64
	var wp float64
	for _, l := range lines {
		s := l.SpeechSeconds
		if s <= 0 {
			continue
		}
		w += s
		b.Seconds += l.Seconds
		b.SpeechSeconds += s
		b.Syllables += l.Syllables
		rate += l.Rate * s
		voiced += l.Pitch.Voiced * s
		if l.Loudness > loudness.Floor {
			pw += math.Pow(10, l.Loudness/10) * s
		}
		b.LoudnessVar += l.LoudnessVar * s
		if l.Pitch.MedianHz > 0 {
			hz += math.Log2(l.Pitch.MedianHz) * s
			std += l.Pitch.StdST * s
			wp += s
		}
	}
	if w == 0 {
		b.Loudness = loudness.Floor
		return b
	}
	b.Rate = rate / w
	b.Pitch.Voiced = voiced / w
	b.LoudnessVar /= w
	b.Loudness = loudness.Floor
	if pw > 0 {
		b.Loudness = 10 * math.Log10(pw/w)
	}
	if wp > 0 {
		b.Pitch.MedianHz = math.Exp2(hz / wp)
		b.Pitch.StdST = std / wp
	}
	return b
}
