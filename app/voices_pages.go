package main

// The voice pages in the shell (0.5 F1): Voices (/voices) is one page
// with two tabs, Discover (/voices/discover, the gallery's view) and Mine
// (/voices/mine, My voices' view); New voice (/voices/new) offers its two
// flows as cards, Quick clone (/voices/new/clone) and Design a voice
// (/voices/new/design), the clone and design views. The views are as they
// were (F4 redesigns them): the hosts give their headers the page's
// title and the tabs (headerHost, ui_layout.go). The old paths (/gallery,
// /voices, /clone, /design) go here (routeAlias, shell_routes.go).

import (
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// voicesTabs are the Voices page's tabs' paths, in order.
var voicesTabs = []string{"discover", "mine"}

// voicesTabPath is the tab the Voices page opens at from the sidebar: the
// one shown last in this session; at first Mine when the user has saved
// voices, Discover otherwise.
func (a *app) voicesTabPath() string {
	switch {
	case a.voicesTab != "":
		return a.voicesTab
	case len(a.voices) > 0:
		return "mine"
	}
	return "discover"
}

// voicesRoute builds the voice area's page for r and reports whether it
// was one.
func (a *app) voicesRoute(c *ui.Context, r *ui.Route) bool {
	switch {
	case r.Match("/voices/discover"):
		r.Title(i18n.X("nav", "Voices") + " · " + i18n.T("Discover"))
		a.voicesTabbed(c, "discover", a.galleryPage)
	case r.Match("/voices/mine"):
		r.Title(i18n.X("nav", "Voices") + " · " + i18n.T("Mine"))
		a.voicesTabbed(c, "mine", a.voicesPage)
	case r.Match("/voices/new"):
		r.Title(i18n.T("New voice"))
		a.newVoicePage(c)
	case r.Match("/voices/new/clone"):
		r.Title(i18n.T("Quick clone"))
		a.clonePage(c)
	case r.Match("/voices/new/design"):
		r.Title(i18n.T("Design a voice"))
		a.designPage(c)
	default:
		return false
	}
	return true
}

// voicePageTitle translates the titles of the area's pages.
func voicePageTitle(path string) (string, bool) {
	switch path {
	case "/voices":
		return i18n.X("nav", "Voices"), true
	case "/voices/new":
		return i18n.T("New voice"), true
	}
	return "", false
}

// voicesTabbed shows a tab's view under the Voices page's header: its
// title "Voices", and the tabs under it.
func (a *app) voicesTabbed(c *ui.Context, tab string, view func(*ui.Context)) {
	t := c.Theme()
	a.voicesTab = tab
	sel := 0
	if tab == "mine" {
		sel = 1
	}
	headerHost.title = i18n.X("nav", "Voices")
	headerHost.below = func() {
		ui.Row(c.Key("voices-tabs")).Padding(0, px(t, sp6)).Shrink(0).Children(func() {
			tabs := underlineTabs(c.Key("voices-tab"), &sel, i18n.T("Discover"), i18n.T("Mine")).Label(i18n.X("nav", "Voices")).Grow(1)
			if tabs.Changed() {
				a.navigate("/voices/" + voicesTabs[sel])
			}
		})
	}
	defer func() { headerHost.title, headerHost.below = "", nil }()
	view(c)
}

// newVoiceButton is Voices' primary button, 「+ 新建音色」: New voice's
// page (Clone, Design); its icon alone when the window is narrow.
func (a *app) newVoiceButton(c *ui.Context) func(narrow bool) {
	return func(narrow bool) {
		label := i18n.T("New voice")
		var b ui.Element
		if narrow {
			b = iconButton(c.Key("voices-new"), icons.AddCircle, label)
		} else {
			b = primaryIconButton(c.Key("voices-new"), icons.AddCircle, label)
		}
		if b.Clicked() {
			a.navigate("/voices/new")
		}
	}
}

// newVoicePage is New voice: the two ways to make a voice, as cards.
func (a *app) newVoicePage(c *ui.Context) {
	t := c.Theme()
	pageHeader(c, i18n.T("New voice"), func() {
		if ghostButton(c.Key("nv-mine"), i18n.T("My voices")+" ›").Clicked() {
			a.navigate("/voices/mine")
		}
	})
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Padding(px(t, sp1), px(t, sp6), px(t, sp6)).Gap(px(t, sp4)).MaxWidth(px(t, 860)).Children(func() {
			caption(c, i18n.T("Make a voice of your own: from a recording of someone, or from a description.")).Wrap()
			ui.Row(c.Key("nv-cards")).Gap(px(t, sp4)).Wrap().AlignItems(ui.Stretch).Children(func() {
				entryCard(c, "nv-clone", cloneIcon, i18n.T("Quick clone"),
					i18n.T("A voice from 10–30 seconds of someone speaking, recorded or from a file (with their consent)."), func() {
						if primaryButton(c.Key("nv-clone-go"), i18n.T("Clone a voice")).Clicked() {
							a.startClone("")
						}
					}).Grow(1).Basis(px(t, 260)).MinWidth(px(t, 220))
				entryCard(c, "nv-design", designIcon, i18n.T("Design a voice"),
					i18n.T("A voice from a description: gender, age, accent, style. Try it, then save the one you like."), func() {
						if ui.Button(c.Key("nv-design-go"), i18n.T("Design a voice")).Clicked() {
							a.navigate("/voices/new/design")
						}
					}).Grow(1).Basis(px(t, 260)).MinWidth(px(t, 220))
			})
			a.newVoiceTips(c)
		})
	})
}

// newVoiceTips: where the voices go, and the gallery for those who need
// none of their own.
func (a *app) newVoiceTips(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	ui.Row(c.Key("nv-discover")).Gap(px(t, sp2)).AlignItems(ui.Center).Padding(px(t, sp3)).Radius(px(t, radiusCard)).Border(1, k.Line).Children(func() {
		ui.Icon(c, icons.Widget.Linear).Size(px(t, 18), px(t, 18)).TextColor(k.Text2).Shrink(0)
		ui.Text(c, i18n.T("Or start from one of the ready voices in Discover.")).Grow(1).MinWidth(0)
		if ghostButton(c.Key("nv-discover-go"), i18n.T("Discover")+" ›").Shrink(0).Clicked() {
			a.navigate("/voices/discover")
		}
	})
}
