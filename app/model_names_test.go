package main

import (
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/i18n"
)

// Thread #45: models are named for what they do ("Standard voices"), not
// by family, version or quantisation, which stay in details, tooltips
// and the API.

// TestModelNames: every catalogue entry has a friendly name, a line and
// tags in English and Chinese; the names are distinct and short in both,
// and say nothing of the family or the size; Japanese (thread #173) too.
func TestModelNames(t *testing.T) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	for _, loc := range []string{"en", "zh-Hans", "ja"} {
		i18n.SetLocale(loc)
		seen := map[string]string{}
		for _, e := range catalog.Builtin().Entries {
			name := e.DisplayName()
			if name == "" || name == e.Label {
				t.Errorf("%s %s: no friendly name (%q)", loc, e.ID, name)
			}
			if loc == "zh-Hans" {
				if name == e.Name {
					t.Errorf("%s: %q is not translated", e.ID, name)
				}
				if n := cjkLen(name); n > 8 || n == 0 {
					t.Errorf("%s: %q has %d Chinese characters (1–8)", e.ID, name, n)
				}
				if d := e.Description(); d == i18n.T("") || !strings.ContainsFunc(d, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
					t.Errorf("%s: description %q is not translated", e.ID, d)
				}
			} else if loc == "ja" {
				if name == e.Name || len([]rune(name)) > 32 {
					t.Errorf("%s: %q is not translated, or long", e.ID, name)
				}
				if d := e.Description(); !strings.ContainsFunc(d, func(r rune) bool { return unicode.In(r, unicode.Hiragana, unicode.Katakana) }) {
					t.Errorf("%s: description %q is not translated", e.ID, d)
				}
			} else if len([]rune(name)) > 32 {
				t.Errorf("%s: %q is long", e.ID, name)
			}
			if other, ok := seen[strings.ToLower(name)]; ok {
				t.Errorf("%s: %s and %s are both %q", loc, other, e.ID, name)
			}
			seen[strings.ToLower(name)] = e.ID
			for _, w := range technicalWords() {
				if strings.Contains(name, w) || strings.Contains(e.Description(), w) && e.Summary != "" {
					t.Errorf("%s %s: %q / %q names %q", loc, e.ID, name, e.Description(), w)
				}
			}
			for _, tg := range e.DisplayTags(0) {
				if l := tg.Label(); l == "" || l == string(tg) {
					t.Errorf("%s: tag %q has no label", loc, tg)
				}
			}
		}
		for _, tg := range catalog.AllTags() {
			if l := tg.Label(); l == "" || l == string(tg) {
				t.Errorf("%s: tag %q has no label", loc, tg)
			}
		}
	}
	// Every speech model has tags; the non-commercial one says so first.
	for _, e := range catalog.Builtin().ForTask(catalog.TaskTTS) {
		tags := e.DisplayTags(3)
		if len(tags) == 0 || len(tags) > 3 {
			t.Errorf("%s: tags %v", e.ID, tags)
		}
		if !e.Licence.Commercial && tags[0] != catalog.TagNonCommercial {
			t.Errorf("%s: %v", e.ID, tags)
		}
	}
}

func cjkLen(s string) int {
	n := 0
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			n++
		}
	}
	return n
}

// technicalWords are what the default UI must not show: every model's
// label and repo, its quantisation, the families and sizes.
func technicalWords() []string {
	words := []string{"Qwen", "Kokoro", "OmniVoice", "Chatterbox", "Whisper", "Parakeet", "Gemma", "Demucs", "RoFormer", "Sortformer",
		"Silero", "AudioSeal", "DeepFilter", "CustomVoice", "VoiceDesign", "0.6B", "1.7B", "82M", "bf16", "8bit", "4bit"}
	for _, e := range catalog.Builtin().Entries {
		words = append(words, e.Label, catalog.ShortRepo(e.Repo))
	}
	return words
}

// technicalIn lists the texts that name a model technically, leaving out
// the ones allowed to (a model's ⓘ tells what it is: its technical
// summary).
func technicalIn(texts []string) []string {
	var out []string
	for _, s := range texts {
		allowed := false
		for _, e := range catalog.Builtin().Entries {
			allowed = allowed || strings.Contains(s, e.TechnicalSummary())
		}
		if allowed {
			continue
		}
		for _, w := range technicalWords() {
			if strings.Contains(s, w) {
				out = append(out, s)
				break
			}
		}
	}
	return out
}

// TestModelNamesInUI: the Studio's picker (every model shown), the
// Models page, ⌘E and the settings that choose a model show friendly
// names only, in English and Chinese; the technical ones are in the
// tooltip and the card's Details.
func TestModelNamesInUI(t *testing.T) {
	simulateDarwin(t)
	t.Cleanup(func() { i18n.SetLocale("en") })
	for _, loc := range []string{"en", "zh-Hans", "ja"} {
		a, _, tt := newTestApp(t)
		i18n.SetLocale(loc)
		check := func(where string) {
			t.Helper()
			tt.Frame()
			tt.Frame()
			if bad := technicalIn(tt.Texts()); len(bad) > 0 {
				t.Errorf("%s %s names models technically: %q", loc, where, bad)
			}
		}
		a.navigate("/studio")
		a.pickerOpen = map[string]bool{"model": true}
		a.pickerAll = map[string]bool{"model": true}
		check("picker")
		if !tt.HasText(entry(t, "omnivoice-bf16").DisplayName()) || !tt.HasText(entry(t, "chatterbox-turbo-8bit").DisplayName()) {
			t.Errorf("%s picker: %q", loc, tt.Texts())
		}
		a.pickerOpen = map[string]bool{}
		openModels(a)
		check("models page")
		for _, e := range catalog.Builtin().Entries {
			if !tt.HasText(e.DisplayName()) {
				t.Errorf("%s models page lacks %q", loc, e.DisplayName())
			}
		}
		// Details tell what it is.
		a.modelsDetail = map[string]bool{"kokoro-82m-bf16": true}
		tt.Frame()
		if !tt.HasText("Kokoro 82M") || !tt.HasText("mlx-community/Kokoro-82M-bf16") || !tt.HasText("bf16") {
			t.Errorf("%s details: %q", loc, tt.Texts())
		}
		a.modelsDetail = nil
		a.switchOpen = true
		check("⌘E")
		a.switchOpen = false
		for _, page := range []string{"studio", "dictation", "writing"} {
			a.navigate("/settings/" + page)
			check("settings " + page)
		}
		a.navigate("/studio")
		check("studio")
		i18n.SetLocale("en")
	}
}

// TestModelSearch: the picker's and the Models page's search find a model
// by its friendly name (in the UI's language or English), its technical
// name, id or repo, and its tags.
func TestModelSearch(t *testing.T) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	hifi := entry(t, "qwen3-tts-1.7b-base-8bit")
	for _, q := range []string{"high-fidelity", "Qwen3-TTS 1.7B Base", "qwen3 1.7b base", "qwen3-tts-1.7b-base-8bit", "Qwen3-TTS-12Hz-1.7B-Base-8bit", "cloning quality"} {
		if !hifi.Matches(q) {
			t.Errorf("%q does not find it", q)
		}
	}
	if hifi.Matches("kokoro") || hifi.Matches("design") {
		t.Error("finds what it is not")
	}
	i18n.SetLocale("zh-Hans")
	for _, q := range []string{"高保真", "克隆", "High-fidelity", "1.7b"} {
		if !hifi.Matches(q) {
			t.Errorf("zh: %q does not find it", q)
		}
	}
	i18n.SetLocale("en")

	a, _, tt := newTestApp(t)
	tt.Frame()
	a.pickerOpen = map[string]bool{"model": true}
	a.pickerFind = map[string]string{"model": "omnivoice"}
	tt.Frame()
	texts := tt.Texts()
	if !slices.Contains(texts, "Multilingual all-rounder") || slices.Contains(texts, "High-fidelity clone") || !slices.Contains(texts, "Search models") {
		t.Errorf("picker search: %q", texts)
	}
	a.pickerFind["model"] = "zzz"
	tt.Frame()
	if !tt.HasText("No model matches") {
		t.Errorf("no match: %q", tt.Texts())
	}
	a.pickerOpen = map[string]bool{}

	openModels(a)
	a.modelsFind = "whisper"
	tt.Frame()
	if !tt.HasText("Multilingual transcription") || tt.HasText("Fast English voices") {
		t.Errorf("models search: %q", tt.Texts())
	}
	a.modelsFind = "light"
	tt.Frame()
	if !tt.HasText("Fast English voices") || !tt.HasText("Speech detection") || tt.HasText("High-fidelity clone") {
		t.Errorf("by tag: %q", tt.Texts())
	}
	// Typing in the field.
	a.modelsFind = ""
	tt.Frame()
	if err := tt.Click("Search models"); err != nil {
		t.Fatal(err)
	}
	tt.Type("whisper")
	tt.Frame()
	if a.modelsFind != "whisper" || !tt.HasText("Multilingual transcription") || tt.HasText("Word timing") {
		t.Errorf("typed %q: %q", a.modelsFind, tt.Texts())
	}
}
