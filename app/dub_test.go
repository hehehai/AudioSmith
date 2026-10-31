package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/dub/dubtest"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/llm"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/translate"
)

// dubFakeTranslator prefixes lines with their language.
type dubFakeTranslator struct{ lines int }

func (f *dubFakeTranslator) Info() translate.Info { return translate.Info{ID: "fake", Kind: "llm"} }

func (f *dubFakeTranslator) Translate(_ context.Context, req translate.Request) (translate.Result, error) {
	res := translate.Result{Translator: "fake"}
	for _, s := range req.Segments {
		f.lines++
		res.Segments = append(res.Segments, translate.Translated{ID: s.ID, Text: "[" + req.Target + "] " + s.Text})
	}
	return res, nil
}

// dubTest is an app whose dub pipeline runs at once, inside Submit, on
// dubtest's fake engine.
type dubTest struct {
	a   *app
	eng *fakeEngine
	tt  *ui.Tester
	fe  *dubtest.Engine
	tr  *dubFakeTranslator
	src string
	out string
}

func newDubTest(t *testing.T, lines int) *dubTest {
	t.Helper()
	saved := llmSecrets
	llmSecrets = llm.StaticSecrets{}
	t.Cleanup(func() { llmSecrets = saved })
	a, eng, tt := newTestApp(t)
	completeAux(t, a)
	d := &dubTest{a: a, eng: eng, tt: tt, fe: dubtest.New(), tr: &dubFakeTranslator{}}
	d.fe.Script = []string{"Hello there.", "How are you today?", "I am fine, thank you.", "That is good to hear.", "See you tomorrow.", "Goodbye."}
	a.dubEngine = d.fe.Executor
	a.lib.useEngine(func() jobs.Executor { return d.fe.Executor(proto.MethodTTSSynthesize) })
	for kind, ex := range a.dubSvc.Executors() {
		eng.executors[kind] = ex
	}
	useFakeLLM(a, &fakeLLM{answer: func(llm.Request) (string, error) { return "{}", nil }})
	savedTr := dubMakeTranslator
	dubMakeTranslator = func(*llm.Router, dub.Settings) (translate.Translator, error) { return d.tr, nil }
	t.Cleanup(func() { dubMakeTranslator = savedTr })
	d.out = t.TempDir()
	savedDir := dubExportDir
	dubExportDir = d.out
	t.Cleanup(func() { dubExportDir = savedDir })
	d.src = filepath.Join(t.TempDir(), "Interview.wav")
	if _, err := dubtest.Speech(d.src, lines); err != nil {
		t.Fatal(err)
	}
	// Settings → Dubbing chose Spanish (TestDubDefaultTargetPage: none chosen).
	a.updateSettings(func(s *settings.Settings) { s.Dubbing.Targets = []string{"es"} })
	a.navigate("/dub")
	return d
}

func (d *dubTest) click(t *testing.T, label string) {
	t.Helper()
	d.tt.Frame()
	if err := d.tt.Click(label); err != nil {
		t.Fatalf("click %q: %v (texts %q)", label, err, d.tt.Texts())
	}
	d.tt.Frame()
}

func TestDubPageEndToEnd(t *testing.T) {
	d := newDubTest(t, 6)
	a, tt := d.a, d.tt
	tt.Frame()
	if !tt.HasText("Drop a video or audio file here") || !tt.HasText("No dubs yet") {
		t.Fatalf("empty state: %q", tt.Texts())
	}
	a.dubSourceFile(d.src)
	tt.Frame()
	if a.dub == nil || a.dub.Source.Name != "Interview.wav" || !a.dub.Settings.Review {
		t.Fatalf("project %+v", a.dub)
	}
	// Review mode: the first run stops after the transcript.
	a.dubStart()
	tt.Frame()
	p := a.dub
	// The catalogue's diarization model (dubtest's fake) finds the two
	// speakers of dubtest.Speech.
	if len(p.Segments) != 6 || len(p.References) != 2 || len(p.Tracks) != 0 {
		t.Fatalf("after transcribing: %d segments, %d references, tracks %v", len(p.Segments), len(p.References), p.Tracks)
	}
	if !tt.HasText("Hello there.") || !tt.HasText("Translate →") {
		t.Errorf("transcript and checkpoint: %q", tt.Texts())
	}
	if a.dubStep != dubStepTranscribe {
		t.Errorf("step %d", a.dubStep)
	}
	// Edits with undo.
	first := p.Segments[0].ID
	a.dubCur = first
	tt.Key(ui.Cmd, ui.KeyD)
	tt.Frame()
	if len(a.dub.Segments) != 6 {
		// "Hello there." has no boundary to split at but the middle.
		t.Logf("split made %d segments", len(a.dub.Segments))
	}
	a.dubCur = a.dub.Segments[1].ID
	tt.Key(ui.Cmd, ui.KeyM)
	tt.Frame()
	n := len(a.dub.Segments)
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if len(a.dub.Segments) != n+1 {
		t.Errorf("undo merge: %d → %d", n, len(a.dub.Segments))
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	tt.Frame()
	if len(a.dub.Segments) != n {
		t.Errorf("redo: %d", len(a.dub.Segments))
	}
	// The saved project has the edits.
	saved, err := a.dubSvc.Get(context.Background(), a.dub.ID)
	if err != nil || len(saved.Segments) != n {
		t.Fatalf("saved %d segments, %v", len(saved.Segments), err)
	}
	// Translate.
	d.click(t, "Translate →")
	if !a.dub.AllTranslated() || d.tr.lines == 0 {
		t.Fatalf("translated? %v", a.dub.AllTranslated())
	}
	if !tt.HasText("[es] Hello there.") && !slices.ContainsFunc(tt.Texts(), func(s string) bool { return strings.HasPrefix(s, "[es]") }) {
		t.Errorf("translations not shown: %q", tt.Texts())
	}
	// Edit a translation inline.
	id := a.dub.Segments[0].ID
	a.dubBeginEdit(id, "es")
	a.dubBuf = "Hola, ¿qué tal?"
	a.dubCommitEdit()
	if got := a.dub.TextFor(a.dub.Segments[0], "es"); got != "Hola, ¿qué tal?" {
		t.Errorf("edited %q", got)
	}
	// Generate.
	d.click(t, "Generate dub →")
	p = a.dub
	if !p.TracksReady() || p.Track("es").File == "" {
		t.Fatalf("tracks %+v", p.Tracks)
	}
	if a.dubStaleCount("es") != 0 {
		t.Errorf("stale %d", a.dubStaleCount("es"))
	}
	tt.Frame()
	if !tt.HasText("Dub · 🇪🇸 Spanish") && !slices.ContainsFunc(tt.Texts(), func(s string) bool { return strings.HasPrefix(s, "Dub · ") }) {
		t.Errorf("A/B track missing: %q", tt.Texts())
	}
	// A/B: switching to the dub keeps the playhead.
	a.dubSeek(3)
	var dubLabel string
	for _, x := range tt.Texts() {
		if strings.HasPrefix(x, "Dub · ") {
			dubLabel = x
			break
		}
	}
	if err := tt.Click(dubLabel); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.dubPlayer.track != "es" {
		t.Errorf("track %q", a.dubPlayer.track)
	}
	a.dubTogglePlay()
	tt.Frame()
	if a.dubPlayer.p == nil || a.dubPosition() < 2.9 {
		t.Errorf("player %v at %.2f", a.dubPlayer.p != nil, a.dubPosition())
	}
	a.dubStopPlayer()
	// A changed line is counted and regenerates alone.
	a.dubBeginEdit(id, "es")
	a.dubBuf = "Hola."
	a.dubCommitEdit()
	if a.dubStaleCount("es") != 1 {
		t.Errorf("stale after edit %d", a.dubStaleCount("es"))
	}
	before := d.fe.Count(proto.MethodTTSSynthesize)
	a.dubGenerate()
	if got := d.fe.Count(proto.MethodTTSSynthesize) - before; got != 1 {
		t.Errorf("regenerated %d lines", got)
	}
	// Export WAV and SRT.
	a.dubStep = dubStepExport
	tt.Frame()
	a.dubExport.format = dub.ExportWAV
	a.dubExport.lang = "es"
	a.dubDoExport()
	if _, err := os.Stat(filepath.Join(d.out, "Interview_es.wav")); err != nil {
		t.Errorf("wav export: %v (%q)", err, a.dubExport.err)
	}
	a.dubExport.format = dub.ExportSRT
	a.dubDoExport()
	srt, err := os.ReadFile(filepath.Join(d.out, "Interview_es.srt"))
	if err != nil || !strings.Contains(string(srt), "Hola.") {
		t.Errorf("srt %q %v", srt, err)
	}
	// Create Story.
	a.dubAskStory()
	if a.currentPage() != "stories" || len(a.lfStories.doc.Lines) == 0 || len(a.lfStories.doc.Cast) != 2 {
		t.Errorf("story: page %s, %d lines, cast %+v", a.currentPage(), len(a.lfStories.doc.Lines), a.lfStories.doc.Cast)
	}
}

func TestDubPastGlossaryCastAndBulk(t *testing.T) {
	d := newDubTest(t, 3)
	a, tt := d.a, d.tt
	a.updateSettings(func(s *settings.Settings) { s.Dubbing.NoReview = true })
	a.dubSourceFile(d.src)
	a.dubSet(func(p *dub.Project) { p.Settings.Review = true })
	a.dubStart()
	tt.Frame()
	p := a.dub
	if len(p.Segments) != 3 {
		t.Fatalf("%d segments", len(p.Segments))
	}
	a.dubLang = "es"
	// Paste a numbered translation.
	a.dubPaste.open = true
	a.dubPaste.text = "1. Hola.\n2. ¿Qué tal?\n3. Bien.\n"
	tt.Frame()
	if !tt.HasText("Numbered lines") || !tt.HasText("3 of 3 segments matched") {
		t.Errorf("paste preview: %q", tt.Texts())
	}
	d.click(t, "Apply to 3 segments")
	if a.dub.TextFor(a.dub.Segments[1], "es") != "¿Qué tal?" || !a.dub.AllTranslated() {
		t.Errorf("pasted %+v", a.dub.Segments[1].Lines)
	}
	// Glossary.
	a.dubGloss.open = true
	a.dubGloss.src, a.dubGloss.tgt = "AudioSmith", "AudioSmith"
	tt.Frame()
	d.click(t, "Add term")
	if len(a.dub.Glossary) != 1 {
		t.Errorf("glossary %+v", a.dub.Glossary)
	}
	// Bulk: voice and delete.
	a.dubSel = map[string]bool{a.dub.Segments[0].ID: true, a.dub.Segments[2].ID: true}
	a.dubBulkVoice(dub.VoiceDefault)
	if a.dub.Segments[0].Voice != dub.VoiceDefault || a.dub.Segments[1].Voice != "" {
		t.Errorf("bulk voice %+v", a.dub.Segments)
	}
	tt.Frame()
	if !tt.HasText("2 selected") {
		t.Errorf("bulk bar: %q", tt.Texts())
	}
	// Cast: a preset for S1.
	a.dubStep = dubStepCast
	tt.Frame()
	a.dubCast("S1", dub.PresetPrefix+"narrator")
	if a.dub.Cast["S1"] != dub.PresetPrefix+"narrator" {
		t.Errorf("cast %+v", a.dub.Cast)
	}
	a.dubBulkDelete()
	if len(a.dub.Segments) != 1 {
		t.Errorf("bulk delete left %d", len(a.dub.Segments))
	}
	// Every panel builds.
	for step := dubStepSource; step <= dubStepExport; step++ {
		a.dubStep = step
		tt.Frame()
	}
}

func TestDubErrorsAndURL(t *testing.T) {
	d := newDubTest(t, 3)
	a, tt := d.a, d.tt
	// URL import is off until yt-dlp is installed.
	a.dubURL = "https://www.youtube.com/watch?v=abc"
	a.dubImportURL()
	if a.dubErrCode != dub.CodeNoYtDlp {
		t.Errorf("url without yt-dlp: %q", a.dubErr)
	}
	a.dubURL = "http://192.168.1.2/x.mp4"
	a.dubImportURL()
	if !strings.Contains(a.dubErr, "local network") {
		t.Errorf("lan url: %q", a.dubErr)
	}
	// No translator: Translate explains.
	dubMakeTranslator = dubTranslator
	a.updateSettings(func(s *settings.Settings) { s.LLM.Manual, s.LLM.Default = true, nil })
	a.applyLLM()
	a.dubSourceFile(d.src)
	a.dubStart()
	tt.Frame()
	a.dubTranslate(false)
	if a.dubErrCode != services.DubCodeNoTranslator {
		t.Errorf("no translator: %q %q", a.dubErrCode, a.dubErr)
	}
	tt.Frame()
	if !tt.HasText("Set up writing help") {
		t.Errorf("no setup link: %q", tt.Texts())
	}
	// A stage failure shows with its fix.
	d.fe.Fail[proto.MethodSTTTranscribe] = proto.CodeModelNotDownloaded
	a.dubErr = ""
	a.dubRun(services.RunRequest{Through: dub.StageReferences, Force: []dub.Stage{dub.StageTranscribe}})
	tt.Frame()
	if a.dubErr == "" {
		t.Error("the failure is not shown")
	}
}

func TestTimelineDragSnapAndKeys(t *testing.T) {
	s := &timelineState{zoom: 1, width: 1000}
	m := tlModel{Duration: 10, Onsets: []float64{2.02, 5.5}, Playhead: -1,
		Blocks: []tlBlock{{ID: "a", Start: 1, End: 2}, {ID: "b", Start: 3, End: 4}}}
	var changed [][3]float64
	var ids []string
	ev := tlEvents{Change: func(id string, st, en float64, clamp bool) {
		ids = append(ids, id)
		changed = append(changed, [3]float64{st, en, 0})
	}, Select: func(string) {}, Seek: func(float64) {}}
	// Drag block b by +0.5 s (50 px): no snap target near.
	timelineInput(s, m, ev, ui.InputEvent{Kind: ui.InputPointerDown, X: 350})
	timelineInput(s, m, ev, ui.InputEvent{Kind: ui.InputPointerMove, X: 400})
	timelineInput(s, m, ev, ui.InputEvent{Kind: ui.InputPointerUp, X: 400})
	if len(changed) != 1 || ids[0] != "b" || changed[0][0] != 3.5 || changed[0][1] != 4.5 {
		t.Fatalf("move %v %v", ids, changed)
	}
	// Drag a's end to near 2.02 (an onset): it snaps.
	timelineInput(s, m, ev, ui.InputEvent{Kind: ui.InputPointerDown, X: 200})
	timelineInput(s, m, ev, ui.InputEvent{Kind: ui.InputPointerMove, X: 205})
	timelineInput(s, m, ev, ui.InputEvent{Kind: ui.InputPointerUp, X: 205})
	if len(changed) != 2 || changed[1][1] != 2.02 {
		t.Errorf("snap %v", changed)
	}
	// Alt: no snap.
	timelineInput(s, m, ev, ui.InputEvent{Kind: ui.InputPointerDown, X: 200})
	timelineInput(s, m, ev, ui.InputEvent{Kind: ui.InputPointerMove, X: 205, Mods: ui.Alt})
	timelineInput(s, m, ev, ui.InputEvent{Kind: ui.InputPointerUp, X: 205, Mods: ui.Alt})
	if changed[2][1] != 2.05 {
		t.Errorf("alt %v", changed[2])
	}
	// Keys: select, precise nudge, snap to onset.
	var nudged []string
	ev.Nudge = func(id, edge string, delta float64) { nudged = append(nudged, id+":"+edge) }
	s.sel = "a"
	timelineInput(s, m, ev, ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyRight})
	if s.sel != "b" {
		t.Errorf("select %s", s.sel)
	}
	timelineInput(s, m, ev, ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyEnter})
	timelineInput(s, m, ev, ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyRight, Mods: ui.Shift})
	timelineInput(s, m, ev, ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyLeft, Mods: ui.Alt})
	if !slices.Equal(nudged, []string{"b:end", "b:move"}) {
		t.Errorf("nudges %v", nudged)
	}
	s.zoom, s.offset = 1, 0
	s.setZoom(4, 5, 10)
	if s.span(10) != 2.5 || s.offset != 3.75 {
		t.Errorf("zoom: span %v offset %v", s.span(10), s.offset)
	}
}

func TestBatchPage(t *testing.T) {
	d := newDubTest(t, 3)
	a, tt := d.a, d.tt
	a.navigate("/batch")
	tt.Frame()
	if !tt.HasText("Batch dubbing") {
		t.Fatalf("%q", tt.Texts())
	}
	second := filepath.Join(filepath.Dir(d.src), "Second.wav")
	dubtest.Speech(second, 2)
	a.batchDropped([]string{d.src, second, "/x/notes.txt"})
	a.batchSetSettings(func(s *services.BatchSettings) { s.Targets = []string{"es", "fr"} })
	tt.Frame()
	if !tt.HasText("2 videos × 2 languages = 4 dubs") {
		t.Errorf("estimate: %q", tt.Texts())
	}
	d.click(t, "Add to queue")
	if len(a.batch.files) != 0 {
		t.Errorf("files left %v (errs %v)", a.batch.files, a.batch.errs)
	}
	a.loadBatch()
	tt.Frame()
	done := 0
	for _, r := range a.batch.items {
		if a.batchStatus(r.ID) == "done" {
			done++
		}
	}
	if done != 2 {
		t.Fatalf("done %d of %d", done, len(a.batch.items))
	}
	a.batch.tab = 1
	tt.Frame()
	if !tt.HasText("Completed (2)") {
		t.Errorf("tabs %q", tt.Texts())
	}
	id := a.batch.items[0].ID
	a.batchExport(id, "fr")
	if files, _ := filepath.Glob(filepath.Join(d.out, "*_fr.wav")); len(files) != 1 {
		t.Errorf("export: %v", files)
	}
	// The Dub page's list does not show batch projects.
	for _, r := range a.dubList {
		if r.ID == id {
			t.Error("batch project on the Dub page")
		}
	}
	// Watch folder.
	in := t.TempDir()
	a.batchWatch(in)
	tt.Frame()
	if !a.batch.watch.Active || !tt.HasText("Pause") {
		t.Errorf("watch %+v", a.batch.watch)
	}
	a.batchWatchStop()
	tt.Frame()
	if a.batch.watch.Active {
		t.Error("still watching")
	}
}
