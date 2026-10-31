package main

// Settings → Integrations (0.5 F6): the apps and agents AudioSmith works
// with and the Local API they talk to are on the Developers page now
// (developers_view.go); this section points there, so the list is in one
// place. Its old paths (/settings/integrations/{slug}, /settings/api) go
// to Developers (shell_routes.go).

import (
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

func (a *app) integrationsSettings(c *ui.Context) {
	t := c.Theme()
	settingGroup(c, "", func() {
		settingBlock(c, "integ-moved", func() {
			ui.Row(c).Gap(px(t, sp3)).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icons.CodeSquare.Linear).Size(px(t, 20), px(t, 20)).TextColor(tok(t).Text2).Shrink(0)
				ui.Column(c).Gap(px(t, 2)).Grow(1).MinWidth(0).Children(func() {
					ui.Text(c, i18n.T("Integrations are on the Developers page")).FontWeight(600).Wrap()
					caption(c, i18n.T("Connect agents, MCP clients and scripts there: the setups, the Local API and its key.")).Wrap()
				})
				if primaryButton(c.Key("integ-open-dev"), i18n.T("Open Developers")).Clicked() {
					a.navigate("/developers")
				}
			})
		})
		settingRow(c, "integ-api", i18n.T("Local API"), i18n.T("Port, key, origins and sharing on the network."), func() {
			if ui.Button(c.Key("integ-open-api"), i18n.T("Open")).Clicked() {
				a.navigate("/developers/api")
			}
		})
	})
}
