package services

// Code review 1103 (data safety): regression tests of its findings in
// the services (the PoCs of data-poc/ among them).

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

// L-1 (PoC TestCR1103DownloadDiskSpaceRecoded): a model download refused
// for disk space (hub code disk_space) was re-coded disk_full by the
// scheduler's classifier; hub.ErrorOf then no longer saw ErrDiskSpace
// (the Models page's message and link).
func TestDownloadDiskSpaceKeepsItsCode(t *testing.T) {
	if downloadDiskSpace != hub.CodeDiskSpace {
		t.Fatalf("downloadDiskSpace %q, hub's %q", downloadDiskSpace, hub.CodeDiskSpace)
	}
	msg := fmt.Errorf("a/b needs 4.3 GB more and keeps 6.0 GB free; 3.0 GB free: %w", hub.ErrDiskSpace).Error()
	je := &jobs.Error{Code: hub.CodeDiskSpace, Message: msg}
	got := DiskFullJob(je)
	if got == nil || got.Code != hub.CodeDiskSpace {
		t.Fatalf("classified %+v", got)
	}
	if !errors.Is(hub.ErrorOf(got), hub.ErrDiskSpace) {
		t.Error("hub.ErrorOf no longer is ErrDiskSpace")
	}
}

// L-7: an export kept a gigabyte free on any volume: 100 MB to a USB
// stick with 800 MB free were refused. A volume of its own keeps a
// hundredth of its size, at most a gigabyte.
func TestExportKeepsLessOnAStick(t *testing.T) {
	const gb = int64(1 << 30)
	for _, c := range []struct {
		system bool
		size   int64
		want   int64
	}{{true, 16 * gb, DiskKeepFree}, {false, 0, DiskKeepFree}, {false, 16 * gb, 16 * gb / 100}, {false, 2048 * gb, DiskKeepFree}} {
		if got := exportKeepOf(c.system, c.size); got != c.want {
			t.Errorf("system %v, size %d: keep %d, want %d", c.system, c.size, got, c.want)
		}
	}
	fakeFree(t, 800<<20)
	if err := checkFreeKeep("the export", t.TempDir(), 200<<20, exportKeepOf(false, 16*gb)); err != nil {
		t.Errorf("a stick: %v", err)
	}
	if exportKeep(t.TempDir()) != DiskKeepFree {
		t.Error("the temporary files' volume keeps less")
	}
	wantDiskFull(t, checkExportFree(t.TempDir(), 200<<20), 800<<20)
}

// L-3: a run deleted the renders of lines no longer in the dub; the
// editor's Undo brought the line back without its render, made again
// (another voice without a fixed seed). They stay until the dub is
// freed.
func TestDubRunKeepsRemovedLinesForUndo(t *testing.T) {
	e := newDubEnv(t, 4)
	p := e.create(t, nil)
	p = e.run(t, p.ID, RunRequest{Through: dub.StageMix})
	gone := p.Segments[2].ID
	snap := dub.Take(p, "")
	if _, err := e.dubs.Update(ctx, p.ID, func(q *dub.Project) error { q.Delete(gone); return nil }); err != nil {
		t.Fatal(err)
	}
	e.run(t, p.ID, RunRequest{Through: dub.StageMix})
	renders := func() []string {
		l, _ := filepath.Glob(e.dubs.Path(p.ID, filepath.Join("renders", "es", gone+"-*")))
		return l
	}
	if len(renders()) == 0 {
		t.Fatal("the run deleted the removed line's render")
	}
	if _, err := e.dubs.Update(ctx, p.ID, func(q *dub.Project) error { snap.Restore(q); return nil }); err != nil {
		t.Fatal(err)
	}
	synth := e.eng.Count(proto.MethodTTSSynthesize)
	e.run(t, p.ID, RunRequest{Through: dub.StageMix})
	if n := e.eng.Count(proto.MethodTTSSynthesize) - synth; n != 0 {
		t.Errorf("the line Undo restored was rendered again (%d renders)", n)
	}
	// Freeing the dub takes them.
	if _, err := e.dubs.Update(ctx, p.ID, func(q *dub.Project) error { q.Delete(gone); return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := e.dubs.FreeWork(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if l := renders(); len(l) != 0 {
		t.Errorf("freed, the removed line's render stayed: %v", l)
	}
}

// L-4: FreeIdle decided before FreeWork took the run lock: a dub opened
// meanwhile lost its audio and stems. It asks again under the lock.
func TestDubFreeIdleAsksAgain(t *testing.T) {
	e := newDubEnv(t, 2)
	p := e.create(t, nil)
	p = e.run(t, p.ID, RunRequest{Through: dub.StageMix})
	exported := func(string) time.Time { return time.Now() }
	asked := 0
	opened := func(string) bool { asked++; return asked > 1 } // opened after the first look
	if n, _ := e.dubs.FreeIdle(ctx, 0, exported, opened); n != 0 || asked < 2 {
		t.Fatalf("freed %d (asked %d times)", n, asked)
	}
	if !fileExists(e.dubs.Path(p.ID, dubAudio)) {
		t.Error("the dub opened during the sweep lost its audio")
	}
	if n, _ := e.dubs.FreeIdle(ctx, 0, exported, func(string) bool { return false }); n != 1 {
		t.Errorf("not freed once idle: %d", n)
	}
}

// L-5: Sweep looked for running isolations once, then deleted every
// audio.wav: an isolation started meanwhile lost its decoded input. Only
// those that ended a while ago lose it now.
func TestIsolationSweepLeavesLiveOnes(t *testing.T) {
	svc, _, _, _ := newIsolateTest(t)
	old := time.Now().Add(-time.Hour)
	isos := map[string]Isolation{
		"queued": {ID: "01Q", State: IsolateQueued, UpdatedAt: old}, "running": {ID: "01R", State: IsolateRunning, UpdatedAt: old},
		"failed": {ID: "01F", State: IsolateFailed, UpdatedAt: old}, "done": {ID: "01D", State: IsolateDone, UpdatedAt: old},
		"just failed": {ID: "01J", State: IsolateFailed, UpdatedAt: time.Now()},
	}
	for _, iso := range isos {
		os.MkdirAll(svc.Dir(iso.ID), 0o755)
		if err := svc.write(iso); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(svc.Path(iso.ID, IsolateAudio), []byte("x"), 0o644)
	}
	if n := svc.Sweep(ctx); n != 2 {
		t.Errorf("swept %d", n)
	}
	for name, iso := range isos {
		want := name != "failed" && name != "done"
		if got := fileExists(svc.Path(iso.ID, IsolateAudio)); got != want {
			t.Errorf("%s: audio.wav there %v, want %v", name, got, want)
		}
	}
}
