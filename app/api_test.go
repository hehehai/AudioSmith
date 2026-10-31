package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/jobs"
	mcpsrv "github.com/hehehai/AudioSmith/internal/mcp"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
)

// apiTestSup is a supervisor whose gpu worker is ready and whose media
// worker probes any file as 2 s of mono audio. Goroutine-safe.
type apiTestSup struct {
	mu    sync.Mutex
	state engine.WorkerState
}

var _ engine.Supervisor = (*apiTestSup)(nil)

func (s *apiTestSup) Start(context.Context) error { return nil }
func (s *apiTestSup) Status() []engine.WorkerStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state
	if st == "" {
		st = engine.WorkerReady
	}
	return []engine.WorkerStatus{{Role: proto.RoleGPU, State: st, Err: "crashed",
		Hello: &proto.HelloResult{Version: "e1", Device: proto.Device{Name: "Apple M4", Memory: 24 << 30}, Families: []string{"kokoro"}}}}
}
func (s *apiTestSup) Restart(proto.Role) error    { return nil }
func (s *apiTestSup) SetMemoryBudget(int64)       {}
func (s *apiTestSup) SetIdleUnload(time.Duration) {}
func (s *apiTestSup) SetCacheDir(string)          {}
func (s *apiTestSup) Hold(...proto.Role) func()   { return func() {} }
func (s *apiTestSup) Stop()                       {}
func (s *apiTestSup) Call(ctx context.Context, role proto.Role, id, method string, params any, notify func(proto.Message)) (json.RawMessage, error) {
	if method == proto.MethodMediaProbe {
		return json.Marshal(proto.ProbeResult{Format: "wav", Duration: 2, HasAudio: true, Codec: "lpcm", SampleRate: 24000, Channels: 1, Bits: 16})
	}
	return nil, &proto.Error{Code: proto.CodeMethodNotFound, Message: method}
}

// apiSTT plays stt.transcribe: "hello from the api".
type apiSTT struct{}

func (apiSTT) Run(ctx context.Context, job jobs.Job, _ func(jobs.Progress), _ func(json.RawMessage)) (json.RawMessage, error) {
	return json.Marshal(proto.TranscribeResult{Text: "Hello from the API.", Language: "en",
		Segments: []proto.Segment{{Start: 0, End: 1.5, Text: "Hello from the API."}}, AudioSeconds: 2})
}

// useAllModelFiles adds the files the catalogue lists (of entries and
// their auxiliary repos) to the snapshots useLocalModels made.
func useAllModelFiles(t *testing.T, cache string) {
	t.Helper()
	put := func(repo, rev string, files []catalog.File) {
		for _, f := range files {
			p := filepath.Join(hub.RepoDir(cache, repo), "snapshots", rev, filepath.FromSlash(f.Path))
			os.MkdirAll(filepath.Dir(p), 0o755)
			if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, e := range catalog.Builtin().Entries {
		put(e.Repo, e.Revision, e.Files)
		for _, x := range e.Aux {
			put(x.Repo, x.Revision, x.Files)
		}
	}
}

// apiEnv is the API over a real library and queue, with a fake engine
// behind the render pipeline and transcription.
type apiEnv struct {
	lib   *library
	synth *lfSynth
	sup   *apiTestSup
	srv   *api.Server
	ts    *httptest.Server
	b     *apiBackend
}

func newAPIEnv(t *testing.T) *apiEnv {
	t.Helper()
	dir := t.TempDir()
	lib := openTestLibraryWith(t, dir, libraryOptions{})
	useAllModelFiles(t, useLocalModels(t, lib))
	synth := &lfSynth{}
	lib.useEngine(func() jobs.Executor { return synth })
	tr := services.NewTranscripts(services.TranscriptsConfig{Store: lib.store.Transcripts(), Bus: lib.bus, Scheduler: lib.sched,
		Licences: lib.licences, ModelPath: lib.modelSnapshot, Engine: func(string) jobs.Executor { return apiSTT{} }})
	if err := lib.sched.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	sup := &apiTestSup{}
	fillApp := &app{}
	fillApp.lib = lib
	b := &apiBackend{lib: lib, tr: tr, sup: sup, version: "test", tmp: filepath.Join(dir, "api-tmp"), data: dir, fill: fillApp.fillVoice}
	srv := api.New(api.Options{Backend: b, Sched: lib.sched, Bus: lib.bus, Version: "test", Logf: t.Logf})
	srv.MountMCP(mcpsrv.New(mcpsrv.Options{API: srv, Backend: b}).Handler())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Close(context.Background())
		lib.sched.Close()
	})
	return &apiEnv{lib: lib, synth: synth, sup: sup, srv: srv, ts: ts, b: b}
}

func (e *apiEnv) post(t *testing.T, path, body string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest("POST", e.ts.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-ignored")
	req.Header.Set(api.HeaderCSRF, "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

// TestAPIEndToEnd runs OpenAI-shaped requests through the app's real
// services, queue and render pipeline.
func TestAPIEndToEnd(t *testing.T) {
	e := newAPIEnv(t)
	resp, body := e.post(t, "/v1/audio/speech", `{"model":"tts-1","input":"Hello from the Local API. A second sentence.","voice":"alloy","response_format":"wav"}`)
	if resp.StatusCode != 200 || !bytes.HasPrefix(body, []byte("RIFF")) || len(body) < 24000 {
		t.Fatalf("speech %d %d bytes: %.200s", resp.StatusCode, len(body), body)
	}
	if resp.Header.Get("X-AudioSmith-Model") != "kokoro-82m-bf16" {
		t.Fatalf("model %v", resp.Header)
	}
	id := resp.Header.Get("X-AudioSmith-Take-Id")
	var take core.Take
	deadline := time.Now().Add(5 * time.Second)
	for {
		var err error
		if take, err = e.lib.svc.Takes.Get(context.Background(), id); err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if take.ID != id || take.Render == nil || take.Render.Master != "" {
		t.Fatalf("the take in the History: %+v", take)
	}

	// pcm, normalized: the whole take, 24 kHz.
	resp, body = e.post(t, "/v1/audio/speech", `{"model":"kokoro-82m-bf16","input":"Short.","voice":"am_adam","response_format":"pcm","normalize":true}`)
	if resp.StatusCode != 200 || len(body) == 0 || len(body)%2 != 0 {
		t.Fatalf("pcm %d %d", resp.StatusCode, len(body))
	}

	// A designed voice by name, on a model that designs.
	attrs, _ := json.Marshal(vd.Parse("a deep male voice").Attrs)
	v, err := e.lib.svc.Voices.Save(context.Background(), core.Voice{Name: "Deep", Kind: core.VoiceDesigned, Design: "a deep male voice", DesignAttrs: attrs, Seed: 7})
	if err != nil {
		t.Fatal(err)
	}
	resp, body = e.post(t, "/api/generate", `{"text":"Designed.","voice":"deep","model":"qwen3-tts-1.7b-voicedesign-4bit"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("designed %d %s", resp.StatusCode, body)
	}
	var gen struct {
		Take api.TakeJSON `json:"take"`
	}
	json.Unmarshal(body, &gen)
	if gen.Take.VoiceID != v.ID || gen.Take.Voice == "" {
		t.Fatalf("designed take %+v", gen.Take)
	}

	// The licence gate.
	resp, body = e.post(t, "/v1/audio/speech", `{"model":"omnivoice-bf16","input":"Hi."}`)
	if resp.StatusCode != 403 || !strings.Contains(string(body), "licence_required") {
		t.Fatalf("licence %d %s", resp.StatusCode, body)
	}

	// Transcription through the transcript service.
	wavPath := filepath.Join(t.TempDir(), "hello.wav")
	writeTestWAV(t, wavPath, 16000, 2)
	data, _ := os.ReadFile(wavPath)
	var mb bytes.Buffer
	w := multipart.NewWriter(&mb)
	w.WriteField("model", "whisper-1")
	fw, _ := w.CreateFormFile("file", "hello.wav")
	fw.Write(data)
	w.Close()
	req, _ := http.NewRequest("POST", e.ts.URL+"/v1/audio/transcriptions", &mb)
	req.Header.Set("Content-Type", w.FormDataContentType())
	tr, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	tb, _ := io.ReadAll(tr.Body)
	tr.Body.Close()
	if tr.StatusCode != 200 || !strings.Contains(string(tb), "Hello from the API.") {
		t.Fatalf("transcription %d %s", tr.StatusCode, tb)
	}
	list, _ := e.b.Transcripts(context.Background(), core.TranscriptQuery{})
	if len(list) != 1 || list[0].Title != "hello.wav (API)" {
		t.Fatalf("transcripts %+v", list)
	}

	// Health and the engine going down.
	var h api.Health
	r, _ := http.Get(e.ts.URL + "/health")
	json.NewDecoder(r.Body).Decode(&h)
	r.Body.Close()
	if h.Status != "ok" || h.Engine.Device != "Apple M4" {
		t.Fatalf("health %+v", h)
	}
	e.sup.mu.Lock()
	e.sup.state = engine.WorkerFailed
	e.sup.mu.Unlock()
	resp, _ = e.post(t, "/v1/audio/speech", `{"model":"tts-1","input":"Hi.","voice":"alloy"}`)
	if resp.StatusCode != 503 || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("down: %d", resp.StatusCode)
	}
}

func TestAPIRecipe(t *testing.T) {
	cv, _ := catalog.Builtin().ByID("qwen3-tts-0.6b-customvoice-8bit")
	design, _ := catalog.Builtin().ByID("qwen3-tts-1.7b-voicedesign-4bit")
	kokoro, _ := catalog.Builtin().ByID("kokoro-82m-bf16")
	nofill := func(*core.Take, *core.Voice, catalog.Entry) {}
	path := func(core.Voice, string) string { return "" }
	// CustomVoice: "Speaker, instruction".
	tk := apiRecipe(api.SpeakRequest{ID: "t1", Text: "Hi", Model: cv, Instructions: "very happy"}, nofill, path)
	if !strings.HasSuffix(tk.Voice, ", very happy") || tk.Render.Master != "" {
		t.Fatalf("customvoice %+v", tk)
	}
	// A design model without a voice: the description.
	tk = apiRecipe(api.SpeakRequest{ID: "t2", Text: "Hi", Model: design, Instructions: "an old man, deep voice", Normalize: true}, nofill, path)
	if tk.Voice == "" || tk.Render.Master != services.MasterDefault {
		t.Fatalf("design %+v", tk)
	}
	// OpenAI's gender: a speaker of it.
	tk = apiRecipe(api.SpeakRequest{ID: "t3", Text: "Hi", Model: kokoro, Gender: "male"}, nofill, path)
	if tk.Voice == "" || tk.Voice[1] != 'm' {
		t.Fatalf("male speaker %q", tk.Voice)
	}
	// Languages: codes map to the model's values; values pass.
	if l := apiModelLanguage(cv, "zh"); l == "" || l == "zh" {
		t.Fatalf("zh → %q", l)
	}
	if l := apiModelLanguage(kokoro, "fr"); l != "" {
		t.Fatalf("kokoro takes no language: %q", l)
	}
}

func TestMCPStdioOptions(t *testing.T) {
	env := map[string]string{"AUDIOSMITH_API_KEY": "k", "AUDIOSMITH_CLIENT": "env-agent"}
	o, err := mcpStdioOptions([]string{"--client", "codex-cli"}, func(k string) string { return env[k] })
	if err != nil || o.Client != "codex-cli" || o.Key != "k" || !strings.HasSuffix(o.PortFile, filepath.Join("AudioSmith", api.PortFileName)) {
		t.Fatalf("%+v %v", o, err)
	}
	if _, err := mcpStdioOptions([]string{"--nope"}, func(string) string { return "" }); err == nil {
		t.Fatal("unknown flag accepted")
	}
}

// memKeys is the API key store of tests.
type memKeys struct {
	mu  sync.Mutex
	key string
}

func (m *memKeys) Get() (string, error) { m.mu.Lock(); defer m.mu.Unlock(); return m.key, nil }
func (m *memKeys) Set(k string) error   { m.mu.Lock(); defer m.mu.Unlock(); m.key = k; return nil }
func (m *memKeys) Delete() error        { m.mu.Lock(); defer m.mu.Unlock(); m.key = ""; return nil }

func useMemKeys(t *testing.T) *memKeys {
	old := apiKeyStore
	m := &memKeys{}
	apiKeyStore = m
	t.Cleanup(func() { apiKeyStore = old })
	return m
}

// freePort is a loopback port nobody listens on.
func freePort(t *testing.T) int {
	t.Helper()
	if snap != nil { // a fixed port in snapshots (snapshot_env_test.go)
		for range 10 {
			port := snapPortBase + snap.ports
			snap.ports++
			if ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
				ln.Close()
				return port
			}
			t.Logf("port %d is in use: the snapshots show another port than other runs", port)
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// startTestAPI starts the app's API on a free port.
func startTestAPI(t *testing.T, a *app) {
	t.Helper()
	port := freePort(t)
	a.updateSettings(func(s *settings.Settings) { s.API.Port = port })
	a.startAPI(t.TempDir())
	t.Cleanup(a.stopAPI)
	waitUntil(t, func() bool { return a.apiPort == port })
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAPISettingsPage(t *testing.T) {
	keys := useMemKeys(t)
	a, _, tt := newTestApp(t)
	tallSettings(tt) // the Local API is under the list of integrations
	startTestAPI(t, a)
	pf, err := api.ReadPortFile(a.apiPortFile())
	if err != nil || pf.Port != a.apiPort {
		t.Fatalf("port file %+v %v", pf, err)
	}
	r, err := http.Get(pf.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	a.showPage("assets") // not the Studio, whose Generate would show under the settings dialog
	a.navigate("/settings/api")
	tt.Frame()
	if !tt.HasText("Run the Local API") || !tt.HasText(a.apiBaseURL()) {
		t.Fatal("the page does not show the server")
	}
	// A key: generated, shown once, required, revoked.
	if err := tt.Click("Generate"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.apiHasKey || a.apiNewKey == "" || a.apiSrv.Key() != keys.key || !tt.HasText(a.apiNewKey) {
		t.Fatalf("key %v %q", a.apiHasKey, a.apiNewKey)
	}
	a.updateSettings(func(s *settings.Settings) { s.API.RequireKey = true })
	tt.Frame()
	if !a.prefs.API.RequireKey {
		t.Fatal("require not on")
	}
	r, _ = http.Get(pf.URL + "/v1/models")
	r.Body.Close()
	if r.StatusCode != 401 {
		t.Fatalf("without the key: %d", r.StatusCode)
	}
	if err := tt.Click("Revoke"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.apiHasKey || keys.key != "" || a.prefs.API.RequireKey {
		t.Fatal("revoke")
	}
	// LAN sharing.
	a.updateSettings(func(s *settings.Settings) { s.API.SharePort = freePort(t) })
	a.startSharing()
	waitUntil(t, func() bool { return a.apiShare != nil || a.apiShareE != "" })
	if a.apiShare == nil || len(a.apiShare.PIN) != 6 {
		t.Fatalf("sharing: %q", a.apiShareE)
	}
	tt.Frame()
	if !tt.HasText(a.apiShare.PIN[:3] + " " + a.apiShare.PIN[3:]) {
		t.Fatal("PIN not shown")
	}
	a.stopSharing()
	if _, on := a.apiSrv.Sharing(); on || a.apiShare != nil {
		t.Fatal("still sharing")
	}
	// Bindings show.
	a.updateSettings(func(s *settings.Settings) {
		s.API.Bindings = []settings.APIBinding{{Client: "claude-code"}}
	})
	tt.Frame()
	if !tt.HasText("claude-code") {
		t.Fatal("binding not shown")
	}
	// Turn it off: the port file goes.
	a.updateSettings(func(s *settings.Settings) { s.API.Enabled = false })
	a.applyAPI()
	waitUntil(t, func() bool { _, err := os.Stat(a.apiPortFile()); return os.IsNotExist(err) })
	if a.apiPort != 0 {
		t.Fatal("still running")
	}
}

func TestAPIPortInUse(t *testing.T) {
	useMemKeys(t)
	a, _, _ := newTestApp(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	a.updateSettings(func(s *settings.Settings) { s.API.Port = port })
	a.startAPI(t.TempDir())
	t.Cleanup(a.stopAPI)
	waitUntil(t, func() bool { return a.apiErr != "" })
	if a.apiPort != 0 || !strings.Contains(a.apiErr, itoa(port)) {
		t.Fatalf("%d %q", a.apiPort, a.apiErr)
	}
}
