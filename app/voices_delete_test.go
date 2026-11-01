package main

// Tests of deleting a voice (#108): from its card's ⋯ menu (the native
// menu takes the pointer, so the card is no longer hovered when the
// chosen item is reported), the confirmation's usage, Undo, the trash,
// the places that named the voice afterwards, and the operations log.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/oplog"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/workflow"
)

// opLines are the operations log's lines so far.
func opLines(a *app) []string {
	var out []string
	for _, e := range a.logs.Tail(5000, oplog.Source) {
		out = append(out, string(e.Level)+" "+e.Message)
	}
	return out
}

// hasOp reports whether a line of the operations log starts with event
// and has every one of parts.
func hasOp(a *app, event string, parts ...string) bool {
	return slices.ContainsFunc(opLines(a), func(l string) bool {
		_, msg, _ := strings.Cut(l, " ")
		if !strings.HasPrefix(msg, event+" ") && msg != event {
			return false
		}
		for _, p := range parts {
			if !strings.Contains(msg, p) {
				return false
			}
		}
		return true
	})
}

// chooseAway opens the menu of the button labelled menu, moves the
// pointer off it (as macOS's menu does) and chooses item.
func chooseAway(t *testing.T, tt *ui.Tester, menu, item string) {
	t.Helper()
	if err := tt.Click(menu); err != nil {
		t.Fatal(err)
	}
	if tt.Menu() == nil {
		t.Fatalf("%s: no menu", menu)
	}
	tt.Move(2, 2)
	if err := tt.ChooseMenuItem(item); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
}

func TestVoiceCardMenuDelete(t *testing.T) {
	a, _ := newVoiceTestApp(t)
	tt := ui.NewTester(a.view, 1200, 900)
	pinTester(tt)
	c1 := addVoice(t, a, core.Voice{Name: "c1", Kind: core.VoiceCloned, RefText: "Hello there."}, 6)
	addVoice(t, a, core.Voice{Name: "c2", Kind: core.VoiceCloned, RefText: "Hello again."}, 6)
	addTake(t, a, core.Take{ID: "T1", Text: "A take in c1.", Model: qwenBase, VoiceID: c1.ID, AudioSeconds: 1}, 0.3)
	a.navigate("/voices")
	tt.Frame()
	r, ok := tt.Find("c1")
	if !ok {
		t.Fatalf("no card: %q", tt.Texts())
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2) // hover the card: its ⋯ shows
	tt.Frame()
	chooseAway(t, tt, "More actions", "Delete")
	if !a.deleteOpen || !tt.HasText("Delete “c1”?") {
		t.Fatalf("no confirmation: open=%v %q", a.deleteOpen, tt.Texts())
	}
	if err := tt.Click("Delete voice"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.findVoice(c1.ID) != nil {
		t.Fatal("not deleted")
	}
	if !tt.HasText("Voice deleted") || !tt.HasText("Undo") {
		t.Errorf("no toast with Undo: %q", tt.Texts())
	}
	if !tt.HasText("c2") {
		t.Error("the other voice went too")
	}
	if !hasOp(a, "voice.delete", "id="+c1.ID, "name=c1", "takes=1", "undo=true", "by=app") {
		t.Errorf("not logged: %q", opLines(a))
	}
	dir := a.lib.svc.Voices.FilePath(c1, c1.RefAudio)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("its files stay in place: %v", err)
	}

	// Undo brings it back: the profile, its files, its takes' link.
	if err := tt.Click("Undo"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	v := a.findVoice(c1.ID)
	if v == nil {
		t.Fatalf("not restored: %q", tt.Texts())
	}
	if _, err := os.Stat(a.lib.svc.Voices.FilePath(*v, v.RefAudio)); err != nil {
		t.Errorf("reference not back: %v", err)
	}
	if u, _ := a.lib.svc.Voices.Usage(context.Background(), c1.ID); u.Takes != 1 {
		t.Errorf("the take's link not back: %+v", u)
	}
	if !hasOp(a, "voice.restore", "id="+c1.ID) {
		t.Errorf("restore not logged: %q", opLines(a))
	}
	// Its toast closing after an Undo leaves it be.
	tt.Advance(undoTime + time.Second)
	tt.Frame()
	if _, err := os.Stat(a.lib.svc.Voices.FilePath(*v, v.RefAudio)); err != nil {
		t.Errorf("reference gone after the toast: %v", err)
	}
}

// TestVoiceDeletePurge: without Undo, the toast's end removes the files
// for good; a delete an earlier run did not finish gets its files back at
// start.
func TestVoiceDeletePurge(t *testing.T) {
	a, _ := newVoiceTestApp(t)
	tt := ui.NewTester(a.view, 1200, 900)
	v := addVoice(t, a, core.Voice{Name: "Mara", Kind: core.VoiceCloned, RefText: "Hello there."}, 6)
	trash := filepath.Join(filepath.Dir(a.lib.store.Voices().VoiceDir()), "trash", "voices", v.ID)
	a.navigate("/voices")
	tt.Frame()
	a.askDeleteVoice(v.ID)
	tt.Frame()
	tt.Click("Delete voice")
	tt.Frame()
	if _, err := os.Stat(trash); err != nil {
		t.Fatalf("not in the trash: %v", err)
	}
	tt.Advance(undoTime + time.Second)
	tt.Frame()
	tt.Frame()
	if tt.HasText("Undo") {
		t.Fatal("the toast stays")
	}
	if _, err := os.Stat(trash); !os.IsNotExist(err) {
		t.Errorf("trash kept: %v", err)
	}
	if _, err := a.lib.svc.Voices.Restore(context.Background(), v.ID); err == nil {
		t.Error("restored after the purge")
	}

	// A delete cut short: the voice is in the library, its files in the
	// trash. The next start puts them back.
	w := addVoice(t, a, core.Voice{Name: "Ines", Kind: core.VoiceCloned, RefText: "Hello."}, 6)
	entry := filepath.Join(filepath.Dir(trash), w.ID)
	os.MkdirAll(entry, 0o755)
	os.WriteFile(filepath.Join(entry, "voice.json"), []byte(`{}`), 0o644)
	if err := os.Rename(filepath.Dir(a.lib.svc.Voices.FilePath(w, w.RefAudio)), filepath.Join(entry, "files")); err != nil {
		t.Fatal(err)
	}
	a.lib.svc.Voices.PurgeTrash(context.Background())
	if _, err := os.Stat(a.lib.svc.Voices.FilePath(w, w.RefAudio)); err != nil {
		t.Errorf("files not back: %v", err)
	}
	if _, err := os.Stat(entry); !os.IsNotExist(err) {
		t.Errorf("entry kept: %v", err)
	}
}

// TestVoiceDeleteUsage: the confirmation names where the voice is used
// (a story, a dub's cast, a workflow, the Studio, the batch queue, an
// agent), and after the delete those places say so instead of using
// another voice in silence.
func TestVoiceDeleteUsage(t *testing.T) {
	a, _ := newVoiceTestApp(t)
	tt := ui.NewTester(a.view, 1200, 900)
	ctx := context.Background()
	v := addVoice(t, a, core.Voice{Name: "Mara", Kind: core.VoiceCloned, RefText: "Hello there."}, 6)
	other := addVoice(t, a, core.Voice{Name: "Unused", Kind: core.VoiceCloned, RefText: "Hello."}, 6)

	// A story whose narrator is the voice.
	doc := services.LongformDoc{Kind: core.ProjectStory, Title: "Night Ferry", Voice: v.ID, Model: qwenBase,
		Lines: []services.StoryLine{{ID: "l1", Text: "The ferry left at eleven."}}}
	if _, err := a.lib.svc.Library.SaveDoc(ctx, core.Project{Kind: core.ProjectStory, Title: "Night Ferry"}, doc); err != nil {
		t.Fatal(err)
	}
	// A dub casting it, a workflow speaking with it.
	if _, err := a.dubSvc.Create(ctx, &dub.Project{Title: "Interview", Cast: map[string]string{"S1": v.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.wfSvc.Save(ctx, &workflow.Workflow{Name: "Morning news", Nodes: []workflow.Node{{ID: "n1", Kind: workflow.KindSpeak, P: workflow.Params{Voice: v.ID}}}}); err != nil {
		t.Fatal(err)
	}
	a.studioVoice = v.ID
	a.batch.settings.Voice = v.ID
	a.lib.settings.Update(ctx, func(s *settings.Settings) {
		s.API.Bindings = []settings.APIBinding{{Client: "claude-code", Label: "Claude Code", Voice: v.ID}}
	})
	tt.Frame()

	a.navigate("/voices")
	a.askDeleteVoice(v.ID)
	tt.Frame()
	text := strings.Join(tt.Texts(), "\n")
	for _, want := range []string{"It is used in 6 places", "“Night Ferry”", "“Interview”", "“Morning news”", "…", "choose another voice there"} {
		if !strings.Contains(text, want) {
			t.Errorf("confirmation lacks %q: %s", want, text)
		}
	}
	// Unused: today's text alone.
	a.askDeleteVoice(other.ID)
	tt.Frame()
	if tt.HasText("Checking where it is used…") || strings.Contains(strings.Join(tt.Texts(), "\n"), "It is used in") {
		t.Errorf("an unused voice: %q", tt.Texts())
	}
	a.deleteOpen = false
	tt.Frame()

	a.askDeleteVoice(v.ID)
	tt.Frame()
	tt.Click("Delete voice")
	tt.Frame()
	if a.findVoice(v.ID) != nil {
		t.Fatal("not deleted")
	}
	if !hasOp(a, "voice.delete.ask", "places=6") || !hasOp(a, "voice.delete", "id="+v.ID, "segments=1") {
		t.Errorf("not logged: %q", opLines(a))
	}
	// The Studio says its voice went; the other places name it deleted.
	if a.studioVoice != "" || !strings.Contains(strings.Join(tt.Texts(), "\n"), "was the Studio's voice") {
		t.Errorf("studio: %q %q", a.studioVoice, tt.Texts())
	}
	if a.lfVoiceLabel(v.ID) != "Voice deleted" || a.dubVoiceLabel(v.ID) != "Voice deleted" || a.wfVoiceLabel(v.ID) != "Voice deleted" {
		t.Errorf("labels: %q %q %q", a.lfVoiceLabel(v.ID), a.dubVoiceLabel(v.ID), a.wfVoiceLabel(v.ID))
	}
	// A story render stops with a clear message, never another voice.
	_, probs := services.Compile(&doc, a.lfResolver(&doc))
	found := false
	for _, p := range probs {
		if p.Code == services.ProblemMissingVoice && p.Blocking {
			found = problemText(p) == "A voice it uses was deleted: choose another one."
		}
	}
	if !found {
		t.Errorf("story problems: %+v", probs)
	}
	// Undo: the Studio has its voice again.
	tt.Click("Undo")
	tt.Frame()
	if a.findVoice(v.ID) == nil || a.studioVoice != v.ID {
		t.Errorf("undo: studio %q", a.studioVoice)
	}
	if u, _ := a.lib.svc.Voices.Usage(ctx, v.ID); len(u.Projects) != 1 || u.Segments != 1 {
		t.Errorf("the story's link not back: %+v", u)
	}
}

// TestOpLog: the operations log has a failed job's error, settings
// changes by name only (never a secret), and no script text.
func TestOpLog(t *testing.T) {
	a, eng, tt := newTestApp(t)
	ctx := context.Background()
	const script = "The lighthouse keeper whispered a secret sentence."
	a.text = script
	tt.Frame()
	tt.Click("Generate")
	id := eng.last(t).ID
	eng.progress(id, "generating", 0.1, 0)
	eng.fail(id, "oom", "out of memory")
	tt.Frame()
	if !hasOp(a, "job.start", "id="+id) || !hasOp(a, "job.fail", "id="+id, "code=oom", `error="out of memory"`) {
		t.Errorf("job lines: %q", opLines(a))
	}
	const secret = "proxy-pass-0123456789abcdef"
	a.lib.settings.Update(ctx, func(s *settings.Settings) {
		s.Models.Proxy = "http://user:" + secret + "@127.0.0.1:7897"
		s.API.Port = 3999
	})
	if !hasOp(a, "settings.change", "models.proxy", "api.port") {
		t.Errorf("settings: %q", opLines(a))
	}
	for _, l := range opLines(a) {
		if strings.Contains(l, secret) || strings.Contains(l, "lighthouse") {
			t.Errorf("leaks: %s", l)
		}
	}
	for _, e := range a.logs.Tail(5000, "") {
		if strings.Contains(e.Message, secret) || strings.Contains(e.Message, script) {
			t.Errorf("log leaks: %s", e.Message)
		}
	}
}
