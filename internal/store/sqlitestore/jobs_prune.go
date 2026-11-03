package sqlitestore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/internal/jobs"
)

// PruneJobs removes the jobs that ended (succeeded, failed, cancelled,
// interrupted) before t, with their dependencies and events, as
// DeleteJobs does, but keeps two kinds whose record AudioSmith still
// reads: jobs of the kinds keep (the model downloads, which tell it what
// it downloaded) and, for each model the params name that is not in
// known (a custom repo it used: family_app.go's usedRepos), the latest
// job naming it. An interrupted job that unfinished jobs still wait on
// stays (a Retry would run them). It returns how many it removed.
// Housekeeping (review #102: the table was never cleaned; code review
// 1102 L2: interrupted jobs and those naming a custom model stayed for
// ever).
func (s *Store) PruneJobs(ctx context.Context, finishedBefore time.Time, keep, known []string) (int64, error) {
	args := []any{string(jobs.StateSucceeded), string(jobs.StateFailed), string(jobs.StateCancelled), string(jobs.StateInterrupted),
		finishedBefore.UnixMilli()}
	sql := `DELETE FROM jobs WHERE state IN (?, ?, ?, ?) AND finished_at IS NOT NULL AND finished_at < ?`
	if len(keep) > 0 {
		sql += ` AND kind NOT IN (` + marks(len(keep)) + `)`
		for _, k := range keep {
			args = append(args, k)
		}
	}
	// No unfinished job waits on it.
	sql += ` AND NOT EXISTS (SELECT 1 FROM job_deps d JOIN jobs w ON w.id = d.job_id
		WHERE d.after_id = jobs.id AND w.state IN (?, ?, ?, ?))`
	args = append(args, string(jobs.StateWaiting), string(jobs.StateQueued), string(jobs.StateRunning), string(jobs.StateInterrupted))
	// A model the params name, '' for none (or params that are no JSON
	// object, which name none).
	model := func(t string) string {
		return `COALESCE(CASE WHEN json_valid(` + t + `.params) AND json_type(` + t + `.params) = 'object' THEN json_extract(` + t + `.params, '$.model') END, '')`
	}
	sql += ` AND (` + model("jobs") + ` = ''`
	if len(known) > 0 {
		sql += ` OR ` + model("jobs") + ` IN (` + marks(len(known)) + `)`
		for _, m := range known {
			args = append(args, m)
		}
	}
	// A custom model's job goes when a later ended job names it too.
	sql += ` OR EXISTS (SELECT 1 FROM jobs n WHERE n.finished_at IS NOT NULL AND n.state IN (?, ?, ?, ?)
		AND (n.finished_at > jobs.finished_at OR (n.finished_at = jobs.finished_at AND n.id > jobs.id))
		AND ` + model("n") + ` = ` + model("jobs") + `))`
	args = append(args, string(jobs.StateSucceeded), string(jobs.StateFailed), string(jobs.StateCancelled), string(jobs.StateInterrupted))
	res, err := s.db.Execute(ctx, sql, args...)
	if err != nil {
		return 0, wrap("jobs", err)
	}
	return res.Changes, nil
}

// marks is n placeholders, comma-separated.
func marks(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// Maintenance is what Maintain did.
type Maintenance struct {
	// Freed is how many bytes the file shrank by (free pages given back).
	Freed int64
	// Converted: the file was rebuilt (VACUUM) to vacuum incrementally
	// from then on.
	Converted bool
}

// Housekeeping of the database file (code review 1102 L2): deleted rows
// left free pages that never went back to the disk, and nothing ever
// truncated the WAL after a burst of writes.
const (
	// vacuumStep is how many pages one incremental_vacuum frees; other
	// queries run between steps.
	vacuumStep = 256
	// convertMinFree and convertMinShare are how much of an old file
	// (auto_vacuum NONE) must be free before it is rebuilt once to vacuum
	// incrementally; convertMaxBytes the largest file rebuilt (VACUUM
	// holds the one connection and needs as much room again).
	convertMinFree  = 8 << 20
	convertMinShare = 0.2
	convertMaxBytes = 256 << 20
)

// pause is how long Maintain waits between vacuum steps (a variable for tests).
var pause = 20 * time.Millisecond

// Maintain tidies the database while the app is idle: SQLite's
// statistics (PRAGMA optimize), free pages back to the disk (an
// incremental vacuum in small steps; a file made before auto_vacuum was
// set is rebuilt once, when a fifth of it and 8 MB are free), and the
// WAL truncated. It stops between steps when ctx ends.
func (s *Store) Maintain(ctx context.Context) (Maintenance, error) {
	var m Maintenance
	if _, err := s.db.Query(ctx, "PRAGMA optimize"); err != nil {
		return m, wrap("optimize", err)
	}
	start, err := s.pages(ctx)
	if err != nil {
		return m, err
	}
	switch {
	case start.autoVacuum == 2: // INCREMENTAL
		for free := start.free; free > 0; {
			if _, err := s.db.Query(ctx, fmt.Sprintf("PRAGMA incremental_vacuum(%d)", vacuumStep)); err != nil {
				return m, wrap("incremental_vacuum", err)
			}
			st, err := s.pages(ctx)
			if err != nil {
				return m, err
			}
			if st.free >= free { // nothing more to give back
				break
			}
			free = st.free
			select {
			case <-ctx.Done():
				return m, ctx.Err()
			case <-time.After(pause):
			}
		}
	case start.autoVacuum == 0 && start.free*start.size >= convertMinFree &&
		float64(start.free) >= convertMinShare*float64(start.count) && start.count*start.size <= convertMaxBytes:
		if _, err := s.db.Query(ctx, "PRAGMA auto_vacuum = INCREMENTAL"); err != nil {
			return m, wrap("auto_vacuum", err)
		}
		if _, err := s.db.Execute(ctx, "VACUUM"); err != nil {
			return m, wrap("vacuum", err)
		}
		m.Converted = true
	}
	end, err := s.pages(ctx)
	if err != nil {
		return m, err
	}
	m.Freed = (start.count - end.count) * start.size
	if _, err := s.db.Query(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return m, wrap("wal_checkpoint", err)
	}
	return m, nil
}

type pageStats struct{ count, free, size, autoVacuum int64 }

func (s *Store) pages(ctx context.Context) (pageStats, error) {
	var st pageStats
	for _, p := range []struct {
		pragma string
		v      *int64
	}{{"page_count", &st.count}, {"freelist_count", &st.free}, {"page_size", &st.size}, {"auto_vacuum", &st.autoVacuum}} {
		rows, err := s.db.Query(ctx, "PRAGMA "+p.pragma)
		if err != nil {
			return st, wrap(p.pragma, err)
		}
		if len(rows.Values) == 1 && len(rows.Values[0]) == 1 {
			*p.v = asInt(rows.Values[0][0])
		}
	}
	return st, nil
}
