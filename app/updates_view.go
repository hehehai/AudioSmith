package main

// The updater's views (updates.go): the sidebar's "update available" line
// (a button on the rail), the update panel, Settings → Diagnostics and
// about → Software updates, and the changelog's history.

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	akup "github.com/hehehai/actnowkit/updates"

	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// updateLine is the sidebar's line while there is something to do: an
// update to install, its download, the restart to finish it ("" none).
func (a *app) updateLine() string {
	u := a.updates
	if u.mgr == nil || u.st.Release == nil {
		return ""
	}
	switch u.st.Phase {
	case akup.Available:
		return i18n.T("Update available")
	case akup.Downloading:
		return i18n.T("Updating… %s", updatePercent(u.st.Fraction()))
	case akup.Ready:
		return i18n.T("Restart to update")
	}
	return ""
}

// updateEntry is the update line at the sidebar's foot, above Settings: a
// click opens the panel.
func (a *app) updateEntry(c *ui.Context) {
	text := a.updateLine()
	if text == "" {
		return
	}
	t := c.Theme()
	k := tok(t)
	tip := i18n.T("AudioSmith %s is available", a.updates.st.Release.Version)
	b := ghostBase(c.Key("nav-update")).Padding(px(t, 6), px(t, sp2)).Gap(px(t, sp2)).Justify(ui.Start).Label(text).Tooltip(tip)
	b.Children(func() {
		ui.Icon(c, icons.DownloadMinimalistic.Linear).Size(px(t, 16), px(t, 16)).TextColor(k.AccentInk)
		ui.Text(c, text).TextColor(k.AccentInk).FontWeight(500).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		ui.Box(c).Size(px(t, 7), px(t, 7)).Radius(px(t, 4)).Background(k.Accent).Shrink(0)
	})
	if b.Clicked() {
		a.updates.panelOpen = true
	}
}

// updateRailButton is the update line on the rail: the download icon with
// a dot.
func (a *app) updateRailButton(c *ui.Context) {
	text := a.updateLine()
	if text == "" {
		return
	}
	a.railButton(c, "rail-update", icons.DownloadMinimalistic, text, text, false, "•", func() { a.updates.panelOpen = true })
}

// updatePercent is a fraction as "42%".
func updatePercent(f float64) string { return fmt.Sprintf("%d%%", int(f*100+0.5)) }

// updatePanel is the update panel (a dialog): the version, its notes, and
// what can be done in the phase it is in.
func (a *app) updatePanel(c *ui.Context) {
	u := a.updates
	if !u.panelOpen {
		return
	}
	t := c.Theme()
	st := u.st
	title, sub := i18n.T("Software update"), ""
	if st.Release != nil {
		title = i18n.T("AudioSmith %s is available", st.Release.Version)
		sub = i18n.T("You have %s", orDash(a.version))
		if !st.Release.Date.IsZero() {
			sub += " · " + i18n.Date(st.Release.Date)
		}
		switch st.Phase {
		case akup.Downloading:
			title = i18n.T("Downloading AudioSmith %s", st.Release.Version)
		case akup.Ready:
			title = i18n.T("AudioSmith %s is installed", st.Release.Version)
		}
	}
	open := true
	dialogFrame(c, "upd-panel", &open, title, sub, px(t, 560), func() { a.updatePanelBody(c) }, func() { a.updatePanelFooter(c) })
	if !open {
		u.panelOpen = false
		if u.mgr != nil && (st.Phase == akup.UpToDate || st.Phase == akup.Failed) {
			u.mgr.Dismiss()
		}
	}
}

func (a *app) updatePanelBody(c *ui.Context) {
	u := a.updates
	t := c.Theme()
	k := tok(t)
	st := a.updates.st
	if a.updates.mgr == nil {
		st.Phase = akup.Disabled
	}
	switch st.Phase {
	case akup.Checking, akup.Idle:
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c)
			ui.Text(c, i18n.T("Checking for updates…")).TextColor(k.Text2)
		})
		return
	case akup.UpToDate:
		ui.Text(c.Key("upd-uptodate"), i18n.T("AudioSmith is up to date (%s)", orDash(a.version))).TextColor(k.Text2)
		return
	case akup.Disabled:
		ui.Text(c, i18n.T("Development builds and copies built on this Mac don't update themselves; copies from actnow.dev do.")).TextColor(k.Text2)
		ui.Link(c, i18n.T("Download the latest version from actnow.dev"), downloadURL).FontSize(px(t, fsCaption))
		return
	case akup.Failed:
		msg := i18n.T("The update did not install: %s. AudioSmith is unchanged.", updateErrText(st.Err))
		if st.Step == "check" {
			msg = i18n.T("Cannot check for updates: %s", updateErrText(st.Err))
		}
		notice(c, "upd-failed", noticeError, msg, "", nil, nil)
		if st.Release == nil {
			return
		}
	}
	switch st.Phase {
	case akup.Downloading:
		ui.Column(c.Key("upd-progress")).Gap(px(t, sp1)).Children(func() {
			ui.Progress(c.Key("upd-bar"), st.Fraction()).Label(i18n.T("Downloading the update"))
			line := i18n.T("Downloading… %s", updatePercent(st.Fraction()))
			if st.Total > 0 {
				line += " · " + humanBytes(st.Downloaded) + " / " + humanBytes(st.Total)
			}
			caption(c, line)
		})
	case akup.Ready:
		ui.Text(c.Key("upd-ready"), i18n.T("The new version runs once AudioSmith restarts.")).TextColor(k.Text2)
	}
	if st.Release == nil {
		return
	}
	if i18n.Locale() == "zh-Hans" && st.NotesLocale == "en" {
		caption(c.Key("upd-en"), i18n.T("These notes are not translated yet; here they are in English."))
	}
	notes := st.Notes
	if notes == "" {
		notes = i18n.T("No notes for this version.")
	}
	ui.Column(c.Key("upd-notes")).Margin(px(t, sp1), 0, 0).Children(func() {
		markdown.View(c.Key("upd-md"), notes, &u.md)
	})
}

func (a *app) updatePanelFooter(c *ui.Context) {
	u := a.updates
	st := u.st
	if u.mgr == nil {
		st.Phase = akup.Disabled
	}
	ui.Spacer(c)
	switch st.Phase {
	case akup.Available:
		if ghostButton(c.Key("upd-later"), i18n.T("Later")).Clicked() {
			u.panelOpen = false
		}
		if primaryButton(c.Key("upd-install"), i18n.T("Download and Install")).Clicked() {
			a.installUpdate()
		}
	case akup.Downloading:
		if ghostButton(c.Key("upd-hide"), i18n.T("Hide")).Clicked() {
			u.panelOpen = false
		}
	case akup.Ready:
		if ghostButton(c.Key("upd-later"), i18n.T("Later")).Clicked() {
			u.panelOpen = false
		}
		if primaryButton(c.Key("upd-restart"), i18n.T("Restart to Finish the Update")).Clicked() {
			a.restartForUpdate()
		}
	case akup.Failed:
		if ghostButton(c.Key("upd-close"), i18n.T("Close")).Clicked() {
			u.panelOpen = false
			u.mgr.Dismiss()
		}
		if primaryButton(c.Key("upd-retry"), i18n.T("Try again")).Clicked() {
			u.mgr.Dismiss()
			a.checkForUpdates()
		}
	default:
		if secondaryButton(c.Key("upd-ok"), i18n.T("OK")).Clicked() {
			u.panelOpen = false
			if u.mgr != nil {
				u.mgr.Dismiss()
			}
		}
	}
}

// softwareUpdateSettings is the Software updates section of Settings →
// Diagnostics and about: when AudioSmith last checked, Check for
// Updates…, checking automatically, installing without asking, the beta
// channel and the changelog. Builds that can't update themselves (built
// on the Mac, ad hoc) say so at the top, with the download page, and show
// the switches off and disabled; Check for Updates… stays: its panel says
// the same and links to the download page (as the app menu's item does).
func (a *app) softwareUpdateSettings(c *ui.Context) {
	u := a.updates
	st := u.st
	on := u.enabled()
	settingGroup(c, i18n.T("Software updates"), func() {
		if !on {
			settingBlock(c, "about-update-off", func() {
				t := c.Theme()
				caption(c, i18n.T("This copy was built on this Mac and can't update itself, so the options below are off. Copies downloaded from actnow.dev update themselves.")).TextColor(tok(t).Text)
				ui.Link(c, i18n.T("Download the latest version from actnow.dev"), downloadURL).FontSize(px(t, fsCaption))
			})
		}
		if st.Phase == akup.Ready {
			settingRowNote(c, "about-relaunch", i18n.T("Update installed"), i18n.T("The new version runs once AudioSmith restarts."), func() {
				if primaryButton(c.Key("about-relaunch"), i18n.T("Restart AudioSmith")).Clicked() {
					a.restartForUpdate()
				}
			})
		}
		last := a.prefs.Updates.LastCheck
		if st.CheckedAt.After(last) {
			last = st.CheckedAt
		}
		note := ""
		switch {
		case !on: // the note at the top says why
		case st.Phase == akup.Checking:
			note = i18n.T("Checking for updates…")
		case st.Phase == akup.Available && st.Release != nil:
			note = i18n.T("AudioSmith %s is available", st.Release.Version)
		case last.IsZero():
			note = i18n.T("Not checked yet.")
		default:
			note = i18n.T("Last checked %s.", i18n.DateTime(last))
		}
		settingRowNote(c, "about-update", i18n.T("Check for updates"), note, func() {
			if secondaryButton(c.Key("about-update"), i18n.T("Check for Updates…")).Disabled(st.Phase == akup.Checking).Clicked() {
				a.checkForUpdates()
			}
		})
		auto := on && a.prefs.Updates.AutoCheckOn()
		settingRow(c, "about-update-auto", i18n.T("Automatic checks"), i18n.T("Once a day, from actnow.dev; a new version shows at the foot of the sidebar. Nothing about you is sent."), func() {
			if inkSwitch(c.Key("about-update-auto"), &auto).Label(i18n.T("Check for updates automatically")).Disabled(!on).Changed() {
				a.setAutoCheck(auto)
			}
		})
		install := on && a.prefs.Updates.AutoCheckOn() && a.prefs.Updates.AutoInstall
		settingRow(c, "about-update-download", i18n.T("Automatic installs"), i18n.T("A new version found by the daily check downloads and installs without asking, and runs the next time you open AudioSmith."), func() {
			if inkSwitch(c.Key("about-update-download"), &install).Label(i18n.T("Install without asking")).Disabled(!on || !a.prefs.Updates.AutoCheckOn()).Changed() {
				v := install
				a.updateSettings(func(s *settings.Settings) { s.Updates.AutoInstall = v })
			}
		})
		// The channel (thread #52, updates.go): betas are prereleases, in the
		// website's beta feed.
		beta := on && betaUpdates(a.prefs, a.version)
		info := i18n.T("Betas get new features sooner but may be less stable. Turned off, AudioSmith only updates to releases.")
		if isPrerelease(a.version) {
			info = i18n.T("Betas get new features sooner but may be less stable. This copy is a beta: turned off, AudioSmith never goes back to an older version, and stays on this one until a release newer than it is out.")
		}
		// The info shows under the name, not in ⓘ: it says what turning it
		// off does to a beta copy.
		settingRowNote(c, "about-update-beta", i18n.T("Receive beta updates"), info, func() {
			if inkSwitch(c.Key("about-update-beta"), &beta).Label(i18n.T("Beta updates")).Disabled(!on).Changed() {
				a.setBetaUpdates(beta)
			}
		})
		settingRow(c, "about-changelog", i18n.T("Changelog"), i18n.T("What changed in each version, in AudioSmith's language."), func() {
			if secondaryButton(c.Key("about-changelog"), i18n.T("Show Changelog…")).Clicked() {
				a.openHistory()
			}
		})
	})
}

// historyDialog is the changelog: every version, newest first, its notes
// in AudioSmith's language (English where one has no translation).
func (a *app) historyDialog(c *ui.Context) {
	u := a.updates
	if !u.historyOpen {
		return
	}
	t := c.Theme()
	k := tok(t)
	dialogFrame(c, "upd-history", &u.historyOpen, i18n.T("Changelog"), i18n.T("AudioSmith · %s", i18n.Name(i18n.Locale())), px(t, 620), func() {
		switch {
		case u.histLoading && u.hist == nil:
			ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c)
				ui.Text(c, i18n.T("Loading…")).TextColor(k.Text2)
			})
			return
		case u.histErr != "" && u.hist == nil:
			notice(c, "hist-err", noticeError, i18n.T("The changelog could not be read: %s", u.histErr), "", nil, nil)
			ui.Link(c, i18n.T("Read it on actnow.dev"), releaseNotesURL).FontSize(px(t, fsCaption))
			return
		case u.hist == nil || len(u.hist.Entries) == 0:
			ui.Text(c, i18n.T("No versions published yet.")).TextColor(k.Text2)
			return
		}
		for i, e := range u.hist.Entries {
			if i > 0 {
				ui.Box(c).Height(1).Background(k.Line).Margin(px(t, sp3), 0)
			}
			ui.Column(c.Key("hist-" + e.Version)).Gap(px(t, sp2)).Children(func() {
				ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Wrap().Children(func() {
					ui.Text(c, e.Version).FontSize(px(t, fsSection)).FontWeight(600)
					if e.Date != "" {
						caption(c, e.Date)
					}
					if e.Channel == "beta" {
						betaBadge(c)
					}
					if e.Version == a.version {
						tagChip(c, i18n.T("This version"))
					}
				})
				if i18n.Locale() == "zh-Hans" && e.Locale == "en" {
					caption(c, i18n.T("These notes are not translated yet; here they are in English."))
				}
				markdown.View(c.Key("hist-md-"+e.Version), e.Notes, &u.md)
			})
		}
	}, func() {
		ui.Link(c, i18n.T("On actnow.dev"), releaseNotesURL).FontSize(px(t, fsCaption))
		ui.Spacer(c)
		if secondaryButton(c.Key("hist-close"), i18n.T("Close")).Clicked() {
			u.historyOpen = false
		}
	})
}
