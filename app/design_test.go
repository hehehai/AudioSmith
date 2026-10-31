package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/hub"
	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
)

// withDesignCatalog offers the catalogue's Qwen3-TTS VoiceDesign entry,
// hidden until the Mac smoke suite passes, to the voice pages.
func withDesignCatalog(t *testing.T) catalog.Entry {
	t.Helper()
	c := *catalog.Builtin()
	c.Entries = append([]catalog.Entry(nil), c.Entries...)
	var vdEntry catalog.Entry
	for i, e := range c.Entries {
		if e.Family == "qwen3_tts" && e.Has(catalog.CapDesign) {
			c.Entries[i].Hidden = false
			vdEntry = c.Entries[i]
		}
	}
	if vdEntry.ID == "" {
		t.Fatal("no Qwen3-TTS VoiceDesign entry in the catalogue")
	}
	saved := voiceCatalog
	voiceCatalog = func() *catalog.Catalog { return &c }
	t.Cleanup(func() { voiceCatalog = saved })
	return vdEntry
}

func TestDesignPage(t *testing.T) {
	a, eng := newVoiceTestApp(t)
	tt := ui.NewTester(a.view, 1200, 1800)
	d := &a.design
	a.navigate("/design")
	tt.Frame()
	// The recommended designer is the default (presets: Qwen3-TTS
	// VoiceDesign); this test goes on with OmniVoice.
	if e, _ := voiceCatalog().ByLabel(d.model); e.ID != "qwen3-tts-1.7b-voicedesign-4bit" {
		t.Errorf("default designer %q", d.model)
	}
	d.model = "OmniVoice"
	d.detailsOpen = true // 细节与模型: the model, what it hears, the seed
	tt.Frame()
	if !tt.HasText("Describe the voice") || !strings.Contains(strings.Join(tt.Texts(), "|"), "Sent to “Multilingual all-rounder”") {
		t.Fatalf("design page: %q", tt.Texts())
	}

	// The description maps to attributes, shown as chips.
	d.description = "a deep elderly British man, very calm, purple"
	tt.Frame()
	if d.attrs.Gender != "male" || d.attrs.Age != "elderly" || d.attrs.Accent != "british" || d.attrs.Pitch == "" {
		t.Fatalf("parsed %+v", d.attrs)
	}
	texts := strings.Join(tt.Texts(), "|")
	if !strings.Contains(texts, "British → English accent: British") || !strings.Contains(texts, "Couldn't map: purple") {
		t.Errorf("chips: %q", texts)
	}
	// A pick holds while the description keeps its word on that category…
	d.pick(vd.Age, "middle_aged")
	d.description = "a deep elderly British man, very calm, slightly raspy"
	tt.Frame()
	if d.attrs.Age != "middle_aged" || !d.attrs.HasStyle("raspy") {
		t.Errorf("pick lost: %+v", d.attrs)
	}
	// …and goes when the description changes it.
	d.description = "a deep young British man, very calm, slightly raspy"
	tt.Frame()
	if d.attrs.Age != "young_adult" {
		t.Errorf("description ignored: %+v", d.attrs)
	}
	// A dialect clears the accent, with a note.
	a.designCleared(d.pick(vd.Dialect, "sichuan"))
	tt.Frame()
	if d.attrs.Accent != "" || !tt.HasText("Cleared English accent: British: it can't go with your pick.") {
		t.Errorf("dialect: %+v %q", d.attrs, tt.Texts())
	}
	// Reset to description drops the picks.
	tt.Click("Reset to description")
	if d.attrs.Dialect != "" || d.attrs.Accent != "british" {
		t.Errorf("reset: %+v", d.attrs)
	}

	// OmniVoice can't say "calm" or "raspy": the page says so.
	tt.Frame()
	if !strings.Contains(strings.Join(tt.Texts(), "|"), "can't express") {
		t.Errorf("dropped note missing: %q", tt.Texts())
	}

	// Generate candidates (after the licence): three takes, the first
	// with the draft's seed.
	d.seed = 777
	d.script = "Good evening."
	tt.Frame()
	tt.Click("Generate candidates")
	tt.Frame()
	if !a.licenceOpen {
		t.Fatal("no licence dialog for OmniVoice")
	}
	acceptLicences(t, a)
	a.licenceOpen = false
	tt.Frame()
	n := len(eng.sent)
	tt.Click("Generate candidates")
	if len(eng.sent) != n+designCandidates || len(d.cands) != designCandidates {
		t.Fatalf("candidates: %d sent, %d candidates", len(eng.sent)-n, len(d.cands))
	}
	for i, req := range eng.sent[n:] {
		if req.Model != omni || req.Seed == nil || !strings.Contains(req.Voice, "british accent") || req.Text != "Good evening." {
			t.Fatalf("request %d %+v", i, req.SynthesizeParams)
		}
		if i == 0 && *req.Seed != 777 || i > 0 && *req.Seed == 777 {
			t.Errorf("candidate %d seed %d", i, *req.Seed)
		}
		writeTestWAV(t, req.Output.Path, 24000, 1.5)
		eng.succeed(req.ID, proto.SynthesizeResult{Output: req.Output.Path, SampleRate: 24000, AudioSeconds: 1.5, Seconds: 1, Seed: req.Seed})
	}
	tt.Frame()
	if a.designStep() != designPickStep || !tt.HasText("Candidate 3") || tt.HasText("Save voice") {
		t.Fatalf("pick step: %d %q", a.designStep(), tt.Texts())
	}
	// Pick the second: its seed becomes the draft's.
	second := d.cands[1].seed
	a.pickCandidate(1)
	a.pickCandidate(0) // …then the first after all
	tt.Frame()
	if int64(d.seed) != 777 || a.designStep() != designSaveStep || second == 777 {
		t.Fatalf("picked: seed %v step %d", d.seed, a.designStep())
	}

	// Save: the picked candidate becomes the voice's sample; then the
	// hand-offs.
	d.saveName = "Old Gent"
	tt.Frame()
	if err := tt.Click("Save voice"); err != nil {
		t.Fatal(err)
	}
	v := a.findVoice(d.linked)
	if v == nil || v.Kind != core.VoiceDesigned || v.Seed != 777 || v.Sample != "sample.wav" || v.CreatedWith != omni ||
		v.Design != d.description || v.SampleText != "Good evening." {
		t.Fatalf("saved %+v", v)
	}
	var attrs vd.Attributes
	json.Unmarshal(v.DesignAttrs, &attrs)
	if attrs.Accent != "british" || attrs.Gender != "male" {
		t.Errorf("attrs %+v", attrs)
	}
	tt.Frame()
	if a.designStep() != designDoneStep || !tt.HasText("Saved to My voices") || !tt.HasText("Use in Text to speech") {
		t.Errorf("done: %q", tt.Texts())
	}
	// Generating again while linked records the voice.
	tt.Click("Generate again")
	if req := eng.last(t); req.Cmd != "synthesize" {
		t.Fatal(req)
	}
	tt.Frame()
	if !tt.HasText("Design · Old Gent") {
		t.Errorf("linked: %q", tt.Texts())
	}
	a.pickCandidate(0)
	tt.Frame()
	if !tt.HasText("Update voice") {
		t.Errorf("update: %q", tt.Texts())
	}
	// A new seed unlinks the draft.
	tt.Click("New seed")
	tt.Frame()
	if d.linked != "" || tt.HasText("Design · Old Gent") {
		t.Error("still linked after a new seed")
	}
	// Open it again from its editor.
	a.openVoiceEditor(v.ID)
	tt.Frame()
	tt.Click("Open in Design")
	if a.router.Path() != "/voices/new/design" || d.linked != v.ID || int64(d.seed) != 777 {
		t.Errorf("open in design: %s %v", d.linked, d.seed)
	}

	// A preset chip replaces the recipe: its description fills the prompt.
	tt.Frame()
	tt.Click("Narrator")
	if d.description != "Calm, low and warm: audiobooks and long reads." || d.attrs.Age != "middle_aged" || d.preset != "narrator" {
		t.Errorf("preset: %+v", d)
	}
}

func TestDesignModels(t *testing.T) {
	if ms := designModels(); len(ms) != 2 || ms[0].Repo != omni { // OmniVoice, and Qwen3 VoiceDesign since the M1 smoke
		t.Fatalf("built-in design models %v", ms)
	}
	vdm := withDesignCatalog(t)
	a, _ := newVoiceTestApp(t)
	tt := ui.NewTester(a.view, 1200, 1800)
	d := &a.design
	if ms := designModels(); len(ms) != 2 || ms[1].Repo != vdm.Repo {
		t.Fatalf("design models %v", ms)
	}
	// Not downloaded here.
	os.RemoveAll(hub.RepoDir(a.lib.settings.Get().Models.CacheDir, vdm.Repo))
	a.navigate("/design")
	d.model = vdm.Label
	d.description = "a cheerful young woman, bright voice"
	d.detailsOpen = true
	tt.Frame()
	texts := strings.Join(tt.Texts(), "|")
	if !strings.Contains(texts, "This model reads your description as written") || !strings.Contains(texts, "a cheerful young woman, bright voice") {
		t.Errorf("freeform: %q", texts)
	}
	// It isn't downloaded here: the error offers the download.
	tt.Click("Generate candidates")
	tt.Frame()
	j := a.candJob(0)
	if j == nil || j.needDownload == nil || !tt.HasText("Download") {
		t.Fatalf("not downloaded: %+v %q", j, tt.Texts())
	}
	// No model that designs: the page says where to get one.
	voiceCatalog = func() *catalog.Catalog {
		c := *catalog.Builtin()
		c.Entries = c.Entries[:1]
		return &c
	}
	tt.Frame()
	if !tt.HasText("No model here can design voices. Download one that can, such as “Voice design”, in Settings → Models & network.") {
		t.Errorf("no models: %q", tt.Texts())
	}
}

func TestDesignSampleOnDemand(t *testing.T) {
	a, eng := newVoiceTestApp(t)
	acceptLicences(t, a)
	tt := ui.NewTester(a.view, 1200, 1800)
	v := addVoice(t, a, core.Voice{Name: "Kid", Kind: core.VoiceDesigned, Language: "zh",
		DesignAttrs: designAttrs(vd.Attributes{Age: "child", Dialect: "sichuan"}), Seed: 5}, 0)
	a.navigate("/voices")
	tt.Frame()
	a.playVoice(a.findVoice(v.ID))
	req := eng.last(t)
	if req.Model != omni || req.Text != sampleLine("zh") || !strings.Contains(req.Voice, "四川话") || *req.Seed != 5 {
		t.Fatalf("sample request %+v", req.SynthesizeParams)
	}
	writeTestWAV(t, req.Output.Path, 24000, 2)
	eng.succeed(req.ID, proto.SynthesizeResult{Output: req.Output.Path, SampleRate: 24000, AudioSeconds: 2, Seconds: 1})
	got := a.findVoice(v.ID)
	if got.Sample != "sample.wav" || got.SampleText != sampleLine("zh") || got.CreatedWith != omni {
		t.Fatalf("sample %+v", got)
	}
	// With a sample, a model that only clones can speak with it.
	base, _ := catalog.Builtin().ByRepo(qwenBase)
	if ok, _ := voiceUsable(got, base); !ok {
		t.Error("sample not usable for cloning")
	}
	tk := core.Take{Model: qwenBase}
	a.fillVoice(&tk, got, base)
	if tk.RefAudio != a.voicePath(got, "sample.wav") || tk.RefText != sampleLine("zh") {
		t.Errorf("clone of the sample %+v", tk)
	}
}
