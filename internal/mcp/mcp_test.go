package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
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

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hehehai/AudioSmith/internal/api"
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

// fake is a lean api.Backend over the real scheduler: renders write
// 0.2 s of sine, transcriptions answer "hello world".
type fake struct {
	dir   string
	sched *jobs.Sched
	bus   *core.Bus

	mu        sync.Mutex
	st        settings.Settings
	voices    []core.Voice
	takes     map[string]core.Take
	generated []api.SpeakRequest
	licence   map[string]bool
	inputs    []string
}

func newFake(t *testing.T) *fake {
	t.Helper()
	f := &fake{dir: t.TempDir(), bus: core.NewBus(), st: settings.Defaults(), takes: map[string]core.Take{}, licence: map[string]bool{}}
	f.sched = jobs.New(jobs.Options{Store: jobs.NewMemStore(), Publish: func(u jobs.Updated) { f.bus.Publish(u) },
		NewID: func() string { return core.NewID(time.Now()) }})
	f.sched.Register(services.KindRender, jobs.ExecutorFunc(f.render))
	f.sched.Register(services.KindTranscribe, jobs.ExecutorFunc(f.transcribe))
	f.sched.Start(context.Background())
	t.Cleanup(func() { f.sched.Close() })
	return f
}

func (f *fake) Status() api.Status { return api.Status{State: api.StateReady, Device: "Apple M4"} }
func (f *fake) Settings() settings.Settings {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.st
}
func (f *fake) Models(ctx context.Context) ([]api.Model, error) {
	var out []api.Model
	for _, e := range catalog.Builtin().Entries {
		out = append(out, api.Model{Entry: e, Downloaded: true, LicenceNeeded: f.licence[e.Repo]})
	}
	return out, nil
}
func (f *fake) DownloadModel(ctx context.Context, id string) (string, error) { return "", nil }
func (f *fake) Voices(ctx context.Context, q core.VoiceQuery) ([]core.Voice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.voices), nil
}
func (f *fake) Voice(ctx context.Context, id string) (core.Voice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, v := range f.voices {
		if v.ID == id {
			return v, nil
		}
	}
	return core.Voice{}, core.ErrNotFound
}
func (f *fake) SaveVoice(ctx context.Context, v core.Voice, files core.VoiceFiles) (core.Voice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if files.Reference != "" {
		data, err := os.ReadFile(files.Reference)
		if err != nil {
			return v, err
		}
		f.inputs = append(f.inputs, string(data))
		v.RefAudio = "reference.wav"
	}
	v.ID = core.NewID(time.Now())
	f.voices = append(f.voices, v)
	return v, nil
}
func (f *fake) DeleteVoice(ctx context.Context, id string) error { return nil }
func (f *fake) VoiceFile(v core.Voice, name string) string       { return filepath.Join(f.dir, name) }
func (f *fake) VoiceFits(v core.Voice, e catalog.Entry) (bool, string) {
	if v.Kind == core.VoiceCloned && !e.Has(catalog.CapClone) {
		return false, "cannot clone"
	}
	return true, ""
}
func (f *fake) Generate(ctx context.Context, req api.SpeakRequest) (core.Take, string, error) {
	f.mu.Lock()
	f.generated = append(f.generated, req)
	lic := f.licence[req.Model.Repo]
	f.mu.Unlock()
	if lic {
		return core.Take{}, "", &core.LicenceRequiredError{Model: req.Model.Repo, Name: "CC BY-NC 4.0"}
	}
	t := core.Take{ID: req.ID, Text: req.Text, Model: req.Model.Repo, File: req.ID + ".wav"}
	p, _ := json.Marshal(services.RenderParams{Take: t, Output: f.TakeAudio(t), Stream: true})
	id, err := f.sched.Submit(ctx, jobs.Spec{Kind: services.KindRender, Lane: jobs.LaneGPU, Params: p, Owner: "take:" + req.ID})
	return t, id, err
}
func (f *fake) render(ctx context.Context, job jobs.Job, _ func(jobs.Progress), _ func(json.RawMessage)) (json.RawMessage, error) {
	var p services.RenderParams
	json.Unmarshal(job.Params, &p)
	buf := dsp.New(24000, 1, 4800)
	for i := range buf.Samples {
		buf.Samples[i] = float32(0.2 * math.Sin(float64(i)/10))
	}
	os.MkdirAll(filepath.Dir(p.Output), 0o755)
	if err := wav.WriteFile(p.Output, buf, wav.PCM16, wav.Options{}); err != nil {
		return nil, err
	}
	t := p.Take
	t.CreatedAt, t.AudioSeconds = time.Now(), 0.2
	f.mu.Lock()
	f.takes[t.ID] = t
	f.mu.Unlock()
	return json.Marshal(services.RenderResult{Output: p.Output, SampleRate: 24000, Channels: 1, Frames: 4800, AudioSeconds: 0.2, Seconds: 0.05, Seed: 7})
}
func (f *fake) Takes(ctx context.Context, q core.TakeQuery) ([]core.Take, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []core.Take
	for _, t := range f.takes {
		out = append(out, t)
	}
	return out, nil
}
func (f *fake) Take(ctx context.Context, id string) (core.Take, error) { return f.takes[id], nil }
func (f *fake) TakeAudio(t core.Take) string                           { return filepath.Join(f.dir, "clips", t.ID+".wav") }
func (f *fake) DeleteTake(ctx context.Context, id string) error        { return nil }
func (f *fake) Transcribe(ctx context.Context, req services.TranscribeRequest) (string, error) {
	data, err := os.ReadFile(req.Input)
	if err != nil {
		return "", fmt.Errorf("input: %w", core.ErrInvalid)
	}
	f.mu.Lock()
	f.inputs = append(f.inputs, string(data))
	f.mu.Unlock()
	p, _ := json.Marshal(req)
	return f.sched.Submit(ctx, jobs.Spec{Kind: services.KindTranscribe, Lane: jobs.LaneGPU, Params: p, Owner: services.TranscriptOwner + req.ID})
}
func (f *fake) transcribe(ctx context.Context, job jobs.Job, _ func(jobs.Progress), _ func(json.RawMessage)) (json.RawMessage, error) {
	var req services.TranscribeRequest
	json.Unmarshal(job.Params, &req)
	t := core.Transcript{ID: req.ID, Language: "en", Duration: 1, Segments: []core.TranscriptSegment{{Start: 0, End: 1, Text: "hello world",
		Words: []core.TranscriptWord{{Text: "hello", Start: 0, End: 0.5}, {Text: "world", Start: 0.5, End: 1}}}}}
	if services.IsSpeakersModel(req.Model) {
		t.Segments[0].Speaker = "S1"
		t.Segments = append(t.Segments, core.TranscriptSegment{Start: 1.2, End: 2, Text: "hi there", Speaker: "S2"})
		t.Speakers = map[string]string{"S2": "Bo"}
	}
	return json.Marshal(services.TranscribeResult{Transcript: t})
}
func (f *fake) Transcripts(ctx context.Context, q core.TranscriptQuery) ([]core.Transcript, error) {
	return nil, nil
}
func (f *fake) Transcript(ctx context.Context, id string) (core.Transcript, error) {
	return core.Transcript{}, core.ErrNotFound
}
func (f *fake) DeleteTranscript(ctx context.Context, id string) error { return nil }
func (f *fake) Translate(ctx context.Context, req translate.Request) (translate.Result, error) {
	return translate.Result{}, core.ErrUnsupported
}
func (f *fake) Direct(ctx context.Context, note string) translate.Direction {
	return translate.ParseDirection(note)
}
func (f *fake) Fit(ctx context.Context, req translate.FitRequest) (translate.FitResult, error) {
	return translate.FitResult{}, nil
}
func (f *fake) Probe(ctx context.Context, path string) (proto.ProbeResult, error) {
	return proto.ProbeResult{}, nil
}
func (f *fake) CanEncode() bool { return false }
func (f *fake) DetectWatermark(context.Context, string) (proto.WatermarkDetectResult, error) {
	return proto.WatermarkDetectResult{}, nil
}
func (f *fake) TempDir() string { return filepath.Join(f.dir, "tmp") }
func (f *fake) DataDir() string { return f.dir }

// serve runs the API with the MCP server mounted; it returns both and the
// URL of the loopback handler.
func serve(t *testing.T, f *fake) (*Server, *api.Server, *httptest.Server) {
	t.Helper()
	a := api.New(api.Options{Backend: f, Sched: f.sched, Bus: f.bus, Version: "9.9", Logf: t.Logf})
	m := New(Options{API: a, Backend: f, Version: "9.9"})
	a.MountMCP(m.Handler())
	ts := httptest.NewServer(a.Handler())
	t.Cleanup(func() {
		ts.Close()
		a.Close(context.Background())
	})
	return m, a, ts
}

// connect is an MCP client session over Streamable HTTP.
func connect(t *testing.T, url, name string, header http.Header) *sdk.ClientSession {
	t.Helper()
	hc := &http.Client{Transport: headerRT{header}}
	c := sdk.NewClient(&sdk.Implementation{Name: name, Version: "1.0"}, nil)
	cs, err := c.Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: url + "/mcp", HTTPClient: hc}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

type headerRT struct{ h http.Header }

func (r headerRT) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	for k, v := range r.h {
		req.Header[k] = v
	}
	return http.DefaultTransport.RoundTrip(req)
}

func call(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) *sdk.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

func textOf(res *sdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestTools(t *testing.T) {
	f := newFake(t)
	m, _, ts := serve(t, f)
	cs := connect(t, ts.URL, "claude-code", nil)
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range tools.Tools {
		names = append(names, tl.Name)
	}
	slices.Sort(names)
	want := append([]string{"check_health", "clone_voice", "describe_voice", "design_voice", "generate_speech", "list_languages", "list_models", "list_voices", "transcribe"}, append(StudioTools, KitTools...)...)
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Fatalf("tools %v", names)
	}

	// generate_speech, inline + file (the default).
	res := call(t, cs, "generate_speech", map[string]any{"text": "Hello from an agent.", "voice": "af_heart", "filename": "../../escape"})
	if res.IsError {
		t.Fatalf("error: %s", textOf(res))
	}
	var audio *sdk.AudioContent
	for _, c := range res.Content {
		if a, ok := c.(*sdk.AudioContent); ok {
			audio = a
		}
	}
	var out speechOut
	json.Unmarshal([]byte(textOf(res)), &out)
	if audio == nil || audio.MIMEType != "audio/wav" || string(audio.Data[:4]) != "RIFF" || out.TakeID == "" || out.Seed != 7 {
		t.Fatalf("speech %+v %+v", res.Content, out)
	}
	if filepath.Dir(out.File) != m.Sandbox() || filepath.Base(out.File) != "escape.wav" {
		t.Fatalf("file %q (sandbox %s)", out.File, m.Sandbox())
	}
	if _, err := os.Stat(out.File); err != nil {
		t.Fatal(err)
	}
	// file only.
	res = call(t, cs, "generate_speech", map[string]any{"text": "Again.", "output": "file"})
	for _, c := range res.Content {
		if _, ok := c.(*sdk.AudioContent); ok {
			t.Fatal("audio inline in file mode")
		}
	}
	// A removed model (thread #43): its replacement answers, and says so.
	res = call(t, cs, "generate_speech", map[string]any{"text": "Hi.", "model": "kitten-tts-mini-0.8", "output": "file"})
	out = speechOut{}
	json.Unmarshal([]byte(textOf(res)), &out)
	if res.IsError || out.Model != "kokoro-82m-bf16" || out.ReplacedBy != "kokoro-82m-bf16" || !strings.Contains(out.Warning, "KittenTTS mini was removed") {
		t.Fatalf("removed model: %v %s", res.IsError, textOf(res))
	}
	res = call(t, cs, "list_models", map[string]any{})
	var models struct {
		Models []map[string]any `json:"models"`
	}
	json.Unmarshal([]byte(textOf(res)), &models)
	for _, m := range models.Models { // thread #45: friendly names, a line, tags
		if m["display_name"] == "" || m["display_name"] == nil || m["ui_name"] == nil || m["description"] == "" || m["tags"] == nil || m["label"] == nil {
			t.Errorf("names: %v", m)
		}
		if m["id"] == "kokoro-82m-bf16" && (m["display_name"] != "Fast English voices" || m["label"] != "Kokoro 82M") {
			t.Errorf("kokoro: %v", m)
		}
	}
	if len(models.Models) == 0 {
		t.Error("no models")
	}
	for _, r := range catalog.Builtin().Removed {
		if strings.Contains(textOf(res), `"`+r.ID+`"`) {
			t.Errorf("list_models lists %s", r.ID)
		}
	}
	// A model whose licence is not accepted: a tool error saying so.
	omni, _ := catalog.Builtin().ByID("omnivoice-bf16")
	f.mu.Lock()
	f.licence[omni.Repo] = true
	f.mu.Unlock()
	res = call(t, cs, "generate_speech", map[string]any{"text": "Hi.", "model": "omnivoice-bf16"})
	if !res.IsError || !strings.Contains(textOf(res), "licence_required") {
		t.Fatalf("licence: %v %s", res.IsError, textOf(res))
	}

	// clone_voice needs consent; from base64.
	res = call(t, cs, "clone_voice", map[string]any{"name": "Me", "audio_base64": base64.StdEncoding.EncodeToString([]byte("RIFFme")), "consent": false})
	if !res.IsError || !strings.Contains(textOf(res), "consent") {
		t.Fatalf("consent: %s", textOf(res))
	}
	res = call(t, cs, "clone_voice", map[string]any{"name": "Me", "audio_base64": "data:audio/wav;base64," + base64.StdEncoding.EncodeToString([]byte("RIFFme")), "consent": true})
	if res.IsError || !strings.Contains(textOf(res), `"name": "Me"`) {
		t.Fatalf("clone: %s", textOf(res))
	}
	// A path on this Mac (loopback).
	ref := filepath.Join(t.TempDir(), "ref.wav")
	os.WriteFile(ref, []byte("RIFFpath"), 0o644)
	res = call(t, cs, "clone_voice", map[string]any{"name": "Path", "path": ref, "consent": true})
	if res.IsError {
		t.Fatalf("clone by path: %s", textOf(res))
	}
	f.mu.Lock()
	inputs := slices.Clone(f.inputs)
	f.mu.Unlock()
	if !slices.Equal(inputs, []string{"RIFFme", "RIFFpath"}) {
		t.Fatalf("inputs %q", inputs)
	}

	res = call(t, cs, "design_voice", map[string]any{"description": "a calm young woman, British accent"})
	if res.IsError || !strings.Contains(textOf(res), `"kind": "designed"`) {
		t.Fatalf("design: %s", textOf(res))
	}
	res = call(t, cs, "describe_voice", map[string]any{"description": "an old man with a deep voice, very sparkly"})
	if res.IsError || !strings.Contains(textOf(res), "unmatched") {
		t.Fatalf("describe: %s", textOf(res))
	}
	res = call(t, cs, "transcribe", map[string]any{"path": ref, "timestamps": true})
	if res.IsError || !strings.Contains(textOf(res), `"text": "hello world"`) || !strings.Contains(textOf(res), `"words"`) {
		t.Fatalf("transcribe: %s", textOf(res))
	}
	// Who says what (thread #48).
	res = call(t, cs, "transcribe", map[string]any{"path": ref, "speakers": true})
	if txt := textOf(res); res.IsError || !strings.Contains(txt, `"speaker": "S2"`) || !strings.Contains(txt, `"speaker_name": "Bo"`) ||
		!strings.Contains(txt, `S1: hello world\n\nBo: hi there`) {
		t.Fatalf("transcribe with speakers: %s", txt)
	}
	// A removed speech-to-text model (thread #46): Qwen3-ASR answers.
	res = call(t, cs, "transcribe", map[string]any{"path": ref, "model": "parakeet-tdt-0.6b-v3"})
	if res.IsError || !strings.Contains(textOf(res), `"replaced_by": "qwen3-asr-0.6b-4bit"`) || !strings.Contains(textOf(res), "Parakeet TDT 0.6B v3 was removed") {
		t.Fatalf("transcribe on Parakeet: %s", textOf(res))
	}
	res = call(t, cs, "list_voices", nil)
	if !strings.Contains(textOf(res), `"Me"`) || !strings.Contains(textOf(res), "alloy") {
		t.Fatalf("voices: %s", textOf(res))
	}
	res = call(t, cs, "list_models", map[string]any{"task": "tts"})
	if !strings.Contains(textOf(res), `"size_gb"`) {
		t.Fatalf("models without sizes: %.400s", textOf(res))
	}
	res = call(t, cs, "list_models", map[string]any{"task": "stt"})
	if strings.Contains(textOf(res), "kokoro") || !strings.Contains(textOf(res), "whisper") || !strings.Contains(textOf(res), "qwen3-asr") || strings.Contains(textOf(res), "parakeet") {
		t.Fatalf("models: %s", textOf(res))
	}
	res = call(t, cs, "list_languages", map[string]any{"model": "kokoro-82m-bf16"})
	if !strings.Contains(textOf(res), `"code": "en-US"`) {
		t.Fatalf("languages: %s", textOf(res))
	}
	// Every speech model: OmniVoice's 600+ languages come once, by count
	// (Mac test #136); a query finds Chinese among them.
	res = call(t, cs, "list_languages", nil)
	if txt := textOf(res); !strings.Contains(txt, `"multilingual_models"`) || !strings.Contains(txt, `"omnivoice-bf16"`) || strings.Contains(txt, `"Abkhazian"`) || len(txt) > 20000 {
		t.Fatalf("languages, every model: %d bytes %.600s", len(txt), txt)
	}
	res = call(t, cs, "list_languages", map[string]any{"query": "chinese"})
	if txt := textOf(res); !strings.Contains(txt, `"zh"`) || strings.Contains(txt, `"en-US"`) || !strings.Contains(txt, "omnivoice-bf16") {
		t.Fatalf("languages, query: %.800s", txt)
	}
	res = call(t, cs, "check_health", nil)
	if !strings.Contains(textOf(res), `"status": "ok"`) {
		t.Fatalf("health: %s", textOf(res))
	}

	// Resources.
	rr, err := cs.ReadResource(context.Background(), &sdk.ReadResourceParams{URI: "history://recent"})
	if err != nil || !strings.Contains(rr.Contents[0].Text, "Hello from an agent.") {
		t.Fatalf("history %v %+v", err, rr)
	}
	f.mu.Lock()
	vid := f.voices[0].ID
	f.mu.Unlock()
	rr, err = cs.ReadResource(context.Background(), &sdk.ReadResourceParams{URI: "voice://" + vid})
	if err != nil || !strings.Contains(rr.Contents[0].Text, `"Me"`) {
		t.Fatalf("voice %v %+v", err, rr)
	}
	if _, err := cs.ReadResource(context.Background(), &sdk.ReadResourceParams{URI: "voice://nope"}); err == nil {
		t.Fatal("unknown voice read")
	}
	// The client is remembered for the bindings UI.
	cl := m.Clients()
	if len(cl) != 1 || cl[0].ID != "claude-code" || cl[0].Calls < 10 {
		t.Fatalf("clients %+v", cl)
	}
}

func TestBindingsAndLAN(t *testing.T) {
	f := newFake(t)
	f.voices = []core.Voice{{ID: "nar", Name: "Narrator", Kind: core.VoiceDesigned, Design: "deep"}}
	f.st.API.Bindings = []settings.APIBinding{{Client: "cursor", Voice: "nar"}}
	_, a, _ := serve(t, f)
	// Over the LAN with the PIN: paths are refused, base64 works; the
	// header names the client.
	share, err := a.StartLAN(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.StopLAN)
	lan := httptest.NewServer(a.LANHandler(share.Port))
	t.Cleanup(lan.Close)
	cs := connect(t, lan.URL, "some-agent", http.Header{api.HeaderPIN: {share.PIN}, api.HeaderClient: {"Cursor"}})
	res := call(t, cs, "generate_speech", map[string]any{"text": "Bound voice."})
	if res.IsError {
		t.Fatal(textOf(res))
	}
	f.mu.Lock()
	req := f.generated[0]
	f.mu.Unlock()
	if req.Voice == nil || req.Voice.ID != "nar" {
		t.Fatalf("binding not applied: %+v", req)
	}
	ref := filepath.Join(t.TempDir(), "ref.wav")
	os.WriteFile(ref, []byte("x"), 0o644)
	res = call(t, cs, "transcribe", map[string]any{"path": ref})
	if !res.IsError || !strings.Contains(textOf(res), api.CodePathNotAllowed) {
		t.Fatalf("LAN path: %s", textOf(res))
	}
	res = call(t, cs, "transcribe", map[string]any{"audio_base64": base64.StdEncoding.EncodeToString([]byte("lan audio")), "filename": "a.m4a"})
	if res.IsError {
		t.Fatal(textOf(res))
	}
	// Temporary inputs are removed.
	entries, _ := os.ReadDir(filepath.Join(f.TempDir(), "mcp"))
	if len(entries) != 0 {
		t.Fatalf("left: %v", entries)
	}
	// The tools over the app's modules touch this Mac's files: refused.
	res = call(t, cs, "list_projects", nil)
	if !res.IsError || !strings.Contains(textOf(res), api.CodeUnauthorized) {
		t.Fatalf("LAN studio tool: %s", textOf(res))
	}
	// Saving voices is admin (security review #100, L7): refused, even
	// with the audio as base64, and nothing is saved.
	for tool, args := range map[string]map[string]any{
		"clone_voice":  {"name": "LAN clone", "audio_base64": base64.StdEncoding.EncodeToString([]byte("lan audio")), "filename": "a.wav", "consent": true},
		"design_voice": {"name": "LAN design", "description": "a warm older British man"},
	} {
		res = call(t, cs, tool, args)
		if !res.IsError || !strings.Contains(textOf(res), api.CodeUnauthorized) {
			t.Fatalf("LAN %s: %s", tool, textOf(res))
		}
	}
	f.mu.Lock()
	saved := len(f.voices)
	f.mu.Unlock()
	if saved != 1 {
		t.Fatalf("LAN saved voices: %d", saved)
	}
	// Without the PIN: refused.
	c := sdk.NewClient(&sdk.Implementation{Name: "x"}, nil)
	if _, err := c.Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: lan.URL + "/mcp", MaxRetries: -1}, nil); err == nil {
		t.Fatal("connected without the PIN")
	}
}

// TestStdioShim drives the shim with the SDK's own client over pipes.
func TestStdioShim(t *testing.T) {
	f := newFake(t)
	_, _, ts := serve(t, f)
	dir := t.TempDir()
	pf := filepath.Join(dir, api.PortFileName)
	if err := api.WritePortFile(pf, api.PortFile{URL: ts.URL, PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	cs := shimSession(t, ProxyOptions{PortFile: pf, Client: "codex"})
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != 9+len(StudioTools)+len(KitTools) {
		t.Fatalf("tools %v %v", err, tools)
	}
	res := call(t, cs, "generate_speech", map[string]any{"text": "Through the shim.", "output": "inline"})
	if res.IsError || len(res.Content) != 2 {
		t.Fatalf("speech %v %s", res.IsError, textOf(res))
	}
	res = call(t, cs, "check_health", nil)
	if !strings.Contains(textOf(res), "ok") {
		t.Fatal(textOf(res))
	}
}

func shimSession(t *testing.T, o ProxyOptions) *sdk.ClientSession {
	t.Helper()
	cr, sw := io.Pipe() // client reads what the shim writes
	sr, cw := io.Pipe() // the shim reads what the client writes
	done := make(chan error, 1)
	go func() {
		done <- Proxy(context.Background(), sr, sw, o)
		sw.Close()
	}()
	c := sdk.NewClient(&sdk.Implementation{Name: "stdio-client", Version: "1"}, nil)
	cs, err := c.Connect(context.Background(), &sdk.IOTransport{Reader: cr, Writer: cw}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cs.Close()
		cw.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the shim did not stop")
		}
	})
	return cs
}

// TestStdioShimNotRunning: without the app, requests get an error that says so.
func TestStdioShimNotRunning(t *testing.T) {
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"x","version":"1"}}}` + "\n" +
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n")
	var out strings.Builder
	if err := Proxy(context.Background(), in, &out, ProxyOptions{PortFile: filepath.Join(t.TempDir(), "none.json")}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], "not running") || !strings.Contains(lines[0], `"id":1`) {
		t.Fatalf("out %q", out.String())
	}
}

// TestStdioShimReconnects: after the app restarts (a new MCP server, the
// old session gone), the shim opens a new session by itself.
func TestStdioShimReconnects(t *testing.T) {
	f := newFake(t)
	a := api.New(api.Options{Backend: f, Sched: f.sched, Bus: f.bus})
	defer a.Close(context.Background())
	m := New(Options{API: a, Backend: f})
	a.MountMCP(m.Handler())
	ts := httptest.NewServer(a.Handler())
	t.Cleanup(ts.Close)
	cs := shimSession(t, ProxyOptions{URL: ts.URL})
	if res := call(t, cs, "check_health", nil); res.IsError {
		t.Fatal(textOf(res))
	}
	a.MountMCP(New(Options{API: a, Backend: f}).Handler()) // "restart": sessions are gone
	if res := call(t, cs, "check_health", nil); res.IsError || !strings.Contains(textOf(res), "ok") {
		t.Fatalf("after restart: %s", textOf(res))
	}
}
