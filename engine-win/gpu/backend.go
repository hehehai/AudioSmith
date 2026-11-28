// Package gpu implements the protocol-v2 "gpu" role for Windows using an
// everyday / iGPU-first hybrid backend router:
//  1. Native ONNX Runtime (DirectML on Intel/AMD/Qualcomm iGPU, CPU fallback)
//  2. llama.cpp adapter (Vulkan on iGPU, CUDA on NVIDIA, CPU fallback)
//  3. Optional on-demand Python worker bridge for PyTorch-only frontier models
package gpu

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hehehai/AudioSmith/engine-win/hw"
	"github.com/hehehai/AudioSmith/engine-win/server"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

// Backend executes AI model RPCs for one inference runtime (ONNX, llama.cpp,
// Python worker, or a fake in tests).
type Backend interface {
	Name() string
	Provider() string // e.g. "directml", "vulkan", "cuda", "cpu", "python"
	Available() bool
	Supports(task, model, modelPath string) bool

	Load(ctx context.Context, p proto.LoadParams) (proto.LoadResult, error)
	Unload(ctx context.Context, model string) error

	Synthesize(ctx context.Context, jobID string, p proto.SynthesizeParams, n server.Notifier) (proto.SynthesizeResult, error)
	EncodeVoice(ctx context.Context, jobID string, p proto.EncodeVoiceParams, n server.Notifier) (proto.EncodeVoiceResult, error)
	Dialogue(ctx context.Context, jobID string, p proto.DialogueParams, n server.Notifier) (proto.DialogueResult, error)
	Transcribe(ctx context.Context, jobID string, p proto.TranscribeParams, n server.Notifier) (proto.TranscribeResult, error)
	Align(ctx context.Context, jobID string, p proto.AlignParams, n server.Notifier) (proto.AlignResult, error)
	VAD(ctx context.Context, jobID string, p proto.VADParams, n server.Notifier) (proto.VADResult, error)
	Enhance(ctx context.Context, jobID string, p proto.EnhanceParams, n server.Notifier) (proto.EnhanceResult, error)
	Separate(ctx context.Context, jobID string, p proto.SeparateParams, n server.Notifier) (proto.SeparateResult, error)
	Diarize(ctx context.Context, jobID string, p proto.DiarizeParams, n server.Notifier) (proto.DiarizeResult, error)
	Generate(ctx context.Context, jobID string, p proto.LLMGenerateParams, n server.Notifier) (proto.LLMGenerateResult, error)
}

// DefaultBackends constructs the iGPU-first backend chain for hwInfo:
// ONNX (DirectML/CPU) -> llama.cpp (Vulkan/CUDA/CPU) -> whisper.cpp -> Python worker.
func DefaultBackends(hwInfo hw.Info) []Backend {
	return []Backend{
		NewONNXBackend(hwInfo),
		NewLlamaCppBackend(hwInfo),
		NewWhisperBackend(hwInfo),
		NewPythonBackend(hwInfo),
	}
}

// unsupportedBackendError returns a structured protocol-v2 error explaining
// which backend/runtime is needed and why it is unavailable.
func unsupportedBackendError(backend, provider, capability, detail string) *proto.Error {
	data, _ := json.Marshal(map[string]string{
		"backend":    backend,
		"provider":   provider,
		"capability": capability,
	})
	return &proto.Error{
		Code:    proto.CodeUnsupported,
		Message: fmt.Sprintf("%s (%s): %s", backend, provider, detail),
		Data:    data,
	}
}

func hasFileWithExt(dir, ext string) bool {
	if dir == "" {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ext {
			return true
		}
	}
	return false
}
