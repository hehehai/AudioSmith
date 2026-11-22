package services

import (
	"testing"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

func voicevoxInfo(string) ModelInfo {
	return ModelInfo{Traits: TraitsFor("voicevox", []string{"speed"}), Revision: ""}
}

// A take of an outside engine (VOICEVOX, thread #171): its scales reach
// the engine and the chunk keys, its silences frame the take, its
// credits are kept with it, and its script is not normalised.
func TestRenderExternalEngine(t *testing.T) {
	e := newRenderEnv(t, voicevoxInfo)
	e.credit = func(model, voice string) string {
		if model == "external/voicevox" && voice == "voicevox:u/3" {
			return "VOICEVOX:ずんだもん"
		}
		return ""
	}
	recipe := core.Take{Text: "今日は2026年11月22日です。", Model: "external/voicevox", Voice: "voicevox:u/3", Language: "ja",
		Scales: &core.Scales{Pitch: 0.05, Intonation: 1.3, PreSilence: 0.5, PostSilence: 0.7}}
	j, tk := e.generate(t, recipe)
	if j.State != jobs.StateSucceeded {
		t.Fatalf("job %s: %+v", j.State, j)
	}
	if tk.Credit != "VOICEVOX:ずんだもん" {
		t.Fatalf("credit %q", tk.Credit)
	}
	calls := e.synth.calls
	if len(calls) != 1 || calls[0].Scales == nil || calls[0].Scales.Pitch != 0.05 || calls[0].Scales.Intonation != 1.3 {
		t.Fatalf("calls %+v", calls)
	}
	if calls[0].Text != "今日は2026年11月22日です。" {
		t.Fatalf("the script was normalised: %q", calls[0].Text)
	}
	// The silences: the take is longer than the same take without them.
	buf, _, err := wav.ReadFile(e.svc.Render.store.AudioPath(tk))
	if err != nil {
		t.Fatal(err)
	}
	plain := recipe
	plain.ID, plain.Scales = "", &core.Scales{Pitch: 0.05, Intonation: 1.3}
	_, tk2 := e.generate(t, plain)
	buf2, _, err := wav.ReadFile(e.svc.Render.store.AudioPath(tk2))
	if err != nil {
		t.Fatal(err)
	}
	if d := buf.Seconds() - buf2.Seconds(); d < 1.1 || d > 1.3 {
		t.Fatalf("silences added %.2f s, want 1.2", d)
	}
	// Same scales: the chunk came from the cache; other scales: made again.
	if len(e.synth.calls) != 1 {
		t.Fatalf("%d calls, want the cached chunk", len(e.synth.calls))
	}
	other := plain
	other.Scales = &core.Scales{Pitch: -0.05}
	e.generate(t, other)
	if len(e.synth.calls) != 2 {
		t.Fatalf("%d calls: other scales came from the cache", len(e.synth.calls))
	}
}
