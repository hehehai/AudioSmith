package gpu

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/hehehai/AudioSmith/engine-win/hw"
	"github.com/hehehai/AudioSmith/engine-win/server"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

// ONNXBackend adapts native ONNX Runtime (DirectML on Windows x64/ARM64 iGPU,
// CPU fallback) for lightweight models (Kokoro, Silero VAD, DeepFilterNet3,
// Whisper ONNX, HTDemucs ONNX).
type ONNXBackend struct {
	hw       hw.Info
	libPath  string
	provider string
}

// NewONNXBackend discovers an installed ONNX Runtime library and picks the
// DirectML execution provider on iGPU/GPU Windows hosts, else CPU.
func NewONNXBackend(hwInfo hw.Info) *ONNXBackend {
	provider := "cpu"
	if hwInfo.GPUTier == hw.TierDirectML || hwInfo.GPUTier == hw.TierCUDA {
		provider = "directml"
	}
	return &ONNXBackend{
		hw:       hwInfo,
		libPath:  findONNXRuntime(),
		provider: provider,
	}
}

func findONNXRuntime() string {
	if p := os.Getenv("AUDIOSMITH_ONNXRUNTIME"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if exe, err := os.Executable(); err == nil {
		for _, name := range []string{"onnxruntime.dll", "libonnxruntime.so", "libonnxruntime.dylib"} {
			cand := filepath.Join(filepath.Dir(exe), name)
			if _, err := os.Stat(cand); err == nil {
				return cand
			}
		}
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		cand := filepath.Join(local, "ActNow", "runtimes", "onnx", "onnxruntime.dll")
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	return ""
}

func (b *ONNXBackend) Name() string     { return "onnx" }
func (b *ONNXBackend) Provider() string { return b.provider }
func (b *ONNXBackend) Available() bool  { return b.libPath != "" }

func (b *ONNXBackend) Supports(task, model, modelPath string) bool {
	if hasFileWithExt(modelPath, ".onnx") {
		return true
	}
	low := strings.ToLower(model)
	switch task {
	case "vad", "enhance":
		return true
	case "tts":
		return strings.Contains(low, "kokoro") || strings.Contains(low, "onnx")
	case "stt":
		return strings.Contains(low, "onnx")
	}
	return false
}

func (b *ONNXBackend) notReady(capName string) error {
	return unsupportedBackendError(b.Name(), b.provider, capName, "ONNX Runtime execution binding is scheduled for Phase 1.5")
}

func (b *ONNXBackend) Load(_ context.Context, p proto.LoadParams) (proto.LoadResult, error) {
	return proto.LoadResult{}, b.notReady("models.load:" + p.Model)
}

func (b *ONNXBackend) Unload(_ context.Context, _ string) error { return nil }

func (b *ONNXBackend) Synthesize(_ context.Context, _ string, _ proto.SynthesizeParams, _ server.Notifier) (proto.SynthesizeResult, error) {
	return proto.SynthesizeResult{}, b.notReady("tts")
}
func (b *ONNXBackend) EncodeVoice(_ context.Context, _ string, _ proto.EncodeVoiceParams, _ server.Notifier) (proto.EncodeVoiceResult, error) {
	return proto.EncodeVoiceResult{}, b.notReady("voicePrompt")
}
func (b *ONNXBackend) Dialogue(_ context.Context, _ string, _ proto.DialogueParams, _ server.Notifier) (proto.DialogueResult, error) {
	return proto.DialogueResult{}, b.notReady("dialogue")
}
func (b *ONNXBackend) Transcribe(_ context.Context, _ string, _ proto.TranscribeParams, _ server.Notifier) (proto.TranscribeResult, error) {
	return proto.TranscribeResult{}, b.notReady("stt")
}
func (b *ONNXBackend) Align(_ context.Context, _ string, _ proto.AlignParams, _ server.Notifier) (proto.AlignResult, error) {
	return proto.AlignResult{}, b.notReady("align")
}
func (b *ONNXBackend) VAD(_ context.Context, _ string, _ proto.VADParams, _ server.Notifier) (proto.VADResult, error) {
	return proto.VADResult{}, b.notReady("vad")
}
func (b *ONNXBackend) Enhance(_ context.Context, _ string, _ proto.EnhanceParams, _ server.Notifier) (proto.EnhanceResult, error) {
	return proto.EnhanceResult{}, b.notReady("enhance")
}
func (b *ONNXBackend) Separate(_ context.Context, _ string, _ proto.SeparateParams, _ server.Notifier) (proto.SeparateResult, error) {
	return proto.SeparateResult{}, b.notReady("separate")
}
func (b *ONNXBackend) Diarize(_ context.Context, _ string, _ proto.DiarizeParams, _ server.Notifier) (proto.DiarizeResult, error) {
	return proto.DiarizeResult{}, b.notReady("diarize")
}
func (b *ONNXBackend) Generate(_ context.Context, _ string, _ proto.LLMGenerateParams, _ server.Notifier) (proto.LLMGenerateResult, error) {
	return proto.LLMGenerateResult{}, b.notReady("llm")
}
