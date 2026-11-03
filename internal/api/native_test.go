package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/settings"
)

func TestGenerateAndTakes(t *testing.T) {
	f := newFake(t)
	_, ts := f.server()
	var res struct {
		Take    TakeJSON `json:"take"`
		JobID   string   `json:"job_id"`
		Ignored []string `json:"ignored"`
	}
	resp := do(t, ts, "POST", "/api/generate", map[string]any{"text": "Hello from the native API.", "model": "Kokoro 82M", "voice": "am_adam", "speed": 1.2}, &res)
	if resp.StatusCode != 200 || res.Take.ID == "" || res.Take.ModelID != "kokoro-82m-bf16" || res.Take.Seed != 42 || res.Take.AudioURL != "/api/takes/"+res.Take.ID+"/audio" {
		t.Fatalf("%d %+v", resp.StatusCode, res)
	}
	f.mu.Lock()
	req := f.generated[0]
	f.mu.Unlock()
	if !req.Normalize || req.Speaker != "am_adam" || req.Speed != 1.2 {
		t.Fatalf("request %+v", req)
	}
	// The audio, with ranges.
	r2, _ := http.NewRequest("GET", ts.URL+res.Take.AudioURL, nil)
	r2.Header.Set("Range", "bytes=0-3")
	a, err := http.DefaultClient.Do(r2)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(a.Body)
	a.Body.Close()
	if a.StatusCode != 206 || string(b) != "RIFF" {
		t.Fatalf("audio %d %q", a.StatusCode, b)
	}
	var list struct {
		Data []TakeJSON `json:"data"`
	}
	do(t, ts, "GET", "/api/takes?limit=5", nil, &list)
	if len(list.Data) != 1 {
		t.Fatalf("takes %+v", list)
	}
	var one TakeJSON
	do(t, ts, "GET", "/api/takes/"+res.Take.ID, nil, &one)
	if one.Text != "Hello from the native API." {
		t.Fatalf("take %+v", one)
	}
	if resp := do(t, ts, "DELETE", "/api/takes/"+res.Take.ID, nil, nil); resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	if resp := do(t, ts, "GET", "/api/takes/"+res.Take.ID, nil, nil); resp.StatusCode != 404 {
		t.Fatal(resp.StatusCode)
	}

	// wait: false answers at once; the job's events follow it.
	var acc map[string]any
	resp = do(t, ts, "POST", "/api/generate", map[string]any{"text": "Later.", "wait": false}, &acc)
	if resp.StatusCode != 202 || acc["job_id"] == "" {
		t.Fatalf("%d %v", resp.StatusCode, acc)
	}
	ev, err := http.Get(ts.URL + acc["events_url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	defer ev.Body.Close()
	last := ""
	sc := bufio.NewScanner(ev.Body)
	for sc.Scan() {
		if e, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
			last = e
		}
	}
	if last != "done" {
		t.Fatalf("events ended with %q", last)
	}
	var j JobJSON
	do(t, ts, "GET", "/api/jobs/"+acc["job_id"].(string), nil, &j)
	if j.State != "succeeded" || len(j.Result) == 0 || j.Kind != "take.render" {
		t.Fatalf("job %+v", j)
	}
}

func TestVoicesNative(t *testing.T) {
	f := newFake(t)
	_, ts := f.server()
	ct, body := multipartFile(t, "me.wav", []byte("RIFF....WAVE"), "name", "Me", "ref_text", "This is my voice.", "language", "en")
	req, _ := http.NewRequest("POST", ts.URL+"/api/voices", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set(HeaderCSRF, "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var v VoiceJSON
	json.NewDecoder(resp.Body).Decode(&v)
	resp.Body.Close()
	if resp.StatusCode != 201 || v.Name != "Me" || v.Kind != "cloned" || v.RefText != "This is my voice." || !v.HasAudio {
		t.Fatalf("clone %d %+v", resp.StatusCode, v)
	}
	var d struct {
		Voice     VoiceJSON `json:"voice"`
		Unmatched []string  `json:"unmatched"`
		Model     string    `json:"model"`
	}
	resp = do(t, ts, "POST", "/api/voices/design", map[string]any{"description": "a warm older British man, deep voice", "name": "Grandpa"}, &d)
	if resp.StatusCode != 201 || d.Voice.Kind != "designed" || d.Voice.Seed == 0 || d.Model == "" || d.Voice.Design == "" {
		t.Fatalf("design %d %+v", resp.StatusCode, d)
	}
	var list struct {
		Data []VoiceJSON `json:"data"`
	}
	do(t, ts, "GET", "/api/voices", nil, &list)
	if len(list.Data) != 2 {
		t.Fatalf("voices %+v", list)
	}
	// Speak with it by name: the design model.
	resp = do(t, ts, "POST", "/api/generate", map[string]any{"text": "Hi.", "voice": "grandpa"}, nil)
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	if resp := do(t, ts, "DELETE", "/api/voices/"+v.ID, nil, nil); resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	if resp := do(t, ts, "POST", "/api/voices/design", map[string]any{"description": " "}, nil); resp.StatusCode != 400 {
		t.Fatal(resp.StatusCode)
	}
}

func TestTranscriptsNative(t *testing.T) {
	f := newFake(t)
	_, ts := f.server()
	ct, body := multipartFile(t, "memo.m4a", []byte("audio"), "language", "fr")
	req, _ := http.NewRequest("POST", ts.URL+"/api/transcripts", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set(HeaderCSRF, "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Transcript TranscriptJSON `json:"transcript"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if resp.StatusCode != 201 || out.Transcript.Title != "memo.m4a" || len(out.Transcript.Segments) != 2 || len(out.Transcript.Segments[0].Words) != 4 {
		t.Fatalf("%d %+v", resp.StatusCode, out)
	}
	r, _ := http.Get(ts.URL + "/api/transcripts/" + out.Transcript.ID + "?format=srt")
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if !strings.Contains(string(b), "-->") {
		t.Fatalf("srt %s", b)
	}
	var list struct {
		Data []TranscriptJSON `json:"data"`
	}
	do(t, ts, "GET", "/api/transcripts", nil, &list)
	if len(list.Data) != 1 || list.Data[0].Segments != nil {
		t.Fatalf("list %+v", list)
	}
}

func TestModelsLanguagesTools(t *testing.T) {
	f := newFake(t)
	_, ts := f.server()
	var list struct {
		Data []ModelJSON `json:"data"`
	}
	do(t, ts, "GET", "/api/models?task=stt", nil, &list)
	if len(list.Data) < 2 {
		t.Fatalf("stt models %+v", list)
	}
	for _, m := range list.Data {
		if m.Tasks[0] != "stt" {
			t.Fatalf("task filter: %+v", m)
		}
	}
	var m ModelJSON
	do(t, ts, "GET", "/api/models/omnivoice-bf16", nil, &m)
	if m.Licence.ID != "cc-by-nc-4.0" || m.Licence.Commercial {
		t.Fatalf("omnivoice %+v", m)
	}
	// Thread #45: friendly names beside the technical ones, which stay.
	if m.ID != "omnivoice-bf16" || m.Label != "OmniVoice" || m.DisplayName != "Multilingual all-rounder" || m.UIName != "Multilingual all-rounder" ||
		m.Description == "" || len(m.Tags) == 0 || m.Tags[0] != "non-commercial" {
		t.Errorf("names %+v", m)
	}
	// A friendly name picks the model as well as its id.
	if e, ok := findEntry("multilingual ALL-rounder", "tts"); !ok || e.ID != "omnivoice-bf16" {
		t.Errorf("findEntry by name: %s %v", e.ID, ok)
	}
	var dl map[string]any
	resp := do(t, ts, "POST", "/api/models/kokoro-82m-bf16/download", nil, &dl)
	if resp.StatusCode != 202 || dl["job_id"] == "" {
		t.Fatalf("download %d %v", resp.StatusCode, dl)
	}
	var langs struct {
		Data []LanguageJSON `json:"data"`
	}
	do(t, ts, "GET", "/api/languages?model=kokoro-82m-bf16", nil, &langs)
	if len(langs.Data) < 5 || langs.Data[0].Name == "" {
		t.Fatalf("languages %+v", langs)
	}
	do(t, ts, "GET", "/api/languages", nil, &langs)
	if len(langs.Data) < 600 { // OmniVoice
		t.Fatalf("all languages: %d", len(langs.Data))
	}
	var dir map[string]any
	do(t, ts, "POST", "/api/tools/direction", map[string]any{"note": "more urgent, a little scared"}, &dir)
	if dir["instruct"] == "" || dir["source"] != "keywords" {
		t.Fatalf("direction %v", dir)
	}
	var fit struct {
		Fit map[string]any `json:"fit"`
	}
	do(t, ts, "POST", "/api/tools/rate-fit", map[string]any{"text": "This is a fairly long line that will never fit in one second.", "language": "en", "slot": 1}, &fit)
	if fit.Fit["verdict"] != "overflow" {
		t.Fatalf("fit %v", fit)
	}
	path := filepath.Join(t.TempDir(), "a.wav")
	os.WriteFile(path, []byte("x"), 0o644)
	var probe map[string]any
	do(t, ts, "POST", "/api/tools/probe", map[string]any{"path": path}, &probe)
	if probe["format"] != "wav" {
		t.Fatalf("probe %v", probe)
	}
	if resp := do(t, ts, "POST", "/api/tools/probe", map[string]any{"path": "relative.wav"}, nil); resp.StatusCode != 400 {
		t.Fatal(resp.StatusCode)
	}
}

func TestHealthAndDiscovery(t *testing.T) {
	f := newFake(t)
	_, ts := f.server()
	var h Health
	resp := do(t, ts, "GET", "/health", nil, &h)
	if resp.StatusCode != 200 || h.Status != "ok" || h.Version != "1.2.3" || h.Engine.Device != "Apple M4" {
		t.Fatalf("%d %+v", resp.StatusCode, h)
	}
	// AGPL-3.0 §13: whoever uses the server can find its source.
	if h.SourceURL != "https://github.com/hehehai/AudioSmith" || h.License != "AGPL-3.0-only" {
		t.Fatalf("source %q, licence %q", h.SourceURL, h.License)
	}
	f.update(func(f *fakeBackend) { f.status = Status{State: StateStarting} })
	resp = do(t, ts, "GET", "/health", nil, &h)
	if resp.StatusCode != 503 || h.Status != "starting" || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("%d %+v", resp.StatusCode, h)
	}
	var caps map[string]any
	do(t, ts, "GET", "/.well-known/audiosmith-speech", nil, &caps)
	if caps["schema"] != "audiosmith.speech-capabilities" || caps["source_url"] != SourceURL || caps["license"] != License {
		t.Fatalf("caps %v", caps)
	}
	var spec map[string]any
	do(t, ts, "GET", "/openapi.json", nil, &spec)
	var root map[string]any
	do(t, ts, "GET", "/", nil, &root)
	if root["source_url"] != SourceURL || root["license"] != License {
		t.Fatalf("root %v", root)
	}
	if spec["openapi"] != "3.1.0" || spec["info"].(map[string]any)["version"] != "1.2.3" ||
		spec["info"].(map[string]any)["license"].(map[string]any)["identifier"] != License {
		t.Fatalf("spec %v", spec["info"])
	}
	r, _ := http.Get(ts.URL + "/docs")
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if !strings.Contains(string(b), "rapi-doc") || r.Header.Get("Content-Security-Policy") == "" {
		t.Fatal("docs page")
	}
	r, _ = http.Get(ts.URL + "/docs/rapidoc-min.js")
	b, _ = io.ReadAll(r.Body)
	r.Body.Close()
	if len(b) < 100000 {
		t.Fatal("viewer script")
	}
}

func TestQueueTimeoutAndCancel(t *testing.T) {
	f := newFake(t)
	gate := make(chan struct{})
	f.update(func(f *fakeBackend) {
		f.renderGate = gate
		f.st.API.QueueSeconds = 1
	})
	_, ts := f.server()
	// A first take holds the gpu lane (wait: false).
	var first map[string]any
	do(t, ts, "POST", "/api/generate", map[string]any{"text": "First.", "wait": false}, &first)
	// The second waits a second for it, then gives up and cancels itself.
	t0 := time.Now()
	resp := sdkSpeech(t, ts, `{"model":"tts-1","input":"Second.","voice":"alloy"}`)
	var e apiErr
	json.NewDecoder(resp.Body).Decode(&e)
	if resp.StatusCode != 503 || e.Error.Code != CodeQueueTimeout || resp.Header.Get("Retry-After") == "" || time.Since(t0) > 5*time.Second {
		t.Fatalf("%d %+v", resp.StatusCode, e)
	}
	waitFor(t, func() bool {
		js, _ := f.sched.List(context.Background(), jobs.Filter{States: []jobs.State{jobs.StateCancelled}})
		return len(js) == 1
	})
	// Cancel the first by id.
	var j JobJSON
	do(t, ts, "POST", "/api/jobs/"+first["job_id"].(string)+"/cancel", nil, &j)
	waitFor(t, func() bool {
		got, _ := f.sched.Get(context.Background(), first["job_id"].(string))
		return got.State == jobs.StateCancelled
	})
	var list struct {
		Data []JobJSON `json:"data"`
	}
	do(t, ts, "GET", "/api/jobs?state=cancelled", nil, &list)
	if len(list.Data) != 2 {
		t.Fatalf("jobs %+v", list)
	}
	close(gate)
}

func TestClientGoneCancels(t *testing.T) {
	f := newFake(t)
	gate := make(chan struct{})
	f.update(func(f *fakeBackend) { f.renderGate = gate })
	_, ts := f.server()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "POST", ts.URL+"/v1/audio/speech", strings.NewReader(`{"model":"tts-1","input":"Bye.","voice":"alloy"}`))
	req.Header.Set("Content-Type", "application/json")
	done := make(chan struct{})
	go func() {
		defer close(done)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
		}
	}()
	waitFor(t, func() bool {
		js, _ := f.sched.List(context.Background(), jobs.Filter{States: []jobs.State{jobs.StateRunning}})
		return len(js) == 1
	})
	cancel()
	<-done
	waitFor(t, func() bool {
		js, _ := f.sched.List(context.Background(), jobs.Filter{States: []jobs.State{jobs.StateCancelled}})
		return len(js) == 1
	})
	close(gate)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAuth(t *testing.T) {
	f := newFake(t)
	s, ts := f.server()
	get := func(path string, hdr ...string) *http.Response {
		req, _ := http.NewRequest("GET", ts.URL+path, nil)
		for i := 0; i+1 < len(hdr); i += 2 {
			if hdr[i] == "Host" {
				req.Host = hdr[i+1]
				continue
			}
			req.Header.Set(hdr[i], hdr[i+1])
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	// DNS rebinding: another host name is refused, loopback names pass.
	if r := get("/v1/models", "Host", "evil.example:3901"); r.StatusCode != 403 {
		t.Fatalf("evil host: %d", r.StatusCode)
	}
	if r := get("/v1/models", "Host", "localhost:3901"); r.StatusCode != 200 {
		t.Fatalf("localhost: %d", r.StatusCode)
	}
	// Browsers: a foreign origin is refused; the API's own and listed ones pass, with CORS.
	if r := get("/v1/models", "Origin", "https://evil.example"); r.StatusCode != 403 {
		t.Fatalf("evil origin: %d", r.StatusCode)
	}
	if r := get("/v1/models", "Origin", ts.URL); r.StatusCode != 200 || r.Header.Get("Access-Control-Allow-Origin") != ts.URL {
		t.Fatalf("own origin: %d %v", r.StatusCode, r.Header)
	}
	f.update(func(f *fakeBackend) { f.st.API.Origins = []string{"http://localhost:5173"} })
	if r := get("/v1/models", "Origin", "http://localhost:5173"); r.StatusCode != 200 {
		t.Fatalf("listed origin: %d", r.StatusCode)
	}
	pre, _ := http.NewRequest("OPTIONS", ts.URL+"/v1/audio/speech", nil)
	pre.Header.Set("Origin", "http://localhost:5173")
	pre.Header.Set("Access-Control-Request-Method", "POST")
	pr, _ := http.DefaultClient.Do(pre)
	pr.Body.Close()
	if pr.StatusCode != 204 || !strings.Contains(pr.Header.Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Fatalf("preflight %d %v", pr.StatusCode, pr.Header)
	}
	// CSRF: state-changing /api requests need the header (or the key).
	req, _ := http.NewRequest("POST", ts.URL+"/api/tools/direction", strings.NewReader(`{"note":"calm"}`))
	r, _ := http.DefaultClient.Do(req)
	r.Body.Close()
	if r.StatusCode != 403 {
		t.Fatalf("no csrf: %d", r.StatusCode)
	}
	// A key: optional on loopback (a wrong one is ignored, as SDKs send one)…
	s.SetKey("as-secret")
	if r := get("/v1/models", "Authorization", "Bearer sk-whatever"); r.StatusCode != 200 {
		t.Fatalf("optional key: %d", r.StatusCode)
	}
	// …required when the setting says so.
	f.update(func(f *fakeBackend) { f.st.API.RequireKey = true })
	if r := get("/v1/models"); r.StatusCode != 401 || r.Header.Get("WWW-Authenticate") == "" {
		t.Fatalf("required: %d", r.StatusCode)
	}
	if r := get("/v1/models", "Authorization", "Bearer as-secret"); r.StatusCode != 200 {
		t.Fatalf("with key: %d", r.StatusCode)
	}
	if r := get("/health"); r.StatusCode != 200 {
		t.Fatalf("health is public: %d", r.StatusCode)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/tools/direction", strings.NewReader(`{"note":"calm"}`))
	req.Header.Set("Authorization", "Bearer as-secret")
	r, _ = http.DefaultClient.Do(req)
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("key replaces csrf: %d", r.StatusCode)
	}
}

func TestLANSharing(t *testing.T) {
	f := newFake(t)
	s, _ := f.server()
	share, err := s.StartLAN(0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.StopLAN()
	if len(share.PIN) != 8 || share.Port == 0 {
		t.Fatalf("share %+v", share)
	}
	lan := httptest.NewServer(s.LANHandler(share.Port))
	defer lan.Close()
	get := func(path string, hdr ...string) int {
		req, _ := http.NewRequest("GET", lan.URL+path, nil)
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := get("/v1/models"); c != 401 {
		t.Fatalf("no pin: %d", c)
	}
	if c := get("/v1/models", HeaderPIN, share.PIN); c != 200 {
		t.Fatalf("pin: %d", c)
	}
	if c := get("/v1/models?pin=" + share.PIN); c != 200 {
		t.Fatalf("pin query: %d", c)
	}
	if c := get("/api/jobs", HeaderPIN, share.PIN); c != 403 {
		t.Fatalf("admin with pin: %d", c)
	}
	s.SetKey("as-k")
	if c := get("/api/jobs", "Authorization", "Bearer as-k"); c != 200 {
		t.Fatalf("admin with key: %d", c)
	}
	for i := 0; i < 10; i++ { // different wrong PINs (the same one counts once)
		get("/v1/models", HeaderPIN, "0000000"+strconv.Itoa(i))
	}
	if c := get("/v1/models", HeaderPIN, share.PIN); c != 429 {
		t.Fatalf("after wrong pins: %d", c)
	}
	if _, ok := s.Sharing(); !ok {
		t.Fatal("sharing off")
	}
	s.StopLAN()
	if _, ok := s.Sharing(); ok {
		t.Fatal("sharing on")
	}
}

func TestStartStopAndPortFile(t *testing.T) {
	f := newFake(t)
	s, _ := f.server()
	if err := s.Start(0); err != nil {
		t.Fatal(err)
	}
	port := s.Port()
	r, err := http.Get("http://127.0.0.1:" + itoa(port) + "/health")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	// A second server cannot take the port.
	s2 := New(Options{Backend: f, Sched: f.sched, Bus: f.bus})
	if err := s2.Start(port); err == nil || !strings.Contains(err.Error(), "in use") {
		t.Fatalf("second: %v", err)
	}
	s.Stop(context.Background())
	if s.Port() != 0 {
		t.Fatal("still running")
	}
	path := filepath.Join(t.TempDir(), PortFileName)
	if err := WritePortFile(path, PortFile{Port: 3901, PID: 7, Version: "1"}); err != nil {
		t.Fatal(err)
	}
	p, err := ReadPortFile(path)
	if err != nil || p.URL != "http://127.0.0.1:3901" || p.PID != 7 {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestAllJobEvents(t *testing.T) {
	f := newFake(t)
	_, ts := f.server()
	resp, err := http.Get(ts.URL + "/api/jobs/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	go func() {
		req, _ := http.NewRequest("POST", ts.URL+"/api/generate", strings.NewReader(`{"text":"Events."}`))
		req.Header.Set(HeaderCSRF, "1")
		if r, err := http.DefaultClient.Do(req); err == nil {
			r.Body.Close()
		}
	}()
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if line, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			var j JobJSON
			json.Unmarshal([]byte(line), &j)
			if j.State == "succeeded" && j.Kind == "take.render" {
				return
			}
		}
	}
	t.Fatal("no succeeded event")
}

var _ = settings.DefaultAPIPort
var _ = core.NewID
