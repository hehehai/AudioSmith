package enginetest

// The fake's capture.* methods (see fake_media.go's comment).

import (
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

// Fake capture devices.
const (
	FakeBuiltInMic  = "BuiltInMicrophoneDevice"
	FakeUSBDevice   = "AppleUSBAudioEngine:Fake:Interface:1"
	FakeFlakyDevice = "AppleUSBAudioEngine:Fake:Flaky:2"  // disconnects 0.2 s into a recording
	FakeSilentMic   = "AppleUSBAudioEngine:Fake:Silent:3" // silent until voice processing comes on
	FakeDeadMic     = "AppleUSBAudioEngine:Fake:Dead:4"   // no sound at all: no_input
)

// FakeDevices are the inputs the fake lists, the default first.
func FakeDevices() []proto.CaptureDevice {
	return []proto.CaptureDevice{
		{ID: FakeBuiltInMic, Name: "MacBook Air Microphone", Channels: 1, SampleRate: 48000, Default: true, Transport: "builtin"},
		{ID: FakeUSBDevice, Name: "Fake USB Interface", Channels: 2, SampleRate: 48000, Transport: "usb"},
		{ID: FakeFlakyDevice, Name: "Fake Flaky Headset", Channels: 1, SampleRate: 16000, Transport: "bluetooth"},
		{ID: FakeSilentMic, Name: "Fake Silent Mic", Channels: 1, SampleRate: 48000, Transport: "usb"},
		{ID: FakeDeadMic, Name: "Fake Dead Mic", Channels: 1, SampleRate: 48000, Transport: "usb"},
	}
}

// capture records until capture.stop, job.cancel, maxSeconds or the
// flaky device going away.
func (f *fakeEngine) capture(m proto.Message) {
	var p proto.CaptureParams
	if err := m.DecodeParams(&p); err != nil || p.Output.Path == "" {
		f.fail(m.ID, proto.CodeBadRequest, "Missing output")
		return
	}
	if _, deny := f.opts["deny-mic"]; deny {
		f.failData(m.ID, proto.CodePermissionDenied, map[string]string{"permission": "microphone"}, "Microphone access is denied for AudioSmith")
		return
	}
	dev := FakeDevices()[0]
	if p.Device != "" {
		i := slices.IndexFunc(FakeDevices(), func(d proto.CaptureDevice) bool { return d.ID == p.Device })
		if i < 0 {
			f.failData(m.ID, proto.CodeDeviceUnavailable, map[string]string{"device": p.Device}, "No audio input %s", p.Device)
			return
		}
		dev = FakeDevices()[i]
	}
	for _, c := range p.InputChannels {
		if c < 1 || c > dev.Channels {
			f.fail(m.ID, proto.CodeBadRequest, "%s has no input channel %d", dev.Name, c)
			return
		}
	}
	channels := max(p.Channels, 1)
	rate := p.SampleRate
	if rate == 0 {
		rate = dev.SampleRate
	}
	every := 50 * time.Millisecond
	if ms, err := strconv.Atoi(f.opts["level-ms"]); err == nil && ms > 0 {
		every = time.Duration(ms) * time.Millisecond
	}
	stop := make(chan bool)
	f.mu.Lock()
	f.stop[m.ID] = stop
	f.mu.Unlock()
	f.notify(proto.MethodJobProgress, proto.ProgressParams{Job: m.ID, Stage: "recording"})
	start := time.Now()
	tick := time.NewTicker(every)
	defer tick.Stop()
	reason, aecAuto := "", ""
	for reason == "" {
		select {
		case <-stop:
			reason = proto.CaptureStopped
		case <-tick.C:
		}
		if f.cancelled(m.ID) {
			os.Remove(p.Output.Path)
			f.fail(m.ID, proto.CodeCancelled, "Cancelled")
			return
		}
		elapsed := time.Since(start).Seconds()
		switch {
		case reason != "":
		case p.MaxSeconds > 0 && elapsed >= p.MaxSeconds:
			reason = proto.CaptureMaxSeconds
		case dev.ID == FakeFlakyDevice && elapsed >= 0.2:
			reason = proto.CaptureDeviceLost
		case dev.ID == FakeDeadMic && elapsed >= 0.15:
			os.Remove(p.Output.Path)
			f.failData(m.ID, proto.CodeNoInput, proto.NoInputData{Device: dev.ID, Name: dev.Name, Reason: proto.CaptureNoSound, AEC: true}, "No sound from %s", dev.Name)
			return
		case (dev.ID == FakeSilentMic && !p.AEC && elapsed < 0.15) || dev.ID == FakeDeadMic:
			f.notify(proto.MethodCaptureLevel, proto.LevelParams{Job: m.ID, Seconds: round3(elapsed), RMS: -120, Peak: -120})
		default:
			if dev.ID == FakeSilentMic && !p.AEC {
				aecAuto = proto.CaptureNoSound
			}
			rms, peak := levels(Tone(rate, 1, every.Seconds(), 440, 0.25).Samples)
			f.notify(proto.MethodCaptureLevel, proto.LevelParams{Job: m.ID, Seconds: round3(elapsed), RMS: round3(rms), Peak: round3(peak),
				AECAuto: aecAuto != "", AECReason: aecAuto})
		}
	}
	seconds := time.Since(start).Seconds()
	if p.MaxSeconds > 0 {
		seconds = min(seconds, p.MaxSeconds)
	}
	audio := Tone(rate, channels, seconds, 440, 0.25)
	if !f.write(m.ID, p.Output, audio) {
		return
	}
	_, peak := levels(audio.Samples)
	f.result(m.ID, proto.CaptureResult{AudioOut: audioOut(p.Output.Path, audio), Device: dev.ID, Peak: round3(peak), Reason: reason,
		AEC: p.AEC || aecAuto != "", AECAuto: aecAuto != "", AECReason: aecAuto,
		AECMono: (p.AEC || aecAuto != "") && (channels == 2 || len(p.InputChannels) > 0)}) // voice processing gives one channel
}

// captureStop ends a recording; the job answers first.
func (f *fakeEngine) captureStop(m proto.Message) {
	var p proto.CaptureStopParams
	m.DecodeParams(&p)
	f.mu.Lock()
	stop, ok := f.stop[p.Job]
	done := f.finished[p.Job]
	delete(f.stop, p.Job)
	f.mu.Unlock()
	if !ok {
		f.result(m.ID, proto.CaptureStopResult{Stopped: false})
		return
	}
	close(stop)
	go func() {
		<-done
		f.result(m.ID, proto.CaptureStopResult{Stopped: true})
	}()
}
