package main

// Tests of a voice from a clip: upload, the rules about lengths, no ASR,
// recording and using once, replacing a reference; split from
// voices_test.go.

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp/dsptest"
	"github.com/hehehai/AudioSmith/internal/dsp/loudness"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

func TestCloneFlowUpload(t *testing.T) {
	a, eng := newVoiceTestApp(t)
	tt := ui.NewTester(a.view, 1200, 1800)
	a.navigate("/voices")
	tt.Frame()
	tt.Click("Clone a voice")
	tt.Frame()
	if a.router.Path() != "/voices/new/clone" || !tt.HasText("Drop a clip here, or click to choose") || !tt.HasText("Record") {
		t.Fatalf("clone page: %s %q", a.router.Path(), tt.Texts())
	}
	// A 30 s clip: by energy, its best 15 s start at 12 s, at once.
	clip := filepath.Join(t.TempDir(), "long.wav")
	writeSpeech(clip, 30, []span{{0.5, 1.2}, {12, 18}, {18.4, 26}, {27, 29}})
	a.cloneDropped([]string{clip})
	tt.Frame()
	s := &a.clone
	if !s.hasClip() || s.seconds < 29.9 || len(s.peaks) != peakCount {
		t.Fatalf("clip %+v", s)
	}
	if s.selLo < 11 || s.selLo > 12 || s.sel().Len() > refIdealHigh+0.01 {
		t.Errorf("best window %.2f–%.2f", s.selLo, s.selHi)
	}
	// Then the engine's VAD (Silero) has its say, while the user hasn't.
	vad := eng.lastOf(t, proto.MethodVADDetect)
	if p := params[proto.VADParams](t, vad); p.Audio != s.work || p.Model != defaultModel(catalog.DefaultVAD) {
		t.Errorf("vad params %+v", p)
	}
	eng.succeed(vad.ID, proto.VADResult{Regions: []proto.Region{{Start: 2, End: 9}, {Start: 9.5, End: 16}}})
	if s.selLo < 1.8 || s.selLo > 2 {
		t.Errorf("VAD window %.2f–%.2f", s.selLo, s.selHi)
	}
	// The kept part is transcribed.
	p := answerTranscribe(t, eng, "This is what my voice sounds like.")
	if p.Model != defaultModel(catalog.DefaultSTT) {
		t.Errorf("stt model %q", p.Model)
	}
	if s.transcript != "This is what my voice sounds like." || s.asr != "done" {
		t.Errorf("transcript %q %s", s.transcript, s.asr)
	}
	if !tt.HasText("long.wav") || !tt.HasText("Save as voice profile") {
		t.Errorf("clip view: %q", tt.Texts())
	}

	// Typing wins over a later machine transcript.
	s.transcript = "My own words."
	tt.Frame()
	tt.Click("Transcribe the part kept")
	answerTranscribe(t, eng, "Machine words.")
	if s.transcript != "My own words." || s.autoText != "Machine words." {
		t.Errorf("transcript overwritten: %q", s.transcript)
	}

	// Keep all: 30 s needs no transcript; over 75 s would.
	tt.Frame()
	tt.Click("Keep all")
	if s.sel().Len() < 29.9 || a.cloneProblem() != "" {
		t.Errorf("keep all: %v %q", s.sel(), a.cloneProblem())
	}
	tt.Click("Best part")
	if s.sel().Len() > refIdealHigh+0.01 {
		t.Error("best part")
	}

	// Saving needs a name.
	tt.Frame()
	tt.Click("Save voice")
	if len(a.voices) != 0 {
		t.Fatal("saved without a name")
	}
	s.name = "My Narrator"
	a.clonePortrait(testPNG(t, 300, 200))
	tt.Frame()
	if s.portrait == nil {
		t.Fatal("portrait not taken")
	}
	if err := tt.Click("Save voice"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.voices) != 1 {
		t.Fatalf("voices %+v", a.voices)
	}
	v := a.voices[0]
	if v.Name != "My Narrator" || v.RefText != "My own words." || v.Portrait != "portrait.png" || v.RefSeconds > refIdealHigh+0.01 || v.RefSeconds < 5 {
		t.Fatalf("voice %+v", v)
	}
	if secs, err := decodeWAV(a.voicePath(&v, v.RefAudio), filepath.Join(t.TempDir(), "x.wav")); err != nil || secs > 15.1 {
		t.Errorf("reference %v %v", secs, err)
	}
	// A clean file is not denoised; its part kept is tidied (−18 LUFS,
	// edges trimmed), the original kept for Undo, the check saved.
	for _, r := range eng.sent {
		if r.Cmd == proto.MethodEnhanceRun || r.Spec.Kind == proto.MethodEnhanceRun {
			t.Error("a clean clip was denoised")
		}
	}
	if b, _, err := wav.ReadFile(a.voicePath(&v, v.RefAudio)); err != nil || math.Abs(loudness.Integrated(b)+18) > 0.3 {
		t.Errorf("reference not evened out: %v", err)
	}
	if v.RefOriginal == "" || v.RefCheck == nil || v.RefCheck.Grade == "" || !slices.Contains(v.RefCheck.Cleaned, "tidied") {
		t.Errorf("original %q, check %+v", v.RefOriginal, v.RefCheck)
	}
	if a.router.Path() != "/voices/mine" || a.edit == nil || a.edit.id != v.ID {
		t.Errorf("after save: %s %+v", a.router.Path(), a.edit)
	}
	// Saved: the Clone page starts afresh next time (no clip, no name or
	// words), keeping the microphone's choices.
	if a.clone.hasClip() || a.clone.name != "" || a.clone.transcript != "" || a.clone.portrait != nil {
		t.Errorf("the clone form kept %q %q %q", a.clone.srcName, a.clone.name, a.clone.transcript)
	}
	a.navigate("/clone")
	tt.Frame()
	if !tt.HasText("Drop a clip here, or click to choose") || tt.HasText("My Narrator") {
		t.Errorf("Clone after a save: %q", tt.Texts())
	}
	a.navigate("/voices")
	tt.Frame()
	// The Studio's model (Kokoro) can't reuse prompts: none is encoded.
	for _, r := range eng.sent {
		if r.Cmd == proto.MethodTTSEncodeVoice {
			t.Error("encoded a prompt for Kokoro")
		}
	}
	// A model that can: the prompt is cached in the voice.
	e, _ := catalog.Builtin().ByRepo(qwenBase)
	e.Capabilities = append(e.Capabilities, catalog.CapVoicePrompt)
	a.encodePrompt(v, e)
	enc := eng.lastOf(t, proto.MethodTTSEncodeVoice)
	ep := params[proto.EncodeVoiceParams](t, enc)
	if ep.Model != qwenBase || ep.RefAudio != a.voicePath(&v, v.RefAudio) || ep.RefText != "My own words." {
		t.Fatalf("encode %+v", ep)
	}
	os.WriteFile(ep.PromptPath, []byte("prompt"), 0o644)
	eng.succeed(enc.ID, proto.EncodeVoiceResult{VoicePrompt: ep.PromptPath, Model: qwenBase, Format: "qwen3_tts/1"})
	if got := a.findVoice(v.ID); got.Prompts[qwenBase] == "" {
		t.Errorf("prompt not kept: %+v", got.Prompts)
	}
}

func TestCloneRules(t *testing.T) {
	for _, c := range []struct {
		secs  float64
		text  bool
		tone  string
		check string
	}{
		{0.5, false, "danger", "Too short"},
		{3, false, "warning", "Short"},
		{10, false, "ok", "Good length"},
		{40, false, "warning", "Longer than 15"},
		{80, false, "danger", "add the transcript"},
		{80, true, "warning", "long references"},
	} {
		msg, tone := refAdvice(c.secs, c.text)
		if tone != c.tone || !strings.Contains(msg, c.check) {
			t.Errorf("%v s: %s %q", c.secs, tone, msg)
		}
	}
	// No speech found: the first 15 s.
	if w := bestWindow(nil, 40, 15); w != (span{0, 15}) {
		t.Errorf("no speech %v", w)
	}
	// Short clips are kept whole.
	if w := bestWindow([]span{{1, 2}}, 9, 15); w != (span{0, 9}) {
		t.Errorf("short %v", w)
	}
	// The most speech wins…
	w := bestWindow([]span{{2, 9}, {10, 20}}, 40, 15)
	if w.Start < 9.89 || w.Start > 9.91 || w.End < 24.89 || w.End > 24.91 {
		t.Errorf("most speech %v", w)
	}
	// …and a word straddling the end is left out.
	for _, speech := range [][]span{
		{{0.5, 3}, {4, 11}, {11.6, 19}, {20, 27}, {28, 31}},
		{{1, 8}, {8.5, 12}, {12.5, 19}},
		{{2, 6}, {7, 13}, {14, 18}, {30, 33}},
	} {
		w := bestWindow(speech, 40, 15)
		if w.Len() > 15.0001 || w.Len() < 5 {
			t.Errorf("%v: length %v", speech, w)
		}
		for _, s := range speech {
			if s.Start < w.End && s.End > w.End && s.Start-w.Start >= refIdealLow {
				t.Errorf("%v: %v cuts %v", speech, w, s)
			}
		}
	}

	a, eng := newVoiceTestApp(t)
	a.startClone("")
	long := filepath.Join(t.TempDir(), "long.wav")
	writeSpeech(long, 80, []span{{1, 79}})
	a.cloneLoad(long, "long.wav", false)
	// The engine's VAD fails (no model): the energy VAD's window stays.
	eng.fail(eng.lastOf(t, proto.MethodVADDetect).ID, proto.CodeModelNotDownloaded, "no model")
	if a.clone.sel().Len() > 15.01 || a.clone.asr != "running" {
		t.Errorf("after VAD failure: %v %s", a.clone.sel(), a.clone.asr)
	}
	a.clone.selLo, a.clone.selHi = 0, 80
	a.clone.transcript = ""
	if p := a.cloneProblem(); !strings.Contains(p, "75 seconds") {
		t.Errorf("over 75 s: %q", p)
	}
	a.clone.transcript = "The whole text."
	if p := a.cloneProblem(); p != "" {
		t.Errorf("with transcript: %q", p)
	}
	a.clone.selLo, a.clone.selHi = 10, 10.5
	if p := a.cloneProblem(); !strings.Contains(p, "too short") {
		t.Errorf("short: %q", p)
	}

	// Anything else is decoded by the engine's media worker.
	a.cloneLoad("/x/interview.mp4", "interview.mp4", false)
	if a.clone.busy == "" {
		t.Error("not busy while decoding")
	}
	dec := eng.lastOf(t, proto.MethodMediaDecode)
	dp := params[proto.DecodeParams](t, dec)
	if dp.Input != "/x/interview.mp4" || dp.SampleRate != refRate || dp.Channels != 1 || dec.Spec.Lane != jobs.LaneMedia {
		t.Fatalf("decode %+v %s", dp, dec.Spec.Lane)
	}
	writeSpeech(dp.Output.Path, 8, []span{{0.5, 7.5}})
	eng.succeed(dec.ID, proto.DecodeResult{AudioOut: proto.AudioOut{Output: dp.Output.Path, SampleRate: refRate, Channels: 1, AudioSeconds: 8}})
	if a.clone.err != "" || a.clone.seconds != 8 || !a.clone.hasClip() || a.clone.asr != "running" {
		t.Errorf("mp4: %q %v", a.clone.err, a.clone.seconds)
	}
	// A file the engine can't read.
	a.cloneLoad("/x/broken.m4a", "broken.m4a", false)
	eng.fail(eng.lastOf(t, proto.MethodMediaDecode).ID, proto.CodeUnsupportedMedia, "bad")
	if !strings.Contains(a.clone.err, "format cannot be read") || a.clone.hasClip() {
		t.Errorf("broken: %q", a.clone.err)
	}
	// A slow, older clip loses to a newer one.
	a.cloneLoad("/x/a.mp3", "a.mp3", false)
	old := eng.lastOf(t, proto.MethodMediaDecode)
	short := filepath.Join(t.TempDir(), "short.wav")
	writeSpeech(short, 6, []span{{0.3, 5.7}})
	a.cloneLoad(short, "short.wav", false)
	op := params[proto.DecodeParams](t, old)
	writeSpeech(op.Output.Path, 8, nil)
	eng.succeed(old.ID, proto.DecodeResult{AudioOut: proto.AudioOut{Output: op.Output.Path, AudioSeconds: 8}})
	if a.clone.srcName != "short.wav" || a.clone.seconds > 6.1 {
		t.Errorf("older clip won: %s %v", a.clone.srcName, a.clone.seconds)
	}
}

func TestCloneNoASR(t *testing.T) {
	a, eng := newVoiceTestApp(t)
	tt := ui.NewTester(a.view, 1200, 1800)
	a.startClone("")
	clip := filepath.Join(t.TempDir(), "c.wav")
	writeSpeech(clip, 8, []span{{0.5, 7}})
	a.cloneLoad(clip, "c.wav", false)
	eng.fail(eng.lastOf(t, proto.MethodSTTTranscribe).ID, proto.CodeModelNotDownloaded, "Whisper is not downloaded")
	tt.Frame()
	if a.clone.asr != "noasr" || !tt.HasText("No speech-to-text model is installed: transcribing needs one.") {
		t.Fatalf("no ASR: %s %q", a.clone.asr, tt.Texts())
	}
	tt.Click("Retry transcription")
	eng.fail(eng.lastOf(t, proto.MethodSTTTranscribe).ID, proto.CodeOOM, "oom")
	tt.Frame()
	if a.clone.asr != "failed" || !tt.HasText("Transcription failed") {
		t.Errorf("failed: %s %q", a.clone.asr, tt.Texts())
	}
	tt.Click("Retry transcription")
	answerTranscribe(t, eng, "Hello.")
	if a.clone.transcript != "Hello." {
		t.Errorf("retry: %q", a.clone.transcript)
	}
	tt.Frame()
	tt.Click("Clean reference noise")
	eng.fail(eng.lastOf(t, proto.MethodEnhanceRun).ID, proto.CodeModelNotDownloaded, "no model")
	tt.Frame()
	if !tt.HasText("Could not clean the clip: The model is not downloaded. Download it in Settings → Models & network.") {
		t.Errorf("clean failure: %q", tt.Texts())
	}
}

func TestCloneRecordAndUseOnce(t *testing.T) {
	a, eng := newVoiceTestApp(t)
	tt := ui.NewTester(a.view, 1200, 1800)
	a.startClone("")
	tt.Frame()
	if len(a.clone.devices) != 3 {
		t.Fatalf("devices %+v", a.clone.devices)
	}
	a.clone.mic.device = a.clone.devices[1].ID
	a.clone.mic.channels = 1
	tt.Frame()
	if !tt.HasText("Fake USB Interface") {
		t.Errorf("device picker: %q", tt.Texts())
	}
	if err := tt.Click("Record"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	m := &a.clone.mic
	if m.phase != "recording" || !tt.HasText("Input detected") || !tt.HasText("Stop recording") {
		t.Fatalf("recording: %+v %q", m, tt.Texts())
	}
	start := params[proto.CaptureParams](t, eng.lastOf(t, proto.MethodCaptureStart))
	if start.Device != a.clone.devices[1].ID || start.Channels != 1 || !strings.HasPrefix(start.Output.Path, a.referencesDir()) {
		t.Errorf("capture params %+v", start)
	}
	writeSpeech(start.Output.Path, 6, []span{{0.3, 5.7}})
	addNoise(t, start.Output.Path, -40) // a fan
	tt.Click("Stop recording")
	tt.Frame()
	s := &a.clone
	if !s.hasClip() || s.srcName != "Recording" || s.busy == "" {
		t.Fatalf("after recording: %+v", s)
	}
	// A noisy recording is cleaned, then transcribed.
	answerEnhance(t, eng)
	answerTranscribe(t, eng, "Recorded words.")
	if !s.cleaned || !s.noisy || s.transcript != "Recorded words." {
		t.Fatalf("cleaned/transcribed: %+v", s)
	}
	tt.Frame()
	if !tt.HasText("Cleaned up: noise removed, silences trimmed, loudness evened out") ||
		!tt.HasText("There was background noise: it was removed automatically. For a better result, record again somewhere quiet.") {
		t.Errorf("cleaning: %q", tt.Texts())
	}
	tt.Click("Undo")
	if s.cleaned || s.original != "" || s.tidy {
		t.Error("undo clean")
	}
	tt.Frame()
	if !tt.HasText("Clean up") {
		t.Errorf("after undo: %q", tt.Texts())
	}

	// Use it once in the Studio: the model switches to one that clones.
	tt.Frame()
	if err := tt.Click("Use without saving"); err != nil {
		t.Fatal(err)
	}
	if a.router.Path() != "/studio" || a.studioClip == nil || a.preset().Repo != qwenBase {
		t.Fatalf("use once: %s %+v %s", a.router.Path(), a.studioClip, a.preset().Repo)
	}
	if !a.studioUsesSavedVoice(a.preset()) {
		t.Error("the Studio doesn't know")
	}
	take := a.recipe(time.Now())
	a.applyStudioVoice(&take, a.preset())
	if take.RefAudio != a.studioClip.Path || take.RefText != "Recorded words." || take.VoiceID != "" {
		t.Errorf("take %+v", take)
	}
	if _, err := os.Stat(take.RefAudio); err != nil {
		t.Error(err)
	}

	// The microphone refused.
	a.startClone("")
	eng.callErr[proto.MethodCaptureStart] = &proto.Error{Code: proto.CodePermissionDenied, Message: "denied"}
	tt.Frame()
	tt.Click("Record")
	tt.Frame()
	if !tt.HasText("AudioSmith may not use the microphone. Allow it in System Settings → Privacy & Security → Microphone.") {
		t.Errorf("denied: %q", tt.Texts())
	}
}

func TestReplaceReference(t *testing.T) {
	a, eng := newVoiceTestApp(t)
	tt := ui.NewTester(a.view, 1200, 1800)
	v := addVoice(t, a, core.Voice{Name: "Me", Kind: core.VoiceCloned, RefText: "old"}, 5)
	// Lock it to a take and verify it.
	tk := core.Take{ID: "take1", Text: "Locked.", Model: qwenBase, Seed: 9, VoiceID: v.ID, SampleRate: 24000, AudioSeconds: 1}
	addTake(t, a, tk, 1)
	ctx := context.Background()
	if _, err := a.lib.svc.Voices.Lock(ctx, v.ID, "take1"); err != nil {
		t.Fatal(err)
	}
	rec := filepath.Join(t.TempDir(), "consent.wav")
	writeSpeech(rec, 3, []span{{0, 3}})
	if _, err := a.lib.svc.Voices.SetConsent(ctx, v.ID, core.ConsentRecording{Audio: rec, Statement: consentStatement()}); err != nil {
		t.Fatal(err)
	}
	a.openVoiceEditor(v.ID)
	tt.Frame()
	if !tt.HasText("Verified") || !tt.HasText("Locked") || !tt.HasText("Locked take") {
		t.Fatalf("badges: %q", tt.Texts())
	}
	tt.Click("Replace reference…")
	tt.Frame()
	if !tt.HasText("Replace the reference of “Me”") {
		t.Fatalf("replace page: %q", tt.Texts())
	}
	clip := filepath.Join(t.TempDir(), "new.wav")
	writeSpeech(clip, 7, []span{{0.3, 6.5}})
	a.cloneLoad(clip, "new.wav", false)
	tt.Frame()
	if err := tt.Click("Replace reference"); err != nil {
		t.Fatal(err)
	}
	got := a.findVoice(v.ID)
	if got == nil || got.Locked || got.Verified() || got.RefText != a.clone.transcript || got.RefSeconds < 6.5 || got.RefOriginal == "" {
		t.Fatalf("after replace %+v", got)
	}
	_ = eng
}

// addNoise adds white noise at dBFS to a WAV.
func addNoise(t *testing.T, path string, dBFS float64) {
	t.Helper()
	b, _, err := wav.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dsptest.Add(b, dsptest.Noise(b.Rate, b.Channels, dBFS, b.Seconds(), 5), 0)
	if err := wav.WriteFile(path, b, wav.PCM16, wav.Options{}); err != nil {
		t.Fatal(err)
	}
}
