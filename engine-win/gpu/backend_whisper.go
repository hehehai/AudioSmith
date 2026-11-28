package gpu

import (
	"context"
	"os"
	"strings"

	"github.com/hehehai/AudioSmith/engine-win/hw"
	"github.com/hehehai/AudioSmith/engine-win/internal/llamacpp"
	"github.com/hehehai/AudioSmith/engine-win/server"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

// WhisperBackend adapts local whisper.cpp (whisper-cli.exe or whisper-server)
// for stt.transcribe on everyday Windows PCs.
type WhisperBackend struct {
	runner *llamacpp.WhisperRunner
}

// NewWhisperBackend discovers whisper-cli / AUDIOSMITH_WHISPER_URL automatically.
func NewWhisperBackend(hwInfo hw.Info) *WhisperBackend {
	return &WhisperBackend{runner: llamacpp.NewWhisperRunner(hwInfo)}
}

// NewWhisperBackendWithRunner wraps an explicit WhisperRunner (used in tests).
func NewWhisperBackendWithRunner(r *llamacpp.WhisperRunner) *WhisperBackend {
	return &WhisperBackend{runner: r}
}

func (b *WhisperBackend) Name() string     { return "whispercpp" }
func (b *WhisperBackend) Provider() string { return b.runner.Provider() }
func (b *WhisperBackend) Available() bool  { return b.runner.Available() }

func (b *WhisperBackend) Supports(task, model, modelPath string) bool {
	if task != "stt" {
		return false
	}
	if hasFileWithExt(modelPath, ".bin") || hasFileWithExt(modelPath, ".gguf") {
		return true
	}
	low := strings.ToLower(model)
	return strings.Contains(low, "whisper") || strings.Contains(low, "ggml")
}

func (b *WhisperBackend) Load(_ context.Context, p proto.LoadParams) (proto.LoadResult, error) {
	if !b.Available() {
		return proto.LoadResult{}, unsupportedBackendError(b.Name(), b.Provider(), "stt", "whisper-cli.exe not found (or set AUDIOSMITH_WHISPER_URL)")
	}
	var sz int64
	if f := llamacpp.ResolveModelFile(p.Path, ".bin", ".gguf"); f != "" {
		if fi, err := os.Stat(f); err == nil {
			sz = fi.Size()
		}
	}
	return proto.LoadResult{
		Model:        p.Model,
		Task:         "stt",
		SampleRate:   16000,
		Bytes:        sz,
		Capabilities: []string{"translate"},
	}, nil
}

func (b *WhisperBackend) Unload(_ context.Context, _ string) error { return nil }

func (b *WhisperBackend) Transcribe(ctx context.Context, _ string, p proto.TranscribeParams, _ server.Notifier) (proto.TranscribeResult, error) {
	if !b.Available() {
		return proto.TranscribeResult{}, unsupportedBackendError(b.Name(), b.Provider(), "stt", "whisper.cpp runtime is not installed")
	}
	return b.runner.Transcribe(ctx, p)
}

func (b *WhisperBackend) notSTT(capName string) error {
	return unsupportedBackendError(b.Name(), b.Provider(), capName, "whisper.cpp backend serves stt.transcribe only")
}

func (b *WhisperBackend) Synthesize(_ context.Context, _ string, _ proto.SynthesizeParams, _ server.Notifier) (proto.SynthesizeResult, error) {
	return proto.SynthesizeResult{}, b.notSTT("tts")
}
func (b *WhisperBackend) EncodeVoice(_ context.Context, _ string, _ proto.EncodeVoiceParams, _ server.Notifier) (proto.EncodeVoiceResult, error) {
	return proto.EncodeVoiceResult{}, b.notSTT("voicePrompt")
}
func (b *WhisperBackend) Dialogue(_ context.Context, _ string, _ proto.DialogueParams, _ server.Notifier) (proto.DialogueResult, error) {
	return proto.DialogueResult{}, b.notSTT("dialogue")
}
func (b *WhisperBackend) Align(_ context.Context, _ string, _ proto.AlignParams, _ server.Notifier) (proto.AlignResult, error) {
	return proto.AlignResult{}, b.notSTT("align")
}
func (b *WhisperBackend) VAD(_ context.Context, _ string, _ proto.VADParams, _ server.Notifier) (proto.VADResult, error) {
	return proto.VADResult{}, b.notSTT("vad")
}
func (b *WhisperBackend) Enhance(_ context.Context, _ string, _ proto.EnhanceParams, _ server.Notifier) (proto.EnhanceResult, error) {
	return proto.EnhanceResult{}, b.notSTT("enhance")
}
func (b *WhisperBackend) Separate(_ context.Context, _ string, _ proto.SeparateParams, _ server.Notifier) (proto.SeparateResult, error) {
	return proto.SeparateResult{}, b.notSTT("separate")
}
func (b *WhisperBackend) Diarize(_ context.Context, _ string, _ proto.DiarizeParams, _ server.Notifier) (proto.DiarizeResult, error) {
	return proto.DiarizeResult{}, b.notSTT("diarize")
}
func (b *WhisperBackend) Generate(_ context.Context, _ string, _ proto.LLMGenerateParams, _ server.Notifier) (proto.LLMGenerateResult, error) {
	return proto.LLMGenerateResult{}, b.notSTT("llm")
}
