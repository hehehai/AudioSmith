package main

// Tests of the gallery, take export, history and workflow modules for
// agents (api_studio_more.go). The harness is api_studio_test.go.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/gallery"
	"github.com/hehehai/AudioSmith/internal/workflow"
)

// TestStudioGallery: search_voices gives what the page's filters give;
// one voice by id; "Use voice" saves it once.
func TestStudioGallery(t *testing.T) {
	e := newStudioEnv(t)
	for _, f := range []map[string]string{
		{"query": "warm", "gender": "female"},
		{"category": "narration", "language": "en", "age": "elderly"},
		{"category": "announcing"},
		{"category": "news"}, // an earlier gallery category: mapped to its use case
		{"query": "旁白"},
	} {
		var page api.GalleryPage
		args := map[string]any{"limit": 100}
		for k, v := range f {
			args[k] = v
		}
		e.must(t, "search_voices", args, &page)
		// The page's query for the same filters.
		g := galleryFilterQuery(f["query"], f["category"], map[gallery.Facet]string{gallery.FacetGender: f["gender"], gallery.FacetAge: f["age"],
			gallery.FacetLanguage: f["language"]})
		g.Limit = 100
		ui := gallery.Default().Search(g)
		if page.Total != ui.Total || len(page.Items) != len(ui.Items) || page.Total == 0 {
			t.Fatalf("%v: %d/%d, the page %d/%d", f, len(page.Items), page.Total, len(ui.Items), ui.Total)
		}
		for i := range ui.Items {
			if page.Items[i].ID != ui.Items[i].ID {
				t.Fatalf("%v: item %d is %s, the page's %s", f, i, page.Items[i].ID, ui.Items[i].ID)
			}
		}
		if len(page.Facets["gender"]) == 0 {
			t.Errorf("no facets")
		}
	}
	// The page's own state gives the same.
	var page api.GalleryPage
	e.must(t, "search_voices", map[string]any{"category": "narration", "gender": "male", "limit": 60}, &page)
	done := make(chan gallery.Page)
	e.a.postQueue(func() {
		g := &e.a.gallery
		g.tab = slices.IndexFunc(gallery.Categories(), func(c gallery.Category) bool { return c.ID == "narration" }) + 1
		g.filters[gallery.FacetGender] = "male"
		g.limit = 60
		done <- e.a.galleryResults()
	})
	if ui := <-done; ui.Total != page.Total || len(ui.Items) != len(page.Items) || ui.Items[0].ID != page.Items[0].ID {
		t.Fatalf("page state %d, search_voices %d", ui.Total, page.Total)
	}

	id := page.Items[0].ID
	e.must(t, "search_voices", map[string]any{"id": id}, &page)
	if len(page.Items) != 1 || page.Items[0].ID != id || page.Items[0].Sample == "" {
		t.Fatalf("by id %+v", page)
	}
	e.fails(t, "search_voices", map[string]any{"id": "nope"}, api.CodeNotFound)
	e.fails(t, "search_voices", map[string]any{"category": "nope"}, api.CodeInvalid)
	var use api.GalleryUse
	e.must(t, "use_gallery_voice", map[string]any{"id": id}, &use)
	if !use.Created || use.Voice.Kind != "designed" || use.Voice.Origin != "gallery:"+id || use.Model == "" || len(use.Models) == 0 || !use.Downloaded {
		t.Fatalf("use %+v", use)
	}
	e.must(t, "use_gallery_voice", map[string]any{"id": id}, &use)
	if use.Created {
		t.Fatal("saved twice")
	}
	e.must(t, "search_voices", map[string]any{"id": id}, &page)
	if page.Items[0].Saved != use.Voice.ID {
		t.Errorf("saved id %+v", page.Items[0])
	}
	e.fails(t, "use_gallery_voice", map[string]any{"id": "nope"}, api.CodeNotFound)
	// A curated designed voice comes with its reference; an earlier
	// gallery's id still resolves and saves its own recipe (no reference
	// yet: it is made on first use).
	if use.Voice.RefText == "" || use.Voice.Design == "" {
		t.Errorf("curated voice without its reference or persona: %+v", use.Voice)
	}
	var oldPage api.GalleryPage
	e.must(t, "search_voices", map[string]any{"id": "f-grandma-osmanthus"}, &oldPage)
	if len(oldPage.Items) != 1 || oldPage.Items[0].Kind != "" || oldPage.Items[0].Persona != "" {
		t.Fatalf("old id %+v", oldPage)
	}
	var old api.GalleryUse
	e.must(t, "use_gallery_voice", map[string]any{"id": "f-grandma-osmanthus"}, &old)
	if !old.Created || old.Voice.Kind != "designed" || old.Voice.RefText != "" || old.Voice.Origin != "gallery:f-grandma-osmanthus" {
		t.Errorf("old use %+v", old)
	}
	if status, body := e.rest(t, "GET", "/api/gallery?gender=female&limit=3", nil); status != 200 || !strings.Contains(string(body), `"facets"`) {
		t.Fatalf("GET /api/gallery %d %s", status, body)
	}
	if status, _ := e.rest(t, "GET", "/api/gallery/"+id, nil); status != 200 {
		t.Fatalf("GET /api/gallery/{id} %d", status)
	}
	if status, _ := e.rest(t, "GET", "/api/gallery/nope", nil); status != 404 {
		t.Fatalf("GET missing %d", status)
	}
	if status, body := e.rest(t, "POST", "/api/gallery/"+id+"/use", nil); status != 200 || !strings.Contains(string(body), use.Voice.ID) {
		t.Fatalf("POST use %d %s", status, body)
	}
}

// TestStudioExportsAndHistory: a take exported with the dialog's
// options (format, rate, channels, loudness, trim, extras), an M4A
// through the engine's encoder, MP3 refused without ffmpeg; a transcript
// as SRT; the History's takes with their files.
func TestStudioExportsAndHistory(t *testing.T) {
	e := newStudioEnv(t)
	take := core.Take{ID: core.NewID(time.Now()), Text: "Welcome to the show. This is AudioSmith.", Model: kokoro, Origin: core.TakeAPI, CreatedAt: time.Now(),
		Render: &core.TakeRender{Chunks: []core.TakeChunk{{Text: "Welcome to the show.", Start: 0, End: 0.9}, {Text: "This is AudioSmith.", Start: 0.9, End: 1.8}}}}
	take.File = take.ID + ".wav"
	writeTestWAV(t, e.lib.svc.Takes.AudioPath(take), 24000, 2)
	if _, err := e.lib.svc.Takes.Add(context.Background(), take); err != nil {
		t.Fatal(err)
	}
	var x api.ExportJSON
	e.must(t, "export_audio", map[string]any{"take_id": take.ID, "bits": 16, "sample_rate": 48000, "channels": 2, "loudness": "podcast",
		"trim": true, "script": true, "subtitles": true, "filename": "show"}, &x)
	if len(x.Files) != 3 || filepath.Base(x.Files[0]) != "show.wav" || x.Loudness > -15 || x.Loudness < -17 {
		t.Fatalf("export %+v", x)
	}
	if _, info, err := wav.ReadFile(x.Files[0]); err != nil || info.Encoding.Bits() != 16 || info.Rate != 48000 || info.Channels != 2 {
		t.Fatalf("wav %v %+v", err, info)
	}
	if srt, _ := os.ReadFile(filepath.Join(e.out, "show.srt")); !strings.Contains(string(srt), "Welcome to the show.") {
		t.Fatalf("srt %s", srt)
	}
	e.must(t, "export_audio", map[string]any{"take_id": take.ID, "format": "m4a", "bitrate_kbps": 192, "title": "Show"}, &x)
	data, _ := os.ReadFile(x.Files[0])
	if !bytes.HasPrefix(data, []byte("FAKE-M4A")) || !strings.Contains(string(data), `"bitrate":192000`) || !strings.Contains(string(data), `"Show"`) {
		t.Fatalf("m4a %s", data)
	}
	e.fails(t, "export_audio", map[string]any{"take_id": take.ID, "format": "mp3"}, api.CodeUnsupportedFormat)
	e.fails(t, "export_audio", map[string]any{"take_id": take.ID, "bits": 12}, api.CodeInvalid)
	e.fails(t, "export_audio", map[string]any{"take_id": "missing"}, api.CodeNotFound)
	if status, body := e.rest(t, "POST", "/api/takes/"+take.ID+"/export", map[string]any{"format": "wav", "bits": 24}); status != 200 || !strings.Contains(string(body), ".wav") {
		t.Fatalf("POST export %d %s", status, body)
	}

	// The History.
	var raw struct {
		Takes []map[string]any `json:"takes"`
	}
	e.must(t, "list_takes", map[string]any{"search": "show"}, &raw)
	if len(raw.Takes) != 1 || raw.Takes[0]["file"] != e.lib.svc.Takes.AudioPath(take) || raw.Takes[0]["model"] != catalog.Builtin().Entries[0].ID {
		t.Fatalf("takes %+v", raw)
	}
	e.must(t, "list_takes", map[string]any{"take_id": take.ID}, &raw)
	if len(raw.Takes) != 1 {
		t.Fatal("by id")
	}

	// A transcript as SRT.
	tr := core.Transcript{ID: core.NewID(time.Now()), Title: "meeting.m4a (API)", Language: "en", Duration: 2,
		Segments: []core.TranscriptSegment{{Start: 0, End: 1.5, Text: "Hello from the meeting."}}}
	tr.CreatedAt, tr.UpdatedAt = time.Now(), time.Now()
	if err := e.lib.store.Transcripts().Put(context.Background(), tr); err != nil {
		t.Fatal(err)
	}
	e.must(t, "export_transcript", map[string]any{"transcript_id": tr.ID, "format": "srt"}, &x)
	if len(x.Files) != 1 || filepath.Base(x.Files[0]) != "meeting.srt" {
		t.Fatalf("transcript %+v", x)
	}
	if data, _ := os.ReadFile(x.Files[0]); !strings.Contains(string(data), "00:00:00,000 --> ") {
		t.Fatalf("srt %s", data)
	}
	e.fails(t, "export_transcript", map[string]any{"transcript_id": tr.ID, "format": "doc"}, api.CodeInvalid)
	if status, body := e.rest(t, "POST", "/api/transcripts/"+tr.ID+"/export", map[string]any{"format": "vtt", "filename": "m"}); status != 200 || !strings.Contains(string(body), "m.vtt") {
		t.Fatalf("POST transcript export %d %s", status, body)
	}
	var items struct{ Items []api.ProjectJSON }
	e.must(t, "list_projects", map[string]any{"category": "export"}, &items)
	if len(items.Items) < 3 {
		t.Errorf("exports listed: %d", len(items.Items))
	}
}

// TestStudioWorkflows: a saved workflow listed with its inputs and run
// with new scripts as a job, to its files; errors.
func TestStudioWorkflows(t *testing.T) {
	e := newStudioEnv(t)
	tpl, _ := workflow.TemplateByID("narration")
	w := tpl.Make("", "Lessons", time.Now(), workflow.TemplateOptions{Model: kokoro})
	saved, err := e.a.wfSvc.Save(context.Background(), w)
	if err != nil {
		t.Fatal(err)
	}
	var list struct{ Workflows []api.WorkflowJSON }
	e.must(t, "list_workflows", nil, &list)
	if len(list.Workflows) != 1 || list.Workflows[0].ID != saved.ID || list.Workflows[0].Input != "scripts" || len(list.Workflows[0].Scripts) != 1 {
		t.Fatalf("workflows %+v", list)
	}
	var j jobAnswer
	e.must(t, "run_workflow", map[string]any{"workflow_id": saved.ID, "scripts": []string{"Lesson one.", "Lesson two."}}, &j)
	if j.JobID != "workflow:"+saved.ID {
		t.Fatalf("run %+v", j)
	}
	j = e.wait(t, j.JobID)
	if j.State != "succeeded" || len(j.Files) != 2 {
		t.Fatalf("ran %+v", j)
	}
	var res struct {
		Items []struct {
			Name, State string
		} `json:"items"`
	}
	json.Unmarshal(j.Result, &res)
	if len(res.Items) != 2 || res.Items[0].State != "done" {
		t.Fatalf("result %s", j.Result)
	}
	// One-off inputs: the saved workflow keeps its own.
	after, run, err := e.a.wfSvc.Get(context.Background(), saved.ID)
	if err != nil || len(after.InputNode().P.Scripts) != 1 || after.InputNode().P.Scripts[0].Text != saved.InputNode().P.Scripts[0].Text ||
		!after.UpdatedAt.Equal(saved.UpdatedAt) || run == nil || len(run.Items) != 2 {
		t.Fatalf("saved inputs changed: %v %+v", err, after.InputNode().P.Scripts)
	}
	e.fails(t, "run_workflow", map[string]any{"workflow_id": saved.ID, "scripts": []string{"x"}, "files": []string{"/a.wav"}}, api.CodeInvalid)
	e.fails(t, "run_workflow", map[string]any{"workflow_id": saved.ID, "files": []string{filepath.Join(t.TempDir(), "no-such.wav")}}, api.CodeNotFound)
	e.fails(t, "run_workflow", map[string]any{"workflow_id": "missing"}, api.CodeNotFound)
	if status, body := e.rest(t, "POST", "/api/workflows/"+saved.ID+"/run", map[string]any{"again": true}); status != 202 {
		t.Fatalf("POST run %d %s", status, body)
	}
	e.wait(t, "workflow:"+saved.ID)
	if status, body := e.rest(t, "GET", "/api/workflows", nil); status != 200 || !strings.Contains(string(body), "Lessons") || strings.Contains(string(body), "Lesson two.") {
		t.Fatalf("GET %d %s", status, body)
	}
	// Group job events end with "done".
	resp, err := http.Get(e.ts.URL + "/api/jobs/workflow:" + saved.ID + "/events")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(data), "event: done") {
		t.Fatalf("events %s", data)
	}
}
