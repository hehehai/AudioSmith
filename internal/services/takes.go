package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/store"
)

// Takes implements core.TakeService.
//
// Generate submits a "take.render" job (render.go) owned by "take:<id>"
// and keeps the take's recipe until the job ends. It listens to
// jobs.Updated: when the job succeeds, the take is filled from the
// result, saved and announced with TakeCreated (on a goroutine, as bus
// handlers must not block), and the history is pruned to its cap. A job
// that finishes after a restart (re-queued because it is idempotent) has
// no recipe in memory: the take is then rebuilt from the job's params.
// Jobs of the M0 kind, a single "tts.synthesize" owned by a take, are
// still completed the same way.
type Takes struct {
	store    store.Takes
	bus      *core.Bus
	sched    jobs.Scheduler
	licences core.LicenceService
	ready    func(context.Context, string) error
	now      func() time.Time
	logf     func(string, ...any)
	async    func(func())

	rmw     sync.Mutex // serializes read-modify-write of takes
	mu      sync.Mutex // guards pending, saving and closed; never held during I/O
	pending map[string]core.Take
	saving  map[string]chan struct{} // closed once the take's job ended and its take is saved (or not)
	closed  bool
	wg      sync.WaitGroup
	unsub   func()

	render    *renderer
	models    func(repo string) ModelInfo
	replace   func(*core.Take) bool
	keepTakes func() int
	cacheDir  string
}

var _ core.TakeService = (*Takes)(nil)

func newTakes(cfg Config, pron *Pronunciation) *Takes {
	t := &Takes{
		store: cfg.Store.Takes(), bus: cfg.Bus, sched: cfg.Scheduler, licences: cfg.Licences,
		ready: cfg.Ready, now: cfg.Now, logf: cfg.Logf, async: cfg.Async, pending: map[string]core.Take{}, saving: map[string]chan struct{}{},
		models: cfg.Models, replace: cfg.Replace, keepTakes: cfg.KeepTakes, cacheDir: cfg.CacheDir,
	}
	t.render = &renderer{
		synth: cfg.Synth, modelPath: cfg.ModelPath, licences: cfg.Licences, models: cfg.Models, replace: cfg.Replace,
		pron: pron, cacheDir: cfg.CacheDir, bus: cfg.Bus, logf: cfg.Logf, watermark: cfg.Watermark,
		engine: cfg.Engine, aligner: cfg.Aligner, recognizer: cfg.Recognizer,
	}
	if cfg.Scheduler != nil {
		cfg.Scheduler.Register(KindRender, t.render)
	}
	t.unsub = core.On(cfg.Bus, t.onJob)
	return t
}

// Executor is the render executor (registered on the scheduler by New),
// for tests that run renders themselves.
func (s *Takes) Executor() jobs.Executor { return s.render }

func (s *Takes) model(repo string) ModelInfo {
	if s.models != nil {
		return s.models(repo)
	}
	return ModelInfo{Traits: TraitsFor("", nil)}
}

// Close stops listening for jobs and waits for takes being saved.
func (s *Takes) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.unsub()
	s.wg.Wait()
	s.mu.Lock()
	for id := range s.saving {
		s.saved(id)
	}
	s.mu.Unlock()
	return nil
}

// Saved waits until the take id that Generate queued is saved, or its
// job ended without a take; at once for takes not being made. Its job
// ends before the take is saved (onJob saves it on a goroutine): a
// caller that reads the history right after the job (the Local API's
// generate_speech, then list_takes or list_assets) waits here first.
func (s *Takes) Saved(ctx context.Context, id string) error {
	s.mu.Lock()
	done := s.saving[id]
	s.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// saved releases Saved's waiters for id; s.mu is held.
func (s *Takes) saved(id string) {
	if done := s.saving[id]; done != nil {
		close(done)
		delete(s.saving, id)
	}
}

func (s *Takes) List(ctx context.Context, q core.TakeQuery) ([]core.Take, error) {
	return s.store.List(ctx, q)
}

func (s *Takes) Get(ctx context.Context, id string) (core.Take, error) {
	return s.store.Get(ctx, id)
}

func (s *Takes) AudioDir() string { return s.store.AudioDir() }

func (s *Takes) AudioPath(t core.Take) string { return s.store.AudioPath(t) }

// SynthesizeParams is the engine request for a recipe: zero parameters
// are left out so the model's defaults apply.
func SynthesizeParams(t core.Take, output string, stream bool) proto.SynthesizeParams {
	p := proto.SynthesizeParams{
		Model: t.Model, Text: t.Text, Voice: t.Voice, Language: t.Language,
		MaxTokens: t.MaxTokens, RefAudio: t.RefAudio, RefText: t.RefText, VoicePrompt: t.VoicePrompt,
		Output: proto.Output{Path: output}, Stream: stream,
	}
	if t.Guidance != 0 {
		p.Guidance = &t.Guidance
	}
	if t.Speed != 0 {
		p.Speed = &t.Speed
	}
	if t.Temperature != 0 {
		p.Temperature = &t.Temperature
	}
	if t.TopP != 0 {
		p.TopP = &t.TopP
	}
	if t.Seed != 0 {
		p.Seed = &t.Seed
	}
	if t.Duration != 0 {
		p.Duration = &t.Duration
	}
	p.Steps = t.Steps
	return p
}

// check validates a recipe and checks its model may run now.
func (s *Takes) check(ctx context.Context, r core.Take) (core.Take, error) {
	r.Text, r.Model = strings.TrimSpace(r.Text), strings.TrimSpace(r.Model)
	if s.replace != nil {
		s.replace(&r)
	}
	if r.Text == "" {
		return r, fmt.Errorf("generate: no text: %w", core.ErrInvalid)
	}
	if r.Model == "" {
		return r, fmt.Errorf("generate: no model: %w", core.ErrInvalid)
	}
	if s.sched == nil {
		return r, fmt.Errorf("generate: no job scheduler: %w", core.ErrUnsupported)
	}
	if err := checkReference(r, s.model(r.Model)); err != nil {
		return r, err
	}
	if s.licences != nil {
		if err := s.licences.Check(ctx, r.Model); err != nil {
			return r, err
		}
	}
	if s.ready != nil {
		if err := s.ready(ctx, r.Model); err != nil {
			return r, err
		}
	}
	return r, nil
}

// HasReference reports whether a recipe gives a cloning model a voice to
// clone: a reference clip or an encoded voice prompt. Without one, a model
// that only clones refuses it (checkReference) and the others speak in
// their default voice; the Studio's hints ask the same question.
func HasReference(t core.Take) bool {
	return strings.TrimSpace(t.RefAudio) != "" || strings.TrimSpace(t.VoicePrompt) != ""
}

// checkReference refuses a recipe for a model that only clones voices
// (ModelInfo.NeedsReference) when it has neither a reference clip nor a
// voice prompt: the engine would fail it.
func checkReference(t core.Take, info ModelInfo) error {
	if !info.NeedsReference || HasReference(t) {
		return nil
	}
	name := info.Label
	if name == "" {
		name = t.Model
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
	}
	return &core.NeedsReferenceError{Model: name}
}

// renderRecipe is the recipe part of a take's render options (results
// cleared); nil asks for the defaults: 16-bit, Even out volume on.
func renderRecipe(r *core.TakeRender) *core.TakeRender {
	if r == nil {
		return &core.TakeRender{Master: MasterDefault}
	}
	return &core.TakeRender{Bits: r.Bits, Master: r.Master, NoNormalize: r.NoNormalize, SeedPinned: r.SeedPinned}
}

func (s *Takes) Generate(ctx context.Context, req core.TakeRequest) (string, error) {
	r, err := s.check(ctx, req.Recipe)
	if err != nil {
		return "", err
	}
	now := s.now()
	t := r
	if t.ID == "" {
		t.ID = core.NewID(now)
	} else if filepath.Base(t.ID) != t.ID || strings.ContainsAny(t.ID, `/\.`) {
		return "", fmt.Errorf("generate: take id %q: %w", t.ID, core.ErrInvalid)
	}
	t.CreatedAt = now
	t.File = t.ID + ".wav"
	t.JobID, t.Starred = "", false
	t.SampleRate, t.AudioSeconds, t.GenSeconds = 0, 0, 0
	t.Render = renderRecipe(t.Render)
	// A seed in the request is the seed to use (pinned): every chunk
	// gets it, and Reuse brings it back.
	t.Render.SeedPinned = t.Seed != 0
	info := s.model(t.Model)
	info.FillDefaults(&t, "") // the take records what was used
	params, err := json.Marshal(RenderParams{Take: t, Lang: info.Lang(t.Language, t.Voice),
		Output: s.store.AudioPath(t), Stream: req.Stream || !req.Background})
	if err != nil {
		return "", err
	}
	priority := jobs.PriorityInteractive
	if req.Background {
		priority = jobs.PriorityBackground
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return "", fmt.Errorf("generate: %w", core.ErrCancelled)
	}
	// Registered before Submit: the job may end before Submit returns.
	s.pending[t.ID] = t
	if s.saving[t.ID] == nil {
		s.saving[t.ID] = make(chan struct{})
	}
	s.mu.Unlock()
	id, err := s.sched.Submit(ctx, jobs.Spec{
		Kind:     KindRender,
		Lane:     jobs.LaneGPU,
		Priority: priority,
		Params:   params,
		Title:    i18n.T("Generate take"),
		Owner:    "take:" + t.ID,
		// The take's file is written atomically, and the chunks made
		// before a crash come from the cache: a run can simply run again.
		MaxAttempts: 2,
		Idempotent:  true,
	})
	if err != nil {
		s.mu.Lock()
		delete(s.pending, t.ID)
		s.saved(t.ID)
		s.mu.Unlock()
		return "", err
	}
	return id, nil
}

// onJob is the jobs.Updated handler: it must not block.
func (s *Takes) onJob(u jobs.Updated) {
	j := u.Job
	if (j.Kind != KindRender && j.Kind != proto.MethodTTSSynthesize) || !j.State.Terminal() {
		return
	}
	id, ok := strings.CutPrefix(j.Owner, "take:")
	if !ok || id == "" {
		return
	}
	s.mu.Lock()
	t, known := s.pending[id]
	delete(s.pending, id)
	if s.closed || j.State != jobs.StateSucceeded {
		// Failures and cancellations reach the UI as the job's own events.
		s.saved(id)
		s.mu.Unlock()
		return
	}
	s.wg.Add(1)
	s.mu.Unlock()
	s.async(func() {
		defer s.wg.Done()
		if err := s.complete(j, id, t, known); err != nil {
			s.logf("take %s from job %s: %v", id, j.ID, err)
		}
		s.mu.Lock()
		s.saved(id)
		s.mu.Unlock()
	})
}

// complete saves the take a succeeded job made and publishes TakeCreated.
func (s *Takes) complete(j jobs.Job, id string, t core.Take, known bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if j.Kind == KindRender {
		return s.completeRender(ctx, j, id, t, known)
	}
	if !known {
		if _, err := s.store.Get(ctx, id); err == nil {
			return nil // already saved
		}
		var p proto.SynthesizeParams
		if err := json.Unmarshal(j.Params, &p); err != nil {
			return fmt.Errorf("params: %w", err)
		}
		t = core.Take{
			ID: id, CreatedAt: j.Created, File: filepath.Base(p.Output.Path),
			Text: p.Text, Model: p.Model, Voice: p.Voice, Language: p.Language,
			MaxTokens: p.MaxTokens, RefAudio: p.RefAudio, RefText: p.RefText,
		}
		if p.Speed != nil {
			t.Speed = *p.Speed
		}
		if p.Temperature != nil {
			t.Temperature = *p.Temperature
		}
		if p.TopP != nil {
			t.TopP = *p.TopP
		}
		if p.Seed != nil {
			t.Seed = *p.Seed
		}
		if p.Duration != nil {
			t.Duration = *p.Duration
		}
		t.Steps = p.Steps
	}
	var res proto.SynthesizeResult
	if err := json.Unmarshal(j.Result, &res); err != nil {
		return fmt.Errorf("result: %w", err)
	}
	t.JobID = j.ID
	t.SampleRate, t.AudioSeconds, t.GenSeconds = res.SampleRate, res.AudioSeconds, res.Seconds
	if res.Seed != nil {
		t.Seed = *res.Seed
	}
	if res.Output != "" {
		t.File = filepath.Base(res.Output)
	}
	if err := s.store.Put(ctx, t); err != nil {
		return err
	}
	s.bus.Publish(core.TakeCreated{Take: t})
	return nil
}

// completeRender saves the take a render made, then prunes the history.
func (s *Takes) completeRender(ctx context.Context, j jobs.Job, id string, t core.Take, known bool) error {
	var p RenderParams
	if err := json.Unmarshal(j.Params, &p); err != nil {
		return fmt.Errorf("params: %w", err)
	}
	if !known {
		if _, err := s.store.Get(ctx, id); err == nil {
			return nil // already saved
		}
		t = p.Take
		if t.CreatedAt.IsZero() {
			t.CreatedAt = j.Created
		}
	}
	var res RenderResult
	if err := json.Unmarshal(j.Result, &res); err != nil {
		return fmt.Errorf("result: %w", err)
	}
	t.ID = id
	t.JobID = j.ID
	t.SampleRate, t.AudioSeconds, t.GenSeconds = res.SampleRate, res.AudioSeconds, res.Seconds
	if res.Seed != 0 {
		t.Seed = res.Seed
	}
	if res.Output != "" {
		t.File = filepath.Base(res.Output)
	}
	rnd := res.Render
	t.Render = &rnd
	if err := s.store.Put(ctx, t); err != nil {
		return err
	}
	s.bus.Publish(core.TakeCreated{Take: t})
	if _, err := s.Prune(ctx); err != nil {
		s.logf("pruning the history: %v", err)
	}
	return nil
}

// Prune deletes the oldest takes beyond the history's cap (KeepTakes),
// sparing starred takes and takes of projects, and returns how many it
// deleted.
func (s *Takes) Prune(ctx context.Context) (int, error) {
	keep := 0
	if s.keepTakes != nil {
		keep = s.keepTakes()
	}
	if keep <= 0 {
		return 0, nil
	}
	all, err := s.store.List(ctx, core.TakeQuery{})
	if err != nil {
		return 0, err
	}
	n, deleted := 0, 0
	for _, t := range all {
		if t.Starred || t.ProjectID != "" {
			continue
		}
		n++
		if n <= keep {
			continue
		}
		if err := s.Delete(ctx, t.ID); err != nil && !errors.Is(err, core.ErrNotFound) {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

// Preview renders a recipe without making a take (the pronunciation
// dictionary's "Test a sentence"): a take.render job owned by
// "preview:<id>" writing a WAV in the cache. It returns the job's id and
// the file it writes; the job's chunks stream the audio.
func (s *Takes) Preview(ctx context.Context, recipe core.Take) (jobID, output string, err error) {
	r, err := s.check(ctx, recipe)
	if err != nil {
		return "", "", err
	}
	dir := filepath.Join(s.cacheDir, "previews")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	removeOld(dir, 24*time.Hour)
	t := r
	t.ID = core.NewID(s.now())
	t.File = t.ID + ".wav"
	t.Render = renderRecipe(t.Render)
	t.Render.SeedPinned = t.Seed != 0
	info := s.model(t.Model)
	info.FillDefaults(&t, "")
	output = filepath.Join(dir, t.File)
	params, err := json.Marshal(RenderParams{Take: t, Lang: info.Lang(t.Language, t.Voice), Output: output, Stream: true})
	if err != nil {
		return "", "", err
	}
	jobID, err = s.sched.Submit(ctx, jobs.Spec{Kind: KindRender, Lane: jobs.LaneGPU, Priority: jobs.PriorityInteractive,
		Params: params, Title: i18n.T("Preview"), Owner: "preview:" + t.ID})
	return jobID, output, err
}

// removeOld deletes files in dir older than age.
func removeOld(dir string, age time.Duration) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if fi, err := e.Info(); err == nil && !e.IsDir() && time.Since(fi.ModTime()) > age {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// Check runs Check audio over a take's file, keeps the findings with the
// take and publishes TakeUpdated.
func (s *Takes) Check(ctx context.Context, id string) (core.Take, error) {
	t, err := s.store.Get(ctx, id)
	if err != nil {
		return t, err
	}
	issues, err := CheckAudio(ctx, s.store.AudioPath(t))
	if err != nil {
		return t, err
	}
	s.rmw.Lock()
	t, err = s.store.Get(ctx, id)
	if err == nil {
		r := core.TakeRender{}
		if t.Render != nil {
			r = *t.Render
		}
		r.Issues, r.QCDone = issues, true
		t.Render = &r
		err = s.store.Put(ctx, t)
	}
	s.rmw.Unlock()
	if err != nil {
		return t, err
	}
	s.bus.Publish(core.TakeUpdated{Take: t})
	return t, nil
}

func (s *Takes) Add(ctx context.Context, t core.Take) (core.Take, error) {
	if t.File == "" || filepath.Base(t.File) != t.File {
		return t, fmt.Errorf("add take: file %q: %w", t.File, core.ErrInvalid)
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = s.now()
	}
	if t.ID == "" {
		t.ID = core.NewID(t.CreatedAt)
	}
	if err := s.store.Put(ctx, t); err != nil {
		return t, err
	}
	s.bus.Publish(core.TakeCreated{Take: t})
	return t, nil
}

func (s *Takes) Delete(ctx context.Context, id string) error {
	err := s.store.Delete(ctx, id)
	logDelete(ctx, "take", id, "", err)
	if err != nil {
		return err
	}
	s.bus.Publish(core.TakeDeleted{ID: id})
	return nil
}

func (s *Takes) SetStarred(ctx context.Context, id string, starred bool) error {
	s.rmw.Lock()
	t, err := s.store.Get(ctx, id)
	changed := err == nil && t.Starred != starred
	if changed {
		t.Starred = starred
		err = s.store.Put(ctx, t)
	}
	s.rmw.Unlock()
	if err != nil || !changed {
		return err
	}
	s.bus.Publish(core.TakeUpdated{Take: t})
	return nil
}
