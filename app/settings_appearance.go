package main

// Settings → Appearance: the sidebar's layout (M7a). (0.3 replaced M7a's
// accent palettes with one accent: ui_tokens.go.)

import (
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
)

// sidebarRow chooses the sidebar's layout.
func (a *app) sidebarRow(c *ui.Context) {
	rail := 0
	if a.prefs.Appearance.Rail {
		rail = 1
	}
	settingRow(c, "sidebar-style", i18n.T("Sidebar"), i18n.T("Icons only leaves the pages more room; names show on pointing."), func() {
		if segmented(c.Key("sidebar-seg"), &rail, i18n.T("Names and icons"), i18n.T("Icons only")).Label(i18n.T("Sidebar")).Changed() {
			a.setRail(rail == 1)
		}
	})
}
