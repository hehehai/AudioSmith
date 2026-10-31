package main

// The sidebar's layouts (M7a, the ActNow family's window layout; 0.5 F1's
// organisation): the full sidebar, resizable, whose width and closed
// sections the settings remember, and the icon rail (Settings → General →
// Appearance, or the ▯ button) that keeps only the icons; then the full
// sidebar itself: the places (Home, Voices, Projects, Assets), the tools
// (a section that folds) with "··· More" (a menu of Workflows, Batch and
// Calls), and at its foot the update line, Developers, Settings and the
// tasks bell (shell_bell.go). Its top is the search (shell_search.go).
// Its pages are in shell_pages.go. There is no bar over the pages: each
// page's header is its own (ui_page_header.go).

import (
	"slices"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// railWidth is the icon rail's width, DIPs at 100 %.
const railWidth = 52

// navState is the sidebar's view state.
type navState struct {
	navOpen    map[string]*bool // sections: open or closed
	sideW      float32          // the sidebar's width, DIPs at the theme's scale
	sideWScale float32          // the px factor sideW was computed at
	sideWAt    time.Time        // when the user last resized it (saved after a pause)
	moreOpen   bool             // the "··· More" menu shows
	railMore   bool             // … from the icon rail
	voicesTab  string           // the Voices tab shown last: "discover", "mine" (voices_pages.go)
	sideSearchState
}

func (a *app) railMode() bool { return a.prefs.Appearance.Rail }

func (a *app) setRail(on bool) {
	a.updateSettings(func(s *settings.Settings) { s.Appearance.Rail = on })
}

// sectionOpen is the open state of a sidebar section, kept in the
// settings when the user closes it.
func (a *app) sectionOpen(id string) *bool {
	if a.navOpen == nil {
		a.navOpen = map[string]*bool{}
	}
	p, ok := a.navOpen[id]
	if !ok {
		open := !slices.Contains(a.prefs.Appearance.ClosedSections, id)
		p = &open
		a.navOpen[id] = p
	}
	return p
}

// saveClosedSections saves the sections the user closed or opened.
func (a *app) saveClosedSections() {
	var closed []string
	for _, g := range navGroups {
		if p, ok := a.navOpen[g.id]; ok && !*p && g.id != "" {
			closed = append(closed, g.id)
		}
	}
	if slices.Equal(closed, a.prefs.Appearance.ClosedSections) {
		return
	}
	a.updateSettings(func(s *settings.Settings) { s.Appearance.ClosedSections = closed })
}

// sidebarFrame lays out the sidebar and the page: the rail, or the full
// sidebar beside a divider that resizes it.
func (a *app) sidebarFrame(c *ui.Context, page func()) {
	t := c.Theme()
	k := tok(t)
	headerInset = 0
	if a.railMode() {
		// The window controls (the traffic lights) reach past the rail:
		// the pages' headers move over to keep clear of them.
		headerInset = max(0, c.TitleBar().Left-px(t, railWidth)-px(t, sp6)+px(t, sp3))
		ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			ui.Column(c).Width(px(t, railWidth)).Shrink(0).Background(k.Sidebar).Children(func() { a.navRail(c) })
			ui.Box(c).Width(1).Background(k.Line)
			ui.Column(c).Grow(1).MinWidth(0).Background(k.Bg).Children(page)
		})
		return
	}
	scale := px(t, 1)
	if a.sideW == 0 || a.sideWScale != scale {
		w := a.prefs.Appearance.SidebarWidth
		if w == 0 {
			w = sidebarWidth
		}
		a.sideW, a.sideWScale = px(t, float32(w)), scale
	}
	split := ui.Split(c.Key("sidebar-split"), &a.sideW, func() {
		ui.Column(c).Fill().Background(k.Sidebar).Children(func() { a.sidebar(c) })
	}, func() {
		ui.Column(c).Grow(1).MinWidth(0).Background(k.Bg).Children(page)
	}).Grow(1).MinHeight(0)
	// Keep the width in its bounds, and save it once the drag rests.
	lo, hi := px(t, settings.MinSidebarWidth), px(t, settings.MaxSidebarWidth)
	if a.sideW < lo || a.sideW > hi {
		a.sideW = min(max(a.sideW, lo), hi)
	}
	if split.Changed() {
		a.sideWAt = time.Now()
		c.After(600 * time.Millisecond)
	}
	if !a.sideWAt.IsZero() && time.Since(a.sideWAt) >= 500*time.Millisecond {
		a.sideWAt = time.Time{}
		w := float64(a.sideW / scale)
		if w != a.prefs.Appearance.SidebarWidth {
			a.updateSettings(func(s *settings.Settings) { s.Appearance.SidebarWidth = w })
		}
	}
}

// navRail is the sidebar as a column of icons; their titles show as
// tooltips, a badge as a dot. The foot's icons stand under the pages.
func (a *app) navRail(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	if bar := c.TitleBar(); bar.Left > 0 {
		// The traffic lights sit at the top of the rail: the button goes
		// under them, in a row of its own.
		ui.Box(c).Height(titleBarHeight).Shrink(0).DragWindow()
		ui.Row(c).Shrink(0).Justify(ui.Center).Children(func() {
			if iconButtonBelow(c.Key("rail-off"), icons.SidebarMinimalistic, i18n.T("Show the sidebar")).Clicked() {
				a.setRail(false)
			}
		})
	} else {
		ui.Row(c).Height(titleBarHeight).Shrink(0).DragWindow().AlignItems(ui.End).Justify(ui.Center).Padding(0, 0, px(t, sp1), 0).Children(func() {
			if iconButtonBelow(c.Key("rail-off"), icons.SidebarMinimalistic, i18n.T("Show the sidebar")).Clicked() {
				a.setRail(false)
			}
		})
	}
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).AlignItems(ui.Center).Gap(px(t, 2)).Padding(px(t, sp1), 0).Children(func() {
			a.railSearch(c) // shell_search.go
			ui.Box(c).Height(px(t, 4)).Shrink(0)
			for gi, g := range navGroups {
				if gi > 0 {
					ui.Box(c).Height(1).Width(px(t, 20)).Background(k.Line).Margin(px(t, 6), 0)
				}
				for _, p := range g.pages {
					a.railItem(c, p.path, p.icon, pageTitle(p.path))
				}
			}
			more := ui.Box(c.Key("rail-more-box")).Children(func() {
				a.railButton(c, "rail/more", icons.MenuDots, i18n.T("More"), i18n.T("More tools"), a.nav == "/more" || a.railMore, a.moreBadge(), func() { a.railMore = !a.railMore })
			})
			sidePopover(c, more, &a.railMore, func() { a.moreMenu(c, &a.railMore) })
		})
	})
	ui.Column(c).AlignItems(ui.Center).Gap(px(t, 2)).Padding(px(t, 6), 0, px(t, 10)).Children(func() {
		a.updateRailButton(c) // updates_view.go
		for _, p := range footPages {
			a.railItem(c, p.path, p.icon, pageTitle(p.path))
		}
		a.railButton(c, "rail/settings", icons.Settings, i18n.T("Settings"), tip(i18n.T("Settings"), "⌘,"), a.settingsOpen, "", func() { a.navigate("/settings") })
		a.tasksBell(c, true) // shell_bell.go
	})
}

// railItem is a page's icon in the rail.
func (a *app) railItem(c *ui.Context, path string, ic icons.Icon, title string) {
	tip := title
	if k := pageKeys(path); k != "" {
		tip += " (" + k + ")"
	}
	selected := !a.settingsOpen && a.nav == path
	a.railButton(c, "rail"+path, ic, title, tip, selected, a.navBadge(path), func() { a.navigate(path) })
}

// railButton is a button of the rail: Bold on the pressed face while
// selected, a dot for a badge.
func (a *app) railButton(c *ui.Context, key string, ic icons.Icon, label, tip string, selected bool, badge string, click func()) {
	t := c.Theme()
	k := tok(t)
	b := ui.ButtonBase(c.Key(key)).Size(px(t, 36), px(t, 32)).Radius(px(t, radiusControl)).Center().Tooltip(tip).Label(label)
	switch {
	case selected:
		b.Background(k.Pressed)
	case b.Hovered():
		b.Background(k.Hover)
	}
	b.Children(func() {
		col := k.Text2
		if selected {
			col = k.Text
		}
		ui.Icon(c, ic.Get(selected)).Size(px(t, 18), px(t, 18)).TextColor(col)
		if badge != "" {
			ui.Box(c).Absolute().Top(px(t, 4)).Right(px(t, 5)).Size(px(t, 7), px(t, 7)).Radius(px(t, 4)).Background(k.Accent)
		}
	})
	if b.Clicked() {
		click()
	}
}

// syncNav makes the sidebar follow the router (Back, deep links, ⌘1…),
// but not undo a choice in the sidebar the router has not seen yet; once
// the Settings dialog closes, its item gives the choice back to the page.
func (a *app) syncNav() {
	if p := navKey(a.router.Path()); p != a.navShown || (a.nav == "/settings" && !a.settingsOpen) {
		a.nav, a.navShown = p, p
	}
}

func (a *app) sidebar(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	ui.Row(c).Height(titleBarHeight).Shrink(0).DragWindow().AlignItems(ui.Center).Justify(ui.End).Padding(0, px(t, sp2), 0, 0).Children(func() {
		if iconButtonBelow(c.Key("rail-on"), icons.SidebarMinimalistic, i18n.T("Show icons only")).Clicked() {
			a.setRail(true)
		}
	})
	a.sideSearch(c) // shell_search.go
	// The sidebar's own theme: icons in the secondary grey, the chosen
	// page's in the text colour (MyGo draws a sidebar item's icon in the
	// accent, and the chosen one's in the accent's text colour).
	side := *t
	side.Accent, side.AccentText = k.Text2, k.Text
	side.SurfaceHover, side.SurfacePressed = k.G[2], k.G[3]
	chosen := side
	chosen.Accent = k.Text
	item := func(p navPage, badge string) ui.Element {
		// While Settings shows, its item is the chosen one.
		sel := p.path == a.nav && !a.settingsOpen || p.path == "/settings" && a.settingsOpen
		if sel {
			c.SetTheme(&chosen)
		} else {
			c.SetTheme(&side)
		}
		it := ui.SidebarItem(c, p.path, p.icon.Get(sel), pageTitle(p.path)).Padding(px(t, 5), px(t, sp2))
		c.SetTheme(t)
		if sel {
			it.Background(k.Pressed).FontWeight(600)
		} else {
			it.TextColor(k.G[8])
		}
		if keys := pageKeys(p.path); keys != "" {
			it.Tooltip(tip(pageTitle(p.path), keys))
		}
		if badge != "" {
			it.Children(func() { ui.Badge(c, badge) })
		}
		return it
	}
	// The list scrolls: a fade at its foot says there is more below.
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		ui.Sidebar(c, &a.nav, func() {
			for _, g := range navGroups {
				if g.id == "" { // the places: no title, they do not fold
					for _, p := range g.pages {
						item(p, a.navBadge(p.path))
					}
					continue
				}
				open := a.sectionOpen(g.id)
				ui.SidebarSection(c.Key("nav-"+g.id), groupTitle(g.id), open, func() {
					for _, p := range g.pages {
						item(p, a.navBadge(p.path))
					}
					// "··· More": a menu of the other tools.
					more := item(navPage{"/more", "More", icons.MenuDots}, a.moreBadge()).Tooltip(i18n.T("More tools"))
					sidePopover(c, more, &a.moreOpen, func() { a.moreMenu(c, &a.moreOpen) })
				}).Margin(px(t, 14), 0, 0, 0)
			}
		}).Grow(1).MinHeight(0).Background(ui.Transparent).Padding(0, px(t, 10), px(t, sp4)).Label(i18n.T("Sections")).OnChange(func() {
			a.navClicked()
		})
		ui.Box(c).Absolute().Left(0).Right(0).Bottom(0).Height(px(t, 20)).Gradient(k.Sidebar.Alpha(0), k.Sidebar, 180).PassThrough()
	})
	a.saveClosedSections()
	ui.Column(c).Shrink(0).Padding(px(t, sp1), 0, px(t, 6)).Gap(px(t, sp1)).Children(func() {
		ui.Column(c).Padding(0, px(t, 10)).Children(func() {
			a.updateEntry(c) // updates_view.go: an update to install, its download, the restart
		})
		ui.Sidebar(c.Key("nav-foot"), &a.nav, func() {
			for _, p := range footPages {
				item(p, "")
			}
			// Settings, and the tasks bell beside it.
			ui.Row(c.Key("nav-foot-settings")).Gap(px(t, 4)).AlignItems(ui.Center).Children(func() {
				s := item(navPage{"/settings", "Settings", icons.Settings}, "")
				s.Tooltip(tip(i18n.T("Settings"), "⌘,")).Grow(1).MinWidth(0)
				a.tasksBell(c, false) // shell_bell.go
			})
		}).Background(ui.Transparent).Padding(0, px(t, 10)).Label(i18n.T("Developers and Settings")).OnChange(func() {
			a.navClicked()
		})
	})
}

// navClicked follows a click in the sidebar: a page, More's menu, the
// Voices tab shown last, or Settings.
func (a *app) navClicked() {
	switch a.nav {
	case "/more":
		a.moreOpen = true
		a.nav = a.navShown // the page shown stays chosen until a tool is
	default:
		a.openPage(a.nav)
	}
}

// openPage opens a page the user picked by name (the sidebar, ⌘1–8, the
// Go menu, the palette): Voices opens the tab its rule chooses
// (voicesTabPath), where navigate's old-path alias always opens Mine.
func (a *app) openPage(path string) {
	if path == "/voices" {
		path = "/voices/" + a.voicesTabPath()
	}
	a.navigate(path)
}

// moreBadge is the badge of "··· More": its pages' (a running batch, a
// live call), the first there is.
func (a *app) moreBadge() string {
	for _, p := range morePages {
		if b := a.navBadge(p.path); b != "" {
			return b
		}
	}
	return ""
}

// moreMenu is "··· More"'s menu: the tools the sidebar does not list,
// each with its icon, title and what it does; open closes it.
func (a *app) moreMenu(c *ui.Context, open *bool) {
	t := c.Theme()
	k := tok(t)
	about := map[string]string{
		"/workflows": i18n.T("Chain steps: transcribe, translate, speak, export"),
		"/batch":     i18n.T("Dub many videos in one go"),
		"/calls":     i18n.T("Phone calls in a voice, through Twilio"),
	}
	ui.Column(c).Width(px(t, 280)).Gap(px(t, 2)).Padding(px(t, 4)).Role(ui.RoleList).Label(i18n.T("More tools")).Children(func() {
		ui.Text(c, i18n.T("More tools")).FontSize(px(t, fsMicro)).FontWeight(600).TextColor(k.Text3).Padding(px(t, 4), px(t, sp2))
		for _, p := range morePages {
			title := pageTitle(p.path)
			row := ui.ButtonBase(c.Key("more"+p.path)).Gap(px(t, sp3)).AlignItems(ui.Center).Padding(px(t, 7), px(t, sp2)).Radius(px(t, radiusControl)).Label(title)
			if row.Hovered() {
				row.Background(k.Hover)
			}
			row.Children(func() {
				ui.Box(c).Size(px(t, 30), px(t, 30)).Radius(px(t, radiusControl)).Border(1, k.Line).Center().Shrink(0).Children(func() {
					ui.Icon(c, p.icon.Linear).Size(px(t, 16), px(t, 16)).TextColor(k.Text)
				})
				ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 1)).Children(func() {
					ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
						ui.Text(c, title).FontWeight(600).SingleLine().Ellipsis("…").MinWidth(0)
						if b := a.navBadge(p.path); b != "" {
							ui.Badge(c, b)
						}
					})
					ui.Text(c, about[p.path]).FontSize(px(t, fsCaption)).TextColor(k.Text2).SingleLine().Ellipsis("…").MinWidth(0)
				})
			})
			if row.Clicked() {
				*open = false
				a.navigate(p.path)
			}
		}
	})
}

// unreadNotices is how many notifications are unread (the bell's dot).
func (a *app) unreadNotices() int {
	n := 0
	for _, x := range a.notices {
		if !x.Read {
			n++
		}
	}
	return n
}
