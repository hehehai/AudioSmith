package main

// Longform's projects: loading, saving, opening, deleting and starting
// new ones, and opening a project from a voice's usage list; split from
// longform.go.

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/services"
)

// loadLfProjects reads a page's saved projects.
func (a *app) loadLfProjects(p *lfPage) {
	svc := a.lib.svc.Projects
	kind := p.kind
	a.background(func() {
		l, err := svc.List(context.Background(), kind, "")
		if err != nil {
			return
		}
		for i := range l {
			l[i].Body = nil
		}
		a.post(func() { p.projects = l })
	})
}

// projectChanged updates the pages' project lists.
func (a *app) projectChanged(ev core.ProjectChanged) {
	a.projects.dirty = true
	for _, p := range []*lfPage{a.lfStories, a.lfBook} {
		if ev.Project.Kind != p.kind {
			continue
		}
		i := slices.IndexFunc(p.projects, func(x core.Project) bool { return x.ID == ev.Project.ID })
		switch {
		case ev.Deleted:
			if i >= 0 {
				p.projects = slices.Delete(p.projects, i, i+1)
			}
			if p.project == ev.Project.ID {
				p.project, p.savedJSON = "", ""
			}
		case i >= 0:
			pr := ev.Project
			pr.Body = nil
			p.projects[i] = pr
		default:
			pr := ev.Project
			pr.Body = nil
			p.projects = append([]core.Project{pr}, p.projects...)
		}
		if !ev.Deleted && p.project == ev.Project.ID && !p.unsaved() && len(ev.Project.Body) > 0 {
			// Changed elsewhere (an agent, thread #35) while open here
			// without changes of its own: show the saved one.
			var d services.LongformDoc
			if json.Unmarshal(ev.Project.Body, &d) == nil && d.Kind == p.kind && docJSON(&d) != p.savedJSON {
				render := p.render
				if d.Render != "" {
					render = d.Render
				}
				a.setLfDoc(p, d, ev.Project.ID, "")
				p.savedJSON, p.saveName, p.render = docJSON(&p.doc), ev.Project.Title, render
			}
		}
		if !ev.Deleted && p.project == ev.Project.ID && ev.Project.Title != p.saveName && p.saveName != "" {
			// Renamed elsewhere (the Projects page): the saved version
			// has the new title, and so does the page; what the page has
			// not saved stays unsaved (#101 M6).
			p.doc.Title, p.saveName = ev.Project.Title, ev.Project.Title
			p.savedJSON = retitled(p.savedJSON, ev.Project.Title)
		}
	}
}

// retitled is a saved document (docJSON) with another title; "" stays "".
func retitled(saved, title string) string {
	var d services.LongformDoc
	if saved == "" || json.Unmarshal([]byte(saved), &d) != nil {
		return saved
	}
	d.Title = title
	return docJSON(&d)
}

// sameAsSaved reports whether a stored document is the one a page saved
// or opened last (savedJSON), as SaveDoc stores it.
func sameAsSaved(saved string, cur services.LongformDoc) bool {
	var d services.LongformDoc
	if json.Unmarshal([]byte(saved), &d) != nil {
		return false
	}
	d.Version, cur.Version = services.LongformVersion, services.LongformVersion
	if d.Kind == "" {
		d.Kind = cur.Kind
	}
	return docJSON(&d) == docJSON(&cur)
}

// unsaved reports whether a page has changes its project lacks.
func (p *lfPage) unsaved() bool {
	if p.savedJSON == "" {
		return !p.empty()
	}
	return docJSON(&p.doc) != p.savedJSON
}

// empty reports whether a page has nothing worth keeping.
func (p *lfPage) empty() bool {
	return len(p.doc.Lines) == 0 && strings.TrimSpace(p.doc.Script) == "" && strings.TrimSpace(p.doc.ImportText) == ""
}

// saveLf saves a page as its project, or as a new one (copy). A project
// saved elsewhere since the page opened or saved it (an agent, thread
// #35) is not overwritten: the page asks (lfConfirm "changed", #101 M7).
func (a *app) saveLf(p *lfPage, copy bool) { a.saveLfAs(p, copy, false) }

// saveLfAs is saveLf; force overwrites a project changed elsewhere.
func (a *app) saveLfAs(p *lfPage, copy, force bool) { a.saveLfWith(p, copy, force, false) }

// saveLfWith is saveLfAs; quiet says nothing once saved (a new project
// from the composer, longform_entry.go).
func (a *app) saveLfWith(p *lfPage, copy, force, quiet bool) {
	// The title is the project's name (an older page may only have a
	// name to save under).
	name := strings.TrimSpace(p.doc.Title)
	if name == "" {
		name = strings.TrimSpace(p.saveName)
	}
	if name == "" {
		name = lfProjectName(p)
	}
	doc := p.doc
	if doc.Title == "" {
		doc.Title = name
	}
	pr := core.Project{ID: p.project, Kind: p.kind, Title: name}
	if copy {
		pr.ID = ""
	}
	var unchanged func(core.Project, services.LongformDoc) bool
	if base := p.savedJSON; !copy && !force && pr.ID != "" && base != "" {
		unchanged = func(_ core.Project, cur services.LongformDoc) bool { return sameAsSaved(base, cur) }
	}
	lib := a.lib.svc.Library
	a.background(func() {
		saved, err := lib.SaveDocIf(context.Background(), pr, doc, unchanged)
		a.post(func() {
			if errors.Is(err, services.ErrChanged) {
				p.confirm, p.confirmOpen = "changed", true
				return
			}
			if err != nil {
				a.toastWarn(i18n.T("Could not save: %v", err))
				return
			}
			p.project = saved.ID
			p.doc.Title = doc.Title
			p.saveName = saved.Title
			p.savedJSON = docJSON(&p.doc)
			a.projects.dirty = true
			if !quiet {
				a.toast(i18n.T("Saved “%s”", saved.Title))
			}
		})
	})
}

// openLfProject opens a saved project in its page (replacing the draft:
// callers ask first when it has unsaved changes).
func (a *app) openLfProject(id string) {
	lib := a.lib.svc.Library
	a.background(func() {
		pr, d, err := lib.LoadDoc(context.Background(), id)
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not open: %v", err))
				return
			}
			p := a.lfPageOf(pr.Kind)
			if a.activeRender() != nil && a.activeRender().rec.ProjectID == p.project && p.project != "" && p.project != pr.ID {
				a.toast(i18n.T("Longform work is active: stop the render first."))
				return
			}
			a.setLfDoc(p, d, pr.ID, "")
			p.savedJSON = docJSON(&p.doc)
			p.saveName = pr.Title
			p.render = d.Render
			p.opened, p.listing = true, false
			a.navigate("/" + lfRoute(pr.Kind))
		})
	})
}

// deleteLfProject deletes a saved project; its renders stay.
func (a *app) deleteLfProject(id string) {
	svc := a.lib.svc.Projects
	a.background(func() {
		err := svc.Delete(context.Background(), id)
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not delete: %v", err))
			} else {
				a.toast(i18n.T("Project deleted"))
			}
		})
	})
}

// newLf starts a new, empty document on a page.
func (a *app) newLf(p *lfPage) {
	model := p.doc.Model
	if model == "" {
		model = a.preset().Repo
	}
	d := services.NewLongformDoc(p.kind, model)
	d.Voice = p.doc.Voice
	d.Language = p.doc.Language
	a.setLfDoc(p, d, "", "")
}

// projectUsageLink is a project in a voice's "Where this voice has been
// used": it opens the project (voice_editor.go).
func (a *app) projectUsageLink(c *ui.Context, u core.VoiceProjectUsage) {
	t := c.Theme()
	label := u.Title + " · " + i18n.N(u.Segments, "%d segment", "%d segments", u.Segments)
	if u.Kind != core.ProjectStory && u.Kind != core.ProjectAudiobook {
		ui.Text(c, label).FontSize(px(t, 12))
		return
	}
	if ui.ButtonBase(c.Key("usage-" + u.ID)).Label(label).Children(func() {
		ui.Text(c, label).FontSize(px(t, 12)).TextColor(t.Accent).Grow(1).MinWidth(0)
	}).Clicked() {
		a.askOpenProject(u.ID, u.Kind, u.Title)
	}
}

// askOpenProject opens a project, asking first when its page has unsaved
// changes.
func (a *app) askOpenProject(id string, kind core.ProjectKind, title string) {
	p := a.lfPageOf(kind)
	p.listing = false
	if p.project == id {
		a.navigate("/" + lfRoute(kind))
		return
	}
	if p.unsaved() {
		// The page asks (lfConfirm), over the draft it would replace.
		a.navigate("/" + lfRoute(kind))
		p.confirm, p.confirmOpen = id, true
		if !slices.ContainsFunc(p.projects, func(x core.Project) bool { return x.ID == id }) {
			p.projects = append(p.projects, core.Project{ID: id, Kind: kind, Title: title})
		}
		return
	}
	a.openLfProject(id)
}
