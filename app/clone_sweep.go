package main

// The clone flow's working files (code review 1102 M3): each clip, its
// cleaned copy and each recording is a WAV in <data>/references. The
// clips a clip replaces go at once; the rest (recordings, the parts
// played or kept, what a crash left) a week after, unless something in
// the library names them: a take cloned from a clip used without saving
// it keeps its clip.

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// cloneScratch are the prefixes of the clone flow's own working clips
// (workPath): only the flow uses them, never a take.
var cloneScratch = []string{"clip-", "clean-"}

// dropCloneWork deletes working clips of the clone flow no longer shown.
func (a *app) dropCloneWork(paths ...string) {
	dir := a.referencesDir()
	for _, p := range paths {
		if p == "" || filepath.Dir(p) != dir {
			continue
		}
		for _, prefix := range cloneScratch {
			if strings.HasPrefix(filepath.Base(p), prefix) {
				os.Remove(p)
			}
		}
	}
}

// sweepUnused deletes the files directly in dir older than age that
// nothing in the library names (sqlitestore.Mentioned); off the main
// thread.
func (a *app) sweepUnused(ctx context.Context, dir string, age time.Duration) {
	ents, err := os.ReadDir(dir)
	if err != nil || len(ents) == 0 {
		return
	}
	used, err := a.lib.store.Mentioned(ctx, dir)
	if err != nil {
		log.Printf("sweep: %s: %v", dir, err)
		return
	}
	var n int
	var freed int64
	for _, e := range ents {
		fi, err := e.Info()
		if err != nil || e.IsDir() || used[e.Name()] || time.Since(fi.ModTime()) < age {
			continue
		}
		if os.Remove(filepath.Join(dir, e.Name())) == nil {
			n, freed = n+1, freed+fi.Size()
		}
	}
	if n > 0 {
		log.Printf("sweep: %s: removed %d files nothing uses (%s)", dir, n, humanBytes(freed))
	}
}
