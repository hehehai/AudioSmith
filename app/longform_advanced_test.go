package main

import (
	"testing"

	"github.com/hehehai/AudioSmith/internal/services"
)

// TestStoryHidesWhatTheModelDoesNotHear: on a model that says lines as
// written (Kokoro) a story shows no persona, scene, AI director or line
// direction; on one without a speed (CustomVoice) a line has no speed.
func TestStoryHidesWhatTheModelDoesNotHear(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.SetSize(1100, 1600)
	a.navigate("/stories")
	p := a.lfStories
	kittenDoc(&p.doc)
	p.open["cast"], p.open["scene"] = true, true
	p.sel = 1
	tt.Frame()
	for _, s := range []string{"Persona of 孩子", "Scene", "AI director", "Intensity"} {
		if !tt.HasText(s) {
			t.Errorf("CustomVoice lacks %q", s)
		}
	}
	if tt.HasText("Speed 1.00×") {
		t.Error("CustomVoice takes no speed: a line's speed shows")
	}
	p.doc.Model, p.doc.Voice = kokoro, "speaker:af_heart"
	tt.Frame()
	for _, s := range []string{"Persona of 孩子", "Scene", "AI director", "Intensity"} {
		if tt.HasText(s) {
			t.Errorf("Kokoro shows %q", s)
		}
	}
	if !tt.HasText("Line by line") || !tt.HasText("Pacing") {
		t.Errorf("Kokoro's performance: %q", tt.Texts())
	}
}

// TestLongformAdvanced: Advanced counts the production changes, Restore
// recommended clears them but keeps the pacing; a story's M4B book and
// its details are there too.
func TestLongformAdvanced(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.SetSize(1100, 1800)
	a.lfEncode = func() bool { return true }
	a.navigate("/stories")
	p := a.lfStories
	kittenDoc(&p.doc)
	p.doc.Production.Pacing = services.PacingRelaxed
	p.doc.Production.Seed, p.doc.Production.VaryRepeats = 9, true
	a.setAdvancedOpen("story", true)
	tt.Frame()
	if !tt.HasText("2 changes") || tt.HasText("Author") {
		t.Fatalf("advanced: %q", tt.Texts())
	}
	tt.Click("M4B (chapters)")
	tt.Frame()
	if p.doc.Format != services.FormatM4B || !tt.HasText("Author") {
		t.Errorf("M4B: %q %q", p.doc.Format, tt.Texts())
	}
	tt.Click("Restore recommended")
	tt.Frame()
	if p.doc.Production.Seed != 0 || p.doc.Production.VaryRepeats || p.doc.Production.Pacing != services.PacingRelaxed {
		t.Errorf("restored %+v", p.doc.Production)
	}
}

// TestLongformProjectMenu: the header's Project menu saves under the
// title, and opens the Projects page.
func TestLongformProjectMenu(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.navigate("/stories")
	p := a.lfStories
	kittenDoc(&p.doc)
	tt.Frame()
	if err := tt.Click("More actions"); err != nil { // the old Project menu's items are in ⋯ (0.5 F5)
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Save"); err != nil {
		t.Fatalf("%v: %q", err, tt.Menu())
	}
	tt.Frame()
	if p.project == "" || len(p.projects) == 0 || p.projects[0].Title != "我想领养一只小猫" {
		t.Fatalf("saved %q %+v", p.project, p.projects)
	}
	tt.Click("More actions")
	if err := tt.ChooseMenuItem("All projects…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.router.Path() != "/projects" {
		t.Errorf("all projects: %s", a.router.Path())
	}
}
