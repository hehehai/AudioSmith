package main

// Developers (/developers, 0.5 F1; laid out in F6 after ElevenLabs'
// Developers overview): on top a card 「接入你的 Agent」 — a segmented
// 编程代理 | MCP | cURL, the snippet with Copy, and whether a client has
// called yet — then quick links (API reference, the skill, Local API
// settings) and the apps and agents with their setups
// (/developers/{slug}). The Local API's settings (/developers/api) and
// the tools (/developers/tools: A/B voices, Convert, Rate fit, Direction,
// File probe, Watermark) are the other tabs. Settings → Integrations
// points here.

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/api"
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
			if underlineTabs(c.Key("dev-tab"), &a.devTab, i18n.T("Overview"), i18n.T("Local API"), i18n.T("Tools")).Label(i18n.T("Developers")).Grow(1).Changed() {
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
				a.devConnectCard(c)
				a.devQuickLinks(c)
				a.integrationList(c, "/developers")
			case 1:
				a.apiSettings(c)
			default:
				a.toolsBody(c) // tools_view.go
			}
		})
	})
}

// Developers' snippets (the segmented control's order).
const (
	devAgent = iota // a prompt to paste into a coding agent
	devMCP          // the MCP server's configuration
	devCurl         // a first request with curl
)

// devSnippet is the snippet of the segment chosen, made from the live
// settings, and a line on what to do with it.
func (a *app) devSnippet(which int) (code, how string) {
	e := a.promptEnvOf()
	switch which {
	case devMCP:
		conf := obj{{"mcpServers", obj{{"audiosmith", obj{{"type", "http"}, {"url", e.mcpURL()},
			{"headers", e.headersObj("<your-agent>", "${"+integKeyEnv+"}")}}}}}}
		return jsonText(conf), i18n.T("Add this server to your MCP client's configuration (Claude Desktop, Cursor, any client).")
	case devCurl:
		return devCurlText(e.integEnv), i18n.T("Run it in Terminal: AudioSmith speaks the line with its default voice and saves hello.wav.")
	}
	return agentPrompt("mcp", e), i18n.T("Paste this into your coding agent (Claude Code, Codex, Cursor…): it connects AudioSmith and installs its skill.")
}

// devCurlText is a first request: speech from a line, with curl.
func devCurlText(e integEnv) string {
	var b strings.Builder
	fmt.Fprintf(&b, "curl %s/v1/audio/speech \\\n", e.Base)
	fmt.Fprintf(&b, "  -H \"Content-Type: application/json\" \\\n  -H \"%s: curl\" \\\n", api.HeaderClient)
	if e.auth() {
		fmt.Fprintf(&b, "  -H \"Authorization: Bearer %s\" \\\n", e.keyRef("$"+integKeyEnv))
	}
	fmt.Fprintf(&b, "  -d '{\"input\": %q, \"response_format\": \"wav\"}' \\\n  -o hello.wav", i18n.T("Hello from AudioSmith."))
	return b.String()
}

// devConnectCard is 「接入你的 Agent」: the segments, the snippet with
// Copy, and the API's state.
func (a *app) devConnectCard(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	code, how := a.devSnippet(a.devSeg)
	card(c.Key("dev-connect")).Children(func() {
		ui.Row(c).Gap(px(t, sp3)).AlignItems(ui.Center).Wrap().Children(func() {
			ui.Column(c).Gap(px(t, 2)).Grow(1).MinWidth(px(t, 200)).Children(func() {
				sectionHeading(c, i18n.T("Connect your agent"))
				caption(c, i18n.T("AudioSmith serves MCP and a local API on this Mac: agents, scripts and apps speak, transcribe and dub through it.")).Wrap()
			})
			segmentedFit(c.Key("dev-seg"), &a.devSeg, i18n.T("Coding agent"), "MCP", "cURL").Label(i18n.T("Connect with"))
		})
		caption(c, how).Wrap()
		_, h := c.Size()
		ui.Row(c.Key("dev-code")).AlignItems(ui.Start).Gap(px(t, sp1)).Radius(px(t, radiusControl)).Background(k.Subtle).Children(func() {
			ui.Scroll(c.Key("dev-code-scroll")).Grow(1).MinWidth(0).MaxHeight(max(px(t, 96), min(px(t, 220), h*0.3))).Children(func() {
				ui.Text(c, code).Font(monoFont).FontSize(px(t, fsCaption)).Wrap().Selectable().Padding(px(t, sp3))
			})
			ui.Box(c).Padding(px(t, 6)).Shrink(0).Children(func() {
				if secondaryIconButton(c.Key("dev-copy"), icons.Copy, i18n.T("Copy")).Label(i18n.T("Copy")).Clicked() {
					c.Services().WriteClipboard(code)
					a.toast(i18n.T("Copied"))
				}
			})
		})
		a.devStatusLine(c)
	})
}

// devStatusLine is whether a client has called: waiting, or when last.
func (a *app) devStatusLine(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	last := a.apiLastRequest()
	ui.Row(c.Key("dev-status")).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
		switch {
		case !a.prefs.API.Enabled:
			statusDot(c, t.Warning)
			truncText(c, i18n.T("The Local API is off: turn it on under Local API.")).FontSize(px(t, fsCaption)).Grow(1)
			if ghostButton(c.Key("dev-api-on"), i18n.T("Local API")).Clicked() {
				a.navigate("/developers/api")
			}
		case a.apiErr != "":
			statusDot(c, t.Danger)
			truncText(c, a.apiErr).FontSize(px(t, fsCaption)).TextColor(t.Danger).Grow(1)
		case last.IsZero():
			ui.Spinner(c)
			truncText(c, i18n.T("Waiting for the first request…")).FontSize(px(t, fsCaption)).TextColor(k.Text2).Grow(1)
			truncText(c, a.apiBaseURL()).FontSize(px(t, fsCaption)).TextColor(k.Text3).Shrink(1)
		default:
			statusDot(c, t.Success)
			truncText(c, i18n.T("Last request: %s", ago(last))).FontSize(px(t, fsCaption)).Grow(1)
			truncText(c, a.apiBaseURL()).FontSize(px(t, fsCaption)).TextColor(k.Text3).Shrink(1)
			c.After(30 * time.Second) // "just now" ages
		}
	})
}

// devQuickLinks are the links under the card.
func (a *app) devQuickLinks(c *ui.Context) {
	t := c.Theme()
	link := func(key string, ic icons.Icon, label string, off bool, go_ func()) {
		if secondaryIconButton(c.Key(key), ic, label).Label(label).Disabled(off).Clicked() {
			go_()
		}
	}
	ui.Row(c.Key("dev-links")).Gap(px(t, sp2)).Wrap().Children(func() {
		link("dev-link-ref", icons.Programming, i18n.T("API reference"), a.apiPort == 0, func() { a.navigate("/api-reference") })
		link("dev-link-skill", icons.DownloadMinimalistic, i18n.T("Install the skill"), false, func() { a.navigate("/developers/claude-code") })
		link("dev-link-api", icons.Settings, i18n.T("Local API settings"), false, func() { a.navigate("/developers/api") })
	})
}
