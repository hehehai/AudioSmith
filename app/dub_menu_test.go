package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestDubEditMenuUndoRedo: the Edit menu's Undo and Redo reach the Dub
// editor. On macOS the menu takes ⌘Z and ⇧⌘Z before the window sees them
// and sends its commands to the focused element, so the editor's window
// shortcuts never ran (⌘Z) or stopped running once MyGo's menus took
// ⇧⌘Z as AppKit does (ActNow's MyGo). The editor has the focus when it appears, keeps it through the
// toolbar's and a line's buttons, gets it back when a line's text is
// left, and its timeline takes the commands too; a line's text being
// edited undoes its own typing, the editor's history untouched.
func TestDubEditMenuUndoRedo(t *testing.T) {
	d := newDubTest(t, 6)
	a, tt := d.a, d.tt
	tt.Frame()
	a.dubSourceFile(d.src)
	tt.Frame()
	a.dubStart() // review mode: stops after the transcript
	tt.Frame()
	tt.Frame()
	if len(a.dub.Segments) != 6 {
		t.Fatalf("after transcribing: %d segments", len(a.dub.Segments))
	}
	if !tt.Focused("Dub editor") {
		t.Fatal("the editor has not the keyboard when it appears")
	}
	count := func() int { return len(a.dub.Segments) }
	a.dubMerge(a.dub.Segments[1].ID, true)
	tt.Frame()
	n := count()
	if n != 5 {
		t.Fatalf("merge: %d segments", n)
	}
	step := func(cmd string, want int, when string) {
		t.Helper()
		tt.Command(cmd)
		tt.Frame()
		if got := count(); got != want {
			t.Fatalf("%s %s: %d segments, want %d", cmd, when, got, want)
		}
	}
	step("undo", n+1, "with the editor's focus")
	step("redo", n, "with the editor's focus")

	// The toolbar's buttons and a line's leave the focus to the editor.
	d.click(t, "All")
	step("undo", n+1, "after the toolbar's select all")
	d.click(t, "Select line 1")
	step("redo", n, "after a line's check box")

	// A line's text being edited undoes its own typing.
	first := a.dub.Segments[0].ID
	a.dubBeginEdit(first, "")
	tt.Frame()
	tt.Frame()
	if !tt.Focused("Line text") {
		t.Fatal("the line's text has not the keyboard")
	}
	before := a.dubBuf
	tt.Type(" again")
	tt.Frame()
	if a.dubBuf == before {
		t.Fatalf("typing changed nothing: %q", a.dubBuf)
	}
	tt.Command("undo")
	tt.Frame()
	if a.dubBuf != before || count() != n {
		t.Fatalf("undo while typing: text %q (want %q), %d segments (want %d: the editor's history untouched)", a.dubBuf, before, count(), n)
	}
	// Escape leaves the text; the keyboard is the editor's again.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	tt.Frame()
	if a.dubEdit != "" || !tt.Focused("Dub editor") {
		t.Fatalf("after Escape: editing %q, the editor focused %v", a.dubEdit, tt.Focused("Dub editor"))
	}
	step("undo", n+1, "after leaving a line's text")

	// The timeline takes them when it has the focus.
	d.click(t, "Timeline")
	if !tt.Focused("Timeline") {
		t.Fatal("the timeline has not the keyboard after a click")
	}
	step("redo", n, "with the timeline's focus")
	step("undo", n+1, "with the timeline's focus")
}
