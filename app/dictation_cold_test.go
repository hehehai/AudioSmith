package main

import (
	"os"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

// dictLoads lists the model loads dictation submitted.
func dictLoads(eng *fakeEngine) []sentReq {
	var out []sentReq
	for _, r := range eng.sent {
		if r.Spec.Owner == dictLoadOwner {
			out = append(out, r)
		}
	}
	return out
}

// #109: after the idle limit unloaded dictation's model, the press starts
// loading it on the listen worker; the transcription waits behind the
// load with the recording kept, the pill says 「正在加载模型…」 once it
// takes longer than half a second, and the dictation's models are held
// from the press to the end.
func TestDictationColdModel(t *testing.T) {
	m := newM2Test(t)
	a, eng, keys := m.a, m.eng, m.keys
	var later []func()
	a.dictAfter = func(_ time.Duration, fn func()) { later = append(later, fn) }

	keys.press(t, defaultAcc)
	s := a.dict
	if s == nil || s.phase != dictListening {
		t.Fatalf("listening: %+v", s)
	}
	loads := dictLoads(eng)
	if len(loads) != 1 || loads[0].Spec.Kind != proto.KindFor(proto.RoleListen, proto.MethodModelsLoad) || loads[0].Spec.Lane != jobs.LaneListen {
		t.Fatalf("no prewarm on the press: %+v", loads)
	}
	if h := eng.held(); !slices.Equal(h, []string{"listen/" + a.dictModel().Repo}) {
		t.Fatalf("only dictation's model should be held while dictating: %v", h)
	}
	load := loads[0].ID
	eng.progress(load, "loading", -1, 0) // still loading when the user stops

	if err := os.WriteFile(s.out, []byte("RIFF"), 0o644); err != nil { // what the engine records
		t.Fatal(err)
	}
	keys.press(t, defaultAcc) // stop
	if s.phase != dictTranscribing || !s.loading {
		t.Fatalf("phase %v loading %v", s.phase, s.loading)
	}
	r, req := transcribeReq(t, eng)
	if req.Input != s.out || req.Role != proto.RoleListen {
		t.Fatalf("request %+v (recording %s)", req, s.out)
	}
	if _, err := os.Stat(s.out); err != nil {
		t.Fatalf("the recording is gone: %v", err)
	}
	eng.update(r.ID, func(j *jobs.Job) { j.State = jobs.StateQueued }) // behind the load in the listen lane

	pill := ui.NewTester(a.pillView, pillWinWidth, pillWinHeight)
	pill.Frame()
	if !pill.HasText("Transcribing…") || pill.HasText("Loading model…") {
		t.Errorf("pill before half a second: %q", pill.Texts())
	}
	for _, fn := range later {
		fn()
	}
	pill.Frame()
	if !pill.HasText("Loading model…") {
		t.Errorf("pill after half a second: %q", pill.Texts())
	}

	eng.succeed(load, proto.LoadResult{Model: req.Model, Task: "stt"})
	eng.progress(r.ID, "transcribing", 0.5, 0)
	pill.Frame()
	if s.loading || !pill.HasText("Transcribing…") {
		t.Errorf("pill once loaded: %q", pill.Texts())
	}
	m.finishDictation(t, "hello there", "en")
	if s.phase != dictInserted || s.text == "" {
		t.Fatalf("delivered: %v %q %q", s.phase, s.text, s.note)
	}
	if h := eng.held(); len(h) != 0 {
		t.Errorf("holds left after the dictation: %v", h)
	}
}

// #109: no load when the listen worker has the model resident.
func TestDictationWarmModelNoLoad(t *testing.T) {
	m := newM2Test(t)
	a, eng, keys := m.a, m.eng, m.keys
	eng.status(engine.WorkerStatus{Role: proto.RoleListen, State: engine.WorkerReady,
		Stats: &proto.StatsResult{Models: []proto.LoadedModel{{Model: a.dictModel().Repo, Task: "stt", IdleSeconds: 60}}}})
	keys.press(t, defaultAcc)
	if loads := dictLoads(eng); len(loads) != 0 {
		t.Fatalf("loaded a resident model: %+v", loads)
	}
	keys.press(t, defaultAcc)
	if a.dict.loading {
		t.Error("loading with the model resident")
	}
	a.dictCancel()
	if h := eng.held(); len(h) != 0 {
		t.Errorf("holds left after a cancel: %v", h)
	}
}
