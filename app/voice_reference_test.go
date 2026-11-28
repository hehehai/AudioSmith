package main

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/dsptest"
	"github.com/hehehai/AudioSmith/internal/dsp/loudness"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/services"
	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
)

// writeTalk writes speech-like audio: phrases of ten syllables at the
// pitches f0s (a lively voice moves), with 0.3 s between them, edges of
// lead seconds.
func writeTalk(t *testing.T, path string, lead float64, f0s ...float64) {
	t.Helper()
	parts := []dsp.Buffer{dsptest.Silence(24000, 1, lead)}
	for _, f := range f0s {
		parts = append(parts, dsptest.Syllables(24000, 10, f, 0.2, 0.22, 0, -8), dsptest.Silence(24000, 1, 0.3))
	}
	parts = append(parts, dsptest.Silence(24000, 1, lead))
	if err := wav.WriteFile(path, dsptest.Concat(parts...), wav.PCM16, wav.Options{}); err != nil {
		t.Fatal(err)
	}
}

// attrsOf are a description's attributes, as Design saves them.
func attrsOf(desc string) json.RawMessage {
	b, _ := json.Marshal(vd.Parse(desc).Attrs.Canonical())
	return b
}

// refRenders are the render jobs sent since n requests had been sent.
func refRenders(t *testing.T, eng *fakeEngine, from int) []sentReq {
	t.Helper()
	var out []sentReq
	for _, r := range eng.sent[from:] {
		if r.Spec.Kind == services.KindRender {
			out = append(out, r)
		}
	}
	return out
}

// finishWith finishes render r with audio write makes.
func finishWith(t *testing.T, eng *fakeEngine, r sentReq, write func(path string)) {
	t.Helper()
	p := renderOf(t, r)
	write(p.Output)
	b, _, _ := wav.ReadFile(p.Output)
	eng.succeed(r.ID, services.RenderResult{Output: p.Output, SampleRate: 24000, Channels: 1, AudioSeconds: b.Seconds(), Render: *p.Take.Render})
}

// TestPortableSpeaker: a built-in speaker made portable says three
// reference texts; the takes are tidied, heard and checked, and the best
// (not too short, heard right) is saved as a cloned voice "<name>
// (approximate)" that the models that clone can speak with; the
// candidates' takes are gone.
func TestPortableSpeaker(t *testing.T) {
	simulateDarwin(t)
	a, eng, tt := newTestApp(t)
	cv := entry(t, "qwen3-tts-0.6b-customvoice-8bit")
	from := len(eng.sent)
	a.makeSpeakerPortable(cv, "Serena")
	rs := refRenders(t, eng, from)
	// Serena is a Chinese speaker (the catalogue's "native"): Chinese
	// texts, whatever the UI's language (English here).
	texts := vd.ReferenceTexts("zh", vd.RegisterAdult)
	if len(rs) != refCandidates {
		t.Fatalf("%d renders", len(rs))
	}
	for i, r := range rs {
		p := renderOf(t, r)
		if p.Take.Model != cv.Repo || p.Take.Voice != "Serena, "+vd.ConversationalStyle("zh") || p.Take.Text != texts[i] || p.Take.Render == nil {
			t.Fatalf("render %d %+v", i, p.Take)
		}
	}
	// One is too short, one is heard wrong.
	finishWith(t, eng, rs[0], func(p string) { writeTalk(t, p, 0.4, 180, 260) })
	finishWith(t, eng, rs[1], func(p string) { writeTalk(t, p, 0.6, 180, 230, 280, 200, 250) })
	finishWith(t, eng, rs[2], func(p string) { writeTalk(t, p, 0.4, 180, 230, 280, 200, 250) })
	for i, heard := range []string{texts[0], texts[1], "完全不同的一句话。"} {
		if got := answerTranscribe(t, eng, heard); !strings.Contains(got.Audio, "refcand") {
			t.Errorf("heard %d: %s", i, got.Audio)
		}
	}
	tt.Frame()
	var v *core.Voice
	for i := range a.voices {
		if isPortable(&a.voices[i]) {
			v = &a.voices[i]
		}
	}
	if v == nil || v.Name != "Serena (approximate)" || v.Kind != core.VoiceCloned || v.RefAudio == "" || v.RefText != texts[1] {
		t.Fatalf("portable voice %+v", v)
	}
	if v.RefOriginal == "" || v.RefCheck == nil || v.RefCheck.Grade == "" || v.RefSeconds >= v.RefCheck.OriginalSeconds-0.3 {
		t.Errorf("check %+v, original %q, %.1f s", v.RefCheck, v.RefOriginal, v.RefSeconds)
	}
	if b, _, err := wav.ReadFile(a.voicePath(v, v.RefAudio)); err != nil || math.Abs(loudness.Integrated(b)+18) > 0.3 {
		t.Errorf("not evened out: %v", err)
	}
	base := entry(t, "qwen3-tts-1.7b-base-8bit")
	if ok, why := voiceUsable(v, base); !ok {
		t.Errorf("not usable on 1.7B Base: %s", why)
	}
	if voiceKindLabel(v) != "Portable copy (approximate clone)" {
		t.Errorf("kind %q", voiceKindLabel(v))
	}
	takes, _ := a.lib.svc.Takes.List(context.Background(), core.TakeQuery{})
	if len(takes) != 0 {
		t.Errorf("candidates kept: %d takes", len(takes))
	}
	// Ryan is an English speaker; a saved built-in voice without a
	// language takes its speaker's.
	from = len(eng.sent)
	a.makeSpeakerPortable(cv, "Ryan")
	if p := renderOf(t, refRenders(t, eng, from)[0]); p.Take.Text != vd.ReferenceTexts("en", vd.RegisterAdult)[0] || p.Take.Language != "English" {
		t.Errorf("Ryan: %q %q", p.Take.Text, p.Take.Language)
	}
	sv := addVoice(t, a, core.Voice{Name: "Vivian", Kind: core.VoiceBuiltin, Model: cv.Repo, Speaker: "Vivian"}, 0)
	from = len(eng.sent)
	a.makePortable(&sv)
	if p := renderOf(t, refRenders(t, eng, from)[0]); p.Take.Text != texts[0] || p.Take.Language != "Chinese" {
		t.Errorf("saved Vivian: %q %q", p.Take.Text, p.Take.Language)
	}
}

// TestPortableHints: a saved built-in voice on a model that clones says
// how to use it there, and the Studio offers Make portable; a designed
// voice with a reference becomes portable without rendering.
func TestPortableHints(t *testing.T) {
	a, eng, tt := newTestApp(t)
	kok := entry(t, "kokoro-82m-bf16")
	v := addVoice(t, a, core.Voice{Name: "Heart", Kind: core.VoiceBuiltin, Model: kok.Repo, Speaker: "af_heart", Language: "en"}, 0)
	base := entry(t, "qwen3-tts-1.7b-base-8bit")
	if ok, why := voiceUsable(&v, base); ok || !strings.Contains(why, "portable copy") {
		t.Errorf("usable %v: %q", ok, why)
	}
	a.selectModel(base)
	a.studioVoice = v.ID
	tt.Frame()
	if err := tt.Click("Make portable"); err != nil {
		t.Fatalf("%v: %q", err, tt.Texts())
	}
	if p := renderOf(t, eng.last(t)); p.Take.Model != kok.Repo || p.Take.Voice != "af_heart" {
		t.Errorf("render %+v", p.Take)
	}
	// A designed voice with a reference: heard once (#93), then copied
	// without rendering.
	d := addVoice(t, a, core.Voice{Name: "Mom", Kind: core.VoiceDesigned, Design: "a young mother", Language: "zh", RefText: "参考的文字。"}, 11)
	n := len(eng.sent)
	a.makePortable(&d)
	answerTranscribe(t, eng, "参考的文字。")
	tt.Frame()
	if len(eng.sent) != n+1 || !slices.ContainsFunc(a.voices, func(x core.Voice) bool { return x.RefText == "参考的文字。" && isPortable(&x) }) {
		t.Errorf("designed portable: %d sent, voices %+v", len(eng.sent)-n, a.voices)
	}
	if g := a.findVoice(d.ID); g.RefCheck == nil || !g.RefCheck.TextChecked {
		t.Errorf("not checked: %+v", g.RefCheck)
	}
}

// answerRefCandidates finishes the reference renders sent since from:
// candidate k lively at pitch f0s[k] (Hz), heard right.
func answerRefCandidates(t *testing.T, eng *fakeEngine, from int, f0s ...float64) []string {
	t.Helper()
	rs := refRenders(t, eng, from)
	if len(rs) != len(f0s) {
		t.Fatalf("%d renders, want %d", len(rs), len(f0s))
	}
	var texts []string
	for i, r := range rs {
		f := f0s[i]
		texts = append(texts, renderOf(t, r).Take.Text)
		finishWith(t, eng, r, func(p string) { writeTalk(t, p, 0.3, f, f*1.25, f*1.5, f*1.1, f*1.35) })
	}
	for _, s := range texts {
		answerTranscribe(t, eng, s)
	}
	return texts
}

// TestDesignedReference: saving a designed voice makes its reference: the
// voice's design says the reference texts with its seed; the take whose
// pitch is nearest the sample's is kept as the reference, apart from the
// sample; the voice then clones from it, and Stories' whole dialogue
// gets it on a model that designs.
func TestDesignedReference(t *testing.T) {
	simulateDarwin(t)
	vdm := withDesignCatalog(t)
	a, eng, tt := newTestApp(t)
	eng.ready()
	sample := t.TempDir() + "/sample.wav"
	writeTalk(t, sample, 0.2, 220, 260)
	v, err := a.lib.svc.Voices.SaveFiles(context.Background(), core.Voice{Name: "Mom", Kind: core.VoiceDesigned, Design: "一位年轻妈妈，声音温柔", Seed: 11, Language: "zh",
		SampleText: "你好。", CreatedWith: vdm.Repo, DesignAttrs: attrsOf("一位年轻妈妈，声音温柔")}, core.VoiceFiles{Sample: sample})
	if err != nil {
		t.Fatal(err)
	}
	a.voiceChanged(core.VoiceChanged{Voice: v})
	if !needsReference(&v) {
		t.Fatal("needs no reference")
	}
	from := len(eng.sent)
	a.makeReference(&v, nil)
	tt.Frame()
	if a.refMaking(v.ID) == nil {
		t.Fatalf("not making: %q %q", a.refFailed(v.ID), a.toasts)
	}
	for _, r := range refRenders(t, eng, from) {
		if p := renderOf(t, r); p.Take.Seed != 11 || p.Take.VoiceID != v.ID || !slices.Contains(vd.ReferenceTexts("zh", vd.RegisterAdult), p.Take.Text) ||
			!strings.HasSuffix(p.Take.Voice, vd.ConversationalStyle("zh")) || !strings.Contains(p.Take.Voice, "温柔") {
			t.Errorf("candidate %+v", p.Take)
		}
	}
	// 360 Hz drifted to a child's voice; 210 Hz is the mother's.
	texts := answerRefCandidates(t, eng, from, 360, 210, 300)
	tt.Frame()
	got := a.findVoice(v.ID)
	if got.RefAudio == "" || got.RefText != texts[1] || got.Sample == "" || got.SampleText != "你好。" || a.refMaking(v.ID) != nil {
		t.Fatalf("reference %+v", got)
	}
	if needsReference(got) || !voiceHasReference(got) {
		t.Error("reference not seen")
	}
	// A model that clones: the reference, not the sample.
	base := entry(t, "qwen3-tts-1.7b-base-8bit")
	var tk core.Take
	a.fillVoice(&tk, got, base)
	if tk.RefAudio != a.voicePath(got, got.RefAudio) || tk.RefText != texts[1] {
		t.Errorf("fill %+v", tk)
	}
	// A model that designs: its design, and the reference for dialogue.
	doc := &services.LongformDoc{Model: vdm.Repo, Language: "zh"}
	_, take, ok := lfResolverOf(doc, a.voices, a.fillVoice, a.lib.svc.Voices.FilePath)(got.ID)
	if !ok || take.Voice == "" || take.RefAudio != "" || take.CloneRef != a.voicePath(got, got.RefAudio) || take.CloneRefText != texts[1] {
		t.Errorf("resolved %+v", take)
	}
	// A changed design drops the reference.
	a.openDesignFor(got)
	a.design.seed = 12
	a.design.saveName = "Mom"
	a.designSave()
	tt.Frame()
	if g := a.findVoice(v.ID); g.RefAudio != "" || g.Seed != 12 || a.refMaking(v.ID) == nil {
		t.Errorf("after a new design: %+v", g)
	}
}

// TestLazyReference: an old designed voice without a reference gets one
// before a story with whole dialogue renders; "Make reference" is in its
// menu.
func TestLazyReference(t *testing.T) {
	simulateDarwin(t)
	vdm := withDesignCatalog(t)
	a, eng, _ := newTestApp(t)
	eng.ready()
	v := addVoice(t, a, core.Voice{Name: "Kid", Kind: core.VoiceDesigned, Design: "一个七岁的小女孩", Language: "zh", Seed: 3, CreatedWith: vdm.Repo, DesignAttrs: attrsOf("一个七岁的小女孩")}, 0)
	a.voiceChanged(core.VoiceChanged{Voice: v})
	called := 0
	from := len(eng.sent)
	if n, k := a.ensureReferences([]*core.Voice{&v, &v}, func() { called++ }); n != 1 || k != 0 || called != 0 {
		t.Fatalf("ensure: %d %d, called %d", n, k, called)
	}
	for _, r := range refRenders(t, eng, from) {
		if p := renderOf(t, r); !slices.Contains(vd.ReferenceTexts("zh", vd.RegisterChild), p.Take.Text) {
			t.Errorf("not a child's text: %s", p.Take.Text)
		}
	}
	answerRefCandidates(t, eng, from, 330, 340, 350)
	if called != 1 || a.findVoice(v.ID).RefAudio == "" {
		t.Errorf("called %d, voice %+v", called, a.findVoice(v.ID))
	}
	// Nothing missing: at once.
	if n, k := a.ensureReferences([]*core.Voice{a.findVoice(v.ID)}, func() { called++ }); n != 0 || k != 0 || called != 2 {
		t.Errorf("again: %d %d, %d", n, k, called)
	}
	// Failing renders: then runs anyway; the failure is shown.
	w := addVoice(t, a, core.Voice{Name: "Dad", Kind: core.VoiceDesigned, Design: "a father", Language: "en", Seed: 4, CreatedWith: vdm.Repo, DesignAttrs: attrsOf("a father")}, 0)
	from = len(eng.sent)
	a.ensureReferences([]*core.Voice{&w}, func() { called++ })
	for _, r := range refRenders(t, eng, from) {
		eng.fail(r.ID, proto.CodeModelNotDownloaded, "no model")
	}
	if called != 3 || a.refFailed(w.ID) == "" || a.findVoice(w.ID).RefAudio != "" {
		t.Errorf("failed: called %d, %q", called, a.refFailed(w.ID))
	}
}
