package main

// The M6 page in the shell: a sidebar section, the route, the title and
// the palette's entries (one call each from shell*.go and app.go).

import (
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/workflow"
)

// wfPageTitle translates the M6 page's title.
func wfPageTitle(path string) (string, bool) {
	if path == "/workflows" {
		return i18n.T("Workflows"), true
	}
	return "", false
}

// wfBadge is the sidebar badge of /workflows (M7a: the shell places the
// page, shell_pages.go navGroups): a running workflow's steps.
func (a *app) wfBadge(path string) (string, bool) {
	if path != "/workflows" {
		return "", false
	}
	if a.wfBusy() && a.wfRun != nil {
		done, _, total := a.wfRun.Counts()
		return itoa(done) + "/" + itoa(total), true
	}
	return "", true
}

// wfRoute builds /workflows.
func (a *app) wfRoute(c *ui.Context, r *ui.Route) bool {
	if !r.Match("/workflows") {
		return false
	}
	r.Title(i18n.T("Workflows"))
	a.workflowsPage(c)
	return true
}

// wfCommands adds the palette's entries of the M6 page.
func (a *app) wfCommands(nav string, add func(id, group, title, en, keys string, enabled bool, run func())) {
	// "Go to" comes from the shell's page list (M7a).
	group := i18n.T("Workflows")
	add("wf-new", group, i18n.T("New workflow"), "New workflow", "", true, func() {
		a.navigate("/workflows")
		a.wfNew("")
	})
	for _, tp := range workflow.Templates {
		id := tp.ID
		add("wf-template-"+id, group, i18n.T("New workflow: %s", workflow.TemplateName(id)), "New workflow: "+tp.Name, "", true, func() {
			a.navigate("/workflows")
			a.wfNew(id)
		})
	}
	add("wf-run", group, i18n.T("Run the workflow"), "Run the workflow", "", a.wf != nil && !a.wfBusy(), func() {
		a.navigate("/workflows")
		a.wfStart(false)
	})
	add("wf-cancel", group, i18n.T("Cancel the workflow's run"), "Cancel the workflow's run", "", a.wfBusy(), a.wfCancel)
}

// wfOpenPage opens a workflow on its page (the Projects page, deep links).
func (a *app) wfOpenPage(id string) {
	a.navigate("/workflows")
	if a.wf == nil || a.wf.ID != id {
		a.wfOpen(id)
	}
}
