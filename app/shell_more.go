package main

// The sidebar's 「··· 更多」 fold (user, 11-01): a click unfolds More's
// tools (Workflows, Batch dubbing, Calls) under it and the item reads
// 「收起」; a click on that folds them again. Opening one of them from
// elsewhere (⌘K, a link) unfolds it once. The icon rail keeps its menu.

import (
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// isMorePage reports whether the sidebar item path is one of More's.
func isMorePage(path string) bool {
	for _, p := range morePages {
		if p.path == path {
			return true
		}
	}
	return false
}

// moreFold builds More's tools while unfolded, then the fold's own item;
// item builds a page's sidebar item, side is the sidebar's theme.
func (a *app) moreFold(c *ui.Context, side *ui.Theme, item func(navPage, string) ui.Element) {
	t := c.Theme()
	k := tok(t)
	switch {
	case isMorePage(a.nav) && a.moreAutoFor != a.nav:
		a.moreExpanded, a.moreAutoFor = true, a.nav // opened from elsewhere
	case !isMorePage(a.nav):
		a.moreAutoFor = ""
	}
	if a.moreExpanded {
		for _, p := range morePages {
			item(p, a.navBadge(p.path))
		}
	}
	label, ic, tipText, badge := i18n.T("More"), icons.MenuDots, i18n.T("More tools"), a.moreBadge()
	if a.moreExpanded {
		label, ic, tipText, badge = i18n.T("Collapse"), icons.AltArrowUp, i18n.T("Fewer tools"), ""
	}
	c.SetTheme(side)
	it := ui.SidebarItem(c, "/more", ic.Linear, label).Padding(px(t, 5), px(t, sp2)).TextColor(k.G[8]).Tooltip(tipText)
	c.SetTheme(t)
	if badge != "" {
		it.Children(func() { ui.Badge(c, badge) })
	}
}
