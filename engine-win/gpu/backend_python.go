package gpu

import (
	"context"
	"os"
	"path/filepath"

	"github.com/hehehai/AudioSmith/engine-win/hw"
	"github.com/hehehai/AudioSmith/engine-win/server"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

// PythonBackend is the optional on-demand bridge to a managed
// python-build-standalone + uv worker speaking protocol-v2 over stdio for
// PyTorch-only frontier models (Qwen3-TTS, VoxCPM2, Irodori-TTS, SoulX-Podcast,
// MOSS-TTSD, Qwen3-ASR). Everyday iGPU/CPU users never need to download it
// unless they explicitly opt into a PyTorch-only model.
type PythonBackend struct {
	hw         hw.Info
	workerPath string
}

// NewPythonBackend discovers an installed managed Python worker if present.
func NewPythonBackend(hwInfo hw.Info) *PythonBackend {
	return &PythonBackend{
		hw:         hwInfo,
		workerPath: findPythonWorker(),
	}
}

func findPythonWorker() string {
	if p := os.Getenv("AUDIOSMITH_PY_WORKER"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		cand := filepath.Join(local, "ActNow", "runtimes", "python", "python.exe")
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	return ""
}

func (b *PythonBackend) Name() string     { return "python" }
func (b *PythonBackend) Provider() string { return string(b.hw.GPUTier) }
func (b *PythonBackend) Available() bool  { return b.workerPath != "" }

// Supports returns true for any task as the catch-all fallback for PyTorch-only
// frontier models after native ONNX and llama.cpp backends.
func (b *PythonBackend) Supports(task, _, _ string) bool {
	switch task {
	case "tts", "dialogue", "stt", "align", "vad", "enhance", "separate", "diarize", "llm":
		return true
	}
	return false
}

func (b *PythonBackend) notReady(capName string) error {
	return unsupportedBackendError(b.Name(), b.Provider(), capName, "optional Python GPU runtime is not installed yet")
}

func (b *PythonBackend) Load(_ context.Context, p proto.LoadParams) (proto.LoadResult, error) {
	return proto.LoadResult{}, b.notReady("models.load:" + p.Model)
}
func (b *PythonBackend) Unload(_ context.Context, _ string) error { return nil }

func (b *PythonBackend) Synthesize(_ context.Context, _ string, _ proto.SynthesizeParams, _ server.Notifier) (proto.SynthesizeResult, error) {
	return proto.SynthesizeResult{}, b.notReady("tts")
}
func (b *PythonBackend) EncodeVoice(_ context.Context, _ string, _ proto.EncodeVoiceParams, _ server.Notifier) (proto.EncodeVoiceResult, error) {
	return proto.EncodeVoiceResult{}, b.notReady("voicePrompt")
}
func (b *PythonBackend) Dialogue(_ context.Context, _ string, _ proto.DialogueParams, _ server.Notifier) (proto.DialogueResult, error) {
	return proto.DialogueResult{}, b.notReady("dialogue")
}
func (b *PythonBackend) Transcribe(_ context.Context, _ string, _ proto.TranscribeParams, _ server.Notifier) (proto.TranscribeResult, error) {
	return proto.TranscribeResult{}, b.notReady("stt")
}
func (b *PythonBackend) Align(_ context.Context, _ string, _ proto.AlignParams, _ server.Notifier) (proto.AlignResult, error) {
	return proto.AlignResult{}, b.notReady("align")
}
func (b *PythonBackend) VAD(_ context.Context, _ string, _ proto.VADParams, _ server.Notifier) (proto.VADResult, error) {
	return proto.VADResult{}, b.notReady("vad")
}
func (b *PythonBackend) Enhance(_ context.Context, _ string, _ proto.EnhanceParams, _ server.Notifier) (proto.EnhanceResult, error) {
	return proto.EnhanceResult{}, b.notReady("enhance")
}
func (b *PythonBackend) Separate(_ context.Context, _ string, _ proto.SeparateParams, _ server.Notifier) (proto.SeparateResult, error) {
	return proto.SeparateResult{}, b.notReady("separate")
}
func (b *PythonBackend) Diarize(_ context.Context, _ string, _ proto.DiarizeParams, _ server.Notifier) (proto.DiarizeResult, error) {
	return proto.DiarizeResult{}, b.notReady("diarize")
}
func (b *LlamaCppBackend) String() string { return b.Name() }

func (b *PythonBackend) Generate(_ context.Context, _ string, _ proto.LLMGenerateParams, _ server.Notifier) (proto.LLMGenerateResult, error) {
	return proto.LLMGenerateResult{}, b.notReady("llm")
}
