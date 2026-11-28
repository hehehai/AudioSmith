package sqlitestore

// Maintenance (M7a, thread #22): local usage statistics for Settings →
// Usage, and for moving the data folder (done while the database is
// closed, at the app's next start: the plugin refuses VACUUM INTO) the
// path rewrite of a copied database and an integrity check.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
)

// Usage are statistics of the library, computed on this Mac and never
// sent anywhere.
type Usage struct {
	Takes        int
	Starred      int
	AudioSeconds float64 // of the takes
	GenSeconds   float64 // GPU time the takes took
	Days         int     // days with at least one take
	First        time.Time
	Voices       int
	Cloned       int
	Designed     int
	Transcripts  int
	// TranscriptSeconds is the audio transcribed.
	TranscriptSeconds float64
	Renders           int // finished stories and audiobooks
	RenderSeconds     float64
	Dubs              int
	DubSeconds        float64 // of the sources dubbed
	Workflows         int
	Exports           int
	ByModel           []Count // takes per model, most first
	ByLanguage        []Count // takes per language ("" = not set), most first
}

// Count is a number of takes and their audio.
type Count struct {
	Key     string
	N       int
	Seconds float64
}

// Usage computes the statistics.
func (s *Store) Usage(ctx context.Context) (Usage, error) {
	var u Usage
	one := func(sql string, fn func(row []any)) error {
		rows, err := s.db.Query(ctx, sql)
		if err != nil {
			return wrap("usage", err)
		}
		for _, r := range rows.Values {
			fn(r)
		}
		return nil
	}
	steps := []struct {
		sql string
		fn  func([]any)
	}{
		{`SELECT COUNT(*), COALESCE(SUM(starred),0), COALESCE(SUM(audio_seconds),0), COALESCE(SUM(gen_seconds),0), MIN(created_at),
			COUNT(DISTINCT date(created_at/1000, 'unixepoch', 'localtime')) FROM takes`, func(r []any) {
			u.Takes, u.Starred, u.AudioSeconds, u.GenSeconds = int(asInt(r[0])), int(asInt(r[1])), asFloat(r[2]), asFloat(r[3])
			if r[4] != nil {
				u.First = fromMS(r[4])
			}
			u.Days = int(asInt(r[5]))
		}},
		{`SELECT COUNT(*), COALESCE(SUM(kind = 'cloned'),0), COALESCE(SUM(kind = 'designed'),0) FROM voices WHERE kind != 'builtin'`, func(r []any) {
			u.Voices, u.Cloned, u.Designed = int(asInt(r[0])), int(asInt(r[1])), int(asInt(r[2]))
		}},
		{`SELECT COUNT(*), COALESCE(SUM(duration),0) FROM transcripts`, func(r []any) {
			u.Transcripts, u.TranscriptSeconds = int(asInt(r[0])), asFloat(r[1])
		}},
		{`SELECT COUNT(*), COALESCE(SUM(audio_seconds),0) FROM renders WHERE audition = 0 AND state = 'done'`, func(r []any) {
			u.Renders, u.RenderSeconds = int(asInt(r[0])), asFloat(r[1])
		}},
		{`SELECT COUNT(*), COALESCE(SUM(duration),0) FROM dubs`, func(r []any) { u.Dubs, u.DubSeconds = int(asInt(r[0])), asFloat(r[1]) }},
		{`SELECT COUNT(*) FROM workflows`, func(r []any) { u.Workflows = int(asInt(r[0])) }},
		{`SELECT COUNT(*) FROM exports`, func(r []any) { u.Exports = int(asInt(r[0])) }},
		{`SELECT model, COUNT(*), COALESCE(SUM(audio_seconds),0) FROM takes GROUP BY model ORDER BY COUNT(*) DESC, model LIMIT 12`, func(r []any) {
			u.ByModel = append(u.ByModel, Count{Key: asString(r[0]), N: int(asInt(r[1])), Seconds: asFloat(r[2])})
		}},
		{`SELECT COALESCE(json_extract(recipe, '$.language'), ''), COUNT(*), COALESCE(SUM(audio_seconds),0) FROM takes
			GROUP BY 1 ORDER BY COUNT(*) DESC, 1 LIMIT 12`, func(r []any) {
			u.ByLanguage = append(u.ByLanguage, Count{Key: asString(r[0]), N: int(asInt(r[1])), Seconds: asFloat(r[2])})
		}},
	}
	for _, st := range steps {
		if err := one(st.sql, st.fn); err != nil {
			return u, err
		}
	}
	return u, nil
}

// CheckFile opens the database at path, runs SQLite's integrity check
// and reports the schema version it has.
func CheckFile(ctx context.Context, path string) (int, error) {
	db, err := sqlite.Open(ctx, path, sqlite.OpenOptions{})
	if err != nil {
		return 0, err
	}
	defer db.Close()
	rows, err := db.Query(ctx, "PRAGMA integrity_check")
	if err != nil {
		return 0, err
	}
	if len(rows.Values) == 0 || asString(rows.Values[0][0]) != "ok" {
		var msgs []string
		for _, r := range rows.Values {
			msgs = append(msgs, asString(r[0]))
		}
		return 0, fmt.Errorf("sqlitestore: %s: integrity check: %s", path, strings.Join(msgs, "; "))
	}
	return userVersion(ctx, db)
}

// RewritePaths replaces the folder oldDir by newDir at the start of the
// paths the database at path holds in its text columns (as plain text,
// or inside JSON strings), so that a copied data folder points at its
// own files. Full-text indexes follow through their triggers. It
// returns how many rows changed.
func RewritePaths(ctx context.Context, path, oldDir, newDir string) (int64, error) {
	oldDir = strings.TrimRight(oldDir, "/\\")
	newDir = strings.TrimRight(newDir, "/\\")
	if oldDir == "" || newDir == "" || oldDir == newDir {
		return 0, errors.New("sqlitestore: RewritePaths needs two different folders")
	}
	db, err := sqlite.Open(ctx, path, sqlite.OpenOptions{})
	if err != nil {
		return 0, err
	}
	defer db.Close()
	tables, err := db.Query(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND sql NOT LIKE 'CREATE VIRTUAL%'
		AND name NOT LIKE 'sqlite_%' AND name NOT LIKE '%_fts%'`)
	if err != nil {
		return 0, err
	}
	// The forms a folder takes in the database: as it is, and escaped
	// in JSON (Go escapes <, >, & and \ on Windows).
	jsonForm := func(s string) string {
		b, _ := json.Marshal(s)
		return string(b[1 : len(b)-1])
	}
	var pairs [][2]string
	addPair := func(a, b string) {
		for _, p := range pairs {
			if p[0] == a && p[1] == b {
				return
			}
		}
		pairs = append(pairs, [2]string{a, b})
	}
	sep := string(os.PathSeparator)
	addPair(oldDir+sep, newDir+sep)
	addPair(jsonForm(oldDir+sep), jsonForm(newDir+sep))
	if sep != "/" {
		addPair(oldDir+"/", newDir+"/")
		addPair(jsonForm(oldDir+"/"), jsonForm(newDir+"/"))
	}
	var stmts []sqlite.Statement
	for _, t := range tables.Values {
		table := asString(t[0])
		cols, err := db.Query(ctx, fmt.Sprintf("PRAGMA table_info(%q)", table))
		if err != nil {
			return 0, err
		}
		for _, c := range cols.Values {
			col, typ := asString(c[1]), strings.ToUpper(asString(c[2]))
			if typ != "TEXT" && typ != "" {
				continue
			}
			for _, p := range pairs {
				stmts = append(stmts, sqlite.Statement{
					SQL:  fmt.Sprintf("UPDATE %q SET %q = replace(%q, ?, ?) WHERE instr(%q, ?) > 0", table, col, col, col),
					Args: []any{p[0], p[1], p[0]},
				})
				// A column holding exactly the folder (no trailing slash).
				stmts = append(stmts, sqlite.Statement{
					SQL:  fmt.Sprintf("UPDATE %q SET %q = ? WHERE %q = ?", table, col, col),
					Args: []any{strings.TrimSuffix(p[1], "/"), strings.TrimSuffix(p[0], "/")},
				})
			}
		}
	}
	if len(stmts) == 0 {
		return 0, nil
	}
	res, err := db.Transaction(ctx, stmts)
	if err != nil {
		return 0, err
	}
	var n int64
	for _, r := range res {
		n += r.Changes
	}
	return n, nil
}
