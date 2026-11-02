package services

// The render pipeline: a take of any length, as one "take.render" job.
//
//	script ─▶ text/plan (markup lowered for the model, dictionary,
//	          normalisation, chunks the model can take)
//	       ─▶ for each chunk: the chunk cache (text.Key) or one
//	          tts.synthesize call through the engine executor
//	       ─▶ edges trimmed, joined (dsp/edit.Joiner: 50 ms crossfades
//	          inside sentences, gaps between sentences and paragraphs,
//	          [pause] silences) into a float WAV, streamed to the UI
//	          while it grows (the job's chunks, pcm16le)
//	       ─▶ "Even out volume" (dsp/master preset, two passes) ─▶ the
//	          take's WAV in its bit depth (TPDF dither at 16 bits)
//	       ─▶ Check audio (dsp/qc) and a dropped-chunk check
//
// The job runs on the gpu lane, so the chunks of one take run back to
// back on a warm model, and the engine sees one call at a time. It is
// idempotent: run again after a crash or a retryable failure, the chunks
// already made come from the cache, so it goes on where it stopped.
// Cancelling the job cancels the chunk being made.
//
// Each engine call uses the job's id with the chunk's number as request
// id ("<job>.3"), so the engine log and its cancellation follow it.
//
// Files: render.go (types, the job: Run, renderOnce), render_state.go
// (chunks and the running state), render_finish.go (the final WAV,
// Check audio), render_cache.go (cache pruning, WAV file helpers),
// render_models.go.

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/text"
	"github.com/hehehai/AudioSmith/internal/text/lexicon"
	"github.com/hehehai/AudioSmith/internal/text/plan"
)

// KindRender is the job kind of a take's render.
const KindRender = "take.render"

// Job error codes of renders, besides the engine's (proto.Code*), which a
// failed chunk passes on.
const (
	CodeNothingToSay      = "nothing_to_say"     // the script has no speech
	CodeRenderFailed      = "render_failed"      // joining, mastering or writing failed
	CodeEngineUnavailable = "engine_unavailable" // as engine.CodeEngineUnavailable
)

// Notice codes of TakeRender.Warnings (besides plan's and markup's
// diagnostic codes, which keep theirs) and of TakeRender.Issues (dsp/qc's
// codes).
const (
	NoticeChunkShort  = "render.chunk_short"  // much shorter than its text: words may be missing (value: seconds, params expected)
	NoticeChunkSilent = "render.chunk_silent" // no speech at all
	NoticeChunkLong   = "render.chunk_long"   // much longer than its text: the model may have rambled
	NoticePostSkipped = "render.post_skipped" // the engine's file could not be read: kept as it is
)

// MasterDefault is the mastering preset of "Even out volume".
const MasterDefault = "broadcast"

// RenderParams are a take.render job's params.
type RenderParams struct {
	// Take is the recipe: its ID and File are set; Render holds the
	// output options.
	Take core.Take `json:"take"`
	// Lang is the script's language for normalisation and the
	// dictionary (BCP-47); "" guesses it from the text.
	Lang string `json:"lang,omitempty"`
	// Output is the absolute path of the WAV to write.
	Output string `json:"output"`
	// Stream sends the audio joined so far as the job's chunks
	// (proto.ChunkParams, pcm16le) for the streaming preview.
	Stream bool `json:"stream,omitempty"`
	// NoWatermark leaves the file unmarked: an intermediate render that
	// is watermarked later as part of something else (a dub's track).
	NoWatermark bool `json:"noWatermark,omitempty"`
	// Workflow picks the model's recommended parameters
	// (ModelInfo.Defaults): "" (the Studio, the API), "story",
	// "audiobook", "dub".
	Workflow string `json:"workflow,omitempty"`
}

// RenderResult is a take.render job's result.
type RenderResult struct {
	Output       string          `json:"output"`
	SampleRate   int             `json:"sampleRate"`
	Channels     int             `json:"channels"`
	Frames       int64           `json:"frames"`
	AudioSeconds float64         `json:"audioSeconds"`
	Seconds      float64         `json:"seconds"` // the render's wall time
	Seed         int64           `json:"seed,omitempty"`
	Render       core.TakeRender `json:"render"`
}

// RenderProgress is published when a render starts a chunk, so the UI can
// say which ("chunk 3 of 12"). Topic "render.progress".
type RenderProgress struct {
	Job    string // the render job
	Owner  string // its owner ("take:<id>")
	Chunk  int    // 1-based chunk starting; 0 when finishing (mastering, writing)
	Chunks int
	Cached int // chunks taken from the cache so far
}

func (RenderProgress) Topic() string { return "render.progress" }

// ModelInfo is what the services need to know of a model (from the
// catalogue, which they do not import).
type ModelInfo struct {
	Traits
	// Label is the model's name in the UI (the catalogue's label).
	Label    string
	Revision string
	// Languages maps the values the language parameter takes to BCP-47
	// tags ("Chinese" → "zh"); VoiceLanguages built-in voices to theirs.
	Languages      map[string]string
	VoiceLanguages map[string]string
	// Defaults are the model's recommended parameters (the catalogue's
	// presets, thread #41) by workflow; "" holds the general ones. A
	// recipe that sets none of them gets these (FillDefaults).
	Defaults map[string]ParamDefaults
}

// ParamDefaults are recommended generation parameters; 0 = none (the
// model's own).
type ParamDefaults struct {
	Temperature, TopP, Guidance float64
	Steps                       int
}

// FillDefaults gives recipe t the model's recommended parameters for
// workflow where t sets none (0): the Studio, a story or an API request
// that leaves the temperature unset gets the tuned one, not the model's
// built-in default.
func (m ModelInfo) FillDefaults(t *core.Take, workflow string) {
	d, ok := m.Defaults[workflow]
	if !ok {
		d = m.Defaults[""]
	}
	if t.Temperature == 0 {
		t.Temperature = d.Temperature
	}
	if t.TopP == 0 {
		t.TopP = d.TopP
	}
	if t.Guidance == 0 {
		t.Guidance = d.Guidance
	}
	if t.Steps == 0 {
		t.Steps = d.Steps
	}
}

// Lang is the BCP-47 tag of a recipe's language for the text pipeline:
// its language parameter, else its voice's language, else "" (guess).
func (m ModelInfo) Lang(language, voice string) string {
	if l, ok := m.Languages[language]; ok {
		return l
	}
	if l, ok := m.VoiceLanguages[voice]; ok {
		return l
	}
	if language != "" && !strings.ContainsAny(language, " ") && len(language) <= 12 {
		return language // a code already ("en-US")
	}
	return ""
}

// renderer runs take.render jobs.
type renderer struct {
	synth     func() jobs.Executor
	modelPath func(ctx context.Context, model string) (string, error)
	licences  core.LicenceService
	models    func(repo string) ModelInfo
	replace   func(*core.Take) bool
	pron      *Pronunciation
	cacheDir  string
	// dataDir is the app's data folder: the longform chunks live there
	// (longformChunkDir); "" = the cache folder (tests).
	dataDir   string
	chunksDir sync.Once // the longform chunks moved out of the cache folder
	// pruneChunksMu: one pruning of the longform chunks at a time.
	pruneChunksMu sync.Mutex
	bus           *core.Bus
	logf          func(string, ...any)
	// cacheMax caps the chunk cache, in bytes.
	cacheMax int64
	// pruneAt is when pruneCache last walked the cache.
	pruneMu sync.Mutex
	pruneAt time.Time
	// watermark marks finished renders (watermark.go).
	watermark WatermarkFunc
	// engine runs the other engine requests of renders (story tracks:
	// stt.align, stt.transcribe) with the aligner and recognizer repos;
	// nil: none.
	engine              func(method string) jobs.Executor
	aligner, recognizer string
}

func (r *renderer) model(repo string) ModelInfo {
	if r.models != nil {
		return r.models(repo)
	}
	return ModelInfo{Traits: TraitsFor("", nil)}
}

func (r *renderer) chunkDir() string { return filepath.Join(r.cacheDir, "chunks") }

func (r *renderer) tmpDir() string { return filepath.Join(r.cacheDir, "tmp") }

// renderChunk is one speech segment's call.
type renderChunk struct {
	seg    plan.Segment
	params proto.SynthesizeParams
	key    string
	path   string
	chars  int
}

// Run implements jobs.Executor.
func (r *renderer) Run(ctx context.Context, job jobs.Job, report func(jobs.Progress), chunkOut func(json.RawMessage)) (json.RawMessage, error) {
	started := time.Now()
	var p RenderParams
	if err := json.Unmarshal(job.Params, &p); err != nil {
		return nil, &jobs.Error{Code: proto.CodeBadRequest, Message: "params: " + err.Error()}
	}
	if r.replace != nil && r.replace(&p.Take) {
		r.logf("render %s: %s replaces a removed model", job.ID, p.Take.Model)
		if p.Lang == "" {
			p.Lang = r.model(p.Take.Model).Lang(p.Take.Language, p.Take.Voice)
		}
	}
	t := p.Take
	opts := core.TakeRender{}
	if t.Render != nil {
		opts = *t.Render
	}
	if p.Output == "" || !filepath.IsAbs(p.Output) {
		return nil, &jobs.Error{Code: proto.CodeBadRequest, Message: "output: an absolute path is required"}
	}
	if r.licences != nil {
		if err := r.licences.Check(ctx, t.Model); err != nil {
			if errors.Is(err, core.ErrLicenceRequired) {
				data, _ := json.Marshal(map[string]string{"model": t.Model})
				return nil, &jobs.Error{Code: proto.CodeLicenceRequired, Message: err.Error(), Data: data}
			}
			return nil, &jobs.Error{Code: proto.CodeInternal, Message: err.Error()}
		}
	}
	modelPath := ""
	if r.modelPath != nil {
		dir, err := r.modelPath(ctx, t.Model)
		if err != nil {
			if errors.Is(err, core.ErrNotDownloaded) {
				data, _ := json.Marshal(map[string]string{"model": t.Model})
				return nil, &jobs.Error{Code: proto.CodeModelNotDownloaded, Message: err.Error(), Data: data}
			}
			return nil, &jobs.Error{Code: proto.CodeInternal, Message: err.Error()}
		}
		modelPath = dir
	}
	info := r.model(t.Model)
	if err := checkReference(t, info); err != nil {
		data, _ := json.Marshal(map[string]string{"model": t.Model})
		return nil, &jobs.Error{Code: core.CodeNeedsReference, Message: err.Error(), Data: data}
	}
	var seed *int64
	if opts.SeedPinned && t.Seed != 0 {
		seed = &t.Seed
	}
	// A chunk the engine finds too long (input_too_long: its own limit is
	// lower than the catalogue says) plans the take again with smaller
	// chunks; the chunks already made come from the cache.
	for attempt := 0; ; attempt++ {
		res, err := r.renderOnce(ctx, job, &p, opts, info, modelPath, &seed, started, report, chunkOut)
		var je *jobs.Error
		if attempt < 2 && errors.As(err, &je) && je.Code == proto.CodeInputTooLong {
			var d struct{ Limit, Actual, Chars float64 }
			json.Unmarshal(je.Data, &d)
			// The failed chunk's length, scaled to the engine's limit.
			f := 0.6
			if d.Limit > 0 && d.Actual > d.Limit {
				f = d.Limit / d.Actual * 0.9
			}
			chars := d.Chars
			if chars <= 0 {
				chars = float64(info.MaxChars)
			}
			ratio := float64(info.MaxCharsCJK) / float64(max(info.MaxChars, 1))
			info.MaxChars = max(min(int(chars*f), info.MaxChars-1), 20)
			info.MaxCharsCJK = max(int(float64(info.MaxChars)*ratio), 8)
			r.logf("render %s: the engine wants shorter chunks (%s); planning again at %d characters", job.ID, je.Message, info.MaxChars)
			continue
		}
		return res, err
	}
}

// renderOnce plans the take with info's chunk limits and renders it.
func (r *renderer) renderOnce(ctx context.Context, job jobs.Job, pp *RenderParams, opts core.TakeRender, info ModelInfo,
	modelPath string, seedp **int64, started time.Time, report func(jobs.Progress), chunkOut func(json.RawMessage)) (json.RawMessage, error) {
	p := *pp
	t := p.Take

	// The plan.
	dict := r.dictionary(ctx, p.Lang)
	pl := plan.Build(t.Text, plan.Options{
		Lang: p.Lang, NonVerbal: info.NonVerbal, NonVerbalTags: info.NonVerbalTags, Speed: info.Speed,
		MaxChars: info.MaxChars, MaxCharsCJK: info.MaxCharsCJK,
		NoNormalize: opts.NoNormalize, Lexicon: dict,
	})
	var warnings []core.TakeNotice
	for _, d := range pl.Diags {
		if d.Code == plan.CodeNothingToSay {
			return nil, &jobs.Error{Code: CodeNothingToSay, Message: "The script has nothing to say."}
		}
		if d.Severity >= text.Warning {
			warnings = append(warnings, core.TakeNotice{Code: d.Code, Severity: "warning", Params: d.Params})
		}
	}

	// One seed for every chunk, so the take can be made again.
	if *seedp == nil && len(pl.Speech()) > 1 {
		n, _ := rand.Int(rand.Reader, big.NewInt(math.MaxInt32))
		s := n.Int64() + 1
		*seedp = &s
	}
	seed := *seedp

	chunks, err := r.chunks(&p, &pl, info, modelPath, seed, r.chunkDir(), nil)
	if err != nil {
		return nil, &jobs.Error{Code: CodeRenderFailed, Message: err.Error()}
	}
	if err := os.MkdirAll(r.chunkDir(), 0o755); err != nil {
		return nil, &jobs.Error{Code: CodeRenderFailed, Message: err.Error()}
	}
	if err := os.MkdirAll(r.tmpDir(), 0o755); err != nil {
		return nil, &jobs.Error{Code: CodeRenderFailed, Message: err.Error()}
	}

	st := &renderState{r: r, job: job, p: &p, report: report, chunkOut: chunkOut, total: 0}
	for _, c := range chunks {
		st.total += c.chars
	}
	defer st.cleanup()

	// Speech and silences, in order.
	n := len(chunks)
	ci := 0
	cached := 0
	var takeChunks []core.TakeChunk
	single := n == 1 && len(pl.Segments) == 1
	var rawSingle string // the one chunk's file, when post-processing failed
	var rawRes proto.SynthesizeResult
	for _, seg := range pl.Segments {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if seg.Kind == plan.Silence {
			if err := st.silence(float64(seg.Millis) / 1000); err != nil {
				return nil, &jobs.Error{Code: CodeRenderFailed, Message: err.Error()}
			}
			continue
		}
		c := chunks[ci]
		ci++
		r.publish(RenderProgress{Job: job.ID, Owner: job.Owner, Chunk: ci, Chunks: n, Cached: cached})
		tc := core.TakeChunk{Text: c.seg.Text}
		res, hit, err := st.synthesize(ctx, ci, n, c)
		if err != nil {
			return nil, err
		}
		if hit {
			cached++
			tc.Cached = true
		} else {
			tc.GenSeconds = res.Seconds
			if res.Seed != nil {
				tc.Seed = *res.Seed
			}
		}
		if seed != nil && tc.Seed == 0 {
			tc.Seed = *seed
		}
		start, end, voiced, err := st.appendChunk(c.path, c.seg.Break)
		if err != nil {
			if single && !errors.Is(err, context.Canceled) {
				// The one file the engine wrote cannot be read: keep it as
				// it is rather than fail the take.
				rawSingle, rawRes = c.path, res
				warnings = append(warnings, core.TakeNotice{Code: NoticePostSkipped, Severity: "info", Params: map[string]string{"error": err.Error()}})
				takeChunks = append(takeChunks, tc)
				break
			}
			os.Remove(c.path) // unreadable: made again next time
			return nil, &jobs.Error{Code: CodeRenderFailed, Message: fmt.Sprintf("chunk %d of %d: %v", ci, n, err), Retryable: true}
		}
		tc.Start, tc.End = start, end
		takeChunks = append(takeChunks, tc)
		warnings = append(warnings, dropCheck(ci, c, t.Speed, start, end, voiced)...)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	r.publish(RenderProgress{Job: job.ID, Owner: job.Owner, Chunk: 0, Chunks: n, Cached: cached})
	report(jobs.Progress{Stage: "writing", Fraction: 1, AudioSeconds: st.seconds()})
	var out finalAudio
	if rawSingle != "" {
		out, err = copyRaw(ctx, rawSingle, p.Output)
		out.rate, out.channels, out.frames = rawRes.SampleRate, max(rawRes.Channels, 1), rawRes.Frames
		if out.frames == 0 && rawRes.SampleRate > 0 {
			out.frames = int64(rawRes.AudioSeconds * float64(rawRes.SampleRate))
		}
	} else {
		out, err = st.finish(ctx, opts)
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &jobs.Error{Code: CodeRenderFailed, Message: err.Error()}
	}
	r.pruneCache()
	var marked bool
	var notice *core.TakeNotice
	if !p.NoWatermark {
		marked, notice = r.watermarkOutput(ctx, p.Output, opts.Bits) // watermark.go
	}
	if notice != nil {
		warnings = append(warnings, *notice)
	}

	res := RenderResult{
		Output: p.Output, SampleRate: out.rate, Channels: out.channels, Frames: out.frames,
		AudioSeconds: out.seconds(), Seconds: time.Since(started).Seconds(),
	}
	if seed != nil {
		res.Seed = *seed
	} else if len(takeChunks) > 0 {
		res.Seed = takeChunks[0].Seed
	}
	res.Render = opts
	res.Render.Channels, res.Render.Bytes = out.channels, out.bytes
	res.Render.Chunks, res.Render.Warnings = takeChunks, warnings
	res.Render.Issues, res.Render.QCDone = out.issues, out.qcDone
	res.Render.Watermarked = marked
	return json.Marshal(res)
}

func (r *renderer) dictionary(ctx context.Context, lang string) *lexicon.Dictionary {
	if r.pron == nil {
		return nil
	}
	d, err := r.pron.Dictionary(ctx, lang)
	if err != nil {
		r.logf("render: pronunciation dictionary: %v", err)
		return nil
	}
	return d
}

func (r *renderer) publish(ev RenderProgress) {
	if r.bus != nil {
		r.bus.Publish(ev)
	}
}
