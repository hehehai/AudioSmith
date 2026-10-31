package main

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// The headers' ‹ › go through the window's history: back twice, forward,
// disabled at the ends; ⌘[ ⌘] and a mouse's back button do the same.
func TestHeaderHistory(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.router.Replace("/home")
	tt.Frame()
	if headerNav.canForward || headerNav.canBack { // nothing before Home
		t.Fatalf("history at the start: back %v forward %v", headerNav.canBack, headerNav.canForward)
	}
	a.navigate("/projects")
	tt.Frame()
	a.navigate("/assets")
	tt.Frame()
	back, fwd := "Back (⌘[)", "Forward (⌘])"
	click := func(s, want string) {
		t.Helper()
		if err := tt.Click(s); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if p := a.router.Path(); p != want {
			t.Fatalf("%s: %s, want %s", s, p, want)
		}
	}
	click(back, "/projects")
	click(back, "/home")
	if headerNav.canBack || !headerNav.canForward {
		t.Errorf("at Home: back %v forward %v", headerNav.canBack, headerNav.canForward)
	}
	click(back, "/home") // disabled: nothing before
	click(fwd, "/projects")
	tt.Key(ui.Cmd, ui.KeyBracketRight)
	tt.Frame()
	if p := a.router.Path(); p != "/assets" {
		t.Fatalf("⌘]: %s", p)
	}
	if headerNav.canForward {
		t.Error("forward at the end")
	}
	click(fwd, "/assets") // disabled at the end
	tt.Key(ui.Cmd, ui.KeyBracketLeft)
	tt.Frame()
	if p := a.router.Path(); p != "/projects" {
		t.Fatalf("⌘[: %s", p)
	}
	tt.Key(0, ui.KeyBack) // a mouse's back button (MyGo's router)
	tt.Frame()
	if p := a.router.Path(); p != "/home" {
		t.Fatalf("mouse back: %s", p)
	}
	// ‹ closes Settings and goes back.
	a.navigate("/studio")
	tt.Frame()
	a.navigate("/settings/general")
	tt.Frame()
	a.goBack()
	tt.Frame()
	if a.settingsOpen || a.router.Path() != "/home" {
		t.Errorf("back from Settings: %v %s", a.settingsOpen, a.router.Path())
	}
}

// There is no bar over the pages: each page's header is at the window's
// top, Home's has only ‹ ›, and no breadcrumb or top search is left.
func TestNoTopBar(t *testing.T) {
	a, _, tt := newTestApp(t)
	for _, p := range []string{"/home", "/studio", "/voices/mine", "/projects", "/assets", "/dub", "/transcripts", "/isolate",
		"/dictation", "/stories", "/audiobook", "/workflows", "/batch", "/calls", "/developers", "/tasks", "/voices/new", "/nowhere"} {
		a.navigate(p)
		tt.Frame()
		tt.Frame()
		r, ok := tt.Find("Page header")
		if !ok || r.Y != 0 {
			t.Errorf("%s: header %+v %v", p, r, ok)
		}
		for _, s := range []string{"Breadcrumb", "Search everything…"} {
			if tt.HasText(s) {
				t.Errorf("%s: %q shows", p, s)
			}
		}
		if _, ok := tt.Find("Back (⌘[)"); !ok {
			t.Errorf("%s: no ‹", p)
		}
	}
	a.navigate("/home")
	tt.Frame()
	if _, ok := tt.Find("What would you like to create?"); !ok {
		t.Errorf("Home: %q", tt.Texts())
	}
	a.navigate("/studio")
	tt.Frame()
	if countText(tt.Texts(), "Text to speech") < 2 { // the sidebar's and the header's
		t.Errorf("Studio's title: %q", tt.Texts())
	}
	if _, ok := tt.Find("Hide settings and history"); !ok {
		t.Error("the panel's toggle is not in the header")
	}
}

func countText(texts []string, s string) int {
	n := 0
	for _, x := range texts {
		if x == s {
			n++
		}
	}
	return n
}

// The sidebar's search opens the palette on a click; ⌘F puts the keyboard
// in it, and what is typed goes on in the palette; with the rail, a
// magnifier opens the palette.
func TestSidebarSearch(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.Frame()
	r, ok := tt.Find("Search everything")
	home, _ := tt.Find("Home")
	if !ok || r.Y >= home.Y || r.X > a.sideW {
		t.Fatalf("search %+v (Home %+v)", r, home)
	}
	if err := tt.Click("Search everything"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.paletteOpen {
		t.Fatal("a click did not open the palette")
	}
	a.paletteOpen = false
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyF)
	tt.Frame()
	tt.Frame()
	if a.paletteOpen || !a.sideKeyed {
		t.Fatalf("⌘F: palette %v keyed %v", a.paletteOpen, a.sideKeyed)
	}
	tt.Type("voi")
	tt.Frame()
	tt.Frame()
	if !a.paletteOpen || !strings.HasPrefix(a.paletteQuery, "v") || a.sideQuery != "" {
		t.Fatalf("typing: open %v query %q side %q", a.paletteOpen, a.paletteQuery, a.sideQuery)
	}
	a.paletteOpen = false
	tt.Frame()
	if err := tt.Click("Command palette"); err != nil {
		t.Fatal(err)
	}
	if !a.paletteOpen {
		t.Error("the ⌘K key did not open the palette")
	}
	a.paletteOpen = false
	a.setRail(true)
	tt.Frame()
	if err := tt.Click("Search everything"); err != nil {
		t.Fatal(err)
	}
	if !a.paletteOpen {
		t.Error("the rail's magnifier did not open the palette")
	}
	a.paletteOpen = false
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyF)
	tt.Frame()
	if !a.paletteOpen {
		t.Error("⌘F with the rail did not open the palette")
	}
}

// The bell is at the sidebar's foot beside Settings (and in the rail),
// and lists a running job.
func TestSidebarBell(t *testing.T) {
	a, eng, tt := newTestApp(t)
	tt.Frame()
	id, _ := eng.host.sched.Submit(context.Background(), jobs.Spec{Kind: "x.batch", Lane: jobs.LaneCPU, Title: "Batch export", Owner: "project:p1"})
	eng.update(id, func(j *jobs.Job) { j.State = jobs.StateRunning; j.Progress.Fraction = 0.4 })
	tt.Frame()
	bell, ok := tt.Find("Tasks and notifications")
	set, _ := tt.Find("Settings")
	mid := func(r ui.Rect) float32 { return r.Y + r.H/2 }
	if d := mid(bell) - mid(set); !ok || bell.X > a.sideW || bell.X < set.X+set.W || d < -6 || d > 6 {
		t.Fatalf("bell %+v, Settings %+v", bell, set)
	}
	if err := tt.Click("Tasks and notifications"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Batch export") || !tt.HasText("View all") {
		t.Fatalf("popover: %q", tt.Texts())
	}
	a.noticesOpen = false
	a.setRail(true)
	tt.Frame()
	if err := tt.Click("Tasks and notifications"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Batch export") {
		t.Errorf("rail popover: %q", tt.Texts())
	}
	eng.cancelled(id)
}

// Assets' header: the count, the search that filters, Open folder.
func TestAssetsHeader(t *testing.T) {
	a, _, tt := newTestApp(t)
	at := time.Now()
	addTake(t, a, core.Take{ID: "A", CreatedAt: at, Text: "The quick brown fox.", Model: kokoro, Voice: "af_heart", AudioSeconds: 1}, 0.3)
	addTake(t, a, core.Take{ID: "B", CreatedAt: at.Add(time.Second), Text: "A slow green turtle.", Model: kokoro, Voice: "af_heart", AudioSeconds: 1}, 0.3)
	var opened []string
	openPath = func(p string) { opened = append(opened, p) }
	t.Cleanup(func() { openPath = func(string) {} })
	a.navigate("/assets")
	tt.Frame()
	if !tt.HasText("Assets") || !tt.HasText("2 takes") {
		t.Fatalf("header: %q", tt.Texts())
	}
	if err := tt.Click("Search takes"); err != nil {
		t.Fatal(err)
	}
	tt.Type("turtle")
	for range 3 {
		tt.Frame()
	}
	if a.history.query != "turtle" || !tt.HasText("1 found") || tt.HasText("The quick brown fox.") {
		t.Fatalf("search %q: %q", a.history.query, tt.Texts())
	}
	if err := tt.Click("Open folder"); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 1 || opened[0] != a.takeStore.AudioDir() {
		t.Errorf("opened %q", opened)
	}
}

// Projects' header: the count and the views.
func TestProjectsHeader(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.navigate("/projects")
	tt.Frame()
	tt.Frame()
	if !tt.HasText("Projects") || !tt.HasText("0 items") {
		t.Fatalf("header: %q", tt.Texts())
	}
	if _, ok := tt.Find("Search the library"); !ok {
		t.Error("no search in the header")
	}
	if err := tt.Click("Cards (⌥2)"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.projects.cards != 1 {
		t.Errorf("cards %d", a.projects.cards)
	}
	if err := tt.Click("List (⌥1)"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.projects.cards != 0 {
		t.Errorf("list %d", a.projects.cards)
	}
}

// In a narrow window (820×520 at 125 %) the search is a magnifier that
// opens the field, and the less used tools are in ⋯.
func TestHeaderNarrow(t *testing.T) {
	a, _, tt := newTestApp(t)
	clear(headSearchOpen) // what earlier tests opened
	tt.SetSize(820, 520)
	a.updateSettings(func(s *settings.Settings) { s.Appearance.Scale = 1.25 })
	a.navigate("/assets")
	tt.Frame()
	tt.Frame()
	if _, ok := tt.Find("More tools"); !ok {
		t.Fatalf("no ⋯: %q", tt.Texts())
	}
	if tt.HasText("All models") {
		t.Error("the model's select shows narrow")
	}
	if headSearchOpen["history-search"] {
		t.Error("the search field shows narrow before it is opened")
	}
	if err := tt.Click("Search takes"); err != nil { // the magnifier
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if !headSearchOpen["history-search"] {
		t.Fatalf("the field did not open: %q", tt.Texts())
	}
	tt.Type("fox")
	tt.Frame()
	if a.history.query != "fox" {
		t.Errorf("query %q", a.history.query)
	}
	if err := tt.Click("More tools"); err != nil {
		t.Fatal(err)
	}
	if m := tt.Menu(); !slices.Contains(m, "Model") {
		t.Errorf("⋯ menu %q", m)
	}
	tt.CloseMenu()
	// The header keeps its title and ‹ › in the narrow window.
	if r, ok := tt.Find("Back (⌘[)"); !ok || r.Y > titleBarHeight {
		t.Errorf("‹ %+v %v", r, ok)
	}
}
