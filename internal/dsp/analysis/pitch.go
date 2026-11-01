package analysis

import (
	"math"

	"github.com/hehehai/AudioSmith/internal/dsp"
)

// PitchOptions tune Pitch; zero values take the defaults.
type PitchOptions struct {
	MinHz     float64 // default 60
	MaxHz     float64 // default 500
	Hop       float64 // s; default 0.01
	Threshold float64 // YIN absolute threshold on the normalised difference; default 0.15
	GateDB    float64 // frames quieter than this (dBFS RMS) are unvoiced; default −50
}

func (o PitchOptions) norm() PitchOptions {
	if o.MinHz <= 0 {
		o.MinHz = 60
	}
	if o.MaxHz <= 0 {
		o.MaxHz = 500
	}
	if o.Hop <= 0 {
		o.Hop = 0.01
	}
	if o.Threshold <= 0 {
		o.Threshold = 0.15
	}
	if o.GateDB == 0 {
		o.GateDB = -50
	}
	return o
}

// PitchContour is F0 every Hop seconds: Hz, or 0 where unvoiced or silent.
// Clarity is 1 − the YIN normalised difference at the chosen lag (0–1) for
// voiced frames, 0 for unvoiced sound and −1 for silence (under the gate).
type PitchContour struct {
	Contour
	Clarity []float64 `json:"clarity"`
}

// pitchRate is the analysis rate: plenty for F0 ≤ 1 kHz, and cheap.
const pitchRate = 16000

// Pitch estimates F0 with YIN (de Cheveigné & Kawahara 2002: difference
// function, cumulative-mean normalisation, absolute threshold, parabolic
// interpolation) on a 16 kHz mono copy, with a window of two periods of
// MinHz.
func Pitch(b dsp.Buffer, o PitchOptions) PitchContour {
	o = o.norm()
	x := at(b, pitchRate).Samples
	tauMin := int(math.Floor(pitchRate / o.MaxHz))
	tauMax := int(math.Ceil(pitchRate / o.MinHz))
	w := tauMax // integration window
	hop := max(int(math.Round(o.Hop*pitchRate)), 1)
	need := w + tauMax + 1
	pc := PitchContour{Contour: Contour{Start: float64(need) / 2 / pitchRate, Hop: float64(hop) / pitchRate}}
	if len(x) < need {
		return pc
	}
	n := (len(x)-need)/hop + 1
	pc.Values = make([]float64, n)
	pc.Clarity = make([]float64, n)
	gate := math.Pow(10, o.GateDB/10)
	d := make([]float64, tauMax+2)
	for f := range n {
		s := x[f*hop : f*hop+need]
		var e float64
		for _, v := range s[:w] {
			e += float64(v) * float64(v)
		}
		if e/float64(w) < gate {
			pc.Clarity[f] = -1
			continue
		}
		// Difference function d(τ) = Σ (x_j − x_{j+τ})², and its cumulative
		// mean normalised form d'(τ) = d(τ)·τ / Σ_{k≤τ} d(k).
		d[0] = 1
		var run float64
		for tau := 1; tau <= tauMax+1; tau++ {
			var acc float64
			for j := range w {
				diff := float64(s[j] - s[j+tau])
				acc += diff * diff
			}
			run += acc
			if run > 0 {
				d[tau] = acc * float64(tau) / run
			} else {
				d[tau] = 1
			}
		}
		tau := -1
		for t := max(tauMin, 2); t <= tauMax; t++ {
			if d[t] < o.Threshold {
				for t+1 <= tauMax && d[t+1] < d[t] {
					t++
				}
				tau = t
				break
			}
		}
		if tau < 0 {
			continue
		}
		// Parabolic interpolation around the minimum.
		a, bb, c := d[tau-1], d[tau], d[tau+1]
		shift := 0.0
		if den := a - 2*bb + c; den != 0 {
			shift = 0.5 * (a - c) / den
		}
		est := float64(tau) + max(min(shift, 1), -1)
		f0 := pitchRate / est
		if f0 < o.MinHz || f0 > o.MaxHz {
			continue
		}
		pc.Values[f] = f0
		pc.Clarity[f] = 1 - bb
	}
	return pc
}

// PitchStats summarise the voiced frames of a contour.
type PitchStats struct {
	Voiced      float64 `json:"voiced"`   // fraction of non-silent frames that are voiced
	MedianHz    float64 `json:"medianHz"` // 0 when nothing is voiced
	MeanHz      float64 `json:"meanHz"`
	StdST       float64 `json:"stdSt"`       // standard deviation in semitones (pitch movement)
	RangeST     float64 `json:"rangeSt"`     // 10th–90th percentile span in semitones
	VariationHz float64 `json:"variationHz"` // standard deviation in Hz
}

// Stats summarises the contour. Frames under the gate (silence) do not
// count towards Voiced.
func (p PitchContour) Stats() PitchStats {
	var hz []float64
	analysed := 0
	for i, v := range p.Values {
		if v > 0 {
			hz = append(hz, v)
		}
		if p.Clarity[i] >= 0 {
			analysed++
		}
	}
	if len(hz) == 0 {
		return PitchStats{}
	}
	st := PitchStats{Voiced: float64(len(hz)) / float64(max(analysed, 1)), MedianHz: median(hz)}
	var sum, sumSq float64
	for _, v := range hz {
		sum += v
	}
	st.MeanHz = sum / float64(len(hz))
	semis := make([]float64, len(hz))
	var ms float64
	for i, v := range hz {
		semis[i] = 12 * math.Log2(v/st.MedianHz)
		ms += semis[i]
		sumSq += (v - st.MeanHz) * (v - st.MeanHz)
	}
	ms /= float64(len(hz))
	var vs float64
	for _, s := range semis {
		vs += (s - ms) * (s - ms)
	}
	st.StdST = math.Sqrt(vs / float64(len(hz)))
	st.VariationHz = math.Sqrt(sumSq / float64(len(hz)))
	st.RangeST = Percentile(semis, 0.9) - Percentile(semis, 0.1)
	return st
}
