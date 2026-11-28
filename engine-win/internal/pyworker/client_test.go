package pyworker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

// buildFakeWorker compiles a tiny cross-platform fake Python worker speaking
// protocol-v2 NDJSON over stdin/stdout so tests run identically on Linux,
// macOS, and Windows without requiring a Python installation.
func buildFakeWorker(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	bin := filepath.Join(dir, "fake-python")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	code := `package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type msg struct {
	ID     string         ` + "`json:\"id,omitempty\"`" + `
	Method string         ` + "`json:\"method,omitempty\"`" + `
	Params map[string]any ` + "`json:\"params,omitempty\"`" + `
}

func writeJSON(v any) {
	b, _ := json.Marshal(v)
	fmt.Println(string(b))
}

func main() {
	dev := os.Getenv("AUDIOSMITH_PY_DEVICE")
	if dev == "" {
		dev = "cpu"
	}
	loaded := map[string]string{}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m msg
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		switch m.Method {
		case "engine.hello":
			writeJSON(map[string]any{
				"id": m.ID,
				"result": map[string]any{
					"protocol": 2,
					"role":     "pyworker",
					"version":  "test",
					"device":   dev,
					"families": []string{"qwen3_tts", "irodori_tts"},
				},
			})
		case "models.load":
			model, _ := m.Params["model"].(string)
			sr := 24000
			langs := []string{"zh", "en", "ja"}
			if strings.Contains(strings.ToLower(model), "irodori") {
				sr = 48000
				langs = []string{"ja"}
			}
			loaded[model] = "ok"
			writeJSON(map[string]any{
				"id": m.ID,
				"result": map[string]any{
					"model":      model,
					"task":       "tts",
					"sampleRate": sr,
					"seconds":    0.05,
					"bytes":      128 << 20,
					"languages":  langs,
				},
			})
		case "models.unload":
			model, _ := m.Params["model"].(string)
			delete(loaded, model)
			writeJSON(map[string]any{
				"id":     m.ID,
				"result": map[string]any{"unloaded": []string{model}},
			})
		case "tts.synthesize":
			model, _ := m.Params["model"].(string)
			if _, ok := loaded[model]; !ok {
				writeJSON(map[string]any{
					"id":    m.ID,
					"error": map[string]any{"code": "not_loaded", "message": "model not loaded"},
				})
				continue
			}
			job, _ := m.Params["job"].(string)
			outMap, _ := m.Params["output"].(map[string]any)
			outPath, _ := outMap["path"].(string)
			writeJSON(map[string]any{
				"method": "job.progress",
				"params": map[string]any{"job": job, "stage": "generating", "fraction": 0.5},
			})
			_ = os.MkdirAll(filepath.Dir(outPath), 0o755)
			_ = os.WriteFile(outPath, []byte("RIFFfakewav"), 0o644)
			sr := 24000
			if strings.Contains(strings.ToLower(model), "irodori") {
				sr = 48000
			}
			writeJSON(map[string]any{
				"id": m.ID,
				"result": map[string]any{
					"output":       outPath,
					"model":        model,
					"sampleRate":   sr,
					"channels":     1,
					"frames":       sr / 2,
					"audioSeconds": 0.5,
					"seconds":      0.1,
					"rtf":          5.0,
				},
			})
		case "tts.encodeVoice":
			promptPath, _ := m.Params["promptPath"].(string)
			model, _ := m.Params["model"].(string)
			_ = os.MkdirAll(filepath.Dir(promptPath), 0o755)
			_ = os.WriteFile(promptPath, []byte("{\"ok\":true}"), 0o644)
			writeJSON(map[string]any{
				"id": m.ID,
				"result": map[string]any{
					"voicePrompt": promptPath,
					"model":       model,
					"format":      "pyworker/1",
					"bytes":       11,
				},
			})
		case "engine.shutdown":
			writeJSON(map[string]any{"id": m.ID, "result": map[string]any{}})
			return
		default:
			writeJSON(map[string]any{
				"id":    m.ID,
				"error": map[string]any{"code": "method_not_found", "message": "unknown: " + m.Method},
			})
		}
	}
}
`
	if err := os.WriteFile(src, []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-o", bin, src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake worker: %v\n%s", err, out)
	}
	return bin
}

func TestClientProtocolV2Roundtrip(t *testing.T) {
	fakePy := buildFakeWorker(t)
	c := New(Config{PythonPath: fakePy, Device: "directml"})
	defer c.Close()

	ctx := context.Background()
	hello, err := c.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if hello.Protocol != proto.Version || hello.Device != "directml" {
		t.Fatalf("unexpected hello: %+v", hello)
	}
	if c.Device() != "directml" {
		t.Fatalf("Device() = %q, want directml", c.Device())
	}

	// Load Qwen3-TTS.
	var loadRes proto.LoadResult
	if err := c.Call(ctx, proto.MethodModelsLoad, proto.LoadParams{
		Model: "Qwen/Qwen3-TTS-12Hz-0.6B-CustomVoice",
		Task:  "tts",
	}, &loadRes, nil); err != nil {
		t.Fatalf("models.load: %v", err)
	}
	if loadRes.SampleRate != 24000 {
		t.Fatalf("sampleRate = %d, want 24000", loadRes.SampleRate)
	}

	// Synthesize with progress notification forwarding.
	outWAV := filepath.Join(t.TempDir(), "qwen3.wav")
	var progressCount int
	var synthRes proto.SynthesizeResult
	err = c.Call(ctx, proto.MethodTTSSynthesize, map[string]any{
		"job":      "job-1",
		"model":    "Qwen/Qwen3-TTS-12Hz-0.6B-CustomVoice",
		"text":     "你好世界",
		"voice":    "Vivian",
		"language": "zh",
		"output":   map[string]any{"path": outWAV},
	}, &synthRes, func(method string, params json.RawMessage) {
		if method == proto.MethodJobProgress {
			progressCount++
		}
	})
	if err != nil {
		t.Fatalf("tts.synthesize: %v", err)
	}
	if progressCount != 1 {
		t.Fatalf("progress notifications = %d, want 1", progressCount)
	}
	if synthRes.Output != outWAV || synthRes.SampleRate != 24000 {
		t.Fatalf("unexpected synthRes: %+v", synthRes)
	}
	if _, err := os.Stat(outWAV); err != nil {
		t.Fatalf("output WAV missing: %v", err)
	}

	// EncodeVoice.
	promptPath := filepath.Join(t.TempDir(), "voice.json")
	var encRes proto.EncodeVoiceResult
	if err := c.Call(ctx, proto.MethodTTSEncodeVoice, proto.EncodeVoiceParams{
		Model:      "Qwen/Qwen3-TTS-12Hz-1.7B-Base",
		PromptPath: promptPath,
		RefAudio:   "ref.wav",
	}, &encRes, nil); err != nil {
		t.Fatalf("tts.encodeVoice: %v", err)
	}
	if encRes.Format != "pyworker/1" {
		t.Fatalf("unexpected encode result: %+v", encRes)
	}

	// Unload & verify protocol error returned cleanly on unloaded synth.
	if err := c.Call(ctx, proto.MethodModelsUnload, proto.UnloadParams{
		Model: "Qwen/Qwen3-TTS-12Hz-0.6B-CustomVoice",
	}, nil, nil); err != nil {
		t.Fatalf("models.unload: %v", err)
	}
	err = c.Call(ctx, proto.MethodTTSSynthesize, map[string]any{
		"model":  "Qwen/Qwen3-TTS-12Hz-0.6B-CustomVoice",
		"text":   "test",
		"output": map[string]any{"path": outWAV},
	}, &synthRes, nil)
	var pe *proto.Error
	if !errors.As(err, &pe) || pe.Code != "not_loaded" {
		t.Fatalf("expected proto not_loaded error, got %v", err)
	}
}

func TestClientNotInstalled(t *testing.T) {
	c := New(Config{})
	if _, err := c.Start(context.Background()); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("expected ErrNotInstalled, got %v", err)
	}
}

// TestEmbeddedWorkerPython exercises the real embedded worker.py when python3
// is present on the host (testing handshake, encodeVoice, unload, and
// structured error handling).
func TestEmbeddedWorkerPython(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available on PATH")
	}
	c := New(Config{PythonPath: py, Device: "cpu"})
	defer c.Close()

	ctx := context.Background()
	hello, err := c.Start(ctx)
	if err != nil {
		t.Fatalf("Start(worker.py): %v", err)
	}
	if hello.Protocol != proto.Version || hello.Role != "pyworker" {
		t.Fatalf("unexpected hello: %+v", hello)
	}

	// EncodeVoice works with pure stdlib Python.
	promptPath := filepath.Join(t.TempDir(), "irodori.json")
	var encRes proto.EncodeVoiceResult
	if err := c.Call(ctx, proto.MethodTTSEncodeVoice, proto.EncodeVoiceParams{
		Model:      "Aratako/Irodori-TTS-v4.1-Small-MF",
		PromptPath: promptPath,
		RefAudio:   "sample.wav",
	}, &encRes, nil); err != nil {
		t.Fatalf("encodeVoice: %v", err)
	}
	if encRes.Format != "pyworker/1" || encRes.Bytes == 0 {
		t.Fatalf("unexpected encRes: %+v", encRes)
	}

	// Loading an unknown family returns a clean structured proto.Error.
	var loadRes proto.LoadResult
	err = c.Call(ctx, proto.MethodModelsLoad, proto.LoadParams{
		Model: "unknown/model",
		Task:  "tts",
	}, &loadRes, nil)
	var pe *proto.Error
	if !errors.As(err, &pe) || pe.Code != proto.CodeUnsupported {
		t.Fatalf("expected unsupported proto.Error, got %v", err)
	}
	if !strings.Contains(pe.Message, "PyTorch") && !strings.Contains(pe.Message, "does not support") {
		t.Fatalf("unexpected error message: %q", pe.Message)
	}
}
