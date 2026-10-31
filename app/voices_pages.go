package main

// The voice pages in the shell (0.5 F1): Voices (/voices) is one page
// with two tabs, Discover (/voices/discover, the gallery's view) and Mine
// (/voices/mine, My voices' view); New voice (/voices/new) offers its two
// flows as cards, Quick clone (/voices/new/clone) and Design a voice
// (/voices/new/design), guided flows (clone_view.go, design_view.go; 0.5
// F4). Voices' host gives the tabs' headers the page's title and the
// tabs (headerHost, ui_layout.go). The old paths (/gallery,
// /voices, /clone, /design) go here (routeAlias, shell_routes.go).

import (
	"slices"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
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
// title "Voices", and the tabs (发现 | 我的) in the header row, after the
// count.
func (a *app) voicesTabbed(c *ui.Context, tab string, view func(*ui.Context)) {
	t := c.Theme()
	a.voicesTab = tab
	sel := 0
	if tab == "mine" {
		sel = 1
	}
	headerHost.title = i18n.X("nav", "Voices")
	headerHost.inline = func() {
		tabs := underlineTabs(c.Key("voices-tab"), &sel, i18n.T("Discover"), i18n.T("Mine")).Label(i18n.X("nav", "Voices")).BorderWidth(0, 0, 0, 0).Gap(px(t, sp4))
		if tabs.Changed() {
			a.navigate("/voices/" + voicesTabs[sel])
		}
	}
	defer func() { headerHost.title, headerHost.below, headerHost.inline = "", nil, nil }()
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

// newVoicePage is New voice (/voices/new): the two ways to make a
// voice as large cards — a title, two lines, what it needs, and a
// full-width button (Quick clone's the page's primary) — then the voices
// made last, with links to them.
func (a *app) newVoicePage(c *ui.Context) {
	t := c.Theme()
	pageHeader(c, i18n.T("New voice"), func() {
		if ghostButton(c.Key("nv-mine"), i18n.T("My voices")+" ›").Clicked() {
			a.navigate("/voices/mine")
		}
	})
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Padding(px(t, sp3), px(t, sp6), px(t, sp6)).Gap(px(t, sp5)).MaxWidth(px(t, 900)).Children(func() {
			ui.Row(c.Key("nv-cards")).Gap(px(t, sp4)).Wrap().AlignItems(ui.Stretch).Children(func() {
				flowCard(c, "nv-clone", cloneIcon, i18n.T("Quick clone"),
					i18n.T("Clone a voice from a recording of someone speaking: a file, or record it here. Use it only with their consent."),
					i18n.T("10 seconds or more of clear speech, 30 seconds is best"), func() {
						if primaryButton(c.Key("nv-clone-go"), i18n.T("Clone a voice")).FillWidth().Clicked() {
							a.startClone("")
						}
					})
				flowCard(c, "nv-design", designIcon, i18n.T("Design a voice"),
					i18n.T("Describe a voice — gender, age, accent, style — or start from a preset. Listen to a few candidates and keep the one you like."),
					i18n.T("A description or a preset; no recording needed"), func() {
						if ui.Button(c.Key("nv-design-go"), i18n.T("Start designing")).FillWidth().Clicked() {
							a.startDesign()
						}
					})
			})
			a.recentVoices(c)
			a.newVoiceTips(c)
		})
	})
}

// flowCard is a large card of New voice: ic, title, body, what the flow
// needs (a check mark and a line), and action (its full-width button) at
// its foot.
func flowCard(c *ui.Context, key string, ic icons.Icon, title, body, needs string, action func()) {
	t := c.Theme()
	k := tok(t)
	ui.Column(c.Key(key)).Padding(px(t, sp5)).Gap(px(t, sp3)).Radius(px(t, radiusCard)).Background(k.Subtle).
		Grow(1).Basis(px(t, 280)).MinWidth(px(t, 220)).Children(func() {
		ui.Box(c).Size(px(t, 40), px(t, 40)).Radius(px(t, radiusControl)).Background(k.Bg).Border(1, k.Line).Center().Children(func() {
			ui.Icon(c, ic.Linear).Size(px(t, 20), px(t, 20)).TextColor(k.Text)
		})
		ui.Column(c).Gap(px(t, 4)).Grow(1).Children(func() {
			ui.Text(c, title).FontSize(px(t, fsSection)).FontWeight(600).Role(ui.RoleHeading)
			ui.Text(c, body).FontSize(px(t, fsCaption)).TextColor(k.Text2)
		})
		ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.CheckCircle.Linear).Size(px(t, 14), px(t, 14)).TextColor(k.Text3).Shrink(0)
			ui.Text(c, needs).FontSize(px(t, fsCaption)).TextColor(k.Text2).MinWidth(0)
		})
		ui.Row(c).Padding(px(t, sp1), 0, 0).Children(action)
	})
}

// madeHere are the voices made in this app (cloned or designed, not
// saved from Discover or imported), newest first, at most n.
func (a *app) madeHere(n int) []*core.Voice {
	var out []*core.Voice
	for i := range a.voices {
		v := &a.voices[i]
		if v.Origin == "" && (v.Kind == core.VoiceCloned || v.Kind == core.VoiceDesigned) {
			out = append(out, v)
		}
	}
	slices.SortStableFunc(out, func(x, y *core.Voice) int { return y.CreatedAt.Compare(x.CreatedAt) })
	return out[:min(len(out), n)]
}

// recentVoices is 「最近做的音色」: a short list; a row opens the voice.
func (a *app) recentVoices(c *ui.Context) {
	t := c.Theme()
	vs := a.madeHere(5)
	if len(vs) == 0 {
		return
	}
	ui.Column(c.Key("nv-recent")).Gap(px(t, sp1)).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			sectionRow(c, "nv-recent-title", i18n.T("Voices you made lately"), "")
			ui.Spacer(c)
			if ghostButton(c.Key("nv-recent-all"), i18n.T("All my voices")+" ›").Clicked() {
				a.voiceKind = kindAll
				a.navigate("/voices/mine")
			}
		})
		for _, v := range vs {
			playing := a.savedPlaying(v)
			id := v.ID
			if voiceRow(c, "recent-"+v.ID, v.Name, voiceRowState{playing: playing}, func() {
				voiceNameCell(c, v.Name, mineKind(v)+" · "+i18n.Date(v.CreatedAt), a.portrait(v), playing, nil)
			}, func() {
				if previewToggle(c, "play", playing).Clicked() {
					a.previewSaved(v)
				}
				if ghostButton(c.Key("open-"+id), i18n.T("Open")+" ›").FontSize(px(t, fsCaption)).Clicked() {
					a.openVoiceEditor(id)
				}
			}) {
				a.openVoiceEditor(id)
			}
		}
	})
}

// newVoiceTips: the gallery, for those who need no voice of their own.
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
