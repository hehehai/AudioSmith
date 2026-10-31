package main

// The tool pages 0.5 F1 adds to the sidebar: Voice isolator (/isolate),
// a placeholder until F2 builds it, and Dictation (/dictation): the
// shortcut, a box to try it in and the recent dictations. Developers'
// route is here too (developers_view.go builds the page).

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// toolPagesState is the pages' view state.
type toolPagesState struct {
	dictTry      string // Dictation's box to try it in
	dictTryFocus bool   // the box takes the focus (Start in it)
	devTab       int    // Developers' tab: its path's (developers_view.go)
}

// toolPageTitle translates the pages' titles.
func toolPageTitle(path string) (string, bool) {
	switch path {
	case "/isolate":
		return i18n.T("Voice isolator"), true
	case "/dictation":
		return i18n.T("Dictation"), true
	case "/developers":
		return i18n.T("Developers"), true
	}
	return "", false
}

// toolRoute builds the pages.
func (a *app) toolRoute(c *ui.Context, r *ui.Route) bool {
	switch {
	case r.Match("/isolate"):
		r.Title(i18n.T("Voice isolator"))
		a.isolatePage(c)
	case r.Match("/dictation"):
		r.Title(i18n.T("Dictation"))
		a.dictationPage(c)
	case r.Match("/developers"):
		r.Title(i18n.T("Developers"))
		a.devTab = 0
		a.developersPage(c, "")
	case r.Match("/developers/api"):
		r.Title(i18n.T("Developers") + " · " + i18n.T("Local API"))
		a.devTab = 1
		a.developersPage(c, "")
	case r.Match("/developers/tools"):
		r.Title(i18n.T("Developers") + " · " + i18n.T("Tools"))
		a.devTab = 2
		a.developersPage(c, "")
	case r.Match("/developers/{slug}"):
		r.Title(i18n.T("Developers"))
		a.developersPage(c, r.Param("slug"))
	default:
		return false
	}
	return true
}

// isolatePage is Voice isolator's place until F2 builds it.
func (a *app) isolatePage(c *ui.Context) {
	pageHeader(c, i18n.T("Voice isolator"), nil)
	emptyState(c, isolateIcon, i18n.T("Coming in this version"),
		i18n.T("Drop an audio or video file and get the voice alone, the music and noise without it, and a cleaned-up voice: on this Mac, with the models dubbing uses."), nil)
}

// dictationPage is Dictation: its shortcut (set in Settings → Dictation),
// a box to try it in, and the recent dictations (transcripts of the
// dictation source, as History keeps them).
func (a *app) dictationPage(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	pageHeader(c, i18n.T("Dictation"), func() {
		if ghostButton(c.Key("dict-settings"), i18n.T("Settings…")).Clicked() {
			a.navigate("/settings/dictation")
		}
	})
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Padding(px(t, sp1), px(t, sp6), px(t, sp6)).Gap(px(t, sp4)).MaxWidth(px(t, 760)).Children(func() {
			caption(c, i18n.T("Dictate into any app: press the shortcut, speak, and the text is typed where your cursor is. Speech stays on this Mac.")).Wrap()
			card(c.Key("dict-shortcut")).Children(func() {
				ui.Row(c).Gap(px(t, sp3)).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, icons.Keyboard.Linear).Size(px(t, 20), px(t, 20)).TextColor(k.Text2).Shrink(0)
					ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 2)).Children(func() {
						ui.Text(c, i18n.T("Dictation shortcut")).FontWeight(600)
						switch {
						case !a.prefs.Dictation.Enabled:
							caption(c, i18n.T("Off. Turn it on in Settings → Dictation."))
						case a.dictShortcut != "":
							caption(c, i18n.T("Press %s in any app.", dictationGlyphs(a.dictShortcut)))
						default:
							caption(c, orDash(a.dictRegErr))
						}
					})
					if a.dictShortcut != "" {
						kbd(c, dictationGlyphs(a.dictShortcut))
					}
				})
			})
			sectionTitle(c, i18n.T("Try it"))
			box := ui.TextArea(c.Key("dict-try"), &a.dictTry).Lines(3, 6).Placeholder(i18n.T("Click here, then press the shortcut and speak…")).Label(i18n.T("Try it"))
			if a.dictTryFocus {
				a.dictTryFocus = false
				box.Focus()
			}
			ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
				live := a.dict != nil && a.dict.phase.live()
				label := i18n.T("Start dictation")
				if live {
					label = i18n.T("Stop dictation")
				}
				if ui.Button(c.Key("dict-try-go"), label).Clicked() {
					a.dictTryFocus = true // the text goes where the focus is: the box
					a.dictToggle()
				}
				if a.dictTry != "" && ghostButton(c.Key("dict-try-studio"), i18n.T("Read it in Text to speech")).Clicked() {
					a.homeToStudio(a.dictTry) // home_route.go
				}
			})
			a.recentDictations(c)
		})
	})
}

// recentDictations lists the latest dictations kept (Settings → Privacy
// decides whether they are): a click opens the transcript.
func (a *app) recentDictations(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	var list []core.Transcript
	for _, tr := range a.trList {
		if tr.Source == core.SourceDictation {
			list = append(list, tr)
			if len(list) == 8 {
				break
			}
		}
	}
	sectionTitle(c, i18n.T("Recent dictations"))
	if len(list) == 0 {
		caption(c, i18n.T("Your dictations show here, if Settings → Privacy keeps them."))
		return
	}
	ui.Column(c.Key("dict-recent")).Gap(px(t, 2)).Children(func() {
		for _, tr := range list {
			text := strings.TrimSpace(tr.Preferred())
			row := ui.ButtonBase(c.Key("dict-" + tr.ID)).Gap(px(t, sp2)).AlignItems(ui.Center).Padding(px(t, sp2)).Radius(px(t, radiusControl)).Label(orDash(text))
			if row.Hovered() {
				row.Background(k.Hover)
			}
			row.Children(func() {
				ui.Icon(c, micIcon.Linear).Size(px(t, 14), px(t, 14)).TextColor(k.Text2).Shrink(0)
				ui.Text(c, orDash(text)).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
				meta := ago(tr.CreatedAt)
				if tr.App != "" {
					meta = tr.App + " · " + meta
				}
				ui.Text(c, meta).FontSize(px(t, fsCaption)).TextColor(k.Text3).SingleLine().Shrink(0)
			})
			if row.Clicked() {
				a.navigate("/transcripts")
				a.selectTranscript(tr.ID)
			}
		}
	})
}
