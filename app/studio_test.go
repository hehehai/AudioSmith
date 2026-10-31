package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// kokoro is the first catalogue entry, which the Studio starts on.
var kokoro = catalog.Builtin().Entries[0].Repo

func TestGenerateTake(t *testing.T) {
	a, eng, tt := newTestApp(t)
	a.text = "Testing one two three."
	a.voice = "zf_xiaobei"
	a.speed = 1.25
	tt.Frame()

	if err := tt.Click("Generate"); err != nil {
		t.Fatal(err)
	}
	req := eng.last(t)
	p := renderOf(t, req)
	if p.Take.Model != kokoro || p.Take.Voice != "zf_xiaobei" || p.Take.Text != "Testing one two three." || p.Take.Speed != 1.25 {
		t.Fatalf("request %+v", p)
	}
	if p.Take.Temperature != 0 || p.Take.TopP != 0 {
		t.Errorf("Kokoro takes no sampling settings, got %+v", p.Take)
	}
	if !strings.HasSuffix(p.Output, ".wav") || !strings.HasPrefix(p.Output, a.takeStore.AudioDir()) {
		t.Errorf("output %q", p.Output)
	}
	if a.job == nil {
		t.Fatal("no job after Generate")
	}

	// A second click while it runs does nothing.
	tt.Frame()
	tt.Click("Generating…")
	if len(eng.sent) != 1 {
		t.Errorf("sent %d requests while busy", len(eng.sent))
	}

	eng.progress(req.ID, "loading", -1, 0)
	eng.progress(req.ID, "generating", 0.5, 0.4)
	tt.Frame()
	if !tt.HasText("Generating…") {
		t.Errorf("status not shown: %q", tt.Texts())
	}

	finishRender(t, eng, req, 0.8)
	tt.Frame()
	if a.job != nil {
		t.Fatal("job still running after done")
	}
	if len(a.takes) != 1 || a.lastClip() == nil {
		t.Fatalf("clips %v", a.takes)
	}
	cl := a.takes[0]
	if cl.RTF() != 4 || cl.Voice != "zf_xiaobei" {
		t.Errorf("clip %+v", cl)
	}
	if !tt.HasText("Latest take") || !tt.HasText("Play") {
		t.Errorf("latest take not shown: %q", tt.Texts())
	}
	if len(a.peaks[cl.ID]) != 160 {
		t.Errorf("waveform has %d peaks", len(a.peaks[cl.ID]))
	}
	if a.loadedModel != p.Take.Model {
		t.Errorf("loaded model %q", a.loadedModel)
	}

	// The take is in the library (it survives a restart: TestDraftAndLibrarySurviveRestart).
	takes, err := a.lib.svc.Takes.List(context.Background(), core.TakeQuery{})
	if err != nil || len(takes) != 1 || takes[0].Text != "Testing one two three." || takes[0].SampleRate != 24000 {
		t.Fatalf("stored takes %v, %v", takes, err)
	}
	if _, err := os.Stat(filepath.Join(a.lib.peaks.Dir, cl.ID+".f32")); err != nil {
		t.Errorf("peaks not cached: %v", err)
	}
}

func TestShortcutGenerates(t *testing.T) {
	a, eng, tt := newTestApp(t)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyEnter)
	if len(eng.sent) != 1 || eng.sent[0].Spec.Kind != services.KindRender {
		t.Fatalf("⌘↩ sent %+v", eng.sent)
	}
	_ = a
}

func TestCannotGenerateWithoutEngineOrText(t *testing.T) {
	a, eng, tt := newTestApp(t)
	a.text = "   "
	tt.Frame()
	tt.Click("Generate")
	a.text = "Hello"
	a.engState = engineStarting
	tt.Frame()
	tt.Click("Generate")
	if len(eng.sent) != 0 {
		t.Fatalf("sent %+v", eng.sent)
	}
}

func TestModelSwitchShowsItsControls(t *testing.T) {
	a, eng, tt := newTestApp(t)
	a.setAdvancedOpen("studio", true)
	tt.Frame()
	// Kokoro: speed in view, no sampling.
	if !tt.HasText("1.00×") || !tt.HasText("Pin the seed") || tt.HasText("Temperature  0.70") {
		t.Errorf("Kokoro's controls: %q", tt.Texts())
	}
	if tt.HasText("Auto-detect") {
		t.Error("Kokoro's language follows the voice: no picker")
	}

	a.modelLabel = "Qwen3-TTS 0.6B Base (voice clone)"
	a.modelChanged()
	tt.Frame()
	if a.voice != "" || a.language != "" {
		t.Errorf("voice %q language %q after switching", a.voice, a.language)
	}
	// The recommended sampling (presets), shown; a change is the model's.
	if !tt.HasText("Auto-detect") || tt.HasText("1.00×") || !tt.HasText("Temperature  0.70") || !tt.HasText("Top P  0.80") || !tt.HasText("Recommended settings") {
		t.Errorf("controls for Qwen Base: %q", tt.Texts())
	}
	a.temperature = 0.5
	a.refAudio, a.refText = "/tmp/ref.wav", "Reference words"
	tt.Frame()
	if !tt.HasText("Clip: ref.wav") || !tt.HasText("Reference transcript") {
		t.Errorf("the clip: %q", tt.Texts())
	}
	tt.Click("Generate")
	req := renderOf(t, eng.last(t)).Take
	if req.Temperature != 0.5 || req.TopP != 0.8 || req.MaxTokens != 0 || req.Speed != 0 ||
		req.RefAudio != "/tmp/ref.wav" || req.RefText != "Reference words" || req.Language != "" {
		t.Fatalf("request %+v", req)
	}
}

// TestDraftAndLibrarySurviveRestart: the form autosaves, and the next run
// starts with it and with the takes.
func TestDraftAndLibrarySurviveRestart(t *testing.T) {
	dir := t.TempDir()
	a, _, tt := newTestAppIn(t, dir)
	lib := a.lib
	addTake(t, a, core.Take{ID: "T1", CreatedAt: time.Now(), Text: "Kept", Model: kokoro}, 0.2)
	a.text = "A script in progress"
	a.modelLabel = "Qwen3-TTS 0.6B Base (voice clone)"
	a.modelChanged()
	a.refText = "ref words"
	tt.Frame()
	tt.Frame() // unchanged: nothing more to save
	if err := lib.svc.Drafts.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	lib.close()

	b, _, tt2 := newTestAppIn(t, dir)
	if b.text != "A script in progress" || b.modelLabel != "Qwen3-TTS 0.6B Base (voice clone)" ||
		b.refText != "ref words" || b.language != "" || b.autoplay {
		t.Errorf("restored form %+v", b.toDraft())
	}
	tt2.Frame()
	if len(b.takes) != 1 || b.lastClip() == nil || !tt2.HasText("Latest take") {
		t.Errorf("takes after restart %v", b.takes)
	}
}

// Generate refuses a model that is not downloaded, and offers to download
// it there; once the download job is done, Generate works.
func TestGenerateNeedsDownload(t *testing.T) {
	a, eng, tt := newTestApp(t)
	f := useFakeHub(t, a) // an empty model folder
	tt.Frame()
	if err := tt.Click("Generate"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.job != nil || a.needDownload == nil || !tt.HasText("“Fast English voices” is not downloaded yet.") {
		t.Fatalf("not refused: job %v, %q", a.job, tt.Texts())
	}
	if len(eng.sent) != 0 {
		t.Fatalf("queued %+v", eng.sent)
	}
	if err := tt.Click("Download"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if r := eng.last(t); r.Cmd != "download" || r.Model != kokoro || r.Spec.Owner != "model:"+kokoro {
		t.Fatalf("download job %+v", r)
	}
	if a.needDownload != nil || a.jobError != "" || f.served[kokoro] != 2 {
		t.Fatalf("after the download: %v %q served %d", a.needDownload, a.jobError, f.served[kokoro])
	}
	tt.Click("Generate")
	if r := eng.last(t); r.Spec.Kind != services.KindRender || !strings.HasPrefix(r.Spec.Owner, "take:") {
		t.Fatalf("generate after download %+v", r)
	}
}

// A model whose licence needs accepting asks first, then generates.
func TestGenerateAsksForTheLicence(t *testing.T) {
	a, eng, tt := newTestApp(t)
	omni := entry(t, "omnivoice-bf16")
	a.selectModel(omni)
	a.voice = "female, young adult, british accent"
	a.duration = 3
	a.setAdvancedOpen("studio", true)
	tt.Frame()
	if !tt.HasText("Duration (seconds, 0 = natural)") || !tt.HasText("Non-commercial use only") ||
		!tt.HasText("A duration overrides the speed.") {
		t.Errorf("OmniVoice controls: %q", tt.Texts())
	}
	tt.Click("Generate")
	tt.Frame()
	if !a.licenceOpen || a.job != nil || len(eng.sent) != 0 {
		t.Fatalf("no licence dialog: open %v job %v sent %v", a.licenceOpen, a.job, eng.sent)
	}
	tt.Click("I have read the licence and will use this model only as it allows.")
	tt.Click("Accept")
	tt.Frame()
	r := renderOf(t, eng.last(t)).Take
	if r.Model != omni.Repo || r.Duration != 3 || r.Voice != a.voice || a.job == nil {
		t.Fatalf("after accepting: %+v job %v", r, a.job)
	}
	if got := a.settings.Get().Studio.Model; got != omni.ID {
		t.Errorf("default model %q", got)
	}
}

// The Studio's defaults come from the settings.
func TestStudioDefaultsFromSettings(t *testing.T) {
	dir := t.TempDir()
	a, _, _ := newTestAppIn(t, dir)
	a.updateSettings(func(s *settings.Settings) {
		s.Studio.Model = "chatterbox-turbo-8bit"
		s.Studio.Autoplay = false
	})
	a.lib.close()
	b, _, tt := newTestAppIn(t, dir)
	if b.preset().ID != "chatterbox-turbo-8bit" || b.autoplay {
		t.Errorf("fresh start: %s autoplay %v", b.preset().ID, b.autoplay)
	}
	b.navigate("/settings/studio")
	tt.Frame()
	if !tt.HasText("Default model") || !tt.HasText("English clone") {
		t.Errorf("studio settings: %q", tt.Texts())
	}
}
