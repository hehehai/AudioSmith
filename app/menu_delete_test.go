package main

// The other Delete… menus (#108): each still reports its choice when the
// pointer left the button for the menu (macOS's native menu), asks, and
// deletes, and the operations log says so.

import (
	"context"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/store"
)

func TestDeleteMenusHistoryAndProjects(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.SetSize(1300, 1000)
	at := time.Date(2026, 10, 13, 9, 0, 0, 0, time.Local)
	addTake(t, a, core.Take{ID: "T1", CreatedAt: at, Text: "First take", Model: kokoro, Voice: "af_heart", AudioSeconds: 1}, 0.3)
	addTake(t, a, core.Take{ID: "T2", CreatedAt: at.Add(time.Minute), Text: "Second take", Model: kokoro, Voice: "af_heart", AudioSeconds: 1}, 0.3)

	// Assets: Delete deletes at once (the newest take's menu is first).
	a.showPage("history")
	tt.Frame()
	chooseAway(t, tt, "Asset actions", "Delete")
	if a.findTake("T2") != nil || a.findTake("T1") == nil {
		t.Fatalf("history delete: %v", a.takes)
	}
	if !hasOp(a, "take.delete", "id=T2") {
		t.Errorf("not logged: %q", opLines(a))
	}

	// Projects: Delete… asks first.
	d := services.NewLongformDoc(core.ProjectStory, kokoro)
	d.Title = "Old story"
	pr, err := a.lib.svc.Library.SaveDoc(context.Background(), core.Project{}, d)
	if err != nil {
		t.Fatal(err)
	}
	a.navigate("/projects")
	tt.Frame()
	tt.Frame()
	chooseAway(t, tt, "Project actions", "Delete…")
	if !a.projects.confirmOpen {
		t.Fatalf("projects: no confirmation: %q", tt.Texts())
	}
	if err := tt.Click("Delete"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, err := a.lib.svc.Projects.Get(context.Background(), pr.ID); err == nil {
		t.Fatal("projects delete")
	}
	if !hasOp(a, "story.delete", "id="+pr.ID) {
		t.Errorf("not logged: %q", opLines(a))
	}
}

func TestDeleteMenuTranscript(t *testing.T) {
	a, _, tt := newTestApp(t)
	tr := putTranscript(t, a, sampleTranscript(core.NewID(time.Now()), time.Now()))
	a.navigate("/transcripts")
	a.selectTranscript(tr.ID)
	tt.Frame()
	tt.Frame()
	chooseAway(t, tt, "Transcript actions", "Delete…")
	tt.Frame()
	if err := tt.Click("Delete"); err != nil {
		t.Fatalf("%v: %q", err, tt.Texts())
	}
	tt.Frame()
	if len(a.trList) != 0 {
		t.Fatalf("transcript delete: %d", len(a.trList))
	}
	if !hasOp(a, "transcript.delete", "id="+tr.ID, "name=interview.m4a") {
		t.Errorf("not logged: %q", opLines(a))
	}
}

func TestDeleteMenuWorkflow(t *testing.T) {
	d := newWfTest(t)
	a, tt := d.a, d.tt
	a.wfNew("")
	tt.Frame()
	id := a.wf.ID
	if !hasOp(a, "workflow.create", "id="+id) {
		t.Errorf("create not logged: %q", opLines(a))
	}
	chooseAway(t, tt, "More actions", "Delete")
	if !a.wfAsk || a.wfConfirm != id {
		t.Fatalf("no confirmation: %q", tt.Texts())
	}
	d.click(t, "Delete")
	if recs, _ := a.wfSvc.List(context.Background()); len(recs) != 0 {
		t.Fatalf("workflow delete: %+v", recs)
	}
	if !hasOp(a, "workflow.delete", "id="+id) {
		t.Errorf("not logged: %q", opLines(a))
	}
}

func TestDeleteMenusDubAndBatch(t *testing.T) {
	d := newDubTest(t, 3)
	a, tt := d.a, d.tt
	tt.Frame()
	a.dubSourceFile(d.src)
	tt.Frame()
	if a.dub == nil {
		t.Fatalf("no dub: %q", tt.Texts())
	}
	id := a.dub.ID
	if !hasOp(a, "dub.create", "id="+id) {
		t.Errorf("create not logged: %q", opLines(a))
	}
	chooseAway(t, tt, "More actions", "Delete…")
	if !a.dubConfirmOpen {
		t.Fatalf("dub: no confirmation: %q", tt.Texts())
	}
	d.click(t, "Delete")
	if _, err := a.dubSvc.Get(context.Background(), id); err == nil {
		t.Fatal("dub delete")
	}
	if !hasOp(a, "dub.delete", "id="+id) {
		t.Errorf("not logged: %q", opLines(a))
	}

	// Batch: a queued video's Dub actions → Delete….
	a.navigate("/batch")
	tt.Frame()
	a.batchDropped([]string{d.src})
	d.click(t, "Add to queue")
	a.loadBatch()
	tt.Frame()
	if len(a.batch.items) != 1 {
		t.Fatalf("batch: %d items", len(a.batch.items))
	}
	bid := a.batch.items[0].ID
	if a.batchStatus(bid) == "done" {
		a.batch.tab = 1
		tt.Frame()
	}
	chooseAway(t, tt, "Dub actions", "Delete…")
	if a.batch.confirm != bid {
		t.Fatalf("batch: no confirmation: %q", tt.Texts())
	}
	d.click(t, "Delete")
	if recs, _ := a.dubSvc.List(context.Background(), store.DubQuery{AllBatches: true}); len(recs) != 0 {
		t.Fatalf("batch delete: %+v", recs)
	}
	if !hasOp(a, "dub.delete", "id="+bid) {
		t.Errorf("not logged: %q", opLines(a))
	}
}
