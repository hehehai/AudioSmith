package services

// Dubbing (M4): the dub service keeps dub projects (internal/dub.Project,
// migration 005's table, files in <data>/dubs/<id>/) and runs their
// pipeline as a graph of jobs owned by "dub:<id>":
//
//	fetch (net: yt-dlp, URL sources)
//	 └ ingest (media: probe, decode, 16 kHz copy, peaks, onsets, poster, preview proxy)
//	    └ separate (gpu: voices / background; skipped when the engine cannot)
//	       └ transcribe (gpu: diarize, transcribe, align; or the captions)
//	          └ references (gpu: each speaker's clone reference, cleaned)
//	             ├ translate:es (net, or gpu for the engine's LLM) ─ synthesize:es (gpu) ─ mix:es (cpu)
//	             └ translate:fr …
//	qc:<lang> (gpu, "Verify")    export (cpu/media)
//
// Every stage is idempotent: it reads the project, skips what is done
// (files are named by their inputs' fingerprints), writes its results
// into the project (Update) and marks its StageState. A run after a crash
// or a cancel goes on where it stopped; a re-dub renders only the lines
// whose fingerprint changed. No job waits for another inside its
// executor: the scheduler's dependencies order them, so a stage never
// holds the gpu lane's one slot while waiting.
//
// With review mode on, a run stops after the transcript (Through
// references) and again after the translation; the UI's checkpoint
// banners start the next run.
//
// Files: dub.go (types, the library), dub_run.go (runs and their plan),
// dub_plumbing.go (what every stage shares), dub_stages.go and
// dub_transcribe.go (the stages up to the references), dub_render.go,
// dub_lines.go and dub_mix.go (translate, synthesize, mix, qc),
// dub_export.go.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/store"
	"github.com/hehehai/AudioSmith/internal/translate"
)

// DubOwner prefixes the owners of a dub's jobs: "dub:<id>".
const DubOwner = "dub:"

// Job kinds of the dub pipeline.
const (
	KindDubFetch      = "dub.fetch"
	KindDubIngest     = "dub.ingest"
	KindDubSeparate   = "dub.separate"
	KindDubTranscribe = "dub.transcribe"
	KindDubReferences = "dub.references"
	KindDubTranslate  = "dub.translate"
	KindDubSynthesize = "dub.synthesize"
	KindDubMix        = "dub.mix"
	KindDubQC         = "dub.qc"
	KindDubExport     = "dub.export"
)

// Warning and error codes of the dub stages (StageState.Code/Warnings).
const (
	DubWarnNoSeparation  = "dub.no_separation"     // the engine cannot separate (or no model): the original is ducked under the dub
	DubWarnNoDiarization = "dub.no_diarization"    // the engine cannot tell speakers apart: one speaker
	DubWarnTooManySpk    = "dub.too_many_speakers" // more speakers than the model separates
	// DubWarnSpeakersModel: the model that tells speakers apart best is
	// not downloaded; Sortformer did (thread #48).
	DubWarnSpeakersModel = "dub.speakers_model"
	DubWarnNoPoster      = "dub.no_poster"     // no media.thumbnail
	DubWarnNoProxy       = "dub.no_proxy"      // the video cannot play in the preview (no media.transcode)
	DubWarnNoEnhance     = "dub.no_enhance"    // references kept as they are
	DubWarnCaptions      = "dub.captions"      // the downloaded captions were used instead of transcription
	DubWarnNoReference   = "dub.no_reference"  // a speaker with too little speech: the default voice
	DubWarnVoice         = "dub.voice"         // a cast voice could not be resolved: the default voice
	DubWarnNoBackground  = "dub.no_background" // keep background, but nothing to keep it from
	DubCodeNoSource      = "dub.no_source"
	DubCodeNoSpeech      = "dub.no_speech"     // transcription found no speech
	DubCodeNoTranslator  = "dub.no_translator" // no translation engine is set up
	DubCodeNoLines       = "dub.no_lines"      // nothing to say in the language
	DubCodeNotMixed      = "dub.not_mixed"     // export of a track not made yet
	DubCodeNeedsEngine   = "dub.needs_engine"  // the export needs an engine method it lacks (data: method)
	DubCodeNeedsFFmpeg   = "dub.needs_ffmpeg"  // MP3 and burnt-in subtitles
	DubCodeFailed        = "dub.failed"
)

// DubChanged is published after a dub project is saved or deleted.
// Topic "dub.changed".
type DubChanged struct {
	ID      string
	Project *dub.Project // a copy; nil when Deleted
	Deleted bool
}

func (DubChanged) Topic() string { return "dub.changed" }

// DubModels are the models the pipeline uses besides the TTS model (repo
// ids; "" = that step is skipped).
type DubModels struct {
	STT, Align, Diarize, Separate, Enhance string
	// Speakers transcribes and labels speakers in one pass (thread #48:
	// MOSS-Transcribe-Diarize): the transcript of a source with more than
	// one speaker, in a language it knows (SpeakersLanguages; nil = any).
	// When it is not downloaded, STT and Diarize (Sortformer) do it, and
	// the stage warns DubWarnSpeakersModel.
	Speakers          string
	SpeakersLanguages []string
	// WordTimes: STT times words itself (no aligner needed).
	WordTimes bool
	// STTLanguages are the base languages STT knows (nil: any); OtherSTT
	// (OtherWordTimes) transcribes the rest (thread #46: Qwen3-ASR, then
	// Whisper). "" = STT for every language.
	STTLanguages   []string
	OtherSTT       string
	OtherWordTimes bool
	// Dialogue are the dialogue models the dub can speak conversations
	// with, the default first (thread #89: SoulX-Podcast); none: every
	// line line by line.
	Dialogue []DubDialogueModel
}

// STTFor is the speech-to-text model for a language (BCP-47; "" =
// detected) and whether it times words itself.
func (m DubModels) STTFor(lang string) (string, bool) {
	if pickOther(m.STTLanguages, m.OtherSTT, lang) {
		return m.OtherSTT, m.OtherWordTimes
	}
	return m.STT, m.WordTimes
}

// SpeakersFor is the speakers model for a source language (BCP-47; "" =
// detected), "" when there is none or it does not know the language.
func (m DubModels) SpeakersFor(lang string) string {
	lang = strings.TrimSpace(lang)
	if m.Speakers == "" || m.SpeakersLanguages == nil || lang == "" || strings.EqualFold(lang, "auto") ||
		slices.Contains(m.SpeakersLanguages, strings.ToLower(baseLang(lang))) {
		return m.Speakers
	}
	return ""
}

// pickOther reports whether a language (BCP-47) needs the other
// speech-to-text model: there is one, and the first one lists its
// languages without lang. Detection ("") stays on the first.
func pickOther(langs []string, other, lang string) bool {
	lang = strings.TrimSpace(lang)
	if other == "" || langs == nil || lang == "" || strings.EqualFold(lang, "auto") {
		return false
	}
	return !slices.Contains(langs, strings.ToLower(baseLang(lang)))
}

// DubModelCaps are what a TTS model can do, as the pipeline needs it.
type DubModelCaps struct {
	Clone, Duration, Instruct, Speed, Steps, Guidance bool
	// NeedsReference: the model only clones: lines cast
	// with a voice that carries no reference (the default voice, a
	// design) clone their speaker instead.
	NeedsReference bool
	// CloneAudioOnly: clones from the reference audio without its
	// transcript (catalog.CapCloneAudioOnly): how a line in another
	// language than its speaker's clip is cloned.
	CloneAudioOnly bool
	// Languages maps BCP-47 codes to the model's language values
	// ("zh" → "Chinese"); nil = it takes codes, or has no language.
	Languages map[string]string
}

// DubsConfig wires the dub service. Store, Bus and Scheduler are required.
type DubsConfig struct {
	Store     store.Dubs
	Bus       *core.Bus
	Scheduler jobs.Scheduler
	// Engine returns the executor of an engine method (engine.Executor
	// in the method's role, dub.RoleOf); nil or a nil executor fails the
	// stage with engine_unavailable (retryable).
	Engine func(method string) jobs.Executor
	// Render renders lines through the take pipeline (svc.Render).
	Render    *Takes
	ModelPath func(ctx context.Context, repo string) (string, error)
	// Models are the pipeline's models now (from the catalogue/settings).
	Models func() DubModels
	// Caps describes a TTS model.
	Caps func(repo string) DubModelCaps
	// Voice resolves a VoiceRef for a model (saved voices, designs,
	// gallery voices). Goroutine-safe. nil: every cast voice is the
	// default one.
	Voice func(ctx context.Context, ref, model, lang string) (dub.VoiceSpec, error)
	// Translator returns the translator for a project's settings, or an
	// error with translate.CodeNoLLM / translate.CodeNoKey when none is set
	// up; Extractor the glossary extractor (nil: no Auto glossary).
	Translator func(ctx context.Context, s dub.Settings) (translate.Translator, error)
	Extractor  func() *translate.Extractor
	// TranslateLane is the lane of translation jobs (net; gpu when the
	// route is the engine's own LLM). nil = net.
	TranslateLane func() jobs.Lane
	// YtDlp is the URL importer ("" path = URL import is off).
	YtDlp func() dub.YtDlp
	// Documents keep the batch queue's settings and watch folder.
	Documents store.Documents
	// WatchEvery is the watch folder's scan interval (0 = 5 s).
	WatchEvery time.Duration
	Now        func() time.Time
	Logf       func(string, ...any)
}

// Dubs is the dub service. Goroutine-safe.
type Dubs struct {
	cfg   DubsConfig
	mu    sync.Mutex // serializes read-modify-write of projects
	runMu sync.Mutex // serializes starting runs and deleting (#101 M4, M3)
	batch batch
	// deleteWait bounds how long Delete waits for a project's cancelled
	// jobs to end (0 = 15 s).
	deleteWait time.Duration
	// probes are the probes going on, by project (dub_probe.go).
	probeMu sync.Mutex
	probes  map[string]*probeRun
}

// NewDubs builds the service and registers its executors.
func NewDubs(cfg DubsConfig) *Dubs {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logf == nil {
		cfg.Logf = log.Printf
	}
	if cfg.Models == nil {
		cfg.Models = func() DubModels { return DubModels{} }
	}
	if cfg.Caps == nil {
		cfg.Caps = func(string) DubModelCaps { return DubModelCaps{Clone: true} }
	}
	s := &Dubs{cfg: cfg}
	s.batch.every = cfg.WatchEvery
	if cfg.Scheduler != nil {
		for kind, ex := range s.Executors() {
			cfg.Scheduler.Register(kind, ex)
		}
	}
	return s
}

// Executors are the service's executors by job kind (registered by
// NewDubs; tests with a fake scheduler run them themselves).
func (s *Dubs) Executors() map[string]jobs.Executor {
	return map[string]jobs.Executor{
		KindDubFetch:      s.stage(dub.StageFetch, s.fetch),
		KindDubIngest:     s.stage(dub.StageIngest, s.ingest),
		KindDubSeparate:   s.stage(dub.StageSeparate, s.separate),
		KindDubTranscribe: s.stage(dub.StageTranscribe, s.transcribe),
		KindDubReferences: s.stage(dub.StageReferences, s.references),
		KindDubTranslate:  s.stage(dub.StageTranslate, s.translate),
		KindDubSynthesize: s.stage(dub.StageSynthesize, s.synthesize),
		KindDubMix:        s.stage(dub.StageMix, s.mix),
		KindDubQC:         s.stage(dub.StageQC, s.qc),
		KindDubExport:     jobs.ExecutorFunc(s.export),
	}
}

// --- the library ------------------------------------------------------------------

// Dir is the directory of project id's files.
func (s *Dubs) Dir(id string) string { return filepath.Join(s.cfg.Store.Dir(), id) }

// Path is a project file's absolute path ("" for "").
func (s *Dubs) Path(id, name string) string {
	if name == "" {
		return ""
	}
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(s.Dir(id), name)
}

// keyPath is how a project file's path enters a render key: relative to
// the project's folder (with slashes) when it is inside it, so the keys
// stay the same when the folder moves (a copied sample, Settings →
// Storage); other paths as they are.
func (s *Dubs) keyPath(id, path string) string {
	if rel, err := filepath.Rel(s.Dir(id), path); err == nil && filepath.IsAbs(path) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(rel)
	}
	return path
}

// Create saves a new project (its ID chosen when empty).
func (s *Dubs) Create(ctx context.Context, p *dub.Project) (*dub.Project, error) {
	if p.ID == "" {
		p.ID = core.NewID(s.cfg.Now())
	}
	now := s.cfg.Now()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	p.Normalize()
	if err := os.MkdirAll(s.Dir(p.ID), 0o755); err != nil {
		return nil, err
	}
	s.mu.Lock()
	err := s.put(ctx, p)
	s.mu.Unlock()
	logSave(ctx, "dub", true, "", p.ID, p.Title, err, "source", string(p.Source.Kind))
	if err != nil {
		return nil, err
	}
	s.publish(p)
	return p.Clone(), nil
}

func (s *Dubs) put(ctx context.Context, p *dub.Project) error {
	doc, err := json.Marshal(p)
	if err != nil {
		return err
	}
	src := p.Source.Path
	if p.Source.Kind == dub.SourceURL && p.Source.URL != "" {
		src = p.Source.URL
	}
	return s.cfg.Store.Put(ctx, store.DubRecord{ID: p.ID, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, Batch: p.Batch,
		Title: p.Title, Source: src, Languages: p.Targets, Phase: string(p.Phase()), Duration: p.Source.Duration, Doc: doc})
}

func (s *Dubs) publish(p *dub.Project) {
	if s.cfg.Bus != nil {
		s.cfg.Bus.Publish(DubChanged{ID: p.ID, Project: p.Clone()})
	}
}

// Get reads a project.
func (s *Dubs) Get(ctx context.Context, id string) (*dub.Project, error) {
	r, err := s.cfg.Store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	p := &dub.Project{}
	if err := json.Unmarshal(r.Doc, p); err != nil {
		return nil, fmt.Errorf("dub %s: %w", id, err)
	}
	p.Normalize()
	return p, nil
}

// List lists projects (without their documents).
func (s *Dubs) List(ctx context.Context, q store.DubQuery) ([]store.DubRecord, error) {
	return s.cfg.Store.List(ctx, q)
}

// Update applies fn to the saved project, saves it and publishes it. fn
// may return an error to change nothing.
func (s *Dubs) Update(ctx context.Context, id string, fn func(*dub.Project) error) (*dub.Project, error) {
	s.mu.Lock()
	p, err := s.Get(ctx, id)
	var oldTitle string
	if err == nil {
		oldTitle = p.Title
		err = fn(p)
	}
	if err == nil {
		p.ID = id
		p.UpdatedAt = s.cfg.Now()
		err = s.put(ctx, p)
	}
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if p.Title != oldTitle {
		logSave(ctx, "dub", false, oldTitle, id, p.Title, nil)
	}
	s.publish(p)
	return p.Clone(), nil
}

// Delete cancels a project's jobs, waits for them to end, and removes
// the project with its files. A job still running after the wait (one
// that does not heed its cancel) makes it ErrBusy: removing the folder
// under it would leave a stray one behind, or break the job (#101 M3).
func (s *Dubs) Delete(ctx context.Context, id string) error {
	err := s.delete(ctx, id)
	logDelete(ctx, "dub", id, "", err)
	return err
}

func (s *Dubs) delete(ctx context.Context, id string) error {
	s.runMu.Lock() // no run starts meanwhile
	defer s.runMu.Unlock()
	probe := s.cancelProbe(id)
	s.Cancel(ctx, id)
	if probe != nil {
		select { // a probe ends at once when cancelled; it writes the cover into the folder
		case <-probe:
		case <-time.After(5 * time.Second):
		}
	}
	if err := s.awaitStopped(ctx, id); err != nil {
		return err
	}
	s.mu.Lock()
	err := s.cfg.Store.Delete(ctx, id)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if dir := s.Dir(id); filepath.Base(dir) == id && id != "" {
		os.RemoveAll(dir)
	}
	if s.cfg.Bus != nil {
		s.cfg.Bus.Publish(DubChanged{ID: id, Deleted: true})
	}
	return nil
}

// awaitStopped waits, at most deleteWait, until none of a project's jobs
// is waiting, queued or running.
func (s *Dubs) awaitStopped(ctx context.Context, id string) error {
	limit := s.deleteWait
	if limit == 0 {
		limit = 15 * time.Second
	}
	deadline := time.Now().Add(limit)
	for {
		active, err := s.Active(ctx, id)
		if err != nil {
			return err
		}
		if len(active) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("dub %s: %s is still stopping, try again in a moment: %w", id, active[0].Title, core.ErrBusy)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// activeStates are the states of jobs that still run, or will.
var activeStates = []jobs.State{jobs.StateWaiting, jobs.StateQueued, jobs.StateRunning}

// Active lists a project's unfinished jobs: waiting, queued or running.
// Interrupted ones are not: they do not run again without a Retry, and a
// project should not look busy because of them (#101 H2).
func (s *Dubs) Active(ctx context.Context, id string) ([]jobs.Job, error) {
	if s.cfg.Scheduler == nil {
		return nil, nil
	}
	return s.cfg.Scheduler.List(ctx, jobs.Filter{Owner: DubOwner + id, States: activeStates})
}

// Cancel cancels a project's unfinished jobs (interrupted ones too) and
// its probe, and forgets its run.
func (s *Dubs) Cancel(ctx context.Context, id string) error {
	s.cancelProbe(id)
	var active []jobs.Job
	if s.cfg.Scheduler != nil {
		var err error
		if active, err = s.cfg.Scheduler.List(ctx, jobs.Filter{Owner: DubOwner + id}); err != nil {
			return err
		}
	}
	var errs []error
	for _, j := range active {
		if err := s.cfg.Scheduler.Cancel(ctx, j.ID); err != nil && !errors.Is(err, jobs.ErrNotFound) {
			errs = append(errs, err)
		}
	}
	s.Update(ctx, id, func(p *dub.Project) error {
		p.Run = nil
		for k, st := range p.Stages {
			if st.Status == dub.StatusRunning {
				st.Status, st.Code = dub.StatusNone, proto.CodeCancelled
				p.Stages[k] = st
			}
		}
		return nil
	})
	return errors.Join(errs...)
}
