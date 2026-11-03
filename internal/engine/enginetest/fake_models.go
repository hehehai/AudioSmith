package enginetest

// The fake's models and jobs: what is loaded, loading a model, the steps
// of a job (with the slow/oom/crash behaviours) and tts.synthesize.

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

func (f *fakeEngine) loadedLocked() []proto.LoadedModel {
	out := []proto.LoadedModel{}
	for model, used := range f.models {
		idle := time.Since(used).Seconds()
		if len(f.running) > 0 {
			idle = 0 // simplification: everything counts as in use while a job runs
		}
		out = append(out, proto.LoadedModel{Model: model, Task: taskOf(model), SampleRate: 24000, Bytes: 100, IdleSeconds: idle})
	}
	slices.SortFunc(out, func(a, b proto.LoadedModel) int { return strings.Compare(a.Model, b.Model) })
	return out
}

// taskOf guesses a model's catalogue task from its repo id.
func taskOf(model string) string {
	l := strings.ToLower(model)
	switch {
	case strings.Contains(l, "aligner"):
		return "align"
	case strings.Contains(l, "whisper"), strings.Contains(l, "asr"), strings.Contains(l, "parakeet"):
		return "stt"
	case strings.Contains(l, "vad"):
		return "vad"
	case strings.Contains(l, "deepfilter"):
		return "enhance"
	case strings.Contains(l, "gemma"), strings.Contains(l, "qwen3-") && !strings.Contains(l, "tts"):
		return "llm"
	}
	return "tts"
}

func (f *fakeEngine) cancelled(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cancel[id]
}

// ensure loads model (job id reports progress); false when it failed (answered).
func (f *fakeEngine) ensure(id, model string) bool {
	f.mu.Lock()
	_, ok := f.models[model]
	f.mu.Unlock()
	if ok {
		return true
	}
	if strings.Contains(model, "broken") {
		f.fail(id, proto.CodeModelLoadFailed, "Unsupported model type: nil")
		return false
	}
	f.notify(proto.MethodJobProgress, proto.ProgressParams{Job: id, Stage: "loading", Message: "Loading " + model})
	f.mu.Lock()
	f.models[model] = time.Now()
	f.mu.Unlock()
	return true
}

func (f *fakeEngine) touch(model string) {
	f.mu.Lock()
	f.models[model] = time.Now()
	f.mu.Unlock()
}

func (f *fakeEngine) load(m proto.Message) {
	var p proto.LoadParams
	if err := m.DecodeParams(&p); err != nil || p.Model == "" {
		f.fail(m.ID, proto.CodeBadRequest, "Missing model")
		return
	}
	if level, ok := strings.CutPrefix(p.Model, "pressure/"); ok {
		f.pressure(m.ID, level)
		return
	}
	if !f.ensure(m.ID, p.Model) {
		return
	}
	task := p.Task
	if task == "" {
		task = "tts"
	}
	res := proto.LoadResult{Model: p.Model, Task: task, SampleRate: 24000, Seconds: 0.01}
	if task == "tts" {
		res.Voices = []string{"af_heart"}
	} else {
		res.SampleRate = 16000
	}
	f.result(m.ID, res)
}

// pressure plays a memory-pressure event at level (models.load of
// "pressure/<level>"): as the engine's MemoryWatch, the idle models go
// (from "warning" up) and engine.memory says so; at "critical" the load
// is refused with oom, else it answers without loading anything.
func (f *fakeEngine) pressure(id, level string) {
	unloaded := []string{}
	if level != proto.PressureNormal {
		f.mu.Lock()
		for model := range f.models {
			delete(f.models, model)
			unloaded = append(unloaded, model)
		}
		f.mu.Unlock()
		slices.Sort(unloaded)
	}
	f.notify(proto.MethodEngineMemory, proto.MemoryParams{Level: level, Unloaded: unloaded})
	if level == proto.PressureCritical {
		f.send(proto.NewError(id, &proto.Error{Code: proto.CodeOOM, Message: "The Mac is critically low on memory",
			Data: json.RawMessage(`{"reason":"memory_pressure","level":"critical"}`)}))
		return
	}
	f.result(id, proto.LoadResult{Model: "pressure/" + level, Task: "tts"})
}

// work runs the steps of a job whose behaviour key (text or input path)
// is key, reporting stage; false when the job ended (answered).
func (f *fakeEngine) work(id, key, stage string, each func(i, steps int)) bool {
	steps, step := 3, time.Millisecond
	switch {
	case strings.Contains(key, "slow"):
		steps, step = 100, 20*time.Millisecond
	case strings.Contains(key, "oom"):
		f.send(proto.NewError(id, &proto.Error{Code: proto.CodeOOM, Message: "Out of memory while generating", Data: json.RawMessage(`{"active":1}`)}))
		return false
	}
	for i := range steps {
		if f.cancelled(id) {
			f.fail(id, proto.CodeCancelled, "Cancelled")
			return false
		}
		if strings.Contains(key, "crash") && i == 1 {
			fmt.Fprintln(os.Stderr, "fatal: boom")
			os.Exit(3)
		}
		time.Sleep(step)
		if each != nil {
			each(i, steps)
		} else {
			frac := float64(i+1) / float64(steps)
			f.notify(proto.MethodJobProgress, proto.ProgressParams{Job: id, Stage: stage, Fraction: &frac})
		}
	}
	return true
}

// FakeKokoroLimit is the longest text, in characters, the fake lets a
// Kokoro model take in one request (the real limit is 510 phonemes).
const FakeKokoroLimit = 500

func (f *fakeEngine) synthesize(m proto.Message) {
	var p proto.SynthesizeParams
	if err := m.DecodeParams(&p); err != nil || p.Model == "" {
		f.fail(m.ID, proto.CodeBadRequest, "Missing model")
		return
	}
	if !f.ensure(m.ID, p.Model) {
		return
	}
	lower := strings.ToLower(p.Model)
	kokoro := strings.Contains(lower, "kokoro")
	if n := len([]rune(p.Text)); kokoro && n > FakeKokoroLimit {
		f.failData(m.ID, proto.CodeInputTooLong, map[string]any{"limit": FakeKokoroLimit, "actual": n, "unit": "chars"},
			"The text is %d characters; %s takes at most %d per request", n, p.Model, FakeKokoroLimit)
		return
	}
	if !f.useVoicePrompt(m.ID, p) {
		return
	}
	if p.RefAudio != "" && (kokoro || strings.Contains(lower, "customvoice") || strings.Contains(lower, "voicedesign")) {
		f.failData(m.ID, proto.CodeUnsupported, map[string]string{"capability": "clone"}, "%s cannot clone voices", p.Model)
		return
	}
	if p.RefAudio != "" && !f.inputOK(m.ID, p.RefAudio) {
		return
	}
	var steps int
	ok := f.work(m.ID, p.Text, "generating", func(i, n int) {
		steps = n
		frac := float64(i+1) / float64(n)
		f.notify(proto.MethodJobProgress, proto.ProgressParams{Job: m.ID, Stage: "generating", Fraction: &frac, AudioSeconds: 0.1 * float64(i+1)})
		if p.Stream {
			pcm := make([]byte, 0, 4)
			pcm = append(pcm, byte(i), 0, byte(i), 0)
			f.notify(proto.MethodTTSChunk, proto.ChunkParams{Job: m.ID, Seq: i, SampleRate: 24000, Channels: 1, Encoding: "pcm16le",
				Data: pcm, Final: i == n-1})
		}
	})
	if !ok {
		return
	}
	audio := Tone(24000, 1, 0.1*float64(steps), 220, 0.25)
	if p.Output.Path != "" {
		if err := WriteWAV(p.Output.Path, audio, p.Output.Bits); err != nil {
			f.fail(m.ID, proto.CodeInternal, "Could not write %s: %v", p.Output.Path, err)
			return
		}
	}
	f.touch(p.Model)
	res := proto.SynthesizeResult{Output: p.Output.Path, Model: p.Model, SampleRate: 24000, Channels: 1,
		Frames: int64(audio.Frames()), AudioSeconds: audio.Seconds(), Seconds: 0.05, RTF: audio.Seconds() / 0.05}
	if p.Seed != nil {
		res.Seed = p.Seed
	}
	f.result(m.ID, res)
}
