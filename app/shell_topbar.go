package main

// The top bar over every page (0.5 F1, after ElevenLabs' window): on the
// left the breadcrumb (音色 › 我的), in the middle "Search everything ⌘K"
// (it opens the command palette), on the right the tasks bell: how many
// things go on, and a popover with them (the activity cards that stood at
// the sidebar's foot, shell_activity.go) and the recent notifications. It
// drags the window, and keeps clear of the traffic lights beside the
// icon rail (inset).

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// crumb is a part of the breadcrumb: its title, and the path a click on
// it opens ("" for the last, the page shown).
type crumb struct{ title, path string }

// crumbs is the breadcrumb of path.
func crumbs(path string) []crumb {
	voices := i18n.X("nav", "Voices")
	newVoice := i18n.T("New voice")
	switch path {
	case "/voices/discover":
		return []crumb{{voices, "/voices/mine"}, {i18n.T("Discover"), ""}}
	case "/voices/mine":
		return []crumb{{voices, "/voices/discover"}, {i18n.T("Mine"), ""}}
	case "/voices/new/clone":
		return []crumb{{newVoice, "/voices/new"}, {i18n.T("Quick clone"), ""}}
	case "/voices/new/design":
		return []crumb{{newVoice, "/voices/new"}, {i18n.T("Design a voice"), ""}}
	case "/calls/setup", "/integrations/twilio":
		return []crumb{{pageTitle("/calls"), "/calls"}, {i18n.T("Twilio setup"), ""}}
	case "/api-reference":
		return []crumb{{pageTitle("/developers"), "/developers"}, {i18n.T("API reference"), ""}}
	}
	for _, p := range morePages {
		if section(path) == p.path {
			return []crumb{{i18n.T("More"), ""}, {pageTitle(p.path), ""}}
		}
	}
	return []crumb{{pageTitle(navKey(path)), ""}}
}

// pageCrumbs is crumbs with the items of the tool pages (0.5 F2): 视频配音 ›
// a dub, 语音转文字 › a transcript, 人声分离 › an isolation.
func (a *app) pageCrumbs(path string) []crumb {
	for _, tool := range []string{"/dub", "/transcripts", "/isolate"} {
		id, ok := strings.CutPrefix(path, tool+"/")
		if !ok || id == "" {
			continue
		}
		title := ""
		switch tool {
		case "/dub":
			title = a.dubTitleOf(id)
		case "/transcripts":
			title = a.transcriptTitleOf(id)
		case "/isolate":
			title = a.isoTitleOf(id)
		}
		return []crumb{{pageTitle(tool), tool}, {title, ""}}
	}
	return crumbs(path)
}

// topBar is the bar over the page.
func (a *app) topBar(c *ui.Context, inset float32) {
	t := c.Theme()
	k := tok(t)
	if a.actTick {
		c.After(time.Second) // a card counts time or goes soon
	}
	narrow := a.pageWidth(c) < px(t, 640)
	ui.Row(c.Key("topbar")).Height(titleBarHeight).Shrink(0).AlignItems(ui.Center).Padding(0, px(t, sp4), 0, px(t, sp6)+inset).Gap(px(t, sp3)).
		DragWindow().BorderWidth(0, 0, 1, 0).BorderColor(k.Line).Children(func() {
		ui.Row(c.Key("crumbs")).Grow(1).Basis(0).MinWidth(0).Gap(px(t, 6)).AlignItems(ui.Center).Role(ui.RoleGroup).Label(i18n.T("Breadcrumb")).Children(func() {
			cs := a.pageCrumbs(a.router.Path())
			for i, cr := range cs {
				if i > 0 {
					ui.Text(c, "›").TextColor(k.Text3).Shrink(0)
				}
				last := i == len(cs)-1
				if cr.path == "" || last {
					tx := ui.Text(c, cr.title).SingleLine().Ellipsis("…").MinWidth(0)
					if last {
						tx.FontWeight(600)
					} else {
						tx.TextColor(k.Text2)
					}
					continue
				}
				b := ghostBase(c.Key("crumb-"+cr.path)).Padding(px(t, 2), px(t, 4)).Label(cr.title).MinWidth(0).Shrink(1)
				b.Children(func() {
					col := k.Text2
					if b.Hovered() {
						col = k.Text
					}
					ui.Text(c, cr.title).TextColor(col).SingleLine().Ellipsis("…").MinWidth(0)
				})
				if b.Clicked() {
					a.navigate(cr.path)
				}
			}
		})
		a.searchEverything(c, narrow)
		ui.Row(c).Grow(1).Basis(0).MinWidth(0).Justify(ui.End).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
			a.tasksBell(c)
		})
	})
}

// searchEverything is the top bar's field that opens the palette (⌘K):
// a magnifier, "Search everything", the key; an icon when narrow.
func (a *app) searchEverything(c *ui.Context, narrow bool) {
	t := c.Theme()
	k := tok(t)
	label := i18n.T("Search everything")
	if narrow {
		if iconButton(c.Key("top-search"), icons.Magnifer, tip(label, "⌘K")).Clicked() {
			a.openPalette()
		}
		return
	}
	b := ui.ButtonBase(c.Key("top-search")).Width(px(t, 280)).Shrink(1).MinWidth(px(t, 150)).Height(px(t, 30)).Gap(px(t, sp2)).AlignItems(ui.Center).
		Padding(0, px(t, 6), 0, px(t, 10)).Radius(px(t, radiusControl)).Border(1, k.Line).Background(k.Subtle).Label(label).Tooltip(tip(label, "⌘K"))
	if b.Hovered() {
		b.Background(k.Hover)
	}
	b.Children(func() {
		ui.Icon(c, searchIcon.Linear).Size(px(t, 14), px(t, 14)).TextColor(k.Text3).Shrink(0)
		ui.Text(c, label+"…").TextColor(k.Text3).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		kbd(c, "⌘K")
	})
	if b.Clicked() {
		a.openPalette()
	}
}

// tasksBell is the bell of the top bar: the count of what goes on (red
// while something needs the user), a dot for unread notifications; it
// opens the tasks popover.
func (a *app) tasksBell(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	n := len(a.actCards)
	unread := a.unreadNotices()
	danger := false
	for _, cd := range a.actCards {
		danger = danger || cd.danger
	}
	label := i18n.T("Tasks and notifications")
	switch {
	case n > 0:
		label = i18n.N(n, "%d in progress", "%d in progress", n)
	case unread > 0:
		label = i18n.N(unread, "Notifications: %d unread", "Notifications: %d unread", unread)
	}
	b := ghostBase(c.Key("top-bell")).Height(px(t, 28)).MinWidth(px(t, 28)).Padding(0, px(t, 6)).Gap(px(t, 4)).AlignItems(ui.Center).Justify(ui.Center).
		Label(i18n.T("Tasks and notifications")).Tooltip(label).Checked(a.noticesOpen)
	if a.noticesOpen {
		b.Background(k.Pressed)
	}
	b.Children(func() {
		col := k.Text2
		if n > 0 || unread > 0 || b.Hovered() {
			col = k.Text
		}
		ui.Icon(c, bellIcon.Get(n > 0 || unread > 0)).Size(px(t, 16), px(t, 16)).TextColor(col)
		if n > 0 {
			face := k.Accent
			if danger {
				face = t.Danger
			}
			ui.Text(c, itoa(n)).FontSize(px(t, fsMicro)).FontWeight(600).TextColor(k.OnInk).Padding(0, px(t, 5)).Radius(px(t, 8)).
				Background(face).SingleLine().Label(label)
		} else if unread > 0 {
			ui.Box(c).Absolute().Top(px(t, 4)).Right(px(t, 4)).Size(px(t, 7), px(t, 7)).Radius(px(t, 4)).Background(k.Accent)
		}
	})
	if b.Clicked() {
		a.noticesOpen = !a.noticesOpen
	}
	ui.PopoverBase(c, b, &a.noticesOpen, func(panel ui.Element) {
		panel.AttachTo(b, ui.AnchorBottomEnd, ui.AnchorTopEnd).Margin(px(t, 6), 0, 0, 0).Padding(px(t, sp2)).
			Radius(px(t, radiusCard)).Background(k.Bg).Border(1, k.Line).Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.18)).Label(i18n.T("Tasks and notifications"))
		a.tasksPopover(c)
	})
}

// tasksPopover is the bell's popover: what goes on (the activity cards,
// as rows: a click goes where it is managed, its button stops it), "View
// all" (the Tasks page), then the notifications.
func (a *app) tasksPopover(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	_, h := c.Size()
	ui.Column(c.Key("tasks-popover")).Width(px(t, 380)).Gap(px(t, sp2)).Children(func() {
		ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Padding(0, px(t, 4)).Children(func() {
			sectionHeading(c, i18n.T("In progress")).Grow(1)
			all := ghostBase(c.Key("tasks-all")).Padding(px(t, 2), px(t, 6)).Label(i18n.T("View all"))
			all.Children(func() {
				ui.Text(c, i18n.T("View all")+" ›").FontSize(px(t, fsCaption)).TextColor(k.Text2).SingleLine()
			})
			if all.Clicked() {
				a.noticesOpen = false
				a.navigate("/tasks")
			}
		})
		if len(a.actCards) == 0 {
			ui.Text(c, i18n.T("Nothing is running.")).FontSize(px(t, fsCaption)).TextColor(k.Text3).Padding(px(t, 2), px(t, 4), px(t, sp1))
		} else {
			ui.Scroll(c).MaxHeight(min(px(t, 260), h*0.35)).Children(func() {
				ui.Column(c).Gap(px(t, 2)).Role(ui.RoleList).Label(i18n.T("In progress")).Children(func() {
					for _, cd := range a.actCards {
						a.bellTaskRow(c, cd)
					}
				})
			})
		}
		ui.Divider(c)
		a.noticeCentreIn(c, min(px(t, 300), h*0.35))
	})
}

// bellTaskRow is an activity card as a row of the bell's popover.
func (a *app) bellTaskRow(c *ui.Context, cd actCard) {
	t := c.Theme()
	k := tok(t)
	ui.Row(c.Key("task-" + cd.key)).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
		label := cd.title
		if cd.detail != "" {
			label += " · " + cd.detail
		}
		b := ui.ButtonBase(c.Key("open")).Grow(1).MinWidth(0).Gap(px(t, sp2)).AlignItems(ui.Center).Padding(px(t, 6), px(t, 6)).Radius(px(t, radiusControl)).Label(label)
		if b.Hovered() {
			b.Background(k.Hover)
		}
		b.Children(func() {
			col := k.Text2
			switch {
			case cd.danger:
				col = t.Danger
			case cd.done:
				col = t.Success
			}
			ui.Icon(c, cd.icon.Linear).Size(px(t, 16), px(t, 16)).TextColor(col).Shrink(0)
			ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 4)).Children(func() {
				ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
					ui.Text(c, cd.title).FontWeight(600).SingleLine().Ellipsis("…").MinWidth(0).Grow(1)
					if cd.detail != "" {
						ui.Text(c, cd.detail).FontSize(px(t, fsCaption)).TextColor(k.Text3).SingleLine().Shrink(0)
					}
				})
				if cd.frac > 0 && cd.frac <= 1 { // only real progress
					ui.Box(c.Key("bar")).Height(px(t, 3)).Radius(px(t, 2)).Background(k.G[2]).Children(func() {
						ui.Box(c).WidthPercent(float32(cd.frac) * 100).Height(px(t, 3)).Radius(px(t, 2)).Background(k.Accent)
					})
				}
			})
		})
		if b.Clicked() && cd.open != nil {
			a.noticesOpen = false
			cd.open()
		}
		if cd.action != nil {
			if iconButton(c.Key("action"), cd.action.icon, cd.action.tip).Clicked() {
				cd.action.run()
			}
		}
	})
}
