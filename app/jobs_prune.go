package main

import (
	"context"
	"log"
	"time"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/hub"
)

// jobsKeepDays is how long finished jobs stay in the library: the Tasks
// page's history, and their errors, which say why something failed.
const jobsKeepDays = 30

// pruneJobs removes the jobs that finished more than jobsKeepDays ago,
// once at startup (review #102: the table only grew, and a book's
// chapter jobs hold its text). The model downloads stay, and the jobs
// naming a custom model: they are how AudioSmith knows it used them
// (usedRepos, family_app.go).
func (l *library) pruneJobs(ctx context.Context) {
	var known []string
	for _, e := range catalog.Builtin().Entries {
		if e.Repo != "" {
			known = append(known, e.Repo)
		}
	}
	n, err := l.store.PruneJobs(ctx, time.Now().AddDate(0, 0, -jobsKeepDays), []string{hub.KindDownload}, known)
	if err != nil {
		log.Printf("jobs: prune: %v", err)
	} else if n > 0 {
		log.Printf("jobs: removed %d finished more than %d days ago", n, jobsKeepDays)
	}
}

// maintainDB is the database's daily housekeeping, with the disk sweep
// (storage_clean.go: two minutes after the start, then every day; off
// the main thread): the jobs past jobsKeepDays again (a session may last
// longer than a day), then, when no job runs, the free pages back to the
// disk and the WAL truncated (code review 1102 L2). Other queries wait
// for one vacuum step at most; the one rebuild of a file made before
// (at most 256 MB) holds the database for a second or two.
func (l *library) maintainDB(ctx context.Context) {
	l.pruneJobs(ctx)
	if !l.sched.Idle() {
		return // next time: a vacuum would hold the database while jobs save
	}
	m, err := l.store.Maintain(ctx)
	switch {
	case err != nil:
		log.Printf("database: maintenance: %v", err)
	case m.Freed > 0 || m.Converted:
		log.Printf("database: gave back %s (rebuilt: %v)", humanBytes(m.Freed), m.Converted)
	}
}
