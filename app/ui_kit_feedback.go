package main

// The 0.5 kit's empty states and tips (ui_kit.go): ghost rows (a list's
// empty state: rows of placeholders fading out, then what to do), the
// "introducing" dialog (an illustration, three points and a wide primary
// button, shown once per feature and remembered in the settings), and the
// corner tip card (a small dark card at the bottom left of the window).

import (
	"slices"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// ghostRows is the empty state of a list that shows as a table: rows
// placeholder rows of grey bars (an avatar, a name, a column), each
// fainter than the one above, then ic, title, body ("" for none) and
// actions (one primary at most; nil for none), as a hint of what the list
// will hold.
func ghostRows(c *ui.Context, rows int, ic icons.Icon, title, body string, actions func()) ui.Element {
	t := c.Theme()
	k := tok(t)
	bar := k.G[4]
	return ui.Column(c).Gap(0).AlignItems(ui.Stretch).Shrink(0).Children(func() {
		for i := range rows {
			fade := 1 - float32(i)/float32(rows)
			ui.Row(c.Key(i)).Height(px(t, 52)).Gap(px(t, sp3)).AlignItems(ui.Center).Padding(0, px(t, sp3)).
				BorderWidth(0, 0, 1, 0).BorderColor(k.Line.Alpha(fade)).Opacity(fade).Children(func() {
				ui.Box(c).Size(px(t, 28), px(t, 28)).Radius(px(t, 14)).Background(bar).Shrink(0)
				ui.Column(c).Gap(px(t, 6)).Grow(1).Children(func() {
					ui.Box(c).Height(px(t, 8)).Width(px(t, float32(150-20*(i%3)))).Radius(px(t, 4)).Background(bar)
					ui.Box(c).Height(px(t, 6)).Width(px(t, float32(90+25*(i%2)))).Radius(px(t, 3)).Background(bar)
				})
				ui.Box(c).Height(px(t, 8)).Width(px(t, 56)).Radius(px(t, 4)).Background(bar).Shrink(0)
				ui.Box(c).Height(px(t, 8)).Width(px(t, 36)).Radius(px(t, 4)).Background(bar).Shrink(0)
			})
		}
		ui.Column(c).Gap(px(t, 6)).AlignItems(ui.Center).Padding(px(t, sp4), px(t, sp6)).Children(func() {
			ui.Icon(c, ic.Linear).Size(px(t, 24), px(t, 24)).TextColor(k.Text3)
			ui.Text(c, title).FontWeight(500).TextAlign(ui.Center)
			if body != "" {
				ui.Text(c, body).FontSize(px(t, fsCaption)).TextColor(k.Text2).TextAlign(ui.Center).MaxWidth(px(t, 420))
			}
			if actions != nil {
				ui.Row(c).Gap(px(t, sp2)).Wrap().Justify(ui.Center).Margin(px(t, 6), 0, 0).Children(actions)
			}
		})
	})
}

// ---- Shown once ----

// seen says whether id (an intro dialog, a promo card, a tip) was shown or
// dismissed already (settings.Appearance.Seen).
func (a *app) seen(id string) bool { return slices.Contains(a.prefs.Appearance.Seen, id) }

// markSeen remembers id as seen, in the settings.
func (a *app) markSeen(id string) {
	if a.seen(id) {
		return
	}
	a.updateSettings(func(s *settings.Settings) {
		if !slices.Contains(s.Appearance.Seen, id) {
			s.Appearance.Seen = append(s.Appearance.Seen, id)
		}
	})
}

// introPoint is one of an intro dialog's three points: an icon, a title
// and a line under it.
type introPoint struct {
	Icon        icons.Icon
	Title, Body string
}

// intro is what an "introducing" dialog says.
type intro struct {
	Title  string
	Art    func()        // the illustration on top (nil: Icon on the grey fill)
	Icon   icons.Icon    // the feature's icon, for the default illustration
	Points [3]introPoint // what it does, in three points
	Action string        // the primary button ("" for 「开始使用」)
}

// introState is the intro dialog on screen: its id, and whether it is
// still open (Escape, a click outside and the button close it).
type introState struct {
	id   string
	open bool
}

// introDialog shows the "introducing" dialog of the feature id (a page's
// first visit: "intro:dub") until the user closes it, once: it is
// remembered in the settings (markSeen) and never shows again. Call it
// from the page's view on each build.
func (a *app) introDialog(c *ui.Context, id string, in intro) {
	if a.seen(id) {
		return
	}
	if a.intro.id != id {
		a.intro = introState{id: id, open: true}
	}
	t := c.Theme()
	k := tok(t)
	w, h := c.Size()
	margin := px(t, sp6)
	action := in.Action
	if action == "" {
		action = i18n.T("Get started")
	}
	ui.DialogBase(c.Key("intro-"+id), &a.intro.open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.32))
		panel.Width(min(w-2*margin, px(t, 440))).MaxHeight(h-2*margin).Radius(px(t, radiusDialog)).Background(k.Bg).Clip().
			Shadow(0, 16, 48, 0, ui.RGBA(0, 0, 0, 0.28)).Label(in.Title)
		ui.Scroll(c.Key("intro-body")).Grow(1).Shrink(1).MinHeight(0).Children(func() {
			ui.Column(c).AlignItems(ui.Stretch).Children(func() {
				art := ui.Column(c).Height(px(t, 168)).Center().Background(k.Subtle).Shrink(0)
				if t.Dark {
					art.Background(k.G[1])
				}
				art.Children(func() {
					if in.Art != nil {
						in.Art()
					} else if in.Icon.Linear != nil {
						ui.Box(c).Size(px(t, 72), px(t, 72)).Radius(px(t, radiusFeature)).Background(k.Bg).Border(1, k.Line).Center().Children(func() {
							ui.Icon(c, in.Icon.Linear).Size(px(t, 34), px(t, 34)).TextColor(k.Text)
						})
					}
				})
				ui.Column(c).Padding(px(t, sp5), px(t, sp6), px(t, sp6)).Gap(px(t, sp4)).AlignItems(ui.Stretch).Children(func() {
					ui.Text(c, in.Title).FontSize(px(t, 20)).FontWeight(500)
					for i, p := range in.Points {
						if p.Title == "" {
							continue
						}
						ui.Row(c.Key(i)).Gap(px(t, sp3)).AlignItems(ui.Start).Children(func() {
							ui.Icon(c, p.Icon.Linear).Size(px(t, 18), px(t, 18)).TextColor(k.Text).Shrink(0).Margin(px(t, 1), 0, 0)
							ui.Column(c).Gap(px(t, 2)).Grow(1).MinWidth(0).Children(func() {
								ui.Text(c, p.Title).FontWeight(500)
								if p.Body != "" {
									ui.Text(c, p.Body).FontSize(px(t, fsCaption)).TextColor(k.Text2)
								}
							})
						})
					}
					if primaryButtonWide(c.Key("intro-go"), icons.Icon{}, action).Margin(px(t, sp2), 0, 0).Clicked() {
						a.intro.open = false
					}
				})
			})
		})
	})
	if !a.intro.open {
		a.markSeen(id)
		a.intro = introState{}
	}
}

// cornerTip is a small dark card at the bottom left of the window (news,
// a hint): ic, title, body ("" for none) and ×, which calls onClose.
// Callers remember the dismissal (app.markSeen). Call it once per window
// build; it floats over the page.
func cornerTip(c *ui.Context, key string, ic icons.Icon, title, body string, onClose func()) {
	t := c.Theme()
	k := tok(t)
	fill, fg := k.G[9], k.G[0]
	if t.Dark {
		fill, fg = k.G[3], k.G[9]
	}
	ui.Overlay(c, func() {
		card := ui.Row(c.Key(key)).Absolute().Left(px(t, sp4)).Bottom(px(t, sp4)).Width(px(t, 280)).Gap(px(t, sp2)).
			Padding(px(t, sp3), px(t, sp2), px(t, sp3), px(t, sp3)).Radius(px(t, radiusCard)).Background(fill).AlignItems(ui.Start).
			Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.24)).Label(title)
		card.Children(func() {
			ui.Icon(c, ic.Linear).Size(px(t, 18), px(t, 18)).TextColor(fg).Shrink(0).Margin(px(t, 1), 0, 0)
			ui.Column(c).Gap(px(t, 2)).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, title).FontWeight(500).TextColor(fg)
				if body != "" {
					ui.Text(c, body).FontSize(px(t, fsCaption)).TextColor(fg.Alpha(0.7))
				}
			})
			x := ui.ButtonBase(c.Key("tip-close")).Size(px(t, 22), px(t, 22)).Radius(px(t, 6)).Center().
				Label(i18n.T("Dismiss")).Tooltip(i18n.T("Dismiss")).Shrink(0)
			if x.Hovered() {
				x.Background(fg.Alpha(0.15))
			}
			x.Children(func() {
				ui.Icon(c, icons.Close.Linear).Size(px(t, 14), px(t, 14)).TextColor(fg.Alpha(0.8))
			})
			if x.Clicked() && onClose != nil {
				onClose()
			}
		})
	})
}
