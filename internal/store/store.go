// Package store is AudioSmith's persistence contract: repositories for
// each kind of record, behind which the database lives. The app uses
// internal/store/sqlitestore (SQLite through the MyGo plugin
// github.com/egoist/mygo/plugins/sqlite; schema in its migrations/*.sql),
// which imports the MVP's history.json on first run with
// internal/store/historyjson (an implementation of Takes alone, kept for
// that import and for tests).
//
// Rules:
//   - Repositories are goroutine-safe and may block on I/O: never call
//     them from a MyGo build pass. The service layer (internal/services)
//     is their only caller once it exists; the UI goes through services.
//   - Values in, values out: a repository copies what it is given and
//     returns copies.
//   - Not found is an error wrapping core.ErrNotFound.
//   - Blobs (audio, portraits, voice prompts) are files under the store's
//     directories, referenced by name; the store deletes a record's files
//     with the record.
//
// Imports: core, jobs.
package store

import (
	"context"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

// Store is the whole database. Methods returning a repository never fail;
// a store that lacks one returns a repository whose methods fail with
// core.ErrUnsupported.
type Store interface {
	Takes() Takes
	Voices() Voices
	Projects() Projects
	Licences() Licences
	// Documents holds small JSON documents by key: "settings:app" (the
	// settings.Settings document) and "draft:<page>" (core.DraftService).
	Documents() Documents
	Favorites() Favorites
	// Renders and Exports are the longform renders and the export
	// records (M3); Library searches every kind of record at once.
	Renders() Renders
	Exports() Exports
	Library() Library
	Jobs() jobs.Store
	Close() error
}

// Takes stores takes and their audio files.
type Takes interface {
	// Put inserts or replaces a take (by ID). Its audio file must already
	// be in AudioDir.
	Put(ctx context.Context, t core.Take) error
	Get(ctx context.Context, id string) (core.Take, error)
	// List returns matching takes, newest first.
	List(ctx context.Context, q core.TakeQuery) ([]core.Take, error)
	// Delete removes the take and its audio file.
	Delete(ctx context.Context, id string) error
	// AudioDir is the directory take audio lives in (the engine writes
	// there). AudioPath joins it with t.File. Both are pure.
	AudioDir() string
	AudioPath(t core.Take) string
}

// Voices stores voice profiles; their blobs live in VoiceDir()/<id>/.
type Voices interface {
	Put(ctx context.Context, v core.Voice) error
	Get(ctx context.Context, id string) (core.Voice, error)
	List(ctx context.Context, q core.VoiceQuery) ([]core.Voice, error)
	Delete(ctx context.Context, id string) error
	// Usage counts the takes and project segments that use the voice.
	Usage(ctx context.Context, id string) (core.VoiceUsage, error)
	// Links are the takes and segments that point at the voice, which
	// Delete unlinks; Relink points them at it again (undoing a delete,
	// after Put restored the profile), those linked elsewhere since left.
	Links(ctx context.Context, id string) (VoiceLinks, error)
	Relink(ctx context.Context, id string, l VoiceLinks) error
	VoiceDir() string
}

// VoiceLinks are the ids of the takes and project segments that use a
// voice.
type VoiceLinks struct {
	Takes    []string `json:"takes,omitempty"`
	Segments []string `json:"segments,omitempty"`
}

// Favorites stores sets of favorite keys by scope ("gallery": archetype
// ids, later "community:<repo>:<id>").
type Favorites interface {
	List(ctx context.Context, scope string) ([]string, error)
	// Set adds (on) or removes a key; both are idempotent.
	Set(ctx context.Context, scope, key string, on bool) error
}

// Projects stores projects; segments, renders and exports are rows of
// their own tables keyed by project id (see sqlitestore/migrations) with repositories
// added by the milestone that needs them.
type Projects interface {
	Put(ctx context.Context, p core.Project) error
	Get(ctx context.Context, id string) (core.Project, error)
	List(ctx context.Context, kind core.ProjectKind, search string) ([]core.Project, error)
	Delete(ctx context.Context, id string) error
	// PutSummary stores what the library shows of a project without
	// reading its body (JSON: lines, words, voices…), and its segments:
	// they replace the project's segments, so "where this voice has been
	// used" counts them. A segment's voice that no longer exists is kept
	// as none.
	PutSummary(ctx context.Context, id string, summary []byte, segs []Segment) error
}

// Segment is a row of a project's segments (story lines, audiobook
// chapters, dub cues): what it says and with which voice.
type Segment struct {
	ID      string
	Ord     float64
	Text    string
	Speaker string
	VoiceID string
}

// RenderQuery selects renders: the zero value is every render but
// auditions, newest first.
type RenderQuery struct {
	ProjectID string
	States    []core.RenderState
	Auditions bool // include auditions
	Limit     int
	// Light lists bodies holding only their "output" (where the book
	// is), rather than whole bodies with the book's manifest (1 MB for a
	// long book; code review 1102 perf M4); a render of no project keeps
	// its whole body (its title is its script's first words).
	Light bool
}

// Renders stores longform renders (core.Render).
type Renders interface {
	Put(ctx context.Context, r core.Render) error
	Get(ctx context.Context, id string) (core.Render, error)
	List(ctx context.Context, q RenderQuery) ([]core.Render, error)
	Delete(ctx context.Context, id string) error
}

// Exports stores export records (core.Export).
type Exports interface {
	Put(ctx context.Context, e core.Export) error
	Get(ctx context.Context, id string) (core.Export, error)
	// List returns the records whose title or path matches search ("" =
	// all), newest first; limit 0 = no limit.
	List(ctx context.Context, search string, limit int) ([]core.Export, error)
	Delete(ctx context.Context, id string) error
}

// Library categories: the filters of the Projects page.
const (
	LibAll        = ""
	LibDub        = "dub"
	LibStory      = "story"
	LibAudiobook  = "audiobook"
	LibVoice      = "voice"
	LibTranscript = "transcript"
	LibTake       = "take"
	LibExport     = "export"
)

// LibraryQuery selects library items.
type LibraryQuery struct {
	Category string // a Lib* category; "" = all
	Search   string // full text over titles, scripts, names, paths
	Limit    int    // 0 = no limit
	Offset   int
}

// LibraryItem is one row of the library: a project, a finished render, a
// voice, a take or an export record.
type LibraryItem struct {
	Source   string // "project", "render", "voice", "take", "export"
	ID       string
	Category string // the Lib* category it is listed under
	Title    string
	// Detail is a second line chosen by source: a project's summary JSON,
	// a render's state, a voice's kind, a take's model, an export's path.
	Detail  string
	Updated time.Time
}

// Library searches across the stores.
type Library interface {
	Search(ctx context.Context, q LibraryQuery) ([]LibraryItem, error)
	// Counts returns how many items each category has for search (the
	// key "" is the total).
	Counts(ctx context.Context, search string) (map[string]int, error)
}

// Licences stores licence acceptances.
type Licences interface {
	Put(ctx context.Context, a core.LicenceAcceptance) error
	Delete(ctx context.Context, licence, model string) error
	List(ctx context.Context) ([]core.LicenceAcceptance, error)
}

// Documents is a key → JSON store for small documents.
type Documents interface {
	// Get returns the document and whether it exists.
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Put(ctx context.Context, key string, doc []byte) error
	Delete(ctx context.Context, key string) error
}
