package main

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/services"
)

// TestStoryDialogueReferences (#83): a story with whole dialogue whose
// designed voice has no reference makes it first, then renders by
// itself; the chapter clones the voice from its reference (CloneRef on a
// model that designs). The Performance section lists the characters'
// references.
func TestStoryDialogueReferences(t *testing.T) {
	vdm := withDesignCatalog(t)
	a, eng, tt := newTestApp(t)
	eng.ready()
	kid := addVoice(t, a, core.Voice{Name: "Kid", Kind: core.VoiceDesigned, Design: "一个七岁的小女孩", Language: "zh", Seed: 3,
		CreatedWith: vdm.Repo, DesignAttrs: attrsOf("一个七岁的小女孩")}, 0)
	a.voiceChanged(core.VoiceChanged{Voice: kid})
	soulx, _ := dialogueEntry()
	p := a.lfStories
	p.doc.Model, p.doc.Language = vdm.Repo, "zh"
	p.doc.Production.Dialogue, p.doc.Production.DialogueModel = true, soulx.Repo
	p.doc.Cast = []services.CastMember{{ID: "c1", Name: "Kid", Voice: kid.ID}}
	p.doc.Voice = kid.ID
	p.doc.Lines = []services.StoryLine{{ID: "l1", Text: "妈妈，我想领养一只小猫。", Character: "c1"}}
	from := len(eng.sent)
	a.startLf(p, "")
	if len(lfJobs(eng)) != 0 || len(refRenders(t, eng, from)) != refCandidates || !strings.Contains(strings.Join(a.toasts, "|"), "Making a reference for 1 voice first") {
		t.Fatalf("jobs %d, renders %d, toasts %q", len(lfJobs(eng)), len(refRenders(t, eng, from)), a.toasts)
	}
	// The Performance section says so.
	a.navigate("/stories")
	p.open["scene"], p.open["dialogue-refs"] = true, true
	tt.Frame()
	if !tt.HasText("Character references") || !tt.HasText("Making a reference… 0 of 3 takes") {
		t.Errorf("references: %q", tt.Texts())
	}
	texts := answerRefCandidates(t, eng, from, 330, 340, 350)
	tt.Frame()
	jl := lfJobs(eng)
	if len(jl) == 0 {
		t.Fatalf("no render after the references: %q", a.toasts)
	}
	got := a.findVoice(kid.ID)
	var take core.Take
	for _, v := range chapterParams(t, jl[0]).Voices {
		if v.VoiceID == kid.ID {
			take = v
		}
	}
	if got.RefAudio == "" || take.CloneRef != a.voicePath(got, got.RefAudio) || !strings.Contains(strings.Join(texts, "|"), take.CloneRefText) || take.CloneRefText == "" {
		t.Errorf("chapter voice %+v", take)
	}
	if !tt.HasText("Reference quality: good") && !tt.HasText("Reference quality: fine") {
		t.Errorf("after: %q", tt.Texts())
	}
}

// TestImportCleansReference (#83): an imported voice's noisy reference is
// denoised and tidied, the original kept; the editor says so and undoes it.
func TestImportCleansReference(t *testing.T) {
	a, eng, _ := newTestApp(t)
	tt := ui.NewTester(a.view, 1100, 1000)
	ctx := context.Background()
	v := addVoice(t, a, core.Voice{Name: "Noisy", Kind: core.VoiceCloned, RefText: "Some words."}, 8)
	addNoise(t, a.voicePath(&v, v.RefAudio), -38)
	pkg := filepath.Join(t.TempDir(), "noisy.asvoice")
	if err := a.lib.svc.Voices.Export(ctx, v.ID, pkg, core.VoiceExport{IncludeReference: true, App: "AudioSmith"}); err != nil {
		t.Fatal(err)
	}
	a.importVoice(pkg)
	answerEnhance(t, eng)
	tt.Frame()
	var got *core.Voice
	for i := range a.voices {
		if a.voices[i].ID != v.ID && a.voices[i].Name == "Noisy" {
			got = &a.voices[i]
		}
	}
	if got == nil || got.RefOriginal == "" || got.RefAudio != "cleaned.wav" || got.RefCheck == nil || !slices.Contains(got.RefCheck.Cleaned, "denoised") {
		t.Fatalf("imported %+v", got)
	}
	if !tt.HasText("Cleaned up: noise removed, silences trimmed, loudness evened out") {
		t.Errorf("editor: %q", tt.Texts())
	}
	if err := tt.Click("Use the original"); err != nil {
		t.Fatal(err)
	}
	if g := a.findVoice(got.ID); g.RefOriginal != "" || g.RefAudio == "cleaned.wav" {
		t.Errorf("reverted %+v", g)
	}
}

// TestRefWindow (#81 §4.5): a 25 s recording keeps its best 10–14 s,
// starting and ending at words; up to 15 s stays whole; one unbroken
// stretch falls back to the 15 s with the most speech.
func TestRefWindow(t *testing.T) {
	speech := []span{{0.5, 4}, {6.5, 9}, {9.3, 13}, {13.4, 17.5}, {18, 20}, {23, 24.5}}
	w := refWindow(speech, 25)
	if w.Len() < 10 || w.Len() > 15 || w.Start < 6.3 || w.Start > 6.4 {
		t.Errorf("window %+v", w)
	}
	if w := refWindow(speech, 14); w != (span{0, 14}) {
		t.Errorf("short clip %+v", w)
	}
	if w := refWindow([]span{{1, 40}}, 41); w.Len() > 15.01 || w.Len() < 14.9 {
		t.Errorf("unbroken %+v", w)
	}
}

// TestVoiceMenuMakeReference: a designed voice's menu offers Make
// reference while it has none.
func TestVoiceMenuMakeReference(t *testing.T) {
	vdm := withDesignCatalog(t)
	a, eng, tt := newTestApp(t)
	eng.ready()
	v := addVoice(t, a, core.Voice{Name: "Kid", Kind: core.VoiceDesigned, Design: "一个七岁的小女孩", Language: "zh", Seed: 3,
		CreatedWith: vdm.Repo, DesignAttrs: attrsOf("一个七岁的小女孩")}, 0)
	a.voiceChanged(core.VoiceChanged{Voice: v})
	a.navigate("/voices")
	a.openVoiceEditor(v.ID)
	tt.Frame()
	if err := tt.Click("More actions for Kid"); err != nil { // the panel's ⋯
		t.Fatal(err)
	}
	if !slices.Contains(tt.Menu(), "Make reference") {
		t.Fatalf("menu %q", tt.Menu())
	}
	from := len(eng.sent)
	if err := tt.ChooseMenuItem("Make reference"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(refRenders(t, eng, from)) != refCandidates || !tt.HasText("Making a reference… 0 of 3 takes") {
		t.Errorf("renders %d: %q", len(refRenders(t, eng, from)), tt.Texts())
	}
}
