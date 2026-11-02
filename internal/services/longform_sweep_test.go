package services

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/store"
)

// stoppedRender starts spec, holds it in its second chapter and stops it.
func (e *lfEnv) stoppedRender(t *testing.T, spec LongformSpec) core.Render {
	t.Helper()
	e.gate.hold.Store(true)
	r, err := e.svc.Longform.Start(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	<-e.gate.held
	e.svc.Longform.Stop(ctx, r.ID)
	r, _ = e.wait(t, r.ID)
	if r.State != core.RenderStopped {
		t.Fatalf("render %s: %s", r.ID, r.State)
	}
	return r
}

func holdSpec(project string) LongformSpec {
	spec := storySpec()
	spec.Project = project
	spec.Chapters[1].Lines[0].Spans[0].Text = "Morning HOLD over the water."
	return spec
}

// Code review 1102 H1 (PoC TestCR1102RendersOutliveProject): deleting a
// project deleted none of its renders; its stopped attempts stayed on
// disk with no project, listed nowhere.
func TestProjectDeleteTakesUnfinishedRenders(t *testing.T) {
	e := newLongformEnv(t, t.TempDir(), false)
	defer e.close()
	p, err := e.svc.Projects.Save(ctx, core.Project{Kind: core.ProjectStory, Title: "Lighthouse"})
	if err != nil {
		t.Fatal(err)
	}
	r1 := e.stoppedRender(t, holdSpec(p.ID))
	r2 := e.stoppedRender(t, holdSpec(p.ID))
	_, b1, _ := e.svc.Longform.Get(ctx, r1.ID)
	_, b2, _ := e.svc.Longform.Get(ctx, r2.ID)
	// A finished book of another project stays.
	e.gate.hold.Store(false)
	other := storySpec()
	other.Project = "other"
	done, err := e.svc.Longform.Start(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	e.wait(t, done.ID)
	if err := e.svc.Projects.Delete(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	l, _ := e.svc.Longform.List(ctx, store.RenderQuery{})
	if len(l) != 1 || l[0].ID != done.ID {
		t.Fatalf("renders after the delete: %+v", l)
	}
	for _, d := range []string{b1.Dir, b2.Dir} {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Errorf("%s stayed", d)
		}
	}
	// The chunk cache empties on demand.
	chunks := e.svc.Longform.ChunkDir()
	if ents, _ := os.ReadDir(chunks); len(ents) == 0 {
		t.Fatal("no chunks were made")
	}
	if n, err := e.svc.Longform.ClearChunks(ctx); err != nil || n == 0 {
		t.Fatalf("clear: %d %v", n, err)
	}
	if ents, _ := os.ReadDir(chunks); len(ents) != 0 {
		t.Errorf("%d chunks left", len(ents))
	}
}

// A finished book replaces its project's earlier unfinished attempts.
func TestDoneRenderDropsEarlierAttempts(t *testing.T) {
	e := newLongformEnv(t, t.TempDir(), false)
	defer e.close()
	p1, _ := e.svc.Projects.Save(ctx, core.Project{Kind: core.ProjectStory, Title: "One"})
	p2, _ := e.svc.Projects.Save(ctx, core.Project{Kind: core.ProjectStory, Title: "Two"})
	old := e.stoppedRender(t, holdSpec(p1.ID))
	keep := e.stoppedRender(t, holdSpec(p2.ID)) // another project's
	e.gate.hold.Store(false)
	spec := storySpec()
	spec.Project = p1.ID
	r, err := e.svc.Longform.Start(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if r, _ = e.wait(t, r.ID); r.State != core.RenderDone {
		t.Fatalf("state %s", r.State)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, _, err := e.svc.Longform.Get(ctx, old.ID)
		if errors.Is(err, core.ErrNotFound) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the earlier attempt stayed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, _, err := e.svc.Longform.Get(ctx, keep.ID); err != nil {
		t.Errorf("another project's render went: %v", err)
	}
}

// Sweep: folders no record names, and unfinished renders of no project
// a week old, go; a recent one, and a young folder, stay.
func TestLongformSweep(t *testing.T) {
	e := newLongformEnv(t, t.TempDir(), false)
	defer e.close()
	orphan := e.stoppedRender(t, holdSpec(""))
	recent := e.stoppedRender(t, holdSpec(""))
	rec, _ := e.st.Renders().Get(ctx, orphan.ID)
	rec.UpdatedAt = time.Now().Add(-8 * 24 * time.Hour)
	if err := e.st.Renders().Put(ctx, rec); err != nil {
		t.Fatal(err)
	}
	e.svc.Longform.forget(orphan.ID)
	root := filepath.Join(e.dir, "renders")
	stray, young := filepath.Join(root, "01STRAY"), filepath.Join(root, "01YOUNG")
	for _, d := range []string{stray, young} {
		os.MkdirAll(filepath.Join(d, "chapters"), 0o755)
		os.WriteFile(filepath.Join(d, "chapters", "001.wav"), make([]byte, 1000), 0o644)
	}
	past := time.Now().Add(-2 * time.Hour)
	os.Chtimes(stray, past, past)
	n, err := e.svc.Longform.Sweep(ctx)
	if err != nil || n != 2 {
		t.Fatalf("swept %d: %v", n, err)
	}
	if _, _, err := e.svc.Longform.Get(ctx, orphan.ID); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("the week-old orphan stayed: %v", err)
	}
	if _, _, err := e.svc.Longform.Get(ctx, recent.ID); err != nil {
		t.Errorf("the recent one went: %v", err)
	}
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Error("the stray folder stayed")
	}
	if _, err := os.Stat(young); err != nil {
		t.Error("a young folder went (a render being started)")
	}
}

// The chunk cache's cap is 5 % of a small disk.
func TestLongformChunkCap(t *testing.T) {
	if c := longformCapFor(t.TempDir()); c <= 0 || c > longformCap {
		t.Fatalf("cap %d", c)
	}
	if size := diskSize(t.TempDir()); size > 0 && longformCapFor(t.TempDir()) != min(int64(longformCap), size/20) {
		t.Errorf("cap %d of a %d disk", longformCapFor(t.TempDir()), size)
	}
}
