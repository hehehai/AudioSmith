package engine

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

// waitStats waits until role's last engine.stats lists want models.
func waitStats(t *testing.T, s Supervisor, role proto.Role, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, st := range s.Status() {
			if st.Role == role && st.Stats != nil && len(st.Stats.Models) == want {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: stats never listed %d models", role, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Code review 1102 M1: the gpu worker's engine.memory at "warning" makes
// the supervisor unload the other workers' idle models too, except held
// ones: a HoldModels pins its model, not the rest of the worker's.
func TestMemoryPressureUnloadsOtherWorkersIdleModels(t *testing.T) {
	s, w := newTestSup(t, "", func(c *Config) { c.OnDemand = []proto.Role{proto.RoleListen} })
	w.until(t, "ready", ready)
	ctx := context.Background()
	const asr, other = "mlx-community/Qwen3-ASR-0.6B-8bit", "mlx-community/whisper-large-v3-turbo-4bit"
	if _, err := s.Call(ctx, proto.RoleGPU, "L1", proto.MethodModelsLoad, proto.LoadParams{Model: "a/one"}, nil); err != nil {
		t.Fatal(err)
	}
	for i, m := range []string{asr, other} {
		if _, err := s.Call(ctx, proto.RoleListen, "LL"+string(rune('0'+i)), proto.MethodModelsLoad, proto.LoadParams{Model: m, Task: "stt"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	waitStats(t, s, proto.RoleListen, 2)
	release := s.HoldModels(proto.RoleListen, asr)
	time.Sleep(30 * time.Millisecond) // idle a while, as the stats will say

	notify := func(m proto.Message) {
		if m.Method == proto.MethodEngineMemory {
			t.Error("engine.memory went to the job's notify: it names no job")
		}
	}
	if _, err := s.Call(ctx, proto.RoleGPU, "P1", proto.MethodModelsLoad, proto.LoadParams{Model: "pressure/" + proto.PressureWarning}, notify); err != nil {
		t.Fatal(err)
	}
	waitLoaded(t, s, 0)                                    // the engine dropped its own
	waitLoadedOn(t, s, proto.RoleListen, 1, 5*time.Second) // the supervisor the listen worker's idle one
	if got := loadedOn(t, s, proto.RoleListen); !slices.Equal(got, []string{asr}) {
		t.Fatalf("listen kept %v, want the held %s", got, asr)
	}
	release()
	release()
	s.(*supervisor).mu.Lock()
	n := len(s.(*supervisor).workers[proto.RoleListen].modelHolds)
	s.(*supervisor).mu.Unlock()
	if n != 0 {
		t.Fatalf("holds left after release: %d", n)
	}
}

// At "critical" the engine refuses the load with a retryable oom that
// says why (memory_pressure).
func TestMemoryPressureCriticalRefusesLoads(t *testing.T) {
	s, w := newTestSup(t, "")
	w.until(t, "ready", ready)
	_, err := s.Call(context.Background(), proto.RoleGPU, "P1", proto.MethodModelsLoad, proto.LoadParams{Model: "pressure/" + proto.PressureCritical}, nil)
	var perr *proto.Error
	if !errors.As(err, &perr) || perr.Code != proto.CodeOOM || !proto.Retryable(perr.Code) {
		t.Fatalf("err %v", err)
	}
	var data struct{ Reason, Level string }
	if json.Unmarshal(perr.Data, &data) != nil || data.Reason != proto.ReasonMemoryPressure || data.Level != proto.PressureCritical {
		t.Fatalf("data %s", perr.Data)
	}
}

// HoldModels keeps only its models from the idle limit; the worker's
// other models go.
func TestHoldModelsPinsOnlyItsModels(t *testing.T) {
	s, w := newTestSup(t, "", func(c *Config) { c.IdleUnload = 200 * time.Millisecond })
	w.until(t, "ready", ready)
	ctx := context.Background()
	release := s.HoldModels(proto.RoleGPU, "mlx-community/Qwen3-4B-Instruct-2507-4bit", "")
	for _, m := range []string{"mlx-community/Qwen3-4B-Instruct-2507-4bit", "a/tts"} {
		if _, err := s.Call(ctx, proto.RoleGPU, "L-"+m, proto.MethodModelsLoad, proto.LoadParams{Model: m}, nil); err != nil {
			t.Fatal(err)
		}
	}
	waitLoaded(t, s, 1)
	time.Sleep(400 * time.Millisecond) // twice the limit
	if got := loadedModels(t, s); !slices.Equal(got, []string{"mlx-community/Qwen3-4B-Instruct-2507-4bit"}) {
		t.Fatalf("loaded %v", got)
	}
	release()
	waitLoaded(t, s, 0)
}
