package services

// Voice isolator (0.5 F2): a file's voices apart from its music and
// effects, and the voices denoised, with the models a dub uses
// (separate.run, HTDemucs; enhance.run, DeepFilterNet). An isolation is
// one "isolate.run" job on the gpu lane (owner "isolate:<id>"): it decodes
// the input to a WAV (the engine's media.decode; WAV files in Go), splits
// it into vocals.wav and background.wav, then denoises the vocals into
// voice-clean.wav (skipped, with a warning, when the denoiser is not
// downloaded). Each isolation is a folder in Dir with isolation.json and
// its stems; IsolationChanged follows every save and delete.

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
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

// KindIsolate is the job kind of an isolation.
const KindIsolate = "isolate.run"

// IsolateOwner prefixes the owners of isolation jobs: "isolate:<id>".
const IsolateOwner = "isolate:"

// The files of an isolation, in its folder.
const (
	IsolateMeta       = "isolation.json"
	IsolateAudio      = "audio.wav"       // the input, decoded
	IsolateVocals     = "vocals.wav"      // the voices
	IsolateBackground = "background.wav"  // the music and effects
	IsolateClean      = "voice-clean.wav" // the voices, denoised
)

// The states of an isolation.
const (
	IsolateQueued  = "queued"
	IsolateRunning = "running"
	IsolateDone    = "done"
	IsolateFailed  = "failed"
)

// WarnNoDenoise: the denoiser is missing; the voices are not denoised.
const WarnNoDenoise = "isolate.no_denoise"

// Isolation is one file's stems.
type Isolation struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Title     string    `json:"title"`
	Input     string    `json:"input"` // the file isolated, as picked
	Video     bool      `json:"video,omitempty"`
	Duration  float64   `json:"duration,omitempty"` // seconds
	State     string    `json:"state"`
	Job       string    `json:"job,omitempty"`
	Error     string    `json:"error,omitempty"` // why it failed
	ErrorCode string    `json:"errorCode,omitempty"`
	// The stems written ("" none yet): file names in the folder.
	Vocals     string   `json:"vocals,omitempty"`
	Background string   `json:"background,omitempty"`
	Clean      string   `json:"clean,omitempty"`
	Separator  string   `json:"separator,omitempty"`
	Denoiser   string   `json:"denoiser,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
}

// IsolationChanged follows a saved or deleted isolation.
type IsolationChanged struct {
	Isolation Isolation
	Deleted   bool
}

func (IsolationChanged) Topic() string { return "isolation.changed" }

// IsolationsConfig wires the service. Dir and Bus are required.
type IsolationsConfig struct {
	Dir       string
	Bus       *core.Bus
	Scheduler jobs.Scheduler // nil: Start fails with core.ErrUnsupported
	// Engine returns the executor of one engine method; nil (or a nil
	// executor) fails with engine_unavailable.
	Engine func(method string) jobs.Executor
	// ModelPath is a model's local snapshot, or an error wrapping
	// core.ErrNotDownloaded.
	ModelPath func(ctx context.Context, repo string) (string, error)
	// Models are the separation and denoising models' repos.
	Models func() (separate, enhance string)
	Now    func() time.Time
	Logf   func(format string, args ...any)
}

// Isolations is the voice isolator's library and jobs. Goroutine-safe.
type Isolations struct {
	cfg IsolationsConfig
	mu  sync.Mutex // serializes read-modify-write of one isolation
	// deleteWait bounds how long Delete waits for a cancelled job to end
	// (0 = 15 s).
	deleteWait time.Duration
}

// NewIsolations builds the service and registers its executor.
func NewIsolations(cfg IsolationsConfig) *Isolations {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logf == nil {
		cfg.Logf = log.Printf
	}
	if cfg.Models == nil {
		cfg.Models = func() (string, string) { return "", "" }
	}
	s := &Isolations{cfg: cfg}
	if cfg.Scheduler != nil {
		cfg.Scheduler.Register(KindIsolate, s.Executor())
	}
	return s
}

// Dir is isolation id's folder.
func (s *Isolations) Dir(id string) string { return filepath.Join(s.cfg.Dir, id) }

// Path is a file of isolation id ("" for "").
func (s *Isolations) Path(id, name string) string {
	if name == "" {
		return ""
	}
	return filepath.Join(s.Dir(id), name)
}

// Missing are the models an isolation needs that are not downloaded:
// the separator (required), then the denoiser (its stem is skipped
// without it).
func (s *Isolations) Missing(ctx context.Context) []string {
	sep, enh := s.cfg.Models()
	var out []string
	for _, m := range []string{sep, enh} {
		if m != "" && s.cfg.ModelPath != nil {
			if _, err := s.cfg.ModelPath(ctx, m); errors.Is(err, core.ErrNotDownloaded) {
				out = append(out, m)
			}
		}
	}
	return out
}

// Start makes an isolation of input and queues its job. It refuses when
// the separation model is not downloaded (an error wrapping
// core.ErrNotDownloaded, naming the model).
func (s *Isolations) Start(ctx context.Context, input string) (Isolation, error) {
	if s.cfg.Scheduler == nil {
		return Isolation{}, fmt.Errorf("isolate: no job queue: %w", core.ErrUnsupported)
	}
	if input == "" {
		return Isolation{}, fmt.Errorf("isolate: no input: %w", core.ErrInvalid)
	}
	if _, err := os.Stat(input); err != nil {
		return Isolation{}, fmt.Errorf("isolate: %w", err)
	}
	sep, _ := s.cfg.Models()
	if sep == "" {
		return Isolation{}, fmt.Errorf("isolate: no separation model: %w", core.ErrUnsupported)
	}
	if s.cfg.ModelPath != nil {
		if _, err := s.cfg.ModelPath(ctx, sep); err != nil {
			return Isolation{}, fmt.Errorf("%s: %w", sep, err)
		}
	}
	// Refused up front when the disk cannot hold the stems (code review
	// 1102 M4): a WAV's length is known now, other files' once decoded.
	if err := checkFree("the isolation", s.cfg.Dir, isolateNeed(wavSeconds(input))); err != nil {
		return Isolation{}, err
	}
	now := s.cfg.Now()
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(input), "."))
	iso := Isolation{ID: core.NewID(now), CreatedAt: now, UpdatedAt: now, Input: input, State: IsolateQueued,
		Title: strings.TrimSuffix(filepath.Base(input), filepath.Ext(input)), Video: slices.Contains(isolateVideoExts, ext)}
	if err := os.MkdirAll(s.Dir(iso.ID), 0o755); err != nil {
		return iso, err
	}
	if err := s.write(iso); err != nil {
		return iso, err
	}
	s.cfg.Bus.Publish(IsolationChanged{Isolation: iso})
	params, _ := json.Marshal(map[string]string{"id": iso.ID})
	job, err := s.cfg.Scheduler.Submit(ctx, jobs.Spec{Kind: KindIsolate, Lane: jobs.LaneGPU, Priority: jobs.PriorityInteractive,
		Params: params, Title: i18n.T("Isolate %s", iso.Title), Owner: IsolateOwner + iso.ID, MaxAttempts: 2, Idempotent: true})
	if err != nil {
		os.RemoveAll(s.Dir(iso.ID))
		s.cfg.Bus.Publish(IsolationChanged{Isolation: iso, Deleted: true})
		return iso, err
	}
	saved, err := s.update(iso.ID, func(x *Isolation) {
		if x.Job == "" {
			x.Job = job
		}
	})
	if err != nil {
		return iso, err
	}
	logSave(ctx, "isolation", true, "", iso.ID, iso.Title, nil)
	return saved, nil
}

// isolateVideoExts are the inputs shown as videos.
var isolateVideoExts = []string{"mp4", "mov", "m4v", "mkv", "webm", "avi"}

// List reads every isolation, newest first.
func (s *Isolations) List(ctx context.Context) ([]Isolation, error) {
	ents, err := os.ReadDir(s.cfg.Dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Isolation
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		if iso, err := s.Get(ctx, e.Name()); err == nil {
			out = append(out, iso)
		}
	}
	slices.SortStableFunc(out, func(x, y Isolation) int {
		if c := y.CreatedAt.Compare(x.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(y.ID, x.ID)
	})
	return out, nil
}

// Get reads isolation id.
func (s *Isolations) Get(_ context.Context, id string) (Isolation, error) {
	var iso Isolation
	if id == "" || strings.ContainsAny(id, `/\`) || id == "." || id == ".." {
		return iso, fmt.Errorf("isolation %q: %w", id, core.ErrNotFound)
	}
	data, err := os.ReadFile(filepath.Join(s.Dir(id), IsolateMeta))
	if os.IsNotExist(err) {
		return iso, fmt.Errorf("isolation %s: %w", id, core.ErrNotFound)
	}
	if err != nil {
		return iso, err
	}
	if err := json.Unmarshal(data, &iso); err != nil {
		return iso, err
	}
	iso.ID = id
	return iso, nil
}

// Delete cancels isolation id's job, waits for it to end, and removes
// its folder. A job still running after the wait (one that does not heed
// its cancel) makes it ErrBusy: removing the folder under it would leave
// a stray one behind when the engine writes its stems (code review 1102
// M5, as Dubs.Delete since #101 M3).
func (s *Isolations) Delete(ctx context.Context, id string) error {
	iso, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if s.cfg.Scheduler != nil {
		if l, err := s.cfg.Scheduler.List(ctx, jobs.Filter{Owner: IsolateOwner + id}); err == nil {
			for _, j := range l {
				s.cfg.Scheduler.Cancel(ctx, j.ID)
			}
		}
		if err := s.awaitStopped(ctx, id); err != nil {
			logDelete(ctx, "isolation", id, iso.Title, err)
			return err
		}
	}
	s.mu.Lock()
	err = os.RemoveAll(s.Dir(id))
	s.mu.Unlock()
	logDelete(ctx, "isolation", id, iso.Title, err)
	if err != nil {
		return err
	}
	s.cfg.Bus.Publish(IsolationChanged{Isolation: iso, Deleted: true})
	return nil
}

// awaitStopped waits, at most deleteWait, until isolation id's job is
// no longer waiting, queued or running.
func (s *Isolations) awaitStopped(ctx context.Context, id string) error {
	limit := s.deleteWait
	if limit == 0 {
		limit = 15 * time.Second
	}
	deadline := time.Now().Add(limit)
	for {
		active, err := s.cfg.Scheduler.List(ctx, jobs.Filter{Owner: IsolateOwner + id, States: activeStates})
		if err != nil {
			return err
		}
		if len(active) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("isolation %s is still stopping, try again in a moment: %w", id, core.ErrBusy)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// Sweep removes, once no isolation job runs, the folders without an
// isolation.json (a Delete that raced a late write, before the wait)
// and the decoded inputs of isolations that did not finish; it returns
// how many it removed. Call it at startup.
func (s *Isolations) Sweep(ctx context.Context) int {
	if s.cfg.Scheduler != nil {
		if l, err := s.cfg.Scheduler.List(ctx, jobs.Filter{Kind: KindIsolate, States: activeStates}); err != nil || len(l) > 0 {
			return 0
		}
	}
	ents, _ := os.ReadDir(s.cfg.Dir)
	n := 0
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(s.cfg.Dir, e.Name())
		if _, err := os.Stat(filepath.Join(dir, IsolateMeta)); os.IsNotExist(err) {
			if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > time.Hour && os.RemoveAll(dir) == nil {
				n++
			}
			continue
		}
		if os.Remove(filepath.Join(dir, IsolateAudio)) == nil {
			n++
		}
	}
	return n
}

// write saves iso's isolation.json through a temporary file.
func (s *Isolations) write(iso Isolation) error {
	data, err := json.MarshalIndent(iso, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.Dir(iso.ID), IsolateMeta)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// update applies fn to the saved isolation id, stamps and publishes it.
func (s *Isolations) update(id string, fn func(*Isolation)) (Isolation, error) {
	s.mu.Lock()
	iso, err := s.Get(context.Background(), id)
	if err != nil {
		s.mu.Unlock()
		return iso, err
	}
	fn(&iso)
	iso.UpdatedAt = s.cfg.Now()
	err = s.write(iso)
	s.mu.Unlock()
	if err != nil {
		return iso, err
	}
	s.cfg.Bus.Publish(IsolationChanged{Isolation: iso})
	return iso, nil
}

// Executor runs isolation jobs (registered by NewIsolations).
func (s *Isolations) Executor() jobs.Executor {
	return jobs.ExecutorFunc(func(ctx context.Context, job jobs.Job, report func(jobs.Progress), _ func(json.RawMessage)) (json.RawMessage, error) {
		var p struct{ ID string }
		if err := json.Unmarshal(job.Params, &p); err != nil || p.ID == "" {
			return nil, &jobs.Error{Code: proto.CodeBadRequest, Message: "isolate.run: bad params"}
		}
		iso, err := s.run(ctx, job, p.ID, report)
		if d := jobs.DiskFull(err); d != nil {
			err = d
		}
		if err != nil {
			// The decoded input is made again by a retry; kept, it is
			// as big as the stems (code review 1102 M5).
			os.Remove(s.Path(p.ID, IsolateAudio))
			if _, gone := s.Get(context.WithoutCancel(ctx), p.ID); gone == nil {
				msg, c := err.Error(), code(err)
				state := IsolateFailed
				if ctx.Err() != nil {
					msg, c = "", jobs.CodeCancelled
				}
				s.update(p.ID, func(x *Isolation) { x.State, x.Error, x.ErrorCode = state, msg, c })
			}
			return nil, err
		}
		return json.Marshal(iso)
	})
}

// run decodes, separates and denoises isolation id.
func (s *Isolations) run(ctx context.Context, job jobs.Job, id string, report func(jobs.Progress)) (Isolation, error) {
	iso, err := s.Get(ctx, id)
	if err != nil {
		return iso, err
	}
	if _, err := s.update(id, func(x *Isolation) {
		x.State, x.Error, x.ErrorCode, x.Job = IsolateRunning, "", "", job.ID
	}); err != nil {
		return iso, err
	}
	sep, enh := s.cfg.Models()
	path := func(name string) string { return s.Path(id, name) }
	// The input, decoded (44.1 kHz stereo: what HTDemucs takes).
	report(jobs.Progress{Stage: "decoding", Fraction: -1})
	seconds, err := s.decode(ctx, job, iso.Input, path(IsolateAudio), report)
	if err != nil {
		return iso, err
	}
	if err := checkFree("the isolation", s.Dir(id), isolateStemsNeed(seconds)); err != nil {
		return iso, err
	}
	// The voices and the rest.
	sp := proto.SeparateParams{Model: sep, Audio: path(IsolateAudio),
		Vocals:     proto.Output{Path: path(IsolateVocals), Format: "wav", Bits: 16},
		Background: proto.Output{Path: path(IsolateBackground), Format: "wav", Bits: 16}}
	var sr proto.SeparateResult
	if err := s.call(ctx, job, proto.MethodSeparateRun, sep, &sp, &sp.ModelPath, &sr, func(pg jobs.Progress) {
		if pg.Stage == "" || pg.Stage == "working" {
			pg.Stage = "separating"
		}
		pg.Fraction = scaled(pg.Fraction, 0, 0.8)
		report(pg)
	}); err != nil {
		return iso, err
	}
	if sr.AudioSeconds > 0 {
		seconds = sr.AudioSeconds
	}
	// The voices, denoised (when the denoiser is there).
	var warnings []string
	clean := ""
	if enh != "" {
		ep := proto.EnhanceParams{Model: enh, Audio: path(IsolateVocals), Output: proto.Output{Path: path(IsolateClean), Format: "wav", Bits: 16}}
		var er proto.EnhanceResult
		err := s.call(ctx, job, proto.MethodEnhanceRun, enh, &ep, &ep.ModelPath, &er, func(pg jobs.Progress) {
			pg.Stage = "denoising"
			pg.Fraction = scaled(pg.Fraction, 0.8, 1)
			report(pg)
		})
		switch {
		case ctx.Err() != nil:
			return iso, ctx.Err()
		case missing(err):
			warnings = append(warnings, WarnNoDenoise)
			enh = ""
		case err != nil:
			return iso, err
		default:
			clean = IsolateClean
		}
	} else {
		warnings = append(warnings, WarnNoDenoise)
	}
	os.Remove(path(IsolateAudio)) // the stems are what is kept
	return s.update(id, func(x *Isolation) {
		x.State, x.Duration, x.Warnings = IsolateDone, seconds, warnings
		x.Vocals, x.Background, x.Clean, x.Separator, x.Denoiser = IsolateVocals, IsolateBackground, clean, sep, enh
	})
}

// scaled maps a stage's fraction onto [lo, hi] of the whole (-1 stays).
func scaled(f, lo, hi float64) float64 {
	if f < 0 {
		return f
	}
	return lo + min(f, 1)*(hi-lo)
}

// decode writes in as a 44.1 kHz stereo WAV and returns its length.
func (s *Isolations) decode(ctx context.Context, job jobs.Job, in, out string, report func(jobs.Progress)) (float64, error) {
	if _, err := os.Stat(in); err != nil {
		data, _ := json.Marshal(map[string]string{"path": in})
		return 0, &jobs.Error{Code: proto.CodeFileNotFound, Message: err.Error(), Data: data}
	}
	const rate, ch = 44100, 2
	if strings.EqualFold(filepath.Ext(in), ".wav") {
		if frames, err := convertStream(ctx, in, out, rate, ch, wav.PCM16, nil); err == nil {
			return float64(frames) / rate, nil
		}
		// An encoding Go doesn't read: the engine may.
	}
	p := proto.DecodeParams{Input: in, Output: proto.Output{Path: out, Format: "wav", Bits: 16}, SampleRate: rate, Channels: ch}
	var dr proto.DecodeResult
	if err := s.call(ctx, job, proto.MethodMediaDecode, "", &p, nil, &dr, func(p jobs.Progress) {
		p.Stage = "decoding"
		p.Fraction = -1
		report(p)
	}); err != nil {
		return 0, err
	}
	return dr.AudioSeconds, nil
}

// call runs one engine method inside the job (see engineCaller.call).
func (s *Isolations) call(ctx context.Context, job jobs.Job, method, model string, params any, modelPath *string, out any, report func(jobs.Progress)) error {
	return engineCaller{s.cfg.Engine, s.cfg.ModelPath}.call(ctx, job, method, model, params, modelPath, out, report)
}
