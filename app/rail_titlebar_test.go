package main

import (
	"image/png"
	"os"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestRailClearsTheTrafficLights: with the icon rail, the rail's button and
// the page's title keep clear of the macOS window controls, which reach
// past the rail.
func TestRailClearsTheTrafficLights(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.prefs.Appearance.Rail = true
	tt.SetTitleBar(ui.TitleBar{Height: 52, Left: 78})
	a.navigate("/integrations/claude-code")
	tt.Frame()
	tt.Frame()
	if out := os.Getenv("AUDIOSMITH_RAIL_SHOT"); out != "" {
		f, _ := os.Create(out)
		png.Encode(f, tt.Image())
		f.Close()
	}
	title, ok := tt.Find("Claude Code")
	if !ok {
		t.Fatalf("no title: %q", tt.Texts())
	}
	if title.X < 78 {
		t.Errorf("the title starts at x=%.0f, under the traffic lights (to 78)", title.X)
	}
	b, ok := tt.Find("Show the sidebar")
	if ok && b.Y < 52 && b.X < 78 {
		t.Errorf("the rail's button at %v is under the traffic lights", b)
	}
}

// TestHeaderClearsWindowsCaptionButtons: when the platform title bar
// places caption buttons on the right (Windows Minimize/Maximize/Close,
// ~138 DIPs), right-aligned controls in the 52px page header stay left
// of the caption button region while macOS (Right == 0) stays flush to
// the normal right gutter.
func TestHeaderClearsWindowsCaptionButtons(t *testing.T) {
	const winW = 1100
	const captionW = 138
	a, _, tt := newTestApp(t)
	tt.SetSize(winW, 720)
	a.navigate("/projects")

	var headR ui.Rect
	gutterProbe = func(kind string, r ui.Rect) {
		if kind == "head-r" {
			headR = r
		}
	}
	t.Cleanup(func() { gutterProbe = nil })

	// macOS: Right == 0 -> tools sit at the normal 16px right gutter.
	tt.SetTitleBar(ui.TitleBar{Height: 52, Left: 78})
	tt.Frame()
	tt.Frame()
	macRight := headR.X + headR.W
	if macRight < winW-32 {
		t.Fatalf("macOS header tools ended unexpectedly far left: %.1f (winW=%d)", macRight, winW)
	}

	// Windows: Right == 138 -> tools stay clear of [winW-138, winW].
	tt.SetTitleBar(ui.TitleBar{Height: 52, Right: captionW})
	tt.Frame()
	tt.Frame()
	winRight := headR.X + headR.W
	if winRight > winW-captionW {
		t.Errorf("Windows header tools end at x=%.1f, colliding with caption buttons starting at x=%d", winRight, winW-captionW)
	}
	if macRight-winRight < captionW-2 {
		t.Errorf("expected Windows header tools to shift left by ~%d DIPs (mac=%.1f win=%.1f)", captionW, macRight, winRight)
	}
}
