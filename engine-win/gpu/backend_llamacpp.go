package gpu

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/engine-win/hw"
	"github.com/hehehai/AudioSmith/engine-win/server"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

// LlamaCppBackend adapts a local llama.cpp server (Vulkan on Intel/AMD iGPU,
// CUDA on NVIDIA, or CPU) for llm.generate with streaming llm.token
// notifications and native JSON Schema / GBNF constrained decoding.
type LlamaCppBackend struct {
	hw       hw.Info
	baseURL  string
	provider string
	client   *http.Client
}

// NewLlamaCppBackend constructs a llama.cpp adapter. Set AUDIOSMITH_LLAMA_URL
// (e.g. http://127.0.0.1:8080) to connect to a running llama-server instance.
func NewLlamaCppBackend(hwInfo hw.Info) *LlamaCppBackend {
	return NewLlamaCppBackendWithURL(hwInfo, strings.TrimRight(os.Getenv("AUDIOSMITH_LLAMA_URL"), "/"))
}

// NewLlamaCppBackendWithURL constructs a llama.cpp adapter pointing at baseURL.
func NewLlamaCppBackendWithURL(hwInfo hw.Info, baseURL string) *LlamaCppBackend {
	provider := "cpu"
	switch hwInfo.GPUTier {
	case hw.TierCUDA:
		provider = "cuda"
	case hw.TierVulkan, hw.TierDirectML:
		if hwInfo.Capabilities.Vulkan {
			provider = "vulkan"
		}
	}
	return &LlamaCppBackend{
		hw:       hwInfo,
		baseURL:  strings.TrimRight(baseURL, "/"),
		provider: provider,
		client:   &http.Client{},
	}
}

func (b *LlamaCppBackend) Name() string     { return "llamacpp" }
func (b *LlamaCppBackend) Provider() string { return b.provider }
func (b *LlamaCppBackend) Available() bool  { return b.baseURL != "" }

func (b *LlamaCppBackend) Supports(task, model, modelPath string) bool {
	if task == "llm" {
		return true
	}
	return hasFileWithExt(modelPath, ".gguf") || strings.Contains(strings.ToLower(model), "gguf")
}

func (b *LlamaCppBackend) Load(_ context.Context, p proto.LoadParams) (proto.LoadResult, error) {
	if !b.Available() {
		return proto.LoadResult{}, unsupportedBackendError(b.Name(), b.provider, "llm", "llama-server is not running (set AUDIOSMITH_LLAMA_URL)")
	}
	return proto.LoadResult{
		Model:        p.Model,
		Task:         "llm",
		Capabilities: []string{"json", "stream"},
	}, nil
}

func (b *LlamaCppBackend) Unload(_ context.Context, _ string) error { return nil }

func (b *LlamaCppBackend) Generate(ctx context.Context, jobID string, p proto.LLMGenerateParams, n server.Notifier) (proto.LLMGenerateResult, error) {
	if !b.Available() {
		return proto.LLMGenerateResult{}, unsupportedBackendError(b.Name(), b.provider, "llm", "llama-server is not configured")
	}
	start := time.Now()
	maxTokens := p.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 2048
	}
	body := map[string]any{
		"model":      p.Model,
		"messages":   p.Messages,
		"max_tokens": maxTokens,
		"stream":     p.Stream,
	}
	if p.Temperature != nil {
		body["temperature"] = *p.Temperature
	}
	if p.TopP != nil {
		body["top_p"] = *p.TopP
	}
	if p.Seed != nil {
		body["seed"] = *p.Seed
	}
	if len(p.Stop) > 0 {
		body["stop"] = p.Stop
	}
	constrained := false
	if p.JSON {
		if len(p.Schema) > 0 {
			body["response_format"] = map[string]any{
				"type":   "json_object",
				"schema": json.RawMessage(p.Schema),
			}
			constrained = true
		} else {
			body["response_format"] = map[string]any{"type": "json_object"}
			constrained = true
		}
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return proto.LLMGenerateResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.baseURL+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return proto.LLMGenerateResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.client.Do(req)
	if err != nil {
		return proto.LLMGenerateResult{}, fmt.Errorf("llama-server request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return proto.LLMGenerateResult{}, fmt.Errorf("llama-server HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	if p.Stream {
		return b.readStream(resp.Body, jobID, p.Model, constrained, start, n)
	}
	return b.readNonStream(resp.Body, p.Model, constrained, start)
}

func (b *LlamaCppBackend) readNonStream(r io.Reader, model string, constrained bool, start time.Time) (proto.LLMGenerateResult, error) {
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(r).Decode(&parsed); err != nil {
		return proto.LLMGenerateResult{}, err
	}
	var text, reason string
	if len(parsed.Choices) > 0 {
		text = parsed.Choices[0].Message.Content
		reason = parsed.Choices[0].FinishReason
	}
	if reason == "" {
		reason = "stop"
	}
	secs := time.Since(start).Seconds()
	tps := 0.0
	if secs > 0 && parsed.Usage.CompletionTokens > 0 {
		tps = float64(parsed.Usage.CompletionTokens) / secs
	}
	return proto.LLMGenerateResult{
		Text:             text,
		FinishReason:     reason,
		Model:            model,
		PromptTokens:     parsed.Usage.PromptTokens,
		CompletionTokens: parsed.Usage.CompletionTokens,
		Seconds:          secs,
		TokensPerSecond:  tps,
		Constrained:      constrained,
	}, nil
}

func (b *LlamaCppBackend) readStream(r io.Reader, jobID, model string, constrained bool, start time.Time, n server.Notifier) (proto.LLMGenerateResult, error) {
	var (
		full             strings.Builder
		reason           = "stop"
		promptTokens     int
		completionTokens int
	)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 32*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Usage != nil {
			promptTokens = chunk.Usage.PromptTokens
			completionTokens = chunk.Usage.CompletionTokens
		}
		if len(chunk.Choices) > 0 {
			c := chunk.Choices[0]
			if c.Delta.Content != "" {
				full.WriteString(c.Delta.Content)
				completionTokens++
				if n != nil {
					_ = n.Notify(proto.MethodLLMToken, proto.LLMTokenParams{
						Job:  jobID,
						Text: c.Delta.Content,
					})
				}
			}
			if c.FinishReason != "" {
				reason = c.FinishReason
			}
		}
	}
	if err := sc.Err(); err != nil {
		return proto.LLMGenerateResult{}, err
	}
	secs := time.Since(start).Seconds()
	tps := 0.0
	if secs > 0 && completionTokens > 0 {
		tps = float64(completionTokens) / secs
	}
	return proto.LLMGenerateResult{
		Text:             full.String(),
		FinishReason:     reason,
		Model:            model,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		Seconds:          secs,
		TokensPerSecond:  tps,
		Constrained:      constrained,
	}, nil
}

func (b *LlamaCppBackend) notLLM(capName string) error {
	return unsupportedBackendError(b.Name(), b.provider, capName, "llama.cpp backend serves llm.generate only")
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
