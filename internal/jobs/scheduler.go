package jobs

// The scheduler (Sched): its options and state, New, Start and Close, and
// the outbox that saves and publishes job updates in order.
//
// Files: scheduler.go, scheduler_submit.go (Submit, SubmitGraph),
// scheduler_dispatch.go (dependencies, dispatching and running jobs),
// scheduler_ops.go (Cancel, Retry, Get, List, Idle), ulid.go (job ids).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"sync"
	"time"
)

// Errors of the scheduler. (jobs imports nothing internal, so these are
// its own; callers that map errors to the UI or HTTP treat ErrNotFound as
// core.ErrNotFound, ErrInvalid as core.ErrInvalid, ErrClosed as
// core.ErrCancelled.)
var (
	ErrNotFound = errors.New("jobs: no such job")
	ErrInvalid  = errors.New("jobs: invalid job")
	ErrClosed   = errors.New("jobs: the scheduler is closed")
)

// Error codes the scheduler sets on its own (executors add theirs, e.g.
// the protocol's).
const (
	CodeCancelled   = "cancelled"         // Cancel
	CodeInterrupted = "interrupted"       // the worker died mid-run, or the app quit (see CodeShutdown)
	CodeShutdown    = "shutdown"          // Close interrupted it; Idempotent jobs re-run at the next Start
	CodeDependency  = "dependency_failed" // a job it depends on failed
	CodeNoExecutor  = "no_executor"       // nothing registered for the kind
	CodeInternal    = "internal"          // an executor returned a plain error, or panicked
)

// Options configure New.
type Options struct {
	Store Store // required; NewMemStore() in tests
	// Publish receives every Updated, in order, on one goroutine of the
	// scheduler that holds no lock: func(u jobs.Updated) { bus.Publish(u) }.
	Publish func(Updated)
	// Slots overrides LaneSlots per lane; a lane in neither has 1 slot.
	Slots map[Lane]int
	// NewID makes job ids (default: a ULID, like core.NewID).
	NewID func() string
	Now   func() time.Time
	// ProgressEvery is the shortest interval between two progress updates
	// of a job (default 100 ms). A change of stage is always published.
	ProgressEvery time.Duration
	// RetryDelay is the wait before automatic attempt n+1 after attempt n
	// failed retryably (default n seconds, at most 30 s).
	RetryDelay func(attempt int) time.Duration
	// KeepFinished bounds the finished jobs kept in memory for Get/List
	// (default 200); older ones are read back from the Store.
	KeepFinished int
	// CloseTimeout bounds how long Close waits for executors to return
	// after cancelling them (default 4 s; negative: no bound). Jobs whose
	// executor is still running then are recorded Interrupted
	// (CodeShutdown) and left behind.
	CloseTimeout time.Duration
	// HoldGrace is how long a lane hold keeps its slot after its context
	// ended without release (default 10 s): the work done under it, such
	// as an engine request being cancelled, may still run.
	HoldGrace time.Duration
	Logf      func(format string, args ...any)
	// ClassifyError, when set, types an executor's failure before the
	// scheduler does (nil: not one it knows): the app maps a full disk to
	// disk_full (services.DiskFullJob), however the executor said it.
	ClassifyError func(error) *Error
}

// Sched implements Scheduler. Make it with New.
//
// How it works: one mutex guards the jobs in memory. Every change is
// appended, as a snapshot, to an ordered outbox that one goroutine drains:
// it saves state changes to the Store and then publishes Updated, holding
// no lock, so subscribers may call back into the scheduler. Submit saves
// new jobs itself before they become visible, so a job the caller got an
// id for is durable. A dispatcher goroutine starts queued jobs when their
// lane has a free slot: higher Priority first, then older.
type Sched struct {
	opts      Options
	execs     map[string]Executor
	root      context.Context
	stopRoot  context.CancelFunc
	kick      chan struct{}
	loopDone  chan struct{}
	executors sync.WaitGroup

	mu         sync.Mutex
	started    bool
	closed     bool
	jobs       map[string]*entry   // unfinished jobs, and recently finished ones
	done       map[string]State    // every job known to be terminal, by id
	dependents map[string][]string // job id → ids of jobs that wait on it
	running    map[Lane]int
	holds      map[*hold]struct{} // lane holds waiting or running (hold.go)
	finished   []string           // ids of terminal entries in s.jobs, oldest first
	timer      *time.Timer

	outMu   sync.Mutex
	outCond *sync.Cond
	outQ    []outItem
	outStop bool
	outDone chan struct{}
}

var _ Scheduler = (*Sched)(nil)

type entry struct {
	job       Job
	cancel    context.CancelCauseFunc // set while running
	cancelBy  string                  // who cancelled it while running (WithActor)
	run       int                     // bumps on every start: stale reports are dropped
	notBefore time.Time               // automatic retry waits until then
	base      int                     // Attempt when last (re)queued by hand: auto-retries count from it
	lastPub   time.Time
	lastStage string
}

type outItem struct {
	job   Job
	chunk json.RawMessage
	save  bool
	flush chan struct{} // closed when the items before it are out
}

var (
	errUserCancel = errors.New("cancelled by the user")
	errShutdown   = errors.New("the scheduler closed")
)

// New returns a scheduler. Register executors, then Start it. Jobs may be
// submitted before Start; they run once it is called.
func New(opts Options) *Sched {
	if opts.Store == nil {
		panic("jobs.New: no Store")
	}
	if opts.NewID == nil {
		opts.NewID = func() string { return newULID(time.Now()) }
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.ProgressEvery == 0 {
		opts.ProgressEvery = 100 * time.Millisecond
	}
	if opts.RetryDelay == nil {
		opts.RetryDelay = func(n int) time.Duration { return min(time.Duration(n)*time.Second, 30*time.Second) }
	}
	if opts.KeepFinished == 0 {
		opts.KeepFinished = 200
	}
	if opts.CloseTimeout == 0 {
		opts.CloseTimeout = 4 * time.Second
	}
	if opts.HoldGrace == 0 {
		opts.HoldGrace = 10 * time.Second
	}
	if opts.Logf == nil {
		opts.Logf = log.Printf
	}
	root, stop := context.WithCancel(context.Background())
	s := &Sched{
		opts: opts, execs: map[string]Executor{}, root: root, stopRoot: stop,
		kick: make(chan struct{}, 1), loopDone: make(chan struct{}),
		jobs: map[string]*entry{}, done: map[string]State{}, dependents: map[string][]string{},
		running: map[Lane]int{}, outDone: make(chan struct{}),
	}
	s.outCond = sync.NewCond(&s.outMu)
	go s.outbox()
	return s
}

func (s *Sched) Register(kind string, ex Executor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.execs[kind] = ex
}

func (s *Sched) slots(l Lane) int {
	if n, ok := s.opts.Slots[l]; ok {
		return n
	}
	if n, ok := LaneSlots[l]; ok {
		return n
	}
	return 1
}

// Start loads the persisted unfinished jobs and starts dispatching. Jobs
// found running were interrupted by a crash: Idempotent ones are
// re-queued, the others become Interrupted. Jobs interrupted by Close
// (CodeShutdown) that are Idempotent are re-queued too.
func (s *Sched) Start(ctx context.Context) error {
	loaded, err := s.opts.Store.LoadJobs(ctx, Filter{})
	if err != nil {
		return fmt.Errorf("jobs: loading: %w", err)
	}
	now := s.opts.Now()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	if s.started {
		s.mu.Unlock()
		return nil
	}
	var waiting []*entry
	for _, j := range loaded {
		if _, ok := s.jobs[j.ID]; ok {
			continue
		}
		e := &entry{job: j, base: j.Attempt}
		changed := false
		switch {
		case j.State == StateRunning && j.Idempotent,
			j.State == StateInterrupted && j.Idempotent && j.Err != nil && j.Err.Code == CodeShutdown:
			e.job.State, e.job.Err, e.job.Finished, changed = StateQueued, nil, time.Time{}, true
			if len(j.After) > 0 {
				e.job.State = StateWaiting
			}
		case j.State == StateRunning:
			e.job.State, e.job.Finished, changed = StateInterrupted, now, true
			e.job.Err = &Error{Code: CodeInterrupted, Message: "AudioSmith stopped while the job ran"}
		}
		s.jobs[j.ID] = e
		if e.job.State == StateWaiting {
			for _, d := range j.After {
				s.dependents[d] = append(s.dependents[d], j.ID)
			}
			waiting = append(waiting, e)
		}
		s.out(e, nil, changed)
		if e.job.State == StateInterrupted {
			s.retire(e) // kept for Retry, dropped from memory like finished jobs
		}
	}
	unknown := s.unknownDeps(waiting)
	s.mu.Unlock()

	if len(unknown) > 0 {
		if err := s.lookupDone(ctx, unknown); err != nil {
			s.opts.Logf("jobs: dependencies: %v", err)
		}
	}

	s.mu.Lock()
	for _, e := range waiting {
		s.evaluate(e, true)
	}
	s.started = true
	s.mu.Unlock()
	s.poke()
	go s.loop()
	return nil
}

// unknownDeps lists the dependencies of es that are neither in memory nor
// known to be finished. Caller holds mu.
func (s *Sched) unknownDeps(es []*entry) []string {
	var out []string
	for _, e := range es {
		for _, d := range e.job.After {
			if _, ok := s.jobs[d]; ok {
				continue
			}
			if _, ok := s.done[d]; ok {
				continue
			}
			out = append(out, d)
		}
	}
	return out
}

// lookupDone reads the states of the finished jobs ids (the dependencies
// of jobs from an earlier run) from the store into s.done: one by one
// when the store can (JobLoader), else every finished job. Slow path.
func (s *Sched) lookupDone(ctx context.Context, ids []string) error {
	var old []Job
	if l, ok := s.opts.Store.(JobLoader); ok {
		for _, id := range ids {
			j, found, err := l.LoadJob(ctx, id)
			if err != nil {
				return err
			}
			if found && ended(j.State) {
				old = append(old, j)
			}
		}
	} else {
		all, err := s.opts.Store.LoadJobs(ctx, Filter{States: []State{StateSucceeded, StateFailed, StateCancelled, StateInterrupted}})
		if err != nil {
			return err
		}
		old = all
	}
	s.mu.Lock()
	for _, j := range old {
		if _, ok := s.jobs[j.ID]; !ok {
			s.done[j.ID] = j.State
		}
	}
	s.mu.Unlock()
	return nil
}

// Close stops dispatching, cancels the running jobs (they become
// Interrupted with CodeShutdown), waits for their executors, and returns
// once every update is saved and published. It never waits much longer
// than Options.CloseTimeout: an executor that ignores its context is left
// running (its job is recorded Interrupted and its late report dropped),
// and an outbox stuck on the store is abandoned with an error.
func (s *Sched) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	started := s.started
	for _, e := range s.jobs {
		if e.job.State == StateRunning && e.cancel != nil {
			e.cancel(errShutdown)
		}
	}
	for h := range s.holds {
		h.cancel(errShutdown)
	}
	if s.timer != nil {
		s.timer.Stop()
	}
	s.mu.Unlock()
	limit := s.opts.CloseTimeout
	deadline := time.Now().Add(limit)
	if !waitFor(s.executors.Wait, limit) {
		s.abandonRunning()
	}
	s.stopRoot()
	if started {
		<-s.loopDone // the loop only waits for mu: quick once root is done
	}
	s.outMu.Lock()
	s.outStop = true
	s.outCond.Broadcast()
	s.outMu.Unlock()
	if limit < 0 {
		<-s.outDone
		return nil
	}
	select {
	case <-s.outDone:
		return nil
	case <-time.After(max(time.Until(deadline), 0) + time.Second):
		return errors.New("jobs: closing: the store did not take the last updates in time")
	}
}

// abandonRunning records the jobs whose executors did not return within
// CloseTimeout as Interrupted (CodeShutdown, so Idempotent ones re-run at
// the next Start) and drops whatever those executors report later.
func (s *Sched) abandonRunning() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.jobs {
		if e.job.State != StateRunning {
			continue
		}
		e.run++ // the executor's finishRun and reports are stale now
		s.running[e.job.Lane]--
		e.cancel = nil
		e.job.State, e.job.Finished = StateInterrupted, s.opts.Now()
		e.job.Err = &Error{Code: CodeShutdown, Message: "AudioSmith quit while the job ran (it did not stop in time)"}
		s.opts.Logf("jobs: %s (%s) did not stop within %s of quitting; left behind", e.job.ID, e.job.Kind, s.opts.CloseTimeout)
		s.out(e, nil, true)
	}
}

// waitFor runs wait and reports whether it returned within d (d < 0: it
// waits for it). A wait that does not return keeps its goroutine.
func waitFor(wait func(), d time.Duration) bool {
	if d < 0 {
		wait()
		return true
	}
	done := make(chan struct{})
	go func() {
		wait()
		close(done)
	}()
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-done:
		return true
	case <-t.C:
		return false
	}
}

// Flush waits until every change so far is saved and published.
func (s *Sched) Flush() {
	ch := make(chan struct{})
	s.outMu.Lock()
	if s.outStop {
		s.outMu.Unlock()
		return
	}
	s.outQ = append(s.outQ, outItem{flush: ch})
	s.outCond.Signal()
	s.outMu.Unlock()
	<-ch
}

// out queues e's current state for saving (save) and publishing. Caller holds mu.
func (s *Sched) out(e *entry, chunk json.RawMessage, save bool) {
	it := outItem{job: cloneJob(e.job), chunk: chunk, save: save}
	s.outMu.Lock()
	s.outQ = append(s.outQ, it)
	s.outCond.Signal()
	s.outMu.Unlock()
}

// outbox saves and publishes queued updates in order, holding no lock of
// the scheduler while it does.
func (s *Sched) outbox() {
	defer close(s.outDone)
	for {
		s.outMu.Lock()
		for len(s.outQ) == 0 && !s.outStop {
			s.outCond.Wait()
		}
		if len(s.outQ) == 0 {
			s.outMu.Unlock()
			return
		}
		it := s.outQ[0]
		s.outQ[0] = outItem{}
		s.outQ = s.outQ[1:]
		s.outMu.Unlock()

		if it.flush != nil {
			close(it.flush)
			continue
		}
		if it.save {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := s.opts.Store.SaveJob(ctx, it.job); err != nil {
				s.opts.Logf("jobs: saving %s: %v", it.job.ID, err)
			}
			cancel()
		}
		if s.opts.Publish != nil {
			s.publish(Updated{Job: it.job, Chunk: it.chunk})
		}
	}
}

// publish calls Publish; a subscriber that panics is logged, and the
// outbox goes on with the next update (#101 L11: it stopped every job
// update, and crashed the app).
func (s *Sched) publish(u Updated) {
	defer func() {
		if r := recover(); r != nil {
			s.opts.Logf("jobs: publishing %s: panic: %v\n%s", u.Job.ID, r, debug.Stack())
		}
	}()
	s.opts.Publish(u)
}
