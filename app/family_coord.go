package main

// The ActNow family, phase 1 P5 (family spec v1.0.4 §10, §11.1):
//   - the dictation shortcut against the other family apps' shortcuts:
//     "Also used by Auralink (Transcribe)" under the recorder, a warning
//     on the settings row, and — when AudioSmith is the later of the two
//     to take the keys — a notification card once per pair, with Change
//     shortcut / Open Auralink settings / Ignore;
//   - the engine's memory budget shared with the family (internal/family
//     Runtime, the supervisor's engine.Family): runtime/<bundle id>.json
//     written as models load and unload, removed at quit; the other apps'
//     requests for memory unload idle models;
//   - "pasting" while dictation pastes.
//
// Only other apps' release builds are named (spec §7.10): without one,
// nothing of this shows.

import (
	"context"
	"path/filepath"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/dictation"
	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/family"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/settings"
	actnow "github.com/hehehai/actnowkit/family"
	"github.com/hehehai/actnowkit/shortcuts"
)

// familyCoord is the app's P5 state.
type familyCoord struct {
	famRT *family.Runtime // nil without the family (tests)
	// famConflicts are the dictation shortcut's conflicts with other
	// apps' release builds, as the last check found them (UI thread).
	famConflicts []shortcuts.Conflict
	// famPrompt is the conflict the card shows; nil = none.
	famPrompt *shortcuts.Conflict
	// famCheck asks the checker for the conflicts again.
	famCheck chan struct{}
}

// connectFamilyRuntime makes the runtime handle the engine supervisor
// shares the memory budget through (connectEngine passes it on).
func (a *app) connectFamilyRuntime() {
	if a.fam == nil {
		return
	}
	a.famCheck = make(chan struct{}, 1)
	lib := a.lib
	a.famRT = a.fam.Runtime(family.RuntimeOptions{Revision: func(repo string) string {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if dir, err := lib.modelSnapshot(ctx, repo); err == nil {
			return filepath.Base(dir) // …/snapshots/<commit>
		}
		return ""
	}, Loaded: func(repos []string) { go a.claimInUse(repos) }}) // family_reuse.go
}

// engineFamily is the supervisor's engine.Family: nil without the family
// (a nil *Runtime would not be a nil interface).
func (a *app) engineFamily() engine.Family {
	if a.famRT == nil {
		return nil
	}
	return a.famRT
}

// familyStarted runs once family.Start returned (on its goroutine): what
// the engine published meanwhile is written, and the shortcuts are
// checked now and on every request (recheckShortcuts) from then on.
func (a *app) familyStarted() {
	if a.famRT != nil {
		a.famRT.Flush()
	}
	check, fam := a.famCheck, a.fam
	if check == nil {
		return
	}
	go func() {
		for range check {
			cs := fam.ShortcutConflicts(true)
			a.post(func() { a.setShortcutConflicts(cs) })
		}
	}()
	a.recheckShortcuts()
}

// familyChanged takes the family's notifications (Family.OnChange, on
// its goroutine): the runtime's, and a changed manifest (another app
// installed, or changed its shortcuts; AudioSmith's own rewrite after a
// new shortcut too) checks the shortcuts again.
func (a *app) familyChanged(topics []string) {
	if a.famRT != nil {
		a.famRT.Handle(topics)
	}
	if slices.Contains(topics, actnow.TopicApps) {
		a.recheckShortcuts()
	}
}

func (a *app) recheckShortcuts() {
	if a.famCheck == nil {
		return
	}
	select {
	case a.famCheck <- struct{}{}:
	default:
	}
}

// quitFamilyRuntime removes the runtime file (at quit, before the
// engines stop: their last updates would otherwise write it again).
func (a *app) quitFamilyRuntime() {
	if a.famRT != nil {
		a.famRT.Close()
	}
}

// familyPasting is dictation.Inserter.Pasting: another app's paste is
// waited for, then AudioSmith's own is announced.
func (a *app) familyPasting(ctx context.Context, on bool) {
	rt := a.famRT
	if rt == nil {
		return
	}
	if on {
		rt.WaitPasting(ctx)
	}
	rt.SetPasting(on)
}

// setShortcutConflicts shows what the check found: the warnings, and the
// card for the first conflict AudioSmith prompts for (it is the later
// one) that was never shown; it is then remembered as shown.
func (a *app) setShortcutConflicts(cs []shortcuts.Conflict) {
	a.famConflicts = cs
	if p := a.famPrompt; p != nil && !slices.ContainsFunc(cs, func(c shortcuts.Conflict) bool { return c.Key() == p.Key() }) {
		a.famPrompt = nil // resolved meanwhile
	}
	if a.famPrompt != nil {
		return
	}
	for _, c := range cs {
		if c.Prompt && !a.prefs.Family.ConflictShown[c.Key()] {
			a.famPrompt = &c
			a.markConflictsShown(c.Key())
			return
		}
	}
}

// markConflictsShown remembers conflict pairs the user was told about.
func (a *app) markConflictsShown(keys ...string) {
	var add []string
	for _, k := range keys {
		if !a.prefs.Family.ConflictShown[k] {
			add = append(add, k)
		}
	}
	if len(add) == 0 {
		return
	}
	a.updateSettings(func(s *settings.Settings) {
		m := make(map[string]bool, len(s.Family.ConflictShown)+len(add))
		for k, v := range s.Family.ConflictShown {
			m[k] = v
		}
		for _, k := range add {
			m[k] = true
		}
		s.Family.ConflictShown = m
	})
}

// shortcutUsedBy lists the other apps' release builds using keys (an
// accelerator), for the recorder.
func (a *app) shortcutUsedBy(keys string) []shortcuts.Hit {
	if a.fam == nil {
		return nil
	}
	return a.fam.UsedBy(keys)
}

// noteUsedBy: saving keys another app uses was the user's choice, made
// with "Also used by…" in sight: no card for these pairs later.
func (a *app) noteUsedBy(hits []shortcuts.Hit) {
	var keys []string
	for _, h := range hits {
		keys = append(keys, shortcuts.Conflict{Mine: shortcuts.Shortcut{ID: "dictation"}, Other: h.App, Theirs: h.Shortcut}.Key())
	}
	a.markConflictsShown(keys...)
}

// usedByText is "Also used by Auralink (Transcribe)" for the hits.
func usedByText(hits []shortcuts.Hit) string {
	if len(hits) == 0 {
		return ""
	}
	return i18n.T("Also used by %s (%s)", hits[0].App.Name, hits[0].Shortcut.Title)
}

// liveConflicts are the conflicts of the shortcut in the settings now
// (a check may still be on its way after a change).
func (a *app) liveConflicts() []shortcuts.Conflict {
	d := a.prefs.Dictation
	keys, err := shortcuts.Normalize(d.Shortcut)
	if err != nil || !d.Enabled {
		return nil
	}
	var out []shortcuts.Conflict
	for _, c := range a.famConflicts {
		if c.Mine.Keys == keys {
			out = append(out, c)
		}
	}
	return out
}

// conflictSentence is "⇧⌘Space is also the Transcribe shortcut in
// Auralink. Pressing it will trigger both apps."
func (a *app) conflictSentence(c shortcuts.Conflict) string {
	return i18n.T("%s is also the %s shortcut in %s. Pressing it will trigger both apps.",
		dictation.GlyphsOf(a.prefs.Dictation.Shortcut), c.Theirs.Title, c.Other.Name)
}

// familyShortcutWarnings are the settings row's warnings.
func (a *app) familyShortcutWarnings(c *ui.Context) {
	t := c.Theme()
	for i, cf := range a.liveConflicts() {
		ui.Text(c.Key("dict-family-conflict-"+itoa(i)), a.conflictSentence(cf)).FontSize(px(t, 12)).TextColor(t.Warning)
	}
}

// shortcutConflictCard is the notification card for famPrompt, at the
// window's lower right, over the page but not modal.
func (a *app) shortcutConflictCard(c *ui.Context) {
	p := a.famPrompt
	if p == nil || a.obOpen {
		return // after the first-run setup, not over it
	}
	t := c.Theme()
	k := tok(t)
	ui.Overlay(c, func() {
		w, _ := c.Size()
		card := ui.Column(c.Key("family-conflict-card")).Absolute().Right(px(t, sp4)).Bottom(px(t, sp4)).
			Width(min(px(t, 440), w-px(t, sp8))).Padding(px(t, sp4)).Gap(px(t, sp3)).Radius(px(t, radiusCard)).
			Background(k.Bg).Border(1, k.Line).Shadow(0, 8, 28, 0, ui.RGBA(0, 0, 0, 0.18))
		card.Children(func() {
			ui.Text(c, a.conflictSentence(*p)).FontSize(px(t, fsBody)).TextColor(k.Text)
			ui.Row(c).Gap(px(t, sp2)).Wrap().Justify(ui.End).Children(func() {
				if ghostButton(c.Key("family-conflict-ignore"), i18n.T("Ignore")).Clicked() {
					a.famPrompt = nil
				}
				if link := family.ShortcutLink(p.Other); link != "" {
					if secondaryButton(c.Key("family-conflict-open"), i18n.T("Open %s settings", p.Other.Name)).Clicked() {
						a.famPrompt = nil
						c.Services().OpenURL(link)
					}
				}
				if secondaryButton(c.Key("family-conflict-change"), i18n.T("Change shortcut")).Clicked() {
					a.famPrompt = nil
					a.navigate("/settings/dictation")
				}
			})
		})
	})
}
