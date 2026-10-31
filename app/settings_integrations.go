package main

// Settings → Integrations (the ActNow family's window layout): the apps
// and agents AudioSmith works with — their setups open in the section
// (/settings/integrations/{slug}, integrations_view.go) — then the Local
// API they talk to (api_settings.go). It was a page of the sidebar and a
// section "Local API" of its own.

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

func (a *app) integrationsSettings(c *ui.Context) {
	if slug, ok := strings.CutPrefix(a.setRouter.Path(), "/integrations/"); ok && slug != "" {
		a.integrationDetail(c, slug, "/settings/integrations")
		return
	}
	caption(c, i18n.T("Agents, SDKs and automation tools use AudioSmith through its Local API, on this Mac: pick one for its setup.")).Wrap()
	a.apiStatusLine(c)
	a.integrationList(c, "/settings/integrations")
	a.revealAnchor(c, "api")
	subHeading(c, i18n.T("Local API"))
	a.apiSettings(c) // its last group opens the API reference
}

// integrationList lists the apps and agents; a click opens the setup of
// one at base/{slug} (Settings → Integrations, or Developers).
func (a *app) integrationList(c *ui.Context, base string) {
	t := c.Theme()
	k := tok(t)
	settingGroup(c, i18n.T("Apps and agents"), func() {
		for _, in := range integrations {
			caps := make([]string, len(in.Caps))
			for i, cp := range in.Caps {
				caps[i] = integCaption(cp)
			}
			settingBlock(c, "integ-"+in.Slug, func() {
				b := ui.ButtonBase(c.Key("open")).Gap(px(t, sp3)).AlignItems(ui.Center).Padding(px(t, 2), 0).Radius(px(t, radiusControl)).Label(in.Name).Tooltip(strings.Join(caps, " · "))
				b.Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 2)).Children(func() {
						ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
							ui.Text(c, in.Name).FontWeight(600).SingleLine().Ellipsis("…").MinWidth(0)
							tagChip(c, integCaption(in.Kind))
						})
						caption(c, integAbout(in.Slug)).MaxLines(2).Ellipsis("…")
					})
					col := k.Text3
					if b.Hovered() {
						col = k.Text
					}
					ui.Icon(c, icons.AltArrowRight.Linear).Size(px(t, 14), px(t, 14)).TextColor(col).Shrink(0)
				})
				if b.Clicked() {
					if in.Slug == "twilio" { // M6b: its setup is the Calls page's
						a.navigate("/calls/setup")
						return
					}
					a.navigate(base + "/" + in.Slug)
				}
			})
		}
	})
}
