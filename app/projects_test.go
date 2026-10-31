package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/store"
)

func TestProjectsLibrary(t *testing.T) {
	a, eng, tt := newTestApp(t)
	useLfSynth(a)
	tt.SetSize(1300, 1000)
	ctx := context.Background()
	at := time.Date(2026, 10, 13, 9, 0, 0, 0, time.Local)
	addTake(t, a, core.Take{ID: "T1", CreatedAt: at, Text: "The lighthouse keeper spoke softly.", Model: kokoro, Voice: "af_heart", SampleRate: 24000, AudioSeconds: 2}, 1)
	a.lib.svc.Voices.Save(ctx, core.Voice{Name: "Mara", Kind: core.VoiceCloned})

	// A story saved as a project, rendered.
	p := a.lfStories
	a.navigate("/stories")
	tt.Frame()
	tt.Click("Load sample")
	p.saveName = "Night Ferry"
	a.saveLf(p, false)
	if p.project == "" || p.unsaved() {
		t.Fatalf("saved %q, unsaved %v", p.project, p.unsaved())
	}
	a.startLf(p, "")
	playLongform(t, a, eng)

	a.navigate("/projects")
	tt.Frame()
	tt.Frame()
	s := &a.projects
	for _, want := range []string{"Night Ferry", "The lighthouse keeper spoke softly.", "Mara", "Story render"} {
		if !hasAnyText(tt.Texts(), want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	if s.counts[store.LibStory] != 2 || s.counts[store.LibVoice] != 1 || s.counts[store.LibTake] != 1 || s.counts[""] != 4 {
		t.Errorf("counts %v", s.counts)
	}
	// Filter, search.
	s.category = 6 // History (the sidebar has a "History" too)
	tt.Frame()
	tt.Frame()
	if len(s.items) != 1 || s.items[0].Source != "take" {
		t.Fatalf("history filter %+v", s.items)
	}
	s.category = 0
	s.query = "ferry"
	tt.Frame()
	tt.Frame()
	if len(s.items) != 2 {
		t.Fatalf("search %+v", s.items)
	}
	// How it was made.
	var render store.LibraryItem
	for _, it := range s.items {
		if it.Source == "render" {
			render = it
		}
	}
	s.details[libKey(render)] = true
	tt.Frame()
	for _, want := range []string{"How it was made", "Fast English voices", "1.00×", "2 chapters · 7 lines", "line gap 400 ms"} {
		if !hasAnyText(tt.Texts(), want) {
			t.Errorf("details: no %q in %q", want, tt.Texts())
		}
	}
	// Rename inline.
	var proj store.LibraryItem
	for _, it := range s.items {
		if it.Source == "project" {
			proj = it
		}
	}
	s.renaming, s.renameText = libKey(proj), "The Ferry Home"
	tt.Frame()
	if err := tt.Click("Save name"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got, _ := a.lib.svc.Projects.Get(ctx, proj.ID); got.Title != "The Ferry Home" || p.doc.Title != "The Ferry Home" {
		t.Errorf("renamed: %q, page %q", got.Title, p.doc.Title)
	}
	// Open over unsaved changes asks first.
	p.doc.Lines[1].Text = "Changed."
	a.askOpenProject(proj.ID, core.ProjectStory, "The Ferry Home")
	if p.project != proj.ID {
		t.Fatal("it is the open project: nothing to ask")
	}
	a.newLf(p)
	p.doc.Lines = []services.StoryLine{{ID: "x", Text: "Draft."}}
	a.askOpenProject(proj.ID, core.ProjectStory, "The Ferry Home")
	tt.Frame()
	if !p.confirmOpen || !hasAnyText(tt.Texts(), "Open “The Ferry Home”?") {
		t.Fatalf("no confirmation: %q", tt.Texts())
	}
	if err := tt.Click("Open"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if p.project != proj.ID || a.currentPage() != "stories" || len(p.doc.Lines) < 8 {
		t.Errorf("opened %q on %s", p.project, a.currentPage())
	}

	// Bulk delete: the take and the export-free project.
	a.navigate("/projects")
	s.query = ""
	tt.Frame()
	tt.Frame()
	for _, it := range s.items {
		if it.Source == "take" || it.Source == "voice" {
			s.selected[libKey(it)] = true
		}
	}
	tt.Frame()
	if err := tt.Click("Delete selected (2)"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !hasAnyText(tt.Texts(), "Delete 2 items?") {
		t.Fatalf("%q", tt.Texts())
	}
	if err := tt.Click("Delete 2 items"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if s.deleteOpen || len(s.selected) != 0 || a.findTake("T1") != nil || s.counts[""] != 2 {
		t.Errorf("deleted: open %v, selected %v, counts %v", s.deleteOpen, s.selected, s.counts)
	}
	_ = strings.Contains
}

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
