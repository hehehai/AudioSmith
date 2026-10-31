package main

// The M4 pages in the shell: a sidebar section (Dub, Batch), the routes,
// the titles, the palette's entries and Settings → Dubbing (one call
// each from shell*.go, app.go and main.go).

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/services"
)

// dubPageTitle translates the M4 pages' titles.
func dubPageTitle(path string) (string, bool) {
	switch path {
	case "/dub":
		return i18n.T("Dub"), true
	case "/batch":
		return i18n.T("Batch"), true
	}
	return "", false
}

// dubBadge is the sidebar badge of an M4 page (M7a: the shell places
// the pages, shell_pages.go navGroups): a running dub, the batch's videos.
func (a *app) dubBadge(path string) (string, bool) {
	n := 0
	switch path {
	case "/dub":
		if a.dubBusy() {
			n = 1
		}
	case "/batch":
		n = len(a.batch.jobs)
	default:
		return "", false
	}
	if n > 0 {
		return itoa(n), true
	}
	return "", true
}

// dubRoute builds the M4 pages.
func (a *app) dubRoute(c *ui.Context, r *ui.Route) bool {
	switch {
	case r.Match("/dub"):
		r.Title(i18n.T("Dub"))
		a.dubListPage(c) // dub_list.go (0.5 F2)
	case r.Match("/dub/{id}"):
		id := r.Param("id")
		r.Title(i18n.T("Dubbing") + " · " + a.dubTitleOf(id))
		a.dubLoad(id)
		if a.dub == nil || a.dub.ID != id {
			a.dubOpeningPage(c)
			return true
		}
		a.dubPage(c)
	case r.Match("/batch"):
		r.Title(i18n.T("Batch"))
		a.batchPage(c)
	default:
		return false
	}
	return true
}

// dubCommands adds the palette's entries of the M4 pages.
func (a *app) dubCommands(nav string, add func(id, group, title, en, keys string, enabled bool, run func())) {
	// The "Go to" entries come from the shell's page list (M7a).
	dubbing := i18n.T("Dubbing")
	add("dub-new", dubbing, i18n.T("Dub a video or audio file…"), "Dub a video or audio file…", "", true, func() {
		a.navigate("/dub")
		a.chooseDubSource()
	})
	add("dub-cancel", dubbing, i18n.T("Stop the dub being made"), "Stop the dub being made", "", a.dubBusy(), a.dubCancel)
}

// dubTitleOf is dub id's title, for the breadcrumb.
func (a *app) dubTitleOf(id string) string {
	if a.dub != nil && a.dub.ID == id {
		return firstOf(a.dub.Title, i18n.T("Untitled dub"))
	}
	for _, r := range a.dubList {
		if r.ID == id {
			return firstOf(r.Title, i18n.T("Untitled dub"))
		}
	}
	return i18n.T("Untitled dub")
}

// dubOpeningPage shows while a dub's editor reads it.
func (a *app) dubOpeningPage(c *ui.Context) {
	backRow(c, "dub-back", pageTitle("/dub"), func() { a.navigate("/dub") }, nil)
	ui.Column(c).Grow(1).Center().Gap(px(c.Theme(), sp2)).Children(func() {
		ui.Spinner(c)
		caption(c, i18n.T("Opening the dub…"))
	})
}

// dubSettingsSection is Settings → Dubbing's entry.
func dubSettingsSection() settingsSection {
	return settingsSection{"dubbing", i18n.T("Dubbing"), "Dubbing"}
}

// dubSettingsRoute builds Settings → Dubbing.
func (a *app) dubSettingsRoute(c *ui.Context, r *ui.Route) bool {
	if !r.Match("/dubbing") {
		return false
	}
	r.Title(i18n.T("Dubbing"))
	a.lastSettings = "dubbing"
	a.dubbingSettings(c)
	return true
}

// registerDubMedia serves the dub preview's media (before the window opens).
func (a *app) registerDubMedia() {
	mygo.Protocol.Handle(dubMediaScheme, dubMediaHandler(func() *services.Dubs { return a.dubSvc }))
}

// dubItemPhase is a dub's phase on the Projects page (the item's detail).
func dubItemPhase(detail string) string { return dubPhaseText(dub.Phase(detail)) }
