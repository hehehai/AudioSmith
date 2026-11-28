package main

import (
	"bytes"
	"context"
	"io"
	"log"
	"sync/atomic"
	"testing"

	"github.com/hehehai/AudioSmith/engine-win/hw"
	"github.com/hehehai/AudioSmith/engine-win/server"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

type trackingBackend struct {
	unloads atomic.Int32
	closes  atomic.Int32
}

func (b *trackingBackend) Name() string     { return "llamacpp" }
func (b *trackingBackend) Provider() string { return "cpu" }
func (b *trackingBackend) Available() bool  { return true }
func (b *trackingBackend) Supports(task, _, _ string) bool {
	return task == "llm"
}
func (b *trackingBackend) Load(_ context.Context, p proto.LoadParams) (proto.LoadResult, error) {
	return proto.LoadResult{Model: p.Model, Task: "llm", Bytes: 1024}, nil
}
func (b *trackingBackend) Unload(_ context.Context, _ string) error {
	b.unloads.Add(1)
	return nil
}
func (b *trackingBackend) Close() error {
	b.closes.Add(1)
	return nil
}
func (b *trackingBackend) Synthesize(_ context.Context, _ string, _ proto.SynthesizeParams, _ server.Notifier) (proto.SynthesizeResult, error) {
	return proto.SynthesizeResult{}, nil
}
func (b *trackingBackend) EncodeVoice(_ context.Context, _ string, _ proto.EncodeVoiceParams, _ server.Notifier) (proto.EncodeVoiceResult, error) {
	return proto.EncodeVoiceResult{}, nil
}
func (b *trackingBackend) Dialogue(_ context.Context, _ string, _ proto.DialogueParams, _ server.Notifier) (proto.DialogueResult, error) {
	return proto.DialogueResult{}, nil
}
func (b *trackingBackend) Transcribe(_ context.Context, _ string, _ proto.TranscribeParams, _ server.Notifier) (proto.TranscribeResult, error) {
	return proto.TranscribeResult{}, nil
}
func (b *trackingBackend) Align(_ context.Context, _ string, _ proto.AlignParams, _ server.Notifier) (proto.AlignResult, error) {
	return proto.AlignResult{}, nil
}
func (b *trackingBackend) VAD(_ context.Context, _ string, _ proto.VADParams, _ server.Notifier) (proto.VADResult, error) {
	return proto.VADResult{}, nil
}
func (b *trackingBackend) Enhance(_ context.Context, _ string, _ proto.EnhanceParams, _ server.Notifier) (proto.EnhanceResult, error) {
	return proto.EnhanceResult{}, nil
}
func (b *trackingBackend) Separate(_ context.Context, _ string, _ proto.SeparateParams, _ server.Notifier) (proto.SeparateResult, error) {
	return proto.SeparateResult{}, nil
}
func (b *trackingBackend) Diarize(_ context.Context, _ string, _ proto.DiarizeParams, _ server.Notifier) (proto.DiarizeResult, error) {
	return proto.DiarizeResult{}, nil
}
func (b *trackingBackend) Generate(_ context.Context, _ string, _ proto.LLMGenerateParams, _ server.Notifier) (proto.LLMGenerateResult, error) {
	return proto.LLMGenerateResult{Text: "ok"}, nil
}

func encodeReq(t *testing.T, id, method string, params any) []byte {
	t.Helper()
	msg, err := proto.NewRequest(id, method, params)
	if err != nil {
		t.Fatalf("NewRequest(%s): %v", method, err)
	}
	b, err := proto.Encode(msg)
	if err != nil {
		t.Fatalf("Encode(%s): %v", method, err)
	}
	return b
}

func TestRoleGPUCleanupOnShutdownAndEOF(t *testing.T) {
	for _, tc := range []struct {
		name         string
		sendShutdown bool
	}{
		{name: "engine.shutdown", sendShutdown: true},
		{name: "eof", sendShutdown: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bk := &trackingBackend{}
			var in bytes.Buffer
			in.Write(encodeReq(t, "1", proto.MethodHello, proto.HelloParams{Protocol: proto.Version, Role: proto.RoleGPU}))
			in.Write(encodeReq(t, "2", proto.MethodModelsLoad, proto.LoadParams{Model: "qwen3-4b-gguf", Task: "llm"}))
			if tc.sendShutdown {
				in.Write(encodeReq(t, "3", proto.MethodShutdown, nil))
			}

			var out bytes.Buffer
			logger := log.New(io.Discard, "", 0)
			if err := runRole(proto.RoleGPU, hw.Probe(), "", &in, &out, logger, bk); err != nil {
				t.Fatalf("runRole(RoleGPU): %v", err)
			}
			if got := bk.unloads.Load(); got != 1 {
				t.Errorf("backend Unload calls = %d, want 1", got)
			}
			if got := bk.closes.Load(); got != 1 {
				t.Errorf("backend Close calls = %d, want 1", got)
			}
		})
	}
}
