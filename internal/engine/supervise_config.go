package engine

// engine.config (code review 1103 F4/F5): each worker with models is told
// the memory budget its own makeRoom keeps to (the memory limit setting or
// the family's share, as the supervisor's evict uses) and the models the
// app holds, so a memory-pressure "warning" leaves a live call's or a
// dictation's models. Sent after engine.hello and whenever either changes;
// an engine that does not list the method gets nothing.

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"time"

	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

// engineConfig is what a worker's engine was told.
type engineConfig struct {
	budget  int64
	held    []string
	holdAll bool
}

func (c engineConfig) equal(o engineConfig) bool {
	return c.budget == o.budget && c.holdAll == o.holdAll && slices.Equal(c.held, o.held)
}

// configLocked is what w's engine should be told with budget. Caller holds s.mu.
func (w *worker) configLocked(budget int64) engineConfig {
	held := slices.Sorted(maps.Keys(w.modelHolds))
	if held == nil {
		held = []string{}
	}
	return engineConfig{budget: max(budget, 0), held: held, holdAll: w.holds > 0}
}

// pushConfigAll tells every ready worker its configuration if it changed;
// budget < 0: s.budget().
func (s *supervisor) pushConfigAll(budget int64) {
	if budget < 0 {
		budget = s.budget()
	}
	type target struct {
		w *worker
		p *proc
	}
	s.mu.Lock()
	var ts []target
	for _, r := range s.roles {
		if w := s.workers[r]; w != nil && w.proc != nil && w.status.State == WorkerReady {
			ts = append(ts, target{w, w.proc})
		}
	}
	s.mu.Unlock()
	for _, t := range ts {
		s.pushConfig(t.w, t.p, budget)
	}
}

// pushConfigOf tells the workers of roles their holds (after Hold,
// HoldModels or their release), off the caller's goroutine.
func (s *supervisor) pushConfigOf(roles ...proto.Role) {
	go func() {
		budget := s.budget()
		for _, r := range roles {
			s.mu.Lock()
			w := s.workers[r]
			var p *proc
			if w != nil && w.status.State == WorkerReady {
				p = w.proc
			}
			s.mu.Unlock()
			if p != nil {
				s.pushConfig(w, p, budget)
			}
		}
	}()
}

// pushConfig sends w's process p engine.config when what it should know
// differs from what it was told. One at a time per worker, so the last
// one sent is the latest.
func (s *supervisor) pushConfig(w *worker, p *proc, budget int64) {
	w.configMu.Lock()
	defer w.configMu.Unlock()
	s.mu.Lock()
	want := w.configLocked(budget)
	hello := w.status.Hello
	stale := w.proc != p || hello == nil || w.configFor == p && w.config.equal(want)
	s.mu.Unlock()
	if stale || !slices.Contains(hello.Methods, proto.MethodEngineConfig) {
		return
	}
	s.sendConfig(w, p, want)
}

// configAtStart tells a worker's new process p its configuration, before
// the worker is ready (no job loads a model before it knows its budget).
func (s *supervisor) configAtStart(w *worker, p *proc, hello proto.HelloResult) {
	if !slices.Contains(hello.Methods, proto.MethodEngineConfig) {
		return
	}
	budget := s.budget()
	if budget <= 0 {
		budget = hello.Device.Memory * 6 / 10 // the first worker: no hello to go by yet
	}
	w.configMu.Lock()
	defer w.configMu.Unlock()
	s.mu.Lock()
	want := w.configLocked(budget)
	s.mu.Unlock()
	s.sendConfig(w, p, want)
}

// sendConfig sends p engine.config want and records it.
func (s *supervisor) sendConfig(w *worker, p *proc, want engineConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	raw, err := p.call(ctx, "", proto.MethodEngineConfig, proto.EngineConfigParams{
		MemoryBudget: &want.budget, Held: &want.held, HoldAll: &want.holdAll,
	}, nil, false)
	var res proto.EngineConfigResult
	if err == nil {
		err = json.Unmarshal(raw, &res)
	}
	if err != nil {
		s.cfg.Logf("engine %s: engine.config: %v", w.role, err)
		return
	}
	s.mu.Lock()
	if w.proc == p {
		w.config, w.configFor = want, p
		w.configApplied = res
	}
	s.mu.Unlock()
}
