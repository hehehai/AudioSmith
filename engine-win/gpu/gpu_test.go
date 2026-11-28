package gpu

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/hehehai/AudioSmith/engine-win/hw"
	"github.com/hehehai/AudioSmith/engine-win/server"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

type fakeBackend struct{}

func (f *fakeBackend) Name() string                             { return "fake" }
func (f *fakeBackend) Provider() string                         { return "cpu" }
func (f *fakeBackend) Available() bool                          { return true }
func (f *fakeBackend) Supports(_, _, _ string) bool             { return true }
func (f *fakeBackend) Unload(_ context.Context, _ string) error { return nil }

func (f *fakeBackend) Load(_ context.Context, p proto.LoadParams) (proto.LoadResult, error) {
	return proto.LoadResult{
		Model:      p.Model,
		Task:       p.Task,
		SampleRate: 24000,
		Seconds:    0.05,
		Bytes:      256 << 20,
		Languages:  []string{"en", "zh", "ja"},
	}, nil
}

func (f *fakeBackend) Synthesize(_ context.Context, jobID string, p proto.SynthesizeParams, n server.Notifier) (proto.SynthesizeResult, error) {
	frac := 0.5
	_ = n.Notify(proto.MethodJobProgress, proto.ProgressParams{Job: jobID, Stage: "generating", Fraction: &frac})
	if p.Stream {
		_ = n.Notify(proto.MethodTTSChunk, proto.ChunkParams{
			Job: jobID, Seq: 0, SampleRate: 24000, Channels: 1, Encoding: "pcm16le", Data: make([]byte, 480), Final: true,
		})
	}
	w, err := wav.Create(p.Output.Path, wav.Format{Rate: 24000, Channels: 1, Encoding: wav.PCM16}, wav.Options{})
	if err != nil {
		return proto.SynthesizeResult{}, err
	}
	_ = w.Write(make([]float32, 2400))
	if err := w.Close(); err != nil {
		return proto.SynthesizeResult{}, err
	}
	return proto.SynthesizeResult{
		Output: p.Output.Path, Model: p.Model, SampleRate: 24000, Channels: 1,
		Frames: 2400, AudioSeconds: 0.1, Seconds: 0.02, RTF: 5.0,
	}, nil
}

func (f *fakeBackend) EncodeVoice(_ context.Context, _ string, p proto.EncodeVoiceParams, _ server.Notifier) (proto.EncodeVoiceResult, error) {
	return proto.EncodeVoiceResult{VoicePrompt: p.PromptPath, Model: p.Model, Format: "fake/1", Bytes: 64}, nil
}
func (f *fakeBackend) Dialogue(_ context.Context, _ string, p proto.DialogueParams, _ server.Notifier) (proto.DialogueResult, error) {
	return proto.DialogueResult{Model: p.Model, SampleRate: 24000, Channels: 1}, nil
}
func (f *fakeBackend) Transcribe(_ context.Context, _ string, p proto.TranscribeParams, _ server.Notifier) (proto.TranscribeResult, error) {
	return proto.TranscribeResult{
		Model: p.Model, Text: "hello world", Language: "en",
		Segments: []proto.Segment{{Start: 0, End: 1.0, Text: "hello world"}},
	}, nil
}
func (f *fakeBackend) Align(_ context.Context, _ string, p proto.AlignParams, _ server.Notifier) (proto.AlignResult, error) {
	return proto.AlignResult{Model: p.Model, Words: []proto.Word{{Start: 0, End: 0.5, Text: "hello"}}}, nil
}
func (f *fakeBackend) VAD(_ context.Context, _ string, p proto.VADParams, _ server.Notifier) (proto.VADResult, error) {
	return proto.VADResult{Model: p.Model, Regions: []proto.Region{{Start: 0.1, End: 0.9, Prob: 0.95}}}, nil
}
func (f *fakeBackend) Enhance(_ context.Context, _ string, p proto.EnhanceParams, _ server.Notifier) (proto.EnhanceResult, error) {
	return proto.EnhanceResult{AudioOut: proto.AudioOut{Output: p.Output.Path, SampleRate: 48000, Channels: 1}, Model: p.Model}, nil
}
func (f *fakeBackend) Separate(_ context.Context, _ string, p proto.SeparateParams, _ server.Notifier) (proto.SeparateResult, error) {
	return proto.SeparateResult{Vocals: p.Vocals.Path, Background: p.Background.Path, Model: p.Model, SampleRate: 44100, Channels: 2}, nil
}
func (f *fakeBackend) Diarize(_ context.Context, _ string, p proto.DiarizeParams, _ server.Notifier) (proto.DiarizeResult, error) {
	return proto.DiarizeResult{Model: p.Model, Speakers: 1, Turns: []proto.Turn{{Start: 0, End: 1, Speaker: "S01"}}}, nil
}
func (f *fakeBackend) Generate(_ context.Context, jobID string, p proto.LLMGenerateParams, n server.Notifier) (proto.LLMGenerateResult, error) {
	if p.Stream {
		_ = n.Notify(proto.MethodLLMToken, proto.LLMTokenParams{Job: jobID, Text: "hi"})
	}
	return proto.LLMGenerateResult{Text: "hi", FinishReason: "stop", Model: p.Model, CompletionTokens: 1}, nil
}

func TestGPURole_ProtocolV2GoldenFlow(t *testing.T) {
	dir := t.TempDir()
	outWAV := filepath.Join(dir, "tts.wav")

	g := New(hw.Probe(), &fakeBackend{})
	srv := server.New(server.Config{
		Role:     proto.RoleGPU,
		Version:  "test",
		Hardware: hw.Probe(),
		Hooks:    g.Hooks(),
	})
	g.Register(srv)

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := srv.Serve(inR, outW)
		_ = outW.Close()
		done <- err
	}()

	sc := bufio.NewScanner(outR)
	sc.Buffer(make([]byte, 64*1024), proto.MaxLine)
	notifs := map[string]int{}
	call := func(id, method string, params any) proto.Message {
		t.Helper()
		b, err := proto.Encode(mustReq(t, id, method, params))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := inW.Write(b); err != nil {
			t.Fatal(err)
		}
		for sc.Scan() {
			m, err := proto.Decode(sc.Bytes())
			if err != nil {
				t.Fatalf("decode %q: %v", sc.Text(), err)
			}
			if m.Kind() == proto.KindNotification {
				notifs[m.Method]++
				continue
			}
			if m.Kind() == proto.KindResponse && m.ID == id {
				if m.Error != nil {
					t.Fatalf("error on %s (%s): %v", id, method, m.Error)
				}
				return m
			}
		}
		t.Fatalf("EOF waiting for response to %s (%s)", id, method)
		return proto.Message{}
	}

	budget := int64(2 << 30)
	call("c1", proto.MethodHello, proto.HelloParams{Protocol: proto.Version, Role: proto.RoleGPU})
	call("c2", server.MethodPing, nil)
	call("c3", server.MethodInfo, nil)
	call("c4", proto.MethodEngineConfig, proto.EngineConfigParams{MemoryBudget: &budget})
	call("j1", proto.MethodModelsLoad, proto.LoadParams{Model: "kokoro-82m", Task: "tts"})
	c5 := call("c5", proto.MethodModelsLoaded, nil)
	j2 := call("j2", proto.MethodTTSSynthesize, proto.SynthesizeParams{
		Model:  "kokoro-82m",
		Text:   "Hello Windows iGPU",
		Output: proto.Output{Path: outWAV, Format: "wav", Bits: 16},
		Stream: true,
	})
	j3 := call("j3", proto.MethodSTTTranscribe, proto.TranscribeParams{Model: "whisper-turbo", Audio: outWAV})
	call("j4", proto.MethodLLMGenerate, proto.LLMGenerateParams{
		Model:    "qwen3-4b",
		Messages: []proto.LLMMessage{{Role: "user", Content: "say hi"}},
		Stream:   true,
	})
	call("c6", proto.MethodModelsUnload, proto.UnloadParams{Model: "kokoro-82m"})
	call("c7", proto.MethodShutdown, nil)
	_ = inW.Close()

	if err := <-done; err != nil {
		t.Fatalf("Serve failed: %v", err)
	}

	if notifs[proto.MethodJobProgress] == 0 || notifs[proto.MethodTTSChunk] == 0 || notifs[proto.MethodLLMToken] == 0 {
		t.Fatalf("missing expected notifications: %+v", notifs)
	}
	var loaded proto.LoadedResult
	_ = c5.DecodeResult(&loaded)
	if len(loaded.Models) != 1 || loaded.Models[0].Model != "kokoro-82m" {
		t.Fatalf("unexpected models.loaded: %+v", loaded)
	}
	var synth proto.SynthesizeResult
	_ = j2.DecodeResult(&synth)
	if synth.SampleRate != 24000 || synth.Frames != 2400 {
		t.Fatalf("unexpected synth result: %+v", synth)
	}
	var stt proto.TranscribeResult
	_ = j3.DecodeResult(&stt)
	if stt.Text != "hello world" {
		t.Fatalf("unexpected stt result: %+v", stt)
	}
}

func TestLlamaCppBackend_StreamingAndJSONSchema(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"{\"ok\":"}}]}`)
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"true}"},"finish_reason":"stop"}]}`)
		fmt.Fprintln(w, `data: [DONE]`)
	}))
	defer ts.Close()

	b := NewLlamaCppBackendWithURL(hw.Probe(), ts.URL)
	var tokens []string
	n := notifyFunc(func(method string, params any) error {
		if method == proto.MethodLLMToken {
			tokens = append(tokens, params.(proto.LLMTokenParams).Text)
		}
		return nil
	})
	res, err := b.Generate(context.Background(), "j-llm", proto.LLMGenerateParams{
		Model:    "qwen3-4b.gguf",
		Messages: []proto.LLMMessage{{Role: "user", Content: "return json"}},
		Stream:   true,
		JSON:     true,
		Schema:   json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}}}`),
	}, n)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if res.Text != `{"ok":true}` || !res.Constrained || len(tokens) != 2 {
		t.Fatalf("unexpected llama.cpp result: %+v tokens=%v", res, tokens)
	}
}

type notifyFunc func(method string, params any) error

func (f notifyFunc) Notify(method string, params any) error { return f(method, params) }

func mustReq(t *testing.T, id, method string, params any) proto.Message {
	t.Helper()
	m, err := proto.NewRequest(id, method, params)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
