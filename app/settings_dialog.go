package main

// The settings dialog (0.3, ⌘,): its column of sections, the router of
// its sections (settingsSection), and the rows and titles every section
// is built of (settingRow, settingRowNote, sectionTitle). The settings
// service wiring is shell_settings.go.

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// settingsIcon is a settings section's icon.
func settingsSectionIcon(id string) icons.Icon {
	switch id {
	case "general":
		return icons.Settings
	case "appearance":
		return icons.Pallete2
	case "models":
		return icons.CloudDownload
	case "integrations":
		return icons.PlugCircle
	case "advanced":
		return icons.Database
	case "about":
		return icons.InfoCircle
	case "studio":
		return icons.Soundwave
	case "engine":
		return icons.CpuBolt
	case "logs":
		return icons.ClipboardList
	case "pronunciation":
		return icons.TextSquare
	case "dictation":
		return icons.Microphone3
	case "writing":
		return icons.PenNewRound
	case "permissions":
		return icons.LockKeyholeMinimalistic
	case "dubbing":
		return icons.VideoFramePlayHorizontal
	case "api":
		return icons.Programming
	case "audio-tools":
		return icons.CaseRound
	case "network":
		return icons.WiFiRouterMinimalistic
	case "privacy":
		return icons.ShieldCheck
	case "storage":
		return icons.Database
	case "usage":
		return icons.Chart2
	}
	return icons.InfoCircle
}

// openSettings shows the settings dialog at a section's path ("/models").
// The sections that are part of another now (settingsAliases: Appearance
// in General, Engine and Network in Models & network…) open that one.
func (a *app) openSettings(sub string) {
	id, rest, _ := strings.Cut(strings.TrimPrefix(sub, "/"), "/")
	if to, ok := settingsAliases[id]; ok {
		a.settingsReveal = id // the section scrolls to the part that was it
		sub = "/" + to
		if rest != "" {
			sub += "/" + rest
		}
	}
	changed := a.setRouter.Path() != sub
	switch {
	case changed && a.settingsOpen:
		a.setRouter.Push(sub) // a section from another: 设置 ‹ › go back to it
	case changed:
		a.setRouter.Replace(sub)
	}
	if (!a.settingsOpen || changed) && strings.HasPrefix(sub, "/models") {
		a.refreshModels() // the models read the disk again, as their page did
	}
	a.settingsOpen = true
}

// revealAnchor marks where a part of a section that was a section of its
// own begins: opened by its old path, the section scrolls to it.
func (a *app) revealAnchor(c *ui.Context, id string) {
	t := c.Theme()
	// Height 0, and the section's gap above it taken back: the part keeps
	// one gap above it, as any group (thread #96).
	ui.Box(c.Key("reveal-"+id)).Height(0).Shrink(0).Margin(-px(t, settingsGap), 0, 0, 0).Children(func() {
		if a.settingsReveal != id {
			return
		}
		a.settingsReveal = ""
		// As tall as the view: scrolled into view as little as shows it,
		// its top (the part's) comes to the top.
		ui.Box(c).Absolute().Top(-px(t, sp3)).Left(0).Width(1).Height(max(a.sectionH-px(t, sp2), 1)).PassThrough().ScrollIntoView()
	})
}

// shownPath is the path of what shows: the settings dialog's section
// ("/settings/models") while it is open, else the page's.
func (a *app) shownPath() string {
	if a.settingsOpen {
		return "/settings" + a.setRouter.Path()
	}
	return a.router.Path()
}

// Settings' size, DIPs at 100 % (the ActNow family's window spec): at
// most settingsW × settingsH (less in a smaller window: the dialog keeps a
// margin, and its sections scroll), the sections' column settingsNavW (at
// most 0.3 of the dialog).
const (
	settingsW    = 800
	settingsNavW = 200
	settingsHMax = 620
	// settingsGap is the gap between a section's groups.
	settingsGap = sp5
)

// settingsH is the height Settings takes at most: settingsHMax (tests
// that look at whole sections in a tall window raise it).
var settingsH float32 = settingsHMax

// settingsDialog is Settings (⌘,): a dialog over the page, its sections
// in a column on the left, each a column of groups (settingGroup).
// syncLastSettings remembers the section the dialog's history went to
// (‹ ›), as navigate does.
func (a *app) syncLastSettings() {
	sec := strings.TrimPrefix(a.setRouter.Path(), "/")
	if sec, _, _ = strings.Cut(sec, "/"); sec != "" {
		a.lastSettings = sec
	}
}

func (a *app) settingsDialog(c *ui.Context) {
	if !a.settingsOpen {
		if len(a.otherShown) > 0 {
			a.forgetOtherShown() // family_shared.go: "… uses a different …" says it once
		}
		return
	}
	t := c.Theme()
	k := tok(t)
	w, h := c.Size()
	margin := px(t, sp6)
	ui.DialogBase(c.Key("settings-dialog"), &a.settingsOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.32))
		dw := min(w-2*margin, px(t, settingsW))
		panel.Width(dw).Height(min(h-2*margin, px(t, settingsH))).Radius(px(t, radiusDialog)).Background(k.Bg).Clip().
			Shadow(0, 16, 48, 0, ui.RGBA(0, 0, 0, 0.28)).Label(i18n.T("Settings"))
		ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			ui.Column(c).Width(min(px(t, settingsNavW), dw*0.3)).Shrink(0).Background(k.Sidebar).Children(func() {
				// 设置 ‹ ›: back and forward through the sections shown.
				ui.Row(c).Height(px(t, 52)).Shrink(0).AlignItems(ui.Center).Gap(px(t, sp1)).Padding(0, px(t, sp2), 0, px(t, sp4)).Children(func() {
					ui.Text(c, i18n.T("Settings")).FontSize(px(t, fsSection)).FontWeight(600).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
					ui.Row(c.Key("settings-history")).Gap(px(t, 2)).Shrink(0).Children(func() {
						if iconButton(c.Key("settings-back"), icons.AltArrowLeft, tip(i18n.X("window history", "Back"), "⌘[")).Disabled(!a.setRouter.CanGoBack()).Clicked() {
							a.setRouter.Back()
							a.syncLastSettings()
						}
						if iconButton(c.Key("settings-forward"), icons.AltArrowRight, tip(i18n.X("window history", "Forward"), "⌘]")).Disabled(!a.setRouter.CanGoForward()).Clicked() {
							a.setRouter.Forward()
							a.syncLastSettings()
						}
					})
				})
				ui.Scroll(c).Grow(1).Children(func() {
					ui.Column(c).Padding(0, px(t, sp2), px(t, sp2)).Gap(px(t, 2)).Children(func() {
						cur := strings.TrimPrefix(a.setRouter.Path(), "/")
						cur, _, _ = strings.Cut(cur, "/")
						for _, s := range settingsSections() {
							sel := cur == s.id
							b := ui.ButtonBase(c.Key("settings-"+s.id)).Padding(px(t, 6), px(t, sp2)).Gap(px(t, sp2)).Radius(px(t, radiusControl)).
								Justify(ui.Start).Label(s.title).Checked(sel)
							switch {
							case sel:
								b.Background(k.Pressed)
							case b.Hovered():
								b.Background(k.Hover)
							}
							b.Children(func() {
								col := k.Text2
								if sel {
									col = k.Text
								}
								ui.Icon(c, settingsSectionIcon(s.id).Get(sel)).Size(px(t, 16), px(t, 16)).TextColor(col)
								txt := ui.Text(c, s.title).SingleLine().Ellipsis("…").MinWidth(0).Grow(1)
								if sel {
									txt.FontWeight(600)
								} else {
									txt.TextColor(k.G[8])
								}
							})
							if b.Clicked() {
								a.navigate("/settings/" + s.id)
							}
						}
					})
				})
			})
			ui.Box(c).Width(1).Background(k.Line)
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				a.setRouter.View(c, func(r *ui.Route) {
					title := ""
					for _, s := range settingsSections() {
						if r.Match("/" + s.id) {
							title = s.title
						}
					}
					ui.Row(c).Height(px(t, 52)).Shrink(0).AlignItems(ui.Center).Padding(0, px(t, sp3), 0, px(t, sp6)).Gap(px(t, sp2)).Children(func() {
						ui.Text(c, title).FontSize(px(t, fsSection)).FontWeight(600).SingleLine().Ellipsis("…").MinWidth(0).Grow(1)
						if iconButton(c.Key("settings-close"), icons.Close, i18n.T("Close")).Clicked() {
							a.settingsOpen = false
						}
					})
					r.Title(title) // the page takes the focus: its name for VoiceOver
					body := func() {
						// What a section lays out by (pageWidth): its own width.
						a.sectionW = dw - min(px(t, settingsNavW), dw*0.3) - 1 - px(t, 2*sp6)
						a.sectionH = min(h-2*margin, px(t, settingsH)) - px(t, 52)
						defer func() { a.sectionW = 0 }()
						a.settingsSection(c, r)
					}
					if r.Match("/logs") { // the log scrolls in itself: the section does not
						ui.Column(c).Grow(1).MinHeight(0).Padding(px(t, sp1), px(t, sp6), px(t, sp6)).Gap(px(t, sp3)).Children(body)
						return
					}
					ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
						ui.Column(c).Padding(px(t, sp1), px(t, sp6), px(t, sp6)).Gap(px(t, settingsGap)).Children(body)
					})
				}).Grow(1).MinHeight(0)
			})
		})
	})
}

// settingsSection builds the section of the settings dialog r shows:
// each a column of groups (settingGroup), several of them made of what
// were sections of their own.
func (a *app) settingsSection(c *ui.Context, r *ui.Route) {
	t := c.Theme()
	section := func(id string, build ...func(*ui.Context)) bool {
		if !r.Match("/"+id) && !r.Match("/"+id+"/{rest}") {
			return false
		}
		for _, s := range settingsSections() {
			if s.id == id {
				r.Title(s.title)
			}
		}
		a.lastSettings = id
		for _, b := range build {
			b(c)
		}
		return true
	}
	switch {
	case section("general", a.generalSettings, a.appearanceSettings):
	case section("studio", a.studioSettings, a.pronunciationSettings): // M1-G: pronunciation_settings.go
	case a.dubSettingsRoute(c, r): // M4: Settings → Dubbing (dub_pages.go, thread #19)
	// M2 (thread #17).
	case section("dictation", a.dictationSettings, a.permissionsSettings):
	case section("writing", a.writingSettings):
	case section("integrations", a.integrationsSettings): // M5: settings_integrations.go
	// M7a (thread #22): settings_*.go.
	case section("audio-tools", a.audioToolsSettings):
	case section("models", a.modelsList, a.networkSettings, a.modelSettings, a.engineSettings, a.licenceSettings):
	case section("engine", a.engineSettingsSection): // engine_view.go (0.5 F1: the Engine page)
	case section("logs", a.logsSettings): // shell_logs.go (0.5 F1: the Logs page)
	case section("privacy", a.privacySettings):
	case section("advanced", a.storageSettings, a.usageSettings, a.diagnosticsSettings):
	case section("about", a.aboutSettings):
	default:
		ui.Text(c, i18n.T("There is no page at %s.", a.shownPath())).TextColor(t.TextMuted)
	}
}

// settingRow is a setting: its name, what it does (desc, "" for none)
// and its control on the right. In a group (settingGroup: Settings) the
// line shows under the name; elsewhere (a page's form, a dialog) it is a
// ⓘ beside it (formRow in ui_layout.go).
func settingRow(c *ui.Context, key, title, desc string, control func()) {
	if settingDepth == 0 {
		formRow(c, key, title, desc, control)
		return
	}
	groupRow(c, key, title, desc, ui.Color{}, false, control)
}

// settingRowNote is a setting whose line is a state to read at once (a
// path, what is missing, a version): it shows under the name, and can be
// selected to copy.
func settingRowNote(c *ui.Context, key, title, note string, control func()) {
	if settingDepth > 0 {
		groupRow(c, key, title, note, ui.Color{}, true, control)
		return
	}
	t := c.Theme()
	ui.Row(c.Key(key)).Wrap().GapX(px(t, sp4)).GapY(px(t, 6)).AlignItems(ui.Center).MinHeight(px(t, 32)).Children(func() {
		ui.Column(c).Grow(1).Basis(px(t, 200)).MinWidth(px(t, 140)).Gap(px(t, 2)).Children(func() {
			ui.Text(c, title)
			if note != "" {
				caption(c, note).Selectable()
			}
		})
		ui.Row(c).Wrap().Gap(px(t, sp2)).AlignItems(ui.Center).Shrink(1).MinWidth(0).Children(control)
	})
}

// sectionTitle heads a group of settings.
func sectionTitle(c *ui.Context, title string) {
	t := c.Theme()
	sectionTitleText(c, title).Margin(px(t, sp2), 0, 0, 0)
}

func sectionTitleText(c *ui.Context, title string) ui.Element {
	t := c.Theme()
	return ui.Text(c, title).FontSize(px(t, fsCaption)).FontWeight(600).TextColor(tok(t).Text2)
}

// sectionTitleTip is sectionTitle with a ⓘ beside it: the margin goes on
// the row, so the title and the ⓘ share one centre line.
func sectionTitleTip(c *ui.Context, title, tip string) {
	t := c.Theme()
	ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Margin(px(t, sp2), 0, 0, 0).Children(func() {
		sectionTitleText(c, title)
		infoTip(c, tip)
	})
}
