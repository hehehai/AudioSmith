package gpu

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/hehehai/AudioSmith/engine-win/hw"
	"github.com/hehehai/AudioSmith/engine-win/internal/pyworker"
	"github.com/hehehai/AudioSmith/engine-win/server"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/winruntime"
)

const pythonNotInstalledDetail = "optional Python/PyTorch worker is not installed — install it under %LOCALAPPDATA%\\ActNow\\runtimes\\python or set AUDIOSMITH_PY_WORKER to run Qwen3-TTS / Irodori-TTS"

// PythonBackend is the optional on-demand bridge to a managed Python/PyTorch
// worker speaking protocol-v2 NDJSON over stdio for Chinese/Japanese neural
// TTS models (Qwen3-TTS, Irodori-TTS v4.1). Everyday iGPU/CPU users never
// need to download it unless they explicitly opt into a PyTorch model.
type PythonBackend struct {
	hw         hw.Info
	workerPath string
	cfg        pyworker.Config

	mu     sync.Mutex
	client *pyworker.Client
}

// NewPythonBackend discovers an installed optional Python worker if present.
func NewPythonBackend(hwInfo hw.Info) *PythonBackend {
	p := winruntime.DiscoverPython()
	return NewPythonBackendWithConfig(hwInfo, pyworker.Config{PythonPath: p})
}

// NewPythonBackendWithConfig constructs a PythonBackend with an explicit
// pyworker.Config (used by tests and custom installations).
func NewPythonBackendWithConfig(hwInfo hw.Info, cfg pyworker.Config) *PythonBackend {
	return &PythonBackend{
		hw:         hwInfo,
		workerPath: cfg.PythonPath,
		cfg:        cfg,
	}
}

func (b *PythonBackend) Name() string { return "python" }

func (b *PythonBackend) Provider() string {
	b.mu.Lock()
	c := b.client
	b.mu.Unlock()
	if c != nil {
		if dev := c.Device(); dev != "" {
			return dev
		}
	}
	return string(b.hw.GPUTier)
}

func (b *PythonBackend) Available() bool { return b.workerPath != "" }

// Supports returns true for any task as the catch-all fallback for PyTorch-only
// frontier models after native ONNX, llama.cpp, and whisper.cpp backends.
func (b *PythonBackend) Supports(task, _, _ string) bool {
	switch task {
	case "tts", "dialogue", "stt", "align", "vad", "enhance", "separate", "diarize", "llm":
		return true
	}
	return false
}

// Close shuts down the managed Python subprocess if running.
func (b *PythonBackend) Close() error {
	b.mu.Lock()
	c := b.client
	b.client = nil
	b.mu.Unlock()
	if c != nil {
		return c.Close()
	}
	return nil
}

func (b *PythonBackend) ensureClient() (*pyworker.Client, error) {
	if !b.Available() {
		return nil, b.notReady("python")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.client == nil {
		b.client = pyworker.New(b.cfg)
	}
	return b.client, nil
}

func (b *PythonBackend) notReady(capName string) error {
	return unsupportedBackendError(b.Name(), b.Provider(), capName, pythonNotInstalledDetail)
}

func forwardNotifier(n server.Notifier) pyworker.NotifyFunc {
	if n == nil {
		return nil
	}
	return func(method string, params json.RawMessage) {
		_ = n.Notify(method, params)
	}
}

func (b *PythonBackend) Load(ctx context.Context, p proto.LoadParams) (proto.LoadResult, error) {
	c, err := b.ensureClient()
	if err != nil {
		return proto.LoadResult{}, b.notReady("models.load:" + p.Model)
	}
	var res proto.LoadResult
	if err := c.Call(ctx, proto.MethodModelsLoad, p, &res, nil); err != nil {
		return proto.LoadResult{}, err
	}
	return res, nil
}

func (b *PythonBackend) Unload(ctx context.Context, model string) error {
	if !b.Available() {
		return nil
	}
	b.mu.Lock()
	c := b.client
	b.mu.Unlock()
	if c == nil {
		return nil
	}
	return c.Call(ctx, proto.MethodModelsUnload, proto.UnloadParams{Model: model}, nil, nil)
}

type synthWithJob struct {
	proto.SynthesizeParams
	Job string `json:"job,omitempty"`
}

func (b *PythonBackend) Synthesize(ctx context.Context, jobID string, p proto.SynthesizeParams, n server.Notifier) (proto.SynthesizeResult, error) {
	c, err := b.ensureClient()
	if err != nil {
		return proto.SynthesizeResult{}, b.notReady("tts")
	}
	var res proto.SynthesizeResult
	req := synthWithJob{SynthesizeParams: p, Job: jobID}
	if err := c.Call(ctx, proto.MethodTTSSynthesize, req, &res, forwardNotifier(n)); err != nil {
		return proto.SynthesizeResult{}, err
	}
	return res, nil
}

func (b *PythonBackend) EncodeVoice(ctx context.Context, _ string, p proto.EncodeVoiceParams, n server.Notifier) (proto.EncodeVoiceResult, error) {
	c, err := b.ensureClient()
	if err != nil {
		return proto.EncodeVoiceResult{}, b.notReady("voicePrompt")
	}
	var res proto.EncodeVoiceResult
	if err := c.Call(ctx, proto.MethodTTSEncodeVoice, p, &res, forwardNotifier(n)); err != nil {
		return proto.EncodeVoiceResult{}, err
	}
	return res, nil
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
