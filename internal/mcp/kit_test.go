package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
)

// kitFake is the fake with the toolkit (api.Toolkit) in memory: the
// app's own tests (api_kit_test.go) run the toolkit for real.
type kitFake struct {
	*fake
	isos    map[string]api.IsolationJSON
	samples []string
}

func newKitFake(t *testing.T) *kitFake {
	return &kitFake{fake: newFake(t), isos: map[string]api.IsolationJSON{}}
}

var _ api.Toolkit = (*kitFake)(nil)

// writeTone writes 0.2 s of a tone as a WAV.
func writeTone(t *testing.T, path string) string {
	t.Helper()
	buf := dsp.New(16000, 1, 3200)
	for i := range buf.Samples {
		buf.Samples[i] = 0.1
	}
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := wav.WriteFile(path, buf, wav.PCM16, wav.Options{}); err != nil {
		t.Fatal(err)
	}
	return path
}

func (k *kitFake) StartIsolation(ctx context.Context, path string) (api.IsolationJSON, error) {
	if !strings.HasSuffix(path, ".wav") {
		return api.IsolationJSON{}, &api.Error{Status: 400, Code: api.CodeUnsupportedFormat, Param: "path", Message: "not media"}
	}
	id := core.NewID(time.Now())
	dir := filepath.Join(k.dir, "isolations", id)
	vocals := filepath.Join(dir, "vocals.wav")
	data, _ := os.ReadFile(path)
	os.MkdirAll(dir, 0o755)
	os.WriteFile(vocals, data, 0o644)
	x := api.IsolationJSON{ID: id, Title: "in", Input: path, State: "done", JobID: "job-" + id, CreatedAt: time.Now(),
		Stems: []api.StemJSON{{Name: "vocals", File: vocals, About: "The voices alone."}}}
	k.mu.Lock()
	k.isos[id] = x
	k.mu.Unlock()
	return x, nil
}
func (k *kitFake) Isolations(ctx context.Context) ([]api.IsolationJSON, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := []api.IsolationJSON{}
	for _, x := range k.isos {
		out = append(out, x)
	}
	return out, nil
}
func (k *kitFake) Isolation(ctx context.Context, id string) (api.IsolationJSON, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	x, ok := k.isos[id]
	if !ok {
		return x, core.ErrNotFound
	}
	return x, nil
}
func (k *kitFake) DeleteIsolation(ctx context.Context, id string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, ok := k.isos[id]; !ok {
		return core.ErrNotFound
	}
	delete(k.isos, id)
	return nil
}
func (k *kitFake) Assets(ctx context.Context, q api.AssetQuery) ([]api.AssetJSON, error) {
	l := []api.AssetJSON{{ID: "take:1", Kind: "speech", Title: "Hello", File: "/x/1.wav"}, {ID: "stem:2:vocals", Kind: "stems", Title: "in · vocals", File: "/x/v.wav"}}
	out := []api.AssetJSON{}
	for _, a := range l {
		if q.Kind == "" || a.Kind == q.Kind {
			out = append(out, a)
		}
	}
	return out, nil
}
func (k *kitFake) Samples(ctx context.Context) ([]api.SampleJSON, error) {
	return []api.SampleJSON{{ID: "story", Kind: "story", Title: "A story for two voices", Available: true}}, nil
}
func (k *kitFake) OpenSample(ctx context.Context, id string) (api.SampleOpened, error) {
	if id != "story" {
		return api.SampleOpened{}, &api.Error{Status: 404, Code: api.CodeNotFound, Message: "There is no sample " + id}
	}
	k.mu.Lock()
	k.samples = append(k.samples, id)
	k.mu.Unlock()
	return api.SampleOpened{ID: id, Kind: "story", Ref: "story:p1", Tool: "get_longform project_id p1"}, nil
}
func (k *kitFake) voiceIndex(id string) int {
	for i, v := range k.voices {
		if v.ID == id || strings.EqualFold(v.Name, id) {
			return i
		}
	}
	return -1
}
func (k *kitFake) UpdateVoice(ctx context.Context, req api.VoiceUpdate) (core.Voice, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	i := k.voiceIndex(req.ID)
	if i < 0 {
		return core.Voice{}, &api.Error{Status: 404, Code: api.CodeVoiceNotFound, Message: "No saved voice is called " + req.ID}
	}
	if req.Name != "" {
		k.voices[i].Name = core.CleanName(req.Name)
	}
	if req.Tags != nil {
		k.voices[i].Tags = *req.Tags
	}
	return k.voices[i], nil
}
func (k *kitFake) DuplicateVoice(ctx context.Context, req api.VoiceDuplicate) (core.Voice, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	i := k.voiceIndex(req.ID)
	if i < 0 {
		return core.Voice{}, &api.Error{Status: 404, Code: api.CodeVoiceNotFound, Message: "No saved voice is called " + req.ID}
	}
	cp := k.voices[i]
	cp.ID, cp.Name = "copy-"+cp.ID, cp.Name+" (copy)"
	k.voices = append(k.voices, cp)
	return cp, nil
}
func (k *kitFake) ExportVoice(ctx context.Context, req api.VoiceExportRequest) (api.ExportJSON, error) {
	dst := filepath.Join(req.Dir, api.SafeName(req.Filename, req.ID, "asvoice"))
	if err := os.WriteFile(dst, []byte("PK"), 0o644); err != nil {
		return api.ExportJSON{}, err
	}
	return api.ExportJSON{Files: []string{dst}, Format: "asvoice", Bytes: 2}, nil
}
func (k *kitFake) ImportVoice(ctx context.Context, path string) (api.VoiceImported, error) {
	v, err := k.SaveVoice(ctx, core.Voice{Name: "Imported", Kind: core.VoiceCloned}, core.VoiceFiles{})
	return api.VoiceImported{Voice: api.VoiceOf(v)}, err
}
func (k *kitFake) VoicePreview(ctx context.Context, q api.VoicePreviewQuery) (api.VoicePreview, error) {
	if q.Gallery == "" {
		return api.VoicePreview{}, &api.Error{Status: 409, Code: "not_ready", Message: "no audio yet"}
	}
	return api.VoicePreview{Data: []byte("OggS-preview"), MIME: "audio/ogg", Ext: "ogg", Name: q.Gallery + ".ogg", Text: "Hi."}, nil
}
func (k *kitFake) CheckReference(ctx context.Context, req api.RefCheckRequest) (api.RefCheckJSON, error) {
	return api.RefCheckJSON{Grade: "good", Seconds: 0.2, SpeechSeconds: 0.2}, nil
}
func (k *kitFake) WriteStory(ctx context.Context, req api.StoryDraftRequest) (api.StoryDraftJSON, error) {
	if strings.Contains(req.Description, "no model") {
		return api.StoryDraftJSON{}, &api.Error{Status: 400, Code: api.CodeLLMUnavailable, Message: "Writing a draft needs a language model."}
	}
	return api.StoryDraftJSON{Script: "# Rain\n[voice:Ann] Hello.\n[voice:Bo] Hi.", Title: "Rain"}, nil
}

// serveKit is serve for a backend with the toolkit.
func serveKit(t *testing.T, k *kitFake) *httptest.Server {
	t.Helper()
	a := api.New(api.Options{Backend: k, Sched: k.sched, Bus: k.bus, Version: "9.9", Logf: t.Logf})
	m := New(Options{API: a, Backend: k, Version: "9.9"})
	a.MountMCP(m.Handler())
	ts := httptest.NewServer(a.Handler())
	t.Cleanup(func() {
		ts.Close()
		a.Close(context.Background())
	})
	return ts
}

// failsWith checks that a call failed with code.
func failsWith(t *testing.T, cs *sdk.ClientSession, tool string, args map[string]any, code string) {
	t.Helper()
	res := call(t, cs, tool, args)
	if !res.IsError || !strings.HasPrefix(textOf(res), code+":") {
		t.Errorf("%s %v: want %s, got %s", tool, args, code, textOf(res))
	}
}

// succeeds checks that a call worked and decodes its JSON.
func succeeds(t *testing.T, cs *sdk.ClientSession, tool string, args map[string]any, out any) *sdk.CallToolResult {
	t.Helper()
	res := call(t, cs, tool, args)
	if res.IsError {
		t.Fatalf("%s %v: %s", tool, args, textOf(res))
	}
	if out != nil {
		if err := json.Unmarshal([]byte(textOf(res)), out); err != nil {
			t.Fatalf("%s: %v in %s", tool, err, textOf(res))
		}
	}
	return res
}

func audioOf(res *sdk.CallToolResult) *sdk.AudioContent {
	for _, c := range res.Content {
		if a, ok := c.(*sdk.AudioContent); ok {
			return a
		}
	}
	return nil
}

// TestKitIsolator: isolate_voice starts an isolation, get_isolation lists
// it, reads it and returns a stem's audio; delete_item deletes it with
// confirm; LAN clients and backends without the toolkit are refused.
func TestKitIsolator(t *testing.T) {
	k := newKitFake(t)
	cs := connect(t, serveKit(t, k).URL, "claude-code", nil)
	in := writeTone(t, filepath.Join(t.TempDir(), "song.wav"))

	var x api.IsolationJSON
	succeeds(t, cs, "isolate_voice", map[string]any{"path": in}, &x)
	if x.ID == "" || x.JobID == "" || !strings.Contains(x.Next, "get_isolation") {
		t.Fatalf("isolation %+v", x)
	}
	failsWith(t, cs, "isolate_voice", map[string]any{"path": filepath.Join(t.TempDir(), "gone.wav")}, api.CodeNotFound)
	txt := filepath.Join(t.TempDir(), "notes.txt")
	os.WriteFile(txt, []byte("x"), 0o644)
	failsWith(t, cs, "isolate_voice", map[string]any{"path": txt}, api.CodeUnsupportedFormat)
	failsWith(t, cs, "isolate_voice", map[string]any{"path": "relative.wav"}, api.CodeInvalid)

	var list struct {
		Count int `json:"count"`
	}
	succeeds(t, cs, "get_isolation", nil, &list)
	if list.Count != 1 {
		t.Fatalf("list %d", list.Count)
	}
	res := succeeds(t, cs, "get_isolation", map[string]any{"isolation_id": x.ID, "stem": "vocals", "output": "inline"}, nil)
	if a := audioOf(res); a == nil || a.MIMEType != "audio/wav" || len(a.Data) < 44 {
		t.Fatalf("stem audio: %v", res.Content)
	}
	failsWith(t, cs, "get_isolation", map[string]any{"isolation_id": x.ID, "stem": "clean"}, api.CodeNotFound)
	failsWith(t, cs, "get_isolation", map[string]any{"stem": "vocals"}, api.CodeInvalid)
	failsWith(t, cs, "get_isolation", map[string]any{"isolation_id": "nope"}, api.CodeNotFound)

	failsWith(t, cs, "delete_item", map[string]any{"kind": "isolation", "id": x.ID}, api.CodeConfirmRequired)
	succeeds(t, cs, "delete_item", map[string]any{"kind": "isolation", "id": x.ID, "confirm": true}, nil)
	if len(k.isos) != 0 {
		t.Fatal("not deleted")
	}

	// Without the toolkit: unsupported.
	plain := connect(t, func() string { _, _, ts := serve(t, newFake(t)); return ts.URL }(), "claude-code", nil)
	for _, tool := range []string{"list_assets", "list_samples", "get_isolation"} {
		failsWith(t, plain, tool, nil, api.CodeUnsupported)
	}
}

// TestKitLibraryAndVoices: assets, samples, voice management, previews,
// the reference check and the story draft, each with an error.
func TestKitLibraryAndVoices(t *testing.T) {
	k := newKitFake(t)
	k.voices = []core.Voice{{ID: "v1", Name: "Ann", Kind: core.VoiceCloned}}
	cs := connect(t, serveKit(t, k).URL, "claude-code", nil)

	var assets struct {
		Count  int             `json:"count"`
		Assets []api.AssetJSON `json:"assets"`
	}
	succeeds(t, cs, "list_assets", map[string]any{"kind": "stems"}, &assets)
	if assets.Count != 1 || assets.Assets[0].Kind != "stems" {
		t.Fatalf("assets %+v", assets)
	}
	failsWith(t, cs, "list_assets", map[string]any{"kind": "photos"}, api.CodeInvalid)

	var samples struct {
		Samples []api.SampleJSON `json:"samples"`
	}
	succeeds(t, cs, "list_samples", nil, &samples)
	if len(samples.Samples) != 1 || !samples.Samples[0].Available {
		t.Fatalf("samples %+v", samples)
	}
	var opened api.SampleOpened
	succeeds(t, cs, "open_sample", map[string]any{"sample_id": "story"}, &opened)
	if opened.Ref != "story:p1" || !strings.HasPrefix(opened.Tool, "get_longform") {
		t.Fatalf("opened %+v", opened)
	}
	failsWith(t, cs, "open_sample", map[string]any{"sample_id": "nope"}, api.CodeNotFound)
	failsWith(t, cs, "open_sample", map[string]any{"sample_id": ""}, api.CodeInvalid)

	var upd struct {
		Voice map[string]any `json:"voice"`
	}
	succeeds(t, cs, "update_voice", map[string]any{"voice_id": "Ann", "name": "Ann Lee", "tags": []string{"warm"}}, &upd)
	if upd.Voice["name"] != "Ann Lee" || len(k.voices[0].Tags) != 1 {
		t.Fatalf("update %+v %+v", upd, k.voices[0])
	}
	failsWith(t, cs, "update_voice", map[string]any{"voice_id": "v1"}, api.CodeInvalid)
	failsWith(t, cs, "update_voice", map[string]any{"voice_id": "Nobody", "name": "X"}, api.CodeVoiceNotFound)
	succeeds(t, cs, "duplicate_voice", map[string]any{"voice_id": "v1"}, &upd)
	if upd.Voice["name"] != "Ann Lee (copy)" {
		t.Fatalf("duplicate %+v", upd)
	}
	failsWith(t, cs, "duplicate_voice", map[string]any{"voice_id": "Nobody"}, api.CodeVoiceNotFound)

	var ex api.ExportJSON
	succeeds(t, cs, "export_voice", map[string]any{"voice_id": "v1", "filename": "ann"}, &ex)
	if len(ex.Files) != 1 || filepath.Dir(ex.Files[0]) != filepath.Join(k.dir, "mcp") || !strings.HasSuffix(ex.Files[0], "ann.asvoice") {
		t.Fatalf("export %+v", ex)
	}
	failsWith(t, cs, "export_voice", map[string]any{"voice_id": "v1", "directory": "relative"}, api.CodeInvalid)
	var imp api.VoiceImported
	succeeds(t, cs, "import_voice", map[string]any{"path": ex.Files[0]}, &imp)
	if imp.Voice.Name != "Imported" {
		t.Fatalf("import %+v", imp)
	}
	failsWith(t, cs, "import_voice", map[string]any{"path": writeTone(t, filepath.Join(t.TempDir(), "a.wav"))}, api.CodeUnsupportedFormat)

	res := succeeds(t, cs, "preview_voice", map[string]any{"gallery_id": "curated-ann", "output": "both"}, nil)
	var pv map[string]any
	json.Unmarshal([]byte(textOf(res)), &pv)
	if a := audioOf(res); a == nil || a.MIMEType != "audio/ogg" || pv["file"] == "" {
		t.Fatalf("preview %v %s", res.Content, textOf(res))
	}
	if data, _ := os.ReadFile(pv["file"].(string)); string(data) != "OggS-preview" {
		t.Fatalf("preview file %q", data)
	}
	failsWith(t, cs, "preview_voice", map[string]any{"gallery_id": "x", "voice_id": "v1"}, api.CodeInvalid)
	failsWith(t, cs, "preview_voice", map[string]any{"model": "kokoro-82m-bf16"}, api.CodeInvalid)
	failsWith(t, cs, "preview_voice", map[string]any{"voice_id": "v1"}, "not_ready")

	var rc api.RefCheckJSON
	succeeds(t, cs, "check_reference", map[string]any{"path": writeTone(t, filepath.Join(t.TempDir(), "ref.wav"))}, &rc)
	if rc.Grade != "good" {
		t.Fatalf("check %+v", rc)
	}
	failsWith(t, cs, "check_reference", map[string]any{"path": "/no/such/ref.wav"}, api.CodeNotFound)

	var draft api.StoryDraftJSON
	succeeds(t, cs, "write_story", map[string]any{"description": "Two friends in the rain"}, &draft)
	if draft.Title != "Rain" || !strings.Contains(draft.Script, "[voice:Ann]") {
		t.Fatalf("draft %+v", draft)
	}
	failsWith(t, cs, "write_story", map[string]any{"description": "no model here"}, api.CodeLLMUnavailable)
	failsWith(t, cs, "write_story", map[string]any{"description": " "}, api.CodeInvalid)
}

// TestKitSmallTools: design candidates and convert over the fake engine,
// model downloads behind confirm, the watermark check, probe, direction,
// rate fit, settings and transcribe's mode.
func TestKitSmallTools(t *testing.T) {
	k := newKitFake(t)
	cs := connect(t, serveKit(t, k).URL, "claude-code", nil)

	var cands struct {
		Candidates []map[string]any `json:"candidates"`
		Next       string           `json:"next"`
	}
	succeeds(t, cs, "design_voice_candidates", map[string]any{"description": "a warm older British man", "count": 2, "output": "file"}, &cands)
	if len(cands.Candidates) != 2 || !strings.Contains(cands.Next, "design_voice") {
		t.Fatalf("candidates %+v", cands)
	}
	for _, c := range cands.Candidates {
		if _, err := os.Stat(c["file"].(string)); err != nil || c["seed"].(float64) == 0 {
			t.Fatalf("candidate %+v: %v", c, err)
		}
	}
	k.mu.Lock()
	g := k.generated[len(k.generated)-1]
	k.mu.Unlock()
	if g.Instructions != "a warm older British man" || g.Seed == 0 || g.Voice != nil {
		t.Fatalf("candidate request %+v", g)
	}
	failsWith(t, cs, "design_voice_candidates", map[string]any{"description": "a voice", "count": 5}, api.CodeInvalid)
	failsWith(t, cs, "design_voice_candidates", map[string]any{"description": "a voice", "count": 1, "model": "kokoro-82m-bf16"}, api.CodeUnsupportedModel)

	var conv map[string]any
	src := writeTone(t, filepath.Join(t.TempDir(), "talk.wav"))
	succeeds(t, cs, "convert_voice", map[string]any{"path": src, "voice": "af_heart", "output": "file", "filename": "talk-af"}, &conv)
	if conv["text"] != "hello world" || !strings.HasSuffix(conv["file"].(string), "talk-af.wav") {
		t.Fatalf("convert %+v", conv)
	}
	// Kokoro has no target length: match_length takes it again at the speed
	// that fits (the fake's takes are 0.2 s); the voice speaks no Chinese.
	conv = nil
	succeeds(t, cs, "convert_voice", map[string]any{"path": src, "voice": "af_heart", "model": "kokoro-82m-bf16", "language": "zh", "match_length": true}, &conv)
	k.mu.Lock()
	g = k.generated[len(k.generated)-1]
	k.mu.Unlock()
	if conv["speed"] == nil || g.Speed == 0 || g.Speed > 1 || conv["warning"] == nil {
		t.Fatalf("convert, match_length %+v, request %+v", conv, g)
	}
	failsWith(t, cs, "convert_voice", map[string]any{"path": src, "voice": ""}, api.CodeInvalid)

	failsWith(t, cs, "download_model", map[string]any{"model": "kokoro-82m-bf16"}, api.CodeConfirmRequired)
	succeeds(t, cs, "download_model", map[string]any{"model": "kokoro-82m-bf16", "confirm": true}, nil)
	failsWith(t, cs, "download_model", map[string]any{"model": "no-such-model", "confirm": true}, api.CodeModelNotFound)

	succeeds(t, cs, "detect_watermark", map[string]any{"path": src}, nil)
	failsWith(t, cs, "detect_watermark", map[string]any{"path": "rel.wav"}, api.CodeInvalid)
	succeeds(t, cs, "probe_media", map[string]any{"path": src}, nil)
	failsWith(t, cs, "probe_media", map[string]any{"path": "/no/such.mp4"}, api.CodeNotFound)
	var d map[string]any
	succeeds(t, cs, "direct_performance", map[string]any{"note": "whispered, getting angry"}, &d)
	failsWith(t, cs, "direct_performance", map[string]any{"note": " "}, api.CodeInvalid)
	succeeds(t, cs, "check_line_fit", map[string]any{"text": "Hello there.", "slot": 1.5}, nil)
	failsWith(t, cs, "check_line_fit", map[string]any{"text": "Hello there.", "slot": 0}, api.CodeInvalid)

	var st map[string]any
	succeeds(t, cs, "get_settings", nil, &st)
	if st["agents_folder"] != filepath.Join(k.dir, "mcp") || st["output"] == "" {
		t.Fatalf("settings %+v", st)
	}
	// Unset models are named by the default AudioSmith uses (#136).
	tr, _ := st["transcribe"].(map[string]any)
	dub, _ := st["dubbing"].(map[string]any)
	if st["speech_model"] == "" || tr["accurate_model"] == "" || tr["fast_model"] == "" || fmt.Sprint(dub["targets"]) != "[zh-Hans]" {
		t.Fatalf("settings defaults %+v", st)
	}

	succeeds(t, cs, "transcribe", map[string]any{"path": src, "mode": "fast"}, nil)
	failsWith(t, cs, "transcribe", map[string]any{"path": src, "mode": "slow"}, api.CodeInvalid)
}

// TestKitLAN: the toolkit's tools that touch this Mac are refused to LAN
// clients with the PIN only.
func TestKitLAN(t *testing.T) {
	k := newKitFake(t)
	a := api.New(api.Options{Backend: k, Sched: k.sched, Bus: k.bus, Version: "9.9", Logf: t.Logf})
	a.MountMCP(New(Options{API: a, Backend: k, Version: "9.9"}).Handler())
	t.Cleanup(func() { a.Close(context.Background()) })
	share, err := a.StartLAN(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.StopLAN)
	lan := httptest.NewServer(a.LANHandler(share.Port))
	t.Cleanup(lan.Close)
	cs := connect(t, lan.URL, "lan-agent", http.Header{api.HeaderPIN: {share.PIN}})
	for tool, args := range map[string]map[string]any{
		"isolate_voice": {"path": "/x.wav"}, "list_assets": {}, "open_sample": {"sample_id": "story"}, "update_voice": {"voice_id": "v1", "name": "X"},
		"export_voice": {"voice_id": "v1"}, "design_voice_candidates": {"description": "a voice"}, "download_model": {"model": "kokoro-82m-bf16", "confirm": true},
		"convert_voice": {"path": "/x.wav", "voice": "a"}, "check_reference": {"path": "/x.wav"}, "preview_voice": {"gallery_id": "x"},
	} {
		res := call(t, cs, tool, args)
		if !res.IsError || !strings.Contains(textOf(res), api.CodeUnauthorized) {
			t.Errorf("LAN %s: %s", tool, textOf(res))
		}
	}
	if len(k.samples) != 0 {
		t.Fatal("a LAN client opened a sample")
	}
}
