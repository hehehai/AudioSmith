package main

import (
	"slices"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dub"
)

// VoxCPM2, an optional model (thread #165): its clones take the Studio's
// delivery, a designed voice with a clip is cloned, its steps tiers sit
// around 10, and a dub into a language Qwen3 lacks offers it.
func TestVoxCPM2Wiring(t *testing.T) {
	vox, base := entry(t, "voxcpm2-8bit"), entry(t, "qwen3-tts-1.7b-base-8bit")

	clone := core.Take{RefAudio: "/ref.wav", Voice: "calm"}
	if got := cloneDelivery(clone, vox, "a little faster"); got != "calm, a little faster" {
		t.Errorf("vox: %q", got)
	}
	if got := cloneDelivery(core.Take{RefAudio: "/ref.wav"}, vox, "a little faster"); got != "a little faster" {
		t.Errorf("vox, no style: %q", got)
	}
	if got := cloneDelivery(clone, base, "a little faster"); got != "calm" {
		t.Errorf("base: %q", got)
	}
	if got := cloneDelivery(core.Take{Voice: "a warm voice"}, vox, "a warm voice"); got != "a warm voice" {
		t.Errorf("design: %q", got)
	}

	designed := &core.Voice{Kind: core.VoiceDesigned, Sample: "sample.wav"}
	if !clonesOwnTake(vox, designed) || clonesOwnTake(entry(t, "omnivoice-bf16"), designed) || clonesOwnTake(vox, &core.Voice{Kind: core.VoiceDesigned}) {
		t.Error("clonesOwnTake")
	}

	pre, _ := catalog.PresetFor(vox.ID)
	if got := tierSteps(pre.ParamsFor(catalog.WorkflowStudio)); !slices.Equal(got, []float64{5, 10, 15}) {
		t.Errorf("vox tiers %v", got)
	}
	omni, _ := catalog.PresetFor("omnivoice-bf16")
	if got := tierSteps(omni.ParamsFor(catalog.WorkflowStudio)); !slices.Equal(got, []float64{16, 32, 48}) {
		t.Errorf("omnivoice tiers %v", got)
	}

	if got := dubUnspoken(base, []string{"es", "vi", "th"}); !slices.Equal(got, []string{"vi", "th"}) {
		t.Errorf("unspoken %v", got)
	}
	if e, ok := dubModelSpeaking([]string{"vi", "th"}, base.Repo); !ok || e.ID != vox.ID {
		t.Errorf("offered %s %v", e.ID, ok)
	}
	if _, ok := dubModelSpeaking([]string{"xh"}, base.Repo); ok {
		// Xhosa: only OmniVoice (no language list, non-commercial) would.
		if e, _ := dubModelSpeaking([]string{"xh"}, base.Repo); e.ID != "omnivoice-bf16" {
			t.Errorf("xh offered %s", e.ID)
		}
	}
	if dub := dubDefaultModelBy(func(catalog.Entry) (bool, bool) { return false, false }); dub == vox.Repo {
		t.Error("VoxCPM2 is the dub's default")
	}
}

// TestVoxCPM2StudioDelivery: with a reference clip, VoxCPM2's 「高级」
// offers Delivery (the clone's instruction) and its steps; the take sends
// the clip, the delivery and 10-step tiers.
func TestVoxCPM2StudioDelivery(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.SetSize(1100, 1000)
	a.selectModel(entry(t, "voxcpm2-8bit"))
	a.setAdvancedOpen("studio", true)
	a.refAudio, a.refText = "/tmp/ref.wav", "what it says"
	tt.Frame()
	if !tt.HasText("Delivery") || !tt.HasText("Voice refinement") || tt.HasText("Voice description") {
		t.Fatalf("advanced: %q", tt.Texts())
	}
	a.voice = "a little faster, cheerful"
	a.text = "Hello there."
	tt.Click("Quality")
	tt.Frame()
	r := a.recipe(time.Now())
	if r.RefAudio != "/tmp/ref.wav" || r.Voice != "a little faster, cheerful" || r.Steps != 15 {
		t.Errorf("recipe %+v", r)
	}
	// Without a clip it designs: the field is the voice description.
	a.refAudio = ""
	tt.Frame()
	if tt.HasText("Delivery") || !tt.HasText("Voice description") {
		t.Errorf("no clip: %q", tt.Texts())
	}
}

// TestDubLanguageNote: a dub into Vietnamese with Qwen3-TTS says it
// doesn't speak it and offers VoxCPM2 (thread #165), which one click
// picks; a model that speaks every target says nothing.
func TestDubLanguageNote(t *testing.T) {
	d := newDubTest(t, 4)
	a, tt := d.a, d.tt
	tt.SetSize(1100, 1400)
	a.dubSourceFile(d.src)
	a.dubSide.set, a.dubSide.open = true, true
	a.dubStart()
	base, vox := entry(t, "qwen3-tts-1.7b-base-8bit"), entry(t, "voxcpm2-8bit")
	a.dubSet(func(p *dub.Project) { p.ChooseTargets([]string{"es", "vi"}) })
	a.dubSettings(func(s *dub.Settings) { s.Model = base.Repo })
	a.dubStep = dubStepGenerate
	tt.Frame()
	use := "Use " + vox.DisplayName()
	if !tt.HasText(use) {
		t.Fatalf("no offer: %q", tt.Texts())
	}
	if err := tt.Click(use); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.dub.Settings.Model != vox.Repo || tt.HasText(use) {
		t.Errorf("model %s; %q", a.dub.Settings.Model, tt.Texts())
	}
}
