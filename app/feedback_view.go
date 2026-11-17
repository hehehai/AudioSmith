package main

// Settings → Feedback: this Mac's feedback (the window's HistoryPanel,
// with the button opening the feedback window), the web form and the
// GitHub issue (feedback_window.go makes the window).

import (
	"github.com/egoist/mygo/ui"

	"github.com/hehehai/AudioSmith/internal/i18n"
)

// feedbackSection builds Settings → Feedback.
func (a *app) feedbackSection(c *ui.Context) {
	if a.fbWin != nil {
		settingGroup(c, i18n.T("Your feedback"), func() {
			settingBlock(c, "fb-panel", func() { a.fbWin.HistoryPanel(c) })
		})
	}
	settingGroup(c, i18n.T("Other ways"), func() { a.fbOtherWays(c) })
}

// fbOtherWays: the site's form, and an issue on GitHub (public).
func (a *app) fbOtherWays(c *ui.Context) {
	settingRow(c, "fb-web", i18n.T("Send feedback on the web"), "actnow.dev", func() {
		if ui.Button(c.Key("fb-web-open"), i18n.T("Open")).Clicked() {
			openExternal(a.fbWebURL())
		}
	})
	settingRow(c, "fb-github", i18n.T("Or open an issue on GitHub"), i18n.T("Public: anyone can read it. Settings → Advanced → Report a bug fills in the diagnostics."), func() {
		if ui.Button(c.Key("fb-github-open"), i18n.T("Open GitHub")).Clicked() {
			openExternal(issuesURL)
		}
	})
}
