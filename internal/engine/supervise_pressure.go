package engine

// The system's memory pressure (code review 1102 M1): the gpu worker
// watches it (MemoryWatch.swift), unloads its idle models from "warning"
// up, refuses loads at "critical" (a retryable oom), and says so with
// engine.memory. The supervisor then unloads the other workers' idle
// models too (holds still apply: a dictation keeps its recogniser) and
// refreshes its view of the worker's.

import (
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

// memoryPressure handles p's engine.memory notification m.
func (s *supervisor) memoryPressure(p *proc, m proto.Message) {
	var mp proto.MemoryParams
	if m.DecodeParams(&mp) != nil {
		return
	}
	s.cfg.Logf("engine %s: memory pressure %s (it unloaded %d idle models)", p.w.role, mp.Level, len(mp.Unloaded))
	p.poll() // its resident models changed
	if mp.Level != proto.PressureWarning && mp.Level != proto.PressureCritical {
		return
	}
	_, idle := s.resident()
	unloaded := false
	for _, c := range idle {
		if c.role != p.w.role && s.unload(c, "under memory pressure") {
			unloaded = true
		}
	}
	if unloaded {
		s.publishFamily()
	}
}
