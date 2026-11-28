package gpu

import (
	"context"
	"strings"

	"github.com/hehehai/AudioSmith/engine-win/hw"
	"github.com/hehehai/AudioSmith/engine-win/internal/llamacpp"
	"github.com/hehehai/AudioSmith/engine-win/server"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

// LlamaCppBackend adapts local llama.cpp (managed llama-server.exe, llama-cli.exe,
// or external AUDIOSMITH_LLAMA_URL) with Vulkan on Intel/AMD iGPU, CUDA on
// NVIDIA, or CPU fallback for llm.generate.
type LlamaCppBackend struct {
	mgr *llamacpp.Manager
}

// NewLlamaCppBackend constructs a llama.cpp adapter using automatic runtime discovery.
func NewLlamaCppBackend(hwInfo hw.Info) *LlamaCppBackend {
	return &LlamaCppBackend{mgr: llamacpp.NewManager(hwInfo)}
}

// NewLlamaCppBackendWithURL constructs a llama.cpp adapter pointing at baseURL.
func NewLlamaCppBackendWithURL(hwInfo hw.Info, baseURL string) *LlamaCppBackend {
	return &LlamaCppBackend{mgr: llamacpp.NewManagerWithConfig(hwInfo, baseURL, "", "")}
}

// NewLlamaCppBackendWithManager wraps an explicit llamacpp.Manager (used in tests).
func NewLlamaCppBackendWithManager(mgr *llamacpp.Manager) *LlamaCppBackend {
	return &LlamaCppBackend{mgr: mgr}
}

func (b *LlamaCppBackend) Name() string     { return "llamacpp" }
func (b *LlamaCppBackend) Provider() string { return b.mgr.Provider() }
func (b *LlamaCppBackend) Available() bool  { return b.mgr.Available() }

func (b *LlamaCppBackend) Supports(task, model, modelPath string) bool {
	if task == "llm" {
		return true
	}
	return hasFileWithExt(modelPath, ".gguf") || strings.Contains(strings.ToLower(model), "gguf")
}

func (b *LlamaCppBackend) Load(ctx context.Context, p proto.LoadParams) (proto.LoadResult, error) {
	if !b.Available() {
		return proto.LoadResult{}, unsupportedBackendError(b.Name(), b.Provider(), "llm", "llama-server.exe / llama-cli.exe not found (or set AUDIOSMITH_LLAMA_URL)")
	}
	return b.mgr.Load(ctx, p.Model, p.Path)
}

func (b *LlamaCppBackend) Unload(_ context.Context, _ string) error {
	return b.mgr.Unload()
}

// Close terminates any managed llama-server subprocess immediately.
func (b *LlamaCppBackend) Close() error {
	return b.mgr.Close()
}

func (b *LlamaCppBackend) Generate(ctx context.Context, jobID string, p proto.LLMGenerateParams, n server.Notifier) (proto.LLMGenerateResult, error) {
	if !b.Available() {
		return proto.LLMGenerateResult{}, unsupportedBackendError(b.Name(), b.Provider(), "llm", "llama.cpp runtime is not configured")
	}
	return b.mgr.Generate(ctx, jobID, p, n)
}

func (b *LlamaCppBackend) notLLM(capName string) error {
	return unsupportedBackendError(b.Name(), b.Provider(), capName, "llama.cpp backend serves llm.generate only")
}

func (b *LlamaCppBackend) Synthesize(_ context.Context, _ string, _ proto.SynthesizeParams, _ server.Notifier) (proto.SynthesizeResult, error) {
	return proto.SynthesizeResult{}, b.notLLM("tts")
}
func (b *LlamaCppBackend) EncodeVoice(_ context.Context, _ string, _ proto.EncodeVoiceParams, _ server.Notifier) (proto.EncodeVoiceResult, error) {
	return proto.EncodeVoiceResult{}, b.notLLM("voicePrompt")
}
func (b *LlamaCppBackend) Dialogue(_ context.Context, _ string, _ proto.DialogueParams, _ server.Notifier) (proto.DialogueResult, error) {
	return proto.DialogueResult{}, b.notLLM("dialogue")
}
func (b *LlamaCppBackend) Transcribe(_ context.Context, _ string, _ proto.TranscribeParams, _ server.Notifier) (proto.TranscribeResult, error) {
	return proto.TranscribeResult{}, b.notLLM("stt")
}
func (b *LlamaCppBackend) Align(_ context.Context, _ string, _ proto.AlignParams, _ server.Notifier) (proto.AlignResult, error) {
	return proto.AlignResult{}, b.notLLM("align")
}
func (b *LlamaCppBackend) VAD(_ context.Context, _ string, _ proto.VADParams, _ server.Notifier) (proto.VADResult, error) {
	return proto.VADResult{}, b.notLLM("vad")
}
func (b *LlamaCppBackend) Enhance(_ context.Context, _ string, _ proto.EnhanceParams, _ server.Notifier) (proto.EnhanceResult, error) {
	return proto.EnhanceResult{}, b.notLLM("enhance")
}
func (b *LlamaCppBackend) Separate(_ context.Context, _ string, _ proto.SeparateParams, _ server.Notifier) (proto.SeparateResult, error) {
	return proto.SeparateResult{}, b.notLLM("separate")
}
func (b *LlamaCppBackend) Diarize(_ context.Context, _ string, _ proto.DiarizeParams, _ server.Notifier) (proto.DiarizeResult, error) {
	return proto.DiarizeResult{}, b.notLLM("diarize")
}
