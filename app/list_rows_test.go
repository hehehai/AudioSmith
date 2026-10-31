package main

// The row kit (ui_list.go, thread #124): no list shows its columns'
// titles or draws lines under its rows, a row's body opens its item,
// Delete on a focused row asks to delete it, and at 820×520 @125 % the
// rows' actions stay in the window.

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
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// listFixture is an app with a story, two dubs, a transcript, an
// isolation, a take and a saved voice.
func listFixture(t *testing.T) *dubTest {
	t.Helper()
	d := newDubTest(t, 3)
	a, eng := d.a, d.eng
	now := time.Now()
	addTake(t, a, core.Take{ID: "T0", CreatedAt: now, Text: "The last ferry leaves at eleven.", Model: kokoro, Voice: "af_heart", AudioSeconds: 3}, 0.3)
	doc := services.NewLongformDoc(core.ProjectStory, kokoro)
	doc.Title = "Night ferry"
	doc.Lines = []services.StoryLine{{ID: "l0", Text: "Hello."}}
	a.lib.svc.Library.SaveDoc(context.Background(), core.Project{Kind: core.ProjectStory, Title: doc.Title}, doc)
	a.dubStartFile(d.src)
	tr := sampleTranscript("t1", now.Add(-time.Hour))
	tr.Title = "Weekly meeting"
	putTranscript(t, a, tr)
	fakeOnDisk(t, a, isoSeparator())
	fakeOnDisk(t, a, isoDenoiser())
	a.isoEngine = isoFakeEngine(nil)
	eng.executors[services.KindIsolate] = a.isoSvc.Executor()
	src := filepath.Join(t.TempDir(), "Field recording.wav")
	copyTestFile(t, d.src, src)
	a.isoStart(src)
	addVoice(t, a, core.Voice{Name: "Ada", Kind: core.VoiceCloned, Language: "en"}, 3)
	a.projects.dirty = true
	return d
}

// listPages are the pages whose lists use the row kit, with a title one
// of their rows shows.
var listPages = []struct{ path, row string }{
	{"/projects", "Night ferry"},
	{"/stories", "Night ferry"},
	{"/assets", "Weekly meeting"},
	{"/dub", "Interview"},
	{"/transcripts", "Weekly meeting"},
	{"/isolate", "Field recording"},
	{"/voices/mine", "Ada"},
	{"/home", "Night ferry"},
}

func settle(tt *ui.Tester) {
	for range 4 {
		tt.Frame()
	}
}

func TestListsHaveNoColumnTitles(t *testing.T) {
	d := listFixture(t)
	a, tt := d.a, d.tt
	// (Status and Languages name the header's filters, not columns.)
	titles := []string{"Name", "Title", "Length", "Created", "Updated", "Date"}
	for _, p := range listPages {
		a.navigate(p.path)
		settle(tt)
		texts := tt.Texts()
		if !slices.ContainsFunc(texts, func(s string) bool { return strings.Contains(s, p.row) }) {
			t.Errorf("%s: no row %q in %q", p.path, p.row, texts)
			continue
		}
		for _, w := range titles {
			if slices.Contains(texts, w) {
				t.Errorf("%s shows a column title %q", p.path, w)
			}
		}
	}
}

// TestListsDrawNoRowLines: the lists' sources draw no hairline under a
// row (the row kit's rows are rounded bands) and no list header.
func TestListsDrawNoRowLines(t *testing.T) {
	for _, f := range []string{"ui_list.go", "projects_view.go", "assets_view.go", "dub_list.go", "transcribe_view_history.go",
		"isolate_view.go", "voices_view.go", "voices_table.go", "gallery_view.go", "studio_history.go", "home_view.go", "ui_kit_nav.go"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"BorderWidth(0, 0, 1, 0)", "listHead(", "listColumn{"} {
			if f == "ui_kit_nav.go" && bad == "BorderWidth(0, 0, 1, 0)" {
				// the underline tabs' hairline, not a row's
				if strings.Count(string(src), bad) > 1 {
					t.Errorf("%s: a row draws %s", f, bad)
				}
				continue
			}
			if strings.Contains(string(src), bad) {
				t.Errorf("%s still has %s", f, bad)
			}
		}
	}
}

func TestListRowsOpenTheirItem(t *testing.T) {
	d := listFixture(t)
	a, tt := d.a, d.tt
	settle(tt)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatalf("%s: %v (%q)", a.router.Path(), err, tt.Texts())
		}
		settle(tt)
	}
	a.navigate("/projects")
	settle(tt)
	click("Night ferry")
	if p := a.router.Path(); !strings.HasPrefix(p, "/stories") {
		t.Errorf("project row → %s", p)
	}
	a.navigate("/isolate")
	settle(tt)
	click("Field recording")
	if p := a.router.Path(); !strings.HasPrefix(p, "/isolate/") {
		t.Errorf("isolation row → %s", p)
	}
	a.navigate("/transcripts")
	settle(tt)
	click("Weekly meeting")
	if a.trSel != "t1" {
		t.Errorf("transcript row → selected %q", a.trSel)
	}
	a.navigate("/voices/mine")
	settle(tt)
	click("Ada")
	if a.edit == nil || a.edit.name != "Ada" {
		t.Error("voice row: no detail")
	}
	a.navigate("/home")
	a.homeUI.tab = 0
	a.homeReadRecent()
	settle(tt)
	click("Weekly meeting")
	if a.trSel != "t1" || !strings.HasPrefix(a.router.Path(), "/transcripts") {
		t.Errorf("recent row → %s", a.router.Path())
	}
}

// TestListRowDeleteKey: Delete on a focused row asks to delete its item.
func TestListRowDeleteKey(t *testing.T) {
	d := listFixture(t)
	a, tt := d.a, d.tt
	a.navigate("/isolate")
	settle(tt)
	id := a.isoList[0].ID
	r, ok := tt.Find("Field recording")
	if !ok {
		t.Fatal("no row")
	}
	// Focus the row's body without opening it: press, then move away
	// before letting go.
	tt.Press(r.X+r.W/2, r.Y+r.H/2)
	tt.Release(1, 1)
	settle(tt)
	tt.Key(0, ui.KeyDelete)
	settle(tt)
	if !a.isoConfirmOpen || a.isoConfirm != id {
		t.Fatalf("Delete: confirm %v %q", a.isoConfirmOpen, a.isoConfirm)
	}
}

// TestListActionsFitNarrow: at 820×520 @125 % each list's ⋯ stays in
// the window.
func TestListActionsFitNarrow(t *testing.T) {
	d := listFixture(t)
	a, tt := d.a, d.tt
	a.updateSettings(func(s *settings.Settings) { s.Appearance.Scale = 1.25 })
	tt.SetSize(820, 520)
	for _, p := range []struct{ path, more string }{
		{"/projects", "Project actions"}, {"/dub", "Dub actions"}, {"/transcripts", "Transcript actions"},
		{"/isolate", "Isolation actions"}, {"/assets", "Asset actions"}, {"/voices/mine", "Use"},
	} {
		a.navigate(p.path)
		settle(tt)
		r, ok := tt.Find(p.more)
		if !ok {
			t.Errorf("%s: no %q", p.path, p.more)
			continue
		}
		if r.X < 0 || r.X+r.W > 820 {
			t.Errorf("%s: %q at %v, outside 820", p.path, p.more, r)
		}
	}
}
