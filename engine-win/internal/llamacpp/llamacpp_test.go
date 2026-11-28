package llamacpp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hehehai/AudioSmith/engine-win/hw"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

func TestSelectProviderAndGPULayers(t *testing.T) {
	intelIGPU := hw.Classify("Windows 11", "amd64", 8, 16<<30, 8<<30, []hw.Adapter{
		{Name: "Intel Iris Xe Graphics", VendorID: hw.VendorIntel, DedicatedVideoMemory: 128 << 20, SharedSystemMemory: 8 << 30},
	}, hw.Capabilities{DirectML: true, Vulkan: true})
	if got := SelectProvider(intelIGPU); got != "vulkan" {
		t.Fatalf("Intel iGPU provider = %q, want vulkan", got)
	}
	if got := GPULayers(intelIGPU); got != 99 {
		t.Fatalf("Intel iGPU GPULayers = %d, want 99", got)
	}

	cpuOnly := hw.Classify("Windows 11", "arm64", 4, 6<<30, 3<<30, nil, hw.Capabilities{CPU: true})
	if got := SelectProvider(cpuOnly); got != "cpu" {
		t.Fatalf("CPU-only provider = %q, want cpu", got)
	}
	if got := GPULayers(cpuOnly); got != 0 {
		t.Fatalf("CPU-only GPULayers = %d, want 0", got)
	}
}

func TestDiscoverBinaries_EnvAndLocalAppData(t *testing.T) {
	dir := t.TempDir()
	llamaBin := filepath.Join(dir, "llama-server")
	if err := os.WriteFile(llamaBin, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AUDIOSMITH_LLAMA_SERVER", llamaBin)
	if got := DiscoverLlamaServer(); got != llamaBin {
		t.Fatalf("DiscoverLlamaServer = %q, want %q", got, llamaBin)
	}

	// Test LOCALAPPDATA\ActNow\runtimes\whisper\whisper-cli(.exe)
	local := t.TempDir()
	wDir := filepath.Join(local, "ActNow", "runtimes", "whisper")
	if err := os.MkdirAll(wDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wName := "whisper-cli"
	if runtime.GOOS == "windows" {
		wName += ".exe"
	}
	wBin := filepath.Join(wDir, wName)
	if err := os.WriteFile(wBin, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AUDIOSMITH_WHISPER_CLI", "")
	t.Setenv("LOCALAPPDATA", local)
	if got := DiscoverWhisperCLI(); got != wBin {
		t.Fatalf("DiscoverWhisperCLI = %q, want %q", got, wBin)
	}
}

func TestManager_HTTPStreamingAndSchema(t *testing.T) {
	var seenSchema bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if rf, ok := body["response_format"].(map[string]any); ok && rf["schema"] != nil {
			seenSchema = true
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"{\"answer\":"}}]}`)
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"42}"},"finish_reason":"stop"}]}`)
		fmt.Fprintln(w, `data: [DONE]`)
	}))
	defer ts.Close()

	mgr := NewManagerWithConfig(hw.Probe(), ts.URL, "", "")
	var tokens []string
	n := testNotifier(func(method string, params any) error {
		if method == proto.MethodLLMToken {
			tokens = append(tokens, params.(proto.LLMTokenParams).Text)
		}
		return nil
	})
	res, err := mgr.Generate(context.Background(), "j1", proto.LLMGenerateParams{
		Model:    "qwen3-4b.gguf",
		Messages: []proto.LLMMessage{{Role: "user", Content: "return 42"}},
		Stream:   true,
		JSON:     true,
		Schema:   json.RawMessage(`{"type":"object","properties":{"answer":{"type":"integer"}}}`),
	}, n)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if !seenSchema || !res.Constrained || res.Text != `{"answer":42}` || len(tokens) != 2 {
		t.Fatalf("unexpected result: %+v seenSchema=%v tokens=%v", res, seenSchema, tokens)
	}
}

func TestManager_FakeCLIScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test runs on Unix")
	}
	dir := t.TempDir()
	gguf := filepath.Join(dir, "qwen3-4b.gguf")
	if err := os.WriteFile(gguf, []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	argsLog := filepath.Join(dir, "args.txt")
	script := filepath.Join(dir, "fake-llama-cli.sh")
	body := fmt.Sprintf("#!/bin/sh\necho \"$@\" > %q\necho '{\"ok\":true}'\n", argsLog)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	cpuHW := hw.Classify("Windows 11", "arm64", 4, 6<<30, 3<<30, nil, hw.Capabilities{CPU: true})
	mgr := NewManagerWithConfig(cpuHW, "", "", script)
	res, err := mgr.Generate(context.Background(), "j-cli", proto.LLMGenerateParams{
		Model:     "qwen3-4b",
		ModelPath: dir,
		Messages:  []proto.LLMMessage{{Role: "user", Content: "hi"}},
		JSON:      true,
		Schema:    json.RawMessage(`{"type":"object"}`),
	}, nil)
	if err != nil {
		t.Fatalf("Generate CLI failed: %v", err)
	}
	if res.Text != `{"ok":true}` || !res.Constrained {
		t.Fatalf("unexpected CLI result: %+v", res)
	}
	logged, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "-ngl 0") || !strings.Contains(string(logged), "--json-schema") {
		t.Fatalf("unexpected CLI args: %q", string(logged))
	}
}

func TestWhisperRunner_FakeCLIScriptAndResample(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test runs on Unix")
	}
	dir := t.TempDir()
	modelBin := filepath.Join(dir, "ggml-base.bin")
	if err := os.WriteFile(modelBin, []byte("whisper"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create a 48 kHz stereo WAV to verify automatic 16 kHz mono resampling.
	inWAV := filepath.Join(dir, "stereo48k.wav")
	w, err := wav.Create(inWAV, wav.Format{Rate: 48000, Channels: 2, Encoding: wav.PCM16}, wav.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Write(make([]float32, 48000*2)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	script := filepath.Join(dir, "fake-whisper-cli.sh")
	body := `#!/bin/sh
OUT=""
while [ $# -gt 0 ]; do
  case "$1" in
    -of) OUT="$2"; shift 2 ;;
    *) shift ;;
  esac
done
cat > "${OUT}.json" <<'EOF'
{"result":{"language":"en"},"transcription":[{"offsets":{"from":0,"to":950},"text":" Hello Windows whisper"}]}
EOF
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	cpuHW := hw.Classify("Windows 11", "arm64", 4, 6<<30, 3<<30, nil, hw.Capabilities{CPU: true})
	wr := NewWhisperRunnerWithConfig(cpuHW, "", script)
	res, err := wr.Transcribe(context.Background(), proto.TranscribeParams{
		Model:     "whisper-base",
		ModelPath: dir,
		Audio:     inWAV,
		Language:  "en",
	})
	if err != nil {
		t.Fatalf("Transcribe failed: %v", err)
	}
	if res.Text != "Hello Windows whisper" || res.Language != "en" || len(res.Segments) != 1 {
		t.Fatalf("unexpected whisper result: %+v", res)
	}
	if res.AudioSeconds < 0.9 || res.AudioSeconds > 1.1 {
		t.Fatalf("unexpected AudioSeconds: %v", res.AudioSeconds)
	}
}

func TestWhisperRunner_HTTPServer(t *testing.T) {
	dir := t.TempDir()
	inWAV := filepath.Join(dir, "mono16k.wav")
	w, err := wav.Create(inWAV, wav.Format{Rate: 16000, Channels: 1, Encoding: wav.PCM16}, wav.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = w.Write(make([]float32, 16000))
	_ = w.Close()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"hello server","language":"en","duration":1.0,"segments":[{"start":0,"end":1.0,"text":"hello server"}]}`))
	}))
	defer ts.Close()

	wr := NewWhisperRunnerWithConfig(hw.Probe(), ts.URL, "")
	res, err := wr.Transcribe(context.Background(), proto.TranscribeParams{
		Model: "whisper-turbo",
		Audio: inWAV,
	})
	if err != nil {
		t.Fatalf("Transcribe HTTP failed: %v", err)
	}
	if res.Text != "hello server" || len(res.Segments) != 1 {
		t.Fatalf("unexpected HTTP whisper result: %+v", res)
	}
}

type testNotifier func(method string, params any) error

func (f testNotifier) Notify(method string, params any) error { return f(method, params) }
