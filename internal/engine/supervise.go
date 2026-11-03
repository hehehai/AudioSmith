package engine

// The Supervisor implementation (supervisor.go has the contract): its
// workers, spawning and restarting engine processes, routing calls to a
// ready one, Restart and Stop.
//
// Files: supervise.go, supervise_proc.go (one engine process),
// supervise_memory.go (stats, memory budget, eviction).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

// NewSupervisor returns a Supervisor for cfg. Nothing runs until Start.
func NewSupervisor(cfg Config) Supervisor {
	if len(cfg.Roles) == 0 {
		cfg.Roles = []proto.Role{proto.RoleGPU}
	}
	if cfg.HelloTimeout == 0 {
		cfg.HelloTimeout = 30 * time.Second
	}
	if cfg.MaxRestarts == 0 {
		cfg.MaxRestarts = 3
	}
	if cfg.RestartWindow == 0 {
		cfg.RestartWindow = 10 * time.Minute
	}
	if cfg.CancelGrace == 0 {
		cfg.CancelGrace = 5 * time.Second
	}
	if cfg.WriteTimeout == 0 {
		cfg.WriteTimeout = 10 * time.Second
	}
	if cfg.StopTimeout == 0 {
		cfg.StopTimeout = 3 * time.Second
	}
	if cfg.StatsEvery == 0 {
		cfg.StatsEvery = 2 * time.Second
	}
	if cfg.Logf == nil {
		cfg.Logf = log.Printf
	}
	s := &supervisor{cfg: cfg, workers: map[proto.Role]*worker{}}
	for _, r := range slices.Concat(cfg.Roles, cfg.OnDemand) {
		if _, dup := s.workers[r]; dup {
			continue
		}
		s.roles = append(s.roles, r)
		s.workers[r] = &worker{role: r, status: WorkerStatus{Role: r, State: WorkerStopped, Since: time.Now()}}
	}
	if cfg.Family != nil {
		go s.watchFamily(cfg.Family)
	}
	return s
}

type supervisor struct {
	cfg     Config
	roles   []proto.Role // Roles, then OnDemand
	ctrlID  atomic.Int64
	mu      sync.Mutex // guards the workers' fields
	workers map[proto.Role]*worker
	stopped bool
}

type worker struct {
	role     proto.Role
	status   WorkerStatus
	proc     *proc         // the running process; nil when none
	ready    chan struct{} // closed when the current start succeeded or failed
	restarts []time.Time   // automatic restarts within the window
	timer    *time.Timer   // the pending automatic restart
	holds    int           // Hold calls not released: its models stay
	// HoldModels calls not released, per model: those models stay.
	modelHolds map[string]int

	// engine.config (supervise_config.go): one push at a time; what proc
	// configFor was told, and what it said applies.
	configMu      sync.Mutex
	config        engineConfig
	configFor     *proc
	configApplied proto.EngineConfigResult
}

// proc is one engine process.
type proc struct {
	s      *supervisor
	w      *worker
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	tail   *lineTail
	exited chan struct{}

	writeSem chan struct{} // one write at a time (a channel: waiting for it heeds ctx)
	mu       sync.Mutex
	pending  map[string]*call
	closed   bool

	helloDone   atomic.Bool
	intentional atomic.Bool // stopped on purpose: no failure, no restart
	// Set before the process is killed for a reason of the supervisor.
	failReason FailReason
	failErr    string
	noRestart  bool
	pollNow    chan struct{}
}

type call struct {
	method string
	job    bool
	notify func(proto.Message)
	done   chan struct{}
	resp   proto.Message
	err    error
}

func (c *call) result() (json.RawMessage, error) {
	if c.err != nil {
		return nil, c.err
	}
	if c.resp.Error != nil {
		return nil, c.resp.Error
	}
	return c.resp.Result, nil
}

func (s *supervisor) Start(ctx context.Context) error {
	s.mu.Lock()
	s.stopped = false
	var ws []*worker
	for _, r := range s.cfg.Roles {
		w := s.workers[r]
		if w.proc == nil && w.timer == nil {
			ws = append(ws, w)
		}
	}
	s.mu.Unlock()
	for _, w := range ws {
		s.spawn(w, false)
	}
	return nil
}

// publish sends w's status on the bus. Never called with s.mu held.
func (s *supervisor) publish(st WorkerStatus) {
	s.cfg.Bus.Publish(StatusChanged{Status: st})
}

// setStatus changes w's status under s.mu and returns the copy to publish.
func (s *supervisor) statusLocked(w *worker) WorkerStatus {
	st := w.status
	st.Hello = cloneHello(st.Hello)
	st.Stats = cloneStats(st.Stats)
	if w.proc != nil {
		st.Running = w.proc.running()
	}
	return st
}

func (s *supervisor) Status() []WorkerStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]WorkerStatus, 0, len(s.roles))
	for _, r := range s.roles {
		out = append(out, s.statusLocked(s.workers[r]))
	}
	return out
}

// spawn starts a process for w. auto marks an automatic restart.
func (s *supervisor) spawn(w *worker, auto bool) {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	w.timer = nil
	w.status.RestartAt = time.Time{}
	if auto {
		w.status.Restarts++
	}
	if w.ready == nil || isClosed(w.ready) {
		w.ready = make(chan struct{})
	}
	bin := s.cfg.Bin
	if bin == "" {
		s.failLocked(w, FailMissing, "audiosmith-engine was not found: build it with scripts/build-engine.sh, or set AUDIOSMITH_ENGINE", "")
		st := s.statusLocked(w)
		s.mu.Unlock()
		s.publish(st)
		return
	}
	s.mu.Unlock()

	if s.cfg.Prepare != nil {
		s.cfg.Prepare(w.role)
	}
	p, err := s.startProcess(w, bin)
	s.mu.Lock()
	if err != nil {
		s.failLocked(w, FailStart, err.Error(), "")
		st := s.statusLocked(w)
		s.mu.Unlock()
		s.publish(st)
		return
	}
	if s.stopped {
		s.mu.Unlock()
		p.intentional.Store(true)
		p.kill()
		return
	}
	w.proc = p
	w.status.State, w.status.PID, w.status.Since = WorkerStarting, p.cmd.Process.Pid, time.Now()
	w.status.Err, w.status.Reason, w.status.StderrTail = "", "", ""
	w.status.Hello, w.status.Stats = nil, nil
	st := s.statusLocked(w)
	s.mu.Unlock()
	s.publish(st)
	go s.handshake(w, p)
}

func isClosed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// failLocked marks w failed and wakes callers waiting for it. Caller holds s.mu.
func (s *supervisor) failLocked(w *worker, reason FailReason, msg, tail string) {
	w.status.State, w.status.Reason, w.status.Err, w.status.StderrTail = WorkerFailed, reason, msg, tail
	w.status.PID, w.status.Hello, w.status.Stats, w.status.Since = 0, nil, nil, time.Now()
	if w.ready != nil && !isClosed(w.ready) {
		close(w.ready)
	}
}

// exited handles the end of a process: its calls fail as interrupted, and
// an unexpected exit marks the worker failed and plans a restart.
func (s *supervisor) exited(p *proc, waitErr error) {
	p.mu.Lock()
	p.closed = true
	calls := p.pending
	p.pending = map[string]*call{}
	reason, msg, noRestart := p.failReason, p.failErr, p.noRestart
	p.mu.Unlock()
	close(p.exited)
	for _, c := range calls {
		c.err = ErrWorkerExited
		close(c.done)
	}

	w := p.w
	s.mu.Lock()
	if w.proc != p {
		s.mu.Unlock()
		return
	}
	w.proc = nil
	if p.intentional.Load() {
		s.mu.Unlock()
		return
	}
	if reason == "" {
		reason, msg = FailCrashed, "the engine exited"
		if waitErr != nil {
			msg += ": " + waitErr.Error()
		}
	}
	tail := p.tail.String()
	s.failLocked(w, reason, msg, tail)
	s.cfg.Logf("engine %s: %s", w.role, msg)
	if !noRestart && !s.stopped {
		now := time.Now()
		w.restarts = slices.DeleteFunc(w.restarts, func(t time.Time) bool { return now.Sub(t) > s.cfg.RestartWindow })
		if len(w.restarts) < s.cfg.MaxRestarts {
			delay := min(time.Second<<len(w.restarts), 30*time.Second)
			w.restarts = append(w.restarts, now)
			w.status.RestartAt = now.Add(delay)
			w.timer = time.AfterFunc(delay, func() { s.spawn(w, true) })
			w.ready = make(chan struct{}) // calls wait for the restart
		}
	}
	st := s.statusLocked(w)
	s.mu.Unlock()
	s.publish(st)
	s.publishFamily() // its models are gone
}

// ready waits for w to be ready and returns its process. A worker that is
// starting, or failed with a restart planned, is waited for (within ctx);
// a stopped one, or a failed one that will not restart, is an error at once.
func (s *supervisor) ready(ctx context.Context, role proto.Role) (*proc, error) {
	for {
		s.mu.Lock()
		w, ok := s.workers[role]
		if !ok {
			s.mu.Unlock()
			return nil, fmt.Errorf("no %s engine is configured: %w", role, core.ErrEngine)
		}
		if w.status.State == WorkerStopped && w.proc == nil && w.timer == nil && !s.stopped && slices.Contains(s.cfg.OnDemand, role) {
			// First call to an on-demand worker: start it and wait.
			w.status.State, w.status.Since = WorkerStarting, time.Now()
			w.ready = make(chan struct{})
			s.mu.Unlock()
			s.spawn(w, false)
			continue
		}
		st, p, ready := w.status, w.proc, w.ready
		s.mu.Unlock()
		switch st.State {
		case WorkerReady:
			if p != nil {
				return p, nil
			}
		case WorkerStarting, WorkerFailed:
			if st.State == WorkerFailed && st.RestartAt.IsZero() {
				break
			}
			select {
			case <-ready:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		msg := string(st.State)
		if st.Err != "" {
			msg = st.Err
		}
		return nil, fmt.Errorf("the %s engine is not ready (%s): %w", role, msg, core.ErrEngine)
	}
}

func (s *supervisor) Call(ctx context.Context, role proto.Role, id, method string, params any, notify func(proto.Message)) (json.RawMessage, error) {
	p, err := s.ready(ctx, role)
	if err != nil {
		return nil, err
	}
	job := proto.IsJob(method)
	if job {
		s.makeRoom(ctx, role, params) // supervise_family.go
	}
	return p.call(ctx, id, method, params, notify, job)
}

func (s *supervisor) Restart(role proto.Role) error {
	s.mu.Lock()
	w, ok := s.workers[role]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("no %s engine is configured: %w", role, core.ErrInvalid)
	}
	s.stopped = false
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
	w.restarts = nil
	w.status.Restarts = 0
	p := w.proc
	w.proc = nil
	// Callers wait for the new process instead of failing.
	w.status.State, w.status.Since = WorkerStarting, time.Now()
	if w.ready == nil || isClosed(w.ready) {
		w.ready = make(chan struct{})
	}
	s.mu.Unlock()
	if p != nil {
		p.intentional.Store(true)
		p.kill()
		<-p.exited
	}
	s.spawn(w, false)
	s.publishFamily() // its models are gone
	return nil
}

func (s *supervisor) Stop() {
	s.mu.Lock()
	s.stopped = true
	var procs []*proc
	var sts []WorkerStatus
	for _, r := range s.roles {
		w := s.workers[r]
		if w.timer != nil {
			w.timer.Stop()
			w.timer = nil
		}
		if w.proc != nil {
			procs = append(procs, w.proc)
			w.proc.intentional.Store(true)
		}
		w.proc = nil
		w.status.State, w.status.PID, w.status.Hello, w.status.Stats = WorkerStopped, 0, nil, nil
		w.status.RestartAt, w.status.Since = time.Time{}, time.Now()
		if w.ready != nil && !isClosed(w.ready) {
			close(w.ready)
		}
		sts = append(sts, s.statusLocked(w))
	}
	s.mu.Unlock()
	var wg sync.WaitGroup
	for _, p := range procs {
		wg.Go(func() {
			// Bounded at every step (#101 H1/M5): the shutdown request's
			// write heeds its context, the exit is awaited StopTimeout,
			// and a process that SIGKILL does not end at once is left.
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			p.call(ctx, "", proto.MethodShutdown, nil, nil, false)
			cancel()
			p.stdin.Close()
			select {
			case <-p.exited:
			case <-time.After(s.cfg.StopTimeout):
				p.kill()
				select {
				case <-p.exited:
				case <-time.After(time.Second):
					s.cfg.Logf("engine %s: still running 1 s after SIGKILL; not waiting for it", p.w.role)
				}
			}
		})
	}
	wg.Wait()
	for _, st := range sts {
		s.publish(st)
	}
}

func cloneHello(h *proto.HelloResult) *proto.HelloResult {
	if h == nil {
		return nil
	}
	c := *h
	c.Methods = slices.Clone(h.Methods)
	c.Families = slices.Clone(h.Families)
	return &c
}

func cloneStats(st *proto.StatsResult) *proto.StatsResult {
	if st == nil {
		return nil
	}
	c := *st
	c.Models = slices.Clone(st.Models)
	c.Jobs = slices.Clone(st.Jobs)
	return &c
}
