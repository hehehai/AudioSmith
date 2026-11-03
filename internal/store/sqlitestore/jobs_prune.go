package sqlitestore

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/volume"
)

// PruneJobs removes the jobs that ended (succeeded, failed, cancelled,
// interrupted) before t, with their dependencies and events, as
// DeleteJobs does, but keeps two kinds whose record AudioSmith still
// reads: jobs of the kinds keep (the model downloads, which tell it what
// it downloaded) and, for each model the params name that is not in
// known (a custom repo it used: family_app.go's usedRepos, which reads
// succeeded jobs), the latest job that succeeded naming it. An
// interrupted job that unfinished jobs still wait on stays (a Retry
// would run them). It returns how many it removed. Housekeeping (review
// #102: the table was never cleaned; code review 1102 L2: interrupted
// jobs and those naming a custom model stayed for ever).
//
// Code review 1103 L-2: the jobs to keep are found with one pass over
// the table each (not a subquery per row: 24 s for 20k jobs), and the
// rest go in batches of pruneBatch, so that other queries run between
// them.
func (s *Store) PruneJobs(ctx context.Context, finishedBefore time.Time, keep, known []string) (int64, error) {
	ended := []any{string(jobs.StateSucceeded), string(jobs.StateFailed), string(jobs.StateCancelled), string(jobs.StateInterrupted)}
	// The latest succeeded job naming each model.
	rows, err := s.db.Query(ctx, `SELECT id, m FROM (SELECT id, `+jobModel+` AS m,
		ROW_NUMBER() OVER (PARTITION BY `+jobModel+` ORDER BY finished_at DESC, id DESC) AS rn
		FROM jobs WHERE state = ? AND finished_at IS NOT NULL) WHERE rn = 1 AND m != ''`, string(jobs.StateSucceeded))
	if err != nil {
		return 0, wrap("jobs", err)
	}
	isKnown := map[string]bool{}
	for _, m := range known {
		isKnown[m] = true
	}
	stay := map[string]bool{}
	for _, r := range rows.Values {
		if !isKnown[asString(r[1])] {
			stay[asString(r[0])] = true
		}
	}
	// The jobs unfinished ones wait on.
	rows, err = s.db.Query(ctx, `SELECT DISTINCT d.after_id FROM job_deps d JOIN jobs w ON w.id = d.job_id WHERE w.state IN (?, ?, ?, ?)`,
		string(jobs.StateWaiting), string(jobs.StateQueued), string(jobs.StateRunning), string(jobs.StateInterrupted))
	if err != nil {
		return 0, wrap("jobs", err)
	}
	for _, r := range rows.Values {
		stay[asString(r[0])] = true
	}
	sql := `SELECT id FROM jobs WHERE state IN (?, ?, ?, ?) AND finished_at IS NOT NULL AND finished_at < ?`
	args := append(ended, finishedBefore.UnixMilli())
	if len(keep) > 0 {
		sql += ` AND kind NOT IN (` + marks(len(keep)) + `)`
		for _, k := range keep {
			args = append(args, k)
		}
	}
	rows, err = s.db.Query(ctx, sql, args...)
	if err != nil {
		return 0, wrap("jobs", err)
	}
	var drop []any
	for _, r := range rows.Values {
		if id := asString(r[0]); !stay[id] {
			drop = append(drop, id)
		}
	}
	var n int64
	for len(drop) > 0 {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		batch := drop[:min(len(drop), pruneBatch)]
		drop = drop[len(batch):]
		res, err := s.db.Execute(ctx, `DELETE FROM jobs WHERE id IN (`+marks(len(batch))+`)`, batch...)
		if err != nil {
			return n, wrap("jobs", err)
		}
		n += res.Changes
	}
	return n, nil
}

// pruneBatch is how many jobs one statement of PruneJobs deletes.
const pruneBatch = 500

// jobModel is the model a job's params name, empty for none (or params that
// are no JSON object, which name none).
const jobModel = `COALESCE(CASE WHEN json_valid(params) AND json_type(params) = 'object' THEN json_extract(params, '$.model') END, '')`

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
// WAL truncated. It stops between steps when ctx ends. The WAL is
// truncated whatever else failed, and the rebuild waits for room for two
// copies of the file and a gigabyte more (code review 1103 L-8: on a
// nearly full disk VACUUM failed every day and left the WAL as large).
func (s *Store) Maintain(ctx context.Context) (m Maintenance, err error) {
	defer func() {
		if _, cerr := s.db.Query(context.WithoutCancel(ctx), "PRAGMA wal_checkpoint(TRUNCATE)"); cerr != nil && err == nil {
			err = wrap("wal_checkpoint", cerr)
		}
	}()
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
		float64(start.free) >= convertMinShare*float64(start.count) && start.count*start.size <= convertMaxBytes &&
		roomToRebuild(s.path, start.count*start.size):
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
	return m, nil
}

// diskFree is the space free on a path's volume, -1 when unknown (a
// variable: tests fake a full disk).
var diskFree = volume.Free

// roomToRebuild reports whether the volume of the database at path has
// room for a VACUUM of a file of bytes: its copy and the WAL that holds
// it, and a gigabyte for the rest.
func roomToRebuild(path string, bytes int64) bool {
	free := diskFree(filepath.Dir(path))
	return free < 0 || free >= 2*bytes+1<<30
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
