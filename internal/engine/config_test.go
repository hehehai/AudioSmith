package engine

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

// waitConfig waits until role's engine says cond of what engine.config applies.
func waitConfig(t *testing.T, s Supervisor, role proto.Role, what string, cond func(proto.EngineConfigResult) bool) {
	t.Helper()
	sup := s.(*supervisor)
	deadline := time.Now().Add(5 * time.Second)
	for {
		sup.mu.Lock()
		got := sup.workers[role].configApplied
		sup.mu.Unlock()
		if cond(got) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: engine.config never applied %s (last %+v)", role, what, got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Code review 1103 F4: the engines' own makeRoom keeps to the app's
// memory limit setting: told at start (60 % of the Mac's memory without a
// setting) and again when it changes.
func TestEngineConfigFollowsMemoryBudget(t *testing.T) {
	s, w := newTestSup(t, "")
	w.until(t, "ready", ready)
	const def = (16 << 30) * 6 / 10 // the fake's 16 GB
	waitConfig(t, s, proto.RoleGPU, "the default budget", func(r proto.EngineConfigResult) bool { return r.MemoryBudget == def })
	s.SetMemoryBudget(20 << 30)
	waitConfig(t, s, proto.RoleGPU, "20 GB", func(r proto.EngineConfigResult) bool { return r.MemoryBudget == 20<<30 })
	s.SetMemoryBudget(0)
	waitConfig(t, s, proto.RoleGPU, "the default again", func(r proto.EngineConfigResult) bool { return r.MemoryBudget == def })
}

// Code review 1103 F5: a "warning" memory pressure leaves the models the
// app holds (a dictation's HoldModels, a call's Hold of the whole worker);
// "critical" still drops them, and releasing the holds tells the engine.
func TestMemoryPressureWarningKeepsHeldModels(t *testing.T) {
	s, w := newTestSup(t, "")
	w.until(t, "ready", ready)
	ctx := context.Background()
	load := func(models ...string) {
		t.Helper()
		for _, m := range models {
			if _, err := s.Call(ctx, proto.RoleGPU, "L-"+m, proto.MethodModelsLoad, proto.LoadParams{Model: m}, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	pressure := func(level string) {
		t.Helper()
		s.Call(ctx, proto.RoleGPU, "P-"+level, proto.MethodModelsLoad, proto.LoadParams{Model: "pressure/" + level}, nil)
	}
	const llm, tts = "mlx-community/Qwen3-4B-Instruct-2507-4bit", "a/tts"
	load(llm, tts)
	release := s.HoldModels(proto.RoleGPU, llm)
	waitConfig(t, s, proto.RoleGPU, "the held LLM", func(r proto.EngineConfigResult) bool { return slices.Equal(r.Held, []string{llm}) })
	pressure(proto.PressureWarning)
	waitLoaded(t, s, 1)
	if got := loadedModels(t, s); !slices.Equal(got, []string{llm}) {
		t.Fatalf("after a warning: %v, want the held %s", got, llm)
	}

	load(tts)
	releaseCall := s.Hold(proto.RoleGPU)
	waitConfig(t, s, proto.RoleGPU, "holdAll", func(r proto.EngineConfigResult) bool { return r.HoldAll })
	pressure(proto.PressureWarning)
	time.Sleep(100 * time.Millisecond)
	if got := loadedModels(t, s); len(got) != 2 {
		t.Fatalf("a call's worker after a warning: %v", got)
	}
	pressure(proto.PressureCritical)
	waitLoaded(t, s, 0)

	release()
	releaseCall()
	waitConfig(t, s, proto.RoleGPU, "no holds", func(r proto.EngineConfigResult) bool { return len(r.Held) == 0 && !r.HoldAll })
}
