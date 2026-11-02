package main

// Thread #143: the Stories and Audiobook editors' ⋯ deletes the project
// open (or discards a draft never saved), asked first and off while a
// render runs; an empty project deletes from its row; Clear works
// whenever there is something to clear; a draft saved on the way to a
// sample shows in Projects.

import (
	"context"
	"strings"
	"testing"

	"github.com/hehehai/AudioSmith/assets"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/services"
)

// saveEmptyProject saves a project with a title and nothing else.
func saveEmptyProject(t *testing.T, a *app, kind core.ProjectKind, title string) core.Project {
	t.Helper()
	d := services.NewLongformDoc(kind, kokoro)
	d.Title = title
	pr, err := a.lib.svc.Library.SaveDoc(context.Background(), core.Project{Kind: kind, Title: title}, d)
	if err != nil {
		t.Fatal(err)
	}
	return pr
}

func TestEditorDeletesSavedProject(t *testing.T) {
	for _, kind := range []core.ProjectKind{core.ProjectStory, core.ProjectAudiobook} {
		t.Run(string(kind), func(t *testing.T) {
			a, _, tt := newTestApp(t)
			tt.SetSize(1300, 1000)
			pr := saveEmptyProject(t, a, kind, "Empty one") // nothing in it: still deletable
			a.openLfProject(pr.ID)
			settle(tt)
			p := a.lfPageOf(kind)
			if p.project != pr.ID || !a.lfEditing(p) {
				t.Fatalf("not open: %q", p.project)
			}
			chooseAway(t, tt, "More actions", "Delete project")
			if !p.confirmOpen || !tt.HasText("Delete this project permanently?") {
				t.Fatalf("no question: %q", tt.Texts())
			}
			if _, err := a.lib.svc.Projects.Get(context.Background(), pr.ID); err != nil {
				t.Fatal("deleted before the answer")
			}
			if err := tt.Click("Delete"); err != nil {
				t.Fatal(err)
			}
			settle(tt)
			if _, err := a.lib.svc.Projects.Get(context.Background(), pr.ID); err == nil {
				t.Fatal("not deleted")
			}
			if a.lfEditing(p) || p.project != "" || tt.HasText("Empty one") {
				t.Fatalf("still open or listed: editing %v %q", a.lfEditing(p), tt.Texts())
			}
		})
	}
}

func TestEditorDiscardsDraft(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.SetSize(1300, 1000)
	p := a.lfStories
	p.doc.Title = "Only a title" // nearly empty: no lines
	p.opened = true
	a.navigate("/stories")
	settle(tt)
	if !a.lfEditing(p) {
		t.Fatal("no editor")
	}
	chooseAway(t, tt, "More actions", "Discard draft")
	if !p.confirmOpen || !tt.HasText("Discard this draft?") {
		t.Fatalf("no question: %q", tt.Texts())
	}
	if err := tt.Click("Discard"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if a.lfEditing(p) || p.doc.Title != "" || tt.HasText("Only a title") {
		t.Fatalf("draft left: editing %v title %q", a.lfEditing(p), p.doc.Title)
	}
	if items := a.projItems(projStory, ""); len(items) != 0 {
		t.Errorf("listed: %+v", items)
	}
}

// While a render runs, Delete project is off and says why; deleting the
// page's draft or project elsewhere says why too.
func TestDeleteWhileRendering(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.SetSize(1300, 1000)
	pr := saveEmptyProject(t, a, core.ProjectAudiobook, "Busy book")
	a.openLfProject(pr.ID)
	settle(tt)
	p := a.lfBook
	p.render = "R1"
	a.lfRenders["R1"] = &lfRender{rec: core.Render{ID: "R1", ProjectID: pr.ID, Kind: core.ProjectAudiobook, State: core.RenderRunning}}
	tt.Frame()
	if err := tt.Click("More actions"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(tt.Menu(), "|"), "Delete project (stop the render first)") {
		t.Fatalf("menu: %q", tt.Menu())
	}
	tt.CloseMenu()
	a.toasts = nil
	a.deleteProj(projItem{kind: projBook, id: pr.ID, title: "Busy book"})
	if _, err := a.lib.svc.Projects.Get(context.Background(), pr.ID); err != nil {
		t.Fatal("deleted while rendering")
	}
	if !strings.Contains(strings.Join(a.toasts, "|"), "stop the render first") {
		t.Errorf("no reason: %q", a.toasts)
	}
	a.toasts = nil
	a.deleteProj(projItem{kind: projBook, id: "draft", draft: true})
	if !strings.Contains(strings.Join(a.toasts, "|"), "stop the render first") {
		t.Errorf("draft: no reason: %q", a.toasts)
	}
}

// An empty saved project is deleted from its row's ⋯ on the Stories list.
func TestEmptyProjectDeletesFromRow(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.SetSize(1300, 1000)
	pr := saveEmptyProject(t, a, core.ProjectStory, "Blank story")
	a.navigate("/stories")
	settle(tt)
	if !tt.HasText("Blank story") {
		t.Fatalf("not listed: %q", tt.Texts())
	}
	chooseAway(t, tt, "Project actions", "Delete")
	if err := tt.Click("Delete"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if _, err := a.lib.svc.Projects.Get(context.Background(), pr.ID); err == nil || tt.HasText("Blank story") {
		t.Fatalf("not deleted: %q", tt.Texts())
	}
}

// Clear is on whenever there is something to clear: lines without text,
// a blank manuscript; not for a title alone.
func TestClearableWhenAnythingToClear(t *testing.T) {
	p := &lfPage{kind: core.ProjectStory}
	p.doc.Title = "Title only"
	if p.clearable() {
		t.Error("a title alone")
	}
	p.doc.Lines = []services.StoryLine{{ID: "l0"}}
	if !p.clearable() {
		t.Error("an empty line")
	}
	b := &lfPage{kind: core.ProjectAudiobook}
	b.doc.Script = "  \n"
	if !b.clearable() {
		t.Error("a blank manuscript")
	}
}

// A draft saved on the way to a sample (lfStash) shows in Projects even
// when the list was read before (the user's 「未命名有声书」).
func TestStashedDraftShowsInProjects(t *testing.T) {
	useSamples(t, mustSub(assets.Samples, "samples"))
	a, _, tt := newTestApp(t)
	tt.SetSize(1300, 1000)
	saveEmptyProject(t, a, core.ProjectAudiobook, "Sample book")
	a.navigate("/projects")
	settle(tt)
	if !a.projects.loaded {
		t.Fatal("not read")
	}
	p := a.lfBook
	a.setManuscript(p, "A draft never saved.")
	a.navigate("/home")
	a.openSample("chapter") // the bundled audiobook sample: the draft is saved first
	settle(tt)
	if a.lfBook.project == "" || strings.Contains(a.lfBook.doc.Script, "never saved") {
		t.Fatalf("sample not open: %q", a.lfBook.project)
	}
	a.navigate("/projects")
	settle(tt)
	if !tt.HasText("Untitled audiobook") || !tt.HasText("Sample book") {
		t.Fatalf("Projects: %q", tt.Texts())
	}
}
