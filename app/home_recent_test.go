package main

// Thread #143: Home's Recent rows have ⋯ (Open, Delete…) and Delete on a
// focused row, asked first, for every kind; a draft's render is named
// after its first words.

import (
	"context"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/services"
)

// importDraftRender records a finished render of a draft never saved,
// stored as 「Untitled」 as the older versions did.
func importDraftRender(t *testing.T, a *app, kind core.ProjectKind, script string) core.Render {
	t.Helper()
	spec := services.LongformSpec{Kind: kind, Title: "Untitled", Chapters: []services.ChapterSpec{{Lines: []services.LineSpec{{Spans: []services.SpanSpec{{Text: script}}}}}}}
	r, err := a.lib.svc.Longform.ImportRender(context.Background(), spec, []services.ImportedAudio{{Samples: make([]float32, 2400), Rate: 24000, Channels: 1}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// homeMore opens the ⋯ of the Recent row titled title.
func homeMore(t *testing.T, tt *ui.Tester, title string) {
	t.Helper()
	row, ok := tt.Find(title)
	if !ok {
		t.Fatalf("no row %q: %q", title, tt.Texts())
	}
	more, ok := tt.Find(i18n.T("Item actions"))
	if !ok {
		t.Fatal("no ⋯")
	}
	tt.ClickAt(more.X+more.W/2, row.Y+row.H/2)
	if tt.Menu() == nil {
		t.Fatalf("%s: no menu", title)
	}
}

func TestHomeRecentDeleteEachKind(t *testing.T) {
	d := listFixture(t)
	a, tt := d.a, d.tt
	tt.SetSize(1300, 1000)
	r := importDraftRender(t, a, core.ProjectAudiobook, "[pause 300ms] The lighthouse keeper counted the ships every night.")
	show := func() {
		a.navigate("/home")
		a.homeUI.tab, a.homeUI.readAt = 0, a.homeUI.readAt.AddDate(-1, 0, 0)
		a.homeReadRecent()
		settle(tt)
	}
	show()
	if !tt.HasText("The lighthouse…") || tt.HasText("Untitled") {
		t.Fatalf("the draft's render: %q", tt.Texts())
	}
	items := map[string]homeItem{}
	for _, it := range a.homeItems(20) {
		items[it.title] = it
	}
	dubTitle := ""
	for _, it := range a.homeItems(20) {
		if strings.HasPrefix(it.key, "dub:") {
			dubTitle = it.title
		}
	}
	if dubTitle == "" {
		t.Fatalf("no dub in Recent: %v", items)
	}
	gone := map[string]func() bool{
		"The lighthouse…": func() bool { _, _, err := a.lib.svc.Longform.Get(context.Background(), r.ID); return err != nil },
		"Night ferry": func() bool {
			return len(a.projItems(projStory, "")) == 0
		},
		dubTitle:                           func() bool { return len(a.dubList) == 0 },
		"Weekly meeting":                   func() bool { return len(a.trList) == 0 },
		"Field recording":                  func() bool { return len(a.isoList) == 0 },
		"The last ferry leaves at eleven.": func() bool { return a.findTake("T0") == nil },
	}
	// Delete on a focused row asks too.
	row, _ := tt.Find("Weekly meeting")
	tt.Press(row.X+row.W/2, row.Y+row.H/2)
	tt.Release(1, 1)
	settle(tt)
	tt.Key(0, ui.KeyDelete)
	settle(tt)
	if !a.homeUI.askOpen || a.homeUI.ask.title != "Weekly meeting" {
		t.Fatalf("Delete key: %v %q", a.homeUI.askOpen, a.homeUI.ask.title)
	}
	a.homeUI.askOpen = false
	settle(tt)
	for title, isGone := range gone {
		show()
		homeMore(t, tt, title)
		if err := tt.ChooseMenuItem("Delete"); err != nil {
			t.Fatalf("%s: %v %q", title, err, tt.Menu())
		}
		tt.Frame()
		if !a.homeUI.askOpen || isGone() {
			t.Fatalf("%s: deleted without asking, or no question", title)
		}
		name := core.TruncateName(title, 40)
		if !tt.HasText("Delete “" + name + "”?") {
			t.Fatalf("%s: question %q", title, tt.Texts())
		}
		if err := tt.Click("Delete"); err != nil {
			t.Fatal(err)
		}
		settle(tt)
		a.projects.dirty = true
		settle(tt)
		if !isGone() {
			t.Fatalf("%s: not deleted (%q)", title, a.toasts)
		}
		show()
		if tt.HasText(title) {
			t.Fatalf("%s: still in Recent: %q", title, tt.Texts())
		}
	}
	if !tt.HasText("Nothing made yet") {
		t.Errorf("Recent after: %q", tt.Texts())
	}
}

// ⋯ → Open opens the row's item.
func TestHomeRecentMenuOpen(t *testing.T) {
	d := listFixture(t)
	a, tt := d.a, d.tt
	tt.SetSize(1300, 1000)
	a.navigate("/home")
	a.homeUI.tab = 0
	a.homeReadRecent()
	settle(tt)
	homeMore(t, tt, "Weekly meeting")
	if err := tt.ChooseMenuItem("Open"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if a.trSel != "t1" {
		t.Errorf("open: %q on %s", a.trSel, a.router.Path())
	}
}

// A render of a draft without a title: the lists show its first words
// (the stored title stays); a project's render keeps its title; new
// renders of a draft are stored under the first words.
func TestRenderNameFromFirstWords(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.SetSize(1300, 1000)
	old := importDraftRender(t, a, core.ProjectStory, "林间小路上，一只狐狸正在追逐月光下的萤火虫，它跑得飞快。")
	tt.Frame()
	want := "林间小路上，一只狐狸正在追逐月光下的萤火…"
	if got, _, _ := a.lib.svc.Longform.Get(context.Background(), old.ID); got.Title != "Untitled" {
		t.Errorf("stored title rewritten: %q", got.Title)
	}
	if r := a.lfRenders[old.ID]; r == nil || r.name != want {
		t.Fatalf("followed: %+v", r)
	}
	if got := a.renderTitle(old.ID, "未命名"); got != want {
		t.Errorf("renderTitle: %q", got)
	}
	// A render the app has not followed: read, then named.
	delete(a.lfRenders, old.ID)
	if got := a.renderTitle(old.ID, "Untitled"); got != "Untitled" {
		t.Errorf("before reading: %q", got)
	}
	tt.Frame()
	if got := a.renderTitle(old.ID, "Untitled"); got != want {
		t.Errorf("after reading: %q", got)
	}
	// A summary-only update (RenderChanged) keeps the name.
	rec := a.lfRenders[old.ID].rec
	body := a.lfRenders[old.ID].body
	body.Spec.Chapters = nil
	rec.Body = mustJSON(t, body)
	a.putRender(rec)
	if a.lfRenders[old.ID].name != want {
		t.Errorf("after a published body: %q", a.lfRenders[old.ID].name)
	}
	// Assets and the API's list_assets.
	a.navigate("/assets")
	settle(tt)
	if !tt.HasText(want) || tt.HasText("Untitled") {
		t.Errorf("Assets: %q", tt.Texts())
	}
	b := &apiBackend{lib: a.lib}
	assets, err := b.Assets(context.Background(), api.AssetQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) == 0 || assets[0].Title != want {
		t.Errorf("list_assets: %+v", assets)
	}
	// Titled and project renders keep their titles.
	for _, c := range []struct {
		rec  core.Render
		want string
	}{
		{core.Render{Title: "Night ferry"}, "Night ferry"},
		{core.Render{Title: "Untitled", ProjectID: "P1"}, ""},
		{core.Render{Title: " 未命名 "}, "Hi."},
	} {
		spec := services.LongformSpec{Chapters: []services.ChapterSpec{{Lines: []services.LineSpec{{Spans: []services.SpanSpec{{Text: "Hi."}}}}}}}
		if got := renderName(c.rec, &spec); got != c.want {
			t.Errorf("%+v: %q, want %q", c.rec, got, c.want)
		}
	}
}
