package services

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/store"
	"github.com/hehehai/AudioSmith/internal/store/sqlitestore"
	"github.com/hehehai/AudioSmith/internal/text/markup"
)

// --- compiling documents ------------------------------------------------

// testVoices resolves voice ids "v-<name>", "speaker:<id>" and names.
func testVoices(gone ...string) VoiceResolver {
	return func(ref string) (string, core.Take, bool) {
		if slices.Contains(gone, ref) {
			return "", core.Take{}, false
		}
		switch {
		case strings.HasPrefix(ref, "v-"):
			return ref, core.Take{VoiceID: ref, RefAudio: "/voices/" + ref + ".wav"}, true
		case strings.HasPrefix(ref, SpeakerRef):
			return ref, core.Take{Voice: strings.TrimPrefix(ref, SpeakerRef)}, true
		case ref == "Bella":
			return "v-bella", core.Take{VoiceID: "v-bella"}, true
		}
		return "", core.Take{}, false
	}
}

func codesOf(ps []Problem) []string {
	var out []string
	for _, p := range ps {
		c := p.Code
		if p.Blocking {
			c += "!"
		}
		out = append(out, c)
	}
	return out
}

func TestCompileStory(t *testing.T) {
	d := NewLongformDoc(core.ProjectStory, "m/kokoro")
	d.Title = "The Lighthouse"
	d.Voice = "speaker:af_heart"
	d.Speed = 1.1
	d.Cast = []CastMember{{ID: "c1", Name: "Mara", Voice: "v-mara"}, {ID: "c2", Name: "Cole"}}
	d.Lines = []StoryLine{
		{ID: "l1", Text: "Night fell on the lighthouse."},
		{ID: "l2", Chapter: true, Text: "# One"},
		{ID: "l3", Text: "Is anyone there?", Character: "c1", Speed: 0.9, PauseAfter: 700},
		{ID: "l4", Text: "Only me, [voice:Mara]said Mara, [voice:default]and the sea.", Character: "c2"},
		{ID: "l5", Text: "   "},
		{ID: "l6", Text: "Is anyone there?", Character: "c1", Speed: 0.9},
		{ID: "l7", Text: "A voice [voice:Nobody]nobody knows.", Voice: "v-bella"},
		{ID: "l8", Chapter: true, Text: "Two"},
		{ID: "l9", Chapter: true, Text: "Three"},
		{ID: "l10", Text: "The end."},
	}
	spec, probs := Compile(&d, testVoices())
	if got := codesOf(probs); !slices.Equal(got, []string{ProblemUnknownVoice, ProblemEmptyChapter}) {
		t.Fatalf("problems %v", got)
	}
	if spec.Seed != d.Seed || spec.Title != "The Lighthouse" || spec.Kind != core.ProjectStory {
		t.Fatalf("spec %+v", spec)
	}
	var titles []string
	for _, ch := range spec.Chapters {
		titles = append(titles, ch.Title)
	}
	if !slices.Equal(titles, []string{"", "One", "Three"}) {
		t.Fatalf("chapters %q", titles)
	}
	one := spec.Chapters[1].Lines
	if len(one) != 4 {
		t.Fatalf("lines %+v", one)
	}
	if l := one[0]; l.ID != "l3" || l.Character != "Mara" || l.Speed != 0.9 || l.PauseAfter != 700 || l.Spans[0].Voice != "v-mara" || l.Repeat != 0 {
		t.Errorf("Mara's line %+v", l)
	}
	// Cole has no voice of his own: the story's; an inline switch to Mara
	// and back.
	if l := one[1]; len(l.Spans) != 3 || l.Spans[0].Voice != "speaker:af_heart" || l.Spans[0].Character != "Cole" ||
		l.Spans[1].Voice != "v-mara" || l.Spans[1].Character != "Mara" || l.Spans[1].Text != "said Mara," ||
		l.Spans[2].Voice != "speaker:af_heart" || l.Speed != 1.1 {
		t.Errorf("Cole's line %+v", l)
	}
	if one[2].Repeat != 1 {
		t.Errorf("the repeated line: %+v", one[2])
	}
	// An unknown name reads in the line's voice.
	if l := one[3]; len(l.Spans) != 2 || l.Spans[1].Voice != "v-bella" {
		t.Errorf("unknown voice %+v", l)
	}
	if spec.Voices["v-mara"].RefAudio != "/voices/v-mara.wav" || spec.Voices["speaker:af_heart"].Voice != "af_heart" {
		t.Errorf("voices %+v", spec.Voices)
	}

	// No voice anywhere, a voice that is gone, a lexicon word twice.
	d.Voice = ""
	d.Lexicon = []LexiconEntry{{Word: "Nguyen", Say: "win"}, {Word: "nguyen", Say: "nwin"}, {Word: " ", Say: "x"}}
	_, probs = Compile(&d, testVoices("v-mara"))
	got := codesOf(probs)
	for _, want := range []string{ProblemDuplicateWord + "!", ProblemNoVoice + "!", ProblemMissingVoice + "!"} {
		if !slices.Contains(got, want) {
			t.Errorf("%s not in %v", want, got)
		}
	}
	empty := NewLongformDoc(core.ProjectStory, "")
	if got := codesOf(func() []Problem { _, p := Compile(&empty, nil); return p }()); !slices.Equal(got, []string{ProblemNoModel + "!", ProblemNothingToSay + "!"}) {
		t.Errorf("empty: %v", got)
	}
}

func TestCompileManuscript(t *testing.T) {
	d := NewLongformDoc(core.ProjectAudiobook, "m/omni")
	d.Voice = "v-narrator"
	d.VoiceCast = map[string]string{"Mara": "v-mara", "Old": "v-gone"}
	d.Script = "A prologue before any chapter.\n\n" +
		"# Chapter One\n\nThe keeper climbed. [voice:Mara][slow]Who is it, [voice:default]she asked[/slow]?\n\n" +
		"[voice:Mara]Still Mara here.\n\n" +
		"# Empty\n\n" +
		"# Two\n\nBack to the narrator [boop] here.\n\n[voice:Bella]Bella speaks."
	spec, probs := Compile(&d, testVoices())
	got := codesOf(probs)
	if !slices.Contains(got, ProblemUnknownTag) || !slices.Contains(got, ProblemEmptyChapter) || slices.ContainsFunc(got, func(s string) bool { return strings.HasSuffix(s, "!") }) {
		t.Fatalf("problems %v", got)
	}
	if len(spec.Chapters) != 3 || spec.Chapters[0].Title != "" || spec.Chapters[1].Title != "Chapter One" || spec.Chapters[2].Title != "Two" {
		t.Fatalf("chapters %+v", spec.Chapters)
	}
	ch1 := spec.Chapters[1].Lines
	if len(ch1) != 2 || ch1[0].Paragraph || !ch1[1].Paragraph {
		t.Fatalf("paragraphs %+v", ch1)
	}
	sp := ch1[0].Spans
	if len(sp) != 3 || sp[0].Voice != "v-narrator" || sp[1].Voice != "v-mara" || sp[1].Character != "Mara" ||
		sp[1].Text != "[slow]Who is it,[/slow]" || sp[2].Text != "[slow]she asked[/slow]?" || sp[2].Voice != "v-narrator" {
		t.Fatalf("spans %+v", sp)
	}
	// A switch lasts across paragraphs, until the chapter ends.
	if ch1[1].Spans[0].Voice != "v-mara" {
		t.Errorf("second paragraph %+v", ch1[1])
	}
	two := spec.Chapters[2].Lines
	if two[0].Spans[0].Voice != "v-narrator" || two[1].Spans[0].Voice != "v-bella" {
		t.Errorf("chapter two %+v", two)
	}
	// A cast name whose voice is gone blocks.
	d.Script += "\n\n[voice:Old]Gone."
	_, probs = Compile(&d, testVoices("v-gone"))
	if !slices.Contains(codesOf(probs), ProblemMissingVoice+"!") {
		t.Errorf("gone: %v", codesOf(probs))
	}
	if n := VoiceNames(d.Script); !slices.Equal(n, []string{"Mara", "Bella", "Old"}) {
		t.Errorf("names %q", n)
	}
	st := StatsOf(d.Script, 1)
	if st.Chapters != 4 || st.Words < 20 || st.Seconds <= 0 {
		t.Errorf("stats %+v", st)
	}
	_ = markup.DefaultNonVerbal
}

// --- rendering ------------------------------------------------------------

// gateSynth wraps fakeSynth: texts with "HOLD" wait while hold is set,
// texts with "FLAKY" fail while flaky > 0.
type gateSynth struct {
	inner *fakeSynth
	hold  atomic.Bool
	flaky atomic.Int32
	held  chan struct{}
}

func (g *gateSynth) Run(ctx context.Context, job jobs.Job, report func(jobs.Progress), chunk func(json.RawMessage)) (json.RawMessage, error) {
	var p struct{ Text string }
	json.Unmarshal(job.Params, &p)
	if strings.Contains(p.Text, "HOLD") && g.hold.Load() {
		select {
		case g.held <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if strings.Contains(p.Text, "FLAKY") && g.flaky.Load() > 0 {
		g.flaky.Add(-1)
		return nil, &jobs.Error{Code: "oom", Message: "out of memory"}
	}
	return g.inner.Run(ctx, job, report, chunk)
}

type lfEnv struct {
	dir     string
	st      *sqlitestore.Store
	bus     *core.Bus
	sched   *jobs.Sched
	svc     *Services
	synth   *fakeSynth
	gate    *gateSynth
	encoded []EncodeParams
	encMu   sync.Mutex
	canEnc  bool
	// beforePublish, when set, sees each scheduler update before the bus
	// (on the scheduler's publishing goroutine): a test delays one there.
	beforePublish atomic.Pointer[func(jobs.Updated)]
	// beforeSave, when set, sees each job the scheduler saves.
	beforeSave atomic.Pointer[func(jobs.Job)]
}

// hookedJobs is the job store with lfEnv.beforeSave.
type hookedJobs struct {
	jobs.Store
	e *lfEnv
}

func (h hookedJobs) SaveJob(ctx context.Context, j jobs.Job) error {
	if f := h.e.beforeSave.Load(); f != nil {
		(*f)(j)
	}
	return h.Store.SaveJob(ctx, j)
}

func newLongformEnv(t *testing.T, dir string, canEncode bool) *lfEnv {
	t.Helper()
	st, err := sqlitestore.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	e := &lfEnv{dir: dir, st: st, bus: core.NewBus(), synth: &fakeSynth{}, canEnc: canEncode}
	e.gate = &gateSynth{inner: e.synth, held: make(chan struct{}, 1)}
	e.sched = jobs.New(jobs.Options{Store: hookedJobs{st.Jobs(), e}, Publish: func(u jobs.Updated) {
		if f := e.beforePublish.Load(); f != nil {
			(*f)(u)
		}
		e.bus.Publish(u)
	},
		RetryDelay: func(int) time.Duration { return time.Millisecond }})
	e.svc = New(Config{Store: st, Bus: e.bus, Scheduler: e.sched, CacheDir: filepath.Join(dir, "cache"),
		Synth:     func() jobs.Executor { return e.gate },
		ModelPath: func(_ context.Context, m string) (string, error) { return "/models/" + m, nil },
		Models:    kokoroInfo,
		Encode: func() jobs.Executor {
			if !e.canEnc {
				return nil
			}
			return jobs.ExecutorFunc(func(ctx context.Context, job jobs.Job, report func(jobs.Progress), _ func(json.RawMessage)) (json.RawMessage, error) {
				var p EncodeParams
				json.Unmarshal(job.Params, &p)
				e.encMu.Lock()
				e.encoded = append(e.encoded, p)
				e.encMu.Unlock()
				os.WriteFile(p.Output.Path, []byte("m4b"), 0o644)
				return json.Marshal(EncodeResult{Output: p.Output.Path, Format: "m4b", Bytes: 3})
			})
		},
	})
	if err := e.sched.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *lfEnv) close() {
	e.sched.Close()
	e.svc.Close(ctx)
	e.st.Close()
}

// wait waits for a render to stop running.
func (e *lfEnv) wait(t *testing.T, id string) (core.Render, RenderBody) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		r, b, err := e.svc.Longform.Get(ctx, id)
		if err == nil && r.State != core.RenderRunning {
			return r, b
		}
		time.Sleep(3 * time.Millisecond)
	}
	r, b, _ := e.svc.Longform.Get(ctx, id)
	t.Fatalf("render %s still runs: %+v", id, b.Chapters)
	return r, b
}

func storySpec() LongformSpec {
	return LongformSpec{Kind: core.ProjectStory, Title: "Lighthouse", Model: "m/kokoro", Lang: "en", Seed: 7,
		Voices: map[string]core.Take{"narrator": {Voice: "af_heart"}, "mara": {Voice: "af_bella", VoiceID: "v-mara"}},
		Chapters: []ChapterSpec{
			{Title: "One", Lines: []LineSpec{
				{ID: "a", Spans: []SpanSpec{{Voice: "narrator", Text: "Night fell on the old lighthouse by the sea."}}},
				{ID: "b", Character: "Mara", Spans: []SpanSpec{{Voice: "mara", Character: "Mara", Text: "Is anyone there? [pause 300ms] Hello?"}}, PauseAfter: 600},
			}},
			{Title: "Two", Lines: []LineSpec{
				{ID: "c", Spans: []SpanSpec{{Voice: "narrator", Text: "Morning came slowly over the water."}}},
				{ID: "d", Paragraph: true, Spans: []SpanSpec{{Voice: "narrator", Text: "The keeper "}, {Voice: "mara", Character: "Mara", Text: "smiled at last."}}},
			}},
		},
		Production: DefaultProduction(), Book: BookInfo{Loudness: LoudnessACX, Author: "A. Writer"}, Stems: true}
}

func TestLongformRendersABook(t *testing.T) {
	e := newLongformEnv(t, t.TempDir(), true)
	defer e.close()
	var events []core.RenderChanged
	var evMu sync.Mutex
	core.On(e.bus, func(ev core.RenderChanged) {
		evMu.Lock()
		events = append(events, ev)
		evMu.Unlock()
	})
	spec := storySpec()
	spec.Format = FormatM4B
	r, err := e.svc.Longform.Start(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Longform.Start(ctx, spec); !errors.Is(err, core.ErrBusy) {
		t.Errorf("a second render: %v", err)
	}
	r, b := e.wait(t, r.ID)
	if r.State != core.RenderDone || b.Output == nil {
		t.Fatalf("render %s %+v %+v", r.State, b.Error, b.Chapters)
	}
	// Chapter one's lines before chapter two's.
	texts := e.synth.texts()
	if len(texts) != 6 || !strings.HasPrefix(texts[0], "Night") || !strings.HasPrefix(texts[3], "Morning") {
		t.Fatalf("calls %q", texts)
	}
	for _, c := range e.synth.calls {
		if c.Seed == nil || (c.Voice != "af_heart" && c.Voice != "af_bella") || c.Output.Bits != 32 {
			t.Errorf("call %+v", c)
		}
	}
	out := b.Output
	book, f, err := wav.ReadFile(out.Book)
	if err != nil || f.Encoding != wav.Float32 {
		t.Fatalf("book %v %+v", err, f)
	}
	var sum float64
	for i, c := range out.Chapters {
		cb, cf, err := wav.ReadFile(c.File)
		if err != nil || cf.Encoding != wav.PCM16 {
			t.Fatalf("chapter %d: %v %+v", i, err, cf)
		}
		if math.Abs(cb.Seconds()-c.Seconds) > 0.01 || math.Abs(c.Start-sum) > 0.01 {
			t.Errorf("chapter %d at %v for %v (file %v)", i, c.Start, c.Seconds, cb.Seconds())
		}
		sum += c.Seconds
		// ACX: −23…−18 LUFS, ≤ −3 dBTP.
		if !c.Report.Compliant || c.Report.Target != "acx" || math.Abs(c.Report.Integrated+19) > 1 {
			t.Errorf("chapter %d report %+v", i, c.Report)
		}
	}
	if math.Abs(book.Seconds()-sum) > 0.01 || math.Abs(out.Seconds-sum) > 0.01 || r.AudioSeconds != out.Seconds {
		t.Errorf("book %v s, chapters %v s, result %v s", book.Seconds(), sum, out.Seconds)
	}
	cues, _ := os.ReadFile(out.Cues)
	if lines := strings.Split(string(cues), "\n"); len(lines) != 2 || lines[0] != "00:00:00\tOne" || !strings.HasSuffix(lines[1], "\tTwo") {
		t.Errorf("cues %q", cues)
	}
	// Stems: the narrator and Mara, each as long as the book, silent
	// where the other speaks.
	if len(out.Stems) != 2 || out.Stems[0].Character != "" || out.Stems[1].Character != "Mara" {
		t.Fatalf("stems %+v", out.Stems)
	}
	for _, s := range out.Stems {
		sb, _, err := wav.ReadFile(s.File)
		if err != nil || sb.Frames() != book.Frames() {
			t.Errorf("stem %s: %v %d frames, book %d", s.Character, err, sb.Frames(), book.Frames())
		}
	}
	side, err := ReadChapter(out.Chapters[0].File)
	if err != nil || len(side.Lines) != 2 || side.Lines[1].Spans[0].Character != "Mara" || side.Lines[1].Start <= side.Lines[0].End {
		t.Fatalf("sidecar %+v %v", side, err)
	}
	// Mara's line follows the narrator's after the line gap.
	if gap := side.Lines[1].Start - side.Lines[0].End; gap < 0.39 {
		t.Errorf("line gap %v", gap)
	}
	if out.Encoded == "" || out.EncodeError != "" || len(e.encoded) != 1 {
		t.Fatalf("encode %q %q %d", out.Encoded, out.EncodeError, len(e.encoded))
	}
	ep := e.encoded[0]
	if ep.Input != out.Book || len(ep.Chapters) != 2 || ep.Chapters[1].Title != "Two" || ep.Metadata.Author != "A. Writer" || ep.Metadata.Title != "Lighthouse" ||
		math.Abs(ep.Chapters[1].Start-out.Chapters[1].Start) > 0.001 {
		t.Errorf("encode params %+v", ep)
	}
	var rep BookReport
	data, _ := os.ReadFile(out.Report)
	if json.Unmarshal(data, &rep) != nil || len(rep.Chapters) != 2 || rep.Target != "acx" {
		t.Errorf("report %s", data)
	}
	evMu.Lock()
	n := len(events)
	last := events[n-1]
	evMu.Unlock()
	if n < 4 || last.Render.State != core.RenderDone {
		t.Errorf("%d events, last %s", n, last.Render.State)
	}
	if lb, _ := DecodeRender(last.Render); len(lb.Spec.Chapters) != 0 || lb.Summary.Lines != 4 || lb.Summary.Chapters != 2 {
		t.Errorf("published bodies carry the summary, not the manifest: %+v", lb.Summary)
	}
	if l, _ := e.svc.Longform.List(ctx, store.RenderQuery{}); len(l) != 1 || l[0].State != core.RenderDone {
		t.Errorf("list %+v", l)
	}

	// The same book again: every chunk from the cache. One line edited:
	// one chunk is made.
	e.synth.reset()
	r2, _ := e.svc.Longform.Start(ctx, storySpec())
	r2, b2 := e.wait(t, r2.ID)
	if r2.State != core.RenderDone || len(e.synth.texts()) != 0 || b2.Chapters[0].State != ChapterCached {
		t.Fatalf("again: %s, %q, %+v", r2.State, e.synth.texts(), b2.Chapters)
	}
	edited := storySpec()
	edited.Chapters[1].Lines[0].Spans[0].Text = "Evening came slowly over the water."
	r3, _ := e.svc.Longform.Start(ctx, edited)
	r3, b3 := e.wait(t, r3.ID)
	if r3.State != core.RenderDone || !slices.Equal(e.synth.texts(), []string{"Evening came slowly over the water."}) ||
		b3.Chapters[0].State != ChapterCached || b3.Chapters[1].State != ChapterDone {
		t.Fatalf("edited: %s, %q, %+v", r3.State, e.synth.texts(), b3.Chapters)
	}
	// Vary repeated lines: a repeat gets its own take.
	e.synth.reset()
	vary := storySpec()
	vary.Production.VaryRepeats = true
	vary.Chapters[1].Lines[0].Repeat = 1
	r4, _ := e.svc.Longform.Start(ctx, vary)
	if r4, _ = e.wait(t, r4.ID); r4.State != core.RenderDone || len(e.synth.texts()) != 1 {
		t.Errorf("vary: %q", e.synth.texts())
	}
	if err := e.svc.Longform.Delete(ctx, r4.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "renders", r4.ID)); !os.IsNotExist(err) {
		t.Errorf("files kept: %v", err)
	}
}

func TestLongformRetriesOnlyFailedChapters(t *testing.T) {
	e := newLongformEnv(t, t.TempDir(), false)
	defer e.close()
	spec := storySpec()
	spec.Format = FormatM4B // the engine cannot encode: the book stays WAV
	spec.Chapters[1].Lines[0].Spans[0].Text = "Morning came FLAKY over the water."
	e.gate.flaky.Store(1) // oom is not retryable here: one failure
	r, _ := e.svc.Longform.Start(ctx, spec)
	r, b := e.wait(t, r.ID)
	if r.State != core.RenderFailed || b.Chapters[0].State != ChapterDone || b.Chapters[1].State != ChapterFailed ||
		b.Chapters[1].Error == nil || b.Output != nil || b.Failed() != 1 {
		t.Fatalf("failed render: %s %+v", r.State, b.Chapters)
	}
	e.synth.reset()
	if _, err := e.svc.Longform.Resume(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	r, b = e.wait(t, r.ID)
	if r.State != core.RenderDone || b.Runs != 2 || b.Output == nil {
		t.Fatalf("resumed: %s %+v", r.State, b.Chapters)
	}
	// Chapter one's file was kept; chapter two made only what it lacked.
	if b.Chapters[0].State != ChapterCached {
		t.Errorf("chapter one: %+v", b.Chapters[0])
	}
	for _, s := range e.synth.texts() {
		if strings.HasPrefix(s, "Night") || strings.HasPrefix(s, "Is anyone") {
			t.Errorf("chapter one made again: %q", s)
		}
	}
	if b.Output.Encoded != "" || !strings.Contains(b.Output.EncodeError, "M4B") {
		t.Errorf("no encoder: %+v", b.Output)
	}
}

// A render resumed before the last update of its failed run arrived (the
// assembly's dependency failure, published after the chapter's failure
// that ended the run) while Resume submits the new jobs: that late update
// must not be taken for the new run's assembly, or the new assembly's
// updates are ignored and the render runs for ever.
func TestLongformResumeIgnoresLateUpdatesOfEarlierRun(t *testing.T) {
	e := newLongformEnv(t, t.TempDir(), false)
	defer e.close()
	spec := storySpec()
	spec.Chapters[1].Lines[0].Spans[0].Text = "Morning came FLAKY over the water."
	e.gate.flaky.Store(1)
	var late, resuming atomic.Bool
	release, applied := make(chan struct{}), make(chan struct{})
	var once sync.Once
	// The first run's assembly failure waits until Resume submits...
	publish := func(u jobs.Updated) {
		if u.Job.Kind == KindAssemble && u.Job.State == jobs.StateFailed && late.CompareAndSwap(false, true) {
			select {
			case <-release:
			case <-time.After(5 * time.Second):
			}
		}
	}
	e.beforePublish.Store(&publish)
	// ...and is applied while it does.
	unsub := core.On(e.bus, func(core.RenderChanged) {
		select {
		case <-release:
			once.Do(func() { close(applied) })
		default:
		}
	})
	defer unsub()
	save := func(j jobs.Job) {
		if resuming.CompareAndSwap(true, false) {
			close(release)
			select {
			case <-applied:
			case <-time.After(5 * time.Second):
			}
		}
	}
	e.beforeSave.Store(&save)
	r, _ := e.svc.Longform.Start(ctx, spec)
	if r, _ = e.wait(t, r.ID); r.State != core.RenderFailed {
		t.Fatalf("first run: %s", r.State)
	}
	resuming.Store(true)
	if _, err := e.svc.Longform.Resume(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	r, b := e.wait(t, r.ID)
	if !late.Load() || r.State != core.RenderDone || b.Runs != 2 || b.Output == nil {
		t.Fatalf("resumed: late %v, %s %+v", late.Load(), r.State, b.Chapters)
	}
}

func TestLongformStopAndAudition(t *testing.T) {
	e := newLongformEnv(t, t.TempDir(), false)
	defer e.close()
	spec := storySpec()
	spec.Chapters[1].Lines[0].Spans[0].Text = "Morning HOLD over the water."
	e.gate.hold.Store(true)
	r, _ := e.svc.Longform.Start(ctx, spec)
	select {
	case <-e.gate.held:
	case <-time.After(10 * time.Second):
		t.Fatal("chapter two never started")
	}
	if a, ok := e.svc.Longform.Active(); !ok || a.ID != r.ID {
		t.Fatalf("active %+v %v", a, ok)
	}
	// An audition runs beside it (after the chapter being made).
	aud := storySpec()
	aud.Audition = true
	aud.Chapters = aud.Chapters[:1]
	a, err := e.svc.Longform.Start(ctx, aud)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Longform.Stop(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	r, b := e.wait(t, r.ID)
	if r.State != core.RenderStopped || b.Chapters[0].State != ChapterDone || b.Chapters[1].State != ChapterStopped {
		t.Fatalf("stopped: %s %+v", r.State, b.Chapters)
	}
	a, ab := e.wait(t, a.ID)
	if a.State != core.RenderDone || !a.Audition || ab.Output != nil || ab.Chapters[0].File == "" {
		t.Fatalf("audition %s %+v", a.State, ab)
	}
	if _, err := os.Stat(ab.Chapters[0].File); err != nil || !strings.Contains(ab.Chapters[0].File, "auditions") {
		t.Errorf("audition file %q: %v", ab.Chapters[0].File, err)
	}
	if l, _ := e.svc.Longform.List(ctx, store.RenderQuery{}); len(l) != 1 {
		t.Errorf("auditions are not listed: %+v", l)
	}
	e.gate.hold.Store(false)
	if _, err := e.svc.Longform.Resume(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if r, _ = e.wait(t, r.ID); r.State != core.RenderDone {
		t.Fatalf("resumed after stop: %s", r.State)
	}
}

// A render the app quits in the middle of goes on at the next start.
func TestLongformResumesAfterRestart(t *testing.T) {
	dir := t.TempDir()
	e := newLongformEnv(t, dir, false)
	spec := storySpec()
	spec.Chapters[1].Lines[0].Spans[0].Text = "Morning HOLD over the water."
	e.gate.hold.Store(true)
	r, _ := e.svc.Longform.Start(ctx, spec)
	select {
	case <-e.gate.held:
	case <-time.After(10 * time.Second):
		t.Fatal("chapter two never started")
	}
	e.close() // quit: the running chapter is interrupted

	e2 := newLongformEnv(t, dir, false)
	defer e2.close()
	e2.svc.Longform.Reconcile(ctx)
	r2, b := e2.wait(t, r.ID)
	if r2.State != core.RenderDone || b.Output == nil {
		t.Fatalf("after restart: %s %+v %+v", r2.State, b.Error, b.Chapters)
	}
	for _, s := range e2.synth.texts() {
		if strings.HasPrefix(s, "Night") {
			t.Errorf("chapter one made again: %q", s)
		}
	}
}

func TestLibrarySavesProjectsAndExports(t *testing.T) {
	e := newLongformEnv(t, t.TempDir(), false)
	defer e.close()
	now := time.Now()
	e.st.Voices().Put(ctx, core.Voice{ID: "v-mara", Name: "Mara", Kind: core.VoiceCloned, CreatedAt: now, UpdatedAt: now})
	d := NewLongformDoc(core.ProjectStory, "m/kokoro")
	d.Title = "The Lighthouse"
	d.Cast = []CastMember{{ID: "c1", Name: "Mara", Voice: "v-mara"}}
	d.Lines = []StoryLine{{ID: "l1", Text: "Hello there.", Character: "c1"}, {ID: "l2", Text: "Hi."}, {ID: "l3", Chapter: true, Text: "Two"}}
	lib := e.svc.Library
	p, err := lib.SaveDoc(ctx, core.Project{}, d)
	if err != nil || p.ID == "" || p.Kind != core.ProjectStory || p.Title != "The Lighthouse" {
		t.Fatalf("%+v %v", p, err)
	}
	u, _ := e.svc.Voices.Usage(ctx, "v-mara")
	if u.Segments != 1 || len(u.Projects) != 1 || u.Projects[0].ID != p.ID {
		t.Errorf("usage %+v", u)
	}
	if _, err := lib.Rename(ctx, p.ID, "  "); !errors.Is(err, core.ErrInvalid) {
		t.Error(err)
	}
	if _, err := lib.Rename(ctx, p.ID, "Night Light"); err != nil {
		t.Fatal(err)
	}
	p2, d2, err := lib.LoadDoc(ctx, p.ID)
	if err != nil || p2.Title != "Night Light" || d2.Title != "Night Light" || len(d2.Lines) != 3 || d2.Version != LongformVersion {
		t.Fatalf("%+v %+v %v", p2, d2, err)
	}
	items, _ := lib.Search(ctx, store.LibraryQuery{Category: store.LibStory})
	if len(items) != 1 || SummaryOf(items[0].Detail).Lines != 2 || SummaryOf(items[0].Detail).Chapters != 2 {
		t.Errorf("library %+v", items)
	}
	f := filepath.Join(t.TempDir(), "book.wav")
	os.WriteFile(f, []byte("12345"), 0o644)
	ex, err := lib.RecordExport(ctx, core.Export{Kind: "book", SourceID: "r1", ProjectID: p.ID, Path: f, Format: "wav"})
	if err != nil || ex.Bytes != 5 || ex.Title != "book.wav" {
		t.Fatalf("%+v %v", ex, err)
	}
	n, _ := lib.Counts(ctx, "")
	if n["export"] != 1 || n["story"] != 1 || n["voice"] != 1 {
		t.Errorf("counts %v", n)
	}
	if err := lib.DeleteExport(ctx, ex.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f); err != nil {
		t.Error("the file goes with the record")
	}
	// A sample's copy in the data folder goes with its record (code
	// review 1102 L1).
	sample := filepath.Join(e.dir, "samples", "01S", "story.ogg")
	os.MkdirAll(filepath.Dir(sample), 0o755)
	os.WriteFile(sample, []byte("ogg"), 0o644)
	ex, _ = lib.RecordExport(ctx, core.Export{Kind: "story", ProjectID: p.ID, Path: sample, Format: "ogg"})
	if err := lib.DeleteExport(ctx, ex.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(sample)); !os.IsNotExist(err) {
		t.Error("the sample's copy stayed")
	}
}

func TestETA(t *testing.T) {
	at := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)
	b := RenderBody{Chapters: []RenderChapter{
		{Chars: 100, State: ChapterDone, Started: at, Finished: at.Add(10 * time.Second)},
		{Chars: 100, State: ChapterRendering, Fraction: 0.5, Started: at.Add(10 * time.Second)},
		{Chars: 200, State: ChapterPending},
	}}
	eta, ok := ETA(&b, at.Add(15*time.Second))
	if !ok || eta != 25*time.Second {
		t.Errorf("eta %v %v", eta, ok)
	}
	if _, ok := ETA(&RenderBody{Chapters: []RenderChapter{{Chars: 10, State: ChapterPending}}}, at); ok {
		t.Error("too early")
	}
}
