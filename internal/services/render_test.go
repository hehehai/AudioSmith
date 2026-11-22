package services

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/dsptest"
	"github.com/hehehai/AudioSmith/internal/dsp/loudness"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/store/sqlitestore"
	"github.com/hehehai/AudioSmith/internal/text"
	"github.com/hehehai/AudioSmith/internal/text/lexicon"
)

// fakeSynth plays the engine's tts.synthesize: it writes a tone as long as
// the text should take (float WAV at 24 kHz), and reacts to words in the
// text: "block" waits to be cancelled, "fail" fails with oom (retryable
// false here), "short" makes a tenth of the audio.
type fakeSynth struct {
	mu      sync.Mutex
	calls   []proto.SynthesizeParams
	ids     []string
	started chan string
	rate    int
	limit   int // texts longer fail with input_too_long
	// pauses: 0.35 s of silence between the sentences of a text, as
	// speech has (merged story turns split there).
	pauses bool
}

func (f *fakeSynth) Run(ctx context.Context, job jobs.Job, report func(jobs.Progress), _ func(json.RawMessage)) (json.RawMessage, error) {
	var p proto.SynthesizeParams
	if err := json.Unmarshal(job.Params, &p); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.calls = append(f.calls, p)
	f.ids = append(f.ids, job.ID)
	rate := f.rate
	f.mu.Unlock()
	if rate == 0 {
		rate = 24000
	}
	if f.started != nil {
		f.started <- p.Text
	}
	report(jobs.Progress{Stage: "loading", Fraction: -1})
	report(jobs.Progress{Stage: "generating", Fraction: 0.5, AudioSeconds: 0.2})
	if f.limit > 0 && len([]rune(p.Text)) > f.limit {
		data, _ := json.Marshal(map[string]any{"limit": f.limit, "actual": len([]rune(p.Text)), "unit": "chars"})
		return nil, &jobs.Error{Code: proto.CodeInputTooLong, Message: "text too long", Data: data}
	}
	switch {
	case strings.Contains(p.Text, "block"):
		<-ctx.Done()
		return nil, ctx.Err()
	case strings.Contains(p.Text, "fail"):
		return nil, &jobs.Error{Code: proto.CodeOOM, Message: "out of memory"}
	}
	w, c := text.Count(p.Text)
	secs := text.Estimate(w, c).Seconds()
	if p.Speed != nil {
		secs /= *p.Speed
	}
	if p.Duration != nil {
		secs = *p.Duration
	}
	if strings.Contains(p.Text, "short") {
		secs /= 10
	}
	speech := dsptest.Sine(rate, 1, 220, -12, secs, 0)
	if f.pauses {
		var parts []dsp.Buffer
		for i, s := range fakeSentences(p.Text) {
			if i > 0 {
				parts = append(parts, dsptest.Silence(rate, 1, 0.35))
			}
			w, c := text.Count(s)
			parts = append(parts, dsptest.Sine(rate, 1, 220, -12, text.Estimate(w, c).Seconds(), 0))
		}
		speech = dsptest.Concat(parts...)
	}
	buf := dsptest.Concat(dsptest.Silence(rate, 1, 0.1), speech, dsptest.Silence(rate, 1, 0.2))
	if err := wav.WriteFile(p.Output.Path, buf, wav.Float32, wav.Options{}); err != nil {
		return nil, err
	}
	seed := int64(42)
	if p.Seed != nil {
		seed = *p.Seed
	}
	return json.Marshal(proto.SynthesizeResult{Output: p.Output.Path, Model: p.Model, SampleRate: rate, Channels: 1,
		Frames: int64(buf.Frames()), AudioSeconds: buf.Seconds(), Seconds: 0.1, Seed: &seed})
}

// fakeSentences splits a text after . ? ! 。 ？ ！ followed by more.
func fakeSentences(s string) []string {
	var out []string
	start := 0
	rs := []rune(s)
	for i, r := range rs {
		if strings.ContainsRune(".?!。？！", r) && i+1 < len(rs) && !strings.ContainsRune(".?!。？！", rs[i+1]) {
			out = append(out, strings.TrimSpace(string(rs[start:i+1])))
			start = i + 1
		}
	}
	if t := strings.TrimSpace(string(rs[start:])); t != "" {
		out = append(out, t)
	}
	return out
}

func (f *fakeSynth) texts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		out = append(out, c.Text)
	}
	return out
}

func (f *fakeSynth) reset() {
	f.mu.Lock()
	f.calls, f.ids = nil, nil
	f.mu.Unlock()
}

// renderEnv is the services over a real scheduler with a fake engine.
type renderEnv struct {
	st    *sqlitestore.Store
	bus   *core.Bus
	sched *jobs.Sched
	svc   *Services
	synth *fakeSynth
	keep  atomic.Int64
	// wm is the render's watermark (watermark_test.go); nil: none.
	wm WatermarkFunc
	// credit is Config.Credit (render_external_test.go); nil: none.
	credit func(model, voice string) string

	mu       sync.Mutex
	stream   map[string][]byte // pcm16 by job
	finals   map[string]bool
	progress []RenderProgress
}

func newRenderEnv(t *testing.T, models func(string) ModelInfo) *renderEnv {
	t.Helper()
	return newRenderEnvWith(t, models, nil)
}

// newRenderEnvWith is newRenderEnv with Config.Replace.
func newRenderEnvWith(t *testing.T, models func(string) ModelInfo, replace func(*core.Take) bool) *renderEnv {
	t.Helper()
	dir := t.TempDir()
	st, err := sqlitestore.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	e := &renderEnv{st: st, bus: core.NewBus(), synth: &fakeSynth{}, stream: map[string][]byte{}, finals: map[string]bool{}}
	e.sched = jobs.New(jobs.Options{Store: jobs.NewMemStore(), Publish: func(u jobs.Updated) { e.bus.Publish(u) },
		RetryDelay: func(int) time.Duration { return time.Millisecond }})
	core.On(e.bus, func(u jobs.Updated) {
		if u.Chunk == nil {
			return
		}
		var c proto.ChunkParams
		json.Unmarshal(u.Chunk, &c)
		e.mu.Lock()
		e.stream[u.Job.ID] = append(e.stream[u.Job.ID], c.Data...)
		if c.Final {
			e.finals[u.Job.ID] = true
		}
		e.mu.Unlock()
	})
	core.On(e.bus, func(p RenderProgress) {
		e.mu.Lock()
		e.progress = append(e.progress, p)
		e.mu.Unlock()
	})
	e.svc = New(Config{Store: st, Bus: e.bus, Scheduler: e.sched, CacheDir: filepath.Join(dir, "cache"),
		Synth:     func() jobs.Executor { return e.synth },
		ModelPath: func(_ context.Context, m string) (string, error) { return "/models/" + m, nil },
		Models:    models,
		Replace:   replace,
		KeepTakes: func() int { return int(e.keep.Load()) },
		Credit: func(_ context.Context, model, voice string) string {
			if e.credit == nil {
				return ""
			}
			return e.credit(model, voice)
		},
		Watermark: func(ctx context.Context, path string, bits int) (bool, error) {
			if e.wm == nil {
				return false, nil
			}
			return e.wm(ctx, path, bits)
		},
	})
	if err := e.sched.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		e.sched.Close()
		e.svc.Close(ctx)
		st.Close()
	})
	return e
}

func kokoroInfo(string) ModelInfo {
	return ModelInfo{Traits: TraitsFor("kokoro", []string{"speed"}), Revision: "r1", VoiceLanguages: map[string]string{"af_heart": "en-US"}}
}

func omniInfo(string) ModelInfo {
	return ModelInfo{Traits: TraitsFor("omnivoice", []string{"speed", "duration", "clone"}), Revision: "r2"}
}

// waitJob waits for a job to end.
func (e *renderEnv) waitJob(t *testing.T, id string) jobs.Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
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

func (e *renderEnv) generate(t *testing.T, recipe core.Take) (jobs.Job, core.Take) {
	t.Helper()
	if recipe.ID == "" {
		recipe.ID = core.NewID(time.Now())
	}
	id, err := e.svc.Takes.Generate(ctx, core.TakeRequest{Recipe: recipe})
	if err != nil {
		t.Fatal(err)
	}
	j := e.waitJob(t, id)
	if j.State != jobs.StateSucceeded {
		return j, core.Take{}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		tk, err := e.svc.Takes.Get(ctx, recipe.ID)
		if err == nil {
			return j, tk
		}
		if time.Now().After(deadline) {
			t.Fatalf("take: %v", err)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

const longScript = "The first sentence is here. The second sentence follows it closely.\n" +
	"[pause 700ms]A third sentence after a pause.\n\n" +
	"A new paragraph begins, and it carries on for a while."

func TestRenderJoinsMastersAndStreams(t *testing.T) {
	e := newRenderEnv(t, func(string) ModelInfo {
		i := kokoroInfo("")
		i.MaxChars = 40 // several chunks
		return i
	})
	j, tk := e.generate(t, core.Take{Text: longScript, Model: "m/kokoro", Voice: "af_heart", Speed: 1,
		Render: &core.TakeRender{Bits: 24, Master: MasterDefault}})
	if j.State != jobs.StateSucceeded {
		t.Fatalf("job %+v", j.Err)
	}
	calls := e.synth.texts()
	if len(calls) < 4 {
		t.Fatalf("chunks %q", calls)
	}
	for i, id := range e.synth.ids {
		if id != j.ID+"."+strconv.Itoa(i+1) {
			t.Errorf("request id %q", id)
		}
	}
	for _, c := range e.synth.calls {
		if c.ModelPath != "/models/m/kokoro" || c.Output.Bits != 32 || c.Speed == nil || c.Voice != "af_heart" {
			t.Errorf("call %+v", c)
		}
		if c.Seed == nil {
			t.Error("a multi-chunk render shares one seed")
		}
	}
	r := tk.Render
	if r == nil || len(r.Chunks) != len(calls) || r.Bits != 24 || r.Master != MasterDefault || !r.QCDone || r.Bytes == 0 {
		t.Fatalf("render %+v", r)
	}
	for i := 1; i < len(r.Chunks); i++ {
		if r.Chunks[i].Start < r.Chunks[i-1].End-0.06 {
			t.Errorf("chunk %d starts at %v, before %v", i, r.Chunks[i].Start, r.Chunks[i-1].End)
		}
	}
	buf, f, err := wav.ReadFile(e.svc.Takes.AudioPath(tk))
	if err != nil || f.Encoding != wav.PCM24 || f.Rate != 24000 {
		t.Fatalf("file %+v %v", f, err)
	}
	if math.Abs(buf.Seconds()-tk.AudioSeconds) > 0.01 || tk.SampleRate != 24000 || tk.Seed == 0 {
		t.Errorf("take seconds %v file %v rate %d seed %d", tk.AudioSeconds, buf.Seconds(), tk.SampleRate, tk.Seed)
	}
	// Even out volume: broadcast is −18 LUFS.
	if l := loudness.Integrated(buf); math.Abs(l+18) > 1 {
		t.Errorf("loudness %.1f LUFS", l)
	}
	// The pause is there: at least 0.7 s of silence somewhere.
	if !hasSilence(buf.Samples, buf.Rate, 0.65) {
		t.Error("no pause in the take")
	}
	// The preview streamed the joined audio, ending with a final chunk.
	e.mu.Lock()
	streamed, final := len(e.stream[j.ID])/2, e.finals[j.ID]
	progress := append([]RenderProgress(nil), e.progress...)
	e.mu.Unlock()
	if !final || math.Abs(float64(streamed)-float64(buf.Frames())) > 2400 {
		t.Errorf("streamed %d frames of %d, final %v", streamed, buf.Frames(), final)
	}
	if len(progress) != len(calls)+1 || progress[0].Chunk != 1 || progress[0].Chunks != len(calls) || progress[0].Owner != "take:"+tk.ID {
		t.Errorf("progress %+v", progress)
	}

	// The same script with the seed pinned: every chunk from the cache.
	e.synth.reset()
	_, again := e.generate(t, core.Take{Text: longScript, Model: "m/kokoro", Voice: "af_heart", Speed: 1, Seed: tk.Seed,
		Render: &core.TakeRender{SeedPinned: true}})
	if n := len(e.synth.texts()); n != 0 {
		t.Errorf("%d chunks made again", n)
	}
	if again.Seed != tk.Seed || len(again.Render.Chunks) != len(r.Chunks) || !again.Render.Chunks[0].Cached {
		t.Errorf("again %+v", again.Render)
	}
	// Kokoro ignores the seed: an unpinned run uses the cache too.
	_, third := e.generate(t, core.Take{Text: longScript, Model: "m/kokoro", Voice: "af_heart", Speed: 1})
	if n := len(e.synth.texts()); n != 0 || third.Render.Bits != 0 {
		t.Errorf("%d chunks made, render %+v", n, third.Render)
	}
	// One sentence changed: one chunk made.
	_, fourth := e.generate(t, core.Take{Text: strings.Replace(longScript, "third", "different", 1), Model: "m/kokoro", Voice: "af_heart", Speed: 1})
	if got := e.synth.texts(); len(got) != 1 || !strings.Contains(got[0], "different") {
		t.Errorf("made %q", got)
	}
	_ = fourth
}

func hasSilence(s []float32, rate int, seconds float64) bool {
	run := 0
	for _, v := range s {
		if math.Abs(float64(v)) < 0.003 {
			run++
			if float64(run)/float64(rate) >= seconds {
				return true
			}
		} else {
			run = 0
		}
	}
	return false
}

func TestRenderLowersMarkupPerModel(t *testing.T) {
	e := newRenderEnv(t, kokoroInfo)
	_, tk := e.generate(t, core.Take{Text: "Ha [laughter] that is [voice:Ann] fine.", Model: "m/kokoro", Voice: "af_heart"})
	if got := e.synth.texts(); len(got) != 1 || got[0] != "Ha that is fine." {
		t.Errorf("kokoro got %q", got)
	}
	codes := map[string]bool{}
	for _, w := range tk.Render.Warnings {
		codes[w.Code] = true
	}
	if !codes["plan.nonverbal_removed"] || !codes["plan.voice_ignored"] {
		t.Errorf("warnings %+v", tk.Render.Warnings)
	}

	o := newRenderEnv(t, omniInfo)
	_, tk = o.generate(t, core.Take{Text: "Ha [laughter] that is fine.", Model: "m/omni", Voice: "female", Duration: 4, Steps: 16})
	got := o.synth.calls
	if len(got) != 1 || got[0].Text != "Ha [laughter] that is fine." || got[0].Duration == nil || *got[0].Duration != 4 || got[0].Steps != 16 {
		t.Errorf("omnivoice got %+v", got)
	}
	if len(tk.Render.Warnings) != 0 {
		t.Errorf("warnings %+v", tk.Render.Warnings)
	}
}

func TestRenderUsesTheDictionary(t *testing.T) {
	e := newRenderEnv(t, kokoroInfo)
	p := e.svc.Pronunciation
	if err := p.Save(ctx, []lexicon.Entry{{Term: "GIF", Replacement: "jif", Type: lexicon.Respelling, Scope: lexicon.Global, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	e.generate(t, core.Take{Text: "A GIF for $5.", Model: "m/kokoro", Voice: "af_heart"})
	if got := e.synth.texts(); len(got) != 1 || got[0] != "A jif for five dollars." {
		t.Errorf("got %q", got)
	}
	// No normalisation when asked.
	e.synth.reset()
	e.generate(t, core.Take{Text: "A GIF for $5.", Model: "m/kokoro", Voice: "af_heart", Render: &core.TakeRender{NoNormalize: true}})
	if got := e.synth.texts(); len(got) != 1 || got[0] != "A jif for $5." {
		t.Errorf("got %q", got)
	}

	// Export, merge, replace.
	data, err := p.Export(ctx)
	if err != nil || !strings.Contains(string(data), `"jif"`) {
		t.Fatalf("export %s %v", data, err)
	}
	rep, err := p.Import(ctx, []byte(`{"SQL":"sequel","GIF":"gif"}`), lexicon.Merge)
	if err != nil || rep.Added != 1 || rep.Updated != 1 {
		t.Fatalf("merge %+v %v", rep, err)
	}
	if es, _ := p.Entries(ctx); len(es) != 2 || es[0].Replacement != "gif" {
		t.Errorf("entries %+v", es)
	}
	if _, err := p.Import(ctx, []byte(`not json`), lexicon.Merge); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("bad file: %v", err)
	}
	if _, err := p.Import(ctx, data, lexicon.Replace); err != nil {
		t.Fatal(err)
	}
	// Kept in the store.
	p2 := newPronunciation(e.st.Documents(), e.bus)
	if es, _ := p2.Entries(ctx); len(es) != 1 || es[0].Term != "GIF" {
		t.Errorf("stored %+v", es)
	}
}

func TestRenderCancelFailAndDrop(t *testing.T) {
	e := newRenderEnv(t, func(string) ModelInfo { i := kokoroInfo(""); i.MaxChars = 30; return i })
	e.synth.started = make(chan string, 16)

	// Cancel during the second chunk: the first stays in the cache.
	id, err := e.svc.Takes.Generate(ctx, core.TakeRequest{Recipe: core.Take{ID: core.NewID(time.Now()),
		Text: "The first part is fine. Now this one will block.", Model: "m/kokoro", Voice: "af_heart"}})
	if err != nil {
		t.Fatal(err)
	}
	<-e.synth.started
	<-e.synth.started
	e.sched.Cancel(ctx, id)
	if j := e.waitJob(t, id); j.State != jobs.StateCancelled {
		t.Fatalf("state %s", j.State)
	}
	if l, _ := e.svc.Takes.List(ctx, core.TakeQuery{}); len(l) != 0 {
		t.Errorf("takes %v", l)
	}
	entries, _ := os.ReadDir(filepath.Join(e.svc.takes.cacheDir, "chunks"))
	if len(entries) != 1 {
		t.Errorf("cache %v", entries)
	}

	// A failing chunk fails the take, saying which.
	e.synth.started = nil
	j, _ := e.generate(t, core.Take{Text: "This part is fine. But this will fail. And more.", Model: "m/kokoro", Voice: "af_heart"})
	if j.State != jobs.StateFailed || j.Err.Code != proto.CodeOOM || !strings.Contains(j.Err.Message, "Chunk 2 of 2") {
		t.Fatalf("job %s %+v", j.State, j.Err)
	}

	// A chunk much shorter than its words is reported.
	_, tk := e.generate(t, core.Take{Text: "This is a rather long sentence that ends up short in the end.", Model: "m/kokoro", Voice: "af_heart"})
	if len(tk.Render.Warnings) == 0 || tk.Render.Warnings[0].Code != NoticeChunkShort || tk.Render.Warnings[0].Chunk == 0 {
		t.Errorf("warnings %+v", tk.Render.Warnings)
	}
	// Nothing to say.
	j, _ = e.generate(t, core.Take{Text: "[pause 1s]", Model: "m/kokoro"})
	if j.State != jobs.StateFailed || j.Err.Code != CodeNothingToSay {
		t.Errorf("nothing to say: %s %+v", j.State, j.Err)
	}
}

// A file the pipeline cannot read (a scripted engine's) is kept as it is
// when it is the whole take.
func TestRenderKeepsAnUnreadableSingleChunk(t *testing.T) {
	e := newRenderEnv(t, kokoroInfo)
	e.svc.takes.render.synth = func() jobs.Executor {
		return jobs.ExecutorFunc(func(_ context.Context, job jobs.Job, _ func(jobs.Progress), _ func(json.RawMessage)) (json.RawMessage, error) {
			var p proto.SynthesizeParams
			json.Unmarshal(job.Params, &p)
			os.WriteFile(p.Output.Path, []byte("RIFF fake engine output........................................"), 0o644)
			return json.Marshal(proto.SynthesizeResult{Output: p.Output.Path, SampleRate: 24000})
		})
	}
	j, tk := e.generate(t, core.Take{Text: "Hello.", Model: "m/kokoro"})
	if j.State != jobs.StateSucceeded || tk.Render == nil || len(tk.Render.Warnings) != 1 || tk.Render.Warnings[0].Code != NoticePostSkipped {
		t.Fatalf("job %s %+v take %+v", j.State, j.Err, tk.Render)
	}
	if data, _ := os.ReadFile(e.svc.Takes.AudioPath(tk)); !strings.HasPrefix(string(data), "RIFF fake") {
		t.Errorf("file %q", data)
	}
}

func TestPrunePreviewCheckAndExport(t *testing.T) {
	e := newRenderEnv(t, kokoroInfo)
	var ids []string
	for i, text := range []string{"One.", "Two.", "Three.", "Four."} {
		_, tk := e.generate(t, core.Take{Text: text, Model: "m/kokoro", Voice: "af_heart"})
		ids = append(ids, tk.ID)
		if i == 0 {
			if err := e.svc.Takes.SetStarred(ctx, tk.ID, true); err != nil {
				t.Fatal(err)
			}
		}
		time.Sleep(2 * time.Millisecond) // distinct creation times
	}
	e.keep.Store(2)
	n, err := e.svc.Render.Prune(ctx)
	if err != nil || n != 1 {
		t.Fatalf("pruned %d %v", n, err)
	}
	left, _ := e.svc.Takes.List(ctx, core.TakeQuery{})
	if len(left) != 3 || left[2].ID != ids[0] || left[1].ID != ids[2] {
		t.Errorf("left %v", left)
	}
	// Each new take prunes too.
	e.generate(t, core.Take{Text: "Five.", Model: "m/kokoro", Voice: "af_heart"})
	deadline := time.Now().Add(5 * time.Second)
	for left, _ = e.svc.Takes.List(ctx, core.TakeQuery{}); len(left) != 3; left, _ = e.svc.Takes.List(ctx, core.TakeQuery{}) {
		if time.Now().After(deadline) {
			t.Fatalf("after a new take %d", len(left))
		}
		time.Sleep(2 * time.Millisecond)
	}

	// A preview makes a file, not a take.
	before := len(left)
	jobID, out, err := e.svc.Render.Preview(ctx, core.Take{Text: "Testing [[GIF|jif]].", Model: "m/kokoro", Voice: "af_heart"})
	if err != nil {
		t.Fatal(err)
	}
	if j := e.waitJob(t, jobID); j.State != jobs.StateSucceeded || !strings.HasPrefix(j.Owner, "preview:") {
		t.Fatalf("preview %+v", j)
	}
	if _, err := os.Stat(out); err != nil {
		t.Error(err)
	}
	if left, _ = e.svc.Takes.List(ctx, core.TakeQuery{}); len(left) != before {
		t.Errorf("preview made a take")
	}
	if got := e.synth.texts(); got[len(got)-1] != "Testing jif." {
		t.Errorf("preview text %q", got[len(got)-1])
	}

	// Check audio again, and export at another depth.
	tk, err := e.svc.Render.Check(ctx, left[0].ID)
	if err != nil || !tk.Render.QCDone {
		t.Fatalf("check %+v %v", tk.Render, err)
	}
	dst := filepath.Join(t.TempDir(), "x.wav")
	if err := e.svc.Render.ExportAs(ctx, tk.ID, dst, 32); err != nil {
		t.Fatal(err)
	}
	if _, f, err := wav.ReadFile(dst); err != nil || f.Encoding != wav.Float32 {
		t.Errorf("export %+v %v", f, err)
	}
}

// The engine's own limit is lower than the catalogue's: the take is
// planned again with shorter chunks.
func TestRenderReplansWhenTheEngineWantsShorterChunks(t *testing.T) {
	e := newRenderEnv(t, func(string) ModelInfo { i := kokoroInfo(""); i.MaxChars = 200; return i })
	e.synth.limit = 40
	j, tk := e.generate(t, core.Take{Text: longScript, Model: "m/kokoro", Voice: "af_heart"})
	if j.State != jobs.StateSucceeded {
		t.Fatalf("job %s %+v", j.State, j.Err)
	}
	for _, c := range tk.Render.Chunks {
		if n := len([]rune(c.Text)); n > 40 {
			t.Errorf("chunk of %d", n)
		}
	}
}

// TestRenderReplacesRemovedModels: Config.Replace (the app's alias table
// of removed models, thread #43) maps a recipe before Generate checks and
// stores it, and again as every render job starts (Stories, Dub,
// Workflows and Calls submit render jobs of their own), so the engine
// never gets a removed model.
func TestRenderReplacesRemovedModels(t *testing.T) {
	replace := func(tk *core.Take) bool {
		if tk.Model != "old/removed" {
			return false
		}
		tk.Model, tk.Voice = "m/kokoro", "af_heart"
		return true
	}
	e := newRenderEnvWith(t, kokoroInfo, replace)
	j, tk := e.generate(t, core.Take{Text: "Hello there.", Model: "old/removed", Voice: "Bella"})
	if j.State != jobs.StateSucceeded || tk.Model != "m/kokoro" || tk.Voice != "af_heart" {
		t.Fatalf("generate: %s %+v", j.State, tk)
	}
	// A render job submitted directly, as the longform service does.
	params, _ := json.Marshal(RenderParams{Take: core.Take{ID: "x", Text: "Again.", Model: "old/removed", Voice: "Bella"},
		Output: filepath.Join(t.TempDir(), "x.wav")})
	id, err := e.sched.Submit(ctx, jobs.Spec{Kind: KindRender, Lane: jobs.LaneGPU, Params: params})
	if err != nil {
		t.Fatal(err)
	}
	if j := e.waitJob(t, id); j.State != jobs.StateSucceeded {
		t.Fatalf("job %s %+v", j.State, j.Err)
	}
	e.synth.mu.Lock()
	defer e.synth.mu.Unlock()
	for _, c := range e.synth.calls {
		if c.Model != "m/kokoro" || c.Voice != "af_heart" {
			t.Errorf("engine got %s %s", c.Model, c.Voice)
		}
	}
	if len(e.synth.calls) < 2 {
		t.Errorf("%d calls", len(e.synth.calls))
	}
}
