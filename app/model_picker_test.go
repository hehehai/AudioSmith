package main

import (
	"strings"
	"testing"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/dub"
)

// TestModelPicker: the Studio's picker lists the recommended models for
// the interface's language first, by friendly name with their one line,
// tags and hints; the
// others come with "Show all models"; picking one applies it; removed
// models are never offered.
func TestModelPicker(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.Frame()
	a.pickerOpen = map[string]bool{"model": true}
	tt.Frame()
	texts := strings.Join(tt.Texts(), "|")
	for _, want := range []string{"Recommended", "Fast English voices", "The fastest, most natural built-in English voices; light on memory.", "Built-in voices", "Speed ●●● · Quality ●●●", "English clone", "Show all models (4)"} {
		if !strings.Contains(texts, want) {
			t.Errorf("picker lacks %q: %s", want, texts)
		}
	}
	if strings.Contains(texts, "Marvis TTS 250M") {
		t.Error("the others show before Show all models")
	}
	tt.Click("Show all models (4)")
	tt.Frame()
	texts = strings.Join(tt.Texts(), "|")
	for _, l := range []string{"Standard voices", "Multilingual all-rounder", "Non-commercial", "Custom Hugging Face repo"} {
		if !strings.Contains(texts, l) {
			t.Errorf("all models lack %q", l)
		}
	}
	for _, r := range catalog.Builtin().Removed {
		if strings.Contains(texts, "|"+r.Label+"|") {
			t.Errorf("removed %s offered", r.ID)
		}
	}
	if err := tt.Click("High-fidelity clone"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.preset().ID != "qwen3-tts-1.7b-base-8bit" || a.pickerOpen["model"] {
		t.Errorf("picked %s, open %v", a.preset().ID, a.pickerOpen["model"])
	}
}

// TestModelPickerDub: the dub's picker greys out the models that can't
// clone, with the reason.
func TestModelPickerDub(t *testing.T) {
	d := newDubTest(t, 3)
	a, tt := d.a, d.tt
	a.dubSourceFile(d.src)
	a.dubStep = dubStepGenerate
	a.dubSide.set, a.dubSide.open = true, true
	a.pickerOpen = map[string]bool{"gen-model": true}
	a.pickerAll = map[string]bool{"gen-model": true}
	tt.Frame()
	tt.Frame()
	texts := strings.Join(tt.Texts(), "|")
	if !strings.Contains(texts, "Can't clone: the dub won't sound like the speakers") || !strings.Contains(texts, "Speaks in a saved or cloned voice at about real time: long narration and dubbing.") {
		t.Errorf("dub picker: %s", texts)
	}
	if err := tt.Click("Fast English voices"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.dub.Settings.Model == entry(t, "kokoro-82m-bf16").Repo {
		t.Error("a model that can't clone was picked")
	}
	_ = dub.TimingSmart
}
