package api

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/translate"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/")

// golden compares got (JSON) with testdata/name, or rewrites it with -update.
func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	var v any
	if err := json.Unmarshal(got, &v); err != nil {
		t.Fatalf("%s: not JSON: %v\n%s", name, err, got)
	}
	norm, _ := json.MarshalIndent(v, "", "  ")
	path := filepath.Join("testdata", name)
	if *update {
		os.MkdirAll("testdata", 0o755)
		os.WriteFile(path, append(norm, '\n'), 0o644)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v (run with -update)", name, err)
	}
	wantStr := strings.ReplaceAll(string(want), "\r\n", "\n")
	if strings.TrimSpace(wantStr) != strings.TrimSpace(string(norm)) {
		t.Fatalf("%s differs:\n got %s\nwant %s", name, norm, wantStr)
	}
}

// openAIHeaders are the headers the official SDKs send (openai-python
// 1.x): a Bearer key the server ignores on loopback, and Stainless's.
func openAIHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer sk-not-needed")
	req.Header.Set("User-Agent", "OpenAI/Python 1.99.0")
	req.Header.Set("X-Stainless-Lang", "python")
	req.Header.Set("X-Stainless-Package-Version", "1.99.0")
	req.Header.Set("Accept", "application/octet-stream")
}

// sdkSpeech posts testdata/<file> as openai-python's audio.speech.create does.
func sdkSpeech(t *testing.T, ts *httptest.Server, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+"/v1/audio/speech", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	openAIHeaders(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func readGolden(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// parseWAV reads a WAV (streamed or not): its rate and its 16-bit data.
func parseWAV(t *testing.T, b []byte) (rate int, data []byte) {
	t.Helper()
	if len(b) < 44 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		t.Fatalf("not a WAV: % x", b[:min(len(b), 16)])
	}
	p := 12
	for p+8 <= len(b) {
		id, n := string(b[p:p+4]), int(binary.LittleEndian.Uint32(b[p+4:]))
		switch id {
		case "fmt ":
			rate = int(binary.LittleEndian.Uint32(b[p+12:]))
		case "data":
			if n == -1 || uint32(n) == 0xFFFFFFFF || p+8+n > len(b) {
				return rate, b[p+8:]
			}
			return rate, b[p+8 : p+8+n]
		}
		p += 8 + n + n%2
	}
	t.Fatal("no data chunk")
	return
}

func TestSpeechOpenAISDK(t *testing.T) {
	f := newFake(t)
	_, ts := f.server()
	// client.audio.speech.create(model="tts-1", voice="alloy", input=…): no
	// response_format, so WAV (OpenAI's mp3 needs an encoder).
	resp := sdkSpeech(t, ts, readGolden(t, "speech_request.json"))
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "audio/wav" {
		t.Fatalf("%d %s %s", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
	rate, data := parseWAV(t, body)
	def := defaultTTS(f.Settings())
	if rate != 24000 || len(data) != 2*24000*len("Today is a wonderful day to build something people love!")/20 {
		t.Fatalf("rate %d, %d bytes", rate, len(data))
	}
	if resp.Header.Get("X-AudioSmith-Model") != def.ID || resp.Header.Get("X-AudioSmith-Take-Id") == "" || resp.Header.Get("X-AudioSmith-Voice") != "alloy" {
		t.Fatalf("headers %v", resp.Header)
	}
	f.mu.Lock()
	req := f.generated[0]
	f.mu.Unlock()
	if req.Model.ID != def.ID || req.Gender != "" || req.Normalize || req.Background {
		t.Fatalf("request %+v", req)
	}
}

// waitEmpty waits for a handler to remove its temporary files from dir:
// it does so as it returns, so the client may have the whole response a
// moment before.
func waitEmpty(t *testing.T, dir string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		entries, _ := os.ReadDir(dir)
		if len(entries) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("files left in %s: %v", filepath.Base(dir), entries)
		}
	}
}

func TestSpeechFormats(t *testing.T) {
	f := newFake(t)
	f.renderRate = 48000
	_, ts := f.server()
	text := "Twenty characters!!"
	// pcm: 24 kHz mono s16le, streamed (resampled from 48 kHz).
	resp := sdkSpeech(t, ts, `{"model":"gpt-4o-mini-tts","input":"`+text+`","voice":"coral","response_format":"pcm","instructions":"Speak cheerfully."}`)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "audio/pcm" {
		t.Fatalf("pcm %d %s", resp.StatusCode, body)
	}
	want := 2 * 24000 * len(text) / 20
	if d := len(body) - want; d < -64 || d > 64 {
		t.Fatalf("pcm: %d bytes, want ≈%d", len(body), want)
	}
	f.mu.Lock()
	req := f.generated[0]
	f.mu.Unlock()
	if req.Gender != "female" || req.Instructions != "Speak cheerfully." {
		t.Fatalf("request %+v", req)
	}
	// normalize: the whole take, with its length.
	resp = sdkSpeech(t, ts, `{"model":"tts-1","input":"`+text+`","voice":"alloy","response_format":"wav","normalize":true}`)
	body, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Length") == "" || resp.Header.Get("X-AudioSmith-Seed") != "42" {
		t.Fatalf("normalized %d %v", resp.StatusCode, resp.Header)
	}
	if rate, data := parseWAV(t, body); rate != 48000 || len(data) != 2*48000*len(text)/20 {
		t.Fatalf("normalized: %d Hz, %d bytes", rate, len(data))
	}
	// aac: through media.encode.
	resp = sdkSpeech(t, ts, `{"model":"tts-1","input":"`+text+`","voice":"alloy","response_format":"aac"}`)
	body, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "audio/mp4" || !bytes.HasPrefix(body, []byte("M4A:RIFF")) {
		t.Fatalf("aac %d %s %q", resp.StatusCode, resp.Header.Get("Content-Type"), body[:min(len(body), 8)])
	}
	waitEmpty(t, filepath.Join(f.TempDir(), "encoded"))
	f.update(func(f *fakeBackend) { f.canEncode = false })
	resp = sdkSpeech(t, ts, `{"model":"tts-1","input":"x","voice":"alloy","response_format":"aac"}`)
	var e apiErr
	json.NewDecoder(resp.Body).Decode(&e)
	if resp.StatusCode != 400 || e.Error.Code != CodeUnsupportedFormat {
		t.Fatalf("aac without encoder: %d %+v", resp.StatusCode, e)
	}
	// M7a: mp3, opus and flac when the backend encodes them (ffmpeg).
	for _, format := range []string{"mp3", "opus", "flac"} {
		resp = sdkSpeech(t, ts, `{"model":"tts-1","input":"x","voice":"alloy","response_format":"`+format+`"}`)
		e = apiErr{}
		json.NewDecoder(resp.Body).Decode(&e)
		if resp.StatusCode != 400 || e.Error.Code != CodeUnsupportedFormat || !strings.Contains(e.Error.Message, "ffmpeg") {
			t.Fatalf("%s without ffmpeg: %d %+v", format, resp.StatusCode, e)
		}
	}
	f.update(func(f *fakeBackend) { f.formats = map[string]bool{"mp3": true, "opus": true, "flac": true} })
	for format, ct := range map[string]string{"mp3": "audio/mpeg", "opus": "audio/ogg; codecs=opus", "flac": "audio/flac"} {
		resp = sdkSpeech(t, ts, `{"model":"tts-1","input":"`+text+`","voice":"alloy","response_format":"`+format+`"}`)
		body, _ = io.ReadAll(resp.Body)
		if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != ct || !bytes.HasPrefix(body, []byte(strings.ToUpper(format)+":RIFF")) {
			t.Fatalf("%s %d %s %q", format, resp.StatusCode, resp.Header.Get("Content-Type"), body[:min(len(body), 8)])
		}
	}
}

func TestSpeechSSE(t *testing.T) {
	f := newFake(t)
	_, ts := f.server()
	resp := sdkSpeech(t, ts, `{"model":"gpt-4o-mini-tts","input":"Hello there, friend.","voice":"nova","response_format":"pcm","stream_format":"sse"}`)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("%d %v", resp.StatusCode, resp.Header)
	}
	var audio []byte
	var types []string
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(nil, 1<<22)
	for sc.Scan() {
		line, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		var ev struct {
			Type  string         `json:"type"`
			Audio string         `json:"audio"`
			Usage map[string]int `json:"usage"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatal(err)
		}
		types = append(types, ev.Type)
		if ev.Type == "speech.audio.delta" {
			b, _ := base64.StdEncoding.DecodeString(ev.Audio)
			audio = append(audio, b...)
		}
		if ev.Type == "speech.audio.done" && ev.Usage["input_tokens"] != len("Hello there, friend.") {
			t.Fatalf("usage %v", ev.Usage)
		}
	}
	if len(types) != 3 || types[0] != "speech.audio.delta" || types[2] != "speech.audio.done" {
		t.Fatalf("events %v", types)
	}
	if want := 2 * 24000 * len("Hello there, friend.") / 20; len(audio) != want {
		t.Fatalf("audio %d bytes, want %d", len(audio), want)
	}
}

func TestSpeechErrors(t *testing.T) {
	cloneOnlyModel(t)
	f := newFake(t)
	f.voices = []core.Voice{{ID: "v1", Name: "Ana", Kind: core.VoiceCloned}, {ID: "v2", Name: "Twin", Kind: core.VoiceCloned}, {ID: "v3", Name: "twin", Kind: core.VoiceCloned}}
	_, ts := f.server()
	omni, _ := catalog.Builtin().ByID("omnivoice-bf16")
	turbo, _ := catalog.Builtin().ByID("chatterbox-turbo-8bit")
	f.licence[omni.Repo] = true
	f.notReady[turbo.Repo] = true
	for _, c := range []struct {
		name, body string
		status     int
		code       string
	}{
		{"empty input", `{"model":"tts-1","input":"  ","voice":"alloy"}`, 400, CodeInvalid},
		{"long input", `{"model":"tts-1","input":"` + strings.Repeat("a", 4097) + `","voice":"alloy"}`, 400, CodeInvalid},
		{"mp3", `{"model":"tts-1","input":"hi","voice":"alloy","response_format":"mp3"}`, 400, CodeUnsupportedFormat},
		{"opus", `{"model":"tts-1","input":"hi","voice":"alloy","response_format":"opus"}`, 400, CodeUnsupportedFormat},
		{"speed", `{"model":"tts-1","input":"hi","voice":"alloy","speed":5}`, 400, CodeInvalid},
		{"stream format", `{"model":"tts-1","input":"hi","voice":"alloy","stream_format":"chunks"}`, 400, CodeInvalid},
		{"unknown model", `{"model":"tts-9000","input":"hi","voice":"alloy"}`, 404, CodeModelNotFound},
		{"unknown voice", `{"model":"tts-1","input":"hi","voice":"Bob"}`, 400, CodeVoiceNotFound},
		{"ambiguous voice", `{"model":"tts-1","input":"hi","voice":"TWIN"}`, 409, CodeAmbiguousVoice},
		{"clone on a model that can't", `{"model":"kokoro-82m-bf16","input":"hi","voice":"Ana"}`, 400, CodeUnsupportedModel},
		{"licence", `{"model":"omnivoice-bf16","input":"hi"}`, 403, CodeLicenceRequired},
		{"not downloaded", `{"model":"chatterbox-turbo-8bit","input":"hi"}`, 409, CodeNotDownloaded},
		{"bad json", `{"model":`, 400, CodeInvalid},
		{"clone-only, no reference", `{"model":"clone-only-nano","input":"hi"}`, 400, CodeNeedsReference},
		{"clone-only, OpenAI voice", `{"model":"clone-only-nano","input":"hi","voice":"alloy"}`, 400, CodeNeedsReference},
		{"removed model, its own speaker", `{"model":"kitten-tts-mini-0.8","input":"hi","voice":"Bella"}`, 400, CodeModelRemoved},
	} {
		resp := sdkSpeech(t, ts, c.body)
		var e apiErr
		json.NewDecoder(resp.Body).Decode(&e)
		if resp.StatusCode != c.status || e.Error.Code != c.code || e.Error.Message == "" || e.Error.Type == "" {
			t.Errorf("%s: %d %+v", c.name, resp.StatusCode, e)
		}
	}
	// The engine is down: 503 with Retry-After.
	f.update(func(f *fakeBackend) { f.status = Status{State: StateFailed, Detail: "it crashed", RetryAfter: 7} })
	resp := sdkSpeech(t, ts, `{"model":"tts-1","input":"hi","voice":"alloy"}`)
	var e apiErr
	json.NewDecoder(resp.Body).Decode(&e)
	if resp.StatusCode != 503 || resp.Header.Get("Retry-After") != "7" || e.Error.Code != CodeEngineUnavailable || e.Error.Type != "server_error" {
		t.Fatalf("down: %d %v %+v", resp.StatusCode, resp.Header, e)
	}
	// A failing render: the job's error.
	f.update(func(f *fakeBackend) {
		f.status = Status{State: StateReady}
		f.failRender = &jobs.Error{Code: "oom", Message: "out of memory", Retryable: true}
	})
	resp = sdkSpeech(t, ts, `{"model":"tts-1","input":"hi","voice":"alloy","normalize":true}`)
	json.NewDecoder(resp.Body).Decode(&e)
	if resp.StatusCode != 503 || e.Error.Code != "oom" {
		t.Fatalf("oom: %d %+v", resp.StatusCode, e)
	}
}

func TestSpeechVoices(t *testing.T) {
	f := newFake(t)
	f.voices = []core.Voice{{ID: "c1", Name: "Narrator", Kind: core.VoiceCloned, Model: "mlx-community/chatterbox-turbo-8bit"}}
	_, ts := f.server()
	// A saved clone by name: the default (Kokoro) can't clone, so its own model.
	resp := sdkSpeech(t, ts, `{"model":"tts-1","input":"hi","voice":"narrator","response_format":"wav","normalize":true}`)
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != 200 || resp.Header.Get("X-AudioSmith-Model") != "chatterbox-turbo-8bit" || resp.Header.Get("X-AudioSmith-Voice") != "Narrator" {
		t.Fatalf("%d %v", resp.StatusCode, resp.Header)
	}
	// A built-in speaker; OpenAI's {"id": …} form; ignored parameters.
	resp = sdkSpeech(t, ts, `{"model":"kokoro-82m-bf16","input":"hi","voice":{"id":"af_heart"},"seed":3,"instructions":"whisper"}`)
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != 200 || resp.Header.Get("X-AudioSmith-Ignored") != "instructions, seed" {
		t.Fatalf("%d %v", resp.StatusCode, resp.Header)
	}
	f.mu.Lock()
	reqs := f.generated
	f.mu.Unlock()
	if reqs[0].Voice == nil || reqs[0].Voice.ID != "c1" || reqs[1].Speaker != "af_heart" || reqs[1].Seed != 3 {
		t.Fatalf("requests %+v", reqs)
	}
	// /v1/audio/voices lists OpenAI's names, saved voices and speakers.
	var list struct {
		Data []map[string]any `json:"data"`
	}
	do(t, ts, "GET", "/v1/audio/voices", nil, &list)
	types := map[string]int{}
	for _, v := range list.Data {
		types[v["type"].(string)]++
	}
	if types["openai"] != 13 || types["saved"] != 1 || types["builtin"] < 20 {
		t.Fatalf("voices %v", types)
	}
}

// multipartFile builds openai-python's multipart body for a file upload.
func multipartFile(t *testing.T, name string, content []byte, fields ...string) (string, *bytes.Buffer) {
	t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	for i := 0; i+1 < len(fields); i += 2 {
		w.WriteField(fields[i], fields[i+1])
	}
	fw, _ := w.CreateFormFile("file", name)
	fw.Write(content)
	w.Close()
	return w.FormDataContentType(), &b
}

func sdkTranscribe(t *testing.T, ts *httptest.Server, path string, fields ...string) *http.Response {
	t.Helper()
	ct, body := multipartFile(t, "interview.m4a", []byte("fake audio"), fields...)
	req, _ := http.NewRequest("POST", ts.URL+path, body)
	req.Header.Set("Content-Type", ct)
	openAIHeaders(req)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestTranscriptionsOpenAISDK(t *testing.T) {
	f := newFake(t)
	_, ts := f.server()
	// client.audio.transcriptions.create(model="whisper-1", file=…)
	resp := sdkTranscribe(t, ts, "/v1/audio/transcriptions", "model", "whisper-1")
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	golden(t, "transcription_json.json", body)
	// verbose_json with word and segment timestamps.
	resp = sdkTranscribe(t, ts, "/v1/audio/transcriptions", "model", "whisper-1", "response_format", "verbose_json",
		"timestamp_granularities[]", "word", "timestamp_granularities[]", "segment", "language", "fr")
	body, _ = io.ReadAll(resp.Body)
	golden(t, "transcription_verbose.json", body)
	for _, format := range []string{"text", "srt", "vtt"} {
		resp = sdkTranscribe(t, ts, "/v1/audio/transcriptions", "model", "gpt-4o-transcribe", "response_format", format)
		body, _ = io.ReadAll(resp.Body)
		if resp.StatusCode != 200 || !strings.Contains(string(body), "Bonjour") {
			t.Fatalf("%s: %d %s", format, resp.StatusCode, body)
		}
		if format == "vtt" && !strings.HasPrefix(string(body), "WEBVTT") || format == "srt" && !strings.HasPrefix(string(body), "1\n") {
			t.Fatalf("%s: %s", format, body)
		}
	}
	f.mu.Lock()
	reqs := f.transcribed
	f.mu.Unlock()
	accurate, fast := defaultSTT(f.st, false, ""), defaultSTT(f.st, true, "")
	if reqs[0].Model != accurate.Repo || reqs[0].Words || reqs[1].Language != "fr" || !reqs[1].Words {
		t.Fatalf("requests %+v", reqs[:2])
	}
	resp = sdkTranscribe(t, ts, "/v1/audio/transcriptions", "model", "gpt-4o-mini-transcribe")
	io.Copy(io.Discard, resp.Body)
	f.mu.Lock()
	last := f.transcribed[len(f.transcribed)-1]
	f.mu.Unlock()
	if last.Model != fast.Repo || last.Mode != "fast" {
		t.Fatalf("mini: %+v", last)
	}
	// Uploads are removed.
	waitEmpty(t, filepath.Join(f.TempDir(), "uploads"))
	// Errors.
	resp = sdkTranscribe(t, ts, "/v1/audio/transcriptions", "model", "kokoro-82m-bf16")
	var e apiErr
	json.NewDecoder(resp.Body).Decode(&e)
	if resp.StatusCode != 404 || e.Error.Code != CodeModelNotFound {
		t.Fatalf("tts model: %d %+v", resp.StatusCode, e)
	}
	resp = sdkTranscribe(t, ts, "/v1/audio/transcriptions", "model", "whisper-1", "response_format", "xml")
	json.NewDecoder(resp.Body).Decode(&e)
	if resp.StatusCode != 400 || e.Error.Code != CodeUnsupportedFormat {
		t.Fatalf("xml: %d %+v", resp.StatusCode, e)
	}
	req, _ := http.NewRequest("POST", ts.URL+"/v1/audio/transcriptions", strings.NewReader(`{"model":"whisper-1"}`))
	req.Header.Set("Content-Type", "application/json")
	r2, _ := http.DefaultClient.Do(req)
	if r2.StatusCode != 400 {
		t.Fatalf("no file: %d", r2.StatusCode)
	}
	r2.Body.Close()
}

func TestTranslations(t *testing.T) {
	f := newFake(t)
	_, ts := f.server()
	resp := sdkTranscribe(t, ts, "/v1/audio/translations", "model", "whisper-1")
	var e apiErr
	json.NewDecoder(resp.Body).Decode(&e)
	if resp.StatusCode != 501 || e.Error.Code != CodeTranslationNotReady {
		t.Fatalf("no LLM: %d %+v", resp.StatusCode, e)
	}
	f.update(func(f *fakeBackend) {
		f.translator = func(req translate.Request) (translate.Result, error) {
			if req.Target != "en" || req.Source != "fr" {
				t.Errorf("translate %s → %s", req.Source, req.Target)
			}
			en := map[string]string{"Bonjour tout le monde.": "Hello everyone.", "Ça va bien.": "All is well."}
			var res translate.Result
			for _, s := range req.Segments {
				res.Segments = append(res.Segments, translate.Translated{ID: s.ID, Text: en[s.Text]})
			}
			return res, nil
		}
	})
	resp = sdkTranscribe(t, ts, "/v1/audio/translations", "model", "whisper-1", "response_format", "verbose_json")
	body, _ := io.ReadAll(resp.Body)
	golden(t, "translation_verbose.json", body)
}

func TestModelsOpenAI(t *testing.T) {
	f := newFake(t)
	_, ts := f.server()
	var list struct {
		Object string        `json:"object"`
		Data   []openAIModel `json:"data"`
	}
	do(t, ts, "GET", "/v1/models", nil, &list)
	ids := map[string]openAIModel{}
	for _, m := range list.Data {
		ids[m.ID] = m
	}
	def := defaultTTS(f.st)
	if list.Object != "list" || ids["tts-1"].AudioSmith["alias_for"] != def.ID || ids["whisper-1"].AudioSmith["kind"] != "stt" ||
		ids["kokoro-82m-bf16"].Object != "model" || ids["kokoro-82m-bf16"].OwnedBy != "mlx-community" {
		t.Fatalf("models %+v", list)
	}
	if _, ok := ids["silero-vad"]; ok {
		t.Fatal("non-speech model listed")
	}
	var one openAIModel
	do(t, ts, "GET", "/v1/models/gpt-4o-mini-tts", nil, &one)
	if one.ID != "gpt-4o-mini-tts" {
		t.Fatalf("one %+v", one)
	}
	resp := do(t, ts, "GET", "/v1/models/nope", nil, nil)
	if resp.StatusCode != 404 {
		t.Fatal(resp.StatusCode)
	}
}

// cloneOnlyModel adds a speech model that only clones voices to the
// catalogue for the test: no shipped model does since thread #43.
func cloneOnlyModel(t *testing.T) catalog.Entry {
	t.Helper()
	e := catalog.Entry{ID: "clone-only-nano", Label: "CloneOnly Nano", Family: "moss_tts_nano", Repo: "test/CloneOnly-Nano-100M",
		Tasks: []catalog.Task{catalog.TaskTTS}, About: "A test model that only clones voices.", NeedsReference: true,
		Capabilities: []catalog.Capability{catalog.CapSampling, catalog.CapClone, catalog.CapSeed},
		Licence:      catalog.Licence{ID: "apache-2.0", Name: "Apache 2.0", Commercial: true, Verified: true}}
	c := *catalog.Builtin()
	c.Entries = append(slices.Clone(c.Entries), e)
	t.Cleanup(catalog.Swap(&c))
	return e
}

// A model that only clones speaks with a saved cloned voice; the services'
// refusal (core.NeedsReferenceError) is a 400 needs_reference too.
func TestSpeechNeedsReference(t *testing.T) {
	cloneOnlyModel(t)
	f := newFake(t)
	f.voices = []core.Voice{{ID: "v1", Name: "Ana", Kind: core.VoiceCloned}}
	_, ts := f.server()
	resp := sdkSpeech(t, ts, `{"model":"clone-only-nano","input":"hi","voice":"Ana","response_format":"wav"}`)
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != 200 || resp.Header.Get("X-AudioSmith-Model") != "clone-only-nano" {
		t.Fatalf("%d %v", resp.StatusCode, resp.Header)
	}
	resp = sdkSpeech(t, ts, `{"model":"clone-only-nano","input":"hi"}`)
	var e apiErr
	json.NewDecoder(resp.Body).Decode(&e)
	if resp.StatusCode != 400 || e.Error.Code != CodeNeedsReference || !strings.Contains(e.Error.Message, "CloneOnly Nano only clones voices") {
		t.Fatalf("%d %+v", resp.StatusCode, e)
	}
	ae := errorOf(fmt.Errorf("generate: %w", &core.NeedsReferenceError{Model: "CloneOnly Nano"}))
	if ae.Status != 400 || ae.Code != CodeNeedsReference {
		t.Fatalf("errorOf: %+v", ae)
	}
	if je := jobErrorOf(&jobs.Error{Code: core.CodeNeedsReference, Message: "x"}); je.Status != 400 {
		t.Fatalf("jobErrorOf: %+v", je)
	}
}

// TestRemovedModels: requests that name a model thread #43 removed are
// answered by its replacement, with X-AudioSmith-Replaced-By and a
// warning (headers, JSON); a cloning request goes to the clone
// replacement; what the replacement can't do (the old model's own
// speaker, downloading the old model) is a clear error. The model list
// never shows them; GET of one says what replaces it.
func TestRemovedModels(t *testing.T) {
	f := newFake(t)
	f.voices = []core.Voice{{ID: "v1", Name: "Ana", Kind: core.VoiceCloned}}
	_, ts := f.server()
	for _, c := range []struct{ body, model string }{
		{`{"model":"kitten-tts-mini-0.8","input":"hi"}`, "kokoro-82m-bf16"},
		{`{"model":"mlx-community/Soprano-80M-bf16","input":"hi","voice":"af_heart"}`, "kokoro-82m-bf16"},
		{`{"model":"Pocket TTS","input":"hi","voice":"Ana"}`, "chatterbox-turbo-8bit"}, // a clone
		{`{"model":"marvis-tts-250m-v0.2-8bit","input":"hi","voice":"alloy"}`, "kokoro-82m-bf16"},
		{`{"model":"MOSS-TTS-Nano-100M","input":"hi","voice":"Ana"}`, "qwen3-tts-0.6b-base-8bit"},
		{`{"model":"chatterbox-8bit","input":"hi","voice":"Ana"}`, "chatterbox-turbo-8bit"},
	} {
		resp := sdkSpeech(t, ts, c.body)
		io.Copy(io.Discard, resp.Body)
		h := resp.Header
		if resp.StatusCode != 200 || h.Get("X-AudioSmith-Model") != c.model || h.Get("X-AudioSmith-Replaced-By") != c.model ||
			!strings.Contains(h.Get("X-AudioSmith-Warning"), "was removed from AudioSmith") {
			t.Errorf("%s: %d %v", c.body, resp.StatusCode, h)
		}
	}
	if g := f.generated[len(f.generated)-1]; g.Model.ID != "chatterbox-turbo-8bit" || g.Voice == nil || g.Voice.ID != "v1" {
		t.Errorf("generated %+v", g)
	}
	// The old model's speaker: Kokoro has no Bella.
	resp := sdkSpeech(t, ts, `{"model":"kitten-tts-mini-0.8","input":"hi","voice":"Bella"}`)
	var e apiErr
	json.NewDecoder(resp.Body).Decode(&e)
	if resp.StatusCode != 400 || e.Error.Code != CodeModelRemoved || !strings.Contains(e.Error.Message, "KittenTTS mini was removed") ||
		!strings.Contains(e.Error.Message, "Kokoro 82M has no voice called Bella") {
		t.Errorf("Bella: %d %+v", resp.StatusCode, e)
	}
	// The native API: replaced_by and warning in the answer.
	var gen map[string]any
	if resp := do(t, ts, "POST", "/api/generate", `{"text":"hi","model":"soprano-80m-bf16"}`, &gen); resp.StatusCode != 200 ||
		gen["replaced_by"] != "kokoro-82m-bf16" || !strings.Contains(fmt.Sprint(gen["warning"]), "Soprano 80M was removed") {
		t.Errorf("generate: %d %v", resp.StatusCode, gen)
	}
	// Lists and lookups.
	var list struct{ Data []map[string]any }
	do(t, ts, "GET", "/v1/models", nil, &list)
	for _, m := range list.Data {
		if _, ok := catalog.Builtin().RemovedModel(fmt.Sprint(m["id"])); ok {
			t.Errorf("listed %v", m["id"])
		}
	}
	for _, path := range []string{"/v1/models/pocket-tts", "/api/models/pocket-tts"} {
		var rm RemovedModelJSON
		if resp := do(t, ts, "GET", path, nil, &rm); resp.StatusCode != 200 || !rm.Removed || rm.ReplacedBy != "kokoro-82m-bf16" ||
			rm.CloneReplacedBy != "chatterbox-turbo-8bit" || rm.Warning == "" {
			t.Errorf("%s: %d %+v", path, resp.StatusCode, rm)
		}
	}
	resp = do(t, ts, "POST", "/api/models/kitten-tts-mini-0.8/download", nil, &e)
	if resp.StatusCode != 410 || e.Error.Code != CodeModelRemoved {
		t.Errorf("download: %d %+v", resp.StatusCode, e)
	}
}

// TestRemovedAnalysisModels: thread #46's removed models. A transcription
// on Parakeet is answered by Qwen3-ASR (Whisper for a language Qwen3-ASR
// does not know) with the replacement headers, or replaced_by / warning
// in the native answer; the default (whisper-1, none) is Qwen3-ASR for
// Chinese and English and Whisper for Swahili; the removed language and
// separation models are not listed, are described with their
// replacement, can't be downloaded and are no speech models.
func TestRemovedAnalysisModels(t *testing.T) {
	f := newFake(t)
	_, ts := f.server()
	qwen, _ := catalog.Builtin().ByID(catalog.DefaultSTT)
	whisper, _ := catalog.Builtin().ByID(catalog.FallbackSTT)
	last := func() TranscribeRequestSeen {
		f.mu.Lock()
		defer f.mu.Unlock()
		r := f.transcribed[len(f.transcribed)-1]
		return TranscribeRequestSeen{r.Model, r.Language}
	}
	for _, c := range []struct {
		fields []string
		repo   string
		rep    string
	}{
		{[]string{"model", "parakeet-tdt-0.6b-v3"}, qwen.Repo, qwen.ID},
		{[]string{"model", "mlx-community/parakeet-tdt-0.6b-v3", "language", "bg"}, whisper.Repo, whisper.ID},
		{[]string{"model", "Parakeet TDT 0.6B v3", "language", "de"}, qwen.Repo, qwen.ID},
		{[]string{"model", "whisper-1"}, qwen.Repo, ""},
		{[]string{"model", "whisper-1", "language", "zh"}, qwen.Repo, ""},
		{[]string{"model", "gpt-4o-transcribe", "language", "sw"}, whisper.Repo, ""},
		{[]string{"language", "en"}, qwen.Repo, ""},
		{[]string{"model", catalog.FallbackSTT, "language", "zh"}, whisper.Repo, ""}, // named: kept
	} {
		resp := sdkTranscribe(t, ts, "/v1/audio/transcriptions", c.fields...)
		io.Copy(io.Discard, resp.Body)
		if resp.StatusCode != 200 || last().Model != c.repo || resp.Header.Get("X-AudioSmith-Replaced-By") != c.rep ||
			(c.rep != "") != strings.Contains(resp.Header.Get("X-AudioSmith-Warning"), "Parakeet TDT 0.6B v3 was removed") {
			t.Errorf("%v: %d %s %v", c.fields, resp.StatusCode, last().Model, resp.Header)
		}
	}
	ct, body := multipartFile(t, "a.m4a", []byte("x"), "model", "parakeet-tdt-0.6b-v3")
	var out map[string]any
	if resp := do(t, ts, "POST", "/api/transcripts", body.String(), &out, "Content-Type", ct); resp.StatusCode != 201 || out["replaced_by"] != qwen.ID ||
		!strings.Contains(fmt.Sprint(out["warning"]), "was removed") {
		t.Errorf("native: %d %v", resp.StatusCode, out)
	}
	var list struct{ Data []map[string]any }
	for _, path := range []string{"/v1/models", "/api/models"} {
		do(t, ts, "GET", path, nil, &list)
		for _, m := range list.Data {
			if _, ok := catalog.Builtin().RemovedModel(fmt.Sprint(m["id"])); ok {
				t.Errorf("%s lists %v", path, m["id"])
			}
		}
	}
	for id, by := range map[string]string{"gemma-4-e2b-it-4bit": catalog.DefaultLLM, "Gemma 4 E4B": catalog.DefaultLLM, "qwen3-1.7b-4bit": catalog.DefaultLLM,
		"mel-roformer-kim-vocal-2": catalog.DefaultSeparate, "parakeet-tdt-0.6b-v3": catalog.DefaultSTT} {
		var rm RemovedModelJSON
		if resp := do(t, ts, "GET", "/api/models/"+strings.ReplaceAll(id, " ", "%20"), nil, &rm); resp.StatusCode != 200 || !rm.Removed || rm.ReplacedBy != by || rm.Warning == "" {
			t.Errorf("%s: %d %+v", id, resp.StatusCode, rm)
		}
	}
	var e apiErr
	if resp := do(t, ts, "POST", "/api/models/gemma-4-e2b-it-4bit/download", nil, &e); resp.StatusCode != 410 || e.Error.Code != CodeModelRemoved {
		t.Errorf("download: %d %+v", resp.StatusCode, e)
	}
	// Not speech models: no speech model answers for them.
	if resp := sdkSpeech(t, ts, `{"model":"gemma-4-e2b-it-4bit","input":"hi"}`); resp.StatusCode != 404 {
		t.Errorf("speech on Gemma: %d", resp.StatusCode)
	}
}

// TranscribeRequestSeen is what the fake backend was asked to transcribe.
type TranscribeRequestSeen struct{ Model, Language string }

// TestTranscriptionsSpeakers (thread #48): speakers=true, OpenAI's
// gpt-4o-transcribe-diarize or diarized_json transcribe with the speakers
// model; segments carry speakers (names given in Transcribe); SRT cues
// start with them; translations refuse speakers.
func TestTranscriptionsSpeakers(t *testing.T) {
	f := newFake(t)
	_, ts := f.server()
	moss, _ := catalog.Builtin().SpeakersModel()
	last := func() services.TranscribeRequest {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.transcribed[len(f.transcribed)-1]
	}
	resp := sdkTranscribe(t, ts, "/v1/audio/transcriptions", "model", "whisper-1", "speakers", "true", "response_format", "verbose_json")
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || last().Model != moss.Repo || !strings.Contains(string(body), `"speaker":"Ana"`) || !strings.Contains(string(body), `"speaker":"S2"`) {
		t.Fatalf("speakers=true: %d %s (%+v)", resp.StatusCode, body, last())
	}
	resp = sdkTranscribe(t, ts, "/v1/audio/transcriptions", "model", "gpt-4o-transcribe-diarize", "response_format", "diarized_json")
	body, _ = io.ReadAll(resp.Body)
	var d struct {
		Text     string
		Segments []struct{ Type, ID, Speaker, Text string }
	}
	json.Unmarshal(body, &d)
	if resp.StatusCode != 200 || last().Model != moss.Repo || len(d.Segments) != 2 || d.Segments[0].Type != "transcript.text.segment" ||
		d.Segments[0].Speaker != "Ana" || d.Segments[1].Speaker != "S2" {
		t.Fatalf("diarized_json: %d %s", resp.StatusCode, body)
	}
	resp = sdkTranscribe(t, ts, "/v1/audio/transcriptions", "response_format", "srt", "speakers", "1")
	body, _ = io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Ana: Bonjour") || !strings.Contains(string(body), "S2: Ça va bien.") {
		t.Fatalf("srt: %s", body)
	}
	resp = sdkTranscribe(t, ts, "/v1/audio/translations", "speakers", "true")
	var e apiErr
	json.NewDecoder(resp.Body).Decode(&e)
	if resp.StatusCode != 400 || e.Error.Param != "speakers" {
		t.Fatalf("translation: %d %+v", resp.StatusCode, e)
	}
	// AudioSmith's own endpoint.
	ct, mb := multipartFile(t, "panel.m4a", []byte("fake audio"), "speakers", "true")
	req, _ := http.NewRequest("POST", ts.URL+"/api/transcripts", mb)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("X-AudioSmith-Request", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != 201 || last().Model != moss.Repo || !strings.Contains(string(body), `"speaker":"S1"`) || !strings.Contains(string(body), `"speakers":{"S1":"Ana"}`) {
		t.Fatalf("/api/transcripts: %d %s", resp.StatusCode, body)
	}
	// Without it, the default.
	sdkTranscribe(t, ts, "/v1/audio/transcriptions", "model", "whisper-1")
	if last().Model == moss.Repo {
		t.Fatal("the speakers model without asking")
	}
}
