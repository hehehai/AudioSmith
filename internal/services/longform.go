package services

// The longform service: renders of Stories and Audiobook projects.
//
// A render is a DAG of jobs owned by "longform:<render id>":
//
//	chapter 1 ─┐
//	chapter 2 ─┼─▶ assemble (cpu lane) ─▶ book.wav, cues.txt, stems, report.json, (M4B)
//	chapter n ─┘
//
// The chapter jobs (gpu lane, normal priority, idempotent) run one after
// the other in chapter order — the gpu lane has one slot and the job ids
// sort in the order they were made — so a Studio take may run between two
// chapters. The assemble job waits on a lane of its own, so it never holds
// the gpu lane while chapters run (the deadlock thread G found for a join
// job inside the gpu lane). A failed chapter fails the assembly; the other
// chapters still render. Retry (Resume) submits the render's jobs again:
// chapters whose files were made from the same program are kept, the
// others come mostly from the chunk cache. A render the app quit in the
// middle of goes on at the next start (its jobs are idempotent).
//
// Only one full render runs at a time; auditions (one chapter or line, for
// listening) may run beside it.
//
// The render's record (core.Render, migration 003) keeps the manifest and
// the chapters' states; RenderChanged is published on every change.
//
// Files: longform.go (types, the service, the library: get, list,
// delete, reconcile), longform_start.go (start, stop, resume),
// longform_jobs.go (job updates), longform_render.go and its program /
// chapter files (one chapter), longform_assemble.go (the book).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/store"
)

// Chapter states.
const (
	ChapterPending   = "pending"
	ChapterRendering = "rendering"
	ChapterDone      = "done"
	ChapterCached    = "cached" // no synthesis was needed
	ChapterFailed    = "failed"
	ChapterStopped   = "stopped"
)

// RenderBody is a render's JSON (core.Render.Body).
type RenderBody struct {
	// Spec is the manifest. Bodies published with RenderChanged carry its
	// summary only (Summary), not its chapters and voices.
	Spec     LongformSpec    `json:"spec"`
	Summary  RenderSummary   `json:"summary"`
	Dir      string          `json:"dir"` // the render's files
	Chapters []RenderChapter `json:"chapters"`
	// The assembly: its job, whether it runs and how far.
	AssembleJob string          `json:"assembleJob,omitempty"`
	Assembling  bool            `json:"assembling,omitempty"`
	Assembled   float64         `json:"assembled,omitempty"`
	Output      *AssembleResult `json:"output,omitempty"`
	Error       *jobs.Error     `json:"error,omitempty"`
	Stopping    bool            `json:"stopping,omitempty"`
	Runs        int             `json:"runs"` // 1 + how often it was resumed
	Started     time.Time       `json:"started"`
	Finished    time.Time       `json:"finished,omitzero"`
}

// RenderChapter is a chapter of a render.
type RenderChapter struct {
	Title        string      `json:"title,omitempty"`
	Chars        int         `json:"chars"` // runes to say, for the ETA
	Lines        int         `json:"lines"`
	Job          string      `json:"job,omitempty"`
	State        string      `json:"state"`
	Fraction     float64     `json:"fraction,omitempty"`
	Error        *jobs.Error `json:"error,omitempty"`
	File         string      `json:"file,omitempty"`
	AudioSeconds float64     `json:"audioSeconds,omitempty"`
	Seconds      float64     `json:"seconds,omitempty"` // wall time
	Chunks       int         `json:"chunks,omitempty"`
	Cached       int         `json:"cached,omitempty"` // chunks from the cache
	Started      time.Time   `json:"started,omitzero"`
	Finished     time.Time   `json:"finished,omitzero"`
}

// RenderSummary is how a render was made, for the library's details.
type RenderSummary struct {
	Model    string     `json:"model"`
	Lang     string     `json:"lang,omitempty"`
	Format   string     `json:"format,omitempty"`
	Loudness string     `json:"loudness,omitempty"`
	Voices   []string   `json:"voices,omitempty"`   // VoiceIDs, or speakers ("speaker:<id>")
	MinSpeed float64    `json:"minSpeed,omitempty"` // of the lines
	MaxSpeed float64    `json:"maxSpeed,omitempty"`
	Chapters int        `json:"chapters"`
	Lines    int        `json:"lines"`
	Words    int        `json:"words"`
	Chars    int        `json:"chars"` // unspaced-script characters
	Prod     Production `json:"production"`
	Stems    bool       `json:"stems,omitempty"`
	// Credits are the attributions the audio must carry when published
	// (its VOICEVOX characters', thread #171), also in the render's
	// folder as credits.txt.
	Credits []string `json:"credits,omitempty"`
}

// Done reports whether every chapter is made.
func (b *RenderBody) Done() int {
	n := 0
	for _, c := range b.Chapters {
		if c.State == ChapterDone || c.State == ChapterCached {
			n++
		}
	}
	return n
}

// Failed counts the failed chapters.
func (b *RenderBody) Failed() int {
	n := 0
	for _, c := range b.Chapters {
		if c.State == ChapterFailed {
			n++
		}
	}
	return n
}

// DecodeRender reads a render's body.
func DecodeRender(r core.Render) (RenderBody, error) {
	var b RenderBody
	if len(r.Body) == 0 {
		return b, nil
	}
	err := json.Unmarshal(r.Body, &b)
	return b, err
}

// Longform runs and keeps longform renders.
type Longform struct {
	renders  store.Renders
	sched    jobs.Scheduler
	bus      *core.Bus
	now      func() time.Time
	logf     func(string, ...any)
	async    func(func())
	dataDir  string
	cacheDir string
	encode   func() jobs.Executor
	render   *renderer

	mu   sync.Mutex
	live map[string]*liveRender // renders followed in memory, by id

	qmu      sync.Mutex
	queue    []jobs.Job
	draining bool
	closed   bool
	drained  sync.WaitGroup
	unsub    func()
}

type liveRender struct {
	rec  core.Render
	body RenderBody
}

func newLongform(cfg Config, t *Takes) *Longform {
	s := &Longform{renders: cfg.Store.Renders(), sched: cfg.Scheduler, bus: cfg.Bus, now: cfg.Now, logf: cfg.Logf,
		async: cfg.Async, dataDir: cfg.DataDir, cacheDir: cfg.CacheDir, encode: cfg.Encode, render: t.render,
		live: map[string]*liveRender{}}
	if s.dataDir == "" {
		s.dataDir = filepath.Dir(cfg.Store.Takes().AudioDir())
	}
	t.render.dataDir = s.dataDir // longform chunks live there (longformChunkDir)
	if cfg.Scheduler != nil {
		cfg.Scheduler.Register(KindChapter, chapterRenderer{t.render})
		cfg.Scheduler.Register(KindAssemble, assembler{s})
	}
	s.unsub = core.On(cfg.Bus, s.onJob)
	return s
}

// ChapterExecutor and AssembleExecutor are the executors New registers,
// for tests that run them.
func (s *Longform) ChapterExecutor() jobs.Executor { return chapterRenderer{s.render} }

func (s *Longform) AssembleExecutor() jobs.Executor { return assembler{s} }

// EncodeSupported reports whether books can be encoded as M4B (the engine
// has media.encode).
func (s *Longform) EncodeSupported() bool { return s.encode != nil && s.encode() != nil }

// Close stops following jobs and waits for the updates being applied.
func (s *Longform) Close() {
	s.unsub()
	s.qmu.Lock()
	s.closed = true
	s.queue = nil
	s.qmu.Unlock()
	s.drained.Wait()
}

// Get returns a render with its whole body (manifest included).
func (s *Longform) Get(ctx context.Context, id string) (core.Render, RenderBody, error) {
	return s.load(ctx, id)
}

// List lists renders (their bodies as stored).
func (s *Longform) List(ctx context.Context, q store.RenderQuery) ([]core.Render, error) {
	out, err := s.renders.List(ctx, q)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	for i, r := range out {
		if lr := s.live[r.ID]; lr != nil {
			out[i].State, out[i].UpdatedAt = lr.rec.State, lr.rec.UpdatedAt
		}
	}
	s.mu.Unlock()
	return out, nil
}

// Active is the full render running now, if any (published body).
func (s *Longform) Active() (core.Render, bool) {
	s.mu.Lock()
	var id string
	for _, l := range s.live {
		if !l.rec.Audition && l.rec.State == core.RenderRunning {
			id = l.rec.ID
		}
	}
	s.mu.Unlock()
	if id == "" {
		return core.Render{}, false
	}
	return s.snapshot(id)
}

// Delete removes a render's record and files; a running render is refused.
func (s *Longform) Delete(ctx context.Context, id string) error {
	err := s.delete(ctx, id)
	logDelete(ctx, "render", id, "", err)
	return err
}

func (s *Longform) delete(ctx context.Context, id string) error {
	rec, body, err := s.load(ctx, id)
	if err != nil {
		return err
	}
	if rec.State == core.RenderRunning {
		return fmt.Errorf("longform: %q is rendering: %w", rec.Title, core.ErrBusy)
	}
	if err := s.renders.Delete(ctx, id); err != nil {
		return err
	}
	s.forget(id)
	s.removeDir(body.Dir)
	s.bus.Publish(core.RenderChanged{Render: rec, Deleted: true})
	return nil
}

// removeDir deletes a render's folder if it is one of ours.
func (s *Longform) removeDir(dir string) {
	for _, root := range []string{filepath.Join(s.dataDir, "renders"), filepath.Join(s.cacheDir, "auditions")} {
		if rel, err := filepath.Rel(root, dir); err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !strings.Contains(rel, string(filepath.Separator)) {
			os.RemoveAll(dir)
		}
	}
}

// pruneAuditions deletes auditions older than an hour.
func (s *Longform) pruneAuditions(ctx context.Context) {
	old, err := s.renders.List(ctx, store.RenderQuery{Auditions: true, States: []core.RenderState{core.RenderDone, core.RenderFailed, core.RenderStopped}})
	if err != nil {
		return
	}
	for _, r := range old {
		if r.Audition && s.now().Sub(r.UpdatedAt) > time.Hour {
			s.Delete(ctx, r.ID)
		}
	}
}

// Reconcile settles renders recorded as running whose jobs are all gone
// or finished (the app stopped between two of their updates): they
// become failed, for Resume. Call it once the scheduler has started.
func (s *Longform) Reconcile(ctx context.Context) error {
	running, err := s.renders.List(ctx, store.RenderQuery{Auditions: true, States: []core.RenderState{core.RenderRunning}})
	if err != nil {
		return err
	}
	for _, r := range running {
		_, body, err := s.load(ctx, r.ID)
		if err != nil {
			continue
		}
		ids := []string{body.AssembleJob}
		for _, c := range body.Chapters {
			ids = append(ids, c.Job)
		}
		alive := false
		for _, j := range ids {
			if j == "" {
				continue
			}
			if job, err := s.sched.Get(ctx, j); err == nil {
				if !job.State.Terminal() {
					alive = true
				}
				s.apply(job) // catch up
			}
		}
		if alive {
			continue
		}
		s.mu.Lock()
		lr := s.live[r.ID]
		changed := lr != nil && lr.rec.State == core.RenderRunning
		if changed {
			lr.rec.State = core.RenderFailed
			if lr.body.Error == nil {
				lr.body.Error = &jobs.Error{Code: jobs.CodeInterrupted, Message: "AudioSmith stopped while the book rendered"}
			}
			for i := range lr.body.Chapters {
				if c := &lr.body.Chapters[i]; c.State == ChapterPending || c.State == ChapterRendering {
					c.State = ChapterStopped
				}
			}
		}
		s.mu.Unlock()
		if changed {
			s.save(ctx, r.ID)
			s.publish(r.ID)
		}
	}
	return nil
}
