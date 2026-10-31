package main

// Behaviour tests of 0.5 F6: the samples (a bundled sample is copied and
// opened, opening it again asks), Stories' 「帮我写」, the features'
// introductions, the Developers page, Settings → Integrations and the
// isolations' file names.

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/hehehai/AudioSmith/assets"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/llm"
	"github.com/hehehai/AudioSmith/internal/services"
)

// fixtureSamples are samples built for the test (not shipped): a story
// from a script with its audio, a dub with its video, a meeting
// transcript with its audio.
func fixtureSamples(t *testing.T) fstest.MapFS {
	t.Helper()
	man := func(id, kind, en, zh string, files ...sampleFile) *fstest.MapFile {
		b, _ := json.Marshal(sampleManifest{Version: 1, ID: id, Kind: kind, Title: map[string]string{"en": en, "zh-Hans": zh},
			Subtitle: map[string]string{"en": en + " (sub)", "zh-Hans": zh}, Files: files})
		return &fstest.MapFile{Data: b}
	}
	d := dub.Project{Title: "Clip", Source: dub.Source{Kind: dub.SourceFile, Path: "media/clip.wav", Name: "clip.wav"}, Targets: []string{"zh-Hans"}}
	db, _ := json.Marshal(d)
	tr := sampleTranscript("x", time.Now())
	tr.Title, tr.Audio = "Weekly meeting", "media/meeting.wav"
	tb, _ := json.Marshal(tr)
	return fstest.MapFS{
		"story/sample.json": man("story", sampleStory, "A story for two voices", "两人对话的故事",
			sampleFile{"script.txt", roleScript}, sampleFile{"audio/story.m4a", roleAudio}),
		"story/script.txt":      {Data: []byte("# The Lighthouse\nThe keeper climbed the stairs.\n[voice:Mara] Is the lamp lit?\n[voice:Jun] Not yet. The oil is cold.\n[voice:default] Night came early.\n")},
		"story/audio/story.m4a": {Data: []byte("m4a bytes")},
		"dub/sample.json": man("dub", sampleDub, "A dubbed clip", "一段配音短片",
			sampleFile{"project.json", roleProject}, sampleFile{"media/clip.wav", roleMedia}),
		"dub/project.json":   {Data: db},
		"dub/media/clip.wav": {Data: []byte("RIFF video")},
		"meeting/sample.json": man("meeting", sampleTranscriptKind, "A meeting transcript", "一段会议转写",
			sampleFile{"project.json", roleProject}, sampleFile{"media/meeting.wav", roleMedia}),
		"meeting/project.json":      {Data: tb},
		"meeting/media/meeting.wav": {Data: []byte("RIFF audio")},
		"broken/sample.json":        {Data: []byte(`{"version":1,"id":"other","kind":"story"}`)},
	}
}

// useSamples reads the samples from fsys for the test.
func useSamples(t *testing.T, fsys fs.FS) {
	saved := samplesFS
	samplesFS, samplesRead = fsys, nil
	t.Cleanup(func() { samplesFS, samplesRead = saved, nil })
}

// Opening a sample copies it into the library and opens it; opening it
// again asks: the copy, or a new one.
func TestSampleCopyOpen(t *testing.T) {
	useSamples(t, fixtureSamples(t))
	a, _, tt := newTestApp(t)
	tt.SetSize(1200, 900)
	a.navigate("/projects")
	tt.Frame()
	if tt.HasText("Coming soon") { // the dub and the meeting are bundled now
		t.Fatalf("soon: %q", tt.Texts())
	}
	if err := tt.Click("A story for two voices"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	p := a.lfStories
	ref := a.prefs.Setup.Samples["story"]
	if p.project == "" || ref != "story:"+p.project || a.router.Path() != "/stories" || p.doc.Title != "The Lighthouse" {
		t.Fatalf("story: %q ref %q on %s title %q", p.project, ref, a.router.Path(), p.doc.Title)
	}
	if len(p.doc.Cast) != 2 || len(p.doc.Lines) != 4 || p.doc.Lines[0].Character != "" || p.doc.Lines[3].Character != "" {
		t.Fatalf("cast %+v lines %+v", p.doc.Cast, p.doc.Lines)
	}
	ex, _ := a.lib.svc.Library.Exports(context.Background(), "", 10)
	if len(ex) != 1 || ex[0].ProjectID != p.project || ex[0].Kind != "story" || !strings.HasPrefix(ex[0].Path, filepath.Join(a.dataDir(), "samples")) {
		t.Fatalf("exports %+v", ex)
	}
	if b, err := os.ReadFile(ex[0].Path); err != nil || string(b) != "m4a bytes" {
		t.Fatalf("audio %q %v", b, err)
	}
	first := p.project

	// Again: it asks. "Open my copy" opens the same project.
	a.navigate("/projects")
	tt.Frame()
	if err := tt.Click("A story for two voices"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.sampleAskOpen || !tt.HasText("You opened this sample before") {
		t.Fatalf("no question: %q", tt.Texts())
	}
	if err := tt.Click("Open my copy"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if p.project != first || a.router.Path() != "/stories" {
		t.Fatalf("opened %q", p.project)
	}
	// "Make a new copy" makes a second project.
	a.openSample("story")
	tt.Frame()
	if err := tt.Click("Make a new copy"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	second := p.project
	if second == first || a.prefs.Setup.Samples["story"] != "story:"+second {
		t.Fatalf("new copy %q (first %q)", second, first)
	}
	// The copy is a normal project: deleted, the sample no longer asks.
	a.lfClose(p)
	if err := a.lib.svc.Projects.Delete(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	a.openSample("story")
	tt.Frame()
	if a.sampleAskOpen || p.project == second || p.project == "" {
		t.Fatalf("after delete: ask %v project %q", a.sampleAskOpen, p.project)
	}

	// The dub: its video in the dub's folder, the project opened.
	a.openSample("dub")
	tt.Frame()
	dref := a.prefs.Setup.Samples["dub"]
	kind, id, _ := strings.Cut(dref, ":")
	if kind != sampleDub || a.router.Path() != "/dub/"+id {
		t.Fatalf("dub %q on %s", dref, a.router.Path())
	}
	dp, err := a.dubSvc.Get(context.Background(), id)
	if err != nil || dp.Source.Path != filepath.Join(a.dubSvc.Dir(id), "media", "clip.wav") || dp.Title != "Clip" {
		t.Fatalf("dub project %+v %v", dp, err)
	}
	if _, err := os.Stat(dp.Source.Path); err != nil {
		t.Fatal(err)
	}
	// The meeting: a transcript with its audio.
	a.openSample("meeting")
	tt.Frame()
	_, tid, _ := strings.Cut(a.prefs.Setup.Samples["meeting"], ":")
	tr, err := a.trSvc.Get(context.Background(), tid)
	if err != nil || tr.Title != "Weekly meeting" || len(tr.Segments) != 2 || !strings.HasPrefix(a.router.Path(), "/transcripts") {
		t.Fatalf("transcript %+v %v on %s", tr, err, a.router.Path())
	}
	if b, err := os.ReadFile(a.trSvc.AudioPath(tr)); err != nil || string(b) != "RIFF audio" {
		t.Fatalf("transcript audio %q %v", b, err)
	}
	// ⌘K finds the samples.
	a.paletteQuery = "meeting"
	found := false
	for _, r := range a.paletteRows() {
		found = found || r.key == "s-meeting"
	}
	if !found {
		t.Error("⌘K: no sample row")
	}
}

// The built-in fallback (no sample bundled) asks the second time too.
func TestSampleFallbackAsks(t *testing.T) {
	useSamples(t, fstest.MapFS{})
	a, _, tt := newTestApp(t)
	a.openSample("chapter")
	tt.Frame()
	b := a.lfBook
	if b.project == "" || a.prefs.Setup.Samples["chapter"] != "audiobook:"+b.project {
		t.Fatalf("chapter %q %v", b.project, a.prefs.Setup.Samples)
	}
	a.openSample("chapter")
	tt.Frame()
	if !a.sampleAskOpen {
		t.Fatal("no question")
	}
}

// The bundled samples are valid and within their budget.
func TestBundledSamples(t *testing.T) {
	root, _ := fs.Sub(assets.Samples, "samples")
	entries, err := fs.ReadDir(root, ".")
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m, size, err := readSample(root, e.Name())
		if err != nil {
			t.Errorf("%s: %v", e.Name(), err)
		}
		if _, ok := map[string]bool{"story": true, "chapter": true, "narration": true, "dub": true, "meeting": true}[m.ID]; !ok {
			t.Errorf("%s: not one of the five cards", e.Name())
		}
		total += size
	}
	if total > sampleBudget {
		t.Errorf("samples weigh %d bytes (budget %d)", total, sampleBudget)
	}
	// The fixture's broken sample is left out.
	useSamples(t, fixtureSamples(t))
	if _, ok := (&app{}).samples()["other"]; ok {
		t.Error("broken sample read")
	}
}

// 「帮我写」 turns a description into a cast story; Cancel makes nothing.
func TestStoryHelpMeWrite(t *testing.T) {
	a, _, tt := newTestApp(t)
	var asked []llm.Request
	draft := "```\n# 灯塔\n守塔人爬上楼梯。\n[voice:玛拉] 灯点亮了吗？\n[voice:阿俊] 还没有，油是冷的。\n```"
	f := &fakeLLM{local: true, answer: func(r llm.Request) (string, error) { asked = append(asked, r); return draft, nil }}
	e, _ := catalog.Builtin().ByID(catalog.DefaultLLM)
	a.setLocal(e, hub.Local{State: hub.Downloaded, Snapshot: t.TempDir()}, nil) // the writing help's model is here
	useFakeLLM(a, f)
	a.navigate("/stories")
	tt.Frame()
	if !a.storyWriteReady() || !tt.HasText("Help me write") {
		t.Fatalf("no toggle: ready %v %q", a.storyWriteReady(), tt.Texts())
	}
	p := a.lfStories
	if err := tt.Click("Help me write"); err != nil {
		t.Fatal(err)
	}
	p.entryText = "灯塔里两个守塔人的夜晚"
	tt.Frame()
	if err := tt.Click("New story"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(asked) != 1 || !strings.Contains(asked[0].Messages[0].Content, "[voice:Name]") || !strings.Contains(asked[0].Messages[0].Content, "no filler words") ||
		asked[0].Messages[1].Content != "灯塔里两个守塔人的夜晚" {
		t.Fatalf("asked %+v", asked)
	}
	if p.project == "" || p.doc.Title != "灯塔" || len(p.doc.Cast) != 2 || len(p.doc.Lines) != 3 || p.writing || p.entryText != "" {
		t.Fatalf("story %q %q cast %+v lines %d", p.project, p.doc.Title, p.doc.Cast, len(p.doc.Lines))
	}
	if p.doc.Cast[0].Name != "玛拉" || p.doc.Lines[1].Character != p.doc.Cast[0].ID {
		t.Fatalf("cast %+v lines %+v", p.doc.Cast, p.doc.Lines)
	}

	// Cancel while it writes: the description stays, nothing is made.
	a.lfClose(p)
	tt.Frame()
	p.entryText = "雨夜的车站"
	f.answer = func(llm.Request) (string, error) {
		if !p.writing {
			t.Error("not writing")
		}
		a.storyWriteCancel(p) // the progress line's Cancel
		return "", context.Canceled
	}
	before := len(a.projItems(projStory, ""))
	a.lfSubmit(p)
	tt.Frame()
	if p.writing || p.project != "" || a.lfEditing(p) || p.entryText != "雨夜的车站" || len(a.projItems(projStory, "")) != before {
		t.Fatalf("cancelled: writing %v project %q text %q", p.writing, p.project, p.entryText)
	}
	// A draft without voices is an error, and no story.
	f.answer = func(llm.Request) (string, error) { return "Sorry, I can't.", nil }
	a.lfSubmit(p)
	tt.Frame()
	if p.project != "" || !tt.HasText("The writing help could not write a draft: the draft had no lines for voices") {
		t.Fatalf("no voices: %q %q", p.project, tt.Texts())
	}
}

// A feature's introduction shows on its first visit only, and again
// after Settings → General's reset.
func TestIntroDialogs(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.noIntros = false
	a.navigate("/transcripts")
	tt.Frame()
	if !a.introOpen || a.introID != "transcripts" || !tt.HasText("Turn on speakers to see who said what, with timings.") {
		t.Fatalf("no intro: %v %q", a.introOpen, tt.Texts())
	}
	if err := tt.Click("Get started"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	a.navigate("/studio")
	tt.Frame()
	a.navigate("/transcripts")
	tt.Frame()
	if a.introOpen {
		t.Fatal("shown twice")
	}
	a.navigate("/voices/new/clone")
	tt.Frame()
	if !a.introOpen || a.introID != "voice-new" {
		t.Fatalf("new voice: %v %q", a.introOpen, a.introID)
	}
	a.introOpen = false
	a.navigate("/settings/general")
	tt.Frame()
	if err := tt.Click("Show again"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.prefs.Setup.Intros) != 0 {
		t.Fatalf("intros %v", a.prefs.Setup.Intros)
	}
	a.navigate("/transcripts")
	tt.Frame()
	if !a.introOpen || a.introID != "transcripts" {
		t.Fatal("not shown again after the reset")
	}
}

// Developers: the segments switch the snippet, Copy puts it on the
// clipboard, the status line says when a client last called.
func TestDevelopersConnect(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.SetSize(1200, 900)
	startTestAPI(t, a)
	a.navigate("/developers")
	tt.Frame()
	for _, s := range []string{"Connect your agent", "Coding agent", "MCP", "cURL", "Waiting for the first request…", "API reference", "Install the skill", "Local API settings", "Claude Code"} {
		if !tt.HasText(s) {
			t.Fatalf("no %q: %q", s, tt.Texts())
		}
	}
	copied := func() string {
		if err := tt.Click("Copy"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		return tt.Clipboard()
	}
	if got := copied(); got != agentPrompt("mcp", a.promptEnvOf()) {
		t.Fatalf("agent: %q", got)
	}
	if err := tt.Click("MCP"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := copied(); !strings.Contains(got, `"mcpServers"`) || !strings.Contains(got, a.apiBaseURL()+"/mcp") {
		t.Fatalf("mcp: %q", got)
	}
	if err := tt.Click("cURL"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := copied(); !strings.HasPrefix(got, "curl "+a.apiBaseURL()+"/v1/audio/speech") {
		t.Fatalf("curl: %q", got)
	}
	r, err := http.Get(a.apiBaseURL() + "/v1/voices")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	waitUntil(t, func() bool { return !a.apiLastRequest().IsZero() })
	tt.Frame()
	if !tt.HasText("Last request: Just now") {
		t.Fatalf("status: %q", tt.Texts())
	}
}

// Settings → Integrations points to Developers; old setup paths go there.
func TestSettingsIntegrationsPointsToDevelopers(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.navigate("/settings/integrations")
	tt.Frame()
	if !tt.HasText("Integrations are on the Developers page") || tt.HasText("Apps and agents") {
		t.Fatalf("section: %q", tt.Texts())
	}
	if err := tt.Click("Open Developers"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.settingsOpen || a.router.Path() != "/developers" {
		t.Fatalf("at %s (settings %v)", a.shownPath(), a.settingsOpen)
	}
	for from, to := range map[string]string{"/integrations": "/developers", "/integrations/codex": "/developers/codex",
		"/settings/integrations/mcp": "/developers/mcp", "/settings/api": "/developers/api"} {
		a.navigate(from)
		if a.shownPath() != to {
			t.Errorf("%s → %s, want %s", from, a.shownPath(), to)
		}
	}
}

// An isolation's rows show its file's name, not its path; Projects'
// search finds a story by its script.
func TestIsolationRowsAndScriptSearch(t *testing.T) {
	d := newDubTest(t, 3)
	a, eng, tt := d.a, d.eng, d.tt
	tt.SetSize(1200, 900)
	fakeOnDisk(t, a, isoSeparator())
	fakeOnDisk(t, a, isoDenoiser())
	a.isoEngine = isoFakeEngine(nil)
	eng.executors[services.KindIsolate] = a.isoSvc.Executor()
	src := filepath.Join(t.TempDir(), "采访原声.wav")
	copyTestFile(t, d.src, src)
	a.isoStart(src)
	a.navigate("/projects")
	tt.Frame()
	if !tt.HasText("采访原声.wav") || tt.HasText(src) || tt.HasText(shortPath(src)) {
		t.Fatalf("projects: %q", tt.Texts())
	}
	items := a.projItems(projIsolate, "")
	if len(items) != 1 || items[0].sub != "采访原声.wav" {
		t.Fatalf("items %+v", items)
	}
	doc := services.NewLongformDoc(core.ProjectStory, kokoro)
	doc.Title = "Night"
	doc.Lines = []services.StoryLine{{ID: "l1", Text: "The ferry waits at the harbour."}}
	if _, err := a.lib.svc.Library.SaveDoc(context.Background(), core.Project{Kind: core.ProjectStory, Title: "Night"}, doc); err != nil {
		t.Fatal(err)
	}
	a.projects.dirty = true
	a.projItems("", "")
	got := a.projItems("", "harbour")
	if len(got) != 1 || got[0].title != "Night" {
		t.Fatalf("script search: %+v", got)
	}
}
