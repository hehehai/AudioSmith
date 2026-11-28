package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hehehai/AudioSmith/internal/winruntime"
)

func buildFakeRuntimeZip(t *testing.T, files map[string][]byte) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	raw := buf.Bytes()
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:])
}

func useFakeWinRuntimeServer(t *testing.T) (*winruntime.Manager, *atomic.Int32) {
	t.Helper()
	ortZip, ortSHA := buildFakeRuntimeZip(t, map[string][]byte{
		"runtimes/win-x64/native/onnxruntime.dll": []byte("fake-ort-dll"),
		"runtimes/win-x64/native/DirectML.dll":    []byte("fake-dml-dll"),
	})
	whisperZip, whisperSHA := buildFakeRuntimeZip(t, map[string][]byte{
		"Release/whisper-cli.exe": []byte("fake-whisper-exe"),
	})
	llamaZip, llamaSHA := buildFakeRuntimeZip(t, map[string][]byte{
		"llama-server.exe": []byte("fake-llama-exe"),
	})

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/ort.zip":
			_, _ = w.Write(ortZip)
		case "/whisper.zip":
			_, _ = w.Write(whisperZip)
		case "/llama.zip":
			_, _ = w.Write(llamaZip)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	cat := winruntime.DefaultCatalog()
	setAsset := func(name winruntime.Name, path, sha string) {
		spec := cat[name]
		asset := spec.Assets["amd64"]
		asset.URL = srv.URL + path
		asset.SHA256 = sha
		spec.Assets["amd64"] = asset
		cat[name] = spec
	}
	setAsset(winruntime.RuntimeONNX, "/ort.zip", ortSHA)
	setAsset(winruntime.RuntimeWhisper, "/whisper.zip", whisperSHA)
	setAsset(winruntime.RuntimeLlama, "/llama.zip", llamaSHA)

	mgr := winruntime.New(winruntime.Options{
		BaseDir: t.TempDir(),
		Catalog: cat,
		GOARCH:  "amd64",
	})

	prevNew := newWinRuntimeManager
	prevPy := discoverPythonRuntime
	newWinRuntimeManager = func(string) *winruntime.Manager { return mgr }
	discoverPythonRuntime = func() string { return "" }
	t.Cleanup(func() {
		newWinRuntimeManager = prevNew
		discoverPythonRuntime = prevPy
	})
	return mgr, &hits
}

func TestWindowsRuntimesSectionUI(t *testing.T) {
	simulateWindows(t)
	mgr, hits := useFakeWinRuntimeServer(t)

	a, _, tt := newTestApp(t)
	useFakeHub(t, a)
	openModels(a)
	tt.SetSize(1100, 2400)
	tt.Frame()

	texts := strings.Join(tt.Texts(), "|")
	for _, want := range []string{
		"Windows runtimes",
		"ONNX Runtime (DirectML + CPU)",
		"whisper.cpp",
		"llama.cpp (Vulkan + CPU)",
		"Python worker (optional)",
		"Optional · only needed for Qwen3-TTS and Irodori-TTS",
	} {
		if !strings.Contains(texts, want) {
			t.Fatalf("Windows runtimes section missing %q in %s", want, texts)
		}
	}

	// Click Download on ONNX Runtime.
	if err := tt.Click("Download ONNX Runtime (DirectML + CPU)"); err != nil {
		t.Fatalf("click download ONNX: %v", err)
	}
	tt.Frame()
	if !mgr.Status(winruntime.RuntimeONNX).Installed {
		t.Fatal("expected ONNX runtime installed after clicking Download")
	}
	if hits.Load() != 1 {
		t.Fatalf("expected 1 HTTP request, got %d", hits.Load())
	}
	if !strings.Contains(strings.Join(tt.Texts(), "|"), "Installed · v1.24.4") {
		t.Fatalf("expected Installed status after download: %q", tt.Texts())
	}

	// Click Remove on ONNX Runtime.
	if err := tt.Click("Remove ONNX Runtime (DirectML + CPU)"); err != nil {
		t.Fatalf("click remove ONNX: %v", err)
	}
	tt.Frame()
	if mgr.Status(winruntime.RuntimeONNX).Installed {
		t.Fatal("expected ONNX runtime removed after clicking Remove")
	}
}

func TestWindowsModelDownloadAutoEnsuresNativeRuntime(t *testing.T) {
	simulateWindows(t)
	mgr, hits := useFakeWinRuntimeServer(t)

	a, _, tt := newTestApp(t)
	useFakeHub(t, a)
	openModels(a)
	tt.Frame()

	for _, tc := range []struct {
		modelID string
		wantRT  winruntime.Name
		wantHit int32
	}{
		{"kokoro-82m-bf16", winruntime.RuntimeONNX, 1},
		{"whisper-large-v3-turbo-4bit", winruntime.RuntimeWhisper, 2},
		{"qwen3-4b-instruct-2507-4bit", winruntime.RuntimeLlama, 3},
	} {
		if mgr.Status(tc.wantRT).Installed {
			t.Fatalf("%s: expected runtime %s uninstalled initially", tc.modelID, tc.wantRT)
		}
		e := entry(t, tc.modelID)
		a.download(e)
		tt.Frame()

		if !mgr.Status(tc.wantRT).Installed {
			t.Fatalf("%s: expected download to auto-install runtime %s", tc.modelID, tc.wantRT)
		}
		if hits.Load() != tc.wantHit {
			t.Fatalf("%s: expected %d runtime download hits, got %d", tc.modelID, tc.wantHit, hits.Load())
		}
		m := a.models[e.ID]
		if m == nil || m.jobID == "" {
			t.Fatalf("%s: expected model download job submitted after runtime install: %+v", tc.modelID, m)
		}
	}
}

func TestWindowsPyTorchModelNeverForcesPythonDownload(t *testing.T) {
	simulateWindows(t)
	_, hits := useFakeWinRuntimeServer(t)

	a, _, tt := newTestApp(t)
	useFakeHub(t, a)
	openModels(a)
	tt.Frame()

	qwenTTS := entry(t, "qwen3-tts-0.6b-customvoice-8bit")
	a.download(qwenTTS)
	beforeFrameToasts := strings.Join(a.toasts, "|")
	tt.Frame()

	if hits.Load() != 0 {
		t.Fatalf("PyTorch model must not trigger native/Python runtime HTTP download, got %d hits", hits.Load())
	}
	all := beforeFrameToasts + "|" + strings.Join(a.toasts, "|") + "|" + strings.Join(tt.Texts(), "|")
	if !strings.Contains(all, "optional Python worker") {
		t.Errorf("expected gentle toast mentioning optional Python worker, got %q", all)
	}
}

func TestWindowsRuntimesHiddenOnMacOS(t *testing.T) {
	simulateDarwin(t)
	a, _, tt := newTestApp(t)
	useFakeHub(t, a)
	openModels(a)
	tt.Frame()

	if strings.Contains(strings.Join(tt.Texts(), "|"), "Windows runtimes") {
		t.Fatal("Windows runtimes section must never appear on macOS")
	}
}
