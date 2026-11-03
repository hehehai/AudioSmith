package sqlitestore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/jobs"
)

// TestPruneJobs: old finished jobs go, interrupted ones too (code review
// 1102 L2), but not the unfinished, the recent, the model downloads, the
// latest job naming each custom model, or an interrupted job unfinished
// ones wait on.
func TestPruneJobs(t *testing.T) {
	s := openTest(t, t.TempDir())
	ctx := context.Background()
	js := s.Jobs()
	old := time.Now().Add(-40 * 24 * time.Hour)
	recent := time.Now().Add(-time.Hour)
	add := func(id, kind string, state jobs.State, finished time.Time, params string) {
		t.Helper()
		j := jobs.Job{ID: id, Spec: jobs.Spec{Kind: kind, Lane: jobs.LaneGPU, Params: json.RawMessage(params)}, State: state, Created: finished.Add(-time.Minute), Finished: finished}
		if err := js.SaveJob(ctx, j); err != nil {
			t.Fatal(err)
		}
	}
	add("old-tts", "tts.synthesize", jobs.StateSucceeded, old, `{"model":"known/repo","text":"long"}`)
	add("older-custom", "tts.synthesize", jobs.StateFailed, old.Add(-time.Hour), `{"model":"someone/custom"}`)
	add("old-interrupted", "dub.ingest", jobs.StateInterrupted, old, `{}`)
	add("old-blocking", "dub.fetch", jobs.StateInterrupted, old, `{}`)
	if err := js.SaveJob(ctx, jobs.Job{ID: "blocked", Spec: jobs.Spec{Kind: "dub.ingest", Lane: jobs.LaneGPU, After: []string{"old-blocking"}},
		State: jobs.StateWaiting, Created: old}); err != nil {
		t.Fatal(err)
	}
	add("old-failed", "longform.chapter", jobs.StateFailed, old, `{"lines":["a"]}`)
	add("old-custom", "tts.synthesize", jobs.StateSucceeded, old, `{"model":"someone/custom"}`)
	add("old-download", "model.download", jobs.StateSucceeded, old, `{"repo":"someone/custom"}`)
	add("old-array", "x.y", jobs.StateCancelled, old, `[1,2]`)
	add("recent", "tts.synthesize", jobs.StateSucceeded, recent, `{"model":"known/repo"}`)
	add("queued", "tts.synthesize", jobs.StateQueued, time.Time{}, `{}`)
	n, err := s.PruneJobs(ctx, time.Now().Add(-30*24*time.Hour), []string{"model.download"}, []string{"known/repo"})
	if err != nil {
		t.Fatal(err)
	}
	all, err := js.LoadJobs(ctx, jobs.Filter{States: []jobs.State{}})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, j := range all {
		ids = append(ids, j.ID)
	}
	slices.Sort(ids)
	if want := []string{"blocked", "old-blocking", "old-custom", "old-download", "queued", "recent"}; n != 5 || !slices.Equal(ids, want) {
		t.Fatalf("removed %d, left %v, want %v", n, ids, want)
	}
}

// TestMaintain: the free pages deleted rows leave go back to the disk,
// in a file made with incremental vacuum and in an older one (rebuilt
// once), and the WAL is truncated.
func TestMaintain(t *testing.T) {
	pause = 0
	for _, old := range []bool{false, true} {
		dir := t.TempDir()
		s := openTest(t, dir)
		ctx := context.Background()
		if old { // a file from before: no auto_vacuum
			for _, q := range []string{"PRAGMA auto_vacuum = NONE", "VACUUM"} {
				if _, err := s.db.Execute(ctx, q); err != nil {
					t.Fatal(err)
				}
			}
		}
		st, _ := s.pages(ctx)
		if want := map[bool]int64{false: 2, true: 0}[old]; st.autoVacuum != want {
			t.Fatalf("old %v: auto_vacuum %d, want %d", old, st.autoVacuum, want)
		}
		js := s.Jobs()
		big := `{"text":"` + strings.Repeat("long text ", 2000) + `"}`
		for i := range 600 { // ~12 MB of params, as a book's chapter jobs
			j := jobs.Job{ID: fmt.Sprintf("j%d", i), Spec: jobs.Spec{Kind: "longform.chapter", Lane: jobs.LaneGPU, Params: json.RawMessage(big)},
				State: jobs.StateSucceeded, Created: time.Now().Add(-50 * 24 * time.Hour), Finished: time.Now().Add(-40 * 24 * time.Hour)}
			if err := js.SaveJob(ctx, j); err != nil {
				t.Fatal(err)
			}
		}
		if n, err := s.PruneJobs(ctx, time.Now().Add(-30*24*time.Hour), nil, nil); err != nil || n != 600 {
			t.Fatalf("pruned %d: %v", n, err)
		}
		pre, _ := s.pages(ctx)
		before := pre.count * pre.size
		m, err := s.Maintain(ctx)
		if err != nil {
			t.Fatal(err)
		}
		after := fileSize(t, s.Path())
		t.Logf("old %v: %d → %d bytes (freed %d, converted %v)", old, before, after, m.Freed, m.Converted)
		if m.Converted != old || m.Freed < 10<<20 || after > before-10<<20 {
			t.Errorf("old %v: %d → %d bytes, %+v", old, before, after, m)
		}
		if fi, err := os.Stat(s.Path() + "-wal"); err == nil && fi.Size() != 0 {
			t.Errorf("old %v: WAL %d bytes after Maintain", old, fi.Size())
		}
		if st, _ := s.pages(ctx); st.autoVacuum != 2 || st.free != 0 {
			t.Errorf("old %v: after: %+v", old, st)
		}
		// Nothing to do: nothing changes.
		if m, err := s.Maintain(ctx); err != nil || m.Freed != 0 || m.Converted {
			t.Errorf("old %v: second Maintain %+v, %v", old, m, err)
		}
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size()
}
