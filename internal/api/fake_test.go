package api

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/translate"
)

// fakeBackend is the app as the API sees it, over the real job
// scheduler with fake executors: renders write a sine WAV and stream it
// in two chunks, transcriptions return a fixed transcript, media.encode
// copies its input.
type fakeBackend struct {
	t     *testing.T
	dir   string
	sched *jobs.Sched
	bus   *core.Bus

	mu          sync.Mutex
	status      Status
	st          settings.Settings
	voices      []core.Voice
	takes       map[string]core.Take
	transcripts map[string]core.Transcript
	generated   []SpeakRequest
	transcribed []services.TranscribeRequest
	notReady    map[string]bool // repo → not downloaded
	licence     map[string]bool // repo → licence needed
	canEncode   bool
	formats     map[string]bool // M7a: FormatEncoder (ffmpeg)
	translator  func(translate.Request) (translate.Result, error)
	renderGate  chan struct{} // when set, renders wait on it
	renderRate  int
	failRender  *jobs.Error
}

func newFake(t *testing.T) *fakeBackend {
	t.Helper()
	dir := t.TempDir()
	bus := core.NewBus()
	f := &fakeBackend{t: t, dir: dir, bus: bus, takes: map[string]core.Take{}, transcripts: map[string]core.Transcript{},
		notReady: map[string]bool{}, licence: map[string]bool{}, st: settings.Defaults(), canEncode: true, renderRate: 24000,
		status: Status{State: StateReady, Version: "test", Device: "Apple M4"}}
	f.sched = jobs.New(jobs.Options{Store: jobs.NewMemStore(), Publish: func(u jobs.Updated) { bus.Publish(u) },
		NewID: func() string { return core.NewID(time.Now()) }})
	f.sched.Register(services.KindRender, jobs.ExecutorFunc(f.render))
	f.sched.Register(services.KindTranscribe, jobs.ExecutorFunc(f.transcribe))
	f.sched.Register(proto.MethodMediaEncode, jobs.ExecutorFunc(f.encode))
	if err := f.sched.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.sched.Close() })
	return f
}

// server is an API server over the fake, and an httptest server of its
// loopback handler.
func (f *fakeBackend) server() (*Server, *httptest.Server) {
	s := New(Options{Backend: f, Sched: f.sched, Bus: f.bus, Version: "1.2.3", Logf: f.t.Logf})
	ts := httptest.NewServer(s.Handler())
	f.t.Cleanup(func() {
		ts.Close()
		s.Close(context.Background())
	})
	return s, ts
}

func (f *fakeBackend) Status() Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func (f *fakeBackend) Settings() settings.Settings {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.st
}

func (f *fakeBackend) update(fn func(*fakeBackend)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeBackend) Models(ctx context.Context) ([]Model, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Model
	for _, e := range catalog.Builtin().Entries {
		out = append(out, Model{Entry: e, Downloaded: !f.notReady[e.Repo], LicenceNeeded: f.licence[e.Repo]})
	}
	return out, nil
}

func (f *fakeBackend) DownloadModel(ctx context.Context, id string) (string, error) {
	return f.sched.Submit(ctx, jobs.Spec{Kind: "model.download", Lane: jobs.LaneNet, Owner: "model:" + id, Title: "Download"})
}

func (f *fakeBackend) Voices(ctx context.Context, q core.VoiceQuery) ([]core.Voice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.voices), nil
}

func (f *fakeBackend) Voice(ctx context.Context, id string) (core.Voice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, v := range f.voices {
		if v.ID == id {
			return v, nil
		}
	}
	return core.Voice{}, fmt.Errorf("voice %s: %w", id, core.ErrNotFound)
}

func (f *fakeBackend) SaveVoice(ctx context.Context, v core.Voice, files core.VoiceFiles) (core.Voice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v.ID == "" {
		v.ID = core.NewID(time.Now())
	}
	if files.Reference != "" {
		if _, err := os.Stat(files.Reference); err != nil {
			return v, err
		}
		v.RefAudio = "reference" + filepath.Ext(files.Reference)
	}
	v.CreatedAt, v.UpdatedAt = time.Now(), time.Now()
	f.voices = append(f.voices, v)
	return v, nil
}

func (f *fakeBackend) DeleteVoice(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := slices.IndexFunc(f.voices, func(v core.Voice) bool { return v.ID == id })
	if i < 0 {
		return fmt.Errorf("voice %s: %w", id, core.ErrNotFound)
	}
	f.voices = slices.Delete(f.voices, i, i+1)
	return nil
}

func (f *fakeBackend) VoiceFile(v core.Voice, name string) string {
	return filepath.Join(f.dir, "voices", v.ID, name)
}

// VoiceFits: clones need a cloning model; designed voices a design model.
func (f *fakeBackend) VoiceFits(v core.Voice, e catalog.Entry) (bool, string) {
	switch v.Kind {
	case core.VoiceCloned:
		if !e.Has(catalog.CapClone) {
			return false, e.Label + " cannot clone voices."
		}
	case core.VoiceDesigned:
		if !e.Has(catalog.CapDesign) && !e.Has(catalog.CapClone) {
			return false, e.Label + " cannot speak designed voices."
		}
	}
	return true, ""
}

func (f *fakeBackend) Generate(ctx context.Context, req SpeakRequest) (core.Take, string, error) {
	f.mu.Lock()
	f.generated = append(f.generated, req)
	notReady, licence := f.notReady[req.Model.Repo], f.licence[req.Model.Repo]
	f.mu.Unlock()
	if licence {
		return core.Take{}, "", &core.LicenceRequiredError{Model: req.Model.Repo, Licence: req.Model.Licence.ID, Name: req.Model.Licence.Name, URL: req.Model.Licence.URL}
	}
	if notReady {
		return core.Take{}, "", fmt.Errorf("%s: %w", req.Model.Label, core.ErrNotDownloaded)
	}
	t := core.Take{ID: req.ID, Text: req.Text, Model: req.Model.Repo, Voice: req.Speaker, Language: req.Language,
		Speed: req.Speed, Seed: req.Seed, File: req.ID + ".wav", CreatedAt: time.Now()}
	if req.Voice != nil {
		t.VoiceID = req.Voice.ID
	}
	params, _ := json.Marshal(services.RenderParams{Take: t, Output: f.TakeAudio(t), Stream: !req.Background})
	id, err := f.sched.Submit(ctx, jobs.Spec{Kind: services.KindRender, Lane: jobs.LaneGPU, Priority: jobs.PriorityInteractive,
		Params: params, Owner: "take:" + req.ID, Title: "Generate take"})
	return t, id, err
}

// render writes 0.5 s of sine per 10 characters and streams it in two chunks.
func (f *fakeBackend) render(ctx context.Context, job jobs.Job, report func(jobs.Progress), chunk func(json.RawMessage)) (json.RawMessage, error) {
	f.mu.Lock()
	gate, rate, fail := f.renderGate, f.renderRate, f.failRender
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if fail != nil {
		return nil, fail
	}
	var p services.RenderParams
	json.Unmarshal(job.Params, &p)
	frames := max(len([]rune(p.Take.Text))*rate/20, rate/10)
	buf := dsp.New(rate, 1, frames)
	for i := range buf.Samples {
		buf.Samples[i] = float32(0.3 * math.Sin(2*math.Pi*440*float64(i)/float64(rate)))
	}
	os.MkdirAll(filepath.Dir(p.Output), 0o755)
	if err := wav.WriteFile(p.Output, buf, wav.PCM16, wav.Options{}); err != nil {
		return nil, err
	}
	if chunk != nil {
		pcm := make([]byte, 0, frames*2)
		for _, v := range buf.Samples {
			pcm = binary.LittleEndian.AppendUint16(pcm, uint16(int16(v*32767)))
		}
		half := (len(pcm) / 4) * 2
		for i, part := range [][]byte{pcm[:half], pcm[half:]} {
			c, _ := json.Marshal(proto.ChunkParams{Job: job.ID, Seq: i, SampleRate: rate, Channels: 1, Encoding: "pcm16le", Data: part, Final: i == 1})
			chunk(c)
		}
	}
	f.mu.Lock()
	t := p.Take
	t.SampleRate, t.AudioSeconds, t.GenSeconds, t.JobID = rate, float64(frames)/float64(rate), 0.1, job.ID
	f.takes[t.ID] = t
	f.mu.Unlock()
	return json.Marshal(services.RenderResult{Output: p.Output, SampleRate: rate, Channels: 1, Frames: int64(frames),
		AudioSeconds: float64(frames) / float64(rate), Seconds: 0.1, Seed: 42})
}

func (f *fakeBackend) Takes(ctx context.Context, q core.TakeQuery) ([]core.Take, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []core.Take
	for _, t := range f.takes {
		out = append(out, t)
	}
	return out, nil
}

func (f *fakeBackend) Take(ctx context.Context, id string) (core.Take, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.takes[id]
	if !ok {
		return t, fmt.Errorf("take %s: %w", id, core.ErrNotFound)
	}
	return t, nil
}

func (f *fakeBackend) TakeAudio(t core.Take) string {
	return filepath.Join(f.dir, "clips", t.ID+".wav")
}

func (f *fakeBackend) TakeSaved(context.Context, string) error { return nil }

func (f *fakeBackend) DeleteTake(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.takes[id]; !ok {
		return fmt.Errorf("take %s: %w", id, core.ErrNotFound)
	}
	delete(f.takes, id)
	return nil
}

func (f *fakeBackend) Transcribe(ctx context.Context, req services.TranscribeRequest) (string, error) {
	f.mu.Lock()
	f.transcribed = append(f.transcribed, req)
	notReady := f.notReady[req.Model]
	f.mu.Unlock()
	if notReady {
		return "", fmt.Errorf("%s: %w", req.Model, core.ErrNotDownloaded)
	}
	if _, err := os.Stat(req.Input); err != nil {
		return "", fmt.Errorf("input: %w", core.ErrInvalid)
	}
	params, _ := json.Marshal(req)
	return f.sched.Submit(ctx, jobs.Spec{Kind: services.KindTranscribe, Lane: jobs.LaneGPU, Priority: jobs.PriorityInteractive,
		Params: params, Owner: services.TranscriptOwner + req.ID})
}

// sampleTranscript is what every transcription finds.
func sampleTranscript(id, model string, words bool) core.Transcript {
	t := core.Transcript{ID: id, Model: model, Language: "fr", Duration: 3.2, Source: core.SourceFile,
		Segments: []core.TranscriptSegment{
			{Start: 0, End: 1.4, Text: "Bonjour tout le monde."},
			{Start: 1.6, End: 3.1, Text: "Ça va bien."},
		}}
	if words {
		t.Segments[0].Words = []core.TranscriptWord{{Text: "Bonjour", Start: 0, End: 0.5}, {Text: "tout", Start: 0.5, End: 0.8},
			{Text: "le", Start: 0.8, End: 0.9}, {Text: "monde.", Start: 0.9, End: 1.4}}
		t.Segments[1].Words = []core.TranscriptWord{{Text: "Ça", Start: 1.6, End: 1.9}, {Text: "va", Start: 1.9, End: 2.3}, {Text: "bien.", Start: 2.3, End: 3.1}}
	}
	return t
}

func (f *fakeBackend) transcribe(ctx context.Context, job jobs.Job, report func(jobs.Progress), _ func(json.RawMessage)) (json.RawMessage, error) {
	var req services.TranscribeRequest
	json.Unmarshal(job.Params, &req)
	t := sampleTranscript(req.ID, req.Model, req.Words || req.Aligner != "")
	t.Title, t.CreatedAt = req.Title, time.Now()
	if services.IsSpeakersModel(req.Model) {
		t.Segments[0].Speaker, t.Segments[1].Speaker = "S1", "S2"
		t.Speakers = map[string]string{"S1": "Ana"}
	}
	f.mu.Lock()
	f.transcripts[t.ID] = t
	f.mu.Unlock()
	return json.Marshal(services.TranscribeResult{Transcript: t})
}

func (f *fakeBackend) encode(ctx context.Context, job jobs.Job, report func(jobs.Progress), _ func(json.RawMessage)) (json.RawMessage, error) {
	var p proto.EncodeParams
	json.Unmarshal(job.Params, &p)
	data, err := os.ReadFile(p.Input)
	if err != nil {
		return nil, err
	}
	format := p.Output.Format
	if format == "" {
		format = "m4a"
	}
	if err := os.WriteFile(p.Output.Path, append([]byte(strings.ToUpper(format)+":"), data...), 0o644); err != nil {
		return nil, err
	}
	return json.Marshal(proto.EncodeResult{Output: p.Output.Path, Format: format})
}

func (f *fakeBackend) Transcripts(ctx context.Context, q core.TranscriptQuery) ([]core.Transcript, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []core.Transcript
	for _, t := range f.transcripts {
		out = append(out, t)
	}
	return out, nil
}

func (f *fakeBackend) Transcript(ctx context.Context, id string) (core.Transcript, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.transcripts[id]
	if !ok {
		return t, fmt.Errorf("transcript %s: %w", id, core.ErrNotFound)
	}
	return t, nil
}

func (f *fakeBackend) DeleteTranscript(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.transcripts, id)
	return nil
}

func (f *fakeBackend) Translate(ctx context.Context, req translate.Request) (translate.Result, error) {
	f.mu.Lock()
	tr := f.translator
	f.mu.Unlock()
	if tr == nil {
		return translate.Result{}, fmt.Errorf("no language model: %w", core.ErrUnsupported)
	}
	return tr(req)
}

func (f *fakeBackend) Direct(ctx context.Context, note string) translate.Direction {
	return translate.ParseDirection(note)
}

func (f *fakeBackend) Fit(ctx context.Context, req translate.FitRequest) (translate.FitResult, error) {
	return (&translate.Fitter{}).Check(ctx, req)
}

func (f *fakeBackend) Probe(ctx context.Context, path string) (proto.ProbeResult, error) {
	if _, err := os.Stat(path); err != nil {
		return proto.ProbeResult{}, &proto.Error{Code: proto.CodeFileNotFound, Message: "no such file"}
	}
	return proto.ProbeResult{Format: strings.TrimPrefix(filepath.Ext(path), "."), Duration: 1.5, HasAudio: true, SampleRate: 24000, Channels: 1}, nil
}

// EncodesFormat is FormatEncoder (M7a).
func (f *fakeBackend) EncodesFormat(format string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.formats[format]
}

func (f *fakeBackend) DetectWatermark(ctx context.Context, path string) (proto.WatermarkDetectResult, error) {
	if _, err := os.Stat(path); err != nil {
		return proto.WatermarkDetectResult{}, &proto.Error{Code: proto.CodeFileNotFound, Message: "no such file"}
	}
	return proto.WatermarkDetectResult{Probability: 1, Detected: true, Message: 0xA53C, Bits: make([]float64, 16), Confidence: 0.9, AudioSeconds: 1.5}, nil
}

func (f *fakeBackend) CanEncode() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.canEncode
}

func (f *fakeBackend) TempDir() string { return filepath.Join(f.dir, "tmp") }
func (f *fakeBackend) DataDir() string { return f.dir }

// do sends a request with the CSRF header and decodes a JSON answer.
func do(t *testing.T, ts *httptest.Server, method, path string, body any, out any, hdr ...string) *http.Response {
	t.Helper()
	var rd *strings.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			rd = strings.NewReader(b)
		default:
			data, _ := json.Marshal(b)
			rd = strings.NewReader(string(data))
		}
	} else {
		rd = strings.NewReader("")
	}
	req, _ := http.NewRequest(method, ts.URL+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(HeaderCSRF, "1")
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("%s %s: decoding: %v", method, path, err)
		}
	}
	return resp
}

// apiErr is an error answer.
type apiErr struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
		Param   any    `json:"param"`
	} `json:"error"`
}
