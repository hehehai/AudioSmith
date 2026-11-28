package main

// Thread #46 removed Parakeet, Mel-RoFormer, Gemma 4 E4B and E2B and
// Qwen3 1.7B, and made Qwen3-ASR the speech recognition of every
// workflow (Whisper for the languages it does not know).

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/settings"
)

const (
	parakeetRepo = "mlx-community/parakeet-tdt-0.6b-v3"
	roformerRepo = "mlx-community/mel-roformer-kim-vocal-2-mlx"
	gemmaE2BRepo = "mlx-community/gemma-4-e2b-it-4bit"
	gemmaE4BRepo = "mlx-community/gemma-4-e4b-it-4bit"
	qwen17Repo   = "mlx-community/Qwen3-1.7B-4bit"
)

// TestTrimSettings: at start the settings that name a removed model go
// back to their defaults, which follow the language (dictation in
// Bulgarian: Whisper; elsewhere Qwen3-ASR; the language model: Qwen3
// 4B); routes of writing help to a removed local model use the default
// one; a model the user chose that is still there (Whisper) stays; one
// notice says it all.
func TestTrimSettings(t *testing.T) {
	dir := t.TempDir()
	a, _, _ := newTestAppIn(t, dir)
	temp := 0.2
	a.updateSettings(func(s *settings.Settings) {
		s.Dictation.Model, s.Dictation.Language = "parakeet-tdt-0.6b-v3", "bg"
		s.Transcribe.FastModel = "parakeet-tdt-0.6b-v3"
		s.Transcribe.AccurateModel = catalog.FallbackSTT // the user's choice: kept
		s.Calls.STTModel = "parakeet-tdt-0.6b-v3"
		s.LLM.LocalModel = "gemma-4-e2b-it-4bit"
		s.LLM.Manual = true
		s.LLM.Default = []settings.LLMRoute{{Provider: localProvider, Model: gemmaE4BRepo}, {Provider: "ollama", Model: "llama3"}}
		s.LLM.Skills = map[string][]settings.LLMRoute{"translate": {{Provider: localProvider, Model: "Qwen3 1.7B", Temperature: &temp}}}
	})
	a.lib.close()
	b, _, tt := newTestAppIn(t, dir)
	st := b.settings.Get()
	if st.Dictation.Model != "" || st.Transcribe.FastModel != "" || st.Calls.STTModel != "" || st.LLM.LocalModel != "" {
		t.Errorf("settings %+v %+v %q %q", st.Dictation, st.Transcribe, st.Calls.STTModel, st.LLM.LocalModel)
	}
	if st.Transcribe.AccurateModel != catalog.FallbackSTT || st.Dictation.Language != "bg" {
		t.Errorf("the user's choices went: %+v", st.Transcribe)
	}
	if r := st.LLM.Default; len(r) != 2 || r[0].Model != "" || r[0].Provider != localProvider || r[1].Model != "llama3" {
		t.Errorf("default routes %+v", r)
	}
	if r := st.LLM.Skills["translate"]; len(r) != 1 || r[0].Model != "" || r[0].Temperature == nil || *r[0].Temperature != 0.2 {
		t.Errorf("translate routes %+v", r)
	}
	if b.dictModel().ID != catalog.FallbackSTT || b.trModel(true).ID != catalog.DefaultSTT || b.trModel(false).ID != catalog.FallbackSTT ||
		callSTT(st).ID != catalog.DefaultSTT || b.localLLMEntry().ID != catalog.DefaultLLM {
		t.Errorf("models: dictation %s, fast %s, accurate %s, calls %s, llm %s", b.dictModel().ID, b.trModel(true).ID, b.trModel(false).ID, callSTT(st).ID, b.localLLMEntry().ID)
	}
	if countToasts(b, tt, "Removed models: Parakeet TDT 0.6B v3 → Multilingual transcription, Gemma 4 E2B → Writing assistant · Standard, Gemma 4 E4B → Writing assistant · Standard, Qwen3 1.7B → Writing assistant · Standard. Your settings now use the replacements.") != 1 {
		t.Errorf("toasts %q %q", b.toasts, tt.Texts())
	}
	b.lib.close()
	c, _, tt3 := newTestAppIn(t, dir)
	if countToasts(c, tt3, "was removed") != 0 {
		t.Errorf("again: %q", c.toasts)
	}
	// A removed model's repo (a re-run job, an old transcript) resolves to
	// its replacement's files.
	for repo, id := range map[string]string{parakeetRepo: catalog.DefaultSTT, roformerRepo: catalog.DefaultSeparate, gemmaE2BRepo: catalog.DefaultLLM, qwen17Repo: catalog.DefaultLLM} {
		if e := entryFor(repo); e.ID != id {
			t.Errorf("entryFor(%s) = %s", repo, e.ID)
		}
	}
}

// TestDefaultASR: Qwen3-ASR transcribes Chinese, English and detected
// speech in every workflow — Transcribe's Fast and Accurate modes,
// dictation, the AI calls, dubbing and its read-back check, workflows,
// Convert, clone references — and Whisper the languages Qwen3-ASR does
// not know; a model the user chose in the settings is kept.
func TestDefaultASR(t *testing.T) {
	a, eng, _ := newTestApp(t)
	for lang, id := range map[string]string{"": catalog.DefaultSTT, "zh": catalog.DefaultSTT, "zh-Hans": catalog.DefaultSTT, "en": catalog.DefaultSTT,
		"ja": catalog.DefaultSTT, "sw": catalog.FallbackSTT, "uk": catalog.FallbackSTT} {
		a.updateSettings(func(s *settings.Settings) { s.Transcribe.Language, s.Dictation.Language = lang, lang })
		if a.trModel(true).ID != id || a.trModel(false).ID != id || a.dictModel().ID != id || sttEntry("", lang).ID != id {
			t.Errorf("%q: fast %s, accurate %s, dictation %s", lang, a.trModel(true).ID, a.trModel(false).ID, a.dictModel().ID)
		}
	}
	// The language picker offers what either model knows.
	if l := a.trLanguages(false); len(l) < 100 || !slices.Contains(l, "sw") || !slices.Contains(l, "zh") || !slices.Contains(l, "fil") {
		t.Errorf("languages %d", len(l))
	}
	a.updateSettings(func(s *settings.Settings) { s.Transcribe.FastModel = catalog.DefaultSTT })
	if l := a.trLanguages(true); len(l) != 30 || !slices.Contains(l, "zh") || slices.Contains(l, "sw") {
		t.Errorf("Qwen3-ASR's languages %v", l)
	}
	// Explicit choices stay.
	a.updateSettings(func(s *settings.Settings) {
		s.Transcribe.Language, s.Dictation.Language = "zh", "zh"
		s.Dictation.Model, s.Transcribe.AccurateModel = catalog.FallbackSTT, catalog.FallbackSTT
	})
	if a.dictModel().ID != catalog.FallbackSTT || a.trModel(false).ID != catalog.FallbackSTT || a.trModel(true).ID != catalog.DefaultSTT {
		t.Errorf("explicit: %s %s %s", a.dictModel().ID, a.trModel(false).ID, a.trModel(true).ID)
	}
	a.updateSettings(func(s *settings.Settings) {
		s.Dictation.Model, s.Transcribe.AccurateModel, s.Transcribe.FastModel = "", "", ""
	})
	if callSTT(a.settings.Get()).ID != catalog.DefaultSTT {
		t.Errorf("calls: %s", callSTT(a.settings.Get()).ID)
	}

	// Dubbing, its read-back check and workflows.
	qwen, whisper := entry(t, catalog.DefaultSTT), entry(t, catalog.FallbackSTT)
	m := dubModels(a.lib)
	if m.STT != qwen.Repo || m.OtherSTT != whisper.Repo || m.Separate != entry(t, catalog.DefaultSeparate).Repo {
		t.Fatalf("dub models %+v", m)
	}
	for lang, repo := range map[string]string{"": qwen.Repo, "zh": qwen.Repo, "en-US": qwen.Repo, "ko": qwen.Repo, "sw": whisper.Repo, "he": whisper.Repo} {
		if got, _ := m.STTFor(lang); got != repo {
			t.Errorf("dub %q: %s", lang, got)
		}
	}
	w := workflowSTT(a.lib)
	if got, _ := w.For("sw"); got != whisper.Repo || w.Model != qwen.Repo || w.Aligner != entry(t, catalog.DefaultAlign).Repo {
		t.Errorf("workflow %+v", w)
	}
	a.updateSettings(func(s *settings.Settings) { s.Transcribe.AccurateModel = catalog.FallbackSTT })
	if m := dubModels(a.lib); m.OtherSTT != "" {
		t.Errorf("dub with an explicit model: %+v", m)
	} else if got, _ := m.STTFor("zh"); got != whisper.Repo {
		t.Errorf("dub with an explicit model: %s", got)
	}
	a.updateSettings(func(s *settings.Settings) { s.Transcribe.AccurateModel = "" })

	// A clone reference.
	for lang, repo := range map[string]string{"": qwen.Repo, "zh": qwen.Repo, "sw": whisper.Repo} {
		clip := filepath.Join(t.TempDir(), "ref.wav")
		writeSpeech(clip, 4, []span{{0.2, 3.8}})
		got := ""
		a.transcribeClip(clip, lang, func(s string, err error) { got = s })
		if p := answerTranscribe(t, eng, "Hello there."); p.Model != repo {
			t.Errorf("clone reference in %q: %s", lang, p.Model)
		}
		if got != "Hello there." {
			t.Errorf("clone reference text %q", got)
		}
	}
}

// TestTrimNotOffered: no picker or list offers a removed model: the
// dictation and Transcribe models, the writing-help model (Qwen3 4B,
// shown by name: there is nothing to choose), the Models page, ⌘E and the
// onboarding packs.
func TestTrimNotOffered(t *testing.T) {
	simulateDarwin(t)
	a, _, tt := newTestApp(t)
	removed := []string{"Parakeet", "European transcription", "Mel-RoFormer", "Fine voice separation", "Gemma", "Qwen3 1.7B",
		"Writing assistant · Light", "Writing assistant · Mini", "Writing assistant · Multilingual"}
	check := func(where string) {
		t.Helper()
		for _, s := range tt.Texts() {
			for _, r := range removed {
				if strings.Contains(s, r) {
					t.Errorf("%s shows %q", where, s)
				}
			}
		}
	}
	for _, e := range append(append(sttEntries(), localLLMOptions()...), packEntries("max")...) {
		if _, ok := catalog.Builtin().RemovedModel(e.ID); ok {
			t.Errorf("offered: %s", e.ID)
		}
	}
	if l := localLLMOptions(); len(l) != 1 || l[0].ID != catalog.DefaultLLM {
		t.Errorf("language models %v", l)
	}
	openModels(a)
	for k := range modelKinds {
		a.modelsKind = k
		tt.Frame()
		check("models tab " + itoa(k))
	}
	a.navigate("/settings/dictation")
	tt.Frame()
	tt.Frame()
	check("dictation settings")
	if !tt.HasText("“Chinese & English transcription” answers fastest; “Multilingual transcription” covers the other languages.") {
		t.Errorf("dictation model: %q", tt.Texts())
	}
	a.navigate("/settings/writing")
	tt.Frame()
	tt.Frame()
	check("writing help")
	if !tt.HasText("Writing assistant · Standard (") {
		t.Errorf("writing help: %q", tt.Texts())
	}
	a.switchOpen = true
	tt.Frame()
	check("⌘E")
	a.switchOpen = false
}

// TestTrimCleanup: the clean-up row also shows on the tabs of the
// removed models of #46 with files on disk, and Clean up deletes exactly
// their folders (Parakeet, Mel-RoFormer, both Gemmas, Qwen3 1.7B) with
// #43's; every kept model stays.
func TestTrimCleanup(t *testing.T) {
	a, _, tt := newTestApp(t)
	cache := a.hubClient().Options().CacheDir
	repos := map[string]int{parakeetRepo: 2e6, roformerRepo: 1e6, gemmaE2BRepo: 3e6, gemmaE4BRepo: 1e6, qwen17Repo: 1e6, kittenRepo: 2e6}
	for repo, n := range repos {
		putLeftover(t, cache, repo, n)
	}
	// An unknown repo next to them is not AudioSmith's to delete.
	putLeftover(t, cache, "someone/other-model", 1e6)
	openModels(a)
	a.refreshModels()
	tt.Frame()
	if !tt.HasText("Removed models still take 10 MB") {
		t.Fatalf("row: %q", tt.Texts())
	}
	for k, want := range []bool{true, true, true, true, true} {
		a.modelsKind = k
		tt.Frame()
		if tt.HasText("Removed models still take 10 MB") != want {
			t.Errorf("tab %d: row %v", k, !want)
		}
	}
	a.modelsKind = 3 // Writing
	tt.Frame()
	if err := tt.Click("Clean up"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	texts := strings.Join(tt.Texts(), "|")
	for _, s := range []string{"Clean up removed models (10 MB)?", "• Parakeet TDT 0.6B v3 · 2 MB", "• Mel-RoFormer (Kim vocal 2) · 1 MB", "• Gemma 4 E2B · 3 MB",
		"• Gemma 4 E4B · 1 MB", "• Qwen3 1.7B · 1 MB", "• KittenTTS mini · 2 MB"} {
		if !strings.Contains(texts, s) {
			t.Errorf("dialog lacks %q: %q", s, texts)
		}
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	for repo := range repos {
		if _, err := os.Stat(hub.RepoDir(cache, repo)); !os.IsNotExist(err) {
			t.Errorf("%s still there: %v", repo, err)
		}
	}
	if _, err := os.Stat(hub.RepoDir(cache, "someone/other-model")); err != nil {
		t.Errorf("another model went: %v", err)
	}
	for _, e := range catalog.Builtin().Entries {
		if _, err := os.Stat(hub.RepoDir(cache, e.Repo)); err != nil {
			t.Errorf("%s went: %v", e.Repo, err)
		}
	}
	a.modelsKind = 2
	tt.Frame()
	if tt.HasText("Removed models still take") {
		t.Errorf("after: %q", tt.Texts())
	}
}
