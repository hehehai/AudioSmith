package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine/enginetest"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

func TestMain(m *testing.M) {
	if enginetest.Enabled() {
		enginetest.Main()
	}
	os.Exit(m.Run())
}

// watch records the bus's StatusChanged and LogAppended events.
type watch struct {
	mu   sync.Mutex
	sts  []WorkerStatus
	logs []core.LogEntry
}

func (w *watch) last() WorkerStatus {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.sts) == 0 {
		return WorkerStatus{}
	}
	return w.sts[len(w.sts)-1]
}

// until waits for a published status that satisfies ok.
func (w *watch) until(t *testing.T, what string, ok func(WorkerStatus) bool) WorkerStatus {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		w.mu.Lock()
		for _, st := range w.sts {
			if ok(st) {
				w.mu.Unlock()
				return st
			}
		}
		w.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("no status %s; last %+v", what, w.last())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (w *watch) reset() {
	w.mu.Lock()
	w.sts = nil
	w.mu.Unlock()
}

func ready(st WorkerStatus) bool { return st.State == WorkerReady }

func newTestSup(t *testing.T, opts string, mod ...func(*Config)) (Supervisor, *watch) {
	t.Helper()
	bus := core.NewBus()
	w := &watch{}
	core.On(bus, func(e StatusChanged) { w.mu.Lock(); w.sts = append(w.sts, e.Status); w.mu.Unlock() })
	core.On(bus, func(e core.LogAppended) { w.mu.Lock(); w.logs = append(w.logs, e.Entry); w.mu.Unlock() })
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Bin: bin, AppVersion: "test", Bus: bus, LogDir: t.TempDir(), Env: enginetest.Env(opts),
		StatsEvery: 20 * time.Millisecond, Logf: t.Logf}
	for _, m := range mod {
		m(&cfg)
	}
	s := NewSupervisor(cfg)
	t.Cleanup(s.Stop)
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s, w
}

// jobStarted returns a notify for a job's call and a wait that returns
// once the job's first notification arrived: the engine runs the job.
func jobStarted() (func(proto.Message), func(*testing.T)) {
	ch := make(chan struct{})
	var once sync.Once
	notify := func(proto.Message) { once.Do(func() { close(ch) }) }
	wait := func(t *testing.T) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(10 * time.Second):
			t.Fatal("the job did not start")
		}
	}
	return notify, wait
}

func synth(text string, stream bool) proto.SynthesizeParams {
	return proto.SynthesizeParams{Model: "mlx-community/Kokoro-82M-bf16", Text: text, Stream: stream}
}

func TestSupervisorHandshakeAndJobs(t *testing.T) {
	s, w := newTestSup(t, "")
	st := w.until(t, "ready", ready)
	if st.Hello == nil || st.Hello.Version != "fake" || st.Hello.Concurrency != 1 || st.PID == 0 || st.Role != proto.RoleGPU {
		t.Fatalf("ready status %+v", st)
	}
	// The engine's log notification reached the bus.
	w.mu.Lock()
	logs := slices.Clone(w.logs)
	w.mu.Unlock()
	var sawLog, sawStderr bool
	for _, l := range logs {
		sawLog = sawLog || (l.Message == "fake engine ready" && l.Source == "engine:gpu")
		sawStderr = sawStderr || strings.HasPrefix(l.Message, "fake engine starting")
	}
	if !sawLog || !sawStderr {
		t.Errorf("logs %+v", logs)
	}

	// A streamed job: notifications in order, before the result.
	var got []string
	out := filepath.Join(t.TempDir(), "x.wav")
	p := synth("hello", true)
	p.Output.Path = out
	raw, err := s.Call(context.Background(), proto.RoleGPU, "J1", proto.MethodTTSSynthesize, p, func(m proto.Message) {
		switch m.Method {
		case proto.MethodJobProgress:
			var pp proto.ProgressParams
			m.DecodeParams(&pp)
			got = append(got, pp.Stage)
		case proto.MethodTTSChunk:
			var cp proto.ChunkParams
			m.DecodeParams(&cp)
			got = append(got, "chunk")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	var res proto.SynthesizeResult
	json.Unmarshal(raw, &res)
	if res.Output != out || res.SampleRate != 24000 || res.AudioSeconds == 0 {
		t.Errorf("result %+v", res)
	}
	want := []string{"loading", "generating", "chunk", "generating", "chunk", "generating", "chunk"}
	if !slices.Equal(got, want) {
		t.Errorf("notifications %v", got)
	}
	if _, err := os.Stat(out); err != nil {
		t.Error(err)
	}

	// Stats are polled after jobs: the loaded model shows in the status.
	w.until(t, "with stats", func(st WorkerStatus) bool { return st.Stats != nil && len(st.Stats.Models) == 1 })

	// Errors arrive as *proto.Error, matched by code.
	_, err = s.Call(context.Background(), proto.RoleGPU, "J2", proto.MethodModelsLoad, proto.LoadParams{Model: "x/broken"}, nil)
	if !errors.Is(err, &proto.Error{Code: proto.CodeModelLoadFailed}) {
		t.Errorf("load broken: %v", err)
	}
	_, err = s.Call(context.Background(), proto.RoleGPU, "", "lid.detect", nil, nil)
	if !errors.Is(err, &proto.Error{Code: proto.CodeMethodNotFound}) {
		t.Errorf("unknown method: %v", err)
	}
	if _, err := s.Call(context.Background(), proto.RoleListen, "", proto.MethodStats, nil, nil); !errors.Is(err, core.ErrEngine) {
		t.Errorf("unconfigured role: %v", err)
	}

	// Control requests are answered while a job runs.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	notify, waitStarted := jobStarted()
	go func() {
		_, err := s.Call(ctx, proto.RoleGPU, "J3", proto.MethodTTSSynthesize, synth("slow", false), notify)
		done <- err
	}()
	waitStarted(t)
	raw, err = s.Call(context.Background(), proto.RoleGPU, "", proto.MethodStats, nil, nil)
	var stats proto.StatsResult
	json.Unmarshal(raw, &stats)
	if err != nil || !slices.Equal(stats.Jobs, []string{"J3"}) {
		t.Errorf("stats during a job: %+v %v", stats, err)
	}
	if run := s.Status()[0].Running; !slices.Equal(run, []string{"J3"}) {
		t.Errorf("running %v", run)
	}
	// Cancelling the context cancels the job by id.
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, &proto.Error{Code: proto.CodeCancelled}) {
			t.Errorf("cancelled job: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not end the job")
	}
	if st := s.Status()[0]; st.State != WorkerReady || st.Restarts != 0 {
		t.Errorf("after cancel %+v", st)
	}
}

// A job sent as soon as the previous one is answered is never busy: the
// engine frees a job's slot before it writes the job's response (the
// scheduler starts the next job of a lane as soon as one ends).
func TestSupervisorBackToBackJobs(t *testing.T) {
	s, w := newTestSup(t, "")
	w.until(t, "ready", ready)
	ctx := context.Background()
	for i := range 200 {
		id := "B" + strconv.Itoa(i)
		if _, err := s.Call(ctx, proto.RoleGPU, id, proto.MethodModelsLoad, proto.LoadParams{Model: "a/one"}, nil); err != nil {
			t.Fatalf("job %d: %v", i, err)
		}
	}
}

func TestSupervisorReplaysFixtures(t *testing.T) {
	for _, name := range []string{"tts_stream", "tts_clone", "models", "cancel", "stt", "analysis", "voice"} {
		t.Run(name, func(t *testing.T) {
			path, _ := filepath.Abs(filepath.Join("..", "..", "engine", "Fixtures", name+".jsonl"))
			s, w := newTestSup(t, "replay="+path, func(c *Config) { c.StatsEvery = -1 })
			w.until(t, "ready", ready)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			type line struct {
				To  string          `json:"to"`
				Msg json.RawMessage `json:"msg"`
			}
			var lines []line
			sc := bufio.NewScanner(bytes.NewReader(data))
			for sc.Scan() {
				var l line
				json.Unmarshal(sc.Bytes(), &l)
				lines = append(lines, l)
			}
			compared := 0
			defer func() {
				if compared < 1 {
					t.Errorf("compared %d requests", compared)
				}
			}()
			for i := 0; i < len(lines); i++ {
				if lines[i].To != "engine" {
					continue
				}
				var req proto.Message
				json.Unmarshal(lines[i].Msg, &req)
				// What the fixture says comes back for this request, in order.
				var wantNotes []string
				var want proto.Message
				for _, l := range lines[i+1:] {
					if l.To != "app" {
						break
					}
					var m proto.Message
					json.Unmarshal(l.Msg, &m)
					if m.Method == proto.MethodEngineMemory {
						continue // names no job: the supervisor's own (supervise_pressure.go)
					}
					if m.Kind() == proto.KindNotification {
						wantNotes = append(wantNotes, string(m.Params))
					} else if m.ID == req.ID {
						want = m
					}
				}
				id := ""
				if proto.IsJob(req.Method) {
					id = req.ID
				}
				ctx, cancel := context.WithCancel(context.Background())
				var notes []string
				notify := func(m proto.Message) {
					notes = append(notes, string(m.Params))
					if name == "cancel" && m.Method == proto.MethodJobProgress {
						cancel() // the fixture's job.cancel follows the first progress
					}
				}
				raw, err := s.Call(ctx, proto.RoleGPU, id, req.Method, req.Params, notify)
				cancel()
				if name == "cancel" && req.Method == proto.MethodTTSSynthesize {
					// The supervisor sent job.cancel itself; the fixture's
					// cancel answer and the job's error are consumed.
					if !errors.Is(err, &proto.Error{Code: proto.CodeCancelled}) {
						t.Fatalf("cancelled job: %v", err)
					}
					i += 4 // progress, cancel request, its answer, the job's error
					continue
				}
				compared++
				if !jsonEqualNotes(notes, wantNotes) {
					t.Errorf("%s: notifications\n%v\nwant\n%v", req.Method, notes, wantNotes)
				}
				if want.Error != nil {
					if !errors.Is(err, &proto.Error{Code: want.Error.Code}) {
						t.Errorf("%s: error %v, want %s", req.Method, err, want.Error.Code)
					}
				} else if err != nil || !jsonEqual(raw, want.Result) {
					t.Errorf("%s: result %s %v, want %s", req.Method, raw, err, want.Result)
				}
			}
		})
	}
}

func jsonEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ja, _ := json.Marshal(x)
	jb, _ := json.Marshal(y)
	return bytes.Equal(ja, jb)
}

func jsonEqualNotes(a, b []string) bool {
	return slices.EqualFunc(a, b, func(x, y string) bool { return jsonEqual([]byte(x), []byte(y)) })
}

func TestSupervisorRefusesOtherProtocols(t *testing.T) {
	for opts, want := range map[string]string{
		"legacy":      "speaks protocol 1",
		"mismatch":    "it speaks protocol 3",
		"refuse":      "version mismatch",
		"role=listen": `role "listen"`,
	} {
		t.Run(opts, func(t *testing.T) {
			s, w := newTestSup(t, opts)
			st := w.until(t, "failed", func(st WorkerStatus) bool { return st.State == WorkerFailed })
			if st.Reason != FailProtocol || !strings.Contains(st.Err, want) || !st.RestartAt.IsZero() {
				t.Fatalf("status %+v", st)
			}
			time.Sleep(50 * time.Millisecond)
			if st := s.Status()[0]; st.State != WorkerFailed || st.Restarts != 0 {
				t.Errorf("restarted: %+v", st)
			}
			if _, err := s.Call(context.Background(), proto.RoleGPU, "", proto.MethodStats, nil, nil); !errors.Is(err, core.ErrEngine) {
				t.Errorf("call to a refused engine: %v", err)
			}
		})
	}
}

func TestSupervisorHandshakeTimeout(t *testing.T) {
	_, w := newTestSup(t, "silent", func(c *Config) { c.HelloTimeout = 100 * time.Millisecond })
	st := w.until(t, "failed", func(st WorkerStatus) bool { return st.State == WorkerFailed })
	if st.Reason != FailHandshake || st.RestartAt.IsZero() {
		t.Fatalf("status %+v", st)
	}
}

func TestSupervisorMissingBinary(t *testing.T) {
	_, w := newTestSup(t, "", func(c *Config) { c.Bin = "" })
	st := w.until(t, "failed", func(st WorkerStatus) bool { return st.State == WorkerFailed })
	if st.Reason != FailMissing || !strings.Contains(st.Err, "build-engine.sh") {
		t.Fatalf("status %+v", st)
	}
	_, w = newTestSup(t, "", func(c *Config) { c.Bin = "/nonexistent/audiosmith-engine" })
	st = w.until(t, "failed", func(st WorkerStatus) bool { return st.State == WorkerFailed })
	if st.Reason != FailStart {
		t.Fatalf("status %+v", st)
	}
}

func TestSupervisorCrashAndRestart(t *testing.T) {
	var prepared atomic.Int32
	s, w := newTestSup(t, "", func(c *Config) {
		c.MaxRestarts = 1
		c.Prepare = func(proto.Role) { prepared.Add(1) }
	})
	first := w.until(t, "ready", ready)
	_, err := s.Call(context.Background(), proto.RoleGPU, "J1", proto.MethodTTSSynthesize, synth("crash", false), nil)
	if !errors.Is(err, ErrWorkerExited) || !errors.Is(err, core.ErrEngine) {
		t.Fatalf("call to a crashing engine: %v", err)
	}
	st := w.until(t, "crashed", func(st WorkerStatus) bool { return st.State == WorkerFailed })
	if st.Reason != FailCrashed || !strings.Contains(st.StderrTail, "fatal: boom") || st.RestartAt.IsZero() {
		t.Fatalf("crash status %+v", st)
	}
	// Calls while it is down wait for the restart.
	w.reset()
	if _, err := s.Call(context.Background(), proto.RoleGPU, "", proto.MethodStats, nil, nil); err != nil {
		t.Errorf("call while down: %v", err)
	}
	st = w.until(t, "restarted", ready)
	if st.Restarts != 1 || st.PID == first.PID {
		t.Fatalf("after restart %+v", st)
	}
	if n := prepared.Load(); n != 2 {
		t.Errorf("Prepare ran %d times for 2 starts", n)
	}
	log, _ := os.ReadFile(filepath.Join(s.(*supervisor).cfg.LogDir, "engine-gpu.log"))
	if !strings.Contains(string(log), "fatal: boom") {
		t.Errorf("log %q", log)
	}

	// The restart budget is spent: the next crash stays failed.
	s.Call(context.Background(), proto.RoleGPU, "J2", proto.MethodTTSSynthesize, synth("crash", false), nil)
	st = w.until(t, "crashed again", func(st WorkerStatus) bool { return st.State == WorkerFailed })
	if !st.RestartAt.IsZero() {
		t.Fatalf("restart planned beyond the budget: %+v", st)
	}
	// Restart by hand resets the budget.
	w.reset()
	if err := s.Restart(proto.RoleGPU); err != nil {
		t.Fatal(err)
	}
	if st := w.until(t, "ready", ready); st.Restarts != 0 {
		t.Errorf("restarts %d", st.Restarts)
	}
}

func TestSupervisorKillsStuckJobs(t *testing.T) {
	s, w := newTestSup(t, "hang-cancel", func(c *Config) { c.CancelGrace = 100 * time.Millisecond })
	w.until(t, "ready", ready)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := s.Call(ctx, proto.RoleGPU, "J1", proto.MethodTTSSynthesize, synth("slow", false), nil)
	if !errors.Is(err, ErrWorkerExited) {
		t.Fatalf("stuck job: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %s", d)
	}
	st := w.until(t, "killed", func(st WorkerStatus) bool { return st.State == WorkerFailed })
	if st.Reason != FailStuck {
		t.Fatalf("status %+v", st)
	}
	w.reset()
	w.until(t, "restarted", ready)
}

func TestSupervisorStopAndRestartDuringCalls(t *testing.T) {
	s, w := newTestSup(t, "")
	w.until(t, "ready", ready)
	done := make(chan error, 1)
	notify, waitStarted := jobStarted()
	go func() {
		_, err := s.Call(context.Background(), proto.RoleGPU, "J1", proto.MethodTTSSynthesize, synth("slow", false), notify)
		done <- err
	}()
	waitStarted(t)
	w.reset()
	if err := s.Restart(proto.RoleGPU); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrWorkerExited) {
		t.Errorf("call across a restart: %v", err)
	}
	w.until(t, "ready", ready)

	// A call made while it starts waits for the handshake.
	s.Restart(proto.RoleGPU)
	if _, err := s.Call(context.Background(), proto.RoleGPU, "", proto.MethodStats, nil, nil); err != nil {
		t.Errorf("call while starting: %v", err)
	}

	notify, waitStarted = jobStarted()
	go func() {
		_, err := s.Call(context.Background(), proto.RoleGPU, "J2", proto.MethodTTSSynthesize, synth("slow", false), notify)
		done <- err
	}()
	waitStarted(t)
	start := time.Now()
	s.Stop()
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("Stop took %s", d)
	}
	if err := <-done; !errors.Is(err, core.ErrEngine) {
		t.Errorf("call across Stop: %v", err)
	}
	if st := s.Status()[0]; st.State != WorkerStopped || st.PID != 0 {
		t.Errorf("after Stop %+v", st)
	}
	if _, err := s.Call(context.Background(), proto.RoleGPU, "", proto.MethodStats, nil, nil); !errors.Is(err, core.ErrEngine) {
		t.Errorf("call after Stop: %v", err)
	}
}

func TestSupervisorEvictsOverBudget(t *testing.T) {
	s, w := newTestSup(t, "", func(c *Config) { c.MemoryBudget = 150 }) // the fake's models are 100 bytes each
	w.until(t, "ready", ready)
	ctx := context.Background()
	if _, err := s.Call(ctx, proto.RoleGPU, "J1", proto.MethodModelsLoad, proto.LoadParams{Model: "a/one"}, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond) // "one" becomes the least recently used
	if _, err := s.Call(ctx, proto.RoleGPU, "J2", proto.MethodModelsLoad, proto.LoadParams{Model: "b/two"}, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, _ := s.Call(ctx, proto.RoleGPU, "", proto.MethodModelsLoaded, nil, nil)
		var res proto.LoadedResult
		json.Unmarshal(raw, &res)
		if len(res.Models) == 1 && res.Models[0].Model == "b/two" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("still loaded: %+v", res.Models)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// loadedModels lists the fake engine's resident models.
func loadedModels(t *testing.T, s Supervisor) []string {
	t.Helper()
	raw, err := s.Call(context.Background(), proto.RoleGPU, "", proto.MethodModelsLoaded, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var res proto.LoadedResult
	json.Unmarshal(raw, &res)
	var ids []string
	for _, m := range res.Models {
		ids = append(ids, m.Model)
	}
	return ids
}

func waitLoaded(t *testing.T, s Supervisor, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := loadedModels(t, s)
		if len(got) == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("loaded %v, want %d models", got, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The settings change the budget and the idle limit while engines run.
func TestSupervisorLimitsAtRunTime(t *testing.T) {
	s, w := newTestSup(t, "")
	w.until(t, "ready", ready)
	ctx := context.Background()
	for _, m := range []string{"a/one", "b/two"} {
		if _, err := s.Call(ctx, proto.RoleGPU, "L-"+m, proto.MethodModelsLoad, proto.LoadParams{Model: m}, nil); err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	waitLoaded(t, s, 2)
	w.until(t, "stats with both models", func(st WorkerStatus) bool { return st.Stats != nil && len(st.Stats.Models) == 2 })

	s.SetMemoryBudget(150) // one 100-byte model fits
	waitLoaded(t, s, 1)
	if got := loadedModels(t, s); got[0] != "b/two" {
		t.Errorf("kept %v, want the most recent", got)
	}

	s.SetMemoryBudget(0)
	s.SetIdleUnload(50 * time.Millisecond)
	waitLoaded(t, s, 0)
}

// #101 M5: an engine that stops reading its stdin does not block callers:
// a write heeds its context, one that cannot finish within WriteTimeout
// kills (and restarts) the worker, and Stop returns within its bounds.
func TestSupervisorStalledStdin(t *testing.T) {
	s, w := newTestSup(t, "stall-stdin", func(c *Config) {
		c.WriteTimeout = 1500 * time.Millisecond
		c.StopTimeout = 200 * time.Millisecond
		c.StatsEvery = -1
	})
	w.until(t, "ready", ready)
	big := synth(strings.Repeat("x", 512<<10), false) // over a pipe's buffer

	// The caller's context ends first, while the write is stalled (not
	// before it starts: 500 ms leaves room for encoding the request under
	// -race on a slow runner), and the call returns at once.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := s.Call(ctx, proto.RoleGPU, "J1", proto.MethodTTSSynthesize, big, nil); err == nil {
		t.Fatal("a call to a stalled engine succeeded")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("the cancelled call took %s", d)
	}
	st := w.until(t, "killed", func(st WorkerStatus) bool { return st.State == WorkerFailed })
	if st.Reason != FailStuck {
		t.Fatalf("status %+v", st)
	}
	w.reset()
	w.until(t, "restarted", ready)

	// No deadline of its own: WriteTimeout ends it.
	start = time.Now()
	if _, err := s.Call(context.Background(), proto.RoleGPU, "J2", proto.MethodTTSSynthesize, big, nil); !errors.Is(err, ErrWorkerExited) {
		t.Fatalf("a call to a stalled engine: %v", err)
	}
	if d := time.Since(start); d > 6*time.Second {
		t.Errorf("the call took %s", d)
	}
	w.until(t, "restarted", ready)

	// Stop does not hang on it either.
	done := make(chan struct{})
	go func() { s.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop hangs on a stalled engine")
	}
}
