package main

// Developers (/developers, 0.5 F1): what agents, scripts and developers
// use, gathered from where it was, as it was: the apps and agents with
// their setups (MCP, Claude Code, Codex, SDKs: Settings → Integrations'
// list; /developers/{slug} a setup), the Local API's settings
// (/developers/api), and the tools (/developers/tools; /tools before: A/B voices, Convert, Rate fit, Direction, File
// probe, Watermark). The API reference opens from its header.

import (
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// developerTabs are the tabs' paths.
var developerTabs = []string{"/developers", "/developers/api", "/developers/tools"}

// developersPage is the page; slug ("" none) an integration's setup.
func (a *app) developersPage(c *ui.Context, slug string) {
	t := c.Theme()
	pageHeader(c, i18n.T("Developers"), func() {
		if ghostIconButton(c.Key("dev-reference"), icons.Programming, i18n.T("API reference")).Label(i18n.T("API reference")).Disabled(a.apiPort == 0).Clicked() {
			a.navigate("/api-reference")
		}
	})
	if slug == "" {
		ui.Row(c.Key("dev-tabs")).Padding(0, px(t, sp6)).Shrink(0).Children(func() {
			if underlineTabs(c.Key("dev-tab"), &a.devTab, i18n.T("Agents and MCP"), i18n.T("Local API"), i18n.T("Tools")).Label(i18n.T("Developers")).Grow(1).Changed() {
				a.navigate(developerTabs[a.devTab])
			}
		})
	}
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Padding(px(t, sp4), px(t, sp6), px(t, sp6)).Gap(px(t, sp4)).MaxWidth(px(t, 760)).Children(func() {
			if slug != "" {
				a.integrationDetail(c, slug, "/developers")
				return
			}
			switch a.devTab {
			case 0:
				caption(c, i18n.T("Agents, SDKs and automation tools use AudioSmith through its Local API, on this Mac: pick one for its setup.")).Wrap()
				a.apiStatusLine(c)
				a.integrationList(c, "/developers")
			case 1:
				a.apiSettings(c)
			default:
				a.toolsBody(c) // tools_view.go
			}
		})
	})
}
