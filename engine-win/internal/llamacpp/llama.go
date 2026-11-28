package llamacpp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hehehai/AudioSmith/engine-win/hw"
	"github.com/hehehai/AudioSmith/engine-win/server"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

// Manager drives local llama.cpp inference via:
//  1. An externally configured HTTP URL (AUDIOSMITH_LLAMA_URL),
//  2. A managed llama-server subprocess spawned on demand on 127.0.0.1:<port>, or
//  3. A one-shot llama-cli subprocess fallback.
type Manager struct {
	hw          hw.Info
	provider    string
	externalURL string
	serverBin   string
	cliBin      string
	client      *http.Client

	mu          sync.Mutex
	cmd         *exec.Cmd
	activeURL   string
	activeModel string
}

// NewManager creates a Manager using environment discovery.
func NewManager(hwInfo hw.Info) *Manager {
	return &Manager{
		hw:          hwInfo,
		provider:    SelectProvider(hwInfo),
		externalURL: strings.TrimRight(os.Getenv("AUDIOSMITH_LLAMA_URL"), "/"),
		serverBin:   DiscoverLlamaServer(),
		cliBin:      DiscoverLlamaCLI(),
		client:      &http.Client{},
	}
}

// NewManagerWithConfig creates a Manager with explicit paths/URL (useful in tests).
func NewManagerWithConfig(hwInfo hw.Info, externalURL, serverBin, cliBin string) *Manager {
	return &Manager{
		hw:          hwInfo,
		provider:    SelectProvider(hwInfo),
		externalURL: strings.TrimRight(externalURL, "/"),
		serverBin:   serverBin,
		cliBin:      cliBin,
		client:      &http.Client{},
	}
}

func (m *Manager) Provider() string { return m.provider }

// Available reports whether any llama.cpp runtime (URL, llama-server, or llama-cli) is present.
func (m *Manager) Available() bool {
	return m.externalURL != "" || m.serverBin != "" || m.cliBin != ""
}

// Load prepares a GGUF model by starting llama-server if available.
func (m *Manager) Load(ctx context.Context, model, modelPath string) (proto.LoadResult, error) {
	if !m.Available() {
		return proto.LoadResult{}, fmt.Errorf("llama.cpp runtime not found (install llama-server.exe or set AUDIOSMITH_LLAMA_URL)")
	}
	start := time.Now()
	gguf := ResolveModelFile(modelPath, ".gguf")
	if m.externalURL == "" && m.serverBin != "" && gguf != "" {
		if _, err := m.ensureServer(ctx, gguf); err != nil {
			return proto.LoadResult{}, err
		}
	}
	var sz int64
	if gguf != "" {
		if fi, err := os.Stat(gguf); err == nil {
			sz = fi.Size()
		}
	}
	return proto.LoadResult{
		Model:        model,
		Task:         "llm",
		Seconds:      time.Since(start).Seconds(),
		Bytes:        sz,
		Capabilities: []string{"json", "stream"},
	}, nil
}

// Unload stops any managed llama-server subprocess.
func (m *Manager) Unload() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
	return nil
}

// Close terminates any managed subprocess.
func (m *Manager) Close() error { return m.Unload() }

// Generate runs llm.generate via HTTP (external or managed llama-server) or llama-cli fallback.
func (m *Manager) Generate(ctx context.Context, jobID string, p proto.LLMGenerateParams, n server.Notifier) (proto.LLMGenerateResult, error) {
	if !m.Available() {
		return proto.LLMGenerateResult{}, fmt.Errorf("llama.cpp runtime is not installed")
	}

	if m.externalURL != "" {
		return m.generateHTTP(ctx, m.externalURL, jobID, p, n)
	}

	gguf := ResolveModelFile(p.ModelPath, ".gguf")
	if m.serverBin != "" {
		if gguf == "" {
			m.mu.Lock()
			url := m.activeURL
			m.mu.Unlock()
			if url != "" {
				return m.generateHTTP(ctx, url, jobID, p, n)
			}
			return proto.LLMGenerateResult{}, proto.Errorf(proto.CodeBadRequest, "llm.generate: modelPath (.gguf) is required for managed llama-server")
		}
		url, err := m.ensureServer(ctx, gguf)
		if err != nil {
			return proto.LLMGenerateResult{}, err
		}
		return m.generateHTTP(ctx, url, jobID, p, n)
	}

	if m.cliBin != "" {
		if gguf == "" {
			return proto.LLMGenerateResult{}, proto.Errorf(proto.CodeBadRequest, "llm.generate: modelPath (.gguf) is required for llama-cli")
		}
		return m.generateCLI(ctx, gguf, jobID, p, n)
	}

	return proto.LLMGenerateResult{}, fmt.Errorf("llama.cpp runtime is not configured")
}

func (m *Manager) ensureServer(ctx context.Context, ggufPath string) (string, error) {
	m.mu.Lock()
	if m.cmd != nil && m.cmd.ProcessState == nil && m.activeModel == ggufPath && m.activeURL != "" {
		url := m.activeURL
		m.mu.Unlock()
		return url, nil
	}
	m.stopLocked()

	port, err := pickFreePort()
	if err != nil {
		m.mu.Unlock()
		return "", fmt.Errorf("llama-server port: %w", err)
	}
	args := []string{
		"--model", ggufPath,
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(port),
		"--ctx-size", "4096",
		"--threads", strconv.Itoa(RecommendedThreads(m.hw)),
		"-ngl", strconv.Itoa(GPULayers(m.hw)),
	}
	cmd := exec.Command(m.serverBin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		m.mu.Unlock()
		return "", fmt.Errorf("start llama-server: %w", err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	m.cmd = cmd
	m.activeURL = url
	m.activeModel = ggufPath
	m.mu.Unlock()

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	if err := m.waitHealth(ctx, url, exited, &stderr); err != nil {
		_ = m.Unload()
		return "", err
	}
	return url, nil
}

func (m *Manager) waitHealth(ctx context.Context, baseURL string, exited <-chan error, stderr *bytes.Buffer) error {
	deadline := time.NewTimer(90 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-exited:
			return fmt.Errorf("llama-server exited early (%v): %s", err, strings.TrimSpace(stderr.String()))
		case <-deadline.C:
			return fmt.Errorf("llama-server health check timed out")
		case <-tick.C:
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health", nil)
			if err != nil {
				continue
			}
			resp, err := m.client.Do(req)
			if err != nil {
				continue
			}
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
	}
}

func (m *Manager) stopLocked() {
	if m.cmd != nil && m.cmd.Process != nil {
		_ = m.cmd.Process.Kill()
	}
	m.cmd = nil
	m.activeURL = ""
	m.activeModel = ""
}

func (m *Manager) generateHTTP(ctx context.Context, baseURL, jobID string, p proto.LLMGenerateParams, n server.Notifier) (proto.LLMGenerateResult, error) {
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return proto.LLMGenerateResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return proto.LLMGenerateResult{}, fmt.Errorf("llama-server request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return proto.LLMGenerateResult{}, fmt.Errorf("llama-server HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	if p.Stream {
		return readStream(resp.Body, jobID, p.Model, constrained, start, n)
	}
	return readNonStream(resp.Body, p.Model, constrained, start)
}

func (m *Manager) generateCLI(ctx context.Context, ggufPath, jobID string, p proto.LLMGenerateParams, n server.Notifier) (proto.LLMGenerateResult, error) {
	start := time.Now()
	maxTokens := p.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 512
	}
	prompt := formatChatPrompt(p.Messages)
	args := []string{
		"-m", ggufPath,
		"-p", prompt,
		"-n", strconv.Itoa(maxTokens),
		"-ngl", strconv.Itoa(GPULayers(m.hw)),
		"-t", strconv.Itoa(RecommendedThreads(m.hw)),
		"--no-display-prompt",
	}
	if p.Temperature != nil {
		args = append(args, "--temp", strconv.FormatFloat(*p.Temperature, 'f', 3, 64))
	}
	if p.TopP != nil {
		args = append(args, "--top-p", strconv.FormatFloat(*p.TopP, 'f', 3, 64))
	}
	if p.Seed != nil {
		args = append(args, "-s", strconv.FormatInt(*p.Seed, 10))
	}
	constrained := false
	if p.JSON {
		if len(p.Schema) > 0 {
			args = append(args, "--json-schema", string(p.Schema))
			constrained = true
		} else {
			args = append(args, "--json-schema", `{"type":"object"}`)
			constrained = true
		}
	}

	cmd := exec.CommandContext(ctx, m.cliBin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return proto.LLMGenerateResult{}, fmt.Errorf("llama-cli failed: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}

	text := strings.TrimSpace(stdout.String())
	if p.Stream && n != nil && text != "" {
		_ = n.Notify(proto.MethodLLMToken, proto.LLMTokenParams{Job: jobID, Text: text})
	}
	secs := time.Since(start).Seconds()
	return proto.LLMGenerateResult{
		Text:         text,
		FinishReason: "stop",
		Model:        p.Model,
		Seconds:      secs,
		Constrained:  constrained,
	}, nil
}

func formatChatPrompt(msgs []proto.LLMMessage) string {
	var b strings.Builder
	for _, m := range msgs {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		if role == "" {
			role = "user"
		}
		fmt.Fprintf(&b, "<|im_start|>%s\n%s<|im_end|>\n", role, m.Content)
	}
	b.WriteString("<|im_start|>assistant\n")
	return b.String()
}

func readNonStream(r io.Reader, model string, constrained bool, start time.Time) (proto.LLMGenerateResult, error) {
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

func readStream(r io.Reader, jobID, model string, constrained bool, start time.Time, n server.Notifier) (proto.LLMGenerateResult, error) {
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

func pickFreePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}
