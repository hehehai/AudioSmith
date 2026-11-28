package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/dictation"
	"github.com/hehehai/AudioSmith/internal/family"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/actnowkit/conformance"
	"github.com/hehehai/actnowkit/models"
	"github.com/hehehai/actnowkit/shortcuts"
)

// testFamily gives a the family of a fresh fixture (AudioSmith's release
// id, a fake .app); started writes its manifest from a's settings.
func testFamily(t *testing.T, a *app, started bool) (*family.Family, *conformance.Fixture) {
	t.Helper()
	fx := conformance.New(t)
	appPath, err := conformance.FakeApp(filepath.Join(testDir(t), "Applications"), "AudioSmith", family.ReleaseBundleID)
	if err != nil {
		t.Fatal(err)
	}
	team := ""
	f := family.New(family.Options{Dir: fx.Family, BundleID: family.ReleaseBundleID, System: conformance.FixtureSystem{},
		AppPath: appPath, TeamID: &team, Logf: t.Logf})
	t.Cleanup(f.Close)
	a.fam = f
	if started {
		none := func(string) []models.Claim { return nil }
		if err := f.Start(context.Background(), familyInputs(a.prefs, "0.4.0-beta.6", testDir(t)), none); err != nil {
			t.Fatal(err)
		}
	}
	return f, fx
}

// auralink registers Auralink's release build with its transcription
// shortcut on keys, turned on at.
func auralink(fx *conformance.Fixture, bundleID, keys string, at time.Time) {
	fx.Register(bundleID, shortcuts.Shortcut{ID: "transcribe", Title: "Transcribe", Keys: keys, Trigger: shortcuts.Press,
		Enabled: true, Mechanism: shortcuts.EventTap, EnabledAt: &at})
}

func TestShortcutConflictPromptOncePerPair(t *testing.T) {
	a, _, tt := newTestApp(t)
	f, fx := testFamily(t, a, true)
	// Auralink took ⌘⇧Space an hour before AudioSmith's manifest: the
	// card shows, once.
	auralink(fx, "dev.hehehai.auralink", "Shift+Command+Space", time.Now().Add(-time.Hour))
	f.Rediscover()
	a.setShortcutConflicts(f.ShortcutConflicts(true))
	if a.famPrompt == nil {
		t.Fatal("no card")
	}
	const key = "dictation|dev.hehehai.auralink|transcribe"
	if !a.settings.Get().Family.ConflictShown[key] {
		t.Errorf("the card is not remembered: %+v", a.settings.Get().Family)
	}
	tt.Frame()
	sentence := dictation.GlyphsOf("CmdOrCtrl+Shift+Space") + " is also the Transcribe shortcut in Auralink. Pressing it will trigger both apps."
	if !tt.HasText(sentence) || !tt.HasText("Open Auralink settings") || !tt.HasText("Change shortcut") {
		t.Errorf("card texts: %q", tt.Texts())
	}
	if err := tt.Click("Ignore"); err != nil {
		t.Fatal(err)
	}
	if a.famPrompt != nil || tt.HasText(sentence) {
		t.Error("Ignore left the card")
	}
	// The next check (a restart, another app's change): no card again,
	// but the settings row warns.
	a.setShortcutConflicts(f.ShortcutConflicts(true))
	if a.famPrompt != nil {
		t.Error("the card showed twice for one pair")
	}
	a.navigate("/settings/dictation")
	tt.Frame()
	if !tt.HasText(sentence) {
		t.Errorf("no warning on the settings row: %q", tt.Texts())
	}
	// Change shortcut goes to the dictation settings.
	a.prefs.Family.ConflictShown = nil
	a.navigate("/studio")
	a.setShortcutConflicts(f.ShortcutConflicts(true))
	tt.Frame()
	if err := tt.Click("Change shortcut"); err != nil {
		t.Fatal(err)
	}
	if a.famPrompt != nil || a.shownPath() != "/settings/dictation" {
		t.Errorf("Change shortcut: card %v, at %s", a.famPrompt != nil, a.shownPath())
	}
}

func TestShortcutConflictEarlierAppOnlyWarns(t *testing.T) {
	a, _, tt := newTestApp(t)
	f, fx := testFamily(t, a, true)
	auralink(fx, "dev.hehehai.auralink", "Shift+Command+Space", time.Now().Add(time.Hour)) // after AudioSmith
	f.Rediscover()
	a.setShortcutConflicts(f.ShortcutConflicts(true))
	if a.famPrompt != nil || len(a.famConflicts) != 1 {
		t.Fatalf("Auralink prompts, AudioSmith only warns: card %v, conflicts %d", a.famPrompt != nil, len(a.famConflicts))
	}
	a.navigate("/settings/dictation")
	tt.Frame()
	if !tt.HasText(dictation.GlyphsOf("CmdOrCtrl+Shift+Space") + " is also the Transcribe shortcut in Auralink. Pressing it will trigger both apps.") {
		t.Errorf("no warning: %q", tt.Texts())
	}
	// A new shortcut: the stale conflict is not shown while the check runs.
	a.prefs.Dictation.Shortcut = "Cmd+Alt+D"
	tt.Frame()
	if hasAnyText(tt.Texts(), "is also the Transcribe shortcut") {
		t.Error("warning for keys no longer used")
	}
}

func TestShortcutConflictsIgnoreDevBuilds(t *testing.T) {
	a, _, tt := newTestApp(t)
	f, fx := testFamily(t, a, true)
	auralink(fx, "dev.hehehai.auralink.dev", "Shift+Command+Space", time.Now().Add(-time.Hour))
	auralink(fx, "dev.hehehai.voxtnew.m7", "Shift+Command+Space", time.Now().Add(-time.Hour)) // a test build
	f.Rediscover()
	a.setShortcutConflicts(f.ShortcutConflicts(true))
	if a.famPrompt != nil || len(a.famConflicts) != 0 {
		t.Errorf("dev/test builds: card %v, conflicts %+v", a.famPrompt != nil, a.famConflicts)
	}
	a.navigate("/settings/dictation")
	a.startShortcutRecording()
	a.dictSet.pending = "Cmd+Shift+Space"
	tt.Frame()
	if hasAnyText(tt.Texts(), "Also used by") || hasAnyText(tt.Texts(), "ActNow") {
		t.Errorf("family text without another release build: %q", tt.Texts())
	}
}

// The recorder names another app's shortcut on the keys heard; saving
// them anyway is allowed and counts as told (no card later).
func TestRecorderAlsoUsedBy(t *testing.T) {
	a, _, tt := newTestApp(t)
	f, fx := testFamily(t, a, true)
	auralink(fx, "dev.hehehai.auralink", "Option+Command+J", time.Now().Add(-time.Hour))
	f.Rediscover()
	a.navigate("/settings/dictation")
	a.startShortcutRecording()
	a.dictSet.pending = "Cmd+Alt+J"
	tt.Frame()
	if !tt.HasText("Also used by Auralink (Transcribe)") {
		t.Fatalf("recorder hint: %q", tt.Texts())
	}
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if got := a.settings.Get().Dictation; got.Shortcut != "CmdOrCtrl+Alt+J" || got.EnabledAt.IsZero() {
		t.Errorf("not saved, or no enabledAt: %+v", got)
	}
	if !a.settings.Get().Family.ConflictShown["dictation|dev.hehehai.auralink|transcribe"] {
		t.Error("the pair the user saw is not remembered")
	}
	// The manifest is rewritten (the family worker in the app; here by
	// hand): the conflict is found, AudioSmith is the later one, and no
	// card shows.
	if err := f.Update(context.Background(), familyInputs(a.settings.Get(), "0.4.0-beta.6", testDir(t)), func(string) []models.Claim { return nil }); err != nil {
		t.Fatal(err)
	}
	cs := f.ShortcutConflicts(true)
	if len(cs) != 1 || !cs[0].Prompt {
		t.Fatalf("conflicts %+v", cs)
	}
	a.setShortcutConflicts(cs)
	if a.famPrompt != nil {
		t.Error("a card for keys the user chose knowing")
	}
}

func TestFamilyInputsShortcutSince(t *testing.T) {
	s := settings.Defaults()
	at := time.Date(2026, 10, 17, 1, 2, 3, 0, time.UTC)
	s.Dictation.EnabledAt = at
	if in := familyInputs(s, "1", ""); !in.ShortcutSince.Equal(at) || !in.ShortcutEnabled || in.Shortcut != settings.DefaultShortcut {
		t.Errorf("inputs %+v", in)
	}
}

// Without the family (tests, the MCP relay) the runtime hooks do nothing.
func TestFamilyRuntimeOff(t *testing.T) {
	a, _, _ := newTestApp(t)
	if a.engineFamily() != nil {
		t.Error("a nil runtime as the engine's family")
	}
	a.familyPasting(context.Background(), true)
	a.quitFamilyRuntime()
	a.familyChanged([]string{"dev.actnow.family.apps"})
}

// The card waits for the first-run setup to close.
func TestShortcutConflictCardAfterOnboarding(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.famPrompt = &shortcuts.Conflict{Other: shortcuts.Holder{Name: "Auralink"}, Theirs: shortcuts.Shortcut{Title: "Transcribe"}, Prompt: true}
	a.openOnboarding()
	tt.Frame()
	if hasAnyText(tt.Texts(), "is also the Transcribe shortcut") {
		t.Error("the card shows over the first-run setup")
	}
	a.obOpen = false
	tt.Frame()
	if !hasAnyText(tt.Texts(), "is also the Transcribe shortcut") {
		t.Error("no card after the setup")
	}
}
