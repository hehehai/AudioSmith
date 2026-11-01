package main

// Measures of rendered dialogue for the e2e listening tests
// (e2e_references_test.go, e2e_book_dialogue_test.go): gaps,
// per-character loudness spread, pitch and rate, and what Qwen3-ASR hears
// of each line.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/loudness"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/services"
)

// abListen transcribes each line's place in the chapter with Qwen3-ASR
// and compares it with the line's text (character error rate over Han
// characters): a split turn's lines must hold their own words, and no
// instruction may be spoken.
func abListen(t *testing.T, ctx context.Context, sup engine.Supervisor, lib *library, path string, res services.ChapterResult, lines []services.LineSpec) string {
	e, ok := catalog.Builtin().ByID("qwen3-asr-0.6b-4bit")
	if !ok {
		return ""
	}
	snap, err := lib.modelSnapshot(ctx, e.Repo)
	if err != nil {
		t.Logf("ASR: %v", err)
		return ""
	}
	buf, _, err := wav.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("\n| # | said (ASR) | CER |\n|---|---|---|\n")
	var errs, total int
	for i, lp := range res.Lines {
		seg := buf.Slice(dsp.FramesAt(lp.Start, buf.Rate), min(dsp.FramesAt(lp.End, buf.Rate), buf.Frames()))
		f := filepath.Join(t.TempDir(), fmt.Sprintf("line%d.wav", i))
		if err := wav.WriteFile(f, seg, wav.Float32, wav.Options{}); err != nil {
			t.Fatal(err)
		}
		raw, err := sup.Call(ctx, proto.RoleGPU, core.NewID(time.Now()), proto.MethodSTTTranscribe,
			proto.TranscribeParams{Model: e.Repo, ModelPath: snap, Audio: f, Language: "zh"}, nil)
		if err != nil {
			t.Logf("ASR line %d: %v", i+1, err)
			continue
		}
		var tr proto.TranscribeResult
		json.Unmarshal(raw, &tr)
		want := hanOnly(lines[i].Spans[0].Text)
		got := hanOnly(tr.Text)
		d := editDistance([]rune(want), []rune(got))
		errs, total = errs+d, total+len([]rune(want))
		fmt.Fprintf(&b, "| %d | %s | %.0f%% |\n", i+1, tr.Text, 100*float64(d)/math.Max(float64(len([]rune(want))), 1))
	}
	fmt.Fprintf(&b, "\nCER over all lines: %.1f%%\n", 100*float64(errs)/math.Max(float64(total), 1))
	return b.String()
}

func hanOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func editDistance(a, b []rune) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			c := 1
			if a[i-1] == b[j-1] {
				c = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+c)
		}
		prev = cur
	}
	return prev[len(b)]
}

func envList(k, def string) []string {
	v := os.Getenv(k)
	if v == "" {
		v = def
	}
	return strings.Split(v, ",")
}

// abMeasure measures a chapter: per line its start, the gap before it,
// loudness, median pitch and pitch range, and speaking rate; per
// character the spread of loudness, pitch and rate.
func abMeasure(t *testing.T, path string, res services.ChapterResult, lines []services.LineSpec) string {
	buf, _, err := wav.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mono := dsp.Remix(buf, 1)
	type stat struct{ lufs, f0, rate []float64 }
	per := map[string]*stat{}
	var b strings.Builder
	b.WriteString("| # | who | start | gap before | turn | LUFS | F0 median | F0 range (st) | chars/s |\n|---|---|---|---|---|---|---|---|---|\n")
	var gaps []float64
	for i, lp := range res.Lines {
		seg := mono.Slice(dsp.FramesAt(lp.Start, mono.Rate), min(dsp.FramesAt(lp.End, mono.Rate), mono.Frames()))
		lufs := loudness.Integrated(seg)
		f0, rng := abPitch(seg)
		chars := 0
		for _, sp := range lp.Spans {
			for _, r := range sp.Text {
				if unicode.Is(unicode.Han, r) {
					chars++
				}
			}
		}
		rate := float64(chars) / math.Max(lp.End-lp.Start, 0.1)
		who := lines[i].Character
		if who == "" {
			who = "Narrator"
		}
		gap, turn := "", ""
		if i > 0 {
			g := lp.Start - res.Lines[i-1].End
			gaps = append(gaps, g)
			gap = fmt.Sprintf("%.2f", g)
			turn = services.TurnOf(lines[i-1], lines[i])
		}
		s := per[who]
		if s == nil {
			s = &stat{}
			per[who] = s
		}
		s.lufs, s.f0, s.rate = append(s.lufs, lufs), append(s.f0, f0), append(s.rate, rate)
		fmt.Fprintf(&b, "| %d | %s | %.2f | %s | %s | %.1f | %.0f Hz | %.1f | %.1f |\n", i+1, who, lp.Start, gap, turn, lufs, f0, rng, rate)
	}
	sorted := slices.Clone(gaps)
	sort.Float64s(sorted)
	if len(sorted) > 0 {
		fmt.Fprintf(&b, "\nGaps: min %.2f, median %.2f, max %.2f, total %.2f s\n\n", sorted[0], sorted[len(sorted)/2], sorted[len(sorted)-1], sum(gaps))
	}
	b.WriteString("| who | lines | loudness spread (LU) | pitch spread (st, across lines) | rate mean ± sd (chars/s) |\n|---|---|---|---|---|\n")
	var whos []string
	for w := range per {
		whos = append(whos, w)
	}
	sort.Strings(whos)
	for _, w := range whos {
		s := per[w]
		var st []float64
		for _, f := range s.f0 {
			if f > 0 {
				st = append(st, 12*math.Log2(f/100))
			}
		}
		fmt.Fprintf(&b, "| %s | %d | %.1f | %.1f | %.1f ± %.1f |\n", w, len(s.lufs), spread(s.lufs), spread(st), mean(s.rate), sd(s.rate))
	}
	return b.String()
}

// abPitch is the median F0 of a mono buffer and its range (10th to 90th
// percentile, semitones), by normalised autocorrelation on 40 ms frames.
func abPitch(b dsp.Buffer) (float64, float64) {
	x := b.Samples
	rate := b.Rate
	// Down to 12 kHz by averaging pairs.
	if rate >= 24000 {
		y := make([]float32, len(x)/2)
		for i := range y {
			y[i] = (x[2*i] + x[2*i+1]) / 2
		}
		x, rate = y, rate/2
	}
	win, hop := rate*40/1000, rate*10/1000
	lo, hi := rate/500, rate/70
	var f0s []float64
	for s := 0; s+win+hi < len(x); s += hop {
		var e float64
		for _, v := range x[s : s+win] {
			e += float64(v) * float64(v)
		}
		if e/float64(win) < 1e-4 {
			continue
		}
		best, bestLag := 0.0, 0
		for lag := lo; lag <= hi; lag++ {
			var c, e2 float64
			for i := 0; i < win; i++ {
				c += float64(x[s+i]) * float64(x[s+i+lag])
				e2 += float64(x[s+i+lag]) * float64(x[s+i+lag])
			}
			if n := c / math.Sqrt(e*e2+1e-12); n > best {
				best, bestLag = n, lag
			}
		}
		if best > 0.6 && bestLag > 0 {
			f0s = append(f0s, float64(rate)/float64(bestLag))
		}
	}
	if len(f0s) < 3 {
		return 0, 0
	}
	sort.Float64s(f0s)
	p := func(q float64) float64 { return f0s[int(q*float64(len(f0s)-1))] }
	return p(0.5), 12 * math.Log2(p(0.9)/p(0.1))
}

func sum(v []float64) float64 {
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s
}

func mean(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	return sum(v) / float64(len(v))
}

func sd(v []float64) float64 {
	if len(v) < 2 {
		return 0
	}
	m, s := mean(v), 0.0
	for _, x := range v {
		s += (x - m) * (x - m)
	}
	return math.Sqrt(s / float64(len(v)-1))
}

func spread(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	return slices.Max(v) - slices.Min(v)
}
