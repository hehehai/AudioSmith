package main

// The tasks bell (0.5 F1; at the sidebar's foot since 0.5 H): how many
// things go on, and a popover with them (the activity cards,
// shell_activity.go: a click goes where each is managed, its button
// stops it), "View all" (the Tasks page) and the recent notifications.

import (
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
)

// tasksBell is the bell at the sidebar's foot, beside Settings (rail: a
// button of the rail's foot): the count of what goes on (red while
// something needs the user), a dot for unread notifications; it opens the
// tasks popover beside the sidebar.
func (a *app) tasksBell(c *ui.Context, rail bool) {
	t := c.Theme()
	k := tok(t)
	if a.actTick {
		c.After(time.Second) // a card counts time or goes soon
	}
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
	b := ghostBase(c.Key("bell")).Height(px(t, 28)).MinWidth(px(t, 28)).Padding(0, px(t, 6)).Gap(px(t, 4)).AlignItems(ui.Center).Justify(ui.Center).
		Label(i18n.T("Tasks and notifications")).Tooltip(label).Checked(a.noticesOpen).Shrink(0)
	if rail {
		b.Size(px(t, 36), px(t, 32)).Padding(0)
	}
	if a.noticesOpen {
		b.Background(k.Pressed)
	}
	b.Children(func() {
		col := k.Text2
		if n > 0 || unread > 0 || b.Hovered() {
			col = k.Text
		}
		ui.Icon(c, bellIcon.Get(n > 0 || unread > 0)).Size(px(t, 16), px(t, 16)).TextColor(col)
		switch {
		case n > 0 && rail: // no room for the count: a dot, red when it needs the user
			face := k.Accent
			if danger {
				face = t.Danger
			}
			ui.Box(c).Absolute().Top(px(t, 4)).Right(px(t, 5)).Size(px(t, 7), px(t, 7)).Radius(px(t, 4)).Background(face)
		case n > 0:
			face := k.Accent
			if danger {
				face = t.Danger
			}
			ui.Text(c, itoa(n)).FontSize(px(t, fsMicro)).FontWeight(600).TextColor(k.OnInk).Padding(0, px(t, 5)).Radius(px(t, 8)).
				Background(face).SingleLine().Label(label)
		case unread > 0:
			ui.Box(c).Absolute().Top(px(t, 4)).Right(px(t, 4)).Size(px(t, 7), px(t, 7)).Radius(px(t, 4)).Background(k.Accent)
		}
	})
	if b.Clicked() {
		a.noticesOpen = !a.noticesOpen
	}
	ui.PopoverBase(c, b, &a.noticesOpen, func(panel ui.Element) {
		// Beside the sidebar, its foot at the bell's.
		panel.AttachTo(b, ui.AnchorBottomEnd, ui.AnchorBottomStart).MarginStart(px(t, 8)).Padding(px(t, sp2)).
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
