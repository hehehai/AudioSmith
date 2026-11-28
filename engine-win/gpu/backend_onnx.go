package gpu

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hehehai/AudioSmith/engine-win/hw"
	"github.com/hehehai/AudioSmith/engine-win/internal/onnx"
	"github.com/hehehai/AudioSmith/engine-win/server"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

type onnxModelEntry struct {
	task      string
	modelPath string
	session   onnx.Session
	vad       *onnx.SileroVAD
	kokoro    *onnx.KokoroTTS
	denoise   *onnx.DeepFilterNet
	provider  string
	bytes     int64
}

// ONNXBackend adapts native ONNX Runtime (DirectML on Windows x64/ARM64 iGPU,
// CPU fallback) for lightweight models (Silero VAD, Kokoro 82M TTS, and future
// ONNX models) with zero CGO.
type ONNXBackend struct {
	hw       hw.Info
	libPath  string
	provider string

	mu       sync.Mutex
	rt       onnx.Runtime
	injected bool
	models   map[string]*onnxModelEntry
}

// NewONNXBackend discovers an installed ONNX Runtime library and prefers
// DirectML on iGPU/GPU Windows hosts, falling back cleanly to CPU EP.
func NewONNXBackend(hwInfo hw.Info) *ONNXBackend {
	provider := "cpu"
	if hwInfo.GPUTier == hw.TierDirectML || hwInfo.GPUTier == hw.TierCUDA {
		provider = "directml"
	}
	return &ONNXBackend{
		hw:       hwInfo,
		libPath:  onnx.DiscoverDLL(),
		provider: provider,
		models:   map[string]*onnxModelEntry{},
	}
}

// NewONNXBackendWithRuntime creates an ONNXBackend backed by an explicit
// onnx.Runtime (used by unit and integration tests on Linux/macOS).
func NewONNXBackendWithRuntime(hwInfo hw.Info, rt onnx.Runtime) *ONNXBackend {
	b := NewONNXBackend(hwInfo)
	b.rt = rt
	b.injected = true
	return b
}

func (b *ONNXBackend) Name() string     { return "onnx" }
func (b *ONNXBackend) Provider() string { return b.provider }
func (b *ONNXBackend) Available() bool  { return b.injected || b.libPath != "" }

func (b *ONNXBackend) Supports(task, model, modelPath string) bool {
	if onnx.ResolveModelFile(modelPath) != "" || hasFileWithExt(modelPath, ".onnx") {
		return true
	}
	low := strings.ToLower(model)
	switch task {
	case "vad":
		return true
	case "tts":
		return strings.Contains(low, "kokoro") || strings.Contains(low, "onnx")
	case "enhance":
		return strings.Contains(low, "onnx") || strings.Contains(low, "deepfilter")
	case "stt":
		return strings.Contains(low, "onnx")
	}
	return false
}

func (b *ONNXBackend) ensureRuntime() (onnx.Runtime, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.rt != nil {
		return b.rt, nil
	}
	if b.libPath == "" {
		b.libPath = onnx.DiscoverDLL()
	}
	if b.libPath == "" {
		return nil, unsupportedBackendError(b.Name(), b.provider, "runtime", "onnxruntime.dll not found")
	}
	rt, err := onnx.Open(b.libPath)
	if err != nil {
		return nil, fmt.Errorf("onnx open runtime: %w", err)
	}
	b.rt = rt
	return rt, nil
}

func (b *ONNXBackend) sessionOpts() onnx.SessionOptions {
	threads := b.hw.NumCPU
	if threads > 4 {
		threads = 4
	}
	if threads < 1 {
		threads = 1
	}
	return onnx.SessionOptions{
		PreferDirectML: b.provider == "directml",
		DeviceID:       0,
		IntraOpThreads: threads,
		InterOpThreads: 1,
	}
}

func (b *ONNXBackend) Load(_ context.Context, p proto.LoadParams) (proto.LoadResult, error) {
	entry, secs, err := b.getOrLoad(p.Model, p.Task, p.Path)
	if err != nil {
		return proto.LoadResult{}, err
	}
	if entry.provider != "" {
		b.provider = entry.provider
	} else if entry.session != nil {
		b.provider = entry.session.Provider()
	}
	sr := 16000
	var caps []string
	var langs []string
	switch entry.task {
	case "tts":
		sr = 24000
		caps = []string{"stream", "speed"}
		langs = []string{"en"}
	case "enhance":
		sr = 48000
	}
	return proto.LoadResult{
		Model:        p.Model,
		Task:         entry.task,
		SampleRate:   sr,
		Seconds:      secs,
		Bytes:        entry.bytes,
		Languages:    langs,
		Capabilities: caps,
	}, nil
}

func (b *ONNXBackend) getOrLoad(model, task, modelPath string) (*onnxModelEntry, float64, error) {
	if task == "" {
		low := strings.ToLower(model)
		switch {
		case strings.Contains(low, "vad") || strings.Contains(low, "silero"):
			task = "vad"
		case strings.Contains(low, "deepfilter") || strings.Contains(low, "denoise"):
			task = "enhance"
		default:
			task = "tts"
		}
	}

	b.mu.Lock()
	if e, ok := b.models[model]; ok {
		b.mu.Unlock()
		return e, 0, nil
	}
	b.mu.Unlock()

	rt, err := b.ensureRuntime()
	if err != nil {
		return nil, 0, err
	}

	if task == "enhance" {
		start := time.Now()
		df, sz, prov, err := onnx.OpenDeepFilterNet(rt, modelPath, b.sessionOpts())
		if err != nil {
			return nil, 0, err
		}
		secs := time.Since(start).Seconds()
		entry := &onnxModelEntry{
			task:      task,
			modelPath: modelPath,
			denoise:   df,
			provider:  prov,
			bytes:     sz,
		}
		b.mu.Lock()
		if prev, ok := b.models[model]; ok {
			closeEntry(prev)
		}
		b.models[model] = entry
		b.mu.Unlock()
		return entry, secs, nil
	}

	var preferred []string
	if task == "vad" {
		preferred = []string{"silero_vad.onnx", "model.onnx"}
	} else {
		preferred = []string{"kokoro-v1.0.onnx", "model_quantized.onnx", "model.onnx"}
	}
	onnxFile := onnx.ResolveModelFile(modelPath, preferred...)
	if onnxFile == "" {
		if b.injected && modelPath != "" {
			onnxFile = modelPath
		} else {
			return nil, 0, proto.Errorf(proto.CodeFileNotFound, "onnx: no .onnx model file found at %q", modelPath)
		}
	}

	start := time.Now()
	sess, err := rt.NewSession(onnxFile, b.sessionOpts())
	if err != nil {
		return nil, 0, fmt.Errorf("onnx: load %q: %w", onnxFile, err)
	}
	secs := time.Since(start).Seconds()

	var sz int64
	if fi, err := os.Stat(onnxFile); err == nil {
		sz = fi.Size()
	}
	modelDir := modelPath
	if fi, err := os.Stat(modelPath); err == nil && !fi.IsDir() {
		modelDir = filepath.Dir(modelPath)
	}

	entry := &onnxModelEntry{
		task:      task,
		modelPath: onnxFile,
		session:   sess,
		provider:  sess.Provider(),
		bytes:     sz,
	}
	switch task {
	case "vad":
		entry.vad = onnx.NewSileroVAD(sess)
	case "tts":
		entry.kokoro = onnx.NewKokoroTTS(sess, modelDir)
	}

	b.mu.Lock()
	if prev, ok := b.models[model]; ok {
		closeEntry(prev)
	}
	b.models[model] = entry
	b.mu.Unlock()
	return entry, secs, nil
}

func closeEntry(e *onnxModelEntry) {
	if e.denoise != nil {
		_ = e.denoise.Close()
	}
	if e.session != nil {
		_ = e.session.Close()
	}
}

func (b *ONNXBackend) Unload(_ context.Context, model string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if model == "" {
		for id, e := range b.models {
			closeEntry(e)
			delete(b.models, id)
		}
		return nil
	}
	if e, ok := b.models[model]; ok {
		closeEntry(e)
		delete(b.models, model)
	}
	return nil
}

func (b *ONNXBackend) VAD(ctx context.Context, _ string, p proto.VADParams, _ server.Notifier) (proto.VADResult, error) {
	entry, _, err := b.getOrLoad(p.Model, "vad", p.ModelPath)
	if err != nil {
		return proto.VADResult{}, err
	}
	if entry.vad == nil {
		entry.vad = onnx.NewSileroVAD(entry.session)
	}
	return entry.vad.Detect(ctx, p)
}

func (b *ONNXBackend) Synthesize(ctx context.Context, jobID string, p proto.SynthesizeParams, n server.Notifier) (proto.SynthesizeResult, error) {
	entry, _, err := b.getOrLoad(p.Model, "tts", p.ModelPath)
	if err != nil {
		return proto.SynthesizeResult{}, err
	}
	if entry.kokoro == nil {
		dir := p.ModelPath
		if fi, err := os.Stat(dir); err == nil && !fi.IsDir() {
			dir = filepath.Dir(dir)
		}
		entry.kokoro = onnx.NewKokoroTTS(entry.session, dir)
	}
	return entry.kokoro.Synthesize(ctx, jobID, p, n)
}

func (b *ONNXBackend) notReady(capName string) error {
	return unsupportedBackendError(b.Name(), b.provider, capName, "ONNX runner for this task is not implemented yet")
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
func (b *ONNXBackend) Enhance(ctx context.Context, jobID string, p proto.EnhanceParams, n server.Notifier) (proto.EnhanceResult, error) {
	entry, _, err := b.getOrLoad(p.Model, "enhance", p.ModelPath)
	if err != nil {
		return proto.EnhanceResult{}, err
	}
	if entry.denoise == nil {
		return proto.EnhanceResult{}, b.notReady("enhance")
	}
	return entry.denoise.Enhance(ctx, jobID, p, n)
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
