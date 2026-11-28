package main

// The Studio's "No reference clip" hint shows only when the model would
// really speak in its default voice: it asks the voice the recipe sends
// (takeVoice over studioVoiceTake), so a saved voice from the picker —
// a designed voice with a sample on Qwen3-TTS Base, as in the promo —
// silences it, and the hint and the render can't disagree.

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
)

const defaultVoiceHint = "No reference clip: the model speaks in its default voice."

func entryByID(t *testing.T, id string) catalog.Entry {
	t.Helper()
	e, ok := catalog.Builtin().ByID(id)
	if !ok {
		t.Fatalf("no catalogue entry %s", id)
	}
	return e
}

func TestTakeVoice(t *testing.T) {
	base := entryByID(t, "qwen3-tts-1.7b-base-8bit")
	custom := entryByID(t, "qwen3-tts-0.6b-customvoice-8bit")
	omni := entryByID(t, "omnivoice-bf16")
	kokoroE := entryByID(t, "kokoro-82m-bf16")
	turbo := entryByID(t, "chatterbox-turbo-8bit")
	moss := cloneOnly(t, nil)
	for _, c := range []struct {
		name string
		t    core.Take
		e    catalog.Entry
		want voiceOutcome
	}{
		{"clone model, nothing", core.Take{}, base, voiceDefault},
		{"clone model, blank reference", core.Take{RefAudio: "  "}, base, voiceDefault},
		{"clone model, reference", core.Take{RefAudio: "/v/sample.wav"}, base, voiceGiven},
		{"clone model, voice prompt", core.Take{VoicePrompt: "/v/prompt.safetensors"}, base, voiceGiven},
		{"clone model, a typed voice", core.Take{Voice: "x"}, base, voiceGiven},
		{"built-in speakers", core.Take{}, custom, voiceGiven},
		{"built-in speaker", core.Take{Voice: "Vivian"}, custom, voiceGiven},
		{"style model, nothing", core.Take{}, omni, voiceGiven},
		{"no cloning (Kokoro)", core.Take{}, kokoroE, voiceGiven},
		{"clone model with a default voice (Turbo)", core.Take{}, turbo, voiceDefault},
		{"only clones, nothing", core.Take{}, moss, voiceMissing},
		{"only clones, a voice is not enough", core.Take{Voice: "x"}, moss, voiceMissing},
		{"only clones, reference", core.Take{RefAudio: "/r.wav"}, moss, voiceGiven},
		{"only clones, voice prompt", core.Take{VoicePrompt: "/p"}, moss, voiceGiven},
	} {
		if got := takeVoice(c.t, c.e); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// voiceWithSample saves a designed voice that carries a rendered sample
// (or none), as Voice Design and Discover voices save them.
func voiceWithSample(t *testing.T, a *app, name string, sample bool) core.Voice {
	t.Helper()
	v := core.Voice{Name: name, Kind: core.VoiceDesigned, Design: "warm, calm female voice", Language: "zh"}
	var f core.VoiceFiles
	if sample {
		f.Sample = filepath.Join(t.TempDir(), "sample.wav")
		if err := writeSpeech(f.Sample, 3, []span{{0.2, 2.8}}); err != nil {
			t.Fatal(err)
		}
		v.SampleText = "你好，欢迎收听。"
	}
	saved, err := a.lib.svc.Voices.SaveFiles(t.Context(), v, f)
	if err != nil {
		t.Fatal(err)
	}
	if sample && saved.Sample == "" {
		t.Fatalf("the sample was not saved: %+v", saved)
	}
	return saved
}

func TestStudioDefaultVoiceHint(t *testing.T) {
	simulateDarwin(t)
	a, eng, tt := newTestApp(t)
	base := entryByID(t, "qwen3-tts-1.7b-base-8bit")
	wenqing := voiceWithSample(t, a, "温晴", true)
	noSample := voiceWithSample(t, a, "Design only", false)
	clone := addVoice(t, a, core.Voice{Name: "Clone", Kind: core.VoiceCloned, RefText: "Words."}, 4)
	a.text = "大家好。"
	a.selectModel(base)
	tt.Frame()

	hint := func() bool { tt.Frame(); return tt.HasText(defaultVoiceHint) }

	// No voice: the hint is true.
	if !hint() || !a.canGenerate() {
		t.Fatalf("no hint without a voice: %q", tt.Texts())
	}

	// The bug: a designed voice with a sample, picked in the Studio.
	if ok, why := voiceUsable(&wenqing, base); !ok {
		t.Fatalf("温晴 on Qwen3-TTS Base: %s", why)
	}
	a.studioVoice = wenqing.ID
	if hint() {
		t.Fatalf("the hint shows with 温晴 picked: %q", tt.Texts())
	}
	// …and the render does use 温晴's sample: hint and recipe agree.
	if err := tt.Click("Generate"); err != nil {
		t.Fatal(err)
	}
	p := renderOf(t, eng.last(t)).Take
	if p.VoiceID != wenqing.ID || p.RefAudio == "" || filepath.Base(p.RefAudio) != wenqing.Sample {
		t.Fatalf("request %+v", p)
	}
	eng.cancelled(eng.last(t).ID)
	tt.Frame()
	if a.job != nil {
		t.Fatal("the job is still on")
	}

	// A cloned voice.
	a.studioVoice = clone.ID
	if hint() {
		t.Errorf("the hint shows with a cloned voice: %q", tt.Texts())
	}

	// A designed voice without a sample can't be spoken by a model that
	// only clones: the recipe has no reference, the hint is true.
	a.studioVoice = noSample.ID
	if !hint() {
		t.Errorf("no hint with a design the model can't speak: %q", tt.Texts())
	}

	// An unsaved clip ("Use without saving").
	a.studioVoice, a.studioClip = "", &clipRef{Path: "/tmp/clip.wav", Text: "Words.", Name: "clip"}
	if hint() {
		t.Errorf("the hint shows with an unsaved clip: %q", tt.Texts())
	}
	a.studioClip = nil

	// The inspector's own reference clip.
	a.refAudio = "/tmp/ref.wav"
	if hint() {
		t.Errorf("the hint shows with a reference clip: %q", tt.Texts())
	}
	a.refAudio = ""
	if !hint() {
		t.Errorf("no hint after clearing the clip: %q", tt.Texts())
	}

	// A model with built-in speakers (CustomVoice), one that takes a style
	// (OmniVoice), one that doesn't clone (Kokoro): never the hint.
	acceptLicences(t, a)
	for _, id := range []string{"qwen3-tts-0.6b-customvoice-8bit", "omnivoice-bf16", "kokoro-82m-bf16"} {
		a.selectModel(entryByID(t, id))
		if hint() {
			t.Errorf("%s: the hint shows: %q", id, tt.Texts())
		}
	}

	// A model that only clones: its own refusal, not the hint.
	moss := cloneOnly(t, a)
	a.selectModel(moss)
	a.studioVoice = ""
	if hint() || !tt.HasText(studioNeedsReferenceText(moss)) || a.canGenerate() {
		t.Errorf("clone-only without a reference: %q", tt.Texts())
	}
	a.studioVoice = wenqing.ID // its sample is a reference
	if hint() || tt.HasText(studioNeedsReferenceText(moss)) || !a.canGenerate() {
		t.Errorf("clone-only with 温晴: %v %q", a.canGenerate(), tt.Texts())
	}
}

// The voice the hints look at is the voice the recipe sends.
func TestStudioVoiceTakeMatchesRecipe(t *testing.T) {
	a, _, _ := newTestApp(t)
	base := entryByID(t, "qwen3-tts-1.7b-base-8bit")
	wenqing := voiceWithSample(t, a, "温晴", true)
	a.text = "大家好。"
	a.selectModel(base)
	for _, sel := range []string{"", wenqing.ID} {
		a.studioVoice = sel
		v, r := a.studioVoiceTake(base), a.recipe(time.Now())
		if v.Voice != r.Voice || v.RefAudio != r.RefAudio || v.RefText != r.RefText || v.VoicePrompt != r.VoicePrompt || v.VoiceID != r.VoiceID {
			t.Errorf("voice %q: hint take %+v, recipe %+v", sel, v, r)
		}
	}
}
