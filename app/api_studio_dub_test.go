package main

// Tests of the dubbing and batch modules for agents (api_studio_dub*.go,
// api_studio_batch.go). The harness is api_studio_test.go.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/dub/dubtest"
)

// TestStudioDub: a dub from a file, run as a job to its track; its lines
// and translations; an edited translation made again; exports; the
// batch queue; deleting needs confirm.
func TestStudioDub(t *testing.T) {
	e := newStudioEnv(t)
	src := filepath.Join(t.TempDir(), "Interview.wav")
	if _, err := dubtest.Speech(src, 4); err != nil {
		t.Fatal(err)
	}
	e.fails(t, "create_dub", map[string]any{"path": src, "target_languages": []string{"xx-nope-1"}}, api.CodeInvalid)
	notMedia := filepath.Join(t.TempDir(), "notes.txt")
	os.WriteFile(notMedia, []byte("x"), 0o644)
	e.fails(t, "create_dub", map[string]any{"path": notMedia}, api.CodeUnsupportedFormat)

	var d api.DubJSON
	e.must(t, "create_dub", map[string]any{"path": src, "target_languages": []string{"es"}}, &d)
	if d.JobID != "dub:"+d.ID || d.Title != "Interview" || !slices.Equal(d.Targets, []string{"es"}) {
		t.Fatalf("dub %+v", d)
	}
	j := e.wait(t, d.JobID)
	if j.State != "succeeded" || len(j.Files) != 1 || !strings.HasSuffix(j.Files[0], ".wav") {
		t.Fatalf("dubbed %+v", j)
	}
	e.must(t, "get_dub", map[string]any{"dub_id": d.ID}, &d)
	if len(d.Segments) != 4 || d.Segments[0].Translation != "[es] Hello there." || d.Tracks["es"].File == "" || d.Phase != string(dub.PhaseDone) {
		t.Fatalf("get_dub %+v", d)
	}
	// Edit a translation; the run makes that line again.
	seg := d.Segments[1].ID
	e.must(t, "edit_dub", map[string]any{"dub_id": d.ID, "segments": []map[string]any{{"id": seg, "translation": "¿Qué tal?"}}, "names": map[string]string{"S1": "Ana"}}, &d)
	if d.Segments[1].Translation != "¿Qué tal?" || d.Speakers[0].Name != "Ana" {
		t.Fatalf("edited %+v", d)
	}
	e.fails(t, "edit_dub", map[string]any{"dub_id": d.ID, "segments": []map[string]any{{"id": "nope", "text": "x"}}}, api.CodeInvalid)
	e.must(t, "run_dub", map[string]any{"dub_id": d.ID}, &j)
	if j = e.wait(t, j.JobID); j.State != "succeeded" {
		t.Fatalf("re-run %+v", j)
	}
	// Exports: subtitles and the track.
	e.must(t, "export_dub", map[string]any{"dub_id": d.ID, "format": "srt"}, &j)
	j = e.wait(t, j.JobID)
	if j.State != "succeeded" || len(j.Files) != 1 || filepath.Dir(j.Files[0]) != e.out {
		t.Fatalf("srt %+v", j)
	}
	if data, _ := os.ReadFile(j.Files[0]); !strings.Contains(string(data), "¿Qué tal?") {
		t.Fatalf("srt %s", data)
	}
	e.must(t, "export_dub", map[string]any{"dub_id": d.ID, "format": "wav", "filename": "track"}, &j)
	if j = e.wait(t, j.JobID); j.State != "succeeded" || filepath.Base(j.Files[0]) != "track.wav" {
		t.Fatalf("wav %+v", j)
	}
	e.fails(t, "export_dub", map[string]any{"dub_id": d.ID, "format": "wav", "language": "fr"}, "not_ready")
	e.fails(t, "export_dub", map[string]any{"dub_id": d.ID, "format": "mp3"}, api.CodeUnsupportedFormat)

	// REST.
	status, body := e.rest(t, "GET", "/api/dubs/"+d.ID+"?segments=false", nil)
	if status != 200 || strings.Contains(string(body), `"segments"`) || !strings.Contains(string(body), `"phase":"done"`) {
		t.Fatalf("GET dub %d %s", status, body)
	}
	if status, body = e.rest(t, "PATCH", "/api/dubs/"+d.ID, map[string]any{"cast": map[string]string{"S9": "x"}}); status != 400 {
		t.Fatalf("PATCH %d %s", status, body)
	}
	if status, body = e.rest(t, "POST", "/api/dubs/"+d.ID+"/run", map[string]any{"through": "translation", "retranslate": true}); status != 202 {
		t.Fatalf("run %d %s", status, body)
	}
	e.wait(t, "dub:"+d.ID)
	if status, body = e.rest(t, "POST", "/api/dubs/"+d.ID+"/export", map[string]any{"format": "vtt", "directory": t.TempDir()}); status != 202 {
		t.Fatalf("export %d %s", status, body)
	}
	if status, body = e.rest(t, "POST", "/api/dubs", map[string]any{"path": src, "through": "none"}); status != 201 || !strings.Contains(string(body), `"running":false`) {
		t.Fatalf("POST /api/dubs %d %s", status, body)
	}
	var d2 api.DubJSON
	json.Unmarshal(body, &d2)
	if !slices.Equal(d2.Targets, []string{dub.DefaultTarget}) { // none given, none in Settings → Dubbing: Chinese
		t.Errorf("default targets %v", d2.Targets)
	}
	if status, body = e.rest(t, "DELETE", "/api/dubs/"+d2.ID, nil); status != 400 || !strings.Contains(string(body), api.CodeConfirmRequired) {
		t.Fatalf("DELETE %d %s", status, body)
	}
	if status, _ = e.rest(t, "DELETE", "/api/dubs/"+d2.ID+"?confirm=1", nil); status != 200 {
		t.Fatalf("DELETE %d", status)
	}
	e.fails(t, "delete_item", map[string]any{"kind": "dub", "id": d.ID}, api.CodeConfirmRequired)
	e.must(t, "delete_item", map[string]any{"kind": "dub", "id": d.ID, "confirm": true}, nil)
	e.fails(t, "get_dub", map[string]any{"dub_id": d.ID}, api.CodeNotFound)
}

// TestStudioBatch: a folder dubbed by the batch queue (the page's
// settings untouched), the queue and its watch folder listed.
func TestStudioBatch(t *testing.T) {
	e := newStudioEnv(t)
	dir := t.TempDir()
	for _, n := range []string{"a.wav", "b.wav"} {
		if _, err := dubtest.Speech(filepath.Join(dir, n), 2); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("x"), 0o644)
	before := e.a.dubSvc.BatchSettings(context.Background())
	var b api.BatchJSON
	e.must(t, "batch_dub", map[string]any{"folder": dir, "target_languages": []string{"fr"}, "paths": []string{filepath.Join(dir, "missing.wav")}}, &b)
	if len(b.Added) != 2 || len(b.Errors) != 1 || b.Errors[0].Code != api.CodeNotFound {
		t.Fatalf("batch %+v", b)
	}
	for _, it := range b.Added {
		if j := e.wait(t, it.JobID); j.State != "succeeded" || !slices.Equal(it.Languages, []string{"fr"}) {
			t.Fatalf("%s: %+v", it.Title, j)
		}
	}
	if after := e.a.dubSvc.BatchSettings(context.Background()); !slices.Equal(after.Targets, before.Targets) {
		t.Errorf("the page's settings changed: %v → %v", before.Targets, after.Targets)
	}
	e.must(t, "list_batch", nil, &b)
	if len(b.Queue) != 2 || b.Watch == nil || b.Watch.Active || b.Prefs == nil {
		t.Fatalf("queue %+v", b)
	}
	if !slices.Equal(b.Prefs.Targets, []string{dub.DefaultTarget}) {
		t.Errorf("batch default targets %v", b.Prefs.Targets)
	}
	e.fails(t, "batch_dub", map[string]any{}, api.CodeInvalid)
	if status, body := e.rest(t, "GET", "/api/batch", nil); status != 200 || !strings.Contains(string(body), `"watch_folder"`) {
		t.Fatalf("GET /api/batch %d %s", status, body)
	}
	if status, body := e.rest(t, "POST", "/api/batch", map[string]any{"folder": filepath.Join(t.TempDir(), "no-such-folder")}); status != 404 {
		t.Fatalf("POST /api/batch %d %s", status, body)
	}
}

// TestStudioDubDialogue: create_dub, edit_dub and batch_dub take
// dialogue and dialogue_model; get_dub lists the conversations, each
// line's and how it was made (by the dialogue model: dubtest's fake).
func TestStudioDubDialogue(t *testing.T) {
	e := newStudioEnv(t)
	src := filepath.Join(t.TempDir(), "Talk.wav")
	if _, err := dubtest.Speech(src, 4); err != nil {
		t.Fatal(err)
	}
	e.fails(t, "create_dub", map[string]any{"path": src, "dialogue": "maybe"}, api.CodeInvalid)
	e.fails(t, "create_dub", map[string]any{"path": src, "dialogue_model": "moss"}, api.CodeInvalid)
	var d api.DubJSON
	e.must(t, "create_dub", map[string]any{"path": src, "target_languages": []string{"zh-Hans"}, "dialogue_model": "soulx"}, &d)
	if j := e.wait(t, d.JobID); j.State != "succeeded" {
		t.Fatalf("dubbed %+v", j)
	}
	id := d.ID
	d = api.DubJSON{}
	e.must(t, "get_dub", map[string]any{"dub_id": id}, &d)
	if d.Dialogue != dub.DialogueAuto || d.DialogueModel != "soulx" || d.DialogueBlocked != "" || len(d.DialogueStretches) != 1 ||
		len(d.DialogueStretches[0].Segments) != 4 || d.DialogueStretches[0].Speakers != 2 {
		t.Fatalf("dialogue %q %q %q %+v", d.Dialogue, d.DialogueModel, d.DialogueBlocked, d.DialogueStretches)
	}
	for _, s := range d.Segments {
		if s.Dialogue != 1 || s.MadeBy != "dialogue" {
			t.Errorf("line %+v", s)
		}
	}
	// A line kept out, then dialogue off.
	last := d.Segments[3].ID
	d = api.DubJSON{}
	e.must(t, "edit_dub", map[string]any{"dub_id": id, "segments": []map[string]any{{"id": last, "no_dialogue": true}}}, &d)
	if !d.Segments[3].NoDialogue || d.Segments[3].Dialogue != 0 || len(d.DialogueStretches) != 1 || len(d.DialogueStretches[0].Segments) != 3 {
		t.Errorf("kept out: %+v %+v", d.Segments[3], d.DialogueStretches)
	}
	d = api.DubJSON{}
	e.must(t, "edit_dub", map[string]any{"dub_id": id, "dialogue": "off"}, &d)
	if d.Dialogue != dub.DialogueOff || d.DialogueBlocked != dub.DialogueWhyOff || len(d.DialogueStretches) != 0 {
		t.Errorf("off: %q %q %v", d.Dialogue, d.DialogueBlocked, d.DialogueStretches)
	}
	e.fails(t, "edit_dub", map[string]any{"dub_id": id, "dialogue": "maybe"}, api.CodeInvalid)
	// Another language: line by line, and why.
	var es api.DubJSON
	e.must(t, "create_dub", map[string]any{"path": src, "target_languages": []string{"ja"}, "through": "none"}, &es)
	if es.Dialogue != dub.DialogueAuto || es.DialogueBlocked != dub.DialogueWhySpeakers { // not transcribed yet
		t.Errorf("ja: %q %q", es.Dialogue, es.DialogueBlocked)
	}
	// The batch queue: off for its dubs.
	var b api.BatchJSON
	e.must(t, "batch_dub", map[string]any{"paths": []string{src}, "target_languages": []string{"zh-Hans"}, "dialogue": "off"}, &b)
	if len(b.Added) != 1 {
		t.Fatalf("batch %+v", b)
	}
	p, err := e.a.dubSvc.Get(context.Background(), b.Added[0].ID)
	if err != nil || p.Settings.Dialogue != dub.DialogueOff {
		t.Errorf("batch dub's dialogue %q (%v)", p.Settings.Dialogue, err)
	}
	e.wait(t, b.Added[0].JobID)
	e.fails(t, "batch_dub", map[string]any{"paths": []string{src}, "dialogue": "x"}, api.CodeInvalid)
}
