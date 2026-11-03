package main

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/llm"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/translate"
)

// warmLoads are the warm-up loads of the engine's language model sent so far.
func warmLoads(m *m2Test) []sentReq {
	var out []sentReq
	for _, r := range m.eng.sent {
		if r.Cmd == "load" && r.Spec.Owner == llmWarmOwner {
			out = append(out, r)
		}
	}
	return out
}

// The engine's language model loads when dictation with clean-up starts
// (not at app start), a cold clean-up gets a longer budget, a resident
// model the normal one; the direction coach warms it while a note is
// written and waits longer when cold (thread #47, from #44 §4.3–4.4).
func TestLocalLLMWarm(t *testing.T) {
	m := newM2Test(t)
	a, keys, tt := m.a, m.keys, m.tt
	m.eng.ready()
	e, _ := catalog.Builtin().ByID(catalog.DefaultLLM)
	var budgets []time.Duration
	var answer string
	a.localLLMHook = func(ctx context.Context, method string, params any, notify func(string, json.RawMessage)) (json.RawMessage, error) {
		if d, ok := llm.Remaining(ctx); ok {
			budgets = append(budgets, d.Round(time.Second))
		}
		raw, _ := json.Marshal(map[string]any{"text": answer, "finishReason": "stop"})
		return raw, nil
	}
	a.setLocal(e, hub.Local{State: hub.Downloaded, Snapshot: t.TempDir()}, nil)
	if !a.localLLMFirst(llm.SkillDictationCleanup) || !a.localLLMCold(llm.SkillDictationCleanup) {
		t.Fatal("the engine's model is not first, or not cold")
	}
	if n := len(warmLoads(m)); n != 0 {
		t.Fatalf("%d warm-ups before any use", n)
	}

	// Dictation starts: the model loads while the user speaks.
	a.updateSettings(func(s *settings.Settings) { s.Dictation.Cleanup = true })
	keys.press(t, defaultAcc)
	loads := warmLoads(m)
	if len(loads) != 1 || loads[0].Model != e.Repo || !strings.Contains(string(loads[0].Params), `"task":"llm"`) || loads[0].Spec.Kind != proto.MethodModelsLoad {
		t.Fatalf("warm-up %+v", loads)
	}
	// It pins its recogniser and the clean-up model, not the gpu worker's
	// other models (code review 1102 M1).
	if h := m.eng.held(); !slices.Equal(h, []string{"listen/" + a.dictModel().Repo, "gpu/" + e.Repo}) {
		t.Fatalf("held %v", h)
	}
	keys.press(t, defaultAcc)
	answer = `{"text":"Meet me at five."}`
	m.finishDictation(t, "meet me at four no wait at five", "en")
	if a.dict.text != "Meet me at five." || len(budgets) != 1 || budgets[0] != coldCleanupBudget {
		t.Fatalf("cold clean-up: %q, budgets %v", a.dict.text, budgets)
	}
	if !a.dict.warming || a.dict.status() == "" {
		t.Error("the cold clean-up was not flagged")
	}
	// A second dictation soon after: the warm-up is not sent again.
	keys.press(t, defaultAcc)
	keys.press(t, defaultAcc)
	if n := len(warmLoads(m)); n != 1 {
		t.Errorf("%d warm-ups", n)
	}
	m.finishDictation(t, "meet me at four no wait at five", "en")

	// Resident: no warm-up, the normal budget.
	m.eng.status(engine.WorkerStatus{Role: proto.RoleGPU, State: engine.WorkerReady, PID: 42,
		Hello: &proto.HelloResult{Protocol: 2, Role: proto.RoleGPU, Concurrency: 1},
		Stats: &proto.StatsResult{Models: []proto.LoadedModel{{Model: e.Repo, Task: "llm"}}}})
	if a.localLLMCold(llm.SkillDictationCleanup) {
		t.Fatal("still cold")
	}
	a.warmAt = time.Time{}
	budgets = nil
	keys.press(t, defaultAcc)
	keys.press(t, defaultAcc)
	m.finishDictation(t, "meet me at four no wait at five", "en")
	if n := len(warmLoads(m)); n != 1 || len(budgets) != 1 || budgets[0] != translate.DefaultCleanupBudget || a.dict.warming {
		t.Errorf("warm: %d warm-ups, budgets %v", n, budgets)
	}

	// Clean-up off: dictation does not load the language model.
	m.eng.status(engine.WorkerStatus{Role: proto.RoleGPU, State: engine.WorkerReady, PID: 42,
		Hello: &proto.HelloResult{Protocol: 2, Role: proto.RoleGPU, Concurrency: 1}, Stats: &proto.StatsResult{}}) // unloaded since
	a.warmAt = time.Time{}
	a.updateSettings(func(s *settings.Settings) { s.Dictation.Cleanup = false })
	keys.press(t, defaultAcc)
	keys.press(t, defaultAcc)
	m.finishDictation(t, "hello there", "en")
	if n := len(warmLoads(m)); n != 1 {
		t.Errorf("clean-up off: %d warm-ups", n)
	}

	// The direction coach: typing a note warms the model; a cold reading
	// waits longer and says it is loading.
	a.toolTab = 3
	a.navigate("/tools")
	tt.Frame()
	if n := len(warmLoads(m)); n != 1 {
		t.Errorf("empty note: %d warm-ups", n)
	}
	a.dir.note = "more urgent, a little scared"
	tt.Frame()
	if n := len(warmLoads(m)); n != 2 {
		t.Errorf("note: %d warm-ups", n)
	}
	budgets = nil
	answer = `{"emotions":["urgent","fearful"],"instruct":"urgent, a little scared","rate":0.2,"pitch":1,"energy":0.3}`
	a.runDirection()
	if len(budgets) != 1 || budgets[0] != coldCoachBudget || !a.dir.warming || a.dir.res == nil || a.dir.res.Source != "llm" {
		t.Errorf("coach: budgets %v, warming %v, %+v", budgets, a.dir.warming, a.dir.res)
	}
}
