package main

import (
	"testing"

	"github.com/hehehai/AudioSmith/internal/settings"
)

// TestModelPickerFitsWindow (thread #84): the Stories picker with "Show
// all models" open stays inside the window, at 1100×720 and at
// 820×520 with 125 % text, and its last model scrolls fully into view;
// the search field stays on top; a shorter window shortens it again.
func TestModelPickerFitsWindow(t *testing.T) {
	for _, sz := range []struct {
		w, h  int
		scale float64
	}{{1100, 720, 1}, {820, 520, 1.25}, {1100, 900, 1}} {
		a, _, tt := newTestApp(t)
		a.updateSettings(func(s *settings.Settings) { s.Appearance.Scale = sz.scale })
		a.sys = &sysInfo{Memory: 24 << 30}
		tt.SetSize(sz.w, sz.h)
		a.navigate("/stories")
		p := a.lfStories
		kittenDoc(&p.doc)
		p.panel.set, p.panel.open = true, true
		tt.Frame()
		a.pickerOpen = map[string]bool{"lf-model": true}
		a.pickerAll = map[string]bool{"lf-model": true}
		for range 3 {
			tt.Frame()
		}
		h := float32(sz.h)
		find, ok := tt.Find("Search models")
		if !ok || find.Y < 0 || find.Y+find.H > h {
			t.Fatalf("%v: search field %v %v", sz, find, ok)
		}
		// Scroll the list to its end: the last model is fully in the window.
		last := "Voice design" // the last of "Show all models"
		tt.Scroll(find.X+find.W/2, find.Y+find.H+60, 0, 4000)
		for range 3 {
			tt.Frame()
		}
		r, ok := tt.Find(last)
		if !ok || r.Y < find.Y+find.H || r.Y+r.H > h-4 {
			t.Errorf("%v: %q at %v (%v), search at %v: out of reach", sz, last, r, ok, find)
		}
		if f2, _ := tt.Find("Search models"); f2 != find {
			t.Errorf("%v: the search field moved as the list scrolled: %v → %v", sz, find, f2)
		}
	}
}

// TestModelPickerDialogue (thread #84): the Stories picker lists the
// dialogue models in a section of their own; choosing one turns whole
// dialogue on with it (the Performance switch shows it) and keeps the
// story's model for narration, as the button says; Turn whole dialogue
// off goes back. A Mac with too little memory sees them greyed out with
// the reason; the Studio's and the audiobook's pickers have no section.
func TestModelPickerDialogue(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.SetSize(1100, 1000)
	a.sys = &sysInfo{Memory: 24 << 30}
	a.navigate("/stories")
	p := a.lfStories
	kittenDoc(&p.doc)
	d := &p.doc
	tt.Frame()
	a.pickerOpen = map[string]bool{"lf-model": true}
	tt.Frame()
	for _, s := range []string{"Whole dialogue (multi-speaker models)", "Dialogue · Light", "Dialogue · One take", "Needs 16 GB of memory · SoulX-Podcast 1.7B", "Needs 24 GB of memory · MOSS-TTSD v1.0"} {
		if !tt.HasText(s) {
			t.Errorf("no %q", s)
		}
	}
	if tt.HasText("Turn whole dialogue off") {
		t.Error("an off row with whole dialogue off")
	}
	if err := tt.Click("Dialogue · One take"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if !d.Production.Dialogue || d.Production.DialogueModel != entry(t, "moss-ttsd-v1.0-8bit").Repo || d.Model != customVoice06 || a.pickerOpen["lf-model"] || !p.open["scene"] {
		t.Fatalf("picked: on %v, %q, model %q, open %v, performance %v", d.Production.Dialogue, d.Production.DialogueModel, d.Model, a.pickerOpen["lf-model"], p.open["scene"])
	}
	for _, s := range []string{"Narration and fallback: Standard voices", "Dialogue model"} {
		if !tt.HasText(s) {
			t.Errorf("dialogue on: no %q", s)
		}
	}
	// A regular model now changes the narration, and whole dialogue stays.
	a.pickerOpen = map[string]bool{"lf-model": true}
	tt.Frame()
	if !tt.HasText("Narration and fallback · Recommended") || !tt.HasText("Turn whole dialogue off") {
		t.Errorf("dialogue on, picker: %q", tt.Texts())
	}
	if err := tt.Click("High-fidelity clone"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !d.Production.Dialogue || d.Model != entry(t, idCloneHiFi).Repo {
		t.Errorf("narration model: on %v, %q", d.Production.Dialogue, d.Model)
	}
	a.pickerOpen = map[string]bool{"lf-model": true}
	tt.Frame()
	if err := tt.Click("Turn whole dialogue off"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if d.Production.Dialogue || tt.HasText("Narration and fallback: High-fidelity clone") {
		t.Errorf("still on: %v", d.Production.Dialogue)
	}

	// 8 GB: greyed out, with the reason; a click does nothing.
	a.sys = &sysInfo{Memory: 8 << 30}
	a.pickerOpen = map[string]bool{"lf-model": true}
	tt.Frame()
	tt.Frame()
	if !tt.HasText("Needs 16 GB of memory; this Mac has 8 GB.") || !tt.HasText("Needs 24 GB of memory; this Mac has 8 GB.") {
		t.Errorf("8 GB: %q", tt.Texts())
	}
	_ = tt.Click("Dialogue · Light")
	tt.Frame()
	if d.Production.Dialogue {
		t.Error("a dialogue model too big for the Mac was turned on")
	}
	// A Japanese story: not for its language.
	a.sys = &sysInfo{Memory: 24 << 30}
	d.Language = "ja"
	tt.Frame()
	if !tt.HasText("For Chinese and English stories.") {
		t.Errorf("ja: %q", tt.Texts())
	}
	a.pickerOpen = nil
	d.Language = "zh"

	// The Studio's and the audiobook's pickers have no section.
	a.lfBook.opened = true // the editor
	for _, page := range []struct{ path, key string }{{"/studio", "model"}, {"/audiobook", "lf-model"}} {
		a.navigate(page.path)
		tt.Frame()
		a.pickerOpen = map[string]bool{page.key: true}
		tt.Frame()
		tt.Frame()
		if !tt.HasText("Search models") || tt.HasText("Whole dialogue (multi-speaker models)") {
			t.Errorf("%s: dialogue section %v, picker %v", page.path, tt.HasText("Whole dialogue (multi-speaker models)"), tt.HasText("Search models"))
		}
		a.pickerOpen = nil
	}
}
