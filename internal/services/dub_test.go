package services

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp/dsptest"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/dub/dubtest"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/store"
	"github.com/hehehai/AudioSmith/internal/store/sqlitestore"
	"github.com/hehehai/AudioSmith/internal/translate"
)

// fakeTranslator prefixes every line with its language.
type fakeTranslator struct {
	mu    sync.Mutex
	calls int
	lines int
	fail  map[string]bool // lines (by text) that fail
	speed float64         // the last request's
}

func (f *fakeTranslator) Info() translate.Info {
	return translate.Info{ID: "fake", Kind: "llm", Qualities: []translate.Quality{translate.Fast}}
}

func (f *fakeTranslator) Translate(_ context.Context, req translate.Request) (translate.Result, error) {
	f.mu.Lock()
	f.calls++
	f.lines += len(req.Segments)
	f.speed = req.Speed
	f.mu.Unlock()
	res := translate.Result{Translator: "fake"}
	for _, s := range req.Segments {
		t := translate.Translated{ID: s.ID, Text: "[" + req.Target + "] " + s.Text}
		if f.fail[s.Text] {
			t.Text, t.Err = "", &translate.Error{Code: translate.CodeLineFailed}
			res.Failed++
		}
		if s.Slot > 0 {
			fit := translate.DefaultPredictor.Fit(t.Text, req.Target, s.Slot, 1)
			t.Fit = &fit
		}
		res.Segments = append(res.Segments, t)
	}
	return res, nil
}

type dubEnv struct {
	dir     string
	st      *sqlitestore.Store
	bus     *core.Bus
	sched   *jobs.Sched
	svc     *Services
	dubs    *Dubs
	eng     *dubtest.Engine
	tr      *fakeTranslator
	src     string
	ytdlp   dub.YtDlp
	models  *DubModels // replaces the default models when set
	notDown []string   // models ModelPath reports not downloaded
	mu      sync.Mutex
	changes int
	marked  []string // files the watermark hook marked (watermark_test.go)
}

func newDubEnv(t *testing.T, lines int) *dubEnv {
	t.Helper()
	dir := t.TempDir()
	st, err := sqlitestore.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	e := &dubEnv{dir: dir, st: st, bus: core.NewBus(), eng: dubtest.New(), tr: &fakeTranslator{fail: map[string]bool{}}}
	e.eng.Script = []string{"Hello there.", "How are you today?", "I am fine, thank you.", "That is good to hear.", "See you tomorrow.", "Goodbye."}
	e.sched = jobs.New(jobs.Options{Store: jobs.NewMemStore(), Publish: func(u jobs.Updated) { e.bus.Publish(u) },
		RetryDelay: func(int) time.Duration { return time.Millisecond }})
	models := func(string) ModelInfo {
		return ModelInfo{Traits: TraitsFor("qwen3_tts", []string{"speed", "clone"}), Revision: "r1"}
	}
	e.svc = New(Config{Store: st, Bus: e.bus, Scheduler: e.sched, CacheDir: filepath.Join(dir, "cache"),
		Synth:     func() jobs.Executor { return e.eng.Executor(proto.MethodTTSSynthesize) },
		ModelPath: func(_ context.Context, m string) (string, error) { return "/models/" + m, nil },
		Models:    models,
		Watermark: func(_ context.Context, path string, _ int) (bool, error) {
			e.mu.Lock()
			e.marked = append(e.marked, path)
			e.mu.Unlock()
			return true, nil
		},
	})
	e.dubs = NewDubs(DubsConfig{
		Store: st.Dubs(), Bus: e.bus, Scheduler: e.sched, Render: e.svc.Render,
		Engine: func(m string) jobs.Executor { return e.eng.Executor(m) },
		ModelPath: func(_ context.Context, m string) (string, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			if slices.Contains(e.notDown, m) {
				return "", fmt.Errorf("%s: %w", m, core.ErrNotDownloaded)
			}
			return "/models/" + m, nil
		},
		Models: func() DubModels {
			e.mu.Lock()
			defer e.mu.Unlock()
			if e.models != nil {
				return *e.models
			}
			return DubModels{STT: "stt", Align: "align", Diarize: "diar", Separate: "sep", Enhance: "dfn"}
		},
		Caps: func(string) DubModelCaps { return DubModelCaps{Clone: true, Speed: true} },
		Voice: func(_ context.Context, ref, model, lang string) (dub.VoiceSpec, error) {
			if ref == "gone" {
				return dub.VoiceSpec{}, core.ErrNotFound
			}
			return dub.VoiceSpec{Voice: "speaker-" + ref}, nil
		},
		Translator: func(context.Context, dub.Settings) (translate.Translator, error) { return e.tr, nil },
		YtDlp:      func() dub.YtDlp { return e.ytdlp },
		Documents:  st.Documents(),
		WatchEvery: time.Hour, // the test scans by hand
	})
	core.On(e.bus, func(DubChanged) {
		e.mu.Lock()
		e.changes++
		e.mu.Unlock()
	})
	if err := e.sched.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		e.sched.Close()
		e.svc.Close(ctx)
		st.Close()
	})
	e.src = filepath.Join(dir, "interview.wav")
	if _, err := dubtest.Speech(e.src, lines); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *dubEnv) create(t *testing.T, edit func(*dub.Project)) *dub.Project {
	t.Helper()
	p := dub.New("", time.Now())
	p.Title = "Interview"
	p.Source = dub.Source{Kind: dub.SourceFile, Path: e.src, Name: "interview.wav"}
	p.Settings.Model = "tts"
	p.Settings.Review = false
	p.ChooseTargets([]string{"es"}) // TestDubDefaultTarget covers the default
	if edit != nil {
		edit(p)
	}
	p, err := e.dubs.Create(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// run runs a request and waits for every job of the project.
func (e *dubEnv) run(t *testing.T, id string, req RunRequest) *dub.Project {
	t.Helper()
	ids, err := e.dubs.Run(ctx, id, req)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range ids {
		e.wait(t, j)
	}
	p, err := e.dubs.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (e *dubEnv) wait(t *testing.T, id string) jobs.Job {
	t.Helper()
	// A whole dub (two targets, every stage) can take over 20 s under
	// -race on a busy CI runner (CI 41215995801): wait a minute.
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		j, err := e.sched.Get(ctx, id)
		if err == nil && (j.State.Terminal() || j.State == jobs.StateInterrupted) {
			return j
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("job %s did not end", id)
	return jobs.Job{}
}

func TestDubPipelineEndToEnd(t *testing.T) {
	e := newDubEnv(t, 6)
	p := e.create(t, func(p *dub.Project) { p.Targets = []string{"es", "fr"} })
	p = e.run(t, p.ID, RunRequest{Through: dub.StageMix})
	for k, st := range p.Stages {
		if st.Status != dub.StatusDone {
			t.Errorf("stage %s: %+v", k, st)
		}
	}
	if p.Run != nil {
		t.Errorf("run not finished: %+v", p.Run)
	}
	if len(p.Segments) != 6 || p.Detected != "en" {
		t.Fatalf("%d segments, detected %q", len(p.Segments), p.Detected)
	}
	if labels := p.SpeakerLabels(); !slices.Equal(labels, []string{"S1", "S2"}) {
		t.Errorf("speakers %v", labels)
	}
	if p.Media.Vocals == "" || p.Media.Background == "" || p.Media.Peaks == "" || len(p.Media.Onsets) == 0 {
		t.Errorf("media %+v", p.Media)
	}
	if len(p.References) != 2 || !fileExists(e.dubs.Path(p.ID, p.References["S1"].File)) {
		t.Errorf("references %+v", p.References)
	}
	if r := p.References["S1"]; r.Enhanced || !r.Tidied || r.Grade == "" {
		t.Errorf("a clean reference is tidied, not denoised: %+v", r)
	}
	for _, l := range []string{"es", "fr"} {
		if done, total := p.Translated(l); done != 6 || total != 6 {
			t.Errorf("%s translated %d/%d", l, done, total)
		}
		tr := p.Track(l)
		if tr.File == "" || !fileExists(e.dubs.Path(p.ID, tr.File)) || tr.Background != dub.BackgroundStem || len(tr.Lines) != 6 {
			t.Errorf("%s track %+v", l, tr)
		}
		for id, r := range tr.Lines {
			seg, _ := p.Segment(id)
			if r.Err != "" || r.Length <= 0 || r.Length > seg.Duration()+0.01 {
				t.Errorf("%s line %s: %+v (slot %.2f)", l, id, r, seg.Duration())
			}
		}
	}
	if p.Phase() != dub.PhaseDone {
		t.Errorf("phase %s", p.Phase())
	}
	// Every line was cloned from its speaker's reference.
	var sp proto.SynthesizeParams
	json.Unmarshal(e.eng.Last(proto.MethodTTSSynthesize), &sp)
	if !strings.Contains(sp.RefAudio, filepath.Join("refs", "S")) || sp.RefText == "" {
		t.Errorf("synthesize %+v", sp)
	}
	if n := e.eng.Count(proto.MethodTTSSynthesize); n != 12 {
		t.Errorf("%d lines rendered", n)
	}

	// Incremental: change one Spanish line; only it renders again.
	id := p.Segments[2].ID
	if _, err := e.dubs.Update(ctx, p.ID, func(p *dub.Project) error {
		p.SetText(id, "es", "Estoy bien, gracias.")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before := e.eng.Count(proto.MethodTTSSynthesize)
	trBefore := e.tr.calls
	p = e.run(t, p.ID, RunRequest{Through: dub.StageMix, Langs: []string{"es"}})
	if n := e.eng.Count(proto.MethodTTSSynthesize) - before; n != 1 {
		t.Errorf("re-dub rendered %d lines", n)
	}
	if e.tr.calls != trBefore {
		t.Error("translated again")
	}
	if p.Track("es").Lines[id].Text != "Estoy bien, gracias." {
		t.Errorf("line %+v", p.Track("es").Lines[id])
	}

	// Verify: a line heard wrong is flagged.
	e.eng.Heard["[fr] Goodbye."] = "Bonjour"
	p = e.run(t, p.ID, RunRequest{Through: dub.StageQC, Langs: []string{"fr"}})
	qc := p.Track("fr").QC
	if qc == nil || len(qc.Lines) != 6 || qc.Flagged != 1 {
		t.Fatalf("qc %+v", qc)
	}
	if p.Track("fr").QCStale() {
		t.Error("fresh QC is stale")
	}

	// Exports.
	out := t.TempDir()
	exp := func(req ExportRequest) (ExportResult, error) {
		req.Dub = p.ID
		return e.dubs.Export(ctx, req, nil)
	}
	for _, f := range []string{dub.ExportSRT, dub.ExportVTT, dub.ExportASS, dub.ExportWAV, dub.ExportM4A, dub.ExportStems, dub.ExportSegments, dub.ExportMP4} {
		path := filepath.Join(out, dub.ExportName(p.Title, "es", f))
		res, err := exp(ExportRequest{Format: f, Lang: "es", Output: path, Dual: f == dub.ExportSRT, Subtitles: true})
		if f == dub.ExportMP4 {
			// The source is a WAV: no video.
			if code(err) != "dub.no_video" {
				t.Errorf("mp4 of audio: %v", err)
			}
			continue
		}
		if err != nil || res.Bytes == 0 {
			t.Errorf("%s: %v %+v", f, err, res)
		}
	}
	srt, _ := os.ReadFile(filepath.Join(out, "Interview_es.srt"))
	if !strings.Contains(string(srt), "[es] Hello there.\nHello there.") {
		t.Errorf("dual srt:\n%s", srt)
	}
	zr, err := zip.OpenReader(filepath.Join(out, "Interview_es_lines.zip"))
	if err != nil || len(zr.File) != 7 {
		t.Errorf("segments zip: %v", err)
	} else {
		zr.Close()
	}
	// MP3 through media.encode (the app runs it on ffmpeg, M7a); burnt-in
	// subtitles need a video.
	if res, err := exp(ExportRequest{Format: dub.ExportMP3, Lang: "es", Output: filepath.Join(out, "x.mp3")}); err != nil || res.Bytes == 0 {
		t.Errorf("mp3 %v %+v", err, res)
	}
	if _, err := exp(ExportRequest{Format: dub.ExportBurn, Lang: "es", Output: filepath.Join(out, "x.mp4")}); code(err) != "dub.no_video" {
		t.Errorf("burn of audio %v", err)
	}
	if _, err := exp(ExportRequest{Format: dub.ExportWAV, Lang: "de", Output: filepath.Join(out, "x.wav")}); code(err) != DubCodeNotMixed {
		t.Errorf("unmixed %v", err)
	}
	e.eng.Missing[dub.MethodEncode] = true
	if _, err := exp(ExportRequest{Format: dub.ExportMP3, Lang: "es", Output: filepath.Join(out, "y.mp3")}); code(err) != DubCodeNeedsFFmpeg {
		t.Errorf("mp3 without ffmpeg %v", err)
	}
	if _, err := exp(ExportRequest{Format: dub.ExportM4A, Lang: "es", Output: filepath.Join(out, "y.m4a")}); code(err) != DubCodeNeedsEngine {
		t.Errorf("m4a without the engine %v", err)
	}

	// The list shows it; deleting removes its files.
	l, _ := e.dubs.List(ctx, store.DubQuery{})
	if len(l) != 1 || l[0].Phase != string(dub.PhaseDone) || l[0].Title != "Interview" {
		t.Errorf("list %+v", l)
	}
	if err := e.dubs.Delete(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.dubs.Dir(p.ID)); !os.IsNotExist(err) {
		t.Error("files kept")
	}
}

func TestDubWithoutSeparationOrDiarization(t *testing.T) {
	e := newDubEnv(t, 4)
	e.eng.Missing[dub.MethodSeparate] = true
	e.eng.Missing[dub.MethodDiarize] = true
	// A noisy source: its references are denoised (thread #83: only then).
	src, _, err := wav.ReadFile(e.src)
	if err != nil {
		t.Fatal(err)
	}
	dsptest.Add(src, dsptest.Noise(src.Rate, src.Channels, -32, src.Seconds(), 3), 0)
	if err := wav.WriteFile(e.src, src, wav.PCM16, wav.Options{}); err != nil {
		t.Fatal(err)
	}
	p := e.create(t, nil)
	p = e.run(t, p.ID, RunRequest{Through: dub.StageMix})
	if st := p.Stage(dub.StageSeparate, ""); st.Status != dub.StatusSkipped || st.Code != DubWarnNoSeparation {
		t.Errorf("separate %+v", st)
	}
	if st := p.Stage(dub.StageTranscribe, ""); !slices.Contains(st.Warnings, DubWarnNoDiarization) {
		t.Errorf("transcribe %+v", st)
	}
	if labels := p.SpeakerLabels(); !slices.Equal(labels, []string{"S1"}) {
		t.Errorf("speakers %v", labels)
	}
	if r := p.References["S1"]; !r.Enhanced || !r.Tidied || !slices.Contains(r.Hints, "noisy") {
		t.Errorf("noisy reference not cleaned: %+v", r)
	}
	tr := p.Track("es")
	if tr.Background != dub.BackgroundDucked || tr.File == "" {
		t.Errorf("track %+v", tr)
	}
	if !slices.Contains(p.Stage(dub.StageMix, "es").Warnings, DubWarnNoSeparation) {
		t.Errorf("mix warnings %+v", p.Stage(dub.StageMix, "es"))
	}
}

func TestDubReviewCheckpointsAndCast(t *testing.T) {
	e := newDubEnv(t, 3)
	p := e.create(t, func(p *dub.Project) { p.Settings.Review = true })
	p = e.run(t, p.ID, RunRequest{Through: dub.StageReferences})
	if len(p.Segments) != 3 || p.Phase() != dub.PhaseTranscribed || len(p.Tracks) != 0 {
		t.Fatalf("after the transcript: %d segments, phase %s", len(p.Segments), p.Phase())
	}
	if e.tr.calls != 0 {
		t.Error("translated before the checkpoint")
	}
	// A plan for the next step translates only.
	if got := e.dubs.Plan(p, RunRequest{Through: dub.StageTranslate}); !slices.Equal(got, []string{"translate:es"}) {
		t.Errorf("plan %v", got)
	}
	e.tr.fail["How are you today?"] = true
	p = e.run(t, p.ID, RunRequest{Through: dub.StageTranslate})
	if p.Phase() != dub.PhaseTranscribed || !slices.Contains(p.Stage(dub.StageTranslate, "es").Warnings, DubWarnTranslateFailed) {
		t.Errorf("phase %s %+v", p.Phase(), p.Stage(dub.StageTranslate, "es"))
	}
	failed := p.Segments[1].Lines["es"]
	if failed.Err != translate.CodeLineFailed {
		t.Errorf("failed line %+v", failed)
	}
	// Retry translates only the failed line.
	delete(e.tr.fail, "How are you today?")
	n := e.tr.lines
	p = e.run(t, p.ID, RunRequest{Through: dub.StageTranslate})
	if e.tr.lines-n != 1 || !p.AllTranslated() {
		t.Errorf("retried %d lines", e.tr.lines-n)
	}
	// Cast: S1 with a saved voice, one line with a voice that is gone.
	e.dubs.Update(ctx, p.ID, func(p *dub.Project) error {
		p.CastSpeaker("S1", "v42")
		p.SetVoice("gone", p.Segments[2].ID)
		return nil
	})
	p = e.run(t, p.ID, RunRequest{Through: dub.StageMix})
	if !slices.Contains(p.Stage(dub.StageSynthesize, "es").Warnings, DubWarnVoice) {
		t.Errorf("synthesize %+v", p.Stage(dub.StageSynthesize, "es"))
	}
	found := false
	for _, c := range e.eng.Calls {
		var sp proto.SynthesizeParams
		if c.Method == proto.MethodTTSSynthesize && json.Unmarshal(c.Params, &sp) == nil && sp.Voice == "speaker-v42" {
			found = true
		}
	}
	if !found {
		t.Error("the saved voice was not used")
	}
	// Line take for the audition.
	take, warn, err := e.dubs.LineRecipe(ctx, p, p.Segments[1].ID, "es")
	if err != nil || warn != "" || take.Text != "[es] How are you today?" || take.Model != "tts" {
		t.Errorf("%+v %q %v", take, warn, err)
	}
}

func TestDubImportedSubtitlesAndInterruption(t *testing.T) {
	e := newDubEnv(t, 3)
	p := e.create(t, func(p *dub.Project) { p.Settings.KeepBackground = false })
	p, _ = e.dubs.Update(ctx, p.ID, func(p *dub.Project) error {
		p.ImportSubtitles("1\n00:00:00,500 --> 00:00:02,000\nFirst\n\n2\n00:00:03,000 --> 00:00:04,000\nSecond\n", 0)
		return nil
	})
	// An interrupted run: a stage left running, no job.
	p, _ = e.dubs.Update(ctx, p.ID, func(p *dub.Project) error {
		p.Run = &dub.Run{Through: dub.StageReferences}
		p.SetStage(dub.StageIngest, "", dub.StageState{Status: dub.StatusRunning})
		return nil
	})
	active, _ := e.dubs.Active(ctx, p.ID)
	if !Interrupted(p, active) {
		t.Fatal("not interrupted")
	}
	ids, err := e.dubs.Resume(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		e.wait(t, id)
	}
	p, _ = e.dubs.Get(ctx, p.ID)
	if e.eng.Count(proto.MethodSTTTranscribe) != 0 {
		t.Error("transcribed over the imported subtitles")
	}
	if len(p.Segments) != 2 || p.Segments[0].Text != "First" || p.Run != nil {
		t.Errorf("%+v run %+v", p.Segments, p.Run)
	}
	if _, ok := p.References["S1"]; ok {
		t.Error("2.5 s of speech made a reference")
	}
	if !slices.Contains(p.Stage(dub.StageReferences, "").Warnings, DubWarnNoReference) {
		t.Errorf("references %+v", p.Stage(dub.StageReferences, ""))
	}
}

func TestDubCancelAndBusy(t *testing.T) {
	e := newDubEnv(t, 3)
	p := e.create(t, nil)
	block := make(chan struct{})
	e.dubs.cfg.Translator = func(ctx context.Context, _ dub.Settings) (translate.Translator, error) {
		select {
		case <-block:
		case <-ctx.Done():
		}
		return nil, ctx.Err()
	}
	ids, err := e.dubs.Run(ctx, p.ID, RunRequest{Through: dub.StageMix})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.dubs.Run(ctx, p.ID, RunRequest{Through: dub.StageMix}); !errors.Is(err, core.ErrBusy) {
		t.Errorf("second run: %v", err)
	}
	// Wait for translation to start, then cancel.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if j, _ := e.sched.Get(ctx, ids["translate:es"]); j.State == jobs.StateRunning {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := e.dubs.Cancel(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		e.wait(t, id)
	}
	p, _ = e.dubs.Get(ctx, p.ID)
	if p.Run != nil || p.Stage(dub.StageTranslate, "es").Status == dub.StatusRunning {
		t.Errorf("after cancel: run %+v stage %+v", p.Run, p.Stage(dub.StageTranslate, "es"))
	}
	if len(p.Segments) == 0 {
		t.Error("the transcript was lost")
	}
	close(block)
}

func TestDubURLNeedsYtDlp(t *testing.T) {
	e := newDubEnv(t, 2)
	p := e.create(t, func(p *dub.Project) {
		p.Source = dub.Source{Kind: dub.SourceURL, URL: "https://www.youtube.com/watch?v=abc", Name: "abc"}
	})
	if got := e.dubs.Plan(p, RunRequest{Through: dub.StageReferences}); len(got) == 0 || got[0] != "fetch" {
		t.Errorf("plan %v", got)
	}
	ids, err := e.dubs.Run(ctx, p.ID, RunRequest{Through: dub.StageReferences})
	if err != nil {
		t.Fatal(err)
	}
	j := e.wait(t, ids["fetch"])
	if j.State != jobs.StateFailed || j.Err.Code != dub.CodeNoYtDlp {
		t.Errorf("fetch %+v %+v", j.State, j.Err)
	}
}

func TestBatchQueueAndWatchFolder(t *testing.T) {
	e := newDubEnv(t, 3)
	bs := e.dubs.BatchSettings(ctx)
	if len(bs.Targets) != 1 || !bs.KeepBackground {
		t.Errorf("defaults %+v", bs)
	}
	bs.Targets, bs.Model, bs.Voice = []string{"es", "de"}, "tts", "v7"
	if err := e.dubs.SetBatchSettings(ctx, bs); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(e.dir, "second.wav")
	dubtest.Speech(other, 2)
	ids, errs := e.dubs.Enqueue(ctx, []string{e.src, other, filepath.Join(e.dir, "notes.txt")}, e.dubs.BatchSettings(ctx))
	if len(ids) != 2 || len(errs) != 1 {
		t.Fatalf("%v %v", ids, errs)
	}
	for _, id := range ids {
		active, _ := e.dubs.Active(ctx, id)
		for _, j := range active {
			if j.Priority != jobs.PriorityBackground {
				t.Errorf("%s priority %d", j.Kind, j.Priority)
			}
			e.wait(t, j.ID)
		}
		p, _ := e.dubs.Get(ctx, id)
		if p.Batch != BatchQueue || !p.TracksReady() || p.Settings.Review {
			t.Errorf("%s: %+v", id, p.Tracks)
		}
		if p.VoiceOf(p.Segments[0]) != "v7" {
			t.Error("batch voice")
		}
	}
	q, _ := e.dubs.Queue(ctx)
	if len(q) != 2 {
		t.Errorf("queue %d", len(q))
	}
	if l, _ := e.dubs.List(ctx, store.DubQuery{}); len(l) != 0 {
		t.Error("batch projects listed on the Dub page")
	}

	// The watch folder.
	in := filepath.Join(e.dir, "in")
	os.MkdirAll(in, 0o755)
	dubtest.Speech(filepath.Join(in, "old.wav"), 2)
	if err := e.dubs.StartWatch(ctx, in); err != nil {
		t.Fatal(err)
	}
	dubtest.Speech(filepath.Join(in, "new.wav"), 2)
	if got := e.dubs.ScanWatch(ctx); len(got) != 0 {
		t.Errorf("first scan %v", got)
	}
	e.dubs.PauseWatch(ctx, true)
	if got := e.dubs.ScanWatch(ctx); len(got) != 0 {
		t.Error("scanned while paused")
	}
	e.dubs.PauseWatch(ctx, false)
	if got := e.dubs.ScanWatch(ctx); !slices.Equal(got, []string{"new.wav"}) {
		t.Errorf("second scan %v", got)
	}
	if w := e.dubs.Watch(); w.Added != 1 || !w.Active || w.Dir != in {
		t.Errorf("watch %+v", w)
	}
	// Another start of the app finds it watching, with the files seen.
	e.dubs.CloseWatch()
	again := NewDubs(DubsConfig{Store: e.st.Dubs(), Documents: e.st.Documents(), WatchEvery: time.Hour})
	again.RestoreWatch(ctx)
	defer again.CloseWatch()
	if w := again.Watch(); !w.Active || w.Added != 1 {
		t.Errorf("restored %+v", w)
	}
	if got := again.ScanWatch(ctx); len(got) != 0 {
		t.Errorf("re-added %v", got)
	}
	e.dubs.StopWatch(ctx)
	if w := e.dubs.Watch(); w.Active {
		t.Error("still watching")
	}
}
