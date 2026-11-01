package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
)

// MaxInput is the longest text of one native or MCP speech request
// (OpenAI's route keeps OpenAI's 4096): longer scripts belong in
// Audiobook or Stories.
const MaxInput = 100_000

// SpeechParams is a speech request as clients write it: models and voices
// by name. Zero values leave the model's defaults.
type SpeechParams struct {
	Text string
	// Model: a catalogue id, repo or label, or one of OpenAI's names (the
	// default model; "" too).
	Model string
	// Voice: a saved voice's id or name, a built-in speaker of the
	// model, an OpenAI voice name, or "" (the client's bound voice, else
	// the model's default).
	Voice string
	// Instructions: delivery (instruct models) or, on a design model
	// without a saved voice, the voice to design.
	Instructions string
	Language     string

	Speed, Temperature, TopP, Guidance, Exaggeration, Duration float64
	Seed                                                       int64
	Steps                                                      int
	Normalize                                                  bool
	Bits                                                       int
	// RefAudio (a readable path) clones without saving a voice.
	RefAudio, RefText string
	// Client is the agent asking (MCP, X-AudioSmith-Client): its bound
	// voice and model apply when Voice and Model are empty.
	Client     string
	Background bool
}

// Speech is a speech request in the queue.
type Speech struct {
	// Recipe is the take being made: model, voice and parameters as the
	// engine gets them.
	Recipe core.Take
	Model  catalog.Entry
	JobID  string
	// VoiceName names the voice used, for headers and answers.
	VoiceName string
	// Ignored lists the parameters the model does not honour.
	Ignored []string
	// Replaced is set when the request named a removed model.
	Replaced *Replacement

	s *Server
	x *watch
}

// SpeechResult is a finished take.
type SpeechResult struct {
	Take core.Take // the recipe and its result (the take is saved in the history)
	// Output is the take's WAV file.
	Output string
}

// engineGate refuses work while no engine can run it.
func (s *Server) engineGate() error {
	st := s.b.Status()
	switch st.State {
	case StateFailed, StateStopped:
		retry := st.RetryAfter
		if retry <= 0 {
			retry = 30
		}
		msg := "The engine is not running"
		if st.Detail != "" {
			msg += ": " + st.Detail
		}
		return errUnavailable(CodeEngineUnavailable, retry, "%s.", msg)
	}
	return nil
}

// queueTimeout is how long a request may wait for the engine.
func (s *Server) queueTimeout() time.Duration {
	if n := s.b.Settings().API.QueueSeconds; n > 0 {
		return time.Duration(n) * time.Second
	}
	return 120 * time.Second
}

// prepare resolves a request's model and voice.
func (s *Server) prepare(ctx context.Context, p SpeechParams) (SpeakRequest, []string, string, *Replacement, error) {
	text := strings.TrimSpace(p.Text)
	if text == "" {
		return SpeakRequest{}, nil, "", nil, errInvalid("input", "There is no text to speak.")
	}
	if n := utf8.RuneCountInString(text); n > MaxInput {
		return SpeakRequest{}, nil, "", nil, errInvalid("input", "The text has %d characters; the limit is %d.", n, MaxInput)
	}
	if p.Voice == "" && p.Model == "" && p.Client != "" {
		if b, ok := s.b.Settings().API.Binding(p.Client); ok {
			p.Voice, p.Model = b.Voice, b.Model
		}
	} else if p.Voice == "" && p.Client != "" {
		if b, ok := s.b.Settings().API.Binding(p.Client); ok {
			p.Voice = b.Voice
		}
	}
	e, vc, rep, err := s.resolveTTS(ctx, p.Model, p.Voice, strings.TrimSpace(p.RefAudio) != "")
	if err != nil {
		return SpeakRequest{}, nil, "", nil, err
	}
	if p.RefAudio != "" && !e.Has(catalog.CapClone) {
		return SpeakRequest{}, nil, "", nil, &Error{Status: http.StatusBadRequest, Code: CodeUnsupportedModel, Param: "model",
			Message: e.Label + " cannot clone a voice: pick a model that can (GET /api/models)."}
	}
	if e.NeedsReference && strings.TrimSpace(p.RefAudio) == "" && vc.Saved == nil {
		// A saved voice without a reference was refused above (VoiceFits).
		return SpeakRequest{}, nil, "", nil, errNeedsReference(e.Label)
	}
	req := SpeakRequest{Text: text, Model: e, Voice: vc.Saved, Speaker: vc.Speaker, Gender: vc.Gender,
		Instructions: strings.TrimSpace(p.Instructions), Language: strings.TrimSpace(p.Language),
		RefAudio: p.RefAudio, RefText: p.RefText,
		Speed: p.Speed, Temperature: p.Temperature, TopP: p.TopP, Guidance: p.Guidance, Exaggeration: p.Exaggeration,
		Duration: p.Duration, Seed: p.Seed, Steps: p.Steps, Normalize: p.Normalize, Bits: p.Bits, Background: p.Background, Client: p.Client}
	name := vc.Speaker
	switch {
	case vc.Saved != nil:
		name = vc.Saved.Name
	case vc.Named != "":
		name = vc.Named
	}
	return req, ignoredParams(e, req), name, rep, nil
}

// ignoredParams lists what model e will not honour of req.
func ignoredParams(e catalog.Entry, r SpeakRequest) []string {
	var out []string
	add := func(set bool, name string, caps ...catalog.Capability) {
		if !set {
			return
		}
		for _, c := range caps {
			if e.Has(c) {
				return
			}
		}
		out = append(out, name)
	}
	add(r.Speed != 0 && r.Speed != 1, "speed", catalog.CapSpeed)
	add(r.Instructions != "", "instructions", catalog.CapInstruct, catalog.CapVoiceStyle, catalog.CapDesign)
	add(r.Seed != 0, "seed", catalog.CapSeed, catalog.CapSampling, catalog.CapDesign)
	add(r.Duration != 0, "duration", catalog.CapDuration)
	add(r.Steps != 0, "steps", catalog.CapSteps)
	add(r.Temperature != 0 || r.TopP != 0, "temperature", catalog.CapSampling)
	add(r.Guidance != 0, "guidance", catalog.CapGuidance)
	add(r.Exaggeration != 0, "exaggeration") // only the removed Chatterbox (multilingual) took it
	add(r.Language != "", "language", catalog.CapLanguage)
	return out
}

// StartSpeech queues a speech request. Follow it with Wait.
func (s *Server) StartSpeech(ctx context.Context, p SpeechParams) (*Speech, error) {
	if err := s.engineGate(); err != nil {
		return nil, err
	}
	req, ignored, name, rep, err := s.prepare(ctx, p)
	if err != nil {
		return nil, err
	}
	req.ID = core.NewID(time.Now())
	x := s.w.owner("take:" + req.ID)
	recipe, jobID, err := s.b.Generate(ctx, req)
	if err != nil {
		x.stop()
		return nil, err
	}
	return &Speech{Recipe: recipe, Model: req.Model, JobID: jobID, VoiceName: name, Ignored: ignored, Replaced: rep, s: s, x: x}, nil
}

// errRestarted: the engine restarted while audio was being streamed, so
// the stream cannot continue seamlessly.
var errRestarted = errors.New("the engine restarted during the stream")

// Wait follows the take until it is made. Chunks of audio (pcm16le, the
// take's rate) go to onChunk as they are made, when it is set. If ctx
// ends, the take is cancelled.
func (sp *Speech) Wait(ctx context.Context, onChunk func(proto.ChunkParams) error) (SpeechResult, error) {
	defer sp.x.stop()
	attempt, sent := 0, false
	job, err := waitJob(ctx, sp.s.sched, sp.x, sp.JobID, sp.s.queueTimeout(), func(u jobs.Updated) error {
		if u.Chunk == nil || onChunk == nil {
			return nil
		}
		if sent && u.Job.Attempt != attempt {
			return errRestarted
		}
		attempt = u.Job.Attempt
		var c proto.ChunkParams
		if err := json.Unmarshal(u.Chunk, &c); err != nil || len(c.Data) == 0 {
			return nil
		}
		sent = true
		return onChunk(c)
	})
	if err != nil {
		return SpeechResult{}, err
	}
	var res services.RenderResult
	if err := jobOutcome(job, &res); err != nil {
		return SpeechResult{}, err
	}
	t := sp.Recipe
	t.JobID = job.ID
	t.SampleRate, t.AudioSeconds, t.GenSeconds = res.SampleRate, res.AudioSeconds, res.Seconds
	if res.Seed != 0 {
		t.Seed = res.Seed
	}
	out := res.Output
	if out == "" {
		out = sp.s.b.TakeAudio(t)
	}
	return SpeechResult{Take: t, Output: out}, nil
}

// Cancel cancels a speech request that is not waited for.
func (sp *Speech) Cancel() {
	sp.x.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sp.s.sched.Cancel(ctx, sp.JobID)
}

// speechHeaders describe a take in response headers.
func speechHeaders(h http.Header, sp *Speech) {
	h.Set("X-AudioSmith-Take-Id", sp.Recipe.ID)
	h.Set("X-AudioSmith-Job-Id", sp.JobID)
	h.Set("X-AudioSmith-Model", sp.Model.ID)
	if sp.VoiceName != "" {
		h.Set("X-AudioSmith-Voice", sp.VoiceName)
	}
	if len(sp.Ignored) > 0 {
		h.Set("X-AudioSmith-Ignored", strings.Join(sp.Ignored, ", "))
	}
	if r := sp.Replaced; r != nil {
		h.Set("X-AudioSmith-Replaced-By", r.ReplacedBy)
		h.Set("X-AudioSmith-Warning", r.Warning)
	}
}
