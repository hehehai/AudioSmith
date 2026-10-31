package main

import (
	"slices"
	"testing"
	"time"
)

// The sidebar's places, tools and foot, the ⌘ numbers and the palette
// agree (0.5 F1): Models, Engine, Logs and Integrations are not in the
// sidebar, More's pages are a menu (TestMoreMenu).
func TestSidebarGroups(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.Frame()
	for _, title := range []string{"Tools", "Home", "Voices", "Projects", "Assets",
		"Text to speech", "New voice", "Stories", "Audiobook", "Dub", "Speech to text", "Voice isolator", "Dictation", "More",
		"Developers", "Settings", "Tasks and notifications", "Search everything…"} {
		if !tt.HasText(title) {
			t.Errorf("window lacks %q", title)
		}
	}
	for _, title := range []string{"Create", "Automate", "Integrations", "Models", "Engine", "Logs", "Engine ready", "Search the library"} {
		if tt.HasText(title) {
			t.Errorf("sidebar has %q", title)
		}
	}
	if navGroups[0].id != "" || len(navGroups[0].pages) != 4 {
		t.Errorf("places %+v", navGroups[0])
	}
	if len(shortcutPages) != 8 {
		t.Fatalf("%d shortcut pages", len(shortcutPages))
	}
	cmds := a.commands()
	for _, p := range pages {
		i := slices.IndexFunc(cmds, func(c command) bool { return c.id == "go"+p.path })
		if i < 0 {
			t.Errorf("no palette entry for %s", p.path)
			continue
		}
		if cmds[i].keys != pageKeys(p.path) {
			t.Errorf("%s: keys %q", p.path, cmds[i].keys)
		}
		n := 0
		for _, c := range cmds {
			if c.id == "go"+p.path {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s: %d palette entries", p.path, n)
		}
	}
	if pageKeys("/assets") != "" || pageKeys("/transcripts") != "⌘6" {
		t.Errorf("keys: assets %q transcripts %q", pageKeys("/assets"), pageKeys("/transcripts"))
	}
	// The sheet lists the new numbers.
	a.sheetOpen = true
	tt.Frame()
	if !tt.HasText("Home, Text to speech, Voices, Projects") || !tt.HasText("Dub, Speech to text, Stories, Audiobook") {
		t.Errorf("sheet: %q", tt.Texts())
	}
}

// The icon rail replaces the sidebar, and comes back; a closed section
// is remembered.
func TestSidebarRail(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.Frame()
	if err := tt.Click("Show icons only"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.prefs.Appearance.Rail || !a.settings.Get().Appearance.Rail {
		t.Fatal("rail not saved")
	}
	if tt.HasText("Tools") {
		t.Error("the rail shows section titles")
	}
	if err := tt.Click("Projects"); err != nil {
		t.Fatal(err)
	}
	if a.currentPage() != "projects" {
		t.Errorf("rail click: %s", a.router.Path())
	}
	if err := tt.Click("Show the sidebar"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.prefs.Appearance.Rail || !tt.HasText("Tools") {
		t.Fatal("the sidebar did not come back")
	}
	// Close a section: its pages go, and the settings remember it.
	*a.sectionOpen("Tools") = false
	tt.Frame()
	tt.Frame()
	if tt.HasText("Voice isolator") {
		t.Error("closed section still shows its pages")
	}
	if !slices.Contains(a.settings.Get().Appearance.ClosedSections, "Tools") {
		t.Errorf("closed sections %v", a.settings.Get().Appearance.ClosedSections)
	}
	// The resized width is saved.
	a.sideW = 260 * a.sideWScale
	a.sideWAt = time.Now().Add(-time.Second) // a drag that rested
	tt.Frame()
	if w := a.settings.Get().Appearance.SidebarWidth; w != 260 {
		t.Errorf("width %v", w)
	}
}
