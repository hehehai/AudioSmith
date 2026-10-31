package main

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/services"
)

// The voice editor's "where used" opens the project.
func TestVoiceUsageLinksToProjects(t *testing.T) {
	a, _, _ := newTestApp(t)
	tt := ui.NewTester(a.view, 1100, 1600) // the detail's usage below its reference and settings (#83)
	ctx := context.Background()
	v, err := a.lib.svc.Voices.Save(ctx, core.Voice{Name: "Mara", Kind: core.VoiceDesigned, Design: "a warm voice"})
	if err != nil {
		t.Fatal(err)
	}
	d := services.NewLongformDoc(core.ProjectStory, kokoro)
	d.Title = "With Mara"
	d.Lines = []services.StoryLine{{ID: "l1", Text: "Hello.", Voice: v.ID}}
	pr, err := a.lib.svc.Library.SaveDoc(ctx, core.Project{}, d)
	if err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	a.openVoiceEditor(v.ID)
	tt.Frame()
	tt.Frame()
	if err := tt.Click("With Mara · 1 segment"); err != nil {
		t.Fatalf("%v: %q", err, tt.Texts())
	}
	tt.Frame()
	if a.lfStories.project != pr.ID || a.currentPage() != "stories" {
		t.Errorf("opened %q on %s", a.lfStories.project, a.currentPage())
	}
	// Use in Stories adds the voice to the cast.
	a.useVoiceInStories(&v)
	if n := len(a.lfStories.doc.Cast); n != 1 || a.lfStories.doc.Cast[0].Voice != v.ID {
		t.Errorf("cast %+v", a.lfStories.doc.Cast)
	}
}

// The gallery's "Use in Stories" saves the archetype and casts it.
func TestGalleryUseInStories(t *testing.T) {
	a, _ := newVoiceTestApp(t)
	acceptLicences(t, a)
	tt := ui.NewTester(a.view, 1200, 1800)
	a.navigate("/gallery")
	a.gallery.text = "Osmanthus" // a designed voice
	tt.Frame()
	tt.Click("More actions")
	if err := tt.ChooseMenuItem("Use in Stories"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.voices) != 1 || a.router.Path() != "/stories" {
		t.Fatalf("%d voices, %s", len(a.voices), a.router.Path())
	}
	p := a.lfStories
	// A curated voice comes with its reference: Stories clones it on its
	// own recommended model (before #94, a gallery voice needed a design
	// model).
	if len(p.doc.Cast) != 1 || p.doc.Cast[0].Voice != a.voices[0].ID || p.doc.Model != entry(t, "qwen3-tts-1.7b-base-8bit").Repo {
		t.Errorf("cast %+v, model %s", p.doc.Cast, p.doc.Model)
	}
	// Narrate an audiobook, from the voice editor's menu.
	a.openVoiceEditor(a.voices[0].ID)
	tt.Frame()
	if err := tt.Click("More actions"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Narrate an audiobook"); err != nil {
		t.Fatal(err)
	}
	if a.lfBook.doc.Voice != a.voices[0].ID || a.router.Path() != "/audiobook" {
		t.Errorf("narrator %q on %s", a.lfBook.doc.Voice, a.router.Path())
	}
}
