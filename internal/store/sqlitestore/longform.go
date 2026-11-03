package sqlitestore

// M3 (thread #18): longform renders, export records, project summaries
// and segments, and the library's search across every kind of record.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/egoist/mygo/plugins/sqlite"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/store"
)

func (s *Store) Renders() store.Renders { return &renderRepo{db: s.db} }
func (s *Store) Exports() store.Exports { return &exportRepo{db: s.db} }
func (s *Store) Library() store.Library { return &libraryRepo{db: s.db} }

// --- project summaries and segments ---------------------------------------

// PutSummary replaces a project's summary and segments in one transaction.
func (r *projectRepo) PutSummary(ctx context.Context, id string, summary []byte, segs []store.Segment) error {
	if len(summary) == 0 {
		summary = []byte("{}")
	}
	if !json.Valid(summary) {
		return fmt.Errorf("project %s: summary is not JSON: %w", id, core.ErrInvalid)
	}
	stmts := []sqlite.Statement{
		{SQL: `UPDATE projects SET summary = ? WHERE id = ?`, Args: []any{string(summary), id}},
		{SQL: `DELETE FROM segments WHERE project_id = ?`, Args: []any{id}},
	}
	for i, sg := range segs {
		sid := sg.ID
		if sid == "" {
			sid = fmt.Sprintf("%s.%d", id, i)
		} else {
			sid = id + "." + sid // segment ids are unique across projects
		}
		stmts = append(stmts, sqlite.Statement{
			SQL: `INSERT INTO segments (id, project_id, ord, text, speaker, voice_id)
VALUES (?, ?, ?, ?, ?, (SELECT id FROM voices WHERE id = ?))`,
			Args: []any{sid, id, sg.Ord, sg.Text, nullable(sg.Speaker), sg.VoiceID},
		})
	}
	res, err := r.db.Transaction(ctx, stmts)
	if err != nil {
		return wrap("project "+id, err)
	}
	if len(res) > 0 && res[0].Changes == 0 {
		return fmt.Errorf("project %s: %w", id, core.ErrNotFound)
	}
	return nil
}

// --- renders ------------------------------------------------------------

type renderRepo struct{ db *sqlite.DB }

func (r *renderRepo) Put(ctx context.Context, x core.Render) error {
	if err := validID(x.ID); err != nil {
		return err
	}
	body := string(x.Body)
	if len(x.Body) == 0 {
		body = "{}"
	} else if !json.Valid(x.Body) {
		return fmt.Errorf("render %s: body is not JSON: %w", x.ID, core.ErrInvalid)
	}
	_, err := r.db.Execute(ctx, `INSERT INTO renders (id, project_id, kind, title, state, audition, audio_seconds, created_at, updated_at, body)
VALUES (?, (SELECT id FROM projects WHERE id = ?), ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET project_id = excluded.project_id, kind = excluded.kind, title = excluded.title,
  state = excluded.state, audition = excluded.audition, audio_seconds = excluded.audio_seconds,
  created_at = excluded.created_at, updated_at = excluded.updated_at, body = excluded.body`,
		x.ID, x.ProjectID, string(x.Kind), x.Title, string(x.State), x.Audition, x.AudioSeconds,
		x.CreatedAt.UnixMilli(), x.UpdatedAt.UnixMilli(), body)
	return wrap("render "+x.ID, err)
}

const renderColumns = `id, project_id, kind, title, state, audition, audio_seconds, created_at, updated_at, body`

// renderLightBody is the body of RenderQuery.Light: its output only,
// but the whole body of a render with no project, and of a body that is
// no JSON object (as stored).
const renderLightBody = `CASE WHEN project_id IS NOT NULL AND project_id != '' AND json_valid(body) AND json_type(body) = 'object'
	THEN json_object('output', json(json_extract(body, '$.output'))) ELSE body END`

func scanRender(row []any) core.Render {
	return core.Render{
		ID: asString(row[0]), ProjectID: asString(row[1]), Kind: core.ProjectKind(asString(row[2])),
		Title: asString(row[3]), State: core.RenderState(asString(row[4])), Audition: asInt(row[5]) != 0,
		AudioSeconds: asFloat(row[6]), CreatedAt: fromMS(row[7]), UpdatedAt: fromMS(row[8]),
		Body: json.RawMessage(asBytes(row[9])),
	}
}

func asFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int64:
		return float64(x)
	}
	return 0
}

func (r *renderRepo) Get(ctx context.Context, id string) (core.Render, error) {
	rows, err := r.db.Query(ctx, `SELECT `+renderColumns+` FROM renders WHERE id = ?`, id)
	if err != nil {
		return core.Render{}, wrap("render "+id, err)
	}
	if len(rows.Values) == 0 {
		return core.Render{}, fmt.Errorf("render %s: %w", id, core.ErrNotFound)
	}
	return scanRender(rows.Values[0]), nil
}

func (r *renderRepo) List(ctx context.Context, q store.RenderQuery) ([]core.Render, error) {
	var where []string
	var args []any
	if q.ProjectID != "" {
		where, args = append(where, `project_id = ?`), append(args, q.ProjectID)
	}
	if len(q.States) > 0 {
		marks := make([]string, len(q.States))
		for i, s := range q.States {
			marks[i] = "?"
			args = append(args, string(s))
		}
		where = append(where, `state IN (`+strings.Join(marks, ", ")+`)`)
	}
	if !q.Auditions {
		where = append(where, `audition = 0`)
	}
	cols := renderColumns
	if q.Light {
		cols = strings.TrimSuffix(cols, `body`) + renderLightBody
	}
	sql := `SELECT ` + cols + ` FROM renders`
	if len(where) > 0 {
		sql += ` WHERE ` + strings.Join(where, ` AND `)
	}
	sql, args = page(sql+` ORDER BY created_at DESC, id DESC`, args, q.Limit, 0)
	rows, err := r.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, wrap("renders", err)
	}
	out := make([]core.Render, 0, len(rows.Values))
	for _, row := range rows.Values {
		out = append(out, scanRender(row))
	}
	return out, nil
}

func (r *renderRepo) Delete(ctx context.Context, id string) error {
	res, err := r.db.Execute(ctx, `DELETE FROM renders WHERE id = ?`, id)
	if err != nil {
		return wrap("render "+id, err)
	}
	if res.Changes == 0 {
		return fmt.Errorf("render %s: %w", id, core.ErrNotFound)
	}
	return nil
}

// --- exports ------------------------------------------------------------

type exportRepo struct{ db *sqlite.DB }

func (r *exportRepo) Put(ctx context.Context, e core.Export) error {
	if err := validID(e.ID); err != nil {
		return err
	}
	if e.Path == "" {
		return fmt.Errorf("export %s: no path: %w", e.ID, core.ErrInvalid)
	}
	_, err := r.db.Execute(ctx, `INSERT INTO exports (id, created_at, kind, source_id, project_id, title, path, format, bytes)
VALUES (?, ?, ?, ?, (SELECT id FROM projects WHERE id = ?), ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET created_at = excluded.created_at, kind = excluded.kind, source_id = excluded.source_id,
  project_id = excluded.project_id, title = excluded.title, path = excluded.path, format = excluded.format, bytes = excluded.bytes`,
		e.ID, e.CreatedAt.UnixMilli(), e.Kind, e.SourceID, e.ProjectID, e.Title, e.Path, e.Format, e.Bytes)
	return wrap("export "+e.ID, err)
}

const exportColumns = `id, created_at, kind, source_id, project_id, title, path, format, bytes`

func scanExport(row []any) core.Export {
	return core.Export{ID: asString(row[0]), CreatedAt: fromMS(row[1]), Kind: asString(row[2]), SourceID: asString(row[3]),
		ProjectID: asString(row[4]), Title: asString(row[5]), Path: asString(row[6]), Format: asString(row[7]), Bytes: asInt(row[8])}
}

func (r *exportRepo) Get(ctx context.Context, id string) (core.Export, error) {
	rows, err := r.db.Query(ctx, `SELECT `+exportColumns+` FROM exports WHERE id = ?`, id)
	if err != nil {
		return core.Export{}, wrap("export "+id, err)
	}
	if len(rows.Values) == 0 {
		return core.Export{}, fmt.Errorf("export %s: %w", id, core.ErrNotFound)
	}
	return scanExport(rows.Values[0]), nil
}

func (r *exportRepo) List(ctx context.Context, search string, limit int) ([]core.Export, error) {
	sql := `SELECT ` + exportColumns + ` FROM exports`
	var args []any
	if s := strings.TrimSpace(search); s != "" {
		if useFTS(s) {
			sql += ` WHERE seq IN (SELECT rowid FROM exports_fts WHERE exports_fts MATCH ?)`
			args = append(args, ftsPhrase(s))
		} else {
			sql += ` WHERE (title LIKE ? ESCAPE '\' OR path LIKE ? ESCAPE '\')`
			args = append(args, likePattern(s), likePattern(s))
		}
	}
	sql, args = page(sql+` ORDER BY created_at DESC, id DESC`, args, limit, 0)
	rows, err := r.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, wrap("exports", err)
	}
	out := make([]core.Export, 0, len(rows.Values))
	for _, row := range rows.Values {
		out = append(out, scanExport(row))
	}
	return out, nil
}

func (r *exportRepo) Delete(ctx context.Context, id string) error {
	res, err := r.db.Execute(ctx, `DELETE FROM exports WHERE id = ?`, id)
	if err != nil {
		return wrap("export "+id, err)
	}
	if res.Changes == 0 {
		return fmt.Errorf("export %s: %w", id, core.ErrNotFound)
	}
	return nil
}

// --- the library --------------------------------------------------------

type libraryRepo struct{ db *sqlite.DB }

// libPart is one source of the library: a SELECT of (src, id, cat, title,
// detail, at) with its search condition.
type libPart struct {
	sql      string // SELECT … FROM … (WHERE added)
	where    string // condition always applied ("" = none)
	fts      string // "seq IN (SELECT rowid FROM x_fts WHERE x_fts MATCH ?)"
	like     string // the LIKE condition for short searches, with one ? per likeArgs
	likeArgs int
	category func(string) bool // whether the part can list category
	catWhere func(string) (string, []any)
}

var libParts = []libPart{
	{
		sql:      `SELECT 'project' AS src, id, kind AS cat, title, summary AS detail, updated_at AS at FROM projects`,
		fts:      `seq IN (SELECT rowid FROM projects_fts WHERE projects_fts MATCH ?)`,
		like:     `title LIKE ? ESCAPE '\'`,
		likeArgs: 1,
		category: func(c string) bool {
			return c == "" || c == store.LibDub || c == store.LibStory || c == store.LibAudiobook || c == store.LibTranscript
		},
		catWhere: func(c string) (string, []any) { return `kind = ?`, []any{c} },
	},
	{
		// Finished renders, filed under their project's kind.
		sql:      `SELECT 'render' AS src, id, kind AS cat, title, state AS detail, updated_at AS at FROM renders`,
		where:    `audition = 0 AND state = 'done'`,
		fts:      `seq IN (SELECT rowid FROM renders_fts WHERE renders_fts MATCH ?)`,
		like:     `title LIKE ? ESCAPE '\'`,
		likeArgs: 1,
		category: func(c string) bool { return c == "" || c == store.LibStory || c == store.LibAudiobook },
		catWhere: func(c string) (string, []any) { return `kind = ?`, []any{c} },
	},
	{
		sql:      `SELECT 'voice' AS src, id, 'voice' AS cat, name AS title, kind AS detail, updated_at AS at FROM voices`,
		fts:      `seq IN (SELECT rowid FROM voices_fts WHERE voices_fts MATCH ?)`,
		like:     `(name LIKE ? ESCAPE '\' OR search LIKE ? ESCAPE '\')`,
		likeArgs: 2,
		category: func(c string) bool { return c == "" || c == store.LibVoice },
	},
	{
		sql:      `SELECT 'take' AS src, id, 'take' AS cat, text AS title, model AS detail, created_at AS at FROM takes`,
		fts:      `seq IN (SELECT rowid FROM takes_fts WHERE takes_fts MATCH ?)`,
		like:     `text LIKE ? ESCAPE '\'`,
		likeArgs: 1,
		category: func(c string) bool { return c == "" || c == store.LibTake },
	},
	{
		sql:      `SELECT 'export' AS src, id, 'export' AS cat, title, path AS detail, created_at AS at FROM exports`,
		fts:      `seq IN (SELECT rowid FROM exports_fts WHERE exports_fts MATCH ?)`,
		like:     `(title LIKE ? ESCAPE '\' OR path LIKE ? ESCAPE '\')`,
		likeArgs: 2,
		category: func(c string) bool { return c == "" || c == store.LibExport },
	},
	// Transcripts (M2): a transcripts table adds its part here; until then
	// transcript projects are listed through the projects part.
}

// query builds the UNION of the parts that can list category, matching
// search. top > 0 keeps each part's newest top rows (offset+limit of the
// page: the page is among them), ordered as Search orders the whole, so
// the outer sort reads top rows a part instead of every row of every
// table (perf review 11-02 H1: 31 ms for 10 000 takes, each script read).
func libQuery(category, search string, top int) (string, []any) {
	search = strings.TrimSpace(search)
	var parts []string
	var args []any
	for _, p := range libParts {
		if !p.category(category) {
			continue
		}
		var where []string
		if p.where != "" {
			where = append(where, p.where)
		}
		if category != "" && p.catWhere != nil {
			w, a := p.catWhere(category)
			where, args = append(where, w), append(args, a...)
		}
		if search != "" {
			if useFTS(search) {
				where, args = append(where, p.fts), append(args, ftsPhrase(search))
			} else {
				where = append(where, p.like)
				for range p.likeArgs {
					args = append(args, likePattern(search))
				}
			}
		}
		sql := p.sql
		if len(where) > 0 {
			sql += ` WHERE ` + strings.Join(where, ` AND `)
		}
		if top > 0 {
			sql = `SELECT * FROM (` + sql + ` ORDER BY at DESC, id DESC LIMIT ?)`
			args = append(args, top)
		}
		parts = append(parts, sql)
	}
	return strings.Join(parts, "\nUNION ALL\n"), args
}

// libCategory files a row: projects and renders by kind, the rest by source.
func libCategory(source, kind string) string {
	switch source {
	case "project", "render":
		return kind
	}
	return source
}

func (r *libraryRepo) Search(ctx context.Context, q store.LibraryQuery) ([]store.LibraryItem, error) {
	top := 0
	if q.Limit > 0 {
		top = q.Limit + max(q.Offset, 0)
	}
	sql, args := libQuery(q.Category, q.Search, top)
	if sql == "" {
		return nil, nil
	}
	sql, args = page(`SELECT src, id, cat, title, detail, at FROM (`+sql+`) ORDER BY at DESC, id DESC`, args, q.Limit, q.Offset)
	rows, err := r.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, wrap("library", err)
	}
	out := make([]store.LibraryItem, 0, len(rows.Values))
	for _, row := range rows.Values {
		src := asString(row[0])
		out = append(out, store.LibraryItem{Source: src, ID: asString(row[1]), Category: libCategory(src, asString(row[2])),
			Title: asString(row[3]), Detail: asString(row[4]), Updated: fromMS(row[5])})
	}
	return out, nil
}

func (r *libraryRepo) Counts(ctx context.Context, search string) (map[string]int, error) {
	sql, args := libQuery("", search, 0)
	rows, err := r.db.Query(ctx, `SELECT src, cat, COUNT(*) FROM (`+sql+`) GROUP BY src, cat`, args...)
	if err != nil {
		return nil, wrap("library counts", err)
	}
	out := map[string]int{}
	for _, row := range rows.Values {
		n := int(asInt(row[2]))
		out[libCategory(asString(row[0]), asString(row[1]))] += n
		out[""] += n
	}
	return out, nil
}
