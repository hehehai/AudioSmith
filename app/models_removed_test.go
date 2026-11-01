package main

// Thread #43 removed six speech models; what still names them moves to
// their replacements (models_removed.go), and their files can be cleaned
// up from the Models page.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/workflow"
)

const (
	kittenRepo  = "mlx-community/kitten-tts-mini-0.8"
	pocketRepo  = "mlx-community/pocket-tts"
	cbMultiRepo = "mlx-community/chatterbox-8bit"
	marvisRepo  = "Marvis-AI/marvis-tts-250m-v0.2-MLX-8bit"
)

// countToasts counts the toasts that contain s: those shown (the view
// hands them to the window's toasts) and those still to show.
func countToasts(a *app, tt *ui.Tester, s string) int {
	tt.Frame()
	n := 0
	for _, m := range append(slices.Clone(a.toasts), tt.Texts()...) {
		if strings.Contains(m, s) && !strings.HasPrefix(m, "toast ") { // not the logs panel's line (#108)
			n++
		}
	}
	return n
}

// TestRemovedStudioDraft: a Studio draft on KittenTTS opens on Kokoro
// with Kokoro's voice; one on Pocket TTS with a reference clip opens on
// Chatterbox Turbo (the clone replacement); the notice shows once.
func TestRemovedStudioDraft(t *testing.T) {
	a, eng, tt := newTestApp(t)
	a.applyStudioDraft(studioDraft{Version: 3, Text: "Hello there.", ModelLabel: "KittenTTS mini", Voice: "Bella"})
	tt.Frame()
	if a.preset().ID != "kokoro-82m-bf16" || a.voice != "af_heart" || a.text != "Hello there." {
		t.Fatalf("draft: %s %q %q", a.preset().ID, a.voice, a.text)
	}
	if countToasts(a, tt, "KittenTTS mini was removed; “Fast English voices” is used instead.") != 1 {
		t.Errorf("toasts %q", a.toasts)
	}
	a.applyStudioDraft(studioDraft{Version: 3, Text: "Again.", ModelLabel: "KittenTTS mini"})
	if countToasts(a, tt, "KittenTTS mini was removed") != 1 {
		t.Errorf("the notice repeats: %q", a.toasts)
	}
	tt.Click("Generate")
	if p := renderOf(t, eng.last(t)).Take; p.Model != entry(t, "kokoro-82m-bf16").Repo || p.Voice != "af_heart" {
		t.Errorf("recipe %+v", p)
	}
	a.applyStudioDraft(studioDraft{Version: 3, Text: "Hi.", ModelLabel: "Pocket TTS", RefAudio: "/tmp/ref.wav", RefText: "Words."})
	if a.preset().ID != "chatterbox-turbo-8bit" || a.refAudio != "/tmp/ref.wav" || countToasts(a, tt, "Pocket TTS was removed; “English clone” is used instead.") != 1 {
		t.Errorf("pocket clone: %s %q %q", a.preset().ID, a.refAudio, a.toasts)
	}
}

// TestRemovedSettings: the settings move off removed models when the app
// starts: the Studio's default, the calls' model, the agents' bindings,
// the dubbing default; the Studio's changes for a removed model go.
func TestRemovedSettings(t *testing.T) {
	dir := t.TempDir()
	a, _, _ := newTestAppIn(t, dir)
	a.updateSettings(func(s *settings.Settings) {
		s.Studio.Model = "soprano-80m-bf16"
		s.Calls.Model = "pocket-tts"
		s.API.Bindings = []settings.APIBinding{{Client: "claude", Model: "marvis-tts-250m-v0.2-8bit"}, {Client: "cursor", Model: "omnivoice-bf16"}}
		s.Dubbing.Model = cbMultiRepo
		s.Studio.Overrides = map[string]settings.ModelParams{"kitten-tts-mini-0.8": {Speed: 1.2}, "kokoro-82m-bf16": {Speed: 0.9}}
	})
	a.lib.close()
	b, _, tt := newTestAppIn(t, dir)
	st := b.settings.Get()
	if st.Studio.Model != "kokoro-82m-bf16" || st.Calls.Model != "chatterbox-turbo-8bit" || st.Dubbing.Model != entry(t, "chatterbox-turbo-8bit").Repo ||
		st.API.Bindings[0].Model != "kokoro-82m-bf16" || st.API.Bindings[1].Model != "omnivoice-bf16" {
		t.Errorf("settings %+v %+v %q %+v", st.Studio, st.Calls.Model, st.Dubbing.Model, st.API.Bindings)
	}
	if _, ok := st.Studio.Overrides["kitten-tts-mini-0.8"]; ok || st.Studio.Overrides["kokoro-82m-bf16"].Speed != 0.9 {
		t.Errorf("overrides %+v", st.Studio.Overrides)
	}
	if b.preset().ID != "kokoro-82m-bf16" {
		t.Errorf("studio on %s", b.preset().ID)
	}
	// One notice for all of them.
	if countToasts(b, tt, "Removed models: Soprano 80M → Fast English voices, Pocket TTS → English clone, Marvis TTS 250M → Fast English voices, Chatterbox → English clone, KittenTTS mini → Fast English voices. Your settings now use the replacements.") != 1 {
		t.Errorf("toasts %q %q", b.toasts, tt.Texts())
	}
	// Nothing to do the next time.
	b.lib.close()
	c, _, tt3 := newTestAppIn(t, dir)
	if countToasts(c, tt3, "was removed") != 0 {
		t.Errorf("again: %q", c.toasts)
	}
}

// TestRemovedProjects: a story on KittenTTS opens on Kokoro, the old
// model's speakers back to the default voice; an audiobook on Pocket TTS
// read by a cloned voice opens on Chatterbox Turbo; a dub on Chatterbox
// (multilingual) moves to Turbo and is saved so; a workflow's Speak step
// on Marvis moves to Kokoro.
func TestRemovedProjects(t *testing.T) {
	a, _, tt := newTestApp(t)
	clone := addVoice(t, a, core.Voice{Name: "Me", Kind: core.VoiceCloned, RefText: "Words."}, 4)
	tt.Frame()
	// Marvis with a cloned voice in the cast: Turbo, not Kokoro.
	story := services.NewLongformDoc(core.ProjectStory, marvisRepo)
	story.Cast = []services.CastMember{{ID: "c1", Name: "Kid", Voice: services.SpeakerRef + "conversational_a"}, {ID: "c2", Name: "Mum", Voice: clone.ID}}
	a.setLfDoc(a.lfStories, story, "", "")
	if d := a.lfStories.doc; d.Model != entry(t, "chatterbox-turbo-8bit").Repo || d.Cast[0].Voice != "" || d.Cast[1].Voice != clone.ID {
		t.Errorf("story on %s, cast %+v", d.Model, d.Cast)
	}
	story = services.NewLongformDoc(core.ProjectStory, kittenRepo)
	story.Voice = services.SpeakerRef + "Bella"
	story.Cast = []services.CastMember{{ID: "c1", Name: "Kid", Voice: services.SpeakerRef + "Luna"}}
	story.Lines = []services.StoryLine{{ID: "l1", Text: "Hi.", Character: "c1", Voice: services.SpeakerRef + "Kiki"}}
	a.setLfDoc(a.lfStories, story, "", "")
	d := a.lfStories.doc
	if d.Model != entry(t, "kokoro-82m-bf16").Repo || d.Voice != "" || d.Cast[0].Voice != "" || d.Lines[0].Voice != "" {
		t.Errorf("story %q voice %q cast %+v", d.Model, d.Voice, d.Cast)
	}
	book := services.NewLongformDoc(core.ProjectAudiobook, pocketRepo)
	book.Voice = clone.ID
	a.setLfDoc(a.lfBook, book, "", "")
	if d := a.lfBook.doc; d.Model != entry(t, "chatterbox-turbo-8bit").Repo || d.Voice != clone.ID {
		t.Errorf("book %q voice %q", d.Model, d.Voice)
	}
	if countToasts(a, tt, "KittenTTS mini was removed") != 1 || countToasts(a, tt, "Pocket TTS was removed; “English clone” is used instead.") != 1 {
		t.Errorf("toasts %q", a.toasts)
	}

	// A workflow saved with Marvis.
	a.wfNew("")
	id := a.wf.ID
	a.wfEdit(func(w *workflow.Workflow) {
		for i := range w.Nodes {
			if w.Nodes[i].Kind == workflow.KindSpeak {
				w.Nodes[i].P.Model, w.Nodes[i].P.Voice = marvisRepo, services.SpeakerRef+"conversational_a"
			}
		}
	})
	a.wf = nil
	a.wfOpen(id)
	stored, _, err := a.wfSvc.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []*workflow.Workflow{a.wf, stored} {
		for _, n := range w.Nodes {
			if n.Kind == workflow.KindSpeak && (n.P.Model != entry(t, "kokoro-82m-bf16").Repo || n.P.Voice != "") {
				t.Errorf("speak step %+v", n.P)
			}
		}
	}
}

// TestRemovedDub: a dub on Chatterbox (multilingual) opens on Turbo and
// is saved so.
func TestRemovedDub(t *testing.T) {
	d := newDubTest(t, 3)
	a, tt := d.a, d.tt
	a.dubSourceFile(d.src)
	if a.dub == nil {
		t.Fatal("no dub")
	}
	id := a.dub.ID
	a.dubSettings(func(s *dub.Settings) { s.Model = cbMultiRepo })
	a.dub = nil
	a.openDub(id)
	turbo := entry(t, "chatterbox-turbo-8bit").Repo
	if a.dub == nil || a.dub.Settings.Model != turbo {
		t.Fatalf("dub %+v", a.dub)
	}
	p, err := a.dubSvc.Get(context.Background(), id)
	if err != nil || p.Settings.Model != turbo {
		t.Errorf("saved %v %+v", err, p.Settings)
	}
	if countToasts(a, tt, "Chatterbox was removed; “English clone” is used instead.") != 1 {
		t.Errorf("toasts %q", a.toasts)
	}
}

// TestRemovedRecipes: history recipes and API-made takes that name a
// removed model render with the replacement (services.Config.Replace):
// its voice, no language it can't take, no guidance; Reuse moves the
// Studio to the replacement.
func TestRemovedRecipes(t *testing.T) {
	a, eng, tt := newTestApp(t)
	if _, err := a.lib.svc.Takes.Generate(context.Background(), core.TakeRequest{Recipe: core.Take{Text: "Hi.", Model: kittenRepo, Voice: "Bella"}}); err != nil {
		t.Fatal(err)
	}
	if p := renderOf(t, eng.last(t)).Take; p.Model != entry(t, "kokoro-82m-bf16").Repo || p.Voice != "af_heart" {
		t.Errorf("kitten %+v", p)
	}
	for _, c := range []struct {
		in   core.Take
		want core.Take
	}{
		{core.Take{Model: cbMultiRepo, RefAudio: "/r.wav", Language: "zh", Guidance: 0.3, Temperature: 0.8},
			core.Take{Model: entry(t, "chatterbox-turbo-8bit").Repo, RefAudio: "/r.wav", Temperature: 0.8}},
		{core.Take{Model: "mlx-community/MOSS-TTS-Nano-100M", RefAudio: "/r.wav", Language: "zh"},
			core.Take{Model: entry(t, "qwen3-tts-0.6b-base-8bit").Repo, RefAudio: "/r.wav", Language: "Chinese"}},
		{core.Take{Model: marvisRepo, Voice: "conversational_b", Temperature: 0.9},
			core.Take{Model: entry(t, "kokoro-82m-bf16").Repo, Voice: "af_heart"}},
		{core.Take{Model: pocketRepo, Voice: "alba", VoicePrompt: "/p"},
			core.Take{Model: entry(t, "chatterbox-turbo-8bit").Repo, VoicePrompt: "/p"}},
		{core.Take{Model: entry(t, "kokoro-82m-bf16").Repo, Voice: "af_bella"}, core.Take{Model: entry(t, "kokoro-82m-bf16").Repo, Voice: "af_bella"}},
	} {
		got := c.in
		changed := replaceRemovedTake(&got)
		if got != c.want || changed != (c.in.Model != c.want.Model) {
			t.Errorf("%s: %+v (%v)", c.in.Model, got, changed)
		}
	}
	// Reuse a take made with Soprano.
	tk := core.Take{ID: "t1", Text: "Old take.", Model: "mlx-community/Soprano-80M-bf16", Temperature: 0.5}
	a.reuse(&tk)
	if a.preset().ID != "kokoro-82m-bf16" || a.voice != "af_heart" || countToasts(a, tt, "Soprano 80M was removed") != 1 {
		t.Errorf("reuse: %s %q %q", a.preset().ID, a.voice, a.toasts)
	}
}

// TestRemovedVoices: a built-in speaker of a removed model saved in My
// voices can't be used anywhere, says why, and has no portable copy; a
// voice cloned on a removed model still clones elsewhere.
func TestRemovedVoices(t *testing.T) {
	bella := core.Voice{Name: "Bella", Kind: core.VoiceBuiltin, Speaker: "Bella", Model: kittenRepo}
	kokoro := entry(t, "kokoro-82m-bf16")
	ok, why := voiceUsable(&bella, kokoro)
	if ok || why != "This voice belongs to KittenTTS mini, which AudioSmith removed: it can't be used any more." || canMakePortable(&bella) {
		t.Errorf("bella: %v %q portable %v", ok, why, canMakePortable(&bella))
	}
	cloned := core.Voice{Name: "Me", Kind: core.VoiceCloned, RefAudio: "ref.wav", Model: cbMultiRepo}
	if ok, why := voiceUsable(&cloned, entry(t, "chatterbox-turbo-8bit")); !ok {
		t.Errorf("clone: %q", why)
	}
	if entryFor(cbMultiRepo).ID != "chatterbox-turbo-8bit" || entryFor(kittenRepo).ID != "kokoro-82m-bf16" {
		t.Error("entryFor")
	}
}

// TestRemovedNotOffered: no picker of any page offers a removed model,
// recommended or under Show all models.
func TestRemovedNotOffered(t *testing.T) {
	a, _, tt := newTestApp(t)
	acceptLicences(t, a)
	check := func(page string) {
		t.Helper()
		texts := "|" + strings.Join(tt.Texts(), "|") + "|"
		for _, r := range catalog.Builtin().Removed {
			if strings.Contains(texts, "|"+r.Label+"|") {
				t.Errorf("%s offers %s", page, r.Label)
			}
		}
	}
	for _, page := range []struct{ path, key string }{{"/studio", "model"}, {"/stories", "lf-model"}, {"/audiobook", "lf-model"}} {
		a.navigate(page.path)
		a.lfStories.panel.set, a.lfStories.panel.open = true, true
		a.lfBook.panel.set, a.lfBook.panel.open = true, true
		a.lfStories.open["setup"], a.lfBook.open["setup"] = true, true
		a.pickerOpen = map[string]bool{page.key: true}
		a.pickerAll = map[string]bool{page.key: true}
		tt.Frame()
		tt.Frame()
		if !tt.HasText("Custom Hugging Face repo") && page.key == "model" {
			t.Errorf("%s: the list is not open: %q", page.path, tt.Texts())
		}
		check(page.path)
	}
	for _, w := range []catalog.Workflow{catalog.WorkflowStudio, catalog.WorkflowStory, catalog.WorkflowAudiobook, catalog.WorkflowDub, catalog.WorkflowDesign} {
		for _, l := range []string{"en", "zh-Hans", "ja"} {
			for _, e := range catalog.Builtin().Recommended(w, l) {
				if _, removed := catalog.Builtin().RemovedModel(e.ID); removed {
					t.Errorf("%s %s recommends %s", w, l, e.ID)
				}
			}
		}
	}
	for _, e := range dubTTSModels() {
		if _, removed := catalog.Builtin().RemovedModel(e.ID); removed {
			t.Errorf("dub offers %s", e.ID)
		}
	}
	for _, e := range designModels() {
		if _, removed := catalog.Builtin().RemovedModel(e.ID); removed {
			t.Errorf("design offers %s", e.ID)
		}
	}
}

// putLeftover lays out a removed repo in the model folder: a blob of size
// bytes and a snapshot linking to it.
func putLeftover(t *testing.T, cache, repo string, size int) {
	t.Helper()
	dir := hub.RepoDir(cache, repo)
	os.MkdirAll(filepath.Join(dir, "blobs"), 0o755)
	os.MkdirAll(filepath.Join(dir, "snapshots", "abc"), 0o755)
	if err := os.WriteFile(filepath.Join(dir, "blobs", "b1"), make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Symlink("../../blobs/b1", filepath.Join(dir, "snapshots", "abc", "model.safetensors"))
}

// TestModelsCleanup: the Models page says how much the removed models
// still take, and Clean up (after a dialog listing them) deletes exactly
// their folders; the kept models and the shared auxiliary repos stay.
func TestModelsCleanup(t *testing.T) {
	a, _, tt := newTestApp(t)
	cache := a.hubClient().Options().CacheDir
	openModels(a)
	tt.Frame()
	if tt.HasText("Clean up") || a.removedBytes() != 0 || !a.removedKnown {
		t.Fatalf("nothing to clean, yet: %q", tt.Texts())
	}
	putLeftover(t, cache, kittenRepo, 3e6)
	putLeftover(t, cache, pocketRepo, 2e6)
	putLeftover(t, cache, "kyutai/moshiko-pytorch-bf16", 1e6)
	a.refreshModels()
	tt.Frame()
	if !tt.HasText("Removed models still take 6 MB") || !tt.HasText("KittenTTS mini, Pocket TTS: AudioSmith no longer uses them.") {
		t.Fatalf("row: %q", tt.Texts())
	}
	// Only on All and TTS.
	a.modelsKind = 2
	tt.Frame()
	if tt.HasText("Removed models still take 6 MB") {
		t.Error("the row on the ASR tab")
	}
	a.modelsKind = 0
	tt.Frame()
	if err := tt.Click("Clean up"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	texts := strings.Join(tt.Texts(), "|")
	if !a.cleanAsk || !strings.Contains(texts, "Clean up removed models (6 MB)?") || !strings.Contains(texts, "• KittenTTS mini · 3 MB") ||
		!strings.Contains(texts, "• Pocket TTS · 3 MB") { // Pocket with its tokenizer
		t.Fatalf("dialog: %q", texts)
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	for _, repo := range []string{kittenRepo, pocketRepo, "kyutai/moshiko-pytorch-bf16"} {
		if _, err := os.Stat(hub.RepoDir(cache, repo)); !os.IsNotExist(err) {
			t.Errorf("%s still there: %v", repo, err)
		}
	}
	for _, e := range catalog.Builtin().Entries {
		repos := []string{e.Repo}
		for _, x := range e.Aux {
			repos = append(repos, x.Repo)
		}
		for _, r := range repos {
			if _, err := os.Stat(hub.RepoDir(cache, r)); err != nil {
				t.Errorf("%s went: %v", r, err)
			}
		}
	}
	if tt.HasText("Removed models still take 6 MB") || countToasts(a, tt, "Freed 6 MB") != 1 {
		t.Errorf("after: %q %q", tt.Texts(), a.toasts)
	}
	if !slices.Contains(catalog.Builtin().Removed[0].Repos(), kittenRepo) {
		t.Error("kitten")
	}
}
