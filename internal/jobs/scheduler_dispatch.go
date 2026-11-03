package jobs

// The dispatcher: settling waiting jobs against their dependencies,
// starting queued jobs in their lanes, running them and recording how
// each run ended.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"slices"
	"strings"
	"time"
)

// depState is the state of job id as far as dependents care. Caller holds mu.
func (s *Sched) depState(id string) (State, bool) {
	if e, ok := s.jobs[id]; ok {
		return e.job.State, true
	}
	st, ok := s.done[id]
	return st, ok
}

// evaluate settles a waiting job against its dependencies: queued when
// all succeeded, failed or cancelled (and so its dependents) when one
// failed, was interrupted (it will not run again on its own, so this job
// would wait forever) or was cancelled. Caller holds mu.
func (s *Sched) evaluate(e *entry, publish bool) {
	if e.job.State != StateWaiting {
		return
	}
	ready := true
	for _, d := range e.job.After {
		st, ok := s.depState(d)
		switch {
		case !ok:
			s.unlink(e)
			s.finishLocked(e, StateFailed, &Error{Code: CodeDependency, Message: "A job it depends on is gone: " + d})
			return
		case st == StateFailed:
			s.unlink(e)
			s.finishLocked(e, StateFailed, &Error{Code: CodeDependency, Message: "A job it depends on failed"})
			return
		case st == StateInterrupted:
			s.unlink(e)
			s.finishLocked(e, StateFailed, &Error{Code: CodeDependency, Message: "A job it depends on was interrupted"})
			return
		case st == StateCancelled:
			s.unlink(e)
			s.finishLocked(e, StateCancelled, &Error{Code: CodeCancelled, Message: "A job it depends on was cancelled"})
			return
		case st != StateSucceeded:
			ready = false
		}
	}
	if ready {
		s.unlink(e)
		e.job.State = StateQueued
		if publish {
			s.out(e, nil, true)
		}
		s.poke()
	}
}

// unlink drops e from the dependents of its dependencies: once it leaves
// Waiting they have nothing more to tell it. Caller holds mu.
func (s *Sched) unlink(e *entry) {
	for _, d := range e.job.After {
		l := slices.DeleteFunc(s.dependents[d], func(id string) bool { return id == e.job.ID })
		if len(l) == 0 {
			delete(s.dependents, d)
		} else {
			s.dependents[d] = l
		}
	}
}

// finishLocked makes a non-running job terminal and settles its
// dependents. Caller holds mu.
func (s *Sched) finishLocked(e *entry, st State, jerr *Error) {
	e.job.State, e.job.Err, e.job.Finished = st, jerr, s.opts.Now()
	s.retire(e)
	s.out(e, nil, true)
	s.settleDependents(e.job.ID)
}

// retire records a terminal entry and drops the oldest finished ones
// from memory. Caller holds mu.
func (s *Sched) retire(e *entry) {
	s.done[e.job.ID] = e.job.State
	if i := slices.Index(s.finished, e.job.ID); i >= 0 { // interrupted, then cancelled
		s.finished = slices.Delete(s.finished, i, i+1)
	}
	s.finished = append(s.finished, e.job.ID)
	for len(s.finished) > s.opts.KeepFinished {
		id := s.finished[0]
		s.finished = s.finished[1:]
		if old, ok := s.jobs[id]; ok && ended(old.job.State) {
			delete(s.jobs, id)
		}
	}
}

// ended reports whether a job in state st will not run again without a
// Retry: terminal, or interrupted.
func ended(st State) bool { return st.Terminal() || st == StateInterrupted }

// settleDependents re-evaluates the waiting jobs that depend on id. Caller holds mu.
func (s *Sched) settleDependents(id string) {
	for _, d := range slices.Clone(s.dependents[id]) {
		if e, ok := s.jobs[d]; ok {
			s.evaluate(e, true)
		}
	}
	if st, ok := s.depState(id); ok && st.Terminal() {
		delete(s.dependents, id)
	}
}

// interruptLocked ends a run as Interrupted: Retry may re-queue it, and
// the jobs waiting on it fail (they would wait forever). Caller holds mu.
func (s *Sched) interruptLocked(e *entry, jerr *Error) {
	e.job.State, e.job.Err, e.job.Finished = StateInterrupted, jerr, s.opts.Now()
	s.retire(e)
	s.out(e, nil, true)
	s.settleDependents(e.job.ID)
}

// poke wakes the dispatcher.
func (s *Sched) poke() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

func (s *Sched) loop() {
	defer close(s.loopDone)
	for {
		select {
		case <-s.kick:
		case <-s.root.Done():
			return
		}
		s.dispatch()
	}
}

// dispatch starts every queued job (and lane hold, hold.go) that has a
// free slot in its lane.
func (s *Sched) dispatch() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || s.closed {
		return
	}
	now := s.opts.Now()
	type cand struct {
		lane    Lane
		prio    Priority
		created time.Time
		id      string
		e       *entry
		h       *hold
	}
	var queued []cand
	var wake time.Time
	for _, e := range s.jobs {
		if e.job.State != StateQueued {
			continue
		}
		if e.notBefore.After(now) {
			if wake.IsZero() || e.notBefore.Before(wake) {
				wake = e.notBefore
			}
			continue
		}
		queued = append(queued, cand{lane: e.job.Lane, prio: e.job.Priority, created: e.job.Created, id: e.job.ID, e: e})
	}
	for h := range s.holds {
		if !h.running {
			queued = append(queued, cand{lane: h.lane, prio: h.prio, created: h.created, id: h.id, h: h})
		}
	}
	slices.SortFunc(queued, func(a, b cand) int {
		if a.prio != b.prio {
			return int(b.prio - a.prio)
		}
		if c := a.created.Compare(b.created); c != 0 {
			return c
		}
		return strings.Compare(a.id, b.id)
	})
	for _, c := range queued {
		if s.running[c.lane] >= s.slots(c.lane) {
			continue
		}
		if c.h != nil {
			s.startHoldLocked(c.h)
		} else {
			s.startLocked(c.e)
		}
	}
	if !wake.IsZero() {
		if s.timer != nil {
			s.timer.Stop()
		}
		s.timer = time.AfterFunc(wake.Sub(now), s.poke)
	}
}

func (s *Sched) startLocked(e *entry) {
	ex, ok := s.execs[e.job.Kind]
	if !ok {
		s.finishLocked(e, StateFailed, &Error{Code: CodeNoExecutor, Message: "Nothing can run jobs of kind " + e.job.Kind})
		return
	}
	ctx, cancel := context.WithCancelCause(withLane(s.root, e.job.Lane)) // work inside it holds the lane (hold.go)
	e.cancel = cancel
	e.run++
	e.job.State = StateRunning
	e.job.Attempt++
	e.job.Started, e.job.Finished = s.opts.Now(), time.Time{}
	e.job.Err, e.job.Result = nil, nil
	e.job.Progress = Progress{Fraction: -1}
	e.lastPub, e.lastStage = time.Time{}, ""
	s.running[e.job.Lane]++
	s.out(e, nil, true)
	job := cloneJob(e.job)
	run := e.run
	s.executors.Add(1)
	go s.execute(ctx, e, run, ex, job)
}

func (s *Sched) execute(ctx context.Context, e *entry, run int, ex Executor, job Job) {
	defer s.executors.Done()
	report := func(p Progress) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if e.run != run || e.job.State != StateRunning {
			return
		}
		e.job.Progress = p
		now := s.opts.Now()
		if p.Stage != e.lastStage || now.Sub(e.lastPub) >= s.opts.ProgressEvery {
			e.lastPub, e.lastStage = now, p.Stage
			s.out(e, nil, false)
		}
	}
	var chunk func(json.RawMessage)
	if wantsStream(job.Params) {
		chunk = func(c json.RawMessage) {
			s.mu.Lock()
			defer s.mu.Unlock()
			if e.run == run && e.job.State == StateRunning {
				s.out(e, slices.Clone(c), false)
			}
		}
	}
	res, err := runSafely(ctx, ex, job, report, chunk)
	s.finishRun(e, run, res, err, context.Cause(ctx))
}

// wantsStream peeks at params for "stream": true.
func wantsStream(params json.RawMessage) bool {
	var p struct {
		Stream bool `json:"stream"`
	}
	return json.Unmarshal(params, &p) == nil && p.Stream
}

func runSafely(ctx context.Context, ex Executor, job Job, report func(Progress), chunk func(json.RawMessage)) (res json.RawMessage, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &Error{Code: CodeInternal, Message: fmt.Sprintf("panic: %v\n%s", r, debug.Stack())}
		}
	}()
	return ex.Run(ctx, job, report, chunk)
}

// finishRun records how a run ended.
func (s *Sched) finishRun(e *entry, run int, res json.RawMessage, err, cause error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.run != run {
		return
	}
	s.running[e.job.Lane]--
	e.cancel(nil)
	e.cancel = nil
	now := s.opts.Now()
	switch {
	case err == nil:
		e.job.Result = slices.Clone(res)
		if e.job.Result == nil {
			e.job.Result = json.RawMessage(`{}`)
		}
		s.finishLocked(e, StateSucceeded, nil)
	case errors.Is(cause, errShutdown):
		e.job.State, e.job.Finished = StateInterrupted, now
		e.job.Err = &Error{Code: CodeShutdown, Message: "AudioSmith quit while the job ran"}
		s.out(e, nil, true)
	case errors.Is(cause, errUserCancel):
		s.finishLocked(e, StateCancelled, cancelledError(e.cancelBy))
	default:
		var jerr *Error
		if d := DiskFull(err); d != nil { // a full disk is said so, never "internal" (code review 1102 M4)
			jerr = d
		} else if !errors.As(err, &jerr) {
			jerr = &Error{Code: CodeInternal, Message: err.Error()}
		}
		jerr = &Error{Code: jerr.Code, Message: jerr.Message, Data: slices.Clone(jerr.Data), Retryable: jerr.Retryable}
		if jerr.Retryable && e.job.Attempt-e.base < max(e.job.MaxAttempts, 1) {
			e.job.State, e.job.Err = StateQueued, jerr
			e.notBefore = now.Add(s.opts.RetryDelay(e.job.Attempt))
			s.out(e, nil, true)
			s.poke()
			return
		}
		if jerr.Code == CodeInterrupted {
			s.interruptLocked(e, jerr)
		} else {
			s.finishLocked(e, StateFailed, jerr)
		}
	}
	s.poke()
}
