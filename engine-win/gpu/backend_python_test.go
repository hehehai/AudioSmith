package gpu

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hehehai/AudioSmith/engine-win/hw"
	"github.com/hehehai/AudioSmith/engine-win/internal/pyworker"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

type captureNotifier struct {
	methods []string
}

func (c *captureNotifier) Notify(method string, _ any) error {
	c.methods = append(c.methods, method)
	return nil
}

func buildFakePythonBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	bin := filepath.Join(dir, "python")
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

type req struct {
	ID     string         ` + "`json:\"id,omitempty\"`" + `
	Method string         ` + "`json:\"method,omitempty\"`" + `
	Params map[string]any ` + "`json:\"params,omitempty\"`" + `
}

func emit(v any) {
	b, _ := json.Marshal(v)
	fmt.Println(string(b))
}

func main() {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r req
		if json.Unmarshal([]byte(line), &r) != nil {
			continue
		}
		switch r.Method {
		case "engine.hello":
			emit(map[string]any{"id": r.ID, "result": map[string]any{
				"protocol": 2, "role": "pyworker", "version": "test", "device": "cpu",
				"families": []string{"qwen3_tts", "irodori_tts"},
			}})
		case "models.load":
			model, _ := r.Params["model"].(string)
			sr := 24000
			if strings.Contains(strings.ToLower(model), "irodori") {
				sr = 48000
			}
			emit(map[string]any{"id": r.ID, "result": map[string]any{
				"model": model, "task": "tts", "sampleRate": sr, "seconds": 0.01, "bytes": 64 << 20,
			}})
		case "models.unload":
			model, _ := r.Params["model"].(string)
			emit(map[string]any{"id": r.ID, "result": map[string]any{"unloaded": []string{model}}})
		case "tts.synthesize":
			model, _ := r.Params["model"].(string)
			job, _ := r.Params["job"].(string)
			outMap, _ := r.Params["output"].(map[string]any)
			outPath, _ := outMap["path"].(string)
			emit(map[string]any{"method": "job.progress", "params": map[string]any{"job": job, "stage": "generating", "fraction": 0.5}})
			_ = os.MkdirAll(filepath.Dir(outPath), 0o755)
			_ = os.WriteFile(outPath, []byte("RIFFfake"), 0o644)
			sr := 24000
			if strings.Contains(strings.ToLower(model), "irodori") {
				sr = 48000
			}
			emit(map[string]any{"id": r.ID, "result": map[string]any{
				"output": outPath, "model": model, "sampleRate": sr, "channels": 1,
				"frames": sr / 4, "audioSeconds": 0.25, "seconds": 0.05, "rtf": 5.0,
			}})
		case "tts.encodeVoice":
			promptPath, _ := r.Params["promptPath"].(string)
			model, _ := r.Params["model"].(string)
			emit(map[string]any{"id": r.ID, "result": map[string]any{
				"voicePrompt": promptPath, "model": model, "format": "pyworker/1", "bytes": 32,
			}})
		case "engine.shutdown":
			emit(map[string]any{"id": r.ID, "result": map[string]any{}})
			return
		}
	}
}
`
	if err := os.WriteFile(src, []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-o", bin, src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake python: %v\n%s", err, out)
	}
	return bin
}

func TestPythonBackendNotInstalledReturnsActionableError(t *testing.T) {
	t.Setenv("AUDIOSMITH_PY_WORKER", "")
	t.Setenv("AUDIOSMITH_PYTHON", "")
	t.Setenv("AUDIOSMITH_RUNTIMES_DIR", t.TempDir())

	pb := NewPythonBackend(hw.Info{GPUTier: hw.TierDirectML})
	if pb.Available() {
		t.Fatal("expected PythonBackend unavailable when no runtime is installed")
	}
	_, err := pb.Load(context.Background(), proto.LoadParams{
		Model: "Qwen/Qwen3-TTS-12Hz-0.6B-CustomVoice",
		Task:  "tts",
	})
	var pe *proto.Error
	if !errors.As(err, &pe) || pe.Code != proto.CodeUnsupported {
		t.Fatalf("expected unsupported proto.Error, got %v", err)
	}
	if !strings.Contains(pe.Message, "optional Python/PyTorch worker") || !strings.Contains(pe.Message, "AUDIOSMITH_PY_WORKER") {
		t.Fatalf("error message not actionable: %q", pe.Message)
	}

	// Router also surfaces the actionable error for Qwen3-TTS when Python is absent.
	svc := New(hw.Info{GPUTier: hw.TierDirectML}, pb)
	_, err = svc.route("tts", "Qwen/Qwen3-TTS-12Hz-0.6B-CustomVoice", "")
	if !errors.As(err, &pe) || pe.Code != proto.CodeUnsupported || !strings.Contains(pe.Message, "optional Python/PyTorch worker") {
		t.Fatalf("router error = %v", err)
	}
}

func TestPythonBackendRoundtripQwenAndIrodori(t *testing.T) {
	fakePy := buildFakePythonBinary(t)
	pb := NewPythonBackendWithConfig(hw.Info{GPUTier: hw.TierDirectML}, pyworker.Config{
		PythonPath: fakePy,
		Device:     "cpu",
	})
	defer pb.Close()

	if !pb.Available() {
		t.Fatal("expected PythonBackend available with fake python binary")
	}

	ctx := context.Background()
	for _, tc := range []struct {
		model  string
		wantSR int
	}{
		{"Qwen/Qwen3-TTS-12Hz-0.6B-CustomVoice", 24000},
		{"Aratako/Irodori-TTS-v4.1-Small-MF", 48000},
	} {
		lr, err := pb.Load(ctx, proto.LoadParams{Model: tc.model, Task: "tts"})
		if err != nil {
			t.Fatalf("Load(%s): %v", tc.model, err)
		}
		if lr.SampleRate != tc.wantSR {
			t.Errorf("Load(%s) sampleRate = %d, want %d", tc.model, lr.SampleRate, tc.wantSR)
		}

		outWAV := filepath.Join(t.TempDir(), "out.wav")
		n := &captureNotifier{}
		sr, err := pb.Synthesize(ctx, "job-42", proto.SynthesizeParams{
			Model:  tc.model,
			Text:   "こんにちは",
			Output: proto.Output{Path: outWAV},
		}, n)
		if err != nil {
			t.Fatalf("Synthesize(%s): %v", tc.model, err)
		}
		if sr.SampleRate != tc.wantSR || sr.Output != outWAV {
			t.Errorf("Synthesize(%s) = %+v", tc.model, sr)
		}
		if len(n.methods) != 1 || n.methods[0] != proto.MethodJobProgress {
			t.Errorf("notifications = %v, want [job.progress]", n.methods)
		}
		if err := pb.Unload(ctx, tc.model); err != nil {
			t.Fatalf("Unload(%s): %v", tc.model, err)
		}
	}
}
