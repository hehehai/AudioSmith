package main

import (
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
)

// TestPresetDefaults: a take that sets no sampling gets the model's
// tuned parameters (thread #41): Qwen3-TTS 0.7 / 0.8, OmniVoice guidance
// 1.5 and 32 steps (16 for dubbing); what the recipe sets wins; models
// without a preset (Kokoro's sampling) get nothing.
func TestPresetDefaults(t *testing.T) {
	simulateDarwin(t)
	a, eng, _ := newTestApp(t)
	acceptLicences(t, a)
	gen := func(id string, edit func(*core.Take)) core.Take {
		t.Helper()
		a.job, a.jobError = nil, ""
		a.selectModel(entry(t, id))
		a.text = "Hello there."
		a.modelDefaults = true
		r := a.recipe(time.Now())
		if edit != nil {
			edit(&r)
		}
		a.startTake(r)
		req := eng.last(t)
		eng.cancelled(req.ID)
		sp := req.SynthesizeParams
		var got core.Take
		if sp.Temperature != nil {
			got.Temperature = *sp.Temperature
		}
		if sp.TopP != nil {
			got.TopP = *sp.TopP
		}
		if sp.Guidance != nil {
			got.Guidance = *sp.Guidance
		}
		got.Steps = sp.Steps
		return got
	}
	if g := gen("qwen3-tts-0.6b-customvoice-8bit", nil); g.Temperature != 0.7 || g.TopP != 0.8 {
		t.Errorf("CustomVoice %+v", g)
	}
	if g := gen("qwen3-tts-1.7b-base-8bit", func(r *core.Take) { r.Temperature = 0.95 }); g.Temperature != 0.95 || g.TopP != 0.8 {
		t.Errorf("1.7B Base with a temperature %+v", g)
	}
	if g := gen("omnivoice-bf16", nil); g.Guidance != 1.5 || g.Steps != 32 {
		t.Errorf("OmniVoice %+v", g)
	}
	if g := gen("kokoro-82m-bf16", nil); g.Temperature != 0 || g.TopP != 0 {
		t.Errorf("Kokoro %+v", g)
	}
	info := modelInfo(entry(t, "omnivoice-bf16").Repo)
	var dubTake core.Take
	info.FillDefaults(&dubTake, "dub")
	if dubTake.Steps != 16 || dubTake.Guidance != 1.5 {
		t.Errorf("OmniVoice dub %+v", dubTake)
	}
}

// TestStudioOverridesPerModel: a change in the Studio is kept for its
// model (settings), comes back when the model is picked again, does not
// leak into another model, and "Restore recommended" clears it.
func TestStudioOverridesPerModel(t *testing.T) {
	a, _, tt := newTestApp(t)
	cv := entry(t, "qwen3-tts-0.6b-customvoice-8bit")
	a.selectModel(cv)
	tt.Frame()
	if a.temperature != 0 || len(a.studioChanges()) != 0 || a.voice != "Serena" {
		t.Fatalf("fresh CustomVoice: temperature %v changes %v voice %q", a.temperature, a.studioChanges(), a.voice)
	}
	a.temperature = 0.5
	a.topP = 0.8 // the recommended value: no change
	tt.Frame()
	if o := a.prefs.Studio.Overrides[cv.ID]; o.Temperature != 0.5 || o.TopP != 0 {
		t.Fatalf("override %+v", o)
	}
	if ch := a.studioChanges(); len(ch) != 1 {
		t.Errorf("changes %v", ch)
	}
	a.selectModel(entry(t, "qwen3-tts-1.7b-base-8bit"))
	tt.Frame()
	if a.temperature != 0 {
		t.Errorf("1.7B Base inherits %v", a.temperature)
	}
	a.selectModel(cv)
	tt.Frame()
	if a.temperature != 0.5 {
		t.Errorf("back on CustomVoice: %v", a.temperature)
	}
	a.restoreRecommended()
	tt.Frame()
	if _, ok := a.prefs.Studio.Overrides[cv.ID]; ok || a.temperature != 0 {
		t.Errorf("after restore: %+v", a.prefs.Studio.Overrides)
	}
}

// TestStudioDraftV2: drafts before the presets keep what they meant —
// "the model's defaults" become the recommended settings; an explicit
// temperature stays a change; max tokens are dropped.
func TestStudioDraftV2(t *testing.T) {
	s := studioState{script: newScriptEditor()}
	s.applyDraft(studioDraft{Version: 2, ModelLabel: "Qwen3-TTS 0.6B CustomVoice", ModelDefaults: true, Temperature: 0.7, TopP: 0.9, MaxTokens: 1200})
	if s.temperature != 0 || s.topP != 0 || s.maxTokens != 0 {
		t.Errorf("defaults: %v %v %v", s.temperature, s.topP, s.maxTokens)
	}
	s.applyDraft(studioDraft{Version: 2, ModelLabel: "Qwen3-TTS 0.6B CustomVoice", ModelDefaults: false, Temperature: 0.6, TopP: 0.9, MaxTokens: 1200})
	if s.temperature != 0.6 || s.topP != 0.9 || s.maxTokens != 0 {
		t.Errorf("explicit: %v %v %v", s.temperature, s.topP, s.maxTokens)
	}
}

// TestNewUserModel: without a model in the settings and without a draft,
// a Chinese interface starts on Qwen3-TTS CustomVoice with Serena, and
// moves to 1.7B Base speaking the newest saved voice once there is one;
// picking a model ends that. An English one starts on Kokoro (af_heart).
func TestNewUserModel(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.Frame()
	if a.preset().ID != "kokoro-82m-bf16" || a.voice != "af_heart" || !a.studioAuto {
		t.Fatalf("English: %s %q auto %v", a.preset().ID, a.voice, a.studioAuto)
	}
	t.Cleanup(func() { i18n.SetLocale("en") })
	i18n.SetLocale("zh-Hans")
	a.autoStudioModel()
	if a.preset().ID != "qwen3-tts-0.6b-customvoice-8bit" || a.voice != "Serena" {
		t.Fatalf("Chinese: %s %q", a.preset().ID, a.voice)
	}
	v := addVoice(t, a, core.Voice{Name: "我的声音", Kind: core.VoiceCloned, Language: "zh"}, 8)
	tt.Frame()
	if a.preset().ID != "qwen3-tts-1.7b-base-8bit" || a.studioVoice != v.ID {
		t.Fatalf("with a voice: %s %q", a.preset().ID, a.studioVoice)
	}
	if a.prefs.Studio.Model != "" {
		t.Errorf("the automatic choice was saved: %q", a.prefs.Studio.Model)
	}
	a.selectModel(entry(t, "omnivoice-bf16"))
	addVoice(t, a, core.Voice{Name: "另一个", Kind: core.VoiceCloned, Language: "zh"}, 8)
	if a.preset().ID != "omnivoice-bf16" || a.studioAuto {
		t.Errorf("after choosing: %s auto %v", a.preset().ID, a.studioAuto)
	}
}
