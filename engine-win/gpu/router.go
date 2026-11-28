package gpu

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"time"

	"github.com/hehehai/AudioSmith/engine-win/hw"
	"github.com/hehehai/AudioSmith/engine-win/server"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

type loadedEntry struct {
	info    proto.LoadedModel
	backend Backend
	lastUse time.Time
}

// Service implements the protocol-v2 "gpu" role: model lifecycle, memory
// budget/LRU tracking, and iGPU-first routing across pluggable backends.
type Service struct {
	hw       hw.Info
	backends []Backend

	mu           sync.Mutex
	models       map[string]*loadedEntry
	memoryBudget int64
	held         []string
	holdAll      bool
	peakBytes    int64
}

// New creates a GPU Service. If backends is empty, DefaultBackends(hwInfo) is used.
func New(hwInfo hw.Info, backends ...Backend) *Service {
	if len(backends) == 0 {
		backends = DefaultBackends(hwInfo)
	}
	return &Service{
		hw:           hwInfo,
		backends:     backends,
		models:       map[string]*loadedEntry{},
		memoryBudget: hwInfo.Budget.RecommendedModelBudget,
	}
}

// Hooks returns the RoleHooks for server.Config.
func (g *Service) Hooks() server.RoleHooks {
	return server.RoleHooks{
		Concurrency: 1,
		Families:    []string{"onnx", "gguf", "qwen3", "qwen3_tts", "irodori_tts", "kokoro", "whisper", "vad", "deepfilternet", "demucs"},
		Stats:       g.stats,
		Flush:       g.flush,
		Configure:   g.configure,
	}
}

// Register attaches all GPU role RPC handlers to s.
func (g *Service) Register(s *server.Server) {
	s.Handle(proto.MethodModelsLoad, g.handleLoad)
	s.Handle(proto.MethodModelsUnload, g.handleUnload)
	s.Handle(proto.MethodModelsLoaded, g.handleLoaded)
	s.Handle(proto.MethodTTSSynthesize, g.handleSynthesize)
	s.Handle(proto.MethodTTSEncodeVoice, g.handleEncodeVoice)
	s.Handle(proto.MethodTTSDialogue, g.handleDialogue)
	s.Handle(proto.MethodSTTTranscribe, g.handleTranscribe)
	s.Handle(proto.MethodSTTAlign, g.handleAlign)
	s.Handle(proto.MethodVADDetect, g.handleVAD)
	s.Handle(proto.MethodEnhanceRun, g.handleEnhance)
	s.Handle(proto.MethodSeparateRun, g.handleSeparate)
	s.Handle(proto.MethodDiarizeRun, g.handleDiarize)
	s.Handle(proto.MethodLLMGenerate, g.handleLLMGenerate)
}

func idleSeconds(now, lastUse time.Time) float64 {
	if d := now.Sub(lastUse).Seconds(); d > 0 {
		return d
	}
	// Windows clock ticks can read 0 for back-to-back load + stats RPCs;
	// supervisor treats IdleSeconds > 0 as idle.
	return 1e-6
}

func (g *Service) stats() (proto.Memory, []proto.LoadedModel) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	var active int64
	out := make([]proto.LoadedModel, 0, len(g.models))
	for _, e := range g.models {
		m := e.info
		m.IdleSeconds = idleSeconds(now, e.lastUse)
		active += m.Bytes
		out = append(out, m)
	}
	if active > g.peakBytes {
		g.peakBytes = active
	}
	slices.SortFunc(out, func(a, b proto.LoadedModel) int {
		if a.Model < b.Model {
			return -1
		}
		if a.Model > b.Model {
			return 1
		}
		return 0
	})
	return proto.Memory{Active: active, Peak: g.peakBytes}, out
}

func (g *Service) flush() {
	// Native runtimes release scratch caches when idle; placeholder for Phase 1.5 hooks.
}

func (g *Service) configure(p proto.EngineConfigParams) (proto.EngineConfigResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if p.MemoryBudget != nil {
		if *p.MemoryBudget <= 0 {
			g.memoryBudget = g.hw.Budget.RecommendedModelBudget
		} else {
			g.memoryBudget = *p.MemoryBudget
		}
	}
	if p.Held != nil {
		g.held = slices.Clone(*p.Held)
	}
	if p.HoldAll != nil {
		g.holdAll = *p.HoldAll
	}
	held := slices.Clone(g.held)
	if held == nil {
		held = []string{}
	}
	return proto.EngineConfigResult{
		MemoryBudget: g.memoryBudget,
		Held:         held,
		HoldAll:      g.holdAll,
	}, nil
}

func (g *Service) route(task, model, modelPath string) (Backend, error) {
	var firstMatch Backend
	for _, b := range g.backends {
		if b.Supports(task, model, modelPath) {
			if firstMatch == nil {
				firstMatch = b
			}
			if b.Available() {
				return b, nil
			}
		}
	}
	if firstMatch != nil {
		detail := "runtime is not installed yet (download on demand or place beside audiosmith-engine.exe)"
		if firstMatch.Name() == "python" {
			detail = pythonNotInstalledDetail
		}
		return nil, unsupportedBackendError(
			firstMatch.Name(),
			firstMatch.Provider(),
			task,
			detail,
		)
	}
	return nil, unsupportedBackendError("router", string(g.hw.GPUTier), task, "no backend supports model "+model)
}

func (g *Service) touch(model string) {
	g.mu.Lock()
	if e, ok := g.models[model]; ok {
		e.lastUse = time.Now()
	}
	g.mu.Unlock()
}

func (g *Service) handleLoad(ctx context.Context, _ string, raw json.RawMessage, _ server.Notifier) (any, error) {
	var p proto.LoadParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, proto.Errorf(proto.CodeBadRequest, "invalid models.load params: %v", err)
	}
	if p.Model == "" {
		return nil, proto.Errorf(proto.CodeBadRequest, "model is required")
	}
	task := p.Task
	if task == "" {
		task = "tts"
	}
	b, err := g.route(task, p.Model, p.Path)
	if err != nil {
		return nil, err
	}
	res, err := b.Load(ctx, p)
	if err != nil {
		return nil, err
	}
	if res.Model == "" {
		res.Model = p.Model
	}
	if res.Task == "" {
		res.Task = task
	}
	g.mu.Lock()
	g.models[res.Model] = &loadedEntry{
		info: proto.LoadedModel{
			Model:      res.Model,
			Task:       res.Task,
			SampleRate: res.SampleRate,
			Bytes:      res.Bytes,
		},
		backend: b,
		lastUse: time.Now(),
	}
	g.mu.Unlock()
	return res, nil
}

func (g *Service) handleUnload(ctx context.Context, _ string, raw json.RawMessage, _ server.Notifier) (any, error) {
	var p proto.UnloadParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, proto.Errorf(proto.CodeBadRequest, "invalid models.unload params: %v", err)
		}
	}
	now := time.Now()
	g.mu.Lock()
	var victims []*loadedEntry
	for id, e := range g.models {
		if p.Model != "" && id != p.Model {
			continue
		}
		if p.IdleAtLeast > 0 && idleSeconds(now, e.lastUse) < p.IdleAtLeast {
			continue
		}
		victims = append(victims, e)
		delete(g.models, id)
	}
	g.mu.Unlock()

	unloaded := make([]string, 0, len(victims))
	for _, v := range victims {
		_ = v.backend.Unload(ctx, v.info.Model)
		unloaded = append(unloaded, v.info.Model)
	}
	slices.Sort(unloaded)
	return proto.UnloadResult{Unloaded: unloaded}, nil
}

func (g *Service) handleLoaded(_ context.Context, _ string, _ json.RawMessage, _ server.Notifier) (any, error) {
	_, models := g.stats()
	return proto.LoadedResult{Models: models}, nil
}

func (g *Service) handleSynthesize(ctx context.Context, jobID string, raw json.RawMessage, n server.Notifier) (any, error) {
	var p proto.SynthesizeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, proto.Errorf(proto.CodeBadRequest, "invalid tts.synthesize params: %v", err)
	}
	b, err := g.route("tts", p.Model, p.ModelPath)
	if err != nil {
		return nil, err
	}
	g.touch(p.Model)
	return b.Synthesize(ctx, jobID, p, n)
}

func (g *Service) handleEncodeVoice(ctx context.Context, jobID string, raw json.RawMessage, n server.Notifier) (any, error) {
	var p proto.EncodeVoiceParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, proto.Errorf(proto.CodeBadRequest, "invalid tts.encodeVoice params: %v", err)
	}
	b, err := g.route("tts", p.Model, p.ModelPath)
	if err != nil {
		return nil, err
	}
	g.touch(p.Model)
	return b.EncodeVoice(ctx, jobID, p, n)
}

func (g *Service) handleDialogue(ctx context.Context, jobID string, raw json.RawMessage, n server.Notifier) (any, error) {
	var p proto.DialogueParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, proto.Errorf(proto.CodeBadRequest, "invalid tts.dialogue params: %v", err)
	}
	b, err := g.route("dialogue", p.Model, p.ModelPath)
	if err != nil {
		return nil, err
	}
	g.touch(p.Model)
	return b.Dialogue(ctx, jobID, p, n)
}

func (g *Service) handleTranscribe(ctx context.Context, jobID string, raw json.RawMessage, n server.Notifier) (any, error) {
	var p proto.TranscribeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, proto.Errorf(proto.CodeBadRequest, "invalid stt.transcribe params: %v", err)
	}
	b, err := g.route("stt", p.Model, p.ModelPath)
	if err != nil {
		return nil, err
	}
	g.touch(p.Model)
	return b.Transcribe(ctx, jobID, p, n)
}

func (g *Service) handleAlign(ctx context.Context, jobID string, raw json.RawMessage, n server.Notifier) (any, error) {
	var p proto.AlignParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, proto.Errorf(proto.CodeBadRequest, "invalid stt.align params: %v", err)
	}
	b, err := g.route("align", p.Model, p.ModelPath)
	if err != nil {
		return nil, err
	}
	g.touch(p.Model)
	return b.Align(ctx, jobID, p, n)
}

func (g *Service) handleVAD(ctx context.Context, jobID string, raw json.RawMessage, n server.Notifier) (any, error) {
	var p proto.VADParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, proto.Errorf(proto.CodeBadRequest, "invalid vad.detect params: %v", err)
	}
	b, err := g.route("vad", p.Model, p.ModelPath)
	if err != nil {
		return nil, err
	}
	g.touch(p.Model)
	return b.VAD(ctx, jobID, p, n)
}

func (g *Service) handleEnhance(ctx context.Context, jobID string, raw json.RawMessage, n server.Notifier) (any, error) {
	var p proto.EnhanceParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, proto.Errorf(proto.CodeBadRequest, "invalid enhance.run params: %v", err)
	}
	b, err := g.route("enhance", p.Model, p.ModelPath)
	if err != nil {
		return nil, err
	}
	g.touch(p.Model)
	return b.Enhance(ctx, jobID, p, n)
}

func (g *Service) handleSeparate(ctx context.Context, jobID string, raw json.RawMessage, n server.Notifier) (any, error) {
	var p proto.SeparateParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, proto.Errorf(proto.CodeBadRequest, "invalid separate.run params: %v", err)
	}
	b, err := g.route("separate", p.Model, p.ModelPath)
	if err != nil {
		return nil, err
	}
	g.touch(p.Model)
	return b.Separate(ctx, jobID, p, n)
}

func (g *Service) handleDiarize(ctx context.Context, jobID string, raw json.RawMessage, n server.Notifier) (any, error) {
	var p proto.DiarizeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, proto.Errorf(proto.CodeBadRequest, "invalid diarize.run params: %v", err)
	}
	b, err := g.route("diarize", p.Model, p.ModelPath)
	if err != nil {
		return nil, err
	}
	g.touch(p.Model)
	return b.Diarize(ctx, jobID, p, n)
}

func (g *Service) handleLLMGenerate(ctx context.Context, jobID string, raw json.RawMessage, n server.Notifier) (any, error) {
	var p proto.LLMGenerateParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, proto.Errorf(proto.CodeBadRequest, "invalid llm.generate params: %v", err)
	}
	b, err := g.route("llm", p.Model, p.ModelPath)
	if err != nil {
		return nil, err
	}
	g.touch(p.Model)
	return b.Generate(ctx, jobID, p, n)
}
