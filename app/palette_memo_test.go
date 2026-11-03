package main

import (
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
)

// TestPoCPaletteMemoStaleRename (code review 1103 L1): ⌘K's memo kept a
// renamed transcript's old title while the query was the same (an
// in-place update keeps the slice's first pointer and length).
func TestPoCPaletteMemoStaleRename(t *testing.T) {
	a, _, tt := newTestApp(t)
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.Local)
	tr := core.Transcript{ID: "tr1", Title: "meeting alpha", Source: core.SourceFile, CreatedAt: at, UpdatedAt: at,
		Segments: []core.TranscriptSegment{{Text: "hello"}}}
	a.trList = []core.Transcript{tr}
	a.openPaletteWith("meeting")
	tt.Frame()
	if !tt.HasText("meeting alpha") {
		t.Fatalf("before: %q", tt.Texts())
	}
	a.paletteOpen = false
	tt.Frame()
	tr.Title, tr.UpdatedAt = "meeting beta", at.Add(time.Minute)
	a.transcriptChanged(core.TranscriptChanged{Transcript: tr})
	a.openPaletteWith("meeting")
	tt.Frame()
	if tt.HasText("meeting alpha") || !tt.HasText("meeting beta") {
		t.Errorf("STALE: after the rename ⌘K shows %q", tt.Texts())
	}
}

// TestPaletteMemoInPlaceWhileOpen: a take's script changed in place while
// ⌘K is open (an agent's edit) shows at once; frames without changes
// keep the memo.
func TestPaletteMemoInPlaceWhileOpen(t *testing.T) {
	a, _, tt := newTestApp(t)
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.Local)
	tk := core.Take{ID: "t1", Text: "zebra crossing one", Model: "m", File: "t1.wav", CreatedAt: at}
	a.takes = []core.Take{tk}
	a.openPaletteWith("zebra")
	tt.Frame()
	if !tt.HasText("zebra crossing one") {
		t.Fatalf("before: %q", tt.Texts())
	}
	kept := a.palMemo.takes
	tt.Frame()
	if len(kept) == 0 || &a.palMemo.takes[0] != &kept[0] {
		t.Error("the memo was not kept across a frame")
	}
	tk.Text = "zebra crossing two"
	a.putTake(tk)
	tt.Frame()
	if tt.HasText("zebra crossing one") || !tt.HasText("zebra crossing two") {
		t.Errorf("after the edit ⌘K shows %q", tt.Texts())
	}
}
