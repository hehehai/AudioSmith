package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

// Echo cancellation (thread #153): one setting for every recording,
// switched beside the clone recorder's input picker and in Settings →
// Dictation; the engine turning it on by itself offers to keep it; a
// recording without sound says what to try.
func TestEchoCancellation(t *testing.T) {
	a, eng := newVoiceTestApp(t)
	tt := ui.NewTester(a.view, 1200, 1800)
	a.startClone("")
	tt.Frame()
	if !tt.HasText("Echo cancellation") || a.echoOn() {
		t.Fatalf("recorder: on=%v %q", a.echoOn(), tt.Texts())
	}

	// Off: the recording asks for none; the engine turns it on by itself
	// and the app offers to keep it on.
	eng.mu.Lock()
	eng.levels = []proto.LevelParams{{Seconds: 0.05, RMS: -120, Peak: -120},
		{Seconds: 0.05, RMS: -30, Peak: -18, AECAuto: true, AECReason: proto.CaptureNoSound}}
	eng.mu.Unlock()
	if err := tt.Click("Record"); err != nil {
		t.Fatal(err)
	}
	if p := params[proto.CaptureParams](t, eng.lastOf(t, proto.MethodCaptureStart)); p.AEC {
		t.Errorf("asked for echo cancellation while off: %+v", p)
	}
	tt.Frame()
	if a.clone.mic.aecAuto != proto.CaptureNoSound ||
		!tt.HasText("The microphone brought no sound: echo cancellation is on for this recording.") {
		t.Fatalf("auto notice: %q %q", a.clone.mic.aecAuto, tt.Texts())
	}
	if err := tt.Click("Keep it on"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.echoOn() {
		t.Error("Keep it on did not turn the setting on")
	}
	a.micCancel(&a.clone.mic)
	tt.Frame()

	// On: every recording asks for it, the checkbox shows it, and the
	// setting is saved.
	eng.mu.Lock()
	eng.levels = nil
	eng.mu.Unlock()
	tt.Click("Record")
	if p := params[proto.CaptureParams](t, eng.lastOf(t, proto.MethodCaptureStart)); !p.AEC {
		t.Errorf("echo cancellation not asked for: %+v", p)
	}
	a.micCancel(&a.clone.mic)
	tt.Frame()
	if s := a.lib.settings.Get(); !s.Recording.EchoCancellation {
		t.Errorf("saved: %+v", s.Recording)
	}
	// The checkbox turns it off again.
	if err := tt.Click("Echo cancellation"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.echoOn() {
		t.Error("the checkbox did not turn it off")
	}

	// No sound at all: the error says what to try.
	data, _ := json.Marshal(proto.NoInputData{Device: "x", Name: "Fake Dead Mic", Reason: proto.CaptureNoSound, AEC: true})
	eng.callErr[proto.MethodCaptureStart] = &proto.Error{Code: proto.CodeNoInput, Message: "No sound from Fake Dead Mic", Data: data}
	tt.Click("Record")
	tt.Frame()
	want := "No sound came from Fake Dead Mic, even with echo cancellation."
	if !strings.HasPrefix(a.clone.mic.err, want) || !strings.Contains(a.clone.mic.err, "choose another input") {
		t.Errorf("no input: %q", a.clone.mic.err)
	}
	delete(eng.callErr, proto.MethodCaptureStart)

	// Settings → Dictation shows the same setting beside the microphone.
	a.navigate("/settings/dictation")
	tt.Frame()
	if !tt.HasText("Echo cancellation") || !tt.HasText(echoSettingHint()) {
		t.Errorf("settings: %q", tt.Texts())
	}
}

// Code review 1103 F1–F3/F7: a recording that asked for echo cancellation
// but runs without it says why, once; with it on, the recorder records
// one channel (the channel choice is off); a no_input without voice
// processing (a loopback input) does not claim it was tried.
func TestEchoFallbackAndChannels(t *testing.T) {
	a, eng := newVoiceTestApp(t)
	tt := ui.NewTester(a.view, 1200, 1800)
	a.startClone("")
	a.setEcho(true)
	a.clone.mic.channels = 2
	tt.Frame()
	eng.mu.Lock()
	eng.levels = []proto.LevelParams{{Seconds: 0.05, RMS: -30, Peak: -18, AECFallback: proto.AECFallbackDevice},
		{Seconds: 0.1, RMS: -30, Peak: -18, AECFallback: proto.AECFallbackDevice}}
	eng.mu.Unlock()
	if err := tt.Click("Record"); err != nil {
		t.Fatal(err)
	}
	if p := params[proto.CaptureParams](t, eng.lastOf(t, proto.MethodCaptureStart)); !p.AEC || p.Channels != 1 {
		t.Errorf("with echo cancellation: %+v, want AEC and one channel", p)
	}
	tt.Frame()
	want := "Echo cancellation works with the system's default input only: this recording runs without it."
	if a.clone.mic.aecAuto != proto.AECFallbackDevice || !tt.HasText(want) {
		t.Fatalf("fallback notice: %q %q", a.clone.mic.aecAuto, tt.Texts())
	}
	a.micCancel(&a.clone.mic)
	tt.Frame()
	if echoFallbackText(proto.AECFallbackFailed) != "Echo cancellation does not work with this input: this recording runs without it." {
		t.Error(echoFallbackText(proto.AECFallbackFailed))
	}

	// Off: the stereo choice applies again.
	a.setEcho(false)
	eng.mu.Lock()
	eng.levels = nil
	eng.mu.Unlock()
	tt.Frame()
	tt.Click("Record")
	if p := params[proto.CaptureParams](t, eng.lastOf(t, proto.MethodCaptureStart)); p.AEC || p.Channels != 2 {
		t.Errorf("without echo cancellation: %+v, want stereo", p)
	}
	a.micCancel(&a.clone.mic)
	tt.Frame()

	data, _ := json.Marshal(proto.NoInputData{Device: "BlackHole2ch_UID", Name: "BlackHole 2ch", Reason: proto.CaptureNoSound})
	eng.callErr[proto.MethodCaptureStart] = &proto.Error{Code: proto.CodeNoInput, Message: "No sound from BlackHole 2ch", Data: data}
	tt.Click("Record")
	tt.Frame()
	if err := a.clone.mic.err; !strings.HasPrefix(err, "No sound came from BlackHole 2ch. Try:") {
		t.Errorf("no input without voice processing: %q", err)
	}
	delete(eng.callErr, proto.MethodCaptureStart)
}
