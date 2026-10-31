package main

import (
	"slices"
	"testing"

	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/actnowkit/sounds"
)

// TestDictationSoundsFallback: without the family's sounds (a source
// build) dictation plays AudioSmith's earlier system sounds.
func TestDictationSoundsFallback(t *testing.T) {
	m := newM2Test(t)
	a, sys := m.a, m.sys
	a.dictCue(dictStartEvent)
	a.dictCue(dictStopEvent)
	a.dictCue(dictErrorEvent)
	if !slices.Equal(sys.Sounds, []string{"Tink", "Pop", "Basso"}) {
		t.Fatalf("sounds %q", sys.Sounds)
	}
}

// TestDictationSounds: the family's sounds by default, a chosen one, a
// system sound, none; off, silence; previews play sounds on or off.
func TestDictationSounds(t *testing.T) {
	m := newM2Test(t)
	a, sys := m.a, m.sys
	rec := useTestSounds(t, a)
	a.dictCue(dictStartEvent)
	a.dictCue(dictStopEvent)
	a.dictCue(dictErrorEvent)
	a.chooseDictSound(dictStartEvent, "pebble")
	a.chooseDictSound(dictStopEvent, sounds.System("Glass"))
	a.chooseDictSound(dictErrorEvent, sounds.None)
	a.dictCue(dictStartEvent)
	a.dictCue(dictStopEvent)
	a.dictCue(dictErrorEvent)
	if got := rec.Played(); !slices.Equal(got, []string{"tally@0.7", "wrap@0.7", "shrug@0.7", "pebble@0.7"}) {
		t.Errorf("played %q", got)
	}
	if !slices.Equal(sys.Sounds, []string{"Glass"}) {
		t.Errorf("system %q", sys.Sounds)
	}
	if d := a.prefs.Dictation; d.StartSound != "pebble" || d.StopSound != "system:Glass" || d.ErrorSound != "none" {
		t.Errorf("settings %+v", d)
	}
	a.chooseDictSound(dictStartEvent, "tally")
	if a.prefs.Dictation.StartSound != "" {
		t.Error("the default is stored")
	}
	rec.Reset()
	a.updateSettings(func(s *settings.Settings) { s.Dictation.Sounds = false; s.Dictation.SoundVolume = 40 })
	a.dictCue(dictStartEvent)
	a.dictPreview(dictStopEvent, "chime")
	if got := rec.Played(); !slices.Equal(got, []string{"chime@0.4"}) {
		t.Errorf("off: %q", got)
	}
}

// TestDictationSoundRows: under Sounds, a line per sound with its picker
// and Try; the panel chooses and plays.
func TestDictationSoundRows(t *testing.T) {
	m := newM2Test(t)
	a, tt := m.a, m.tt
	rec := useTestSounds(t, a)
	tt.SetSize(1000, 2400)
	a.navigate("/settings/dictation")
	tt.Frame()
	for _, s := range []string{"When listening starts", "When listening stops", "When something goes wrong", "Tally", "Wrap", "Shrug"} {
		if !tt.HasText(s) {
			t.Fatalf("no %q: %q", s, tt.Texts())
		}
	}
	if err := tt.Click("Try the error sound"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Start sound"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Pebble"); err != nil {
		t.Fatalf("%v: %q", err, tt.Texts())
	}
	tt.Frame()
	if a.prefs.Dictation.StartSound != "pebble" {
		t.Errorf("start %q", a.prefs.Dictation.StartSound)
	}
	if got := rec.Played(); !slices.Equal(got, []string{"shrug@0.7", "stop shrug", "pebble@0.7"}) {
		t.Errorf("played %q", got)
	}
	a.updateSettings(func(s *settings.Settings) { s.Dictation.Sounds = false })
	a.loadDictSettingsForm()
	tt.Frame()
	if tt.HasText("When listening starts") {
		t.Error("the lines show while sounds are off")
	}
}

// TestDictationSoundPanel: the panel's type menu and volume menu; ▶ in the
// accent while its sound plays, a click then stops it.
func TestDictationSoundPanel(t *testing.T) {
	m := newM2Test(t)
	a, tt := m.a, m.tt
	rec := useTestSounds(t, a)
	tt.SetSize(1000, 2400)
	a.navigate("/settings/dictation")
	tt.Frame()
	if err := tt.Click("Start sound"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Type"); err != nil {
		t.Fatal(err)
	}
	if got := tt.Menu(); !slices.Equal(got, []string{"All", "Suggested", "-", "Tap", "Media", "Notify", "-", "macOS"}) {
		t.Errorf("type menu %q", got)
	}
	if err := tt.ChooseMenuItem("Notify"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Chime") || tt.HasText("Pebble") {
		t.Errorf("Notify: %q", tt.Texts())
	}
	if err := tt.Click("Volume"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Mute"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.prefs.Dictation.SoundVolume != -1 || a.dictSoundPrefs().Volume != 0 {
		t.Errorf("mute: %d", a.prefs.Dictation.SoundVolume)
	}
	if err := tt.Click("Volume"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("75%"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Play Chime"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if ch, _ := a.dictPlayer.Previewing(); ch != "chime" || !tt.HasText("Stop Chime") {
		t.Fatalf("playing %q", ch)
	}
	if err := tt.Click("Stop Chime"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := rec.Played(); !slices.Equal(got, []string{"chime@0.75", "stop chime"}) {
		t.Errorf("played %q", got)
	}
}

// TestDictationSoundTry: Try plays the line's sound and, while it plays
// (the pause glyph), a click stops it.
func TestDictationSoundTry(t *testing.T) {
	m := newM2Test(t)
	a, tt := m.a, m.tt
	rec := useTestSounds(t, a)
	tt.SetSize(1000, 2400)
	a.navigate("/settings/dictation")
	tt.Frame()
	for range 2 {
		if err := tt.Click("Try the error sound"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
	}
	if got := rec.Played(); !slices.Equal(got, []string{"shrug@0.7", "stop shrug"}) {
		t.Errorf("played %q", got)
	}
	if ch, _ := a.dictPlayer.Previewing(); ch != "" {
		t.Errorf("still playing %q", ch)
	}
}
