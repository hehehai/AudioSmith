package services

// Transcripts (M2): speech to text for the Transcribe page, dictation and
// later the Local API. A transcription is one "transcript.make" job on
// the gpu lane: it makes a 16 kHz mono WAV of the input (in Go for WAV
// files, else the engine's media.decode), runs stt.transcribe and, when
// an aligner is named, stt.align for word timings, cuts the text into
// sentences, saves the transcript and publishes TranscriptChanged. The
// engine calls run inside the job (as renders do), so the gpu lane's one
// slot is held once, not twice.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/resample"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/store"
)

// KindTranscribe is the job kind of a transcription.
const KindTranscribe = "transcript.make"

// TranscriptOwner prefixes the owners of transcription jobs:
// "transcript:<id>".
const TranscriptOwner = "transcript:"

// TranscriptRate is the sample rate of the audio kept with transcripts
// (what speech models listen at).
const TranscriptRate = 16000

// Warnings of a transcription (TranscribeResult.Warnings).
const (
	WarnNoWordTimes = "transcript.no_word_times" // the aligner failed: segments without word timings
)

// TranscriptsConfig wires the transcript service. Store and Bus are required.
type TranscriptsConfig struct {
	Store     store.Transcripts
	Bus       *core.Bus
	Scheduler jobs.Scheduler // nil: Transcribe fails with core.ErrUnsupported
	// Engine returns the executor of one engine method (the supervisor's,
	// engine.Executor), asked as each job runs; nil, or a nil executor,
	// fails jobs with engine_unavailable (retryable).
	Engine func(method string) jobs.Executor
	// ModelPath is the local snapshot of a model, or an error wrapping
	// core.ErrNotDownloaded; Ready checks the same before queueing.
	ModelPath func(ctx context.Context, repo string) (string, error)
	Licences  core.LicenceService
	Now       func() time.Time
	Logf      func(format string, args ...any)
}

// Transcripts is the transcript library and its transcription jobs.
// Goroutine-safe.
type Transcripts struct {
	store     store.Transcripts
	bus       *core.Bus
	sched     jobs.Scheduler
	engine    func(string) jobs.Executor
	modelPath func(context.Context, string) (string, error)
	licences  core.LicenceService
	now       func() time.Time
	logf      func(string, ...any)
	mu        sync.Mutex // serializes read-modify-write of one transcript
}

// NewTranscripts builds the service and registers its executor.
func NewTranscripts(cfg TranscriptsConfig) *Transcripts {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logf == nil {
		cfg.Logf = log.Printf
	}
	s := &Transcripts{store: cfg.Store, bus: cfg.Bus, sched: cfg.Scheduler, engine: cfg.Engine,
		modelPath: cfg.ModelPath, licences: cfg.Licences, now: cfg.Now, logf: cfg.Logf}
	if cfg.Scheduler != nil {
		cfg.Scheduler.Register(KindTranscribe, s.Executor())
	}
	return s
}

// TranscribeRequest asks for a transcript of a file. It is the job's params.
type TranscribeRequest struct {
	// ID of the transcript to make; "" chooses one.
	ID     string                `json:"id"`
	Input  string                `json:"input"` // absolute path: any audio or video
	Source core.TranscriptSource `json:"source"`
	Title  string                `json:"title,omitempty"`
	// RemoveInput deletes Input once its audio is kept (a recording the
	// app made for this transcript).
	RemoveInput bool   `json:"removeInput,omitempty"`
	Mode        string `json:"mode"`              // "fast" | "accurate", recorded with the transcript
	Model       string `json:"model"`             // speech-to-text repo
	Aligner     string `json:"aligner,omitempty"` // forced-aligner repo: time the words; "" = don't
	// Words asks the model for word timings (models with them).
	Words    bool   `json:"words,omitempty"`
	Language string `json:"language,omitempty"` // BCP-47; "" detects it
	Prompt   string `json:"prompt,omitempty"`   // vocabulary hint
	App      string `json:"app,omitempty"`      // dictation: the app it was for
	// Role is the engine worker that transcribes: "" the gpu worker, or
	// proto.RoleListen for dictation, which then runs on jobs.LaneListen
	// and the listen worker's own model, not behind renders.
	Role proto.Role `json:"role,omitempty"`
}

// TranscribeResult is a transcription job's result.
type TranscribeResult struct {
	Transcript core.Transcript `json:"transcript"`
	Warnings   []string        `json:"warnings,omitempty"`
	// AlignError says why word timings are missing (with WarnNoWordTimes).
	AlignError *jobs.Error `json:"alignError,omitempty"`
}

// Transcribe queues a transcription and returns its job id; the job is
// owned by TranscriptOwner+req.ID. It refuses models that are not
// downloaded (core.ErrNotDownloaded) or whose licence is not accepted
// (*core.LicenceRequiredError) before queueing.
func (s *Transcripts) Transcribe(ctx context.Context, req TranscribeRequest) (TranscribeRequest, string, error) {
	if s.sched == nil {
		return req, "", fmt.Errorf("transcripts: no job queue: %w", core.ErrUnsupported)
	}
	if req.Input == "" || req.Model == "" {
		return req, "", fmt.Errorf("transcribe: no input or model: %w", core.ErrInvalid)
	}
	if req.ID == "" {
		req.ID = core.NewID(s.now())
	}
	if req.Source == "" {
		req.Source = core.SourceFile
	}
	for i, m := range []string{req.Model, req.Aligner} {
		if m == "" {
			continue
		}
		err := s.Ready(ctx, m)
		if err == nil && s.licences != nil {
			err = s.licences.Check(ctx, m)
		}
		if err != nil {
			if i == 1 {
				err = fmt.Errorf("aligner: %w", err)
			}
			return req, "", err
		}
	}
	params, err := json.Marshal(req)
	if err != nil {
		return req, "", err
	}
	title := i18n.T("Transcribe %s", req.Title)
	if req.Title == "" {
		title = i18n.T("Transcribe a recording")
	}
	lane := jobs.LaneGPU
	if req.Role == proto.RoleListen {
		lane = jobs.LaneListen
	}
	id, err := s.sched.Submit(ctx, jobs.Spec{Kind: KindTranscribe, Lane: lane, Priority: jobs.PriorityInteractive,
		Params: params, Title: title, Owner: TranscriptOwner + req.ID, MaxAttempts: 2, Idempotent: true})
	return req, id, err
}

// Ready reports whether a model can be used now (nil, or an error
// wrapping core.ErrNotDownloaded).
func (s *Transcripts) Ready(ctx context.Context, repo string) error {
	if s.modelPath == nil || repo == "" {
		return nil
	}
	_, err := s.modelPath(ctx, repo)
	return err
}

func (s *Transcripts) Get(ctx context.Context, id string) (core.Transcript, error) {
	return s.store.Get(ctx, id)
}

func (s *Transcripts) List(ctx context.Context, q core.TranscriptQuery) ([]core.Transcript, error) {
	return s.store.List(ctx, q)
}

// AudioPath is the file of a transcript's audio ("" when none is kept).
func (s *Transcripts) AudioPath(t core.Transcript) string { return s.store.AudioPath(t) }

// Update applies fn to the saved transcript id (edits, clean-up,
// speaker names), stamps it and publishes TranscriptChanged.
func (s *Transcripts) Update(ctx context.Context, id string, fn func(*core.Transcript)) (core.Transcript, error) {
	s.mu.Lock()
	t, err := s.store.Get(ctx, id)
	if err != nil {
		s.mu.Unlock()
		return t, err
	}
	oldTitle := t.Title
	fn(&t)
	t.ID = id
	t.UpdatedAt = s.now()
	err = s.store.Put(ctx, t)
	s.mu.Unlock()
	if err != nil {
		return t, err
	}
	if t.Title != oldTitle {
		logSave(ctx, "transcript", false, oldTitle, id, t.Title, nil)
	}
	s.bus.Publish(core.TranscriptChanged{Transcript: t})
	return t, nil
}

// Import saves a transcript made elsewhere (a bundled sample, 0.5 F6) as
// a new one: a new ID and times; audio (nil none) is kept as its audio,
// a file with ext (".m4a"). It publishes TranscriptChanged.
func (s *Transcripts) Import(ctx context.Context, t core.Transcript, ext string, audio io.Reader) (core.Transcript, error) {
	now := s.now()
	t.ID, t.CreatedAt, t.UpdatedAt, t.Audio = core.NewID(now), now, now, ""
	if audio != nil {
		t.Audio = t.ID + strings.ToLower(ext)
		if err := writeReaderAtomic(filepath.Join(s.store.AudioDir(), t.Audio), audio); err != nil {
			return t, err
		}
	}
	if err := s.store.Put(ctx, t); err != nil {
		if t.Audio != "" {
			os.Remove(s.store.AudioPath(t))
		}
		return t, err
	}
	logSave(ctx, "transcript", true, "", t.ID, t.Title, nil)
	s.bus.Publish(core.TranscriptChanged{Transcript: t})
	return t, nil
}

// writeReaderAtomic writes r to path through a temporary file beside it.
func writeReaderAtomic(path string, r io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.part")
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		os.Remove(f.Name())
	}
	return err
}

// Delete removes a transcript and its audio.
func (s *Transcripts) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	t, err := s.store.Get(ctx, id)
	if err == nil {
		err = s.store.Delete(ctx, id)
	}
	s.mu.Unlock()
	logDelete(ctx, "transcript", id, t.Title, err)
	if err != nil {
		return err
	}
	s.bus.Publish(core.TranscriptChanged{Transcript: t, Deleted: true})
	return nil
}

// Executor runs transcription jobs (registered by NewTranscripts).
func (s *Transcripts) Executor() jobs.Executor {
	return jobs.ExecutorFunc(func(ctx context.Context, job jobs.Job, report func(jobs.Progress), _ func(json.RawMessage)) (json.RawMessage, error) {
		var req TranscribeRequest
		if err := json.Unmarshal(job.Params, &req); err != nil || req.ID == "" || req.Input == "" {
			return nil, &jobs.Error{Code: proto.CodeBadRequest, Message: "transcript.make: bad params"}
		}
		res, err := s.run(ctx, job, req, report)
		if err != nil {
			return nil, err
		}
		return json.Marshal(res)
	})
}

func (s *Transcripts) run(ctx context.Context, job jobs.Job, req TranscribeRequest, report func(jobs.Progress)) (TranscribeResult, error) {
	var res TranscribeResult
	dir := s.store.AudioDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return res, err
	}
	audioName := req.ID + ".wav"
	audio := filepath.Join(dir, audioName)
	report(jobs.Progress{Stage: "decoding", Fraction: -1})
	seconds, err := s.decode(ctx, job, req.Input, audio, report)
	if err != nil {
		return res, err
	}
	keep := func(ok bool) {
		if !ok {
			os.Remove(audio)
		}
	}

	// Speech to text.
	tp := proto.TranscribeParams{Model: req.Model, Audio: audio, Language: req.Language, Words: req.Words, Prompt: ASRPrompt(req.Model, req.Prompt, seconds)}
	var tr proto.TranscribeResult
	transcribe := func(k, n int, tp *proto.TranscribeParams, tr *proto.TranscribeResult) error {
		return s.call(ctx, job, proto.KindFor(req.Role, proto.MethodSTTTranscribe), req.Model, tp, &tp.ModelPath, tr, func(p jobs.Progress) {
			if p.Stage == "" {
				p.Stage = "transcribing"
			}
			p.Fraction = windowFraction(p.Fraction, k, n)
			report(p)
		})
	}
	if IsSpeakersModel(req.Model) {
		// Who speaks (thread #48): windows of at most 10 minutes, linked.
		tr, err = TranscribeSpeakers(ctx, transcribe, tp, seconds)
	} else {
		err = transcribe(0, 1, &tp, &tr)
	}
	if err != nil {
		keep(false)
		return res, err
	}
	lang := tr.Language
	if lang == "" || lang == "auto" {
		lang = req.Language
	}
	if tr.AudioSeconds > 0 {
		seconds = tr.AudioSeconds
	} else {
		tr.AudioSeconds = seconds
	}
	CleanTranscription(req.Model, &tr)
	words := segmentWords(tr.Segments)
	aligner := ""
	if req.Aligner != "" && len(words) == 0 && strings.TrimSpace(tr.Text) != "" {
		ap := proto.AlignParams{Model: req.Aligner, Audio: audio, Text: strings.TrimSpace(tr.Text), Language: lang}
		aw, err := alignWords(ctx, func(p *proto.AlignParams, out *proto.AlignResult) error {
			return s.call(ctx, job, proto.MethodSTTAlign, req.Aligner, p, &p.ModelPath, out, func(p jobs.Progress) {
				p.Stage = "aligning"
				report(p)
			})
		}, ap, tr.Segments, seconds)
		switch {
		case ctx.Err() != nil:
			keep(false)
			return res, ctx.Err()
		case err != nil:
			// The text is good without timings: keep it, and say why.
			res.Warnings = append(res.Warnings, WarnNoWordTimes)
			var je *jobs.Error
			if !errors.As(err, &je) {
				je = &jobs.Error{Code: proto.CodeInternal, Message: err.Error()}
			}
			res.AlignError = je
		default:
			words, aligner = aw, req.Aligner
		}
	}
	report(jobs.Progress{Stage: "writing", Fraction: -1})
	now := s.now()
	t := core.Transcript{
		ID: req.ID, CreatedAt: now, UpdatedAt: now, Title: req.Title, Source: req.Source, Audio: audioName,
		Mode: req.Mode, Model: req.Model, Aligner: aligner, Language: lang, Duration: seconds,
		Raw: strings.TrimSpace(tr.Text), App: req.App, Seconds: tr.Seconds,
	}
	if req.Source == core.SourceFile {
		t.SourcePath = req.Input
	}
	t.Segments = BuildSegments(tr.Segments, words, seconds)
	if old, err := s.store.Get(ctx, req.ID); err == nil {
		// A re-run (after an interruption) keeps the first creation time.
		t.CreatedAt = old.CreatedAt
	}
	if err := s.store.Put(ctx, t); err != nil {
		keep(false)
		return res, err
	}
	if req.RemoveInput && req.Input != audio {
		os.Remove(req.Input)
	}
	s.bus.Publish(core.TranscriptChanged{Transcript: t})
	res.Transcript = t
	return res, nil
}

// windowFraction maps the fraction done of window k of n onto the
// whole (-1, unknown, stays so).
func windowFraction(f float64, k, n int) float64 {
	if n <= 1 || f < 0 {
		return f
	}
	return (float64(k) + min(f, 1)) / float64(n)
}

// segmentWords are the words the model timed itself, if it did.
func segmentWords(segs []proto.Segment) []proto.Word {
	var out []proto.Word
	for _, s := range segs {
		out = append(out, s.Words...)
	}
	return out
}

// decode writes in as a 16 kHz mono 16-bit WAV at out and returns its
// length: in Go for WAV files, else with the engine's media.decode.
func (s *Transcripts) decode(ctx context.Context, job jobs.Job, in, out string, report func(jobs.Progress)) (float64, error) {
	if strings.EqualFold(filepath.Ext(in), ".wav") {
		b, _, err := wav.ReadFile(in)
		if err == nil {
			b = resample.Convert(b, TranscriptRate, 1, resample.Good)
			if err := writeWAVAtomic(out, b); err != nil {
				return 0, err
			}
			return b.Seconds(), nil
		}
		if os.IsNotExist(err) {
			data, _ := json.Marshal(map[string]string{"path": in})
			return 0, &jobs.Error{Code: proto.CodeFileNotFound, Message: err.Error(), Data: data}
		}
		// An encoding Go doesn't read: the engine may.
	}
	p := proto.DecodeParams{Input: in, Output: proto.Output{Path: out, Format: "wav", Bits: 16}, SampleRate: TranscriptRate, Channels: 1}
	var dr proto.DecodeResult
	if err := s.call(ctx, job, proto.MethodMediaDecode, "", &p, nil, &dr, func(p jobs.Progress) {
		p.Stage = "decoding"
		report(p)
	}); err != nil {
		return 0, err
	}
	return dr.AudioSeconds, nil
}

// writeWAVAtomic writes b as 16-bit PCM through a temporary file.
func writeWAVAtomic(out string, b dsp.Buffer) error {
	tmp := out + ".tmp"
	if err := wav.WriteFile(tmp, b, wav.PCM16, wav.Options{}); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, out)
}

// call runs one engine method inside the job: the model's snapshot is
// filled into *modelPath first (when model is set), the result decoded
// into out.
func (s *Transcripts) call(ctx context.Context, job jobs.Job, method, model string, params any, modelPath *string, out any, report func(jobs.Progress)) error {
	if model != "" && modelPath != nil && s.modelPath != nil {
		dir, err := s.modelPath(ctx, model)
		if err != nil {
			if errors.Is(err, core.ErrNotDownloaded) {
				data, _ := json.Marshal(map[string]string{"model": model})
				return &jobs.Error{Code: proto.CodeModelNotDownloaded, Message: err.Error(), Data: data}
			}
			return &jobs.Error{Code: proto.CodeInternal, Message: err.Error()}
		}
		*modelPath = dir
	}
	var ex jobs.Executor
	if s.engine != nil {
		ex = s.engine(method)
	}
	if ex == nil {
		return &jobs.Error{Code: CodeEngineUnavailable, Message: "The engine is not connected.", Retryable: true}
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	role, m := proto.SplitKind(method) // a method, or "listen:stt.transcribe"
	sub := jobs.Job{ID: job.ID + "." + m, Spec: jobs.Spec{Kind: method, Lane: jobs.Lane(role), Params: raw, Owner: job.Owner}}
	res, err := ex.Run(ctx, sub, report, nil)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return json.Unmarshal(res, out)
}
