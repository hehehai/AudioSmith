package catalog

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestBuiltin pins the MVP presets still offered, now data: the pickers
// must offer them in order, then OmniVoice (M0), then what M1 added; the
// seven speech models thread #43 kept.
func TestBuiltin(t *testing.T) {
	c := Builtin()
	want := []struct {
		label, repo, size string
		voices, languages []string
		speed, sampling   bool
		clone, style      bool
	}{
		{"Kokoro 82M", "mlx-community/Kokoro-82M-bf16", "≈ 481 MB", nil, nil, true, false, false, false},
		{"Qwen3-TTS 0.6B CustomVoice", "mlx-community/Qwen3-TTS-12Hz-0.6B-CustomVoice-8bit", "≈ 2 GB",
			[]string{"Vivian", "Serena", "Uncle_Fu", "Dylan", "Eric", "Ryan", "Aiden"},
			[]string{"Chinese", "English", "Japanese", "Korean", "German", "French", "Spanish", "Italian", "Portuguese", "Russian"},
			false, true, false, true},
		{"Qwen3-TTS 0.6B Base (voice clone)", "mlx-community/Qwen3-TTS-12Hz-0.6B-Base-8bit", "≈ 2 GB", nil,
			[]string{"Chinese", "English", "Japanese", "Korean", "German", "French", "Spanish", "Italian", "Portuguese", "Russian"},
			false, true, true, false},
	}
	labels := c.Labels(TaskTTS)
	// The MVP presets, then OmniVoice (M0), then what M1 added, then Custom.
	if len(labels) < len(want)+2 || labels[len(want)] != "OmniVoice" || labels[len(labels)-1] != CustomLabel {
		t.Fatalf("labels %q", labels)
	}
	for i, w := range want {
		e, ok := c.ByLabel(w.label)
		if !ok || labels[i] != w.label || e.Repo != w.repo || HumanSize(e.Size) != w.size {
			t.Errorf("%s: %+v (size %q)", w.label, e, HumanSize(e.Size))
			continue
		}
		if w.voices != nil && !slices.Equal(e.VoiceIDs(), w.voices) {
			t.Errorf("%s voices %q", w.label, e.VoiceIDs())
		}
		if !slices.Equal(e.LanguageOptions(), w.languages) {
			t.Errorf("%s languages %q", w.label, e.LanguageOptions())
		}
		if e.Has(CapSpeed) != w.speed || e.Has(CapSampling) != w.sampling || e.Has(CapClone) != w.clone || e.Has(CapVoiceStyle) != w.style {
			t.Errorf("%s capabilities %v", w.label, e.Capabilities)
		}
		if r, ok := c.ByRepo(w.repo); !ok || r.ID != e.ID {
			t.Errorf("ByRepo(%s)", w.repo)
		}
		if id, ok := c.ByID(e.ID); !ok || id.Repo != w.repo {
			t.Errorf("ByID(%s)", e.ID)
		}
	}
	// 7 since the curation (#41–#46), and VoxCPM2 (thread #165).
	if got := c.ForTask(TaskTTS); len(got) != 8 {
		t.Errorf("%d speech models offered, want 8", len(got))
	}
	if k, _ := c.ByLabel("Kokoro 82M"); len(k.Voices) != 25 || k.VoiceIDs()[0] != "af_heart" || len(k.Aux) != 2 {
		t.Errorf("kokoro voices %d aux %v", len(k.Voices), k.Aux)
	}
	for _, e := range c.Entries {
		if !e.Licence.Verified {
			t.Errorf("%s: licence not verified against the upstream model card", e.ID)
		}
	}
}

// TestOmniVoice: the bf16 checkpoint only (the quantized ones crash the
// pinned engine), non-commercial and gated behind acceptance (the
// services and the engine executors check it), offered since the engine
// runs it end to end (TestModelLifecycle on the Mac).
func TestOmniVoice(t *testing.T) {
	c := Builtin()
	e, ok := c.ByID("omnivoice-bf16")
	if !ok || e.Repo != "mlx-community/OmniVoice-bf16" || e.Hidden || e.Licence.Commercial || !e.Licence.RequiresAcceptance || e.Licence.ID != "cc-by-nc-4.0" {
		t.Fatalf("%+v", e)
	}
	if !e.Has(CapClone) || !e.Has(CapDesign) || !e.Has(CapDuration) || !e.Has(CapSpeed) {
		t.Errorf("capabilities %v", e.Capabilities)
	}
	if !slices.Contains(c.Labels(TaskTTS), e.Label) {
		t.Error("OmniVoice is not in the picker")
	}
	for _, x := range c.Entries {
		if strings.Contains(strings.ToLower(x.Repo), "omnivoice") && x.Quantization != "bf16" {
			t.Errorf("%s: only OmniVoice-bf16 loads", x.Repo)
		}
	}
	// Hidden entries stay out of pickers.
	h := *c
	h.Entries = slices.Clone(c.Entries)
	for i := range h.Entries {
		if h.Entries[i].ID == e.ID {
			h.Entries[i].Hidden = true
		}
	}
	if slices.Contains(h.Labels(TaskTTS), e.Label) || slices.ContainsFunc(h.ForTask(TaskTTS), func(x Entry) bool { return x.ID == e.ID }) {
		t.Error("a hidden entry is offered")
	}
}

// TestVoxCPM2: an optional model (thread #165): Apache, 48 kHz, offered
// but no workflow's default, after the Qwen3 and OmniVoice models (a dub
// without its default model falls back to the first clone model on disk).
func TestVoxCPM2(t *testing.T) {
	c := Builtin()
	e, ok := c.ByID("voxcpm2-8bit")
	if !ok || e.Hidden || e.Family != "voxcpm2" || e.SampleRate != 48000 || !e.Licence.Commercial || e.Licence.RequiresAcceptance {
		t.Fatalf("%+v", e)
	}
	for _, cp := range []Capability{CapClone, CapCloneAudioOnly, CapDesign, CapInstruct, CapVoiceStyle, CapSteps, CapGuidance, CapSeed} {
		if !e.Has(cp) {
			t.Errorf("no %s", cp)
		}
	}
	if e.Has(CapLanguage) || e.Has(CapVoicePrompt) || len(e.Voices) > 0 {
		t.Errorf("capabilities %v", e.Capabilities)
	}
	for _, l := range []string{"zh", "en", "vi", "th", "id", "ar", "yue"} {
		if !e.Knows(l) {
			t.Errorf("does not know %s", l)
		}
	}
	if len(e.Languages) != 31 {
		t.Errorf("%d languages", len(e.Languages))
	}
	var sum int64
	for _, f := range e.Files {
		sum += f.Size
	}
	if sum != e.Size || slices.ContainsFunc(e.Files, func(f File) bool { return strings.HasSuffix(f.Path, ".wav") }) {
		t.Errorf("files %v (sum %d, size %d)", e.Files, sum, e.Size)
	}
	tts := c.ForTask(TaskTTS)
	if i := slices.IndexFunc(tts, func(x Entry) bool { return x.ID == e.ID }); i != len(tts)-1 {
		t.Errorf("at %d of %d speech models: keep it last", i, len(tts))
	}
	for _, w := range []Workflow{WorkflowStudio, WorkflowStory, WorkflowAudiobook, WorkflowDub, WorkflowDesign} {
		for _, l := range []string{"zh", "en", "ja", "vi"} {
			for _, voice := range []bool{false, true} {
				if d, ok := c.DefaultModel(w, l, voice); ok && d.ID == e.ID {
					t.Errorf("default for %s/%s", w, l)
				}
			}
		}
	}
}

func TestCustom(t *testing.T) {
	e := Custom("me/model")
	if e.Repo != "me/model" || !e.Has(CapSpeed) || !e.Has(CapSampling) || !e.Has(CapClone) || e.Has(CapVoiceStyle) {
		t.Errorf("%+v", e)
	}
	if len(e.LanguageOptions()) != 0 || len(e.VoiceIDs()) != 0 {
		t.Errorf("custom offers no suggestions: %+v", e)
	}
}

func TestValidate(t *testing.T) {
	for name, data := range map[string]string{
		"version":    `{"version":0,"entries":[]}`,
		"dup repo":   `{"version":1,"entries":[{"id":"a","label":"A","name":"Aa","family":"f","repo":"r","tasks":["tts"],"licence":{"id":"x","name":"X","commercial":true}},{"id":"b","label":"B","name":"Bb","family":"f","repo":"r","tasks":["tts"],"licence":{"id":"x","name":"X","commercial":true}}]}`,
		"task":       `{"version":1,"entries":[{"id":"a","label":"A","name":"Aa","family":"f","repo":"r","tasks":["sing"],"licence":{"id":"x","name":"X","commercial":true}}]}`,
		"capability": `{"version":1,"entries":[{"id":"a","label":"A","name":"Aa","family":"f","repo":"r","tasks":["tts"],"capabilities":["fly"],"licence":{"id":"x","name":"X","commercial":true}}]}`,
		"licence":    `{"version":1,"entries":[{"id":"a","label":"A","name":"Aa","family":"f","repo":"r","tasks":["tts"]}]}`,
		"aux":        `{"version":1,"entries":[{"id":"a","label":"A","name":"Aa","family":"f","repo":"r","tasks":["tts"],"aux":[{"repo":"r"}],"licence":{"id":"x","name":"X","commercial":true}}]}`,
		"no name":    `{"version":1,"entries":[{"id":"a","label":"A","family":"f","repo":"r","tasks":["tts"],"licence":{"id":"x","name":"X","commercial":true}}]}`,
		"dup name":   `{"version":1,"entries":[{"id":"a","label":"A","name":"Same","family":"f","repo":"r","tasks":["tts"],"licence":{"id":"x","name":"X","commercial":true}},{"id":"b","label":"B","name":"same","family":"f","repo":"r2","tasks":["tts"],"licence":{"id":"x","name":"X","commercial":true}}]}`,
		"tag":        `{"version":1,"entries":[{"id":"a","label":"A","name":"Aa","tags":["shiny"],"family":"f","repo":"r","tasks":["tts"],"licence":{"id":"x","name":"X","commercial":true}}]}`,
		"computed":   `{"version":1,"entries":[{"id":"a","label":"A","name":"Aa","tags":["recommended"],"family":"f","repo":"r","tasks":["tts"],"licence":{"id":"x","name":"X","commercial":true}}]}`,
		"repeated":   `{"version":1,"entries":[{"id":"a","label":"A","name":"Aa","tags":["fast","fast"],"family":"f","repo":"r","tasks":["tts"],"licence":{"id":"x","name":"X","commercial":true}}]}`,
		"nc gate":    `{"version":1,"entries":[{"id":"a","label":"A","name":"Aa","family":"f","repo":"r","tasks":["tts"],"licence":{"id":"cc-by-nc-4.0","name":"CC BY-NC 4.0","commercial":false}}]}`,
	} {
		if _, err := Parse([]byte(data)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	ok := `{"version":1,"entries":[{"id":"a","label":"A","name":"Aa","family":"f","repo":"r","tasks":["tts"],"licence":{"id":"cc-by-nc-4.0","name":"CC BY-NC 4.0","commercial":false,"requiresAcceptance":true}}]}`
	if _, err := Parse([]byte(ok)); err != nil {
		t.Error(err)
	}
}

func TestHumanSize(t *testing.T) {
	for in, want := range map[int64]string{0: "", 220e6: "≈ 220 MB", 1_500_000_000: "≈ 1.5 GB", 2e9: "≈ 2 GB", 999_600_000: "≈ 1000 MB"} {
		if got := HumanSize(in); got != want {
			t.Errorf("HumanSize(%d) = %q, want %q", in, got, want)
		}
	}
	if !strings.HasPrefix(HumanSize(12e9), "≈ 12") {
		t.Error(HumanSize(12e9))
	}
}

func TestNames(t *testing.T) {
	c := Builtin()
	if got := c.DisplayName("mlx-community/Kokoro-82M-bf16"); got != "Fast English voices" {
		t.Error(got)
	}
	if got := c.TechnicalName("mlx-community/Kokoro-82M-bf16"); got != "Kokoro 82M" {
		t.Error(got)
	}
	if got := c.DisplayName("someone/My-TTS"); got != "My-TTS" {
		t.Error(got)
	}
	if got := ShortRepo("plain"); got != "plain" {
		t.Error(got)
	}
}

// Every entry and auxiliary repo pins a commit.
func TestRevisionsArePinned(t *testing.T) {
	commit := regexp.MustCompile(`^[0-9a-f]{40}$`)
	for _, e := range Builtin().Entries {
		if !commit.MatchString(e.Revision) {
			t.Errorf("%s: revision %q is not a commit", e.ID, e.Revision)
		}
		for _, a := range e.Aux {
			if !commit.MatchString(a.Revision) {
				t.Errorf("%s: aux %s: revision %q is not a commit", e.ID, a.Repo, a.Revision)
			}
		}
	}
}

func TestUnknownLanguageCode(t *testing.T) {
	c := *Builtin()
	c.Entries = slices.Clone(c.Entries)
	e := c.Entries[0]
	e.Languages = []Language{{Code: "xx-nonsense"}}
	c.Entries[0] = e
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "xx-nonsense") {
		t.Errorf("validate: %v", err)
	}
}

func TestM1Entries(t *testing.T) {
	c := Builtin()
	for id, task := range map[string]Task{DefaultSTT: TaskSTT, DefaultAlign: TaskAlign, DefaultVAD: TaskVAD, DefaultEnhance: TaskEnhance, FallbackSTT: TaskSTT, DefaultSeparate: TaskSeparate, DefaultDiarize: TaskDiarize, DefaultLLM: TaskLLM} {
		e, ok := c.ByID(id)
		if !ok || !slices.Contains(e.Tasks, task) || e.SampleRate == 0 && task != TaskLLM {
			t.Errorf("default %s: %+v", id, e)
		}
	}
	if e, _ := c.ByID(DefaultAlign); !e.Has(CapWordTimes) {
		t.Error("the aligner times words")
	}
	if e, _ := c.ByID("kokoro-82m-bf16"); e.MaxChars == 0 {
		t.Error("kokoro has a per-request limit")
	}
	if n := TextLength("Hi 你好"); n != 9 {
		t.Errorf("TextLength = %d", n)
	}
}

// TestNeedsReference: every speech model kept speaks without a
// reference: its own voices (Kokoro, CustomVoice), a default voice
// (Qwen3-TTS Base, Chatterbox Turbo's conds.safetensors), or a design
// (OmniVoice, VoiceDesign). The flag stays for models that only clone
// (MOSS-TTS-Nano was one); it needs the clone capability.
func TestNeedsReference(t *testing.T) {
	var got []string
	for _, e := range Builtin().Entries {
		if e.NeedsReference {
			got = append(got, e.ID)
		}
	}
	if len(got) != 0 {
		t.Errorf("needsReference: %q", got)
	}
	c := Catalog{Version: 1, Entries: []Entry{{ID: "x", Label: "X", Name: "Xx", Family: "x", Repo: "o/x", Tasks: []Task{TaskTTS},
		NeedsReference: true, Licence: Licence{ID: "mit", Name: "MIT", Commercial: true}}}}
	if err := c.Validate(); err == nil {
		t.Error("needsReference without clone validated")
	}
	c.Entries[0].Capabilities = []Capability{CapClone}
	if err := c.Validate(); err != nil {
		t.Error(err)
	}
}

// TestPresets: presets.json (thread #41) names live speech models, every
// workflow recommends 1–3 per language, and new users get what main
// decided on 2026-10-15.
func TestPresets(t *testing.T) {
	c := Builtin()
	p := BuiltinPresets()
	if err := p.Validate(c); err != nil {
		t.Fatal(err)
	}
	ids := func(es []Entry) []string {
		var out []string
		for _, e := range es {
			out = append(out, e.ID)
		}
		return out
	}
	for _, w := range []struct {
		w    Workflow
		lang string
		want []string
	}{
		{WorkflowStudio, "zh-Hans", []string{"qwen3-tts-0.6b-customvoice-8bit", "qwen3-tts-1.7b-base-8bit"}},
		{WorkflowStudio, "en", []string{"kokoro-82m-bf16", "qwen3-tts-1.7b-base-8bit", "chatterbox-turbo-8bit"}},
		{WorkflowStudio, "ja", []string{"kokoro-82m-bf16", "qwen3-tts-1.7b-base-8bit", "chatterbox-turbo-8bit"}},
		{WorkflowAudiobook, "zh", []string{"qwen3-tts-0.6b-base-8bit", "qwen3-tts-1.7b-base-8bit"}},
		{WorkflowDub, "en", []string{"qwen3-tts-0.6b-base-8bit", "qwen3-tts-1.7b-base-8bit"}},
		{WorkflowDesign, "zh", []string{"qwen3-tts-1.7b-voicedesign-4bit"}},
	} {
		if got := ids(c.Recommended(w.w, w.lang)); !slices.Equal(got, w.want) {
			t.Errorf("Recommended(%s, %s) = %v, want %v", w.w, w.lang, got, w.want)
		}
	}
	for _, d := range []struct {
		w         Workflow
		lang      string
		haveVoice bool
		want      string
	}{
		{WorkflowStudio, "zh-Hans", false, "qwen3-tts-0.6b-customvoice-8bit"},
		{WorkflowStudio, "zh-Hans", true, "qwen3-tts-1.7b-base-8bit"},
		{WorkflowStudio, "en", true, "kokoro-82m-bf16"},
		{WorkflowAudiobook, "zh", true, "qwen3-tts-0.6b-base-8bit"},
		{WorkflowAudiobook, "zh", false, "qwen3-tts-0.6b-customvoice-8bit"},
		{WorkflowDub, "zh", false, "qwen3-tts-0.6b-base-8bit"},
		{WorkflowStory, "en", false, "kokoro-82m-bf16"},
	} {
		if e, ok := c.DefaultModel(d.w, d.lang, d.haveVoice); !ok || e.ID != d.want {
			t.Errorf("DefaultModel(%s, %s, %v) = %s", d.w, d.lang, d.haveVoice, e.ID)
		}
	}
	cv, _ := PresetFor("qwen3-tts-0.6b-customvoice-8bit")
	if pp := cv.ParamsFor(WorkflowStudio); pp.Temperature == nil || *pp.Temperature != 0.7 || pp.TopP == nil || *pp.TopP != 0.8 {
		t.Errorf("CustomVoice params %+v", pp)
	}
	if cv.VoiceFor("zh-Hans") != "Serena" {
		t.Errorf("CustomVoice voice %q", cv.VoiceFor("zh-Hans"))
	}
	ov, _ := PresetFor("omnivoice-bf16")
	if pp := ov.ParamsFor(WorkflowStudio); *pp.Guidance != 1.5 || *pp.Steps != 32 {
		t.Errorf("OmniVoice params %+v", pp)
	}
	if pp := ov.ParamsFor(WorkflowDub); *pp.Steps != 16 || *pp.Guidance != 1.5 {
		t.Errorf("OmniVoice dub params %+v", pp)
	}
}

// TestRemoved: the six speech models thread #43 removed are gone from
// the entries and the pickers, and every name they had (id, repo, short
// repo, label, any case) maps to its replacement; a cloning request of
// Marvis, Soprano or Pocket goes to Chatterbox Turbo. Kokoro's Chinese
// speakers stay hidden.
func TestRemoved(t *testing.T) {
	c := Builtin()
	want := []struct{ id, repo, by, cloneBy string }{
		{"kitten-tts-mini-0.8", "mlx-community/kitten-tts-mini-0.8", "kokoro-82m-bf16", "kokoro-82m-bf16"},
		{"moss-tts-nano-100m", "mlx-community/MOSS-TTS-Nano-100M", "qwen3-tts-0.6b-base-8bit", "qwen3-tts-0.6b-base-8bit"},
		{"chatterbox-8bit", "mlx-community/chatterbox-8bit", "chatterbox-turbo-8bit", "chatterbox-turbo-8bit"},
		{"marvis-tts-250m-v0.2-8bit", "Marvis-AI/marvis-tts-250m-v0.2-MLX-8bit", "kokoro-82m-bf16", "chatterbox-turbo-8bit"},
		{"soprano-80m-bf16", "mlx-community/Soprano-80M-bf16", "kokoro-82m-bf16", "chatterbox-turbo-8bit"},
		{"pocket-tts", "mlx-community/pocket-tts", "kokoro-82m-bf16", "chatterbox-turbo-8bit"},
	}
	if len(c.Removed) != len(want)+5 {
		t.Fatalf("%d removed", len(c.Removed))
	}
	for _, w := range want {
		if _, ok := c.ByID(w.id); ok {
			t.Errorf("%s is still an entry", w.id)
		}
		r, ok := c.RemovedModel(w.id)
		if !ok || r.Repo != w.repo {
			t.Fatalf("%s: %+v", w.id, r)
		}
		for _, ref := range []string{w.id, w.repo, strings.ToUpper(w.repo), ShortRepo(w.repo), r.Label, " " + r.Label + " "} {
			if e, rr, ok := c.Replace(ref, false); !ok || e.ID != w.by || rr.ID != w.id {
				t.Errorf("Replace(%q) = %s", ref, e.ID)
			}
			if e, _, ok := c.Replace(ref, true); !ok || e.ID != w.cloneBy {
				t.Errorf("Replace(%q, clone) = %s", ref, e.ID)
			}
		}
		if slices.Contains(c.Labels(TaskTTS), r.Label) {
			t.Errorf("%s is still offered", w.id)
		}
		if c.DisplayName(w.repo) != r.Label {
			t.Errorf("DisplayName(%s) = %q", w.repo, c.DisplayName(w.repo))
		}
	}
	for _, ref := range []string{"", "kokoro-82m-bf16", "mlx-community/Kokoro-82M-bf16", "someone/else", "parakeet-tdt-0.6b-v3", "Gemma 4 E2B", "htdemucs"} {
		if _, _, ok := c.Replace(ref, false); ok {
			t.Errorf("Replace(%q) maps", ref)
		}
	}
	// Repos shared with kept models stay: Kokoro's G2P (Kitten's too) and
	// S3TokenizerV2 (Chatterbox Turbo's).
	pocket, _ := c.RemovedModel("pocket-tts")
	if !slices.Equal(pocket.Repos(), []string{"mlx-community/pocket-tts", "kyutai/moshiko-pytorch-bf16"}) {
		t.Errorf("pocket repos %v", pocket.Repos())
	}
	for _, r := range c.Removed {
		for _, repo := range r.Repos() {
			if c.UsesRepo(repo) {
				t.Errorf("%s: %s is still used", r.ID, repo)
			}
		}
	}
	if !c.UsesRepo("beshkenadze/kitten-tts-g2p") || !c.UsesRepo("mlx-community/S3TokenizerV2") {
		t.Error("shared aux repos")
	}
	k, _ := c.ByID("kokoro-82m-bf16")
	if slices.ContainsFunc(k.VoiceIDs(), func(v string) bool { return strings.HasPrefix(v, "z") }) || !slices.Contains(k.AllVoiceIDs(), "zf_xiaobei") || slices.Contains(k.VoiceIDs(), "jf_alpha") || !slices.Contains(k.AllVoiceIDs(), "jf_alpha") || len(k.VoiceIDs()) != 15 {
		t.Errorf("kokoro voices %v", k.VoiceIDs())
	}
	// #91's weakest two are hidden but still named; Ryan is not offered in
	// Chinese (#94).
	for _, v := range []string{"af_nicole", "bm_lewis"} {
		if slices.Contains(k.VoiceIDs(), v) || !slices.Contains(k.AllVoiceIDs(), v) {
			t.Errorf("%s offered or gone", v)
		}
	}
	cv, _ := c.ByID("qwen3-tts-0.6b-customvoice-8bit")
	if slices.Contains(cv.VoiceIDsIn("zh-Hans"), "Ryan") || !slices.Contains(cv.VoiceIDsIn("en"), "Ryan") || !slices.Contains(cv.VoiceIDs(), "Ryan") {
		t.Errorf("Ryan: zh %v, en %v", cv.VoiceIDsIn("zh-Hans"), cv.VoiceIDsIn("en"))
	}
	if !slices.Equal(k.VoiceIDsIn("zh-Hans"), k.VoiceIDs()) {
		t.Error("Kokoro's speakers differ by interface language")
	}
	for name, edit := range map[string]func(*Catalog){
		"replaced by a removed model":   func(b *Catalog) { b.Removed[0].ReplacedBy = "moss-tts-nano-100m" },
		"clone replacement can't clone": func(b *Catalog) { b.Removed[3].CloneReplacedBy = "kokoro-82m-bf16" },
		"no replacement":                func(b *Catalog) { b.Removed[0].ReplacedBy = "" },
		"an entry's repo":               func(b *Catalog) { b.Removed[0].Repo = "mlx-community/Kokoro-82M-bf16" },
		"a shared aux repo":             func(b *Catalog) { b.Removed[0].Aux = []string{"beshkenadze/kitten-tts-g2p"} },
	} {
		bad := *c
		bad.Removed = slices.Clone(c.Removed)
		edit(&bad)
		if bad.Validate() == nil {
			t.Errorf("%s validates", name)
		}
	}
}

// TestRemovedAnalysis: thread #46 removed Parakeet (→ Qwen3-ASR, or
// Whisper for a language Qwen3-ASR does not know), Mel-RoFormer (→
// HTDemucs) and Gemma 4 E4B, E2B and Qwen3 1.7B (→ Qwen3 4B): by every
// name, only for work of their task; no picker offers them; the one
// language model left is the default.
func TestRemovedAnalysis(t *testing.T) {
	c := Builtin()
	want := []struct {
		id   string
		task Task
		by   string
	}{
		{"parakeet-tdt-0.6b-v3", TaskSTT, DefaultSTT},
		{"mel-roformer-kim-vocal-2", TaskSeparate, DefaultSeparate},
		{"gemma-4-e4b-it-4bit", TaskLLM, DefaultLLM},
		{"gemma-4-e2b-it-4bit", TaskLLM, DefaultLLM},
		{"qwen3-1.7b-4bit", TaskLLM, DefaultLLM},
	}
	for _, w := range want {
		if _, ok := c.ByID(w.id); ok {
			t.Errorf("%s is still an entry", w.id)
		}
		r, ok := c.RemovedModel(w.id)
		if !ok || r.Did() != w.task || r.ReplacedBy != w.by {
			t.Fatalf("%s: %+v", w.id, r)
		}
		for _, ref := range []string{w.id, r.Repo, strings.ToLower(r.Repo), ShortRepo(r.Repo), r.Label} {
			if e, rr, ok := c.ReplaceTask(ref, w.task, ""); !ok || e.ID != w.by || rr.ID != w.id {
				t.Errorf("ReplaceTask(%q) = %s %v", ref, e.ID, ok)
			}
			if _, _, ok := c.ReplaceTask(ref, TaskTTS, ""); ok {
				t.Errorf("ReplaceTask(%q, tts) maps", ref)
			}
		}
		for _, e := range c.ForTask(w.task) {
			if e.ID == w.id {
				t.Errorf("%s is offered", w.id)
			}
		}
		if c.DisplayName(r.Repo) != r.Label {
			t.Errorf("DisplayName(%s) = %q", r.Repo, c.DisplayName(r.Repo))
		}
	}
	// Parakeet's European languages Qwen3-ASR lacks go to Whisper.
	for lang, id := range map[string]string{"": DefaultSTT, "en": DefaultSTT, "de": DefaultSTT, "bg": FallbackSTT, "uk": FallbackSTT, "sk-SK": FallbackSTT} {
		if e, _, ok := c.ReplaceTask("parakeet-tdt-0.6b-v3", TaskSTT, lang); !ok || e.ID != id {
			t.Errorf("parakeet in %q → %s", lang, e.ID)
		}
	}
	if l := c.ForTask(TaskLLM); len(l) != 1 || l[0].ID != DefaultLLM {
		t.Errorf("language models %v", l)
	}
	if l := c.ForTask(TaskSeparate); len(l) != 1 || l[0].ID != DefaultSeparate {
		t.Errorf("separation models %v", l)
	}
	if l := c.ForTask(TaskSTT); len(l) != 3 || l[0].ID != DefaultSTT || l[1].ID != FallbackSTT || l[2].ID != DefaultSpeakers {
		t.Errorf("speech-to-text models %v", l)
	}
	bad := *c
	bad.Removed = slices.Clone(c.Removed)
	bad.Removed[len(bad.Removed)-1].ReplacedBy = "kokoro-82m-bf16" // a speech model can't replace a language model
	if bad.Validate() == nil {
		t.Error("an LLM replaced by a TTS model validates")
	}
}

// TestSTTFor: Qwen3-ASR transcribes Chinese, English, detection and its
// other 28 languages; Whisper the rest, and every translation into
// English.
func TestSTTFor(t *testing.T) {
	c := Builtin()
	for lang, id := range map[string]string{
		"": DefaultSTT, "auto": DefaultSTT, "zh": DefaultSTT, "zh-Hans": DefaultSTT, "zh-TW": DefaultSTT, "en": DefaultSTT, "en-GB": DefaultSTT,
		"yue": DefaultSTT, "ja": DefaultSTT, "ko": DefaultSTT, "fr": DefaultSTT, "de": DefaultSTT, "ru": DefaultSTT, "fil": DefaultSTT,
		"sw": FallbackSTT, "uk": FallbackSTT, "he": FallbackSTT, "bg": FallbackSTT, "cy": FallbackSTT,
	} {
		if e := c.STTFor(lang, false); e.ID != id {
			t.Errorf("STTFor(%q) = %s, want %s", lang, e.ID, id)
		}
	}
	for _, lang := range []string{"", "zh", "en", "sw"} {
		if e := c.STTFor(lang, true); e.ID != FallbackSTT || !e.Has(CapTranslate) {
			t.Errorf("STTFor(%q, translate) = %s", lang, e.ID)
		}
	}
	q, _ := c.ByID(DefaultSTT)
	if len(q.Languages) != 30 || q.Has(CapTranslate) {
		t.Errorf("qwen3-asr: %d languages", len(q.Languages))
	}
	w, _ := c.ByID(FallbackSTT)
	if !slices.Contains(w.Tags, TagFallback) || slices.Contains(q.Tags, TagFallback) {
		t.Errorf("tags %v %v", w.Tags, q.Tags)
	}
}

// TestSpeakersModel: MOSS-Transcribe-Diarize (thread #48) is a pinned,
// Apache-2.0, speech-to-text entry that labels speakers; nothing else
// does, and it never answers for the default speech recognition.
func TestSpeakersModel(t *testing.T) {
	c := Builtin()
	e, ok := c.SpeakersModel()
	if !ok || e.Repo != "vanch007/mlx-MOSS-Transcribe-Diarize-8bit" || e.Family != "moss_transcribe_diarize" || len(e.Revision) != 40 {
		t.Fatalf("speakers model %+v", e)
	}
	if !slices.Contains(e.Tasks, TaskSTT) || slices.Contains(e.Tasks, TaskDiarize) || e.Has(CapLanguage) || e.Has(CapWordTimes) {
		t.Errorf("tasks %v capabilities %v", e.Tasks, e.Capabilities)
	}
	if e.Licence.ID != "apache-2.0" || !e.Licence.Commercial || !e.Licence.Verified || e.Licence.RequiresAcceptance {
		t.Errorf("licence %+v", e.Licence)
	}
	var sum int64
	for _, f := range e.Files {
		sum += f.Size
		if strings.HasSuffix(f.Path, ".safetensors") && len(f.SHA256) != 64 {
			t.Errorf("%s: no hash", f.Path)
		}
	}
	if sum != e.Size || e.Size < 1_200_000_000 || e.Size > 1_300_000_000 {
		t.Errorf("size %d, files %d", e.Size, sum)
	}
	if e.DisplayName() != "Transcription with speakers" || e.Tags[0] != TagSpeakers {
		t.Errorf("name %q tags %v", e.DisplayName(), e.Tags)
	}
	for _, x := range c.Entries {
		if x.Has(CapSpeakers) != (x.ID == DefaultSpeakers) {
			t.Errorf("%s: speakers %v", x.ID, x.Has(CapSpeakers))
		}
	}
	if !c.LabelsSpeakers(e.Repo) || !c.LabelsSpeakers(e.ID) || c.LabelsSpeakers("mlx-community/Qwen3-ASR-0.6B-4bit") {
		t.Error("LabelsSpeakers")
	}
	for _, lang := range []string{"", "zh", "en", "ja", "sw"} {
		if s := c.STTFor(lang, false); s.ID == DefaultSpeakers {
			t.Errorf("STTFor(%q) = the speakers model", lang)
		}
	}
	if !e.Knows("zh") || !e.Knows("en") || !e.Knows("") || e.Knows("sw") {
		t.Error("languages")
	}
}

// TestSpeakerLanguage (#83): a built-in speaker's language is the one it
// speaks (Kokoro's), else its native one (Qwen3-TTS CustomVoice's, from
// the model card); unknown otherwise. Native languages do not reach the
// text pipeline's per-voice languages (they speak the others too).
func TestSpeakerLanguage(t *testing.T) {
	c := Builtin()
	cv, _ := c.ByID("qwen3-tts-0.6b-customvoice-8bit")
	kok, _ := c.ByID("kokoro-82m-bf16")
	for _, x := range []struct {
		e        Entry
		id, want string
	}{{cv, "Vivian", "zh"}, {cv, "Serena", "zh"}, {cv, "Uncle_Fu", "zh"}, {cv, "Dylan", "zh"}, {cv, "Eric", "zh"},
		{cv, "Ryan", "en"}, {cv, "Aiden", "en"}, {kok, "af_heart", "en"}, {cv, "Nobody", ""}} {
		if got := x.e.SpeakerLanguage(x.id); got != x.want {
			t.Errorf("%s: %q, want %q", x.id, got, x.want)
		}
	}
	for _, v := range cv.Voices {
		if v.Language != "" || v.Native == "" || v.Gender == "" {
			t.Errorf("%+v", v)
		}
	}
}
