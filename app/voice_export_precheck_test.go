package main

import (
	"context"
	"slices"
	"testing"

	"github.com/hehehai/AudioSmith/internal/core"
)

// TestVoiceExportNoPreviewFirst (code review 1103 L3): exporting a
// designed voice without a sample says so before any save dialog, with
// 「Open voice」 to its page (where a take is locked).
func TestVoiceExportNoPreviewFirst(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.SetSize(1200, 900)
	v, err := a.lib.svc.Voices.Save(context.Background(), core.Voice{Name: "Wren", Kind: core.VoiceDesigned, Design: "a calm narrator"})
	if err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.findVoice(v.ID) == nil {
		t.Fatal("voice not listed")
	}
	a.exportVoice(v.ID, false, "")
	tt.Frame()
	if !tt.HasText("This voice has no sample to share as its preview") {
		t.Fatalf("no notice: %q", tt.Texts())
	}
	// The toast's one button: its text and its label.
	if n := len(slices.DeleteFunc(tt.Texts(), func(s string) bool { return s != "Open voice" })); n != 2 {
		t.Fatalf("not exactly one Open voice: %q", tt.Texts())
	}
	if err := tt.Click("Open voice"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.router.Path() != "/voices/mine" || a.edit == nil || a.edit.id != v.ID {
		t.Errorf("Open voice: %s %+v", a.router.Path(), a.edit)
	}
}
