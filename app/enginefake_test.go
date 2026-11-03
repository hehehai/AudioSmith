package main

// Fakes of the engine side for the view tests: a scheduler that records
// submissions and a supervisor that records calls. The test plays the
// engine by publishing jobs.Updated and engine.StatusChanged on the app's
// bus (synchronously, so views see the result at once).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/engine/enginetest"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
)

// sentReq is what the app asked of the engine side.
type sentReq struct {
	ID  string
	Cmd string // "synthesize", "load", "download", "cancel", "unload", or the method / job kind
	proto.SynthesizeParams
	Params json.RawMessage // the job's or call's params, whatever the kind
	Spec   jobs.Spec
}

// fakeEngine plays the engine side for view tests. Jobs (any kind) are
// recorded and finished by the test (succeed, fail, progress…). Direct
// calls (fakeSup.Call) answer at once: media.probe with probes[path] (or
// a 3 s mono WAV), capture.devices with enginetest.FakeDevices, and any
// method in callErr with that error. capture.start reports "recording",
// sends levels, then waits for capture.stop or cancellation and answers
// with a CaptureResult whose file the test may need to create.
type fakeEngine struct {
	host      *engineHost
	a         *app
	sent      []sentReq
	jobs      map[string]jobs.Job
	executors map[string]jobs.Executor
	restarts  int
	// What the settings told the supervisor.
	budget   int64
	idle     time.Duration
	cacheDir string

	mu       sync.Mutex
	probes   map[string]proto.ProbeResult
	callErr  map[string]*proto.Error // method → error to answer with
	levels   []proto.LevelParams     // sent by capture.start (job id filled in)
	captures map[string]chan struct{}
	holds    []proto.Role // Hold calls not released
}

// held lists the roles held (fakeSup.Hold) now.
func (f *fakeEngine) held() []proto.Role {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.holds)
}

func newFakeEngine() *fakeEngine {
	f := &fakeEngine{jobs: map[string]jobs.Job{}, executors: map[string]jobs.Executor{},
		probes: map[string]proto.ProbeResult{}, callErr: map[string]*proto.Error{}, captures: map[string]chan struct{}{},
		levels: []proto.LevelParams{{Seconds: 0.05, RMS: -30, Peak: -18}, {Seconds: 0.1, RMS: -24, Peak: -12}}}
	f.host = &engineHost{bin: "/fake/audiosmith-engine", sup: &fakeSup{f}, sched: fakeSched{f}}
	return f
}

// lastOf returns the last request of kind cmd.
func (f *fakeEngine) lastOf(t *testing.T, cmd string) sentReq {
	t.Helper()
	for i := len(f.sent) - 1; i >= 0; i-- {
		if f.sent[i].Cmd == cmd {
			return f.sent[i]
		}
	}
	t.Fatalf("no %s was sent to the engine (sent %v)", cmd, f.sent)
	return sentReq{}
}

func (f *fakeEngine) last(t *testing.T) sentReq {
	t.Helper()
	if len(f.sent) == 0 {
		t.Fatal("nothing was sent to the engine")
	}
	return f.sent[len(f.sent)-1]
}

// ready publishes the gpu worker as ready.
func (f *fakeEngine) ready() {
	f.status(engine.WorkerStatus{Role: proto.RoleGPU, State: engine.WorkerReady, PID: 42,
		Hello: &proto.HelloResult{Protocol: 2, Version: "test", Role: proto.RoleGPU, Concurrency: 1}})
}

func (f *fakeEngine) status(st engine.WorkerStatus) {
	if st.Since.IsZero() {
		st.Since = time.Now()
	}
	f.a.bus.Publish(engine.StatusChanged{Status: st})
}

func (f *fakeEngine) update(id string, mod func(*jobs.Job)) {
	j, ok := f.jobs[id]
	if !ok {
		panic("unknown job " + id)
	}
	mod(&j)
	f.jobs[id] = j
	f.a.bus.Publish(jobs.Updated{Job: j})
}

// progress reports a running job's stage; fraction -1 is unknown.
func (f *fakeEngine) progress(id, stage string, fraction, audioSeconds float64) {
	f.update(id, func(j *jobs.Job) {
		j.State = jobs.StateRunning
		j.Progress = jobs.Progress{Stage: stage, Fraction: fraction, AudioSeconds: audioSeconds}
	})
}

func (f *fakeEngine) succeed(id string, result any) {
	raw, _ := json.Marshal(result)
	f.update(id, func(j *jobs.Job) { j.State, j.Result = jobs.StateSucceeded, raw })
}

func (f *fakeEngine) fail(id, code, message string) {
	f.update(id, func(j *jobs.Job) { j.State, j.Err = jobs.StateFailed, &jobs.Error{Code: code, Message: message} })
}

func (f *fakeEngine) cancelled(id string) {
	f.update(id, func(j *jobs.Job) { j.State, j.Err = jobs.StateCancelled, &jobs.Error{Code: jobs.CodeCancelled} })
}

type fakeSched struct{ f *fakeEngine }

// Register keeps the executors of Go-side kinds (downloads, moving the
// models), which the fake runs at once, inside Submit.
func (s fakeSched) Register(kind string, ex jobs.Executor) {
	if kind == hub.KindDownload || kind == kindMoveModels {
		s.f.executors[kind] = ex
	}
}
func (s fakeSched) Start(context.Context) error         { return nil }
func (s fakeSched) Idle() bool                          { return true }
func (s fakeSched) Close() error                        { return nil }
func (s fakeSched) Retry(context.Context, string) error { return nil }

func (s fakeSched) Submit(_ context.Context, spec jobs.Spec) (string, error) {
	id := fmt.Sprintf("job%d", len(s.f.jobs)+1)
	r := sentReq{ID: id, Spec: spec, Cmd: spec.Kind, Params: spec.Params}
	switch spec.Kind {
	case proto.MethodTTSSynthesize:
		r.Cmd = "synthesize"
		json.Unmarshal(spec.Params, &r.SynthesizeParams)
	case services.KindRender: // M1-G: a take's render; tests read it as the one synthesize call it would make
		r.Cmd = "synthesize"
		var p services.RenderParams
		json.Unmarshal(spec.Params, &p)
		r.SynthesizeParams = services.SynthesizeParams(p.Take, p.Output, p.Stream)
	case hub.KindDownload:
		r.Cmd = "download"
		var p hub.DownloadParams
		json.Unmarshal(spec.Params, &p)
		r.Model = p.Repo
	case proto.MethodModelsLoad:
		r.Cmd = "load"
		var p proto.LoadParams
		json.Unmarshal(spec.Params, &p)
		r.Model = p.Model
	}
	s.f.sent = append(s.f.sent, r)
	s.f.jobs[id] = jobs.Job{ID: id, Spec: spec, State: jobs.StateQueued, Created: time.Now()}
	if ex := s.f.executors[spec.Kind]; ex != nil {
		s.f.run(id, ex)
	}
	return id, nil
}

// run executes a job at once, publishing its updates as the scheduler would.
func (f *fakeEngine) run(id string, ex jobs.Executor) {
	f.update(id, func(j *jobs.Job) { j.State, j.Attempt, j.Started = jobs.StateRunning, 1, time.Now() })
	res, err := ex.Run(context.Background(), f.jobs[id], func(p jobs.Progress) {
		f.update(id, func(j *jobs.Job) { j.Progress = p })
	}, nil)
	f.update(id, func(j *jobs.Job) {
		j.Finished = time.Now()
		var je *jobs.Error
		switch {
		case err == nil:
			j.State, j.Result = jobs.StateSucceeded, res
		case errors.As(err, &je):
			j.State, j.Err = jobs.StateFailed, je
		case errors.Is(err, context.Canceled):
			j.State, j.Err = jobs.StateCancelled, &jobs.Error{Code: jobs.CodeCancelled}
		default:
			j.State, j.Err = jobs.StateFailed, &jobs.Error{Code: "internal", Message: err.Error()}
		}
	})
}

func (s fakeSched) SubmitGraph(ctx context.Context, nodes []jobs.Node) (map[string]string, error) {
	ids := map[string]string{}
	for _, n := range nodes {
		ids[n.Key], _ = s.Submit(ctx, n.Spec)
	}
	return ids, nil
}

func (s fakeSched) Cancel(_ context.Context, id string) error {
	s.f.sent = append(s.f.sent, sentReq{ID: id, Cmd: "cancel"})
	return nil
}

func (s fakeSched) Get(_ context.Context, id string) (jobs.Job, error) {
	j, ok := s.f.jobs[id]
	if !ok {
		return j, jobs.ErrNotFound
	}
	return j, nil
}

// List returns the jobs of the filter's kind, owner (exact, or a "kind:"
// prefix) and states (tests that look at the job history); without
// states, none (no history).
func (s fakeSched) List(_ context.Context, fl jobs.Filter) ([]jobs.Job, error) {
	var out []jobs.Job
	for _, j := range s.f.jobs {
		owned := fl.Owner == "" || j.Owner == fl.Owner || strings.HasSuffix(fl.Owner, ":") && strings.HasPrefix(j.Owner, fl.Owner)
		if len(fl.States) > 0 && slices.Contains(fl.States, j.State) && (fl.Kind == "" || fl.Kind == j.Spec.Kind) && owned {
			out = append(out, j)
		}
	}
	slices.SortFunc(out, func(a, b jobs.Job) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

type fakeSup struct{ f *fakeEngine }

var _ engine.Supervisor = (*fakeSup)(nil)

func (s fakeSup) Start(context.Context) error   { return nil }
func (s fakeSup) Status() []engine.WorkerStatus { return nil }
func (s fakeSup) Stop()                         {}

func (s fakeSup) SetMemoryBudget(n int64)       { s.f.budget = n }
func (s fakeSup) SetIdleUnload(d time.Duration) { s.f.idle = d }
func (s fakeSup) SetCacheDir(dir string)        { s.f.cacheDir = dir }

func (s fakeSup) Hold(roles ...proto.Role) func() {
	s.f.mu.Lock()
	s.f.holds = append(s.f.holds, roles...)
	s.f.mu.Unlock()
	held := slices.Clone(roles)
	return func() {
		s.f.mu.Lock()
		defer s.f.mu.Unlock()
		for _, r := range held {
			if i := slices.Index(s.f.holds, r); i >= 0 {
				s.f.holds = slices.Delete(s.f.holds, i, i+1)
			}
		}
		held = nil
	}
}

func (s fakeSup) Restart(proto.Role) error {
	s.f.restarts++
	return nil
}

func (s fakeSup) Call(ctx context.Context, _ proto.Role, id, method string, params any, notify func(proto.Message)) (json.RawMessage, error) {
	f := s.f
	cmd := method
	switch method {
	case proto.MethodModelsUnload:
		cmd = "unload"
	}
	raw, _ := json.Marshal(params)
	f.mu.Lock()
	f.sent = append(f.sent, sentReq{ID: id, Cmd: cmd, Params: raw})
	perr := f.callErr[method]
	f.mu.Unlock()
	if perr != nil {
		return nil, perr
	}
	answer := func(v any) (json.RawMessage, error) {
		out, err := json.Marshal(v)
		return out, err
	}
	switch method {
	case proto.MethodModelsUnload:
		return json.RawMessage(`{"unloaded":[]}`), nil
	case proto.MethodMediaProbe:
		var p proto.ProbeParams
		json.Unmarshal(raw, &p)
		f.mu.Lock()
		info, ok := f.probes[p.Input]
		f.mu.Unlock()
		if !ok {
			info = proto.ProbeResult{Format: "wav", Duration: 3, HasAudio: true, AudioTracks: 1, Codec: "lpcm", SampleRate: 48000, Channels: 1, Bits: 16}
		}
		return answer(info)
	case proto.MethodMediaEncode:
		// "Encodes" to M4A/M4B: the output holds "FAKE-M4A" and the
		// params, so tests can read back what was asked (export_dialog_test.go).
		var p proto.EncodeParams
		json.Unmarshal(raw, &p)
		if p.Output.Path != "" {
			if err := os.WriteFile(p.Output.Path, append([]byte("FAKE-M4A\n"), raw...), 0o644); err != nil {
				return nil, &proto.Error{Code: proto.CodeInternal, Message: err.Error()}
			}
		}
		return answer(proto.EncodeResult{Output: p.Output.Path, Format: p.Output.Format, Bitrate: p.Bitrate})
	case proto.MethodCaptureDevices:
		return answer(proto.DevicesResult{Devices: enginetest.FakeDevices()})
	case proto.MethodCaptureStop:
		var p proto.CaptureStopParams
		json.Unmarshal(raw, &p)
		f.mu.Lock()
		stop, ok := f.captures[p.Job]
		delete(f.captures, p.Job)
		f.mu.Unlock()
		if ok {
			close(stop)
		}
		return answer(proto.CaptureStopResult{Stopped: ok})
	case proto.MethodCaptureStart:
		var p proto.CaptureParams
		json.Unmarshal(raw, &p)
		stop := make(chan struct{})
		f.mu.Lock()
		f.captures[id] = stop
		levels := f.levels
		f.mu.Unlock()
		note := func(method string, v any) {
			if notify != nil {
				m, _ := proto.NewNotification(method, v)
				notify(m)
			}
		}
		// The levels come before "recording" here (a real engine sends them
		// after): StartCapture returns once "recording" arrives, so what the
		// levels changed is visible to the test's goroutine, without a race.
		for _, l := range levels {
			l.Job = id
			note(proto.MethodCaptureLevel, l)
		}
		note(proto.MethodJobProgress, proto.ProgressParams{Job: id, Stage: "recording"})
		select {
		case <-stop:
		case <-ctx.Done():
			f.mu.Lock()
			delete(f.captures, id)
			f.mu.Unlock()
			return nil, &proto.Error{Code: proto.CodeCancelled, Message: "Cancelled"}
		}
		dev := p.Device
		if dev == "" {
			dev = enginetest.FakeBuiltInMic
		}
		return answer(proto.CaptureResult{AudioOut: proto.AudioOut{Output: p.Output.Path, SampleRate: 48000, Channels: 1, Frames: 48000, AudioSeconds: 1},
			Device: dev, Peak: -12, Reason: proto.CaptureStopped})
	}
	return json.RawMessage(`{}`), nil
}
