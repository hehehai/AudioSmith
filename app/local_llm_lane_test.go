package main

// The engine's language model takes its turn on the gpu lane (thread
// #53), over the real scheduler and supervisor with the test binary as a
// scripted engine (one gpu job at a time: a second request gets busy).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/engine/enginetest"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/llm"
	"github.com/hehehai/AudioSmith/internal/translate"
)

// laneTest is an app over the real scheduler and a scripted engine, with
// the engine's language model downloaded and first in the routing.
type laneTest struct {
	a      *app
	lib    *library
	events chan func()
}

func newLaneTest(t *testing.T) *laneTest {
	t.Helper()
	lib := openTestLibrary(t)
	cache := useLocalModels(t, lib)
	e, _ := catalog.Builtin().ByID(catalog.DefaultLLM)
	seed := func(repo, rev string, files []catalog.File) {
		for _, f := range files {
			p := filepath.Join(hub.RepoDir(cache, repo), "snapshots", rev, filepath.FromSlash(f.Path))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	seed(e.Repo, e.Revision, e.Files)
	if e.Windows != nil && e.Windows.Repo != "" {
		seed(e.Windows.Repo, e.Windows.Revision, e.Windows.Files)
	}
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	h := &engineHost{bin: bin, logDir: t.TempDir()}
	a := newApp(h, lib)
	a.autoplay = false
	events := make(chan func(), 4096)
	a.enginePost = func(fn func()) { events <- fn }
	a.postQueue = a.enginePost
	h.sup = engine.NewSupervisor(engine.Config{Bin: bin, Bus: a.bus, LogDir: h.logDir, Env: enginetest.Env(""), Logf: t.Logf})
	a.connectEngine(h)
	t.Cleanup(h.Stop)
	lt := &laneTest{a: a, lib: lib, events: events}
	a.startEngine()
	lt.pump(t, "ready", func() bool { return a.engState == engineReady })
	a.setLocal(e, hub.Local{State: hub.Downloaded, Snapshot: t.TempDir()}, nil)
	if !a.localLLMFirst(llm.SkillTranslate) {
		t.Fatal("the engine's language model is not first")
	}
	return lt
}

// pump runs posted work until done holds.
func (lt *laneTest) pump(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.After(20 * time.Second)
	for !done() {
		select {
		case fn := <-lt.events:
			fn()
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func (lt *laneTest) state(id string) jobs.Job {
	j, _ := lt.lib.sched.Get(context.Background(), id)
	return j
}

// render queues a take; "slow" in the text makes it take ~2 s.
func (lt *laneTest) render(t *testing.T, text string, background bool) string {
	t.Helper()
	id, err := lt.lib.svc.Takes.Generate(context.Background(), core.TakeRequest{Recipe: core.Take{Text: text, Model: kokoro}, Background: background})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

type chatResult struct {
	resp   llm.Response
	err    error
	deltas []string
	first  time.Time // when the first token arrived
}

// chat asks the routed model in the background, streaming.
func (lt *laneTest) chat(ctx context.Context, text string) chan chatResult {
	ch := make(chan chatResult, 1)
	router := lt.a.llmRouter
	go func() {
		var r chatResult
		var mu sync.Mutex
		r.resp, r.err = router.Chat(ctx, llm.SkillTranslate, llm.Request{Messages: []llm.Message{{Role: llm.User, Content: text}},
			OnDelta: func(d string) {
				mu.Lock()
				defer mu.Unlock()
				if r.first.IsZero() {
					r.first = time.Now()
				}
				r.deltas = append(r.deltas, d)
			}})
		ch <- r
	}()
	return ch
}

func waitChat(t *testing.T, ch chan chatResult) chatResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(20 * time.Second):
		t.Fatal("the language model never answered")
	}
	return chatResult{}
}

// A request made while a render runs waits for it (no busy), streams its
// tokens, and goes before a batch render queued earlier; the batch render
// runs after it.
func TestLocalLLMWaitsForTheGPULane(t *testing.T) {
	lt := newLaneTest(t)
	running := lt.render(t, "slow render", false)
	lt.pump(t, "render running", func() bool { return lt.state(running).State == jobs.StateRunning })
	batch := lt.render(t, "batch line", true)

	r := waitChat(t, lt.chat(context.Background(), "hello there friend"))
	if r.err != nil {
		t.Fatalf("chat during a render: %v", r.err)
	}
	if r.resp.Text != "echo: hello there friend" || strings.Join(r.deltas, "") != r.resp.Text || len(r.deltas) < 2 {
		t.Errorf("reply %q, streamed %q", r.resp.Text, r.deltas)
	}
	ren := lt.state(running)
	if ren.State != jobs.StateSucceeded || r.first.Before(ren.Finished) {
		t.Errorf("render %s, finished %v; first token %v", ren.State, ren.Finished, r.first)
	}
	lt.pump(t, "batch done", func() bool { return lt.state(batch).State == jobs.StateSucceeded })
	if b := lt.state(batch); b.Started.Before(r.first) {
		t.Errorf("the batch render (started %v) went before the language model (%v)", b.Started, r.first)
	}
	if js, _ := lt.lib.sched.List(context.Background(), jobs.Filter{States: []jobs.State{jobs.StateSucceeded}}); len(js) != 2 {
		t.Errorf("listed %d jobs, want the 2 renders only", len(js))
	}
}

// Cancelling a request while it waits frees its place: the render goes on
// and the queue after it is not held up.
func TestLocalLLMCancelWhileQueued(t *testing.T) {
	lt := newLaneTest(t)
	running := lt.render(t, "slow render", false)
	lt.pump(t, "render running", func() bool { return lt.state(running).State == jobs.StateRunning })
	ctx, cancel := context.WithCancel(context.Background())
	ch := lt.chat(ctx, "never mind")
	time.Sleep(100 * time.Millisecond)
	cancel()
	start := time.Now()
	r := waitChat(t, ch)
	if llm.Code(r.err) != llm.CodeCancelled || len(r.deltas) > 0 {
		t.Fatalf("cancelled request: %v %q", r.err, r.deltas)
	}
	if time.Since(start) > time.Second {
		t.Errorf("the cancel took %v", time.Since(start))
	}
	if st := lt.state(running).State; st != jobs.StateRunning {
		t.Errorf("render %s", st)
	}
	next := lt.render(t, "next", false)
	lt.pump(t, "next done", func() bool { return lt.state(next).State == jobs.StateSucceeded })
	if st := lt.state(running).State; st != jobs.StateSucceeded {
		t.Errorf("render %s", st)
	}
}

// Work inside a gpu job (a dub's translation on the gpu lane) calls the
// language model directly: no deadlock on its own lane, and it does not
// collide with anything since the job holds the lane.
func TestLocalLLMInsideAGPUJob(t *testing.T) {
	lt := newLaneTest(t)
	router := lt.a.llmRouter
	got := make(chan string, 1)
	lt.lib.sched.Register("test.translate", jobs.ExecutorFunc(func(ctx context.Context, job jobs.Job, _ func(jobs.Progress), _ func(json.RawMessage)) (json.RawMessage, error) {
		resp, err := router.Chat(ctx, llm.SkillTranslate, llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "bonjour"}}})
		if err != nil {
			return nil, err
		}
		got <- resp.Text
		return nil, nil
	}))
	id, err := lt.lib.sched.Submit(context.Background(), jobs.Spec{Kind: "test.translate", Lane: translateLane(router), Title: "translate", Params: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if lane := lt.state(id).Lane; lane != jobs.LaneGPU {
		t.Fatalf("translation lane %s", lane)
	}
	select {
	case text := <-got:
		if text != "echo: bonjour" {
			t.Errorf("reply %q", text)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the translation deadlocked on its own lane")
	}
	lt.pump(t, "job done", func() bool { return lt.state(id).State == jobs.StateSucceeded })
}

// Dictation clean-up never holds up the paste behind a render: with a gpu
// job running it types the polished text at once (FallbackBusy); behind
// the language model's own work it waits at most its budget, from when it
// was asked.
func TestCleanupBehindARender(t *testing.T) {
	lt := newLaneTest(t)
	a := lt.a
	running := lt.render(t, "slow render", false)
	lt.pump(t, "render running", func() bool {
		return lt.state(running).State == jobs.StateRunning && a.gpuLaneBusy()
	})
	cleaner := &translate.Cleaner{Router: a.llmRouter}
	res := cleaner.Clean(context.Background(), translate.CleanupRequest{Text: "um so meet me at five okay", Lang: "en", Busy: a.gpuLaneBusy()})
	if res.LLM || res.Fallback != translate.FallbackBusy || res.Elapsed > 100*time.Millisecond {
		t.Errorf("busy clean-up %+v", res)
	}

	// Without the busy check, the wall-clock bound dictation puts around it
	// still ends it within the budget, while the render runs.
	wall := 300 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), wall)
	start := time.Now()
	res = cleaner.Clean(ctx, translate.CleanupRequest{Text: "um so meet me at five okay", Lang: "en", Budget: wall})
	cancel()
	if res.LLM || res.Fallback != translate.FallbackTimeout || time.Since(start) > wall+500*time.Millisecond {
		t.Errorf("bounded clean-up after %v: %+v", time.Since(start), res)
	}
	if st := lt.state(running).State; st != jobs.StateRunning {
		t.Fatalf("render %s", st)
	}

	// Its own budget counts from its turn: a 300 ms budget behind the ~2 s
	// render still gets an answer (transcript clean-up waits like this).
	// (The scripted model's reply is no clean-up: what matters is that it ran.)
	res = cleaner.Clean(context.Background(), translate.CleanupRequest{Text: "um so meet me at five okay", Lang: "en", Budget: wall})
	if res.Fallback == translate.FallbackTimeout || res.Usage.PromptTokens == 0 || res.Detail == llm.CodeServer {
		t.Errorf("clean-up behind the render: %+v", res)
	}
	if st := lt.state(running).State; st != jobs.StateSucceeded {
		t.Errorf("render %s", st)
	}
}

// The direction coach waits for a render at most coachQueueWait, saying
// so, then reads the note by keywords.
func TestCoachBehindARender(t *testing.T) {
	lt := newLaneTest(t)
	a := lt.a
	defer func(d time.Duration) { coachQueueWait = d }(coachQueueWait)
	coachQueueWait = 400 * time.Millisecond
	running := lt.render(t, "slow render", false)
	lt.pump(t, "render running", func() bool { return lt.state(running).State == jobs.StateRunning })
	a.dir.note = "more urgent, a little scared"
	a.runDirection()
	lt.pump(t, "waiting shown", func() bool { return a.dir.waiting })
	lt.pump(t, "keywords", func() bool { return !a.dir.busy })
	if res := a.dir.res; res == nil || res.Source != "keywords" || res.Fallback != coachFallbackBusy || len(res.Emotions) == 0 {
		t.Fatalf("coach behind a render: %+v", res)
	}
	if a.dir.waiting {
		t.Error("still waiting")
	}
	if st := lt.state(running).State; st != jobs.StateRunning {
		t.Errorf("render %s", st)
	}
}
