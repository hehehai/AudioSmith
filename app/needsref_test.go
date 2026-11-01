package main

// A model that only clones voices (catalog NeedsReference: no shipped
// model since thread #43 removed MOSS-TTS-Nano, so the tests add one,
// cloneOnly) never gets a job without a reference: the Studio disables Generate and
// says why, the services refuse the recipe, a render job refuses it, the
// pickers drop voices without a clip; with a clip or a saved cloned voice
// it generates. The engine's own words reach the user.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/workflow"
)

// cloneOnly adds a speech model that only clones voices to the catalogue
// for the test and returns it; with an app, its files are put in the
// app's model folder (newTestApp does it for the models it finds).
func cloneOnly(t *testing.T, a *app) catalog.Entry {
	t.Helper()
	e := catalog.Entry{ID: "clone-only-nano", Label: "CloneOnly Nano", Name: "CloneOnly Nano", Family: "moss_tts_nano", Repo: "test/CloneOnly-Nano-100M",
		Revision: "0123456789abcdef0123456789abcdef01234567", Tasks: []catalog.Task{catalog.TaskTTS}, About: "A test model that only clones voices.",
		NeedsReference: true, Capabilities: []catalog.Capability{catalog.CapSampling, catalog.CapClone, catalog.CapSeed},
		SampleRate: 48000, MaxChars: 400, Licence: catalog.Licence{ID: "apache-2.0", Name: "Apache 2.0", Commercial: true, Verified: true}}
	c := *catalog.Builtin()
	c.Entries = append(slices.Clone(c.Entries), e)
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(catalog.Swap(&c))
	if a != nil {
		snap := filepath.Join(hub.RepoDir(modelCache(a.lib.settings.Get()), e.Repo), "snapshots", e.Revision)
		os.MkdirAll(snap, 0o755)
		os.WriteFile(filepath.Join(snap, "config.json"), []byte("{}"), 0o644)
		os.WriteFile(filepath.Join(snap, "model.safetensors"), []byte("weights"), 0o644)
	}
	return e
}

func TestStudioNeedsReference(t *testing.T) {
	moss := cloneOnly(t, nil)
	a, eng, tt := newTestApp(t)
	a.text = "Hello there."
	a.modelLabel = moss.Label
	a.modelChanged()
	tt.Frame()

	want := studioNeedsReferenceText(moss)
	if a.canGenerate() || !tt.HasText(want) {
		t.Fatalf("Generate allowed without a reference: %v, %q", a.canGenerate(), tt.Texts())
	}
	if !tt.HasText("Required: “CloneOnly Nano” only clones voices.") {
		t.Errorf("no caption in the Voice cloning section: %q", tt.Texts())
	}
	tt.Click("Generate")
	a.generate() // ⌘↩ too
	tt.Frame()
	if len(eng.sent) != 0 || a.job != nil {
		t.Fatalf("a job went out: %d", len(eng.sent))
	}

	// Chinese, as the user reads it.
	i18n.SetLocale("zh-Hans")
	got := studioNeedsReferenceText(moss)
	i18n.SetLocale("en")
	if got != "“CloneOnly Nano”只能克隆声音：请在右侧“音色”里选一个已保存的音色，或一段要克隆的音频。" {
		t.Errorf("zh: %q", got)
	}

	// A reference clip: Generate works and sends it.
	a.refAudio, a.refText = "/tmp/ref.wav", "Reference words."
	tt.Frame()
	if !a.canGenerate() || tt.HasText(want) {
		t.Fatalf("still refused with a clip: %q", tt.Texts())
	}
	if err := tt.Click("Generate"); err != nil {
		t.Fatal(err)
	}
	p := renderOf(t, eng.last(t)).Take
	if p.Model != moss.Repo || p.RefAudio != "/tmp/ref.wav" || p.RefText != "Reference words." {
		t.Fatalf("request %+v", p)
	}
}

func TestStudioNeedsReferenceSavedVoice(t *testing.T) {
	moss := cloneOnly(t, nil)
	a, eng, tt := newTestApp(t)
	clone := addVoice(t, a, core.Voice{Name: "Clone", Kind: core.VoiceCloned, RefText: "Words."}, 4)
	designed := addVoice(t, a, core.Voice{Name: "Design only", Kind: core.VoiceDesigned}, 0)
	builtin := core.Voice{Name: "Speaker", Kind: core.VoiceBuiltin, Speaker: "af_heart"}
	a.text = "Hello there."
	a.modelLabel = moss.Label
	a.modelChanged()
	tt.Frame()

	if ok, why := voiceUsable(&builtin, moss); ok || why == "" {
		t.Errorf("a built-in speaker on the clone-only model: %v %q", ok, why)
	}
	if ok, _ := voiceUsable(&designed, moss); ok {
		t.Error("a design without a sample on the clone-only model")
	}
	if ok, _ := voiceUsable(&clone, moss); !ok {
		t.Fatal("a cloned voice with its clip is refused")
	}
	for _, v := range a.lfVoices(moss) { // Stories / Audiobook
		if v.ref != clone.ID {
			t.Errorf("Stories offer %+v on the clone-only model", v)
		}
	}

	// The designed voice (no clip): still refused.
	a.studioVoice = designed.ID
	tt.Frame()
	if a.canGenerate() {
		t.Error("Generate allowed with a voice without a clip")
	}
	// The cloned voice: it generates with its reference.
	a.studioVoice = clone.ID
	tt.Frame()
	if !a.canGenerate() {
		t.Fatalf("refused with a saved cloned voice: %q", tt.Texts())
	}
	tt.Click("Generate")
	p := renderOf(t, eng.last(t)).Take
	if p.Model != moss.Repo || p.RefAudio == "" || p.VoiceID != clone.ID {
		t.Fatalf("request %+v", p)
	}
}

// The services refuse a recipe without a reference (core.NeedsReferenceError,
// which wraps ErrInvalid: the Local API's 400), and a render job of one
// (Dub, Workflows and Calls render through it) fails with needs_reference.
func TestServicesNeedReference(t *testing.T) {
	moss := cloneOnly(t, nil)
	a, eng, _ := newTestApp(t)
	_, err := a.lib.svc.Takes.Generate(context.Background(), core.TakeRequest{Recipe: core.Take{Text: "Hi", Model: moss.Repo}})
	var nr *core.NeedsReferenceError
	if !errors.As(err, &nr) || !errors.Is(err, core.ErrInvalid) || nr.Model != "CloneOnly Nano" {
		t.Fatalf("err %v", err)
	}
	if got := engineErrorText(err); got != needsReferenceText("“CloneOnly Nano”") {
		t.Errorf("text %q", got)
	}
	if _, _, err := a.lib.svc.Render.Preview(context.Background(), core.Take{Text: "Hi", Model: moss.Repo}); !errors.As(err, &nr) {
		t.Errorf("preview: %v", err)
	}
	if len(eng.sent) != 0 {
		t.Fatalf("sent %d", len(eng.sent))
	}

	params, _ := json.Marshal(services.RenderParams{Take: core.Take{ID: "x", Text: "Hi", Model: moss.Repo}, Output: t.TempDir() + "/x.wav"})
	_, err = a.lib.svc.Render.Executor().Run(context.Background(), jobs.Job{ID: "j", Spec: jobs.Spec{Kind: services.KindRender, Params: params}},
		func(jobs.Progress) {}, nil)
	var je *jobs.Error
	if !errors.As(err, &je) || je.Code != core.CodeNeedsReference {
		t.Fatalf("render: %v", err)
	}
	if got := jobErrorText(je); got != needsReferenceText("“CloneOnly Nano”") {
		t.Errorf("job text %q", got)
	}
	if len(eng.sent) != 0 {
		t.Fatalf("the render reached the engine: %d", len(eng.sent))
	}
}

// The engine's words: its bad_request for a model that only clones
// without a reference (named by the catalogue, or "This model"), and
// Cocoa's generic sentence from engines before the fix.
func TestEngineErrorWords(t *testing.T) {
	cloneOnly(t, nil)
	for _, c := range []struct {
		err  jobs.Error
		want string
	}{
		{jobs.Error{Code: proto.CodeBadRequest, Message: "CloneOnly-Nano-100M only clones voices: give it a reference clip (refAudio)"},
			needsReferenceText("“CloneOnly Nano”")},
		{jobs.Error{Code: proto.CodeBadRequest, Message: "CloneOnly Nano requires refAudio for voice_clone generation."},
			needsReferenceText("“CloneOnly Nano”")},
		{jobs.Error{Code: proto.CodeBadRequest, Message: "MOSS-TTS-Nano requires refAudio for voice_clone generation."},
			"This model only clones voices: it needs a reference clip or a saved cloned voice."},
		{jobs.Error{Code: proto.CodeInternal, Message: "internal: The operation couldn’t be completed. (MLXAudioTTS.MossTTSNanoError error 0.)"},
			"internal: The operation couldn’t be completed. (MLXAudioTTS.MossTTSNanoError error 0.)"},
		{jobs.Error{Code: proto.CodeInternal, Message: "The operation couldn’t be completed. (MLXAudioTTS.MossTTSNanoError error 0.)"},
			"The engine failed: MLXAudioTTS.MossTTSNanoError error 0."},
		{jobs.Error{Code: proto.CodeInternal, Message: "未能完成操作。（MLXAudioTTS.MossTTSNanoError错误0。）"},
			"The engine failed: MLXAudioTTS.MossTTSNanoError error 0."},
		{jobs.Error{Code: proto.CodeInternal, Message: "Generation failed: no audio"}, "Generation failed: no audio"},
	} {
		if got := jobErrorText(&c.err); got != c.want {
			t.Errorf("%q: %q, want %q", c.err.Message, got, c.want)
		}
	}
}

// Dub: a model that only clones offers no default voice or design presets,
// and a line cast without a reference clones its speaker.
func TestDubNeedsReference(t *testing.T) {
	moss := cloneOnly(t, nil)
	a, _, _ := newTestApp(t)
	a.dub = &dub.Project{Settings: dub.Settings{Model: moss.Repo}}
	for _, ch := range a.dubVoiceChoices() {
		if ch.ref == dub.VoiceDefault || strings.HasPrefix(ch.ref, dub.PresetPrefix) {
			t.Errorf("choice %+v for the clone-only model", ch)
		}
	}
	if caps := dubCaps(moss.Repo); !caps.NeedsReference || !caps.Clone {
		t.Errorf("caps %+v", caps)
	}
	a.dub.Settings.Model = kokoro
	if n := len(a.dubVoiceChoices()); n < 3 {
		t.Errorf("Kokoro's choices: %d", n)
	}
}

// Workflows: a Speak step on a clone-only model without a saved cloned voice is an error
// of the graph.
func TestWorkflowNeedsReference(t *testing.T) {
	moss := cloneOnly(t, nil)
	a, _, tt := newTestApp(t)
	n := &workflow.Node{ID: "s", Kind: workflow.KindSpeak, P: workflow.Params{Model: moss.Repo}}
	ds := a.wfCheck(n, "")
	if len(ds) == 0 || ds[0].Severity != workflow.Error || !strings.Contains(ds[0].Arg, "only clones voices") {
		t.Fatalf("diags %+v", ds)
	}
	clone := addVoice(t, a, core.Voice{Name: "Clone", Kind: core.VoiceCloned, RefText: "Words."}, 4)
	tt.Frame()
	n.P.Voice = clone.ID
	for _, d := range a.wfCheck(n, "") {
		if d.Severity == workflow.Error {
			t.Errorf("with a cloned voice: %+v", d)
		}
	}
	if got := wfErrorText(core.CodeNeedsReference, "CloneOnly Nano only clones voices: give it a reference clip, or a saved voice cloned from one"); got != needsReferenceText("“CloneOnly Nano”") {
		t.Errorf("wf text %q", got)
	}
}
