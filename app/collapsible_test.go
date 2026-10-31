package main

import "testing"

// TestCollapsibleSectionsToggle: the inspector's sections (and every other
// kit collapsible fed from a map) open and close on a click. They used to
// copy their state into a local the click was applied to after the frame,
// so nothing happened.
func TestCollapsibleSectionsToggle(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.navigate("/stories")
	p := a.lfStories
	p.opened = true // a new story's editor
	tt.Frame()
	if p.open["scene"] {
		t.Fatal("Performance starts open")
	}
	if err := tt.Click("Performance"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !p.open["scene"] {
		t.Fatal("a click did not open Performance")
	}
	if err := tt.Click("Performance"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if p.open["scene"] {
		t.Fatal("a second click did not close Performance")
	}
	// Advanced, remembered in the settings.
	if err := tt.Click("Advanced"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.advancedOpen("story") || a.advancedOpen("audiobook") {
		t.Fatal("a click did not open the story's Advanced")
	}
	if !p.open["setup"] {
		t.Fatal("Setup starts open")
	}
	if err := tt.Click("Setup"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if p.open["setup"] {
		t.Fatal("a click did not close Setup")
	}
}
