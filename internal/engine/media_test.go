package engine

// The M1 methods end to end over the supervisor and the scripted engine:
// on-demand roles, the queue's executors for the new job kinds, and the
// direct calls (probe, devices, recordings).

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/engine/enginetest"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

func allRoles(c *Config) {
	c.OnDemand = []proto.Role{proto.RoleListen, proto.RoleMedia}
	c.StatsEvery = -1
}

func statusOf(s Supervisor, role proto.Role) WorkerStatus {
	for _, st := range s.Status() {
		if st.Role == role {
			return st
		}
	}
	return WorkerStatus{}
}

// speechWAV writes 0.5 s silence, 1 s tone, 0.6 s silence, 0.8 s tone,
// 0.4 s silence at 16 kHz.
func speechWAV(t *testing.T, dir string) string {
	t.Helper()
	var s []float64
	for _, part := range []struct {
		sec  float64
		tone bool
	}{{0.5, false}, {1, true}, {0.6, false}, {0.8, true}, {0.4, false}} {
		amp := 0.0
		if part.tone {
			amp = 0.3
		}
		s = append(s, enginetest.Tone(16000, 1, part.sec, 300, amp).Samples...)
	}
	path := filepath.Join(dir, "speech.wav")
	if err := enginetest.WriteWAV(path, enginetest.Audio{Rate: 16000, Channels: 1, Samples: s}, 16); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOnDemandRoles(t *testing.T) {
	s, w := newTestSup(t, "", allRoles)
	w.until(t, "gpu ready", func(st WorkerStatus) bool { return st.Role == proto.RoleGPU && ready(st) })
	if st := statusOf(s, proto.RoleMedia); st.State != WorkerStopped || len(s.Status()) != 3 {
		t.Fatalf("media before use: %+v (%d workers)", st, len(s.Status()))
	}
	dir := t.TempDir()
	wav := speechWAV(t, dir)
	ctx := context.Background()
	info, err := Probe(ctx, s, wav)
	if err != nil || info.Format != "wav" || info.Codec != "lpcm" || info.Bits != 16 || info.HasVideo || info.Duration < 3.2 || info.Duration > 3.4 {
		t.Fatalf("probe: %+v %v", info, err)
	}
	if st := statusOf(s, proto.RoleMedia); st.State != WorkerReady || st.Hello == nil || st.Hello.Concurrency != 2 {
		t.Fatalf("media after use: %+v", st)
	}
	// Methods go to their role only.
	if _, err := s.Call(ctx, proto.RoleMedia, "", proto.MethodTTSSynthesize, synth("x", false), nil); !errors.Is(err, &proto.Error{Code: proto.CodeMethodNotFound}) {
		t.Errorf("tts on media: %v", err)
	}
	// Errors are *jobs.Error with the protocol's codes.
	var je *jobs.Error
	if _, err := Probe(ctx, s, filepath.Join(dir, "gone.wav")); !errors.As(err, &je) || je.Code != proto.CodeFileNotFound {
		t.Errorf("missing file: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi"), 0o644)
	if _, err := Probe(ctx, s, filepath.Join(dir, "notes.txt")); !errors.As(err, &je) || je.Code != proto.CodeUnsupportedMedia {
		t.Errorf("text file: %v", err)
	}
	if info, err := Probe(ctx, s, writeFile(t, dir, "clip.mov")); err != nil || !info.HasVideo || info.Video == nil || info.Duration != enginetest.FakeMediaSeconds {
		t.Errorf("mov: %+v %v", info, err)
	}

	// Stop stops on-demand workers too; Start leaves them stopped.
	s.Stop()
	if st := statusOf(s, proto.RoleMedia); st.State != WorkerStopped {
		t.Errorf("after stop: %+v", st)
	}
}

func writeFile(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("fake media"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// The new job kinds run through the scheduler on their roles' workers.
func TestM1Executors(t *testing.T) {
	sup, w := newTestSup(t, "", allRoles)
	w.until(t, "ready", ready)
	sched := jobs.New(jobs.Options{Store: jobs.NewMemStore(), Logf: t.Logf})
	defer sched.Close()
	RegisterExecutors(sched, sup)
	ctx := context.Background()
	sched.Start(ctx)
	run := func(method string, params any, result any) *jobs.Error {
		t.Helper()
		raw, _ := json.Marshal(params)
		id, err := sched.Submit(ctx, jobs.Spec{Kind: method, Lane: jobs.Lane(proto.RoleOf(method)), Params: raw})
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(10 * time.Second)
		for {
			j, _ := sched.Get(ctx, id)
			switch j.State {
			case jobs.StateSucceeded:
				if err := json.Unmarshal(j.Result, result); err != nil {
					t.Fatal(err)
				}
				return nil
			case jobs.StateFailed:
				return j.Err
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: %+v", method, j)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	dir := t.TempDir()
	wav := speechWAV(t, dir)

	// media.decode: a window, resampled and duplicated to stereo.
	var dec proto.DecodeResult
	out := filepath.Join(dir, "window.wav")
	if e := run(proto.MethodMediaDecode, proto.DecodeParams{Input: wav, Output: proto.Output{Path: out, Bits: 24}, SampleRate: 24000, Channels: 2, Start: 0.5, Duration: 1}, &dec); e != nil {
		t.Fatal(e)
	}
	got, err := enginetest.ReadWAV(out)
	if err != nil || got.Rate != 24000 || got.Channels != 2 || got.Bits != 24 || got.Frames() != 24000 || dec.Frames != 24000 || dec.Source.SampleRate != 16000 {
		t.Fatalf("decode: %+v %+v %v", dec, got, err)
	}

	// vad.detect finds the two tones.
	var vad proto.VADResult
	if e := run(proto.MethodVADDetect, proto.VADParams{Model: "mlx-community/silero-vad", Audio: wav}, &vad); e != nil {
		t.Fatal(e)
	}
	if len(vad.Regions) != 2 || vad.Regions[0].Start > 0.5 || vad.Regions[0].End < 1.5 || vad.Regions[1].Start > 2.1 || vad.Regions[1].End < 2.9 {
		t.Fatalf("vad: %+v", vad)
	}

	// enhance.run writes 48 kHz mono.
	var enh proto.EnhanceResult
	clean := filepath.Join(dir, "clean.wav")
	if e := run(proto.MethodEnhanceRun, proto.EnhanceParams{Model: "mlx-community/DeepFilterNet-mlx", Audio: wav, Output: proto.Output{Path: clean}}, &enh); e != nil {
		t.Fatal(e)
	}
	if enh.SampleRate != 48000 || enh.Channels != 1 || enh.AudioSeconds < 3.2 {
		t.Fatalf("enhance: %+v", enh)
	}

	// stt.transcribe reads the sidecar transcript; stt.align times its words.
	os.WriteFile(wav+".txt", []byte("你好，世界。再见！"), 0o644)
	var tr proto.TranscribeResult
	if e := run(proto.MethodSTTTranscribe, proto.TranscribeParams{Model: "mlx-community/whisper-large-v3-turbo-4bit", Audio: wav, Words: true}, &tr); e != nil {
		t.Fatal(e)
	}
	if tr.Language != "zh" || len(tr.Segments) != 2 || tr.Segments[1].End < 3.2 || tr.Segments[0].Words != nil {
		t.Fatalf("transcribe: %+v", tr)
	}
	var al proto.AlignResult
	if e := run(proto.MethodSTTAlign, proto.AlignParams{Model: "mlx-community/Qwen3-ForcedAligner-0.6B-4bit", Audio: wav, Text: tr.Text}, &al); e != nil {
		t.Fatal(e)
	}
	if len(al.Words) != 6 || al.Words[0].Text != "你" || al.Words[5].End < 3.2 {
		t.Fatalf("align: %+v", al)
	}
	if e := run(proto.MethodSTTTranscribe, proto.TranscribeParams{Model: "mlx-community/Qwen3-ASR-0.6B-4bit", Audio: wav, Task: "translate"}, &tr); e == nil || e.Code != proto.CodeUnsupported {
		t.Errorf("translate: %v", e)
	}

	// tts.encodeVoice, then a take from the prompt; a stale prompt falls back to refAudio.
	var enc proto.EncodeVoiceResult
	prompt := filepath.Join(dir, "base.voiceprompt")
	base := "mlx-community/Qwen3-TTS-12Hz-1.7B-Base-8bit"
	if e := run(proto.MethodTTSEncodeVoice, proto.EncodeVoiceParams{Model: base, RefAudio: wav, RefText: "hi", PromptPath: prompt}, &enc); e != nil {
		t.Fatal(e)
	}
	if enc.VoicePrompt != prompt || enc.Format != enginetest.FakePromptFormat {
		t.Fatalf("encode: %+v", enc)
	}
	var syn proto.SynthesizeResult
	take := filepath.Join(dir, "take.wav")
	if e := run(proto.MethodTTSSynthesize, proto.SynthesizeParams{Model: base, Text: "hi", VoicePrompt: prompt, Output: proto.Output{Path: take}}, &syn); e != nil {
		t.Fatal(e)
	}
	if a, err := enginetest.ReadWAV(take); err != nil || int64(a.Frames()) != syn.Frames {
		t.Fatalf("take: %+v %v", a, err)
	}
	if e := run(proto.MethodTTSSynthesize, proto.SynthesizeParams{Model: "other/Model", Text: "hi", VoicePrompt: prompt, Output: proto.Output{Path: take}}, &syn); e == nil || e.Code != proto.CodeBadRequest {
		t.Errorf("prompt of another model, no reference: %v", e)
	}
	if e := run(proto.MethodTTSSynthesize, proto.SynthesizeParams{Model: "other/Model", Text: "hi", VoicePrompt: prompt, RefAudio: wav, Output: proto.Output{Path: take}}, &syn); e != nil {
		t.Errorf("prompt of another model, with reference: %v", e)
	}
	if e := run(proto.MethodTTSEncodeVoice, proto.EncodeVoiceParams{Model: "mlx-community/Kokoro-82M-bf16", RefAudio: wav, PromptPath: prompt}, &enc); e == nil || e.Code != proto.CodeUnsupported || string(e.Data) != `{"capability":"voicePrompt"}` {
		t.Errorf("kokoro prompt: %+v", e)
	}

	// Typed errors.
	long := make([]byte, enginetest.FakeKokoroLimit+1)
	for i := range long {
		long[i] = 'a'
	}
	if e := run(proto.MethodTTSSynthesize, proto.SynthesizeParams{Model: "mlx-community/Kokoro-82M-bf16", Text: string(long), Output: proto.Output{Path: take}}, &syn); e == nil || e.Code != proto.CodeInputTooLong {
		t.Errorf("long text: %+v", e)
	}
	if e := run(proto.MethodSTTTranscribe, proto.TranscribeParams{Model: "m/whisper", Audio: filepath.Join(dir, "gone.m4a")}, &tr); e == nil || e.Code != proto.CodeFileNotFound {
		t.Errorf("missing audio: %+v", e)
	}
}

func TestCapture(t *testing.T) {
	s, w := newTestSup(t, "level-ms=10", allRoles)
	w.until(t, "ready", ready)
	ctx := context.Background()
	devs, err := Devices(ctx, s)
	if err != nil || len(devs) != 5 || !devs[0].Default {
		t.Fatalf("devices: %+v %v", devs, err)
	}
	dir := t.TempDir()

	// Record from the USB interface's input 2, stop: the file is there.
	var mu sync.Mutex
	var levels []proto.LevelParams
	out := filepath.Join(dir, "rec.wav")
	rec, err := StartCapture(ctx, s, "", proto.CaptureParams{Device: enginetest.FakeUSBDevice, InputChannels: []int{2}, SampleRate: 24000, Output: proto.Output{Path: out}},
		func(l proto.LevelParams) { mu.Lock(); levels = append(levels, l); mu.Unlock() })
	if err != nil {
		t.Fatal(err)
	}
	// At least 150 ms and three meter readings (a loaded machine may be late).
	time.Sleep(150 * time.Millisecond)
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		mu.Lock()
		n := len(levels)
		mu.Unlock()
		if n >= 3 || time.Now().After(deadline) {
			break
		}
	}
	res, err := rec.Stop(ctx)
	if err != nil || res.Reason != proto.CaptureStopped || res.Device != enginetest.FakeUSBDevice || res.SampleRate != 24000 || res.AudioSeconds < 0.1 {
		t.Fatalf("stop: %+v %v", res, err)
	}
	mu.Lock()
	n := len(levels)
	mu.Unlock()
	if n < 3 || levels[0].Peak > -11 || levels[0].Peak < -13 {
		t.Errorf("levels: %d %+v", n, levels)
	}
	if a, err := enginetest.ReadWAV(out); err != nil || int64(a.Frames()) != res.Frames {
		t.Errorf("file: %+v %v", a, err)
	}
	if again, err := rec.Stop(ctx); err != nil || again.Output != out {
		t.Errorf("second stop: %+v %v", again, err)
	}

	// Cancel discards it.
	out2 := filepath.Join(dir, "discard.wav")
	rec, err = StartCapture(ctx, s, "", proto.CaptureParams{Output: proto.Output{Path: out2}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec.Cancel()
	if _, err := rec.Result(); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
	if _, err := os.Stat(out2); !os.IsNotExist(err) {
		t.Errorf("cancelled file: %v", err)
	}

	// It ends by itself: maxSeconds, a device going away.
	rec, err = StartCapture(ctx, s, "", proto.CaptureParams{Output: proto.Output{Path: out2}, MaxSeconds: 0.05}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res, err := rec.Result(); err != nil || res.Reason != proto.CaptureMaxSeconds || res.AudioSeconds > 0.051 {
		t.Errorf("maxSeconds: %+v %v", res, err)
	}
	rec, err = StartCapture(ctx, s, "", proto.CaptureParams{Device: enginetest.FakeFlakyDevice, Output: proto.Output{Path: out2}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res, err := rec.Result(); err != nil || res.Reason != proto.CaptureDeviceLost || res.SampleRate != 16000 {
		t.Errorf("device lost: %+v %v", res, err)
	}

	// Refusals.
	var je *jobs.Error
	if _, err := StartCapture(ctx, s, "", proto.CaptureParams{Device: "nope", Output: proto.Output{Path: out2}}, nil); !errors.As(err, &je) || je.Code != proto.CodeDeviceUnavailable {
		t.Errorf("unknown device: %v", err)
	}
	denied, w2 := newTestSup(t, "deny-mic", allRoles)
	w2.until(t, "ready", ready)
	if _, err := StartCapture(ctx, denied, "", proto.CaptureParams{Output: proto.Output{Path: out2}}, nil); !errors.As(err, &je) || je.Code != proto.CodePermissionDenied {
		t.Errorf("denied: %v", err)
	}
}

// A silent input turns voice processing on by itself (the levels and the
// result say so); one that brings nothing fails with no_input, its file
// gone (thread #153).
func TestCaptureNoSound(t *testing.T) {
	s, w := newTestSup(t, "level-ms=10", allRoles)
	w.until(t, "ready", ready)
	ctx := context.Background()
	dir := t.TempDir()
	var mu sync.Mutex
	var auto []proto.LevelParams
	rec, err := StartCapture(ctx, s, "", proto.CaptureParams{Device: enginetest.FakeSilentMic, Output: proto.Output{Path: filepath.Join(dir, "a.wav")}},
		func(l proto.LevelParams) {
			if l.AECAuto {
				mu.Lock()
				auto = append(auto, l)
				mu.Unlock()
			}
		})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	res, err := rec.Stop(ctx)
	mu.Lock()
	n := len(auto)
	mu.Unlock()
	if err != nil || !res.AEC || !res.AECAuto || res.AECReason != proto.CaptureNoSound || n == 0 || auto[0].AECReason != proto.CaptureNoSound {
		t.Errorf("silent mic: %+v %v (%d auto levels)", res, err, n)
	}
	// Asked for, it is on from the start: nothing automatic.
	rec, err = StartCapture(ctx, s, "", proto.CaptureParams{Device: enginetest.FakeSilentMic, AEC: true, Output: proto.Output{Path: filepath.Join(dir, "b.wav")}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if res, err := rec.Stop(ctx); err != nil || !res.AEC || res.AECAuto {
		t.Errorf("aec on: %+v %v", res, err)
	}
	out := filepath.Join(dir, "c.wav")
	rec, err = StartCapture(ctx, s, "", proto.CaptureParams{Device: enginetest.FakeDeadMic, Output: proto.Output{Path: out}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var je *jobs.Error
	var d proto.NoInputData
	if _, err := rec.Result(); !errors.As(err, &je) || je.Code != proto.CodeNoInput || json.Unmarshal(je.Data, &d) != nil || d.Name != "Fake Dead Mic" || d.Reason != proto.CaptureNoSound {
		t.Errorf("dead mic: %v %+v", err, d)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("dead mic's file: %v", err)
	}
}

// Dictation's speech-to-text runs on the listen worker, beside a
// recording, through the "listen:" kinds.
func TestListenTranscribes(t *testing.T) {
	sup, w := newTestSup(t, "level-ms=10", allRoles)
	w.until(t, "ready", ready)
	ctx := context.Background()
	dir := t.TempDir()
	wav := speechWAV(t, dir)
	if proto.KindFor(proto.RoleListen, proto.MethodSTTTranscribe) != "listen:stt.transcribe" ||
		proto.KindFor(proto.RoleListen, proto.MethodSTTAlign) != proto.MethodSTTAlign || proto.KindFor(proto.RoleGPU, proto.MethodSTTTranscribe) != proto.MethodSTTTranscribe {
		t.Fatal("KindFor")
	}
	// A recording runs meanwhile: the listen worker takes both.
	rec, err := StartCapture(ctx, sup, "", proto.CaptureParams{Output: proto.Output{Path: filepath.Join(dir, "r.wav")}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rec.Cancel()
	model := "mlx-community/whisper-large-v3-turbo-4bit"
	raw, _ := json.Marshal(proto.LoadParams{Model: model, Task: "stt"})
	if _, err := KindExecutor(sup, "listen:models.load").Run(ctx, jobs.Job{ID: "L1", Spec: jobs.Spec{Params: raw}}, func(jobs.Progress) {}, nil); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(proto.TranscribeParams{Model: model, Audio: wav})
	res, err := KindExecutor(sup, "listen:stt.transcribe").Run(ctx, jobs.Job{ID: "L2", Spec: jobs.Spec{Params: raw}}, func(jobs.Progress) {}, nil)
	var tr proto.TranscribeResult
	if err != nil || json.Unmarshal(res, &tr) != nil || tr.Text != enginetest.FakeTranscript {
		t.Fatalf("transcribe on listen: %s %v", res, err)
	}
	raw, err = sup.Call(ctx, proto.RoleListen, "", proto.MethodModelsLoaded, nil, nil)
	if err != nil || !strings.Contains(string(raw), model) {
		t.Errorf("listen models: %s %v", raw, err)
	}
	// The gpu worker did not load it.
	raw, _ = sup.Call(ctx, proto.RoleGPU, "", proto.MethodModelsLoaded, nil, nil)
	if strings.Contains(string(raw), model) {
		t.Errorf("gpu loaded it: %s", raw)
	}
}

// media.encode (M3): an executor kind on the media worker.
func TestEncode(t *testing.T) {
	sup, w := newTestSup(t, "", allRoles)
	w.until(t, "ready", ready)
	ctx := context.Background()
	dir := t.TempDir()
	wav := speechWAV(t, dir)
	out := filepath.Join(dir, "book.m4b")
	p := proto.EncodeParams{Input: wav, Output: proto.Output{Path: out, Format: "m4b"}, Codec: "aac",
		Chapters: []proto.EncodeChapter{{Title: "One", Start: 0, End: 1.5}, {Title: "Two", Start: 1.5, End: 3.3}},
		Metadata: proto.EncodeMetadata{Title: "Book", Author: "A"}}
	raw, _ := json.Marshal(p)
	res, err := KindExecutor(sup, proto.MethodMediaEncode).Run(ctx, jobs.Job{ID: "E1", Spec: jobs.Spec{Params: raw}}, func(jobs.Progress) {}, nil)
	var r proto.EncodeResult
	if err != nil || json.Unmarshal(res, &r) != nil || r.Output != out || r.Format != "m4b" || r.Bitrate != 64000 || r.Duration < 3.2 {
		t.Fatalf("encode: %s %v", res, err)
	}
	p.Output = proto.Output{Path: filepath.Join(dir, "book.mp3"), Format: "mp3"}
	raw, _ = json.Marshal(p)
	_, err = KindExecutor(sup, proto.MethodMediaEncode).Run(ctx, jobs.Job{ID: "E2", Spec: jobs.Spec{Params: raw}}, func(jobs.Progress) {}, nil)
	var je *jobs.Error
	if !errors.As(err, &je) || je.Code != proto.CodeUnsupported {
		t.Errorf("mp3: %v", err)
	}
}
