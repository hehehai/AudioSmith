package services

// Reclaiming the room longform renders take (code review 1102 H1): a
// project's unfinished renders go with it, a finished render replaces the
// project's older unfinished attempts, and a sweep (at startup, then
// daily) removes render folders without a record, unfinished renders of
// no project, and keeps the chunk cache within its limits.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/store"
)

const (
	// orphanRenderAge is how long an unfinished render of no project
	// (its project deleted, or none) stays for Resume.
	orphanRenderAge = 7 * 24 * time.Hour
	// strayDirAge is how old a render folder without a record must be to
	// go (a render being started makes its folder before its record).
	strayDirAge = time.Hour
)

// unfinished are the states of renders that ended without a book.
var unfinished = []core.RenderState{core.RenderFailed, core.RenderStopped}

// DropProject deletes project id's unfinished renders (its finished books
// stay, as the delete confirmation says) and returns how many went; a
// running one is left (the app refuses to delete the project first).
func (s *Longform) DropProject(ctx context.Context, project string) (int, error) {
	if project == "" {
		return 0, nil
	}
	l, err := s.renders.List(ctx, store.RenderQuery{ProjectID: project, States: unfinished, Auditions: true})
	if err != nil {
		return 0, err
	}
	n, err := s.deleteAll(ctx, l)
	if n > 0 {
		s.render.pruneLongformChunks()
	}
	return n, err
}

// deleteAll deletes renders, going on past the ones that fail.
func (s *Longform) deleteAll(ctx context.Context, l []core.Render) (int, error) {
	var errs []error
	n := 0
	for _, r := range l {
		if err := s.Delete(ctx, r.ID); err != nil && !errors.Is(err, core.ErrNotFound) {
			errs = append(errs, err)
			continue
		}
		n++
	}
	return n, errors.Join(errs...)
}

// dropSuperseded deletes the unfinished renders of done's project made
// before it: a finished book replaces the attempts that came before.
func (s *Longform) dropSuperseded(ctx context.Context, done core.Render) {
	if done.ProjectID == "" || done.Audition {
		return
	}
	l, err := s.renders.List(ctx, store.RenderQuery{ProjectID: done.ProjectID, States: unfinished})
	if err != nil {
		return
	}
	var old []core.Render
	for _, r := range l {
		if r.ID != done.ID && r.CreatedAt.Before(done.CreatedAt) {
			old = append(old, r)
		}
	}
	if n, _ := s.deleteAll(ctx, old); n > 0 {
		s.logf("longform: %q is done: removed %d earlier unfinished renders", done.Title, n)
	}
}

// Sweep removes what renders left behind: folders in renders/ no record
// names (older than strayDirAge), unfinished renders of no project older
// than orphanRenderAge, and chunks beyond the cache's limits. It returns
// how many renders and folders went. Call it once the renders are
// reconciled (after Reconcile), at startup and daily.
func (s *Longform) Sweep(ctx context.Context) (int, error) {
	all, err := s.renders.List(ctx, store.RenderQuery{Auditions: true})
	if err != nil {
		return 0, err
	}
	known := map[string]bool{}
	var orphans []core.Render
	running := false
	for _, r := range all {
		known[r.ID] = true
		state := r.State
		s.mu.Lock()
		if lr := s.live[r.ID]; lr != nil {
			state = lr.rec.State
		}
		s.mu.Unlock()
		if state == core.RenderRunning {
			running = true
		}
		if r.ProjectID == "" && !r.Audition && (state == core.RenderFailed || state == core.RenderStopped) &&
			s.now().Sub(r.UpdatedAt) > orphanRenderAge {
			orphans = append(orphans, r)
		}
	}
	n, err := s.deleteAll(ctx, orphans)
	root := filepath.Join(s.dataDir, "renders")
	ents, _ := os.ReadDir(root)
	for _, e := range ents {
		if !e.IsDir() || known[e.Name()] {
			continue
		}
		if fi, ierr := e.Info(); ierr == nil && time.Since(fi.ModTime()) > strayDirAge {
			if os.RemoveAll(filepath.Join(root, e.Name())) == nil {
				n++
			}
		}
	}
	if !running {
		s.render.pruneLongformChunks()
	}
	if n > 0 {
		s.logf("longform: swept %d unfinished renders and stray folders", n)
	}
	return n, err
}

// ChunkDir is the longform chunk cache's folder (Settings → Storage).
func (s *Longform) ChunkDir() string { return s.render.longformChunkDir() }

// ClearChunks empties the longform chunk cache and returns the bytes it
// freed; it refuses (core.ErrBusy) while a render runs: its chapters
// read and write chunks.
func (s *Longform) ClearChunks(ctx context.Context) (int64, error) {
	running, err := s.renders.List(ctx, store.RenderQuery{Auditions: true, States: []core.RenderState{core.RenderRunning}})
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	for _, l := range s.live {
		if l.rec.State == core.RenderRunning {
			running = append(running, l.rec)
		}
	}
	s.mu.Unlock()
	for _, r := range running {
		if title, busy := s.busyRender(r.ID); busy {
			return 0, fmt.Errorf("longform: %q is rendering: %w", title, core.ErrBusy)
		}
	}
	dir := s.render.longformChunkDir()
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	var freed int64
	for _, e := range ents {
		path := filepath.Join(dir, e.Name())
		var size int64
		if fi, err := e.Info(); err == nil {
			size = fi.Size()
		}
		if os.RemoveAll(path) == nil {
			freed += size
		}
	}
	return freed, nil
}

// busyRender reports whether render id runs now (in memory, else as
// stored), with its title.
func (s *Longform) busyRender(id string) (string, bool) {
	s.mu.Lock()
	lr := s.live[id]
	s.mu.Unlock()
	if lr != nil {
		return lr.rec.Title, lr.rec.State == core.RenderRunning
	}
	r, err := s.renders.Get(context.Background(), id)
	return r.Title, err == nil && r.State == core.RenderRunning
}
