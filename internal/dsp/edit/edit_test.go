package edit

import (
	"errors"
	"math"
	"testing"

	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/dsptest"
)

func TestConcatJoins(t *testing.T) {
	const r = 24000
	a := dsptest.Noise(r, 2, -20, 1, 1)
	b := dsptest.Noise(r, 2, -20, 1, 2)
	cases := []struct {
		name   string
		join   Join
		frames int
		bStart int
	}{
		{"butt", Join{}, 48000, 24000},
		{"gap 0.35 s", Join{Gap: 0.35}, 48000 + 8400, 24000 + 8400},
		{"crossfade 50 ms", Join{Crossfade: 0.05}, 48000 - 1200, 24000 - 1200},
		{"gap wins", Join{Gap: 0.1, Crossfade: 0.05}, 48000 + 2400, 24000 + 2400},
	}
	for _, c := range cases {
		out, spans, err := Concat(r, 2, [][]float32{a.Samples, b.Samples}, []Join{{}, c.join})
		if err != nil {
			t.Fatal(err)
		}
		if out.Frames() != c.frames || spans[1].Start != c.bStart || spans[1].End != c.bStart+24000 || spans[0] != (Span{0, 24000}) {
			t.Errorf("%s: %d frames, spans %v", c.name, out.Frames(), spans)
		}
		if c.join.Gap > 0 {
			gap := out.Slice(24000, c.bStart)
			if dsp.Peak(gap.Samples) != 0 {
				t.Errorf("%s: gap not silent", c.name)
			}
		}
	}
	// Equal power keeps the level of uncorrelated material through the fade.
	out, _, _ := Concat(r, 2, [][]float32{a.Samples, b.Samples}, []Join{{}, {Crossfade: 0.05}})
	mid := out.Slice(24000-1200, 24000)
	if l := dsp.DB(dsp.RMS(mid.Samples)); math.Abs(l+20) > 1 {
		t.Errorf("crossfade region %.2f dB, want −20 ±1", l)
	}
	// Linear keeps correlated (identical) material constant.
	dc := dsp.New(r, 1, 4800)
	for i := range dc.Samples {
		dc.Samples[i] = 0.5
	}
	out, _, _ = Concat(r, 1, [][]float32{dc.Samples, dc.Samples}, []Join{{}, {Crossfade: 0.05, Curve: dsp.Linear}})
	for i, v := range out.Samples {
		if math.Abs(float64(v)-0.5) > 1e-6 {
			t.Fatalf("linear crossfade sample %d = %v", i, v)
		}
	}
	// A crossfade longer than a segment is clamped to it.
	short := dsptest.Sine(r, 1, 440, -6, 0.01, 0)
	out, spans, _ := Concat(r, 1, [][]float32{dc.Samples, short.Samples}, []Join{{}, {Crossfade: 0.05}})
	if out.Frames() != 4800 || spans[1].Start != 4800-240 {
		t.Errorf("clamped crossfade: %d frames, %v", out.Frames(), spans)
	}
	if _, _, err := Concat(r, 2, [][]float32{{1}}, nil); !errors.Is(err, ErrFormat) {
		t.Error("partial frame")
	}
}

func TestJoinerStreams(t *testing.T) {
	const r = 48000
	var segs [][]float32
	var joins []Join
	g := DefaultGaps
	for i := range 40 {
		segs = append(segs, dsptest.Noise(r, 1, -20, 0.2+0.01*float64(i%7), uint64(i)).Samples)
		joins = append(joins, g.Join(Boundary(i%5)))
	}
	want, wantSpans, _ := Concat(r, 1, segs, joins)
	var got []float32
	j := NewJoiner(r, 1, 0.05, func(s []float32) error {
		got = append(got, s...)
		return nil
	})
	for i, s := range segs {
		sp, err := j.Append(s, joins[i])
		if err != nil || sp != wantSpans[i] {
			t.Fatalf("seg %d: %v %v vs %v", i, err, sp, wantSpans[i])
		}
		if len(j.tail) > 2400 {
			t.Fatalf("tail grew to %d frames", len(j.tail))
		}
	}
	sp, _ := j.Silence(0.5)
	if sp.End-sp.Start != 24000 {
		t.Errorf("silence span %v", sp)
	}
	j.Close()
	if len(got) != len(want.Samples)+24000 {
		t.Fatalf("len %d vs %d", len(got), len(want.Samples)+24000)
	}
	for i := range want.Samples {
		if got[i] != want.Samples[i] {
			t.Fatalf("sample %d differs", i)
		}
	}
	boom := errors.New("disk full")
	j = NewJoiner(r, 1, 0.05, func([]float32) error { return boom })
	if _, err := j.Append(segs[0], Join{}); !errors.Is(err, boom) {
		t.Error("sink error")
	}
	if _, err := j.Append(segs[1], Join{}); !errors.Is(err, boom) || j.Close() != boom {
		t.Error("sticky error")
	}
}

func TestGapPolicy(t *testing.T) {
	g := DefaultGaps
	if g.Join(Chunk).Crossfade != 0.05 || g.Join(Line).Gap != 0.4 || g.Join(Paragraph).Gap != 0.8 || g.Join(Chapter).Gap != 2 || g.Join(Sentence).Gap != 0.25 {
		t.Error("default gaps")
	}
	if (GapPolicy{}).Join(Chunk).Crossfade != DefaultCrossfade {
		t.Error("zero crossfade → default")
	}
}

func TestTrim(t *testing.T) {
	const r = 24000
	tone := dsptest.Sine(r, 2, 300, -12, 1, 0)
	b := dsptest.Concat(dsptest.Silence(r, 2, 0.5), tone, dsptest.Silence(r, 2, 0.7))
	dsptest.Add(b, dsptest.Noise(r, 2, -70, b.Seconds(), 4), 0) // a hiss under the threshold
	s, e := TrimRange(b, TrimOptions{})
	// Sound is [12000, 36000); keep 30 ms (720 frames), window 10 ms (240).
	if s < 12000-720-240 || s > 12000-720 || e < 36000+720 || e > 36000+720+240 {
		t.Errorf("trim range [%d, %d)", s, e)
	}
	out := Trim(b, TrimOptions{})
	if out.Frames() != e-s || math.Abs(float64(out.Samples[0])) > 1e-4 {
		t.Errorf("trim: %d frames, first %v", out.Frames(), out.Samples[0])
	}
	if s, e := TrimRange(dsptest.Silence(r, 1, 1), TrimOptions{}); s != e {
		t.Error("all silence")
	}
	// Nothing to trim: unchanged length, no fades.
	if out := Trim(tone, TrimOptions{}); out.Frames() != tone.Frames() || out.Samples[2] != tone.Samples[2] {
		t.Error("no-op trim")
	}
	// A higher threshold trims a quiet lead-in.
	q := dsptest.Concat(dsptest.Sine(r, 1, 300, -45, 0.3, 0), dsptest.Sine(r, 1, 300, -6, 0.5, 0))
	if s, _ := TrimRange(q, TrimOptions{ThresholdDB: -30, Keep: -1}); s != 7200 {
		t.Errorf("threshold −30: start %d", s)
	}
}

func TestPauses(t *testing.T) {
	const r = 16000
	tone := func(sec float64) dsp.Buffer { return dsptest.Sine(r, 1, 200, -10, sec, 0) }
	sil := func(sec float64) dsp.Buffer { return dsptest.Silence(r, 1, sec) }
	b := dsptest.Concat(sil(0.4), tone(0.5), sil(1.5), tone(0.5), sil(0.2), tone(0.5), sil(1))
	ps := Pauses(b, 0.3, TrimOptions{})
	if len(ps) != 1 || math.Abs(dsp.SecondsAt(ps[0].End-ps[0].Start, r)-1.5) > 0.03 {
		t.Fatalf("pauses %v", ps)
	}
	out, cuts := LimitPauses(b, 0.4, TrimOptions{})
	if len(cuts) != 1 {
		t.Fatalf("cuts %v", cuts)
	}
	ps = Pauses(out, 0.3, TrimOptions{})
	if len(ps) != 1 || math.Abs(dsp.SecondsAt(ps[0].End-ps[0].Start, r)-0.4) > 0.03 {
		t.Errorf("after limit: %v", ps)
	}
	if d := b.Seconds() - out.Seconds(); math.Abs(d-1.1) > 0.03 {
		t.Errorf("removed %.3f s, want 1.1", d)
	}
}

func BenchmarkJoiner(b *testing.B) {
	seg := dsptest.Noise(48000, 2, -20, 2, 1).Samples
	b.SetBytes(int64(len(seg) * 4 * 20))
	for b.Loop() {
		j := NewJoiner(48000, 2, 0.05, func([]float32) error { return nil })
		for range 20 {
			j.Append(seg, Join{Crossfade: 0.05})
		}
		j.Close()
	}
}

// TestTrimScannerChunks: fed in uneven chunks, the scanner finds the
// range TrimRange finds whole (thread #150 streams exports with it).
func TestTrimScannerChunks(t *testing.T) {
	b := dsp.Buffer{Rate: 16000, Channels: 2, Samples: make([]float32, 2*16000)}
	for i := 2 * 3000; i < 2*11007; i++ {
		b.Samples[i] = 0.3
	}
	ws, we := TrimRange(b, TrimOptions{})
	sc := NewTrimScanner(b.Rate, b.Channels, TrimOptions{})
	for i := 0; i < len(b.Samples); {
		n := min(2*(37+i%101), len(b.Samples)-i)
		sc.Write(b.Samples[i : i+n])
		i += n
	}
	if s, e := sc.Range(); s != ws || e != we || ws == 0 {
		t.Errorf("chunked %d–%d, whole %d–%d", s, e, ws, we)
	}
	if sc.FadeFrames() != 80 {
		t.Errorf("fade %d frames", sc.FadeFrames())
	}
}
