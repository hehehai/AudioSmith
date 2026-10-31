package main

// The M5 pages in the shell: the API reference viewer's page, its route,
// title and the palette's entry (one call each from shell*.go). Tools are
// Developers' tab (developers_view.go), Integrations a Settings section
// (settings_integrations.go) and Developers' first tab.

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

var (
	toolsIcon = icons.RulerCrossPen
)

// m5PageTitle translates the M5 pages' titles.
func m5PageTitle(path string) (string, bool) {
	switch path {
	case "/api-reference":
		return i18n.T("API reference"), true
	}
	return "", false
}

// m5Route builds the M5 pages.
func (a *app) m5Route(c *ui.Context, r *ui.Route) bool {
	switch {
	case r.Match("/api-reference"):
		r.Title(i18n.T("API reference"))
		a.apiReferencePage(c)
	default:
		return false
	}
	return true
}

// m5Commands adds the palette's entries.
func (a *app) m5Commands(nav string, add func(id, group, title, en, keys string, enabled bool, run func())) {
	add("go/api-reference", nav, i18n.T("API reference"), "API reference OpenAPI docs", "", a.apiPort != 0, func() { a.navigate("/api-reference") })
}

// apiReferencePage shows the bundled reference viewer (RapiDoc, served
// by the API itself) in a web view.
func (a *app) apiReferencePage(c *ui.Context) {
	t := c.Theme()
	pageHeaderBar(c, pageHead{title: i18n.T("API reference"), parent: pageTitle("/developers"), up: "/developers", tools: func(bool) {
		if iconButton(c.Key("ref-browser"), icons.SquareArrowRightUp, i18n.T("Open in the browser")).Disabled(a.apiPort == 0).Clicked() {
			go mygo.Shell.OpenExternal(a.apiBaseURL() + "/docs")
		}
		if iconButton(c.Key("ref-settings"), icons.Settings, i18n.T("Settings…")).Clicked() {
			a.navigate("/settings/integrations")
		}
	}})
	if a.apiPort == 0 {
		emptyState(c, icons.Programming, i18n.T("The Local API is not running."), "", func() {
			if ui.Button(c.Key("ref-settings-empty"), i18n.T("Settings…")).Clicked() {
				a.navigate("/settings/integrations")
			}
		})
		return
	}
	if a.apiDocs == nil && a.win != nil && a.apiDocsErr == "" {
		theme := ""
		if t.Dark {
			theme = "?theme=dark"
		}
		v, err := a.win.NewWebView(mygo.WebViewOptions{URL: a.apiBaseURL() + "/docs" + theme})
		if err != nil {
			a.apiDocsErr = err.Error()
		}
		a.apiDocs = v
	}
	if a.apiDocs == nil {
		msg := i18n.T("The reference opens in a web view of the app window.")
		if a.apiDocsErr != "" {
			msg = i18n.T("The reference could not open here: %s", a.apiDocsErr)
		}
		emptyState(c, icons.Programming, msg, "", func() {
			ui.Text(c, a.apiBaseURL()+"/docs").Font(monoFont).FontSize(px(t, fsCaption)).Padding(px(t, 6), px(t, sp3)).Radius(px(t, radiusControl)).
				Background(tok(t).Subtle).Selectable()
		})
		return
	}
	ui.WebView(c, a.apiDocs).Grow(1)
}
