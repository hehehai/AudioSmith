package main

// Behaviour tests of 0.5 F1's structure: the sidebar's places, tools,
// More and foot; the old paths' redirects; the ⌘ numbers; the top bar's
// bell; Voices' tabs and New voice; Home's composer sending to the tools.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

// The app opens at Home.
func TestHomeStart(t *testing.T) {
	if p := newShellState().router.Path(); p != "/home" {
		t.Errorf("start page %s", p)
	}
	a, _, tt := newTestApp(t)
	a.navigate("/home")
	tt.Frame()
	for _, s := range []string{"What would you like to create?", "Recent", "Samples", "Nothing made yet"} {
		if !tt.HasText(s) {
			t.Errorf("Home lacks %q: %q", s, tt.Texts())
		}
	}
	// The tiles: one word each, and More's menu.
	for _, s := range []string{"Speech", "Clone", "Design", "Stories", "Audiobook", "Dub", "Transcribe", "Isolate", "More"} {
		if !tt.HasText(s) {
			t.Errorf("no tile %q", s)
		}
	}
	if err := tt.Click("Transcribe"); err != nil {
		t.Fatal(err)
	}
	if p := a.router.Path(); p != "/transcripts" {
		t.Errorf("tile: %s", p)
	}
	// Samples: coming soon.
	a.navigate("/home")
	a.homeUI.tab = 1
	tt.Frame()
	if !tt.HasText("A story for two voices") || !tt.HasText("A meeting transcript") {
		t.Errorf("samples: %q", tt.Texts())
	}
}

// Text goes to Text to speech's script; a link and a video to Dub; an
// audio file to Speech to text; a text file to Audiobook.
func TestHomeRouting(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.navigate("/home")
	tt.Frame()
	a.homeUI.text = "Welcome to the spring sale."
	tt.Frame()
	if err := tt.Click("Start (⌘↩)"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if p := a.router.Path(); p != "/studio" || a.text != "Welcome to the spring sale." {
		t.Fatalf("text: %s %q", p, a.text)
	}
	if a.homeUI.text != "" {
		t.Error("the composer kept the text")
	}

	dir := t.TempDir()
	write := func(name, data string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if d := homeFileDest("a/Clip.MOV"); d != homeDub {
		t.Errorf("mov: %q", d)
	}
	if d := homeFileDest("x.pdf"); d != homeNone {
		t.Errorf("pdf: %q", d)
	}

	// A video: a new dub of it.
	video := write("trailer.mp4", "not really a video")
	a.navigate("/home")
	a.homeUI.file = video
	tt.Frame()
	if !tt.HasText("→ Dub") {
		t.Errorf("file chip: %q", tt.Texts())
	}
	a.homeSubmit()
	tt.Frame()
	if p := a.router.Path(); p != "/dub" || a.dub == nil || a.dub.Source.Path != video {
		t.Fatalf("video: %s %+v", p, a.dub)
	}

	// An audio file: transcribed at once.
	audio := write("meeting.wav", "RIFF")
	a.navigate("/home")
	a.homeUI.file = audio
	a.homeSubmit()
	tt.Frame()
	if p := a.router.Path(); p != "/transcripts" || a.trJob == nil || a.trJob.title != "meeting.wav" {
		t.Fatalf("audio: %s %+v", p, a.trJob)
	}

	// A text file: the audiobook's manuscript.
	book := write("chapter.txt", "Chapter one.\n\nIt was a dark night.")
	a.navigate("/home")
	a.homeUI.file = book
	a.homeSubmit()
	for i := 0; i < 50 && a.lfBook.importing; i++ {
		time.Sleep(20 * time.Millisecond)
		tt.Frame()
	}
	tt.Frame()
	if p := a.router.Path(); p != "/audiobook" || !strings.Contains(a.lfBook.doc.Script, "dark night") {
		t.Fatalf("book: %s %q", p, a.lfBook.doc.Script)
	}

	// A link: a dub of it (or why not, on the Dub page).
	a.navigate("/home")
	a.homeUI.text = "https://example.com/watch?v=1"
	a.homeSubmit()
	if p := a.router.Path(); p != "/dub" || (a.dubErr == "" && a.dub.Source.URL == "") {
		t.Errorf("link: %s %q", p, a.dubErr)
	}

	// The model's answer picks a tool.
	for in, want := range map[string]homeDest{"stories": homeStories, " Audiobook.": homeAudiobook, "studio": homeStudio, "??": homeStudio, "story": homeStories} {
		if got := parseHomeDest(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
	// Without the writing help loaded, no smart routing toggle.
	a.navigate("/home")
	tt.Frame()
	if tt.HasText("Smart routing") {
		t.Error("smart routing shows without the model loaded")
	}
}

// The old paths go where their pages are now, links too.
func TestOldRoutes(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.Frame()
	for old, want := range map[string]string{
		"/gallery": "/voices/discover", "/voices": "/voices/mine", "/clone": "/voices/new/clone", "/design": "/voices/new/design",
		"/history": "/assets", "/tools": "/developers/tools",
		"/models": "/settings/models", "/engine": "/settings/engine", "/logs": "/settings/logs", "/integrations": "/settings/integrations",
		"/integrations/mcp": "/settings/integrations/mcp", "/integrations/twilio": "/integrations/twilio",
	} {
		a.settingsOpen = false
		a.navigate(old)
		tt.Frame()
		if got := a.shownPath(); got != want {
			t.Errorf("%s → %s, want %s", old, got, want)
		}
	}
	a.settingsOpen = false
	a.openURL("audiosmith://gallery")
	if p := a.router.Path(); p != "/voices/discover" {
		t.Errorf("deep link: %s", p)
	}
	a.openURL("audiosmith:///history")
	if p := a.router.Path(); p != "/assets" {
		t.Errorf("deep link: %s", p)
	}
	a.openURL("audiosmith://engine")
	if a.shownPath() != "/settings/engine" {
		t.Errorf("deep link: %s", a.shownPath())
	}
	tt.Frame()
	if !tt.HasText("Processes") || !tt.HasText("Restart engine") {
		t.Errorf("Settings → Engine: %q", tt.Texts())
	}
	a.navigate("/settings/logs")
	tt.Frame()
	if !tt.HasText("Show the logs panel") {
		t.Errorf("Settings → Logs: %q", tt.Texts())
	}
}

// ⌘1… open Home, Text to speech, Voices, Projects, Dub, Speech to text,
// Stories, Audiobook; the sidebar lists the places, the tools, More and
// the foot, and none of Models, Engine, Logs, Integrations.
func TestShortcutOrder(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.Frame()
	want := []string{"/home", "/studio", "/voices", "/projects", "/dub", "/transcripts", "/stories", "/audiobook"}
	if !slices.Equal(shortcutPages, want) {
		t.Fatalf("shortcuts %v", shortcutPages)
	}
	for i, p := range want {
		tt.Key(ui.Cmd, ui.Key1+ui.Key(i))
		tt.Frame()
		if got := section(a.router.Path()); got != p {
			t.Errorf("⌘%d: %s, want %s", i+1, got, p)
		}
	}
	for _, s := range []string{"Home", "Voices", "Projects", "Assets", "Text to speech", "New voice", "Stories", "Audiobook", "Dub",
		"Speech to text", "Voice isolator", "Dictation", "More", "Developers", "Settings", "Search everything…"} {
		if !tt.HasText(s) {
			t.Errorf("the window lacks %q", s)
		}
	}
	for _, s := range []string{"Models", "Engine", "Logs", "Integrations", "Workflows", "Batch", "Calls", "History", "Discover voices"} {
		if tt.HasText(s) {
			t.Errorf("the sidebar shows %q", s)
		}
	}
	// The palette has every page; the top bar's field opens it.
	if err := tt.Click("Search everything"); err != nil {
		t.Fatal(err)
	}
	if !a.paletteOpen {
		t.Error("the search field did not open the palette")
	}
}

// "··· More" is a menu of Workflows, Batch and Calls; the item stays lit
// on their pages.
func TestMoreMenu(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.Frame()
	if err := tt.Click("More"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.moreOpen || !tt.HasText("Workflows") || !tt.HasText("Batch") || !tt.HasText("Calls") || !tt.HasText("Dub many videos in one go") {
		t.Fatalf("menu: %q", tt.Texts())
	}
	if a.router.Path() != "/studio" || a.nav != "/studio" {
		t.Errorf("More navigated: %s %s", a.router.Path(), a.nav)
	}
	if err := tt.Click("Batch"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.router.Path() != "/batch" || a.moreOpen || a.nav != "/more" {
		t.Errorf("batch: %s %v %s", a.router.Path(), a.moreOpen, a.nav)
	}
	if !tt.HasText("Batch") {
		t.Errorf("breadcrumb: %q", tt.Texts())
	}
	// The rail has it too.
	a.setRail(true)
	tt.Frame()
	if err := tt.Click("More"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.railMore || !tt.HasText("Workflows") {
		t.Errorf("rail menu: %q", tt.Texts())
	}
}

// The bell counts what goes on and lists it in its popover.
func TestTasksBell(t *testing.T) {
	a, eng, tt := newTestApp(t)
	tt.Frame()
	other, _ := eng.host.sched.Submit(context.Background(), jobs.Spec{Kind: "x.batch", Lane: jobs.LaneCPU, Title: "Batch export", Owner: "project:p1"})
	eng.update(other, func(j *jobs.Job) { j.State = jobs.StateRunning; j.Progress.Fraction = 0.4 })
	tt.Frame()
	if len(a.actCards) != 1 || tt.HasText("Batch export") {
		t.Fatalf("cards %+v, shown before the bell opens: %q", a.actCards, tt.Texts())
	}
	if err := tt.Click("Tasks and notifications"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.noticesOpen || !tt.HasText("Batch export") || !tt.HasText("In progress") {
		t.Fatalf("popover: %q", tt.Texts())
	}
	if err := tt.Click("View all"); err != nil {
		t.Fatal(err)
	}
	if a.router.Path() != "/tasks" || a.noticesOpen {
		t.Errorf("view all: %s", a.router.Path())
	}
	eng.cancelled(other)
	tt.Frame()
	if len(a.actCards) != 0 {
		t.Errorf("after: %+v", a.actCards)
	}
}

// Voices is one page with two tabs; the sidebar's item opens the one
// shown last; New voice offers its two flows.
func TestVoicesTabs(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.navigate("/voices/discover")
	tt.Frame()
	if !tt.HasText("Discover") || !tt.HasText("Mine") || !tt.HasText("Search Discover voices") {
		t.Fatalf("discover: %q", tt.Texts())
	}
	if slices.Contains(tt.Texts(), "Discover voices") {
		t.Error("the gallery's own title shows")
	}
	if err := tt.Click("Mine"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.router.Path() != "/voices/mine" || !tt.HasText("Clone a voice") {
		t.Fatalf("mine: %s %q", a.router.Path(), tt.Texts())
	}
	a.navigate("/voices/discover")
	tt.Frame()
	a.navigate("/studio")
	tt.Frame()
	if err := tt.Click("Voices"); err != nil {
		t.Fatal(err)
	}
	if a.router.Path() != "/voices/discover" {
		t.Errorf("sidebar: %s", a.router.Path())
	}
	if err := tt.Click("New voice"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.router.Path() != "/voices/new" || !tt.HasText("Quick clone") || !tt.HasText("Design a voice") {
		t.Fatalf("new voice: %q", tt.Texts())
	}
	if err := tt.Click("Clone a voice"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.router.Path() != "/voices/new/clone" || a.nav != "/voices/new" {
		t.Errorf("clone: %s %s", a.router.Path(), a.nav)
	}
}
