package main

// Tests of the toolkit for agents (thread #135) over the real library,
// job queue and services (studioEnv): the Voice isolator, Assets, the
// samples, voice management and previews, the reference check, the
// story draft, and dubbing a link.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/gallery"
	"github.com/hehehai/AudioSmith/internal/llm"
)

// TestAPIKitIsolator: isolate_voice runs the isolator's job; get_job
// follows it, get_isolation gives its stems, list_assets lists them, and
// delete_item removes them after confirm.
func TestAPIKitIsolator(t *testing.T) {
	e := newStudioEnv(t)
	e.onMain(func() { e.a.isoEngine = isoFakeEngine(nil) })
	in := filepath.Join(t.TempDir(), "song.wav")
	writeTestWAV(t, in, 24000, 1)

	var x api.IsolationJSON
	e.must(t, "isolate_voice", map[string]any{"path": in}, &x)
	if x.ID == "" || x.JobID == "" {
		t.Fatalf("isolation %+v", x)
	}
	if j := e.wait(t, x.JobID); j.State != "succeeded" {
		t.Fatalf("job %+v", j)
	}
	e.must(t, "get_isolation", map[string]any{"isolation_id": x.ID}, &x)
	if x.State != "done" || len(x.Stems) == 0 {
		t.Fatalf("isolation %+v", x)
	}
	for _, s := range x.Stems {
		if _, err := os.Stat(s.File); err != nil || s.About == "" {
			t.Fatalf("stem %+v: %v", s, err)
		}
	}
	var assets struct {
		Assets []api.AssetJSON `json:"assets"`
	}
	e.must(t, "list_assets", map[string]any{"kind": "stems"}, &assets)
	if len(assets.Assets) != len(x.Stems) || assets.Assets[0].Tool != "get_isolation" {
		t.Fatalf("assets %+v", assets)
	}
	e.fails(t, "isolate_voice", map[string]any{"path": filepath.Join(t.TempDir(), "x.txt")}, api.CodeNotFound)
	txt := filepath.Join(t.TempDir(), "notes.txt")
	os.WriteFile(txt, []byte("x"), 0o644)
	e.fails(t, "isolate_voice", map[string]any{"path": txt}, api.CodeUnsupportedFormat)
	e.fails(t, "delete_item", map[string]any{"kind": "isolation", "id": x.ID}, api.CodeConfirmRequired)
	e.must(t, "delete_item", map[string]any{"kind": "isolation", "id": x.ID, "confirm": true}, nil)
	e.fails(t, "get_isolation", map[string]any{"isolation_id": x.ID}, api.CodeNotFound)
}

// TestAPIKitVoices: a cloned voice is checked, renamed, tagged,
// duplicated, exported as .asvoice and imported again; previews of a
// saved voice, a Discover voice and a built-in speaker come back.
func TestAPIKitVoices(t *testing.T) {
	e := newStudioEnv(t)
	ref := filepath.Join(t.TempDir(), "ref.wav")
	writeTestWAV(t, ref, 24000, 6)

	var rc api.RefCheckJSON
	e.must(t, "check_reference", map[string]any{"path": ref, "text": "Hello there."}, &rc)
	if rc.Grade == "" || rc.Seconds < 5 {
		t.Fatalf("check %+v", rc)
	}
	e.fails(t, "check_reference", map[string]any{"path": "/no/such.wav"}, api.CodeNotFound)

	var cl struct {
		Voice map[string]any `json:"voice"`
	}
	e.must(t, "clone_voice", map[string]any{"name": "Ann", "path": ref, "ref_text": "Hello there.", "consent": true}, &cl)
	id := cl.Voice["id"].(string)
	var up struct {
		Voice map[string]any `json:"voice"`
	}
	e.must(t, "update_voice", map[string]any{"voice_id": "Ann", "name": "Ann Lee", "style": "calm", "tags": []string{"warm", "warm", " narrator "}}, &up)
	v, err := e.lib.svc.Voices.Get(t.Context(), id)
	if err != nil || v.Name != "Ann Lee" || v.Style != "calm" || strings.Join(v.Tags, ",") != "warm,narrator" {
		t.Fatalf("updated %+v %v", v, err)
	}
	e.fails(t, "update_voice", map[string]any{"voice_id": "Nobody", "name": "X"}, api.CodeVoiceNotFound)
	e.must(t, "duplicate_voice", map[string]any{"voice_id": id}, &up)
	if up.Voice["name"] != "Ann Lee (copy)" || up.Voice["id"] == id {
		t.Fatalf("copy %+v", up)
	}

	var ex api.ExportJSON
	e.must(t, "export_voice", map[string]any{"voice_id": id, "include_reference": true}, &ex)
	if len(ex.Files) != 1 || filepath.Dir(ex.Files[0]) != e.out || !strings.HasSuffix(ex.Files[0], ".asvoice") || ex.Bytes == 0 {
		t.Fatalf("export %+v", ex)
	}
	var imp api.VoiceImported
	e.must(t, "import_voice", map[string]any{"path": ex.Files[0]}, &imp)
	if imp.Voice.ID == "" || imp.Voice.ID == id {
		t.Fatalf("import %+v", imp)
	}
	e.fails(t, "import_voice", map[string]any{"path": ref}, api.CodeUnsupportedFormat)

	var pv map[string]any
	e.must(t, "preview_voice", map[string]any{"voice_id": id, "output": "file"}, &pv)
	if data, err := os.ReadFile(pv["file"].(string)); err != nil || !strings.HasPrefix(string(data), "RIFF") || pv["text"] != "Hello there." {
		t.Fatalf("saved voice preview %+v: %v", pv, err)
	}
	var arch string
	for _, a := range gallery.Default().All() {
		if strings.HasPrefix(a.ID, gallery.CuratedPrefix) && a.Kind != gallery.KindBuiltin && galleryClip(a.ID, "preview") != nil {
			arch = a.ID
			break
		}
	}
	e.must(t, "preview_voice", map[string]any{"gallery_id": arch, "output": "file"}, &pv)
	if data, _ := os.ReadFile(pv["file"].(string)); !strings.HasPrefix(string(data), "OggS") {
		t.Fatalf("gallery preview %+v", pv)
	}
	e.must(t, "preview_voice", map[string]any{"model": "kokoro-82m-bf16", "speaker": "af_heart", "output": "file"}, &pv)
	if data, _ := os.ReadFile(pv["file"].(string)); !strings.HasPrefix(string(data), "OggS") {
		t.Fatalf("speaker preview %+v", pv)
	}
	e.fails(t, "preview_voice", map[string]any{"model": "kokoro-82m-bf16", "speaker": "nobody"}, api.CodeVoiceNotFound)
	e.fails(t, "preview_voice", map[string]any{"gallery_id": "c-no-such-voice"}, api.CodeNotFound)

	// REST: PATCH and the bundled previews.
	if code, body := e.rest(t, "PATCH", "/api/voices/"+id, map[string]any{"name": "Ann B"}); code != 200 || !strings.Contains(string(body), `"Ann B"`) {
		t.Fatalf("PATCH %d %s", code, body)
	}
	if code, body := e.rest(t, "GET", "/api/models/kokoro-82m-bf16/voices/af_heart/preview", nil); code != 200 || !strings.HasPrefix(string(body), "OggS") {
		t.Fatalf("speaker preview %d", code)
	}
}

// TestAPIKitLibrary: Assets lists a take; the samples are listed and one
// opens as a project get_longform reads; the story draft needs a
// language model; a dub from a link needs link import.
func TestAPIKitLibrary(t *testing.T) {
	e := newStudioEnv(t)
	var sp struct {
		TakeID string `json:"take_id"`
		File   string `json:"file"`
	}
	e.must(t, "generate_speech", map[string]any{"text": "An asset to find.", "output": "file"}, &sp)
	var assets struct {
		Count  int             `json:"count"`
		Assets []api.AssetJSON `json:"assets"`
	}
	e.must(t, "list_assets", map[string]any{"search": "asset to find"}, &assets)
	if assets.Count != 1 || assets.Assets[0].Kind != "speech" || assets.Assets[0].Source != sp.TakeID {
		t.Fatalf("assets %+v", assets)
	}
	if _, err := os.Stat(assets.Assets[0].File); err != nil {
		t.Fatal(err)
	}
	e.fails(t, "list_assets", map[string]any{"kind": "photos"}, api.CodeInvalid)

	var samples struct {
		Samples []api.SampleJSON `json:"samples"`
	}
	e.must(t, "list_samples", nil, &samples)
	if len(samples.Samples) != 5 {
		t.Fatalf("samples %+v", samples)
	}
	var opened api.SampleOpened
	e.must(t, "open_sample", map[string]any{"sample_id": "story"}, &opened)
	kind, pid, _ := strings.Cut(opened.Ref, ":")
	if kind != sampleStory || !strings.HasPrefix(opened.Tool, "get_longform") {
		t.Fatalf("opened %+v", opened)
	}
	var lf json.RawMessage
	e.must(t, "get_longform", map[string]any{"project_id": pid}, &lf)
	e.fails(t, "open_sample", map[string]any{"sample_id": "nope"}, api.CodeNotFound)

	e.onMain(func() { e.a.llmReg.Register(localProvider, nil) })
	e.fails(t, "write_story", map[string]any{"description": "Two friends meet in the rain."}, api.CodeLLMUnavailable)
	e.onMain(func() {
		useFakeLLM(e.a, &fakeLLM{answer: func(llm.Request) (string, error) {
			return "```\n# Rain\n[voice:Ann] It's pouring.\nThe street went quiet.\n[voice:Bo] Share my umbrella?\n```", nil
		}})
	})
	var draft api.StoryDraftJSON
	e.must(t, "write_story", map[string]any{"description": "Two friends meet in the rain.", "create": true}, &draft)
	if draft.Title != "Rain" || !strings.Contains(draft.Script, "[voice:Bo]") || draft.Project == nil || len(draft.Project.Cast) != 2 {
		t.Fatalf("draft %+v", draft)
	}
	e.must(t, "get_longform", map[string]any{"project_id": draft.Project.ID}, &lf)

	e.fails(t, "create_dub", map[string]any{"url": "https://www.youtube.com/watch?v=abc"}, dub.CodeNoYtDlp)
	e.fails(t, "create_dub", map[string]any{"url": "ftp://example.com/v.mp4"}, api.CodeInvalid)
	e.fails(t, "create_dub", map[string]any{"url": "https://example.com/v", "path": "/x.mp4"}, api.CodeInvalid)
}
