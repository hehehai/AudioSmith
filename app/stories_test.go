package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/services"
)

const screenplay = "# The Ferry\n\nThe fog rolled in over the harbour.\n\nINES: Is this the boat to Harrow Point?\n\nTOMAS: It is the only boat.\n\nThe lights went out one by one."

func TestStoriesAutoCastRenderAndOutputs(t *testing.T) {
	a, eng, tt := newTestApp(t)
	synth := useLfSynth(a)
	a.navigate("/stories")
	tt.Frame()
	if !tt.HasText("New story") {
		t.Fatalf("empty state: %q", tt.Texts())
	}
	p := a.lfStories
	p.doc.ImportText = screenplay
	tt.Frame()
	if err := tt.Click("Auto-cast"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(p.doc.Cast) != 2 || p.doc.Cast[0].Name != "Ines" || p.doc.Cast[1].Name != "Tomas" {
		t.Fatalf("cast %+v", p.doc.Cast)
	}
	if p.doc.Cast[0].Voice == "" || p.doc.Cast[0].Voice == p.doc.Cast[1].Voice || p.doc.Voice == "" {
		t.Errorf("voices round-robin: %+v, default %q", p.doc.Cast, p.doc.Voice)
	}
	if len(p.doc.Lines) != 5 || !p.doc.Lines[0].Chapter || p.doc.Lines[2].Character != p.doc.Cast[0].ID || p.doc.ImportText != "" {
		t.Fatalf("lines %+v", p.doc.Lines)
	}
	if !tt.HasText("Ines") || !tt.HasText("Line 4") {
		t.Errorf("the line list: %q", tt.Texts())
	}

	// Generate: a chapter job, then the book.
	if err := tt.Click("Generate"); err != nil {
		t.Fatalf("%v: %q", err, a.lfBlocker(p))
	}
	tt.Frame()
	jl := lfJobs(eng)
	if len(jl) != 2 || jl[0].Spec.Kind != services.KindChapter || jl[1].Spec.Kind != services.KindAssemble || jl[0].Spec.Lane != "gpu" || jl[1].Spec.Lane != "cpu" {
		t.Fatalf("jobs %+v", jl)
	}
	cp := chapterParams(t, jl[0])
	if cp.Title != "The Ferry" || len(cp.Lines) != 4 || len(cp.Voices) != 3 {
		t.Fatalf("chapter %+v", cp)
	}
	if !tt.HasText("Stop") || a.lfBlocker(a.lfBook) == "" {
		t.Errorf("rendering: %q; the audiobook page waits: %q", tt.Texts(), a.lfBlocker(a.lfBook))
	}
	playLongform(t, a, eng)
	tt.Frame()
	r := a.lfRenders[p.render]
	if r == nil || r.rec.State != core.RenderDone || r.body.Output == nil {
		t.Fatalf("render %+v", r)
	}
	if len(synth.texts) != 4 {
		t.Errorf("synthesized %q", synth.texts)
	}
	if !tt.HasText("Story ready") {
		t.Errorf("outputs: %q", tt.Texts())
	}
	tt.SetSize(2400, 1000) // the outputs in view, clear of the toasts
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if m := strings.Join(tt.Menu(), "|"); !strings.Contains(m, "Whole book") || !strings.Contains(m, "Chapter times (.txt)") {
		t.Errorf("the Save menu: %q", m)
	}
	tt.CloseMenu()
	tt.SetSize(1100, 720)
	// Save the cue sheet and the chapters (as the dialogs would).
	dir := t.TempDir()
	a.writeOutput(a.lib.svc.Library, r.rec, "cues", r.body.Output.Cues, filepath.Join(dir, "cues.txt"), "txt", 0)
	a.writeChapters(a.lib.svc.Library, r.rec, r.body.Output.Chapters, dir)
	cues, err := os.ReadFile(filepath.Join(dir, "cues.txt"))
	if err != nil || !strings.HasPrefix(string(cues), "00:00:00\tThe Ferry") {
		t.Errorf("cues %q %v", cues, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "01 The Ferry.wav")); err != nil {
		t.Errorf("chapter: %v", err)
	}
	if n, _ := a.lib.svc.Library.Counts(t.Context(), ""); n["export"] != 2 {
		t.Errorf("exports recorded: %v", n)
	}

	// The same story again: every chunk from the cache.
	synth.texts = nil
	a.startLf(p, "")
	playLongform(t, a, eng)
	tt.Frame()
	if len(synth.texts) != 0 || !tt.HasText("Reused 1 already-rendered chapter.") {
		t.Errorf("again: %q %q", synth.texts, tt.Texts())
	}
}

func TestStoryLines(t *testing.T) {
	a, eng, tt := newTestApp(t)
	useLfSynth(a)
	tt.SetSize(1100, 1800) // every line in view
	a.navigate("/stories")
	p := a.lfStories
	tt.Frame()
	if err := tt.Click("Load sample"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	n := len(p.doc.Lines)
	if n < 8 || len(p.doc.Cast) != 2 || p.doc.Title == "" {
		t.Fatalf("sample %d lines, cast %+v", n, p.doc.Cast)
	}
	// Options of line 2: a tone, a speed, a pause after.
	p.sel = 2
	tt.Frame()
	tt.Click("Insert")
	if err := tt.ChooseMenuItem("Laughter"); err != nil {
		t.Fatalf("%v %q", err, tt.Menu())
	}
	if !strings.HasSuffix(p.doc.Lines[2].Text, "[laughter]") {
		t.Errorf("tone: %q", p.doc.Lines[2].Text)
	}
	tt.Click("Insert")
	if err := tt.ChooseMenuItem("Insert pause"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(p.doc.Lines[2].Text, "[pause 500ms]") {
		t.Errorf("pause: %q", p.doc.Lines[2].Text)
	}
	// Move it up, delete it, add a line after the selection.
	id := p.doc.Lines[2].ID
	a.moveLine(p, 2, -1)
	if p.doc.Lines[1].ID != id || p.sel != 1 {
		t.Errorf("moved: sel %d", p.sel)
	}
	a.deleteLine(p, 1)
	if len(p.doc.Lines) != n-1 || p.sel != -1 {
		t.Errorf("deleted: %d lines, sel %d", len(p.doc.Lines), p.sel)
	}
	p.sel = 0
	a.addLine(p, false)
	if len(p.doc.Lines) != n || p.sel != 1 || p.doc.Lines[1].Text != "" {
		t.Errorf("added: sel %d", p.sel)
	}
	a.deleteLine(p, 1)
	tt.Frame()

	// Preview a line: an audition of that line only.
	line := p.doc.Lines[2]
	a.startLf(p, "line:"+line.ID)
	tt.Frame()
	jl := lfJobs(eng)
	if len(jl) != 1 || jl[0].Spec.Priority <= 0 {
		t.Fatalf("audition jobs %+v", jl)
	}
	if cp := chapterParams(t, jl[0]); len(cp.Lines) != 1 || cp.Lines[0].ID != line.ID || cp.Loudness != "" {
		t.Fatalf("audition %+v", cp)
	}
	playLongform(t, a, eng)
	tt.Frame()
	if st, r := a.auditionState(p, "line:"+line.ID); st != "done" || a.pl.Current() != "audition:"+r.rec.ID {
		t.Errorf("audition %s, playing %q", st, a.pl.Current())
	}

	// Removing a character gives its lines to the narrator.
	ines := p.doc.Cast[0].ID
	a.removeCast(p, ines)
	for _, l := range p.doc.Lines {
		if l.Character == ines {
			t.Fatal("a line kept a removed character")
		}
	}
	// A line without any voice blocks Generate, and says which.
	p.doc.Voice = ""
	p.doc.Cast = nil
	for i := range p.doc.Lines {
		p.doc.Lines[i].Character = ""
	}
	tt.Frame()
	if msg := a.lfBlocker(p); !strings.Contains(msg, "has no voice") {
		t.Errorf("blocker %q", msg)
	}
}

func TestStorySplitAndImport(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.navigate("/stories")
	p := a.lfStories
	p.doc.ImportText = "# One\n\nFirst sentence. Second sentence.\n\nA paragraph."
	p.doc.SplitBy = "sentences"
	tt.Frame()
	if err := tt.Click("Split into lines"); err != nil {
		t.Fatal(err)
	}
	if len(p.doc.Lines) != 4 || !p.doc.Lines[0].Chapter || p.doc.Lines[0].Text != "One" || p.doc.Lines[1].Text != "First sentence." {
		t.Fatalf("lines %+v", p.doc.Lines)
	}
	// An SRT file comes in without its timings.
	f := filepath.Join(t.TempDir(), "subs.srt")
	os.WriteFile(f, []byte("1\n00:00:01,000 --> 00:00:02,000\nHello there.\n\n2\n00:00:03,000 --> 00:00:04,000\nGeneral Kenobi.\n"), 0o644)
	a.importStoryFrom(p, f)
	if !strings.Contains(p.doc.ImportText, "Hello there.") || strings.Contains(p.doc.ImportText, "-->") {
		t.Errorf("imported %q", p.doc.ImportText)
	}
}

// The render bar's Clear empties the lines at once (the cast stays), and
// Undo brings them back.
func TestStoriesClearWithUndo(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.navigate("/stories")
	tt.Frame()
	p := a.lfStories
	p.doc.ImportText = screenplay
	tt.Frame()
	if err := tt.Click("Auto-cast"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	lines, cast := len(p.doc.Lines), len(p.doc.Cast)
	if lines == 0 {
		t.Fatal("no lines")
	}
	if err := tt.Click("Clear"); err != nil {
		t.Fatalf("%v: %q", err, tt.Texts())
	}
	tt.Frame()
	if len(p.doc.Lines) != 0 || len(p.doc.Cast) != cast {
		t.Fatalf("after Clear: %d lines, %d cast", len(p.doc.Lines), len(p.doc.Cast))
	}
	if err := tt.Click("Undo"); err != nil {
		t.Fatalf("%v: %q", err, tt.Texts())
	}
	tt.Frame()
	if len(p.doc.Lines) != lines {
		t.Fatalf("after Undo: %d lines, want %d", len(p.doc.Lines), lines)
	}
}
