package main

import (
	"context"
	"strings"
	"testing"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
)

// designedVoice saves a designed voice of design desc (Chinese), with a
// sample, as Design does.
func designedVoice(t *testing.T, a *app, model, name, desc, lang string, seed int64) core.Voice {
	t.Helper()
	sample := t.TempDir() + "/sample.wav"
	writeTalk(t, sample, 0.2, 200, 240)
	v, err := a.lib.svc.Voices.SaveFiles(context.Background(), core.Voice{Name: name, Kind: core.VoiceDesigned, Design: desc, Seed: seed, Language: lang,
		SampleText: "你好。", CreatedWith: model, DesignAttrs: attrsOf(desc)}, core.VoiceFiles{Sample: sample})
	if err != nil {
		t.Fatal(err)
	}
	a.voiceChanged(core.VoiceChanged{Voice: v})
	return v
}

// answerRound finishes round's reference renders sent since from (lively
// takes at 200 Hz) and has the recognizer hear heard(k, text) for each.
func answerRound(t *testing.T, eng *fakeEngine, from, round int, heard func(k int, text string) string) []renderReq {
	t.Helper()
	rs := refRenders(t, eng, from)[round*refCandidates:]
	if len(rs) != refCandidates {
		t.Fatalf("%d renders", len(rs))
	}
	var out []renderReq
	for _, r := range rs {
		p := renderOf(t, r)
		out = append(out, renderReq{text: p.Take.Text, seed: p.Take.Seed, maxTokens: p.Take.MaxTokens})
		finishWith(t, eng, r, func(path string) { writeTalk(t, path, 0.3, 200, 250, 300, 220, 270) })
	}
	for k, r := range out {
		answerTranscribe(t, eng, heard(k, r.text))
	}
	return out
}

type renderReq struct {
	text      string
	seed      int64
	maxTokens int
}

// half is the first half of s: a take cut off in the middle.
func half(s string) string {
	r := []rune(s)
	return string(r[:len(r)/2])
}

// TestReferenceRetake (#93): reference takes of Qwen3-TTS get a generous
// length limit; when the recognizer hears none of them say its text, they
// are made again once with the next seed, and the one that says it is
// kept, its text checked.
func TestReferenceRetake(t *testing.T) {
	simulateDarwin(t)
	vdm := withDesignCatalog(t)
	a, eng, tt := newTestApp(t)
	eng.ready()
	v := designedVoice(t, a, vdm.Repo, "Elder", "一位老年男性，声音低沉", "zh", 11)
	from := len(eng.sent)
	a.makeReference(&v, nil)
	first := answerRound(t, eng, from, 0, func(_ int, s string) string { return half(s) })
	for _, r := range first {
		if r.seed != 11 || r.maxTokens != refMaxTokens {
			t.Errorf("first round %+v", r)
		}
	}
	tt.Frame()
	if a.refMaking(v.ID) == nil || a.findVoice(v.ID).RefAudio != "" {
		t.Fatalf("not taken again: %q", a.refFailed(v.ID))
	}
	second := answerRound(t, eng, from, 1, func(k int, s string) string {
		if k == 1 {
			return s
		}
		return half(s)
	})
	for _, r := range second {
		if r.seed != 12 || r.maxTokens != refMaxTokens {
			t.Errorf("second round %+v", r)
		}
	}
	tt.Frame()
	got := a.findVoice(v.ID)
	if got.RefAudio == "" || got.RefText != second[1].text || got.RefCheck == nil || !got.RefCheck.TextChecked || a.refMaking(v.ID) != nil {
		t.Fatalf("reference %+v %+v", got, got.RefCheck)
	}
	takes, _ := a.lib.svc.Takes.List(context.Background(), core.TakeQuery{})
	if len(takes) != 0 {
		t.Errorf("candidates kept: %d takes", len(takes))
	}
}

// TestReferenceHeardText (#93): when no take says its text after the
// second round either, the kept one gets what the recognizer heard as its
// text; when what it heard can't be the text of that much speech, none
// is kept and the failure says why.
func TestReferenceHeardText(t *testing.T) {
	simulateDarwin(t)
	vdm := withDesignCatalog(t)
	a, eng, tt := newTestApp(t)
	eng.ready()
	v := designedVoice(t, a, vdm.Repo, "Elder", "一位老年男性，声音低沉", "zh", 11)
	from := len(eng.sent)
	a.makeReference(&v, nil)
	answerRound(t, eng, from, 0, func(_ int, s string) string { return half(s) })
	second := answerRound(t, eng, from, 1, func(_ int, s string) string { return half(s) })
	tt.Frame()
	got := a.findVoice(v.ID)
	if got.RefAudio == "" || got.RefCheck == nil || !got.RefCheck.TextChecked || got.RefText != half(second[0].text) &&
		got.RefText != half(second[1].text) && got.RefText != half(second[2].text) {
		t.Fatalf("reference %q %+v", got.RefText, got.RefCheck)
	}
	for _, h := range got.RefCheck.Hints {
		if h == "mismatch" {
			t.Errorf("hints %v", got.RefCheck.Hints)
		}
	}

	// Heard only "嗯" in 12 s: nothing is kept.
	w := designedVoice(t, a, vdm.Repo, "Sage", "一位老年女性，声音沙哑", "zh", 21)
	from = len(eng.sent)
	a.makeReference(&w, nil)
	answerRound(t, eng, from, 0, func(int, string) string { return "嗯。" })
	answerRound(t, eng, from, 1, func(int, string) string { return "嗯。" })
	tt.Frame()
	if g := a.findVoice(w.ID); g.RefAudio != "" || !strings.Contains(a.refFailed(w.ID), "did not say its reference text") {
		t.Errorf("rejected: %+v, %q", g, a.refFailed(w.ID))
	}
}

// TestReferenceTruncatedNoASR (#93): without a recognizer, a take as long
// as the length limit was cut off: it is not kept, and with every take
// cut off they are made again.
func TestReferenceTruncatedNoASR(t *testing.T) {
	simulateDarwin(t)
	vdm := withDesignCatalog(t)
	a, eng, tt := newTestApp(t)
	eng.ready()
	v := designedVoice(t, a, vdm.Repo, "Elder", "一位老年男性，声音低沉", "zh", 11)
	from := len(eng.sent)
	a.makeReference(&v, nil)
	long := []float64{}
	for range 10 { // 50 s: at the limit
		long = append(long, 200, 260)
	}
	rs := refRenders(t, eng, from)
	for _, r := range rs {
		finishWith(t, eng, r, func(p string) { writeTalk(t, p, 0.2, long...) })
	}
	r := eng.lastOf(t, proto.MethodSTTTranscribe)
	eng.fail(r.ID, proto.CodeModelNotDownloaded, "no recognizer")
	tt.Frame()
	if len(refRenders(t, eng, from)) != 2*refCandidates {
		t.Fatalf("not taken again: %d renders, %q", len(refRenders(t, eng, from)), a.refFailed(v.ID))
	}
	// The second round: one is cut off, two end in time; no recognizer.
	for i, r := range refRenders(t, eng, from)[refCandidates:] {
		if i == 0 {
			finishWith(t, eng, r, func(p string) { writeTalk(t, p, 0.2, long...) })
			continue
		}
		finishWith(t, eng, r, func(p string) { writeTalk(t, p, 0.3, 200, 250, 300, 220, 270) })
	}
	r = eng.lastOf(t, proto.MethodSTTTranscribe)
	eng.fail(r.ID, proto.CodeModelNotDownloaded, "no recognizer")
	tt.Frame()
	got := a.findVoice(v.ID)
	if got.RefAudio == "" || got.RefSeconds > 20 || got.RefCheck == nil || got.RefCheck.TextChecked {
		t.Fatalf("reference %+v", got)
	}
}

// TestReferenceRepair (#93): a designed voice's reference made before
// #93 is heard once before a Studio take clones it; one that doesn't say
// its text is made again (a note says so), then the take follows with
// the new reference. One that says it is only marked checked.
func TestReferenceRepair(t *testing.T) {
	simulateDarwin(t)
	vdm := withDesignCatalog(t)
	a, eng, tt := newTestApp(t)
	eng.ready()
	text := vd.ReferenceTexts("zh", vd.RegisterAdult)[2]
	old := designedVoice(t, a, vdm.Repo, "Storyteller", "一位老年男性，声音温暖", "zh", 5)
	old.RefText = text
	old = addVoiceFiles(t, a, old, 16)
	base := entry(t, "qwen3-tts-1.7b-base-8bit")
	a.selectModel(base)
	a.studioVoice = old.ID
	a.text = "那天傍晚雨刚停。"
	tt.Frame()
	from := len(eng.sent)
	a.generate()
	p := answerTranscribe(t, eng, half(text)) // cut off
	if p.Audio != a.voicePath(&old, old.RefAudio) || p.Language != "zh" {
		t.Errorf("heard %+v", p)
	}
	if !strings.Contains(strings.Join(a.toasts, "|"), "doesn't say its text: making it again") || a.refMaking(old.ID) == nil {
		t.Fatalf("not made again: %q", a.toasts)
	}
	texts := answerRefCandidates(t, eng, from, 200, 210, 220)
	tt.Frame()
	got := a.findVoice(old.ID)
	if got.RefOriginal == "" || got.RefCheck == nil || !got.RefCheck.TextChecked {
		t.Fatalf("repaired %+v", got)
	}
	take := renderOf(t, eng.last(t)).Take
	if take.Model != base.Repo || take.RefText != got.RefText || !strings.Contains(strings.Join(texts, "|"), take.RefText) {
		t.Fatalf("take %+v", take)
	}
	if a.refWork(got) {
		t.Error("checked again")
	}

}

// TestReferenceRepairChecked (#93): an old reference that says its text
// is only marked checked; the take follows.
func TestReferenceRepairChecked(t *testing.T) {
	simulateDarwin(t)
	vdm := withDesignCatalog(t)
	a, eng, tt := newTestApp(t)
	eng.ready()
	text := vd.ReferenceTexts("zh", vd.RegisterAdult)[2]
	good := designedVoice(t, a, vdm.Repo, "Mom", "一位年轻妈妈，声音温柔", "zh", 6)
	good.RefText = text
	good = addVoiceFiles(t, a, good, 12)
	a.selectModel(entry(t, "qwen3-tts-1.7b-base-8bit"))
	a.studioVoice = good.ID
	a.text = "那天傍晚雨刚停。"
	tt.Frame()
	from := len(eng.sent)
	a.generate()
	answerTranscribe(t, eng, text)
	tt.Frame()
	if g := a.findVoice(good.ID); g.RefAudio != good.RefAudio || g.RefCheck == nil || !g.RefCheck.TextChecked {
		t.Errorf("checked %+v", g)
	}
	if rs := refRenders(t, eng, from); len(rs) != 1 || renderOf(t, rs[0]).Take.RefText != text {
		t.Errorf("renders %d", len(rs))
	}
}

// TestReferenceRepairHeard (#93): a portable copy's reference that
// doesn't say its text gets what it says; without a recognizer a
// reference is used as it is, and not heard again this session.
func TestReferenceRepairHeard(t *testing.T) {
	simulateDarwin(t)
	a, eng, tt := newTestApp(t)
	eng.ready()
	text := vd.ReferenceTexts("zh", vd.RegisterAdult)[0]
	pc := addVoice(t, a, core.Voice{Name: "Serena (approximate)", Kind: core.VoiceCloned, RefText: text + "多出来的一句。", Language: "zh",
		Origin: "portable:speaker:x:Serena", Tags: []string{portableTag}}, 12)
	a.voiceChanged(core.VoiceChanged{Voice: pc})
	base := entry(t, "qwen3-tts-1.7b-base-8bit")
	a.selectModel(base)
	a.studioVoice = pc.ID
	a.text = "你好。"
	tt.Frame()
	a.generate()
	answerTranscribe(t, eng, text)
	tt.Frame()
	if g := a.findVoice(pc.ID); g.RefText != text || g.RefCheck == nil || !g.RefCheck.TextChecked {
		t.Fatalf("portable %+v", g)
	}
	if take := renderOf(t, eng.last(t)).Take; take.RefText != text {
		t.Errorf("take %+v", take)
	}

}

// TestReferenceRepairNoASR (#93): without a recognizer a reference is
// used as it is, and not heard again this session.
func TestReferenceRepairNoASR(t *testing.T) {
	simulateDarwin(t)
	a, eng, tt := newTestApp(t)
	eng.ready()
	text := vd.ReferenceTexts("zh", vd.RegisterAdult)[0]
	other := addVoice(t, a, core.Voice{Name: "Vivian (approximate)", Kind: core.VoiceCloned, RefText: text, Language: "zh",
		Origin: "portable:speaker:x:Vivian", Tags: []string{portableTag}}, 12)
	a.voiceChanged(core.VoiceChanged{Voice: other})
	a.selectModel(entry(t, "qwen3-tts-1.7b-base-8bit"))
	a.studioVoice = other.ID
	a.text = "你好。"
	tt.Frame()
	a.generate()
	r := eng.lastOf(t, proto.MethodSTTTranscribe)
	eng.fail(r.ID, proto.CodeModelNotDownloaded, "no recognizer")
	tt.Frame()
	if take := renderOf(t, eng.last(t)).Take; take.VoiceID != other.ID || take.RefText != text {
		t.Fatalf("take %+v", take)
	}
	if a.refUnchecked(a.findVoice(other.ID)) {
		t.Error("heard again this session")
	}
}

// addVoiceFiles gives saved voice v a reference clip of seconds.
func addVoiceFiles(t *testing.T, a *app, v core.Voice, seconds float64) core.Voice {
	t.Helper()
	ref := t.TempDir() + "/ref.wav"
	writeTalk(t, ref, 0.3, 200, 250, 300, 220, 270)
	v.RefSeconds = seconds
	saved, err := a.lib.svc.Voices.SaveFiles(context.Background(), v, core.VoiceFiles{Reference: ref})
	if err != nil {
		t.Fatal(err)
	}
	a.voiceChanged(core.VoiceChanged{Voice: saved})
	return saved
}

// TestReferenceLanguages (#93): a German designed voice's reference says
// German texts (it said English ones), heard as German; the description
// keeps the English style.
func TestReferenceLanguages(t *testing.T) {
	simulateDarwin(t)
	vdm := withDesignCatalog(t)
	a, eng, _ := newTestApp(t)
	eng.ready()
	v := designedVoice(t, a, vdm.Repo, "Abendstern", "a warm middle-aged woman", "de", 7)
	from := len(eng.sent)
	a.makeReference(&v, nil)
	rs := refRenders(t, eng, from)
	for _, r := range rs {
		p := renderOf(t, r).Take
		if vd.RefTextLang(p.Text) != "de" || !strings.HasSuffix(p.Voice, vd.ConversationalStyle("en")) || p.Language != "German" {
			t.Errorf("take %q %q %q", p.Text, p.Voice, p.Language)
		}
	}
	for _, r := range rs {
		finishWith(t, eng, r, func(p string) { writeTalk(t, p, 0.3, 200, 250, 300, 220, 270) })
	}
	if p := params[proto.TranscribeParams](t, eng.lastOf(t, proto.MethodSTTTranscribe)); p.Language != "de" {
		t.Errorf("heard as %q", p.Language)
	}
}
