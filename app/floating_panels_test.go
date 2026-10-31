package main

// The floating panels (#126, ui_panels.go): over the page, a gap from the
// window's bottom, sliding in and out.

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// mineWithVoices is My voices with two saved voices.
func mineWithVoices(t *testing.T) (*app, *ui.Tester, core.Voice, core.Voice) {
	t.Helper()
	a, _, tt := newTestApp(t)
	a.testMotion = true
	v1 := addVoice(t, a, core.Voice{Name: "Narrator", Kind: core.VoiceCloned, RefText: "Once upon a time.", Language: "en"}, 6)
	v2 := addVoice(t, a, core.Voice{Name: "Grandma", Kind: core.VoiceDesigned, Seed: 7}, 0)
	a.navigate("/voices/mine")
	tt.Frame()
	tt.Frame()
	return a, tt, v1, v2
}

// Opening a voice's details leaves the table alone; the panel runs from
// the window's top to a gap from its bottom, flush with its right edge,
// and slides in: off to the right at first, in place after the motion.
// The header's tools stand left of it all along.
func TestFloatingPanelOverPage(t *testing.T) {
	a, tt, v1, v2 := mineWithVoices(t)
	before, ok := tt.Find("Use") // a row's last button
	if !ok {
		t.Fatalf("no table: %q", tt.Texts())
	}
	a.openVoiceEditor(v1.ID)
	tt.Frame()
	tt.Advance(60 * time.Millisecond)
	mid, ok := tt.Find("Voice details")
	if !ok {
		t.Fatal("no panel while it slides in")
	}
	if s, ok := findHeaderTool(tt, "Search voices"); !ok || s.X+s.W > mid.X {
		t.Errorf("the header's search under the sliding panel: %v %+v %+v", ok, s, mid)
	}
	tt.Advance(400 * time.Millisecond)
	r, _ := tt.Find("Voice details")
	if !(mid.X > r.X) {
		t.Errorf("no slide: %+v then %+v", mid, r)
	}
	after, _ := tt.Find("Use")
	if after != before {
		t.Errorf("the table moved: %+v → %+v", before, after)
	}
	th := ui.LightTheme()
	gap := px(th, sp3) - 0.5
	if r.Y > 0.5 || 720-(r.Y+r.H) < gap || abs32(1100-(r.X+r.W)) > 0.5 {
		t.Errorf("panel %+v: not from the top, flush right, a gap from the bottom", r)
	}
	for _, l := range []string{"Search voices", "More tools"} {
		if s, ok := findHeaderTool(tt, l); !ok || s.X+s.W > r.X {
			t.Errorf("%s under the panel: %v %+v", l, ok, s)
		}
	}
	// Another voice: the content changes, the panel stays put.
	a.openVoiceEditor(v2.ID)
	tt.Frame()
	if r2, _ := tt.Find("Voice details"); r2 != r {
		t.Errorf("another voice slid the panel: %+v → %+v", r, r2)
	}
	// The page under it stays usable: a click on a row's name opens it.
	if err := tt.Click("Narrator"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.edit == nil || a.edit.id != v1.ID {
		t.Errorf("click under the panel: %+v", a.edit)
	}
}

// Its ✕ and Escape send it out: still there while it slides, then gone,
// and the editor closed.
func TestFloatingPanelSlidesOut(t *testing.T) {
	a, tt, v1, _ := mineWithVoices(t)
	a.openVoiceEditor(v1.ID)
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	if err := tt.Click("Close"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(60 * time.Millisecond)
	if !tt.HasText("Voice details") || a.edit == nil {
		t.Fatal("gone before its motion")
	}
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	if tt.HasText("Voice details") || a.edit != nil {
		t.Fatalf("still open after its motion: %v", a.edit != nil)
	}
	a.openVoiceEditor(v1.ID)
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Key(0, ui.KeyEscape)
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	if a.edit != nil || tt.HasText("Voice details") {
		t.Error("Escape keeps it")
	}
}

// With less motion asked for, it shows and goes at once.
func TestFloatingPanelReduceMotion(t *testing.T) {
	a, tt, v1, _ := mineWithVoices(t)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	a.openVoiceEditor(v1.ID)
	tt.Frame()
	r, ok := tt.Find("Voice details")
	if !ok || r.X+r.W > 1100 {
		t.Fatalf("not in place at once: %v %+v", ok, r)
	}
	a.dismissPanel("voice-editor")()
	tt.Frame()
	tt.Frame()
	if a.edit != nil {
		t.Error("not gone at once")
	}
}

// At 820×520 @125 % the panels fit in the window: Mine's, Discover's,
// the Studio's (over the script). There the header could not stand
// beside them: they keep under it.
func TestFloatingPanelNarrow(t *testing.T) {
	a, tt, v1, _ := mineWithVoices(t)
	tt.SetSize(820, 520)
	a.updateSettings(func(s *settings.Settings) { s.Appearance.Scale = 1.25 })
	tt.Frame()
	fits := func(label string) {
		t.Helper()
		tt.Frame()
		tt.Advance(400 * time.Millisecond)
		r, ok := tt.Find(label)
		if !ok || r.X < 40 || r.X+r.W > 820 || r.Y+r.H > 520-12 || r.Y < pageTop {
			t.Errorf("%s: %v %+v", label, ok, r)
		}
	}
	a.openVoiceEditor(v1.ID)
	fits("Voice details")
	a.navigate("/voices/discover")
	a.openArchetype("c-serena")
	fits("Voice details")
	a.navigate("/studio")
	a.showStudioPanel(studioTabSettings)
	fits("Text to speech panel")
}

// A tool panel in its lane: the page keeps room for it while it shows,
// and gives it back as it slides out.
func TestFloatingPanelLane(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.Frame()
	tt.Frame()
	panel, ok := tt.Find("Text to speech panel")
	if !ok {
		t.Fatal("no Studio panel at 1100")
	}
	bar, _ := tt.Find("Generate")
	if bar.X+bar.W > panel.X {
		t.Errorf("the panel covers the script's bar: %+v %+v", bar, panel)
	}
	if err := tt.Click("Hide settings and history"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	if tt.HasText("Text to speech panel") {
		t.Error("hidden panel shows")
	}
	if bar2, _ := tt.Find("Generate"); bar2.X <= bar.X {
		t.Errorf("the script did not take the room: %+v → %+v", bar, bar2)
	}
	_ = a
}
