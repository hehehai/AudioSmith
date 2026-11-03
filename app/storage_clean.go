package main

// Reclaiming disk space (code review 1102 H1, M2, M3, M6, L1, L4): what
// Settings → Storage clears (the caches, the audiobook chunk cache, the
// agents' files) and the sweep that runs a while after the start, then
// daily: the renders and chunks longform left, the working files of dubs
// exported a week ago, stray isolations, old agent files, clone clips
// and covers nothing uses, and leftovers of writes a crash cut short.

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/services"
)

// cacheParts are the caches Clear removes (all rebuilt when needed); the
// rest of the cache folder is the system's (WebKit). The longform chunks
// live in the data folder now (their own category); the old place stays
// for a move that failed.
var cacheParts = []string{"peaks", "chunks", "tmp", "previews", "calls", "auditions", "voice-previews", "ffmpeg", filepath.Join("longform", "chunks")}

// Sweep timing: the first a while after the start (not to slow it), then
// daily.
const (
	sweepDelay = 2 * time.Minute
	sweepEvery = 24 * time.Hour
	// agentFilesKeep: files AudioSmith wrote for agents in <data>/mcp are
	// deleted after this long (a folder chosen in the settings is the
	// user's: nothing there is deleted).
	agentFilesKeep = 30 * 24 * time.Hour
	// unusedClipAge: clone clips and covers nothing in the library names.
	unusedClipAge  = 7 * 24 * time.Hour
	unusedCoverAge = 24 * time.Hour
)

// cacheBytes measures the caches Clear removes.
func cacheBytes(ctx context.Context, cache string) int64 {
	var n int64
	for _, p := range cacheParts {
		b, _ := treeSize(ctx, filepath.Join(cache, p), nil)
		n += b
	}
	return n
}

// clearCaches empties the caches; work in progress may use them, so the
// button waits until no job runs.
func (a *app) clearCaches() {
	dir := a.cacheDir()
	a.background(func() {
		var freed int64
		for _, p := range cacheParts {
			freed += removeTree(filepath.Join(dir, p))
		}
		a.post(func() { a.cleared(freed, nil) })
	})
}

// clearChunks empties the longform chunk cache (refused while a book
// renders).
func (a *app) clearChunks() {
	lf := a.lib.svc.Longform
	a.background(func() {
		freed, err := lf.ClearChunks(context.Background())
		a.post(func() { a.cleared(freed, err) })
	})
}

// clearAgentFiles deletes the files agents were given in <data>/mcp.
func (a *app) clearAgentFiles() {
	dir := filepath.Join(a.dataDir(), "mcp")
	a.background(func() {
		freed := pruneOld(dir, 0)
		a.post(func() { a.cleared(freed, nil) })
	})
}

// cleared says what a Clear freed and measures again.
func (a *app) cleared(freed int64, err error) {
	switch {
	case errors.Is(err, core.ErrBusy):
		a.toast(i18n.T("Wait for the jobs in progress"))
	case err != nil:
		a.toastWarn(i18n.T("Could not clear: %v", err))
	default:
		a.toast(i18n.T("Freed %s", humanBytes(freed)))
	}
	a.stReport = nil
	a.loadStorage()
}

// stClearButton is a category's Clear (true when it has one).
func (a *app) stClearButton(c *ui.Context, cat storageCat) bool {
	var clear func()
	switch cat.id {
	case "caches":
		clear = a.clearCaches
	case "chunks":
		clear = a.clearChunks
	case "agents":
		clear = a.clearAgentFiles
	default:
		return false
	}
	busy := len(a.activeJobs()) > 0
	b := ui.Button(c.Key("st-clear-"+cat.id), i18n.T("Clear")).Label(i18n.T("Clear %s", cat.title)).Disabled(busy || cat.bytes == 0)
	if busy {
		b.Tooltip(i18n.T("Wait for the jobs in progress"))
	}
	if b.Clicked() {
		clear()
	}
	return true
}

// removeTree removes a file or folder and returns the bytes it held.
func removeTree(path string) int64 {
	n, _ := treeSize(context.Background(), path, nil)
	if os.RemoveAll(path) != nil {
		return 0
	}
	return n
}

// pruneOld deletes the files in dir (and below) older than age, and
// returns the bytes.
func pruneOld(dir string, age time.Duration) int64 {
	var freed int64
	filepath.Walk(dir, func(path string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || time.Since(fi.ModTime()) < age {
			return nil
		}
		if os.Remove(path) == nil {
			freed += fi.Size()
		}
		return nil
	})
	return freed
}

// startSweeps runs the sweep a while after the start, then daily (not in
// tests: they call sweepDisk).
func (a *app) startSweeps() {
	if a.win == nil {
		return
	}
	go func() {
		time.Sleep(sweepDelay)
		for {
			a.sweepDisk(context.Background())
			time.Sleep(sweepEvery)
		}
	}()
}

// sweepDisk reclaims what nothing needs any more (off the main thread).
func (a *app) sweepDisk(ctx context.Context) {
	lib := a.lib
	data := lib.store.Dir()
	if n, err := lib.svc.Longform.Sweep(ctx); err != nil || n > 0 {
		log.Printf("sweep: longform: %d (%v)", n, err)
	}
	if a.dubSvc != nil {
		open := lib.settings.Get().Dubbing.Open // the dub open (or last open) stays
		a.dubSvc.FreeIdle(ctx, services.DubFreeAfter, a.dubExports(ctx), func(id string) bool { return id == open })
	}
	if a.isoSvc != nil {
		a.isoSvc.Sweep(ctx)
	}
	if n := pruneOld(filepath.Join(data, "mcp"), agentFilesKeep); n > 0 {
		log.Printf("sweep: agent files: freed %s", humanBytes(n))
	}
	a.sweepUnused(ctx, a.referencesDir(), unusedClipAge) // clone_sweep.go
	a.sweepUnused(ctx, filepath.Join(data, "covers"), unusedCoverAge)
	if lib.sched.Idle() { // a long job's temporary files may be over an hour old
		cleanLeftovers(data, a.cacheDir()) // data_cleanup.go
	}
}

// dubExports is when each dub was last exported, by the export records.
func (a *app) dubExports(ctx context.Context) func(id string) time.Time {
	at := map[string]time.Time{}
	if l, err := a.lib.svc.Library.Exports(ctx, "", 0); err == nil {
		for _, e := range l {
			if e.Kind == "dub" && e.CreatedAt.After(at[e.SourceID]) {
				at[e.SourceID] = e.CreatedAt
			}
		}
	}
	return func(id string) time.Time { return at[id] }
}

// sampleCopy reports a sample's audio copied into the data folder: its
// record goes with the file (services.Library.DeleteExport, code review
// 1102 L1).
func (a *app) sampleCopy(path string) bool {
	rel, err := filepath.Rel(filepath.Join(a.dataDir(), "samples"), filepath.Dir(path))
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !strings.Contains(rel, string(filepath.Separator))
}
