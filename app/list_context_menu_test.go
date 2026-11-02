package main

// Right-click menus (thread #144): a row of the kit (ui_list.go) whose
// spec has a menu shows its ⋯ items where it is clicked with the
// secondary button, chosen items run, and the row takes the focus; a
// row without a ⋯ has no menu. The lists' rows show the same items in
// both menus.

import (
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestListRowContextMenu: the kit's row, with and without a menu.
func TestListRowContextMenu(t *testing.T) {
	var opened, renamed, deleted, asked int
	view := func(c *ui.Context) {
		ui.Column(c).Children(func() {
			menu := func(m *ui.Menu) {
				if m.Item("Open").Chosen() {
					opened++
				}
				if m.Item("Rename").Chosen() {
					renamed++
				}
				m.Separator()
				if m.Item("Delete").Chosen() {
					deleted++
				}
			}
			listRow(c, listRowSpec{key: "a", label: "Alpha", menu: menu, menuTip: "Alpha actions", del: func() { asked++ }}, func() {
				listTitle(c, "Alpha", "first")
			}, nil)
			listRow(c, listRowSpec{key: "b", label: "Beta"}, func() { listTitle(c, "Beta", "second") }, nil)
		})
	}
	tt := ui.NewTester(view, 800, 300)
	settle(tt)
	// The ⋯ is the row's last action.
	if err := tt.Click("Alpha actions"); err != nil {
		t.Fatal(err)
	}
	more := tt.Menu()
	if want := []string{"Open", "Rename", "-", "Delete"}; !slices.Equal(more, want) {
		t.Fatalf("⋯ %q", more)
	}
	tt.CloseMenu()
	settle(tt)
	if err := tt.RightClick("first"); err != nil {
		t.Fatal(err)
	}
	if got := tt.Menu(); !slices.Equal(got, more) {
		t.Fatalf("right-click %q, ⋯ %q", got, more)
	}
	if err := tt.ChooseMenuItem("Rename"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if renamed != 1 || opened != 0 || deleted != 0 {
		t.Errorf("chose Rename: open %d rename %d delete %d", opened, renamed, deleted)
	}
	// The row took the focus: Delete asks to delete it.
	if !tt.Focused("Alpha") {
		t.Error("the row has not the focus")
	}
	tt.Key(0, ui.KeyDelete)
	settle(tt)
	if asked != 1 {
		t.Errorf("Delete after the menu: asked %d", asked)
	}
	// No ⋯, no menu.
	if err := tt.RightClick("second"); err != nil {
		t.Fatal(err)
	}
	if got := tt.Menu(); got != nil {
		t.Errorf("a row without ⋯ shows %q", got)
	}
}

// TestListsContextMenuMatchesMore: on each list, a row's right-click
// menu has its ⋯'s items.
func TestListsContextMenuMatchesMore(t *testing.T) {
	d := listFixture(t)
	a, tt := d.a, d.tt
	tt.SetSize(1300, 1000)
	for _, p := range []struct{ path, row, more string }{
		{"/projects", "Night ferry", "Project actions"},
		{"/dub", "Interview", "Dub actions"},
		{"/transcripts", "Weekly meeting", "Transcript actions"},
		{"/isolate", "Field recording", "Isolation actions"},
		{"/assets", "The last ferry leaves at eleven.", "Asset actions"},
		{"/voices/mine", "Ada", "More actions"},
		{"/home", "Weekly meeting", "Item actions"},
	} {
		a.navigate(p.path)
		if p.path == "/home" {
			a.homeUI.tab = 0
			a.homeReadRecent()
		}
		settle(tt)
		row, ok := findPrefix(tt, p.row)
		if !ok {
			t.Errorf("%s: no row %q in %q", p.path, p.row, tt.Texts())
			continue
		}
		more, ok := tt.Find(p.more)
		if !ok {
			t.Errorf("%s: no %q", p.path, p.more)
			continue
		}
		tt.ClickAt(more.X+more.W/2, row.Y+row.H/2)
		want := tt.Menu()
		tt.CloseMenu()
		settle(tt)
		tt.RightClickAt(row.X+row.W/2, row.Y+row.H/2)
		got := tt.Menu()
		tt.CloseMenu()
		settle(tt)
		if len(want) == 0 || !slices.Equal(got, want) {
			t.Errorf("%s: right-click %q, ⋯ %q", p.path, got, want)
		}
	}
}

// findPrefix finds the text that starts with s.
func findPrefix(tt *ui.Tester, s string) (ui.Rect, bool) {
	for _, x := range tt.Texts() {
		if strings.HasPrefix(x, s) {
			return tt.Find(x)
		}
	}
	return ui.Rect{}, false
}

// Projects: a right-click on a story's row, then Rename, edits its name.
func TestProjectsRowContextMenu(t *testing.T) {
	d := listFixture(t)
	a, tt := d.a, d.tt
	a.navigate("/projects")
	settle(tt)
	if err := tt.RightClick("Night ferry"); err != nil {
		t.Fatal(err)
	}
	if got := tt.Menu(); !slices.Equal(got, []string{"Open", "Rename", "Duplicate", "-", "Delete"}) {
		t.Fatalf("menu %q", got)
	}
	if err := tt.ChooseMenuItem("Rename"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if a.projects.renaming == "" || a.projects.renameText != "Night ferry" {
		t.Errorf("rename: %q %q", a.projects.renaming, a.projects.renameText)
	}
	if !strings.HasPrefix(a.router.Path(), "/projects") {
		t.Errorf("the right-click opened the item: %s", a.router.Path())
	}
}

// Home · Recent: a right-click, then Open, opens the row's item.
func TestHomeRecentContextMenu(t *testing.T) {
	d := listFixture(t)
	a, tt := d.a, d.tt
	tt.SetSize(1300, 1000)
	a.navigate("/home")
	a.homeUI.tab = 0
	a.homeReadRecent()
	settle(tt)
	if err := tt.RightClick("Weekly meeting"); err != nil {
		t.Fatal(err)
	}
	if a.trSel == "t1" {
		t.Fatal("the right-click opened the item")
	}
	if err := tt.ChooseMenuItem("Open"); err != nil {
		t.Fatalf("%v %q", err, tt.Menu())
	}
	settle(tt)
	if a.trSel != "t1" || !strings.HasPrefix(a.router.Path(), "/transcripts") {
		t.Errorf("open: %q on %s", a.trSel, a.router.Path())
	}
}

// Dub editor: a right-click on a line shows its ⋯ items, makes it the
// current line, and an item chosen runs.
func TestDubLineContextMenu(t *testing.T) {
	d := dubEditorOpen(t)
	a, tt := d.a, d.tt
	settle(tt)
	if err := tt.Click("Line actions"); err != nil { // the first line's ⋯
		t.Fatal(err)
	}
	more := tt.Menu()
	tt.CloseMenu()
	settle(tt)
	first := a.dub.Segments[0]
	if err := tt.RightClick("Play from " + tlClock(first.Start)); err != nil {
		t.Fatal(err)
	}
	if got := tt.Menu(); len(more) == 0 || !slices.Equal(got, more) {
		t.Fatalf("right-click %q, ⋯ %q", got, more)
	}
	tt.CloseMenu()
	settle(tt)
	n := len(a.dub.Segments)
	second := a.dub.Segments[1]
	if err := tt.RightClick("Play from " + tlClock(second.Start)); err != nil {
		t.Fatal(err)
	}
	if a.dubCur != second.ID {
		t.Errorf("current line %q, want %q", a.dubCur, second.ID)
	}
	if err := tt.ChooseMenuItem("Merge with previous (⇧⌘M)"); err != nil {
		t.Fatalf("%v %q", err, tt.Menu())
	}
	settle(tt)
	if len(a.dub.Segments) != n-1 {
		t.Errorf("merge: %d lines, want %d", len(a.dub.Segments), n-1)
	}
}
