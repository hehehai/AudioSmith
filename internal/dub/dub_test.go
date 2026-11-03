package dub

import (
	"bytes"
	"context"
	"math"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
)

func sample() *Project {
	p := New("d1", time.Unix(0, 0))
	p.SourceLang = "en"
	p.Source.Duration = 20
	add := func(start, end float64, sp, text string) {
		p.Segments = append(p.Segments, p.NewSegment(start, end, sp, text))
	}
	add(0.5, 3, "S1", "Hello there. How are you today?")
	add(3.4, 6, "S2", "I am fine, thank you.")
	add(7, 9.5, "S1", "Good to hear.")
	return p
}

func TestSplitMergeRestoresSpeakers(t *testing.T) {
	p := sample()
	id, ok := p.Split("s1", "", -1)
	if !ok || len(p.Segments) != 4 {
		t.Fatalf("split: %v %d", ok, len(p.Segments))
	}
	a, b := p.Segments[0], p.Segments[1]
	if a.Text != "Hello there." || b.Text != "How are you today?" || b.ID != id {
		t.Fatalf("texts %q | %q", a.Text, b.Text)
	}
	if a.End != b.Start || a.End <= 0.5 || a.End >= 3 {
		t.Errorf("timing %v %v", a.End, b.Start)
	}
	// Merge S1's second half with S2's line, then split again: S2 is back.
	if !p.Merge(id, true) {
		t.Fatal("merge")
	}
	m := p.Segments[1]
	if m.Text != "How are you today? I am fine, thank you." || len(m.Parts) != 1 || m.Parts[0].Speaker != "S2" {
		t.Fatalf("merged %q %+v", m.Text, m.Parts)
	}
	at := strings.Index(m.Text, "I am")
	id2, ok := p.Split(m.ID, "", at)
	if !ok {
		t.Fatal("split again")
	}
	s, _ := p.Segment(id2)
	if s.Speaker != "S2" || s.Text != "I am fine, thank you." {
		t.Errorf("after split: %q by %s", s.Text, s.Speaker)
	}
}

func TestSplitTranslationAndCJK(t *testing.T) {
	p := sample()
	p.SetText("s1", "zh", "你好。你今天好吗？")
	if !p.Segments[0].Lines["zh"].Edited {
		t.Error("edited flag")
	}
	_, ok := p.Split("s1", "zh", 3)
	if !ok {
		t.Fatal("split")
	}
	if got := p.Segments[0].Lines["zh"].Text; got != "你好。" {
		t.Errorf("zh first %q", got)
	}
	if got := p.Segments[1].Lines["zh"].Text; got != "你今天好吗？" {
		t.Errorf("zh second %q", got)
	}
	if p.Segments[0].Text == "" || p.Segments[1].Text == "" {
		t.Error("source not split too")
	}
	if got := joinText("你好", "世界"); got != "你好世界" {
		t.Error(got)
	}
}

func TestInsertDeleteBulkAndHistory(t *testing.T) {
	p := sample()
	var h History
	h.Push(Take(p, "insert"))
	id := p.Insert("s2", p.Source.Duration)
	s, _ := p.Segment(id)
	if s.Start != 6 || s.End != 7 || s.Speaker != "S2" {
		t.Errorf("inserted %+v", s)
	}
	h.Push(Take(p, "voice"))
	p.SetVoice("v123", "s1", "s3")
	if p.VoiceOf(p.Segments[0]) != "v123" {
		t.Error("voice")
	}
	h.Push(Take(p, "delete"))
	p.Delete("s1", id)
	if len(p.Segments) != 2 {
		t.Fatal(len(p.Segments))
	}
	h.Undo(p)
	h.Undo(p)
	if len(p.Segments) != 4 || p.Segments[0].Voice != "" {
		t.Errorf("after 2 undos: %d segments, voice %q", len(p.Segments), p.Segments[0].Voice)
	}
	h.Redo(p)
	if p.Segments[0].Voice != "v123" {
		t.Error("redo")
	}
	for range HistoryLimit + 10 {
		h.Push(Take(p, ""))
	}
	if h.Len() != HistoryLimit {
		t.Errorf("history %d", h.Len())
	}
	if !h.Coalesce("") == false && h.Coalesce("") {
		t.Error("empty labels never coalesce")
	}
}

func TestTimingAndNudge(t *testing.T) {
	p := sample()
	p.SetTiming("s2", 2.5, 6.5, 20, true) // clamped by neighbours
	s, _ := p.Segment("s2")
	if s.Start != 3 || s.End != 6.5 {
		t.Errorf("clamped %v–%v", s.Start, s.End)
	}
	p.Nudge("s2", "move", 0.1, 20)
	s, _ = p.Segment("s2")
	if math.Abs(s.Start-3.1) > 1e-9 || math.Abs(s.End-6.6) > 1e-9 {
		t.Errorf("nudged %v–%v", s.Start, s.End)
	}
	p.SetTiming("s3", 19.9, 25, 20, false)
	s, _ = p.Segment("s3")
	if s.End != 20 || s.End-s.Start < MinSegment-1e-9 {
		t.Errorf("end clamp %v–%v", s.Start, s.End)
	}
	p.Segments[0].End = 4
	if o := p.Overlaps(); !slices.Equal(o, []string{"s1"}) {
		t.Errorf("overlaps %v", o)
	}
}

func TestCleanupMergesFragments(t *testing.T) {
	p := New("d", time.Now())
	p.Segments = []Segment{
		p.NewSegment(0, 2, "S1", "This is a sentence"),
		p.NewSegment(2.1, 2.6, "S1", "really."),
		p.NewSegment(4, 6, "S2", "Another one here."),
	}
	if n := p.Cleanup(); n != 1 || len(p.Segments) != 2 || p.Segments[0].Text != "This is a sentence really." {
		t.Errorf("%d %+v", n, p.Segments)
	}
}

func TestFitPlans(t *testing.T) {
	cases := []struct {
		in   FitIn
		want Verdict
		cut  bool
	}{
		{FitIn{Rendered: 2, Slot: 2.2, Timing: TimingLipSync}, VerdictOK, false},
		{FitIn{Rendered: 2.6, Slot: 2, Timing: TimingLipSync}, VerdictTight, false},
		{FitIn{Rendered: 4, Slot: 2, Timing: TimingLipSync}, VerdictTrimmed, true},
		{FitIn{Rendered: 2.5, Slot: 2, Room: 1, Timing: TimingSmart}, VerdictOK, false},
		{FitIn{Rendered: 4, Slot: 2, Room: 0.5, Timing: TimingSmart}, VerdictOverflow, false},
		{FitIn{Rendered: 3, Slot: 2, Room: 0.5, Timing: TimingConcise}, VerdictOverflow, false},
		{FitIn{Rendered: 4, Slot: 2, Room: 0.5, Timing: TimingStretchVideo}, VerdictVideo, false},
	}
	for _, c := range cases {
		got := Plan(c.in)
		if got.Verdict != c.want || got.Cut != c.cut {
			t.Errorf("%+v: %+v", c.in, got)
		}
		if c.in.Timing == TimingLipSync && got.Length > c.in.Slot+0.01 {
			t.Errorf("lip sync longer than its slot: %+v", got)
		}
	}
	short := Plan(FitIn{Rendered: 1, Slot: 2, Timing: TimingLipSync})
	if short.Ratio != SlowestLipSync {
		t.Errorf("short line ratio %v", short.Ratio)
	}
	segs := sample().Segments
	if r := Room(segs, 0, 20); math.Abs(r-0.4) > 1e-9 {
		t.Errorf("room %v", r)
	}
	if r := Room(segs, 2, 20); r != 10.5 {
		t.Errorf("room at end %v", r)
	}
}

func TestImportSubtitlesKeepsSpeakers(t *testing.T) {
	p := sample()
	srt := "1\n00:00:00,600 --> 00:00:02,900\nHi there.\n\n2\n00:00:03,500 --> 00:00:05,900\nFine, thanks.\n\nbroken block\n\n3\n00:00:04,000 --> 00:00:05,000\nInside the one before\n\n4\n00:00:19,000 --> 00:00:25,000\nPast the end\n"
	st := p.ImportSubtitles(srt, 20)
	if st.Imported != 3 || st.Dropped != 1 || st.Clamped != 1 || st.Malformed != 1 {
		t.Errorf("stats %+v", st)
	}
	if p.Segments[0].Speaker != "S1" || p.Segments[1].Speaker != "S2" || p.Segments[2].End != 20 {
		t.Errorf("segments %+v", p.Segments)
	}
	vtt := "WEBVTT\n\n00:01.000 --> 00:02.000\n<v Ana>Hola\n\n00:02.500 --> 00:03.000\n<v Ben>Qué tal\n"
	p.ImportSubtitles(vtt, 0)
	if p.SpeakerName(p.Segments[0].Speaker) != "Ana" || p.SpeakerName(p.Segments[1].Speaker) != "Ben" {
		t.Errorf("voices %+v %v", p.Segments, p.Names)
	}
}

func TestPasteModes(t *testing.T) {
	p := sample()
	mode, e := ParsePaste("1. Hola.\n2) Estoy bien.\n[3] Me alegro.\n")
	if mode != PasteNumbered || len(e) != 3 || e[2].Number != 3 {
		t.Fatalf("%v %+v", mode, e)
	}
	pv := p.MatchPaste(mode, e, "es")
	if pv.Matched != 3 || pv.Unused != 0 || pv.Rows[1].After != "Estoy bien." {
		t.Errorf("%+v", pv)
	}
	if n := p.ApplyPaste(pv, "es"); n != 3 || p.TextFor(p.Segments[2], "es") != "Me alegro." {
		t.Errorf("applied %d", n)
	}
	mode, e = ParsePaste("Uno\n\nDos\nTres\nCuatro\n")
	pv = p.MatchPaste(mode, e, "es")
	if mode != PastePlain || pv.Matched != 3 || pv.Unused != 1 {
		t.Errorf("%v %+v", mode, pv)
	}
	mode, e = ParsePaste("1\n00:00:00,500 --> 00:00:02,000\nHola\n\n2\n00:00:02,000 --> 00:00:03,000\nqué tal\n\n3\n00:00:03,500 --> 00:00:05,500\nBien\n")
	pv = p.MatchPaste(mode, e, "es")
	if mode != PasteTimed || pv.Rows[0].After != "Hola qué tal" || pv.Rows[1].After != "Bien" || pv.Rows[2].After != "" {
		t.Errorf("%v %+v", mode, pv.Rows)
	}
}

func TestSubtitleExports(t *testing.T) {
	p := sample()
	p.SetText("s1", "es", "Hola. ¿Cómo estás hoy?")
	p.SetText("s2", "es", "Bien, gracias.")
	srt, err := p.Subtitles(SubtitleOptions{Format: SubSRT, Lang: "es", Dual: true})
	if err != nil || !strings.Contains(string(srt), "Hola. ¿Cómo estás hoy?\nHello there. How are you today?") {
		t.Errorf("%s %v", srt, err)
	}
	if strings.Contains(string(srt), "Good to hear") {
		t.Error("untranslated line exported")
	}
	ass, err := p.Subtitles(SubtitleOptions{Format: SubASS, Lang: "es"})
	if err != nil || !strings.Contains(string(ass), `{\kf`) {
		t.Errorf("karaoke: %v %s", err, ass)
	}
	vtt, _ := p.Subtitles(SubtitleOptions{Format: SubVTT, Split: true})
	if !strings.HasPrefix(string(vtt), "WEBVTT") {
		t.Error(string(vtt))
	}
}

func TestFingerprintsAndStale(t *testing.T) {
	p := sample()
	p.SetText("s1", "es", "Hola")
	v := VoiceSpec{Auto: true}
	k1 := p.Fingerprint(p.Segments[0], "es", v)
	if k1 != p.Fingerprint(p.Segments[0], "es", v) {
		t.Fatal("not stable")
	}
	p.PutTrack(Track{Lang: "es", Lines: map[string]Render{"s1": {Key: k1}}})
	keys := map[string]string{"s1": k1}
	if s := p.Stale("es", keys); len(s) != 0 {
		t.Errorf("stale %v", s)
	}
	p.Segments[0].Volume = 0.5 // the mix applies volume
	if p.Fingerprint(p.Segments[0], "es", v) != k1 {
		t.Error("volume changed the fingerprint")
	}
	p.SetText("s1", "es", "Hola, amigo")
	if p.Fingerprint(p.Segments[0], "es", v) == k1 {
		t.Error("text did not change the fingerprint")
	}
	if p.Fingerprint(p.Segments[0], "es", VoiceSpec{Voice: "x"}) == p.Fingerprint(p.Segments[0], "es", v) {
		t.Error("voice did not change it")
	}
	// A line kept in the source language speaks the source.
	p.Segments[2].Lang = "en"
	if p.TextFor(p.Segments[2], "es") != "Good to hear." {
		t.Error("kept line")
	}
	if got := p.Untranslated("es"); !slices.Equal(got, []string{"s2"}) {
		t.Errorf("untranslated %v", got)
	}
}

func TestDrift(t *testing.T) {
	if d := Drift("Hello there, how are you?", "hello there how are you"); d != 0 {
		t.Errorf("same words %v", d)
	}
	if d := Drift("Hello there, how are you?", "yellow hair how are you"); d != 0.4 {
		t.Errorf("two of five %v", d)
	}
	if d := Drift("你好，世界", "你好世界"); d != 0 {
		t.Errorf("cjk %v", d)
	}
	if d := Drift("你好世界", "你们世界"); d != 0.25 {
		t.Errorf("cjk one %v", d)
	}
	// Characters, spaces left out (#93).
	if d := CharDrift("habe ihm hinterhergeschaut.", "Habe ihm hinterher geschaut"); d != 0 {
		t.Errorf("split word %v", d)
	}
	if d := CharDrift("eine Katze", "meine Katze"); d != 0.111 {
		t.Errorf("one letter of nine %v", d)
	}
	if d := CharDrift("你好世界", "你们世界"); d != 0.25 || CharDrift("", "") != 0 || CharDrift("", "x") != 1 {
		t.Errorf("cjk one %v", d)
	}
	q := CheckLine("s1", "a b c d", "x y z", 2, 2)
	if !q.Flagged || q.SyncRatio != 1 {
		t.Errorf("%+v", q)
	}
}

func TestStory(t *testing.T) {
	p := sample()
	p.Names = map[string]string{"S1": "Ana"}
	p.Cast = map[string]string{"S1": "01VOICE", "S2": "design:old man"}
	p.Merge("s1", true)
	st := p.ToStory("")
	if len(st.Characters) != 2 || st.Characters[0].Voice != "01VOICE" || st.Characters[1].Voice != "" {
		t.Errorf("%+v", st.Characters)
	}
	if len(st.Lines) != 3 || st.Lines[1].Character != "S2" || st.Lines[1].Text != "I am fine, thank you." {
		t.Errorf("%+v", st.Lines)
	}
}

func TestCheckURL(t *testing.T) {
	lookup := func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == "evil.example" {
			return []netip.Addr{netip.MustParseAddr("192.168.1.5")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("142.250.1.1")}, nil
	}
	ok := []string{"https://www.youtube.com/watch?v=abc&list=PL1", "https://youtu.be/abc", "http://vimeo.com/123"}
	for _, u := range ok {
		if _, err := CheckURL(context.Background(), u, lookup); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	bad := map[string]string{
		"file:///etc/passwd":                        CodeURLScheme,
		"http://localhost:8080/v.mp4":               CodeURLPrivate,
		"http://192.168.0.2/v.mp4":                  CodeURLPrivate,
		"http://[::1]/v.mp4":                        CodeURLPrivate,
		"http://nas/v.mp4":                          CodeURLPrivate,
		"https://evil.example/v.mp4":                CodeURLPrivate,
		"https://www.youtube.com/playlist?list=PL1": CodeURLPlaylist,
		"https://www.youtube.com/watch?list=PL1":    CodeURLPlaylist,
		"https://cdn.example.com/master.m3u8":       CodeURLManifest,
		"https://www.youtube.com/live/abc":          CodeURLLive,
		"not a url":                                 CodeURLInvalid,
	}
	for u, code := range bad {
		_, err := CheckURL(context.Background(), u, lookup)
		ue, _ := err.(*URLError)
		if ue == nil || ue.Code != code {
			t.Errorf("%s: %v, want %s", u, err, code)
		}
	}
}

func TestWatcher(t *testing.T) {
	t0 := time.Unix(100, 0)
	w := NewWatcher("/in", nil, map[string]FileState{"old.mp4": {1, t0}})
	if got := w.Scan(map[string]FileState{"old.mp4": {1, t0}, "new.mp4": {10, t0}, "notes.txt": {5, t0}}); len(got) != 0 {
		t.Errorf("first sighting %v", got)
	}
	// Still growing.
	if got := w.Scan(map[string]FileState{"new.mp4": {20, t0.Add(time.Second)}}); len(got) != 0 {
		t.Errorf("growing %v", got)
	}
	if got := w.Scan(map[string]FileState{"new.mp4": {20, t0.Add(time.Second)}}); !slices.Equal(got, []string{"new.mp4"}) {
		t.Errorf("stable %v", got)
	}
	if got := w.Scan(map[string]FileState{"new.mp4": {20, t0.Add(time.Second)}}); len(got) != 0 {
		t.Errorf("again %v", got)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.mov"), []byte("x"), 0o644)
	os.Mkdir(filepath.Join(dir, "sub"), 0o755)
	l, err := List(dir)
	if err != nil || len(l) != 1 {
		t.Errorf("%v %v", l, err)
	}
}

func tone(rate, ch int, seconds, amp float64) dsp.Buffer {
	n := int(float64(rate) * seconds)
	b := dsp.New(rate, ch, n)
	for i := range n {
		v := float32(amp * math.Sin(2*math.Pi*200*float64(i)/float64(rate)))
		for c := range ch {
			b.Samples[i*ch+c] = v
		}
	}
	return b
}

func TestTrackMixerAndWAVSource(t *testing.T) {
	dir := t.TempDir()
	bgPath := filepath.Join(dir, "bg.wav")
	if err := wav.WriteFile(bgPath, tone(44100, 2, 4, 0.1), wav.PCM16, wav.Options{}); err != nil {
		t.Fatal(err)
	}
	bg, err := OpenWAV(bgPath, 48000, 2)
	if err != nil {
		t.Fatal(err)
	}
	line := tone(24000, 1, 1, 0.5)
	lines := []MixLine{{ID: "s1", Start: 1, Length: 1, Volume: 1, Load: func() (dsp.Buffer, error) { return line, nil }}}
	m := NewTrackMixer(lines, MixOptions{Rate: 48000, Channels: 2, Seconds: 4, Background: bg, DuckDB: DuckOriginalDB})
	defer m.Close()
	if m.Len() != 192000 {
		t.Fatalf("len %d", m.Len())
	}
	out := make([]float32, 0, 192000*2)
	buf := make([]float32, 4096)
	for {
		n, err := m.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			break
		}
	}
	if len(out) != 192000*2 {
		t.Fatalf("%d samples", len(out))
	}
	rmsAt := func(sec float64) float64 {
		a := int(sec * 48000)
		var e float64
		for i := a; i < a+4800; i++ {
			e += float64(out[2*i]) * float64(out[2*i])
		}
		return math.Sqrt(e / 4800)
	}
	if r := rmsAt(0.3); math.Abs(r-0.1/math.Sqrt2) > 0.01 {
		t.Errorf("background alone %.3f", r)
	}
	if r := rmsAt(1.5); r < 0.3 {
		t.Errorf("line %.3f", r)
	}
	if r := rmsAt(3.5); math.Abs(r-0.1/math.Sqrt2) > 0.01 {
		t.Errorf("background after %.3f", r)
	}
	// Seek back into the line.
	m.SetPosition(48000 * 3 / 2)
	n, _ := m.Read(buf)
	var e float64
	for _, v := range buf[:n] {
		e += float64(v) * float64(v)
	}
	if math.Sqrt(e/float64(n)) < 0.3 {
		t.Error("seek lost the line")
	}
}

func TestPeaks(t *testing.T) {
	b := NewPeakBuilder(16000, 1)
	b.Write(tone(16000, 1, 1, 0.25).Samples)
	b.Write(make([]float32, 16000))
	p := b.Peaks()
	if p.Seconds() != 2 || len(p.Levels) < 5 {
		t.Fatalf("%v %d", p.Seconds(), len(p.Levels))
	}
	bars := p.Bars(0, 2, 4)
	if bars[0] < 0.45 || bars[3] != 0 {
		t.Errorf("bars %v", bars)
	}
	path := filepath.Join(t.TempDir(), "p.peaks")
	if err := WritePeaks(path, p); err != nil {
		t.Fatal(err)
	}
	q, err := ReadPeaks(path)
	if err != nil || !bytes.Equal(q.Levels[0], p.Levels[0]) || len(q.Levels) != len(p.Levels) {
		t.Errorf("read back %v", err)
	}
}

func TestReferencesAndSpeakers(t *testing.T) {
	p := New("d", time.Now())
	// S1 talks 0–12 s in three lines, S2 has one 1 s line.
	words := func(start, end float64, texts ...string) []Word {
		var w []Word
		step := (end - start) / float64(len(texts))
		for i, x := range texts {
			w = append(w, Word{Text: x, Start: start + step*float64(i), End: start + step*float64(i+1)})
		}
		return w
	}
	s := p.NewSegment(0, 4, "", "one two three four five six")
	s.Words = words(0, 4, "one", "two", "three", "four", "five", "six")
	p.Segments = append(p.Segments, s, p.NewSegment(4.2, 8, "", "seven eight"), p.NewSegment(8.3, 12, "", "nine ten"), p.NewSegment(13, 14, "", "hey"))
	turns := []Turn{{Start: 0, End: 2, Speaker: "B"}, {Start: 2, End: 12, Speaker: "A"}, {Start: 12.5, End: 14.5, Speaker: "C"}}
	p.AssignSpeakers(turns)
	if len(p.Segments) != 5 {
		t.Fatalf("split by turns: %+v", p.Segments)
	}
	if p.Segments[0].Speaker != "S1" || p.Segments[0].Text != "one two three" || p.Segments[1].Speaker != "S2" || p.Segments[4].Speaker != "S3" {
		t.Errorf("%+v", p.Segments)
	}
	audio := tone(16000, 1, 15, 0.1)
	refs, err := PickReferences(p.Segments, BufferAudio{audio})
	if err != nil {
		t.Fatal(err)
	}
	r, ok := refs["S2"]
	if !ok || r.Start != 2 || r.End != 12 || r.Score <= 0 {
		t.Errorf("S2 ref %+v", r)
	}
	if _, ok := refs["S3"]; ok {
		t.Error("S3 has too little speech for a reference")
	}
}
