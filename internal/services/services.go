// Package services implements AudioSmith's domain services (core.*Service)
// over a store.Store, a jobs.Scheduler and the core.Bus: the take library
// and generation, voice and project libraries, and debounced drafts.
//
// Every service is goroutine-safe, returns copies, wraps core.Err*
// sentinels and publishes its event on the bus after the change is
// durable, never while holding a lock. Bus handlers the services install
// (TakeService listens for jobs.Updated) hand slow work to goroutines.
//
// Imports: core, store, jobs, engine/proto (the tts.synthesize params a
// job carries) and i18n (job titles).
package services

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/store"
)

// Config wires the services. Store and Bus are required.
type Config struct {
	Store store.Store
	Bus   *core.Bus
	// Scheduler runs generation jobs. Nil until the job queue is wired:
	// Generate then fails with core.ErrUnsupported.
	Scheduler jobs.Scheduler
	// Licences gates models whose licence needs accepting; nil gates nothing.
	Licences core.LicenceService
	// Ready reports whether a model can be used now: nil, or an error
	// wrapping core.ErrNotDownloaded (from the hub). Nil skips the check.
	Ready func(ctx context.Context, model string) error
	// DraftDelay is how long drafts wait for more edits before they are
	// written; 0 means 750 ms.
	DraftDelay time.Duration
	// Now is the clock (tests); nil means time.Now.
	Now func() time.Time
	// Async runs the background work bus handlers hand off (saving the take
	// a job made); nil means a new goroutine. Tests without goroutines
	// pass a function that runs it at once.
	Async func(func())
	// Logf reports failures of background work (saving a generated take, a
	// draft); nil means log.Printf.
	Logf func(format string, args ...any)

	// Synth returns the executor of one tts.synthesize call (the engine's,
	// engine.Executor), asked each time a render starts, so the engine
	// may be connected after the services. nil, or a nil executor, fails
	// renders with engine_unavailable (retryable).
	Synth func() jobs.Executor
	// ModelPath returns the local snapshot directory of a model, or an
	// error wrapping core.ErrNotDownloaded (as engine.ExecutorOptions).
	// Renders fill it into every engine call; nil leaves it out.
	ModelPath func(ctx context.Context, model string) (string, error)
	// Models describes a model (from the catalogue); nil uses defaults.
	Models func(repo string) ModelInfo
	// Replace maps a recipe that names a model AudioSmith removed to its
	// replacement (the catalogue's alias table: model, and a voice,
	// language or parameter the replacement can't take) and reports
	// whether it did. Generate, Preview and every render call it, so old
	// projects, workflows and history recipes keep working. nil maps
	// nothing.
	Replace func(t *core.Take) bool
	// CacheDir holds the render's chunk cache, temporary files and
	// previews; "" means AudioSmith in the system's temporary directory.
	CacheDir string
	// KeepTakes caps the history (settings.Studio.KeepTakes): beyond it
	// the oldest takes neither starred nor in a project are deleted. nil or
	// 0 keeps every take.
	KeepTakes func() int

	// M3 (longform.go). DataDir holds longform renders (<DataDir>/renders);
	// "" means the parent of the store's audio directory. Encode returns
	// the engine's media.encode executor, or nil while the engine has
	// none: books are then WAV only.
	DataDir string
	Encode  func() jobs.Executor

	// Watermark marks every finished render (watermark.go); nil: none.
	Watermark WatermarkFunc

	// Engine returns the engine's executor of a method other than
	// tts.synthesize, for renders that align or check what they made
	// (story tracks: Aligner, Recognizer are the forced aligner's and
	// the speech recognizer's repos); nil: renders do neither.
	Engine              func(method string) jobs.Executor
	Aligner, Recognizer string
}

// Services is the set of services over one store, as core.Services for
// the UI and the Local API.
type Services struct {
	core.Services
	// Pronunciation is the pronunciation dictionary.
	Pronunciation *Pronunciation
	// Render is the take service's render pipeline and history tools
	// beyond core.TakeService (previews, Check audio, pruning).
	Render *Takes
	// Longform renders Stories and Audiobook projects; Library saves them
	// and searches everything the Projects page lists (M3).
	Longform *Longform
	Library  *Library
	takes    *Takes
	drafts   *Drafts
}

// New builds the services. Close releases them.
func New(cfg Config) *Services {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logf == nil {
		cfg.Logf = log.Printf
	}
	if cfg.Async == nil {
		cfg.Async = func(fn func()) { go fn() }
	}
	if cfg.DraftDelay <= 0 {
		cfg.DraftDelay = 750 * time.Millisecond
	}
	if cfg.CacheDir == "" {
		cfg.CacheDir = filepath.Join(os.TempDir(), "AudioSmith")
	}
	pron := newPronunciation(cfg.Store.Documents(), cfg.Bus)
	s := &Services{
		Pronunciation: pron,
		takes:         newTakes(cfg, pron),
		drafts:        newDrafts(cfg),
	}
	s.Render = s.takes
	projects := &Projects{store: cfg.Store.Projects(), bus: cfg.Bus, now: cfg.Now}
	s.Services = core.Services{
		Bus:      cfg.Bus,
		Takes:    s.takes,
		Voices:   newVoices(cfg), // voices.go
		Projects: projects,
		Licences: cfg.Licences,
		Drafts:   s.drafts,
	}
	s.Longform = newLongform(cfg, s.takes) // M3: longform.go
	projects.dropped = func(ctx context.Context, id string) {
		if n, err := s.Longform.DropProject(ctx, id); n > 0 || err != nil {
			cfg.Logf("project %s deleted: removed %d unfinished renders (%v)", id, n, err)
		}
	}
	s.Library = newLibrary(cfg, projects)
	return s
}

// Close writes pending drafts, stops listening to the bus and waits for
// takes being saved. The store stays open: its owner closes it after.
//
// Drafts go first: they are the user's edits. The waits for renders and
// takes in progress end with ctx (#101 L6); what still runs then is left.
func (s *Services) Close(ctx context.Context) error {
	derr := s.drafts.Close(ctx)
	var errs []error
	for _, c := range []struct {
		what  string
		close func()
	}{{"longform", s.Longform.Close}, {"takes", func() { s.takes.Close() }}} {
		done := make(chan struct{})
		go func() {
			c.close()
			close(done)
		}()
		select {
		case <-done:
		case <-ctx.Done():
			errs = append(errs, fmt.Errorf("closing %s: %w", c.what, ctx.Err()))
		}
	}
	return errors.Join(append([]error{derr}, errs...)...)
}
