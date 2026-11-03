package engine

// Memory: polling engine.stats, the memory budget, the cache directory,
// idle unloading and evicting resident models over the budget. The ActNow
// family's share of it is in supervise_family.go.

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

// pollStats polls engine.stats while jobs run (and once after each),
// publishing changes and keeping resident models within the budget.
func (s *supervisor) pollStats(w *worker, p *proc) {
	tick := time.NewTicker(s.cfg.StatsEvery)
	defer tick.Stop()
	var lastIdlePoll time.Time
	for {
		select {
		case <-p.exited:
			return
		case <-tick.C:
			if p.jobCount() == 0 {
				// Idle: poll now and then only to unload idle models.
				if !s.idlePollDue(w, lastIdlePoll) {
					continue
				}
				lastIdlePoll = time.Now()
			}
		case <-p.pollNow:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		raw, err := p.call(ctx, "", proto.MethodStats, nil, nil, false)
		cancel()
		if err != nil {
			continue
		}
		var stats proto.StatsResult
		if json.Unmarshal(raw, &stats) != nil {
			continue
		}
		s.mu.Lock()
		if w.proc != p {
			s.mu.Unlock()
			return
		}
		changed := w.status.Stats == nil || !sameModels(w.status.Stats.Models, stats.Models)
		busyChanged := w.status.Stats == nil || !sameBusy(w.status.Stats.Models, stats.Models)
		w.status.Stats = &stats
		st := s.statusLocked(w)
		s.mu.Unlock()
		if changed {
			s.publish(st)
		}
		if changed || busyChanged {
			s.publishFamily() // the family's runtime file (spec §11.1)
		}
		s.evict()
	}
}

// idlePollDue reports whether an idle worker's stats should be polled to
// find models idle for longer than the idle-unload limit.
func (s *supervisor) idlePollDue(w *worker, last time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	idle := s.cfg.IdleUnload
	if idle <= 0 || w.status.Stats == nil || len(w.status.Stats.Models) == 0 {
		return false
	}
	every := min(max(idle/4, time.Second), 30*time.Second)
	return time.Since(last) >= every
}

// SetMemoryBudget changes Config.MemoryBudget and applies it at once.
func (s *supervisor) SetMemoryBudget(bytes int64) {
	s.mu.Lock()
	s.cfg.MemoryBudget = max(bytes, 0)
	s.mu.Unlock()
	go s.evict()
}

// SetCacheDir changes Config.CacheDir for the workers started next.
func (s *supervisor) SetCacheDir(dir string) {
	s.mu.Lock()
	s.cfg.CacheDir = dir
	s.mu.Unlock()
}

// SetIdleUnload changes Config.IdleUnload; it applies at the next poll.
func (s *supervisor) SetIdleUnload(d time.Duration) {
	s.mu.Lock()
	s.cfg.IdleUnload = max(d, 0)
	s.mu.Unlock()
	go s.evict()
}

// Hold keeps the models of roles resident until release (Supervisor).
func (s *supervisor) Hold(roles ...proto.Role) (release func()) {
	s.mu.Lock()
	for _, r := range roles {
		if w := s.workers[r]; w != nil {
			w.holds++
		}
	}
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			for _, r := range roles {
				if w := s.workers[r]; w != nil {
					w.holds = max(w.holds-1, 0)
				}
			}
			s.mu.Unlock()
		})
	}
}

// HoldModels keeps models of role resident until release (Supervisor).
func (s *supervisor) HoldModels(role proto.Role, models ...string) (release func()) {
	models = slices.DeleteFunc(slices.Clone(models), func(m string) bool { return m == "" })
	s.mu.Lock()
	w := s.workers[role]
	if w != nil {
		if w.modelHolds == nil {
			w.modelHolds = map[string]int{}
		}
		for _, m := range models {
			w.modelHolds[m]++
		}
	}
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			if w == nil {
				return
			}
			s.mu.Lock()
			for _, m := range models {
				if w.modelHolds[m]--; w.modelHolds[m] <= 0 {
					delete(w.modelHolds, m)
				}
			}
			s.mu.Unlock()
		})
	}
}

// heldLocked reports whether a Hold or HoldModels keeps w's model m. Caller holds s.mu.
func (w *worker) heldLocked(m string) bool {
	return w.holds > 0 || w.modelHolds[m] > 0
}

func sameModels(a, b []proto.LoadedModel) bool {
	return slices.EqualFunc(a, b, func(x, y proto.LoadedModel) bool { return x.Model == y.Model })
}

// sameBusy: the same models, each as busy (or idle) as before.
func sameBusy(a, b []proto.LoadedModel) bool {
	return slices.EqualFunc(a, b, func(x, y proto.LoadedModel) bool {
		return x.Model == y.Model && (x.IdleSeconds > 0) == (y.IdleSeconds > 0)
	})
}

// budget is the bytes resident models may take together: the setting
// (Config.MemoryBudget), else 60 % of physical memory; with the ActNow
// family (Config.Family) also no more than the family's share less what
// the other family apps hold. 0 = no limit known. Called without s.mu
// (the family reads its runtime files).
func (s *supervisor) budget() int64 {
	s.mu.Lock()
	own := s.cfg.MemoryBudget
	var physical int64
	for _, r := range s.roles {
		if w := s.workers[r]; w.status.Hello != nil && w.status.Hello.Device.Memory > 0 {
			physical = w.status.Hello.Device.Memory
			break
		}
	}
	fam := s.cfg.Family
	s.mu.Unlock()
	if fam != nil {
		return fam.Budget(own, physical)
	}
	if own > 0 {
		return own
	}
	return physical * 6 / 10
}

// candidate is a resident model that may be unloaded.
type candidate struct {
	role proto.Role
	m    proto.LoadedModel
}

// resident sums the resident models of the ready workers (as their last
// engine.stats said) and lists the idle ones, the longest idle first. A
// held worker's or model's (Hold, HoldModels) are counted but never
// listed: they stay.
func (s *supervisor) resident() (total int64, idle []candidate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.roles {
		w := s.workers[r]
		if w.status.State != WorkerReady || w.status.Stats == nil {
			continue
		}
		for _, m := range w.status.Stats.Models {
			total += m.Bytes
			if m.IdleSeconds > 0 && !w.heldLocked(m.Model) {
				idle = append(idle, candidate{w.role, m})
			}
		}
	}
	sort.SliceStable(idle, func(i, j int) bool { return idle[i].m.IdleSeconds > idle[j].m.IdleSeconds })
	return total, idle
}

// evict unloads the least recently used idle models while the resident
// models of all workers are over the budget, and every model idle for
// longer than IdleUnload.
func (s *supervisor) evict() {
	budget := s.budget()
	s.mu.Lock()
	idle := s.cfg.IdleUnload.Seconds()
	s.mu.Unlock()
	total, cands := s.resident()
	overBudget := budget > 0 && total > budget
	if !overBudget && idle <= 0 {
		return
	}
	unloaded := false
	for _, c := range cands {
		tooIdle := idle > 0 && c.m.IdleSeconds >= idle
		if !tooIdle && (budget <= 0 || total <= budget) {
			continue
		}
		why := "to stay within the memory budget"
		if tooIdle {
			why = "after the idle limit"
		}
		if s.unload(c, why) {
			total -= c.m.Bytes
			unloaded = true
		}
	}
	if unloaded {
		s.publishFamily()
	}
}

// unload asks c's worker to drop its model if it is still idle: not in
// use, and unused for at least as long as its last engine.stats said
// (a job may have taken it since; the engine checks, under its lock).
// The engine then clears MLX's buffer cache, so the memory goes back to
// the system. A dropped model leaves the worker's stats. It reports
// whether the engine dropped it.
func (s *supervisor) unload(c candidate, why string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	raw, err := s.Call(ctx, c.role, "", proto.MethodModelsUnload, proto.UnloadParams{Model: c.m.Model, IdleAtLeast: c.m.IdleSeconds}, nil)
	cancel()
	if err != nil {
		s.cfg.Logf("engine %s: unloading %s: %v", c.role, c.m.Model, err)
		return false
	}
	var res proto.UnloadResult
	if json.Unmarshal(raw, &res) == nil && res.Unloaded != nil && !slices.Contains(res.Unloaded, c.m.Model) {
		s.cfg.Logf("engine %s: kept %s: used again since", c.role, c.m.Model)
		return false
	}
	s.cfg.Logf("engine %s: unloaded %s (idle %.0f s) %s", c.role, c.m.Model, c.m.IdleSeconds, why)
	s.mu.Lock()
	w := s.workers[c.role]
	if w.status.Stats != nil {
		st := *w.status.Stats
		st.Models = slices.DeleteFunc(slices.Clone(st.Models), func(m proto.LoadedModel) bool { return m.Model == c.m.Model })
		w.status.Stats = &st
	}
	st := s.statusLocked(w)
	s.mu.Unlock()
	s.publish(st)
	return true
}
