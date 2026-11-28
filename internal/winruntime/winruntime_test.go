package winruntime

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func buildTestZip(t *testing.T, files map[string][]byte) ([]byte, string) {
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

func TestRuntimesDirPrecedence(t *testing.T) {
	t.Setenv("AUDIOSMITH_RUNTIMES_DIR", "")
	t.Setenv("ACTNOW_MODELS_DIR", "")
	t.Setenv("ACTNOW_FAMILY_DIR", "")
	t.Setenv("LOCALAPPDATA", "")

	local := filepath.Join(t.TempDir(), "LocalAppData")
	t.Setenv("LOCALAPPDATA", local)
	if got := RuntimesDir(); got != filepath.Join(local, "ActNow", "runtimes") {
		t.Fatalf("LOCALAPPDATA RuntimesDir = %q", got)
	}

	fam := filepath.Join(t.TempDir(), "ActNowFamily")
	t.Setenv("ACTNOW_FAMILY_DIR", fam)
	if got := RuntimesDir(); got != filepath.Join(fam, "runtimes") {
		t.Fatalf("ACTNOW_FAMILY_DIR RuntimesDir = %q", got)
	}

	modelsDir := filepath.Join(t.TempDir(), "isolated", "models")
	t.Setenv("ACTNOW_MODELS_DIR", modelsDir)
	if got := RuntimesDir(); got != filepath.Join(filepath.Dir(modelsDir), "runtimes") {
		t.Fatalf("ACTNOW_MODELS_DIR RuntimesDir = %q", got)
	}

	explicit := filepath.Join(t.TempDir(), "custom-runtimes")
	t.Setenv("AUDIOSMITH_RUNTIMES_DIR", explicit)
	if got := RuntimesDir(); got != explicit {
		t.Fatalf("AUDIOSMITH_RUNTIMES_DIR RuntimesDir = %q", got)
	}
}

func TestDiscoverPrecedence(t *testing.T) {
	root := t.TempDir()
	baseDir := filepath.Join(root, "runtimes")
	exeDir := filepath.Join(root, "bin")
	envFile := filepath.Join(root, "override", "onnxruntime.dll")
	exeFile := filepath.Join(exeDir, "onnxruntime.dll")
	managedFile := filepath.Join(baseDir, "onnx", "onnxruntime.dll")

	for _, f := range []string{envFile, exeFile, managedFile} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("dll"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// 1. Env override wins.
	t.Setenv("TEST_ORT_OVERRIDE", envFile)
	got := Discover(RuntimeONNX, []string{"onnxruntime.dll"}, DiscoverOptions{
		EnvVar:  "TEST_ORT_OVERRIDE",
		BaseDir: baseDir,
		ExeDir:  exeDir,
	})
	if got != envFile {
		t.Fatalf("expected env override %q, got %q", envFile, got)
	}

	// 2. Beside exe wins over managed dir.
	t.Setenv("TEST_ORT_OVERRIDE", "")
	got = Discover(RuntimeONNX, []string{"onnxruntime.dll"}, DiscoverOptions{
		EnvVar:  "TEST_ORT_OVERRIDE",
		BaseDir: baseDir,
		ExeDir:  exeDir,
	})
	if got != exeFile {
		t.Fatalf("expected exe-adjacent %q, got %q", exeFile, got)
	}

	// 3. Managed runtime dir found when exe dir has none.
	_ = os.Remove(exeFile)
	got = Discover(RuntimeONNX, []string{"onnxruntime.dll"}, DiscoverOptions{
		EnvVar:  "TEST_ORT_OVERRIDE",
		BaseDir: baseDir,
		ExeDir:  exeDir,
	})
	if got != managedFile {
		t.Fatalf("expected managed %q, got %q", managedFile, got)
	}
}

func TestManagerEnsureDownloadVerifyExtract(t *testing.T) {
	zipData, zipSHA := buildTestZip(t, map[string][]byte{
		"runtimes/win-x64/native/onnxruntime.dll":   []byte("ort-x64-binary"),
		"runtimes/win-x64/native/DirectML.dll":      []byte("dml-x64-binary"),
		"runtimes/win-arm64/native/onnxruntime.dll": []byte("ort-arm64-binary"),
	})

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Length", string(rune(0))) // let http set it via Write
		w.Header().Del("Content-Length")
		_, _ = w.Write(zipData)
	}))
	defer srv.Close()

	makeCatalog := func(sha string) map[Name]Spec {
		cat := DefaultCatalog()
		spec := cat[RuntimeONNX]
		for arch, asset := range spec.Assets {
			asset.URL = srv.URL + "/ort.nupkg"
			asset.SHA256 = sha
			spec.Assets[arch] = asset
		}
		cat[RuntimeONNX] = spec
		return cat
	}

	// 1. amd64 extracts both onnxruntime.dll and DirectML.dll.
	base := t.TempDir()
	mgr := New(Options{
		BaseDir: base,
		Catalog: makeCatalog(zipSHA),
		GOARCH:  "amd64",
	})
	if st := mgr.Status(RuntimeONNX); st.Installed {
		t.Fatalf("expected not installed initially")
	}

	var stages []string
	var maxBytes int64
	dir, err := mgr.Ensure(context.Background(), RuntimeONNX, func(p Progress) {
		stages = append(stages, p.Stage)
		if p.Bytes > maxBytes {
			maxBytes = p.Bytes
		}
	})
	if err != nil {
		t.Fatalf("Ensure(amd64): %v", err)
	}
	if maxBytes != int64(len(zipData)) {
		t.Fatalf("progress maxBytes = %d, want %d", maxBytes, len(zipData))
	}
	if len(stages) < 3 {
		t.Fatalf("expected downloading/verifying/extracting stages, got %v", stages)
	}
	ortBytes, err := os.ReadFile(filepath.Join(dir, "onnxruntime.dll"))
	if err != nil || string(ortBytes) != "ort-x64-binary" {
		t.Fatalf("onnxruntime.dll = (%q, %v)", string(ortBytes), err)
	}
	dmlBytes, err := os.ReadFile(filepath.Join(dir, "DirectML.dll"))
	if err != nil || string(dmlBytes) != "dml-x64-binary" {
		t.Fatalf("DirectML.dll = (%q, %v)", string(dmlBytes), err)
	}
	st := mgr.Status(RuntimeONNX)
	if !st.Installed || st.Arch != "amd64" || st.Version == "" {
		t.Fatalf("unexpected Status after Ensure: %+v", st)
	}

	// 2. Second Ensure is a no-op cache hit (no extra HTTP requests).
	prevHits := hits.Load()
	if _, err := mgr.Ensure(context.Background(), RuntimeONNX, nil); err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if hits.Load() != prevHits {
		t.Fatalf("expected 0 HTTP requests on cached Ensure, got %d", hits.Load()-prevHits)
	}

	// 3. arm64 succeeds even when optional DirectML.dll is absent from zip.
	armBase := t.TempDir()
	armMgr := New(Options{
		BaseDir: armBase,
		Catalog: makeCatalog(zipSHA),
		GOARCH:  "arm64",
	})
	armDir, err := armMgr.Ensure(context.Background(), RuntimeONNX, nil)
	if err != nil {
		t.Fatalf("Ensure(arm64): %v", err)
	}
	armOrt, err := os.ReadFile(filepath.Join(armDir, "onnxruntime.dll"))
	if err != nil || string(armOrt) != "ort-arm64-binary" {
		t.Fatalf("arm64 onnxruntime.dll = (%q, %v)", string(armOrt), err)
	}

	// 4. Checksum mismatch fails cleanly and leaves runtime uninstalled.
	badBase := t.TempDir()
	badMgr := New(Options{
		BaseDir: badBase,
		Catalog: makeCatalog("0000000000000000000000000000000000000000000000000000000000000000"),
		GOARCH:  "amd64",
	})
	if _, err := badMgr.Ensure(context.Background(), RuntimeONNX, nil); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("expected ErrChecksumMismatch, got %v", err)
	}
	if badMgr.Status(RuntimeONNX).Installed {
		t.Fatalf("failed checksum must not leave runtime installed")
	}

	// 5. Remove cleans up installed runtime.
	if err := mgr.Remove(RuntimeONNX); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if mgr.Status(RuntimeONNX).Installed {
		t.Fatalf("expected not installed after Remove")
	}
}

func TestRequiredForBackendAndDiscoverPython(t *testing.T) {
	cases := []struct {
		backend string
		tasks   []string
		want    Name
	}{
		{"onnx_directml", []string{"tts"}, RuntimeONNX},
		{"onnx_cpu", []string{"vad"}, RuntimeONNX},
		{"llamacpp", []string{"stt"}, RuntimeWhisper},
		{"llamacpp", []string{"llm"}, RuntimeLlama},
		{"pytorch", []string{"tts"}, ""},
		{"external", []string{"tts"}, ""},
		{"", []string{"tts"}, ""},
	}
	for _, tc := range cases {
		if got := RequiredForBackend(tc.backend, tc.tasks); got != tc.want {
			t.Errorf("RequiredForBackend(%q, %v) = %q, want %q", tc.backend, tc.tasks, got, tc.want)
		}
	}

	// OrderedNames only exposes the 3 lightweight native runtimes (never Python).
	names := OrderedNames()
	if len(names) != 3 || names[0] != RuntimeONNX || names[1] != RuntimeWhisper || names[2] != RuntimeLlama {
		t.Fatalf("OrderedNames = %v", names)
	}

	// DiscoverPython uses explicit env override or managed runtimes/python dir.
	root := t.TempDir()
	t.Setenv("AUDIOSMITH_RUNTIMES_DIR", root)
	t.Setenv("AUDIOSMITH_PY_WORKER", "")
	t.Setenv("AUDIOSMITH_PYTHON", "")
	if got := DiscoverPython(); got != "" {
		t.Fatalf("expected empty DiscoverPython when uninstalled, got %q", got)
	}
	managedPy := filepath.Join(root, "python", "python.exe")
	if err := os.MkdirAll(filepath.Dir(managedPy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedPy, []byte("py"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DiscoverPython(); got != managedPy {
		t.Fatalf("DiscoverPython managed = %q, want %q", got, managedPy)
	}
}

func TestLlamaARM64ResolutionAndGlobExtraction(t *testing.T) {
	cat := DefaultCatalog()
	spec, ok := cat[RuntimeLlama]
	if !ok {
		t.Fatal("RuntimeLlama missing from DefaultCatalog")
	}
	amd64Asset, ok := spec.Assets["amd64"]
	if !ok || amd64Asset.URL == "" {
		t.Fatalf("missing amd64 llama asset: %+v", amd64Asset)
	}
	arm64Asset, ok := spec.Assets["arm64"]
	if !ok || arm64Asset.URL == "" {
		t.Fatalf("missing arm64 llama asset: %+v", arm64Asset)
	}
	if arm64Asset.URL != amd64Asset.URL {
		t.Fatalf("arm64 llama URL = %q, want vulkan-x64 fallback %q", arm64Asset.URL, amd64Asset.URL)
	}

	zipData, zipSHA := buildTestZip(t, map[string][]byte{
		"llama-server.exe":      []byte("llama-server-bin"),
		"llama.dll":             []byte("llama-dll"),
		"ggml.dll":              []byte("ggml-dll"),
		"ggml-base.dll":         []byte("ggml-base-dll"),
		"ggml-vulkan.dll":       []byte("ggml-vulkan-dll"),
		"ggml-cpu-haswell.dll":  []byte("ggml-cpu-haswell-dll"),
		"ggml-cpu-x64.dll":      []byte("ggml-cpu-x64-dll"),
		"ggml-cpu-sse42.dll":    []byte("ggml-cpu-sse42-dll"),
		"libcurl-x64.dll":       []byte("libcurl-x64-dll"),
		"README.txt":            []byte("ignored"),
		"../ggml-evil.dll":      []byte("zip-slip-1"),
		"sub/../../libcurl.dll": []byte("zip-slip-2"),
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(zipData)
	}))
	defer srv.Close()

	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			testCat := DefaultCatalog()
			s := testCat[RuntimeLlama]
			a := s.Assets[arch]
			a.URL = srv.URL + "/llama.zip"
			a.SHA256 = zipSHA
			s.Assets[arch] = a
			testCat[RuntimeLlama] = s

			base := t.TempDir()
			mgr := New(Options{BaseDir: base, Catalog: testCat, GOARCH: arch})
			dir, err := mgr.Ensure(context.Background(), RuntimeLlama, nil)
			if err != nil {
				t.Fatalf("Ensure(%s): %v", arch, err)
			}

			wantFiles := map[string]string{
				"llama-server.exe":     "llama-server-bin",
				"llama.dll":            "llama-dll",
				"ggml.dll":             "ggml-dll",
				"ggml-base.dll":        "ggml-base-dll",
				"ggml-vulkan.dll":      "ggml-vulkan-dll",
				"ggml-cpu-haswell.dll": "ggml-cpu-haswell-dll",
				"ggml-cpu-x64.dll":     "ggml-cpu-x64-dll",
				"ggml-cpu-sse42.dll":   "ggml-cpu-sse42-dll",
				"libcurl-x64.dll":      "libcurl-x64-dll",
			}
			for name, want := range wantFiles {
				got, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil {
					t.Errorf("%s missing: %v", name, err)
					continue
				}
				if string(got) != want {
					t.Errorf("%s = %q, want %q", name, string(got), want)
				}
			}

			// Unmatched and zip-slip entries must never be extracted.
			for _, bad := range []string{
				filepath.Join(dir, "README.txt"),
				filepath.Join(dir, "ggml-evil.dll"),
				filepath.Join(base, "ggml-evil.dll"),
				filepath.Join(dir, "libcurl.dll"),
				filepath.Join(base, "libcurl.dll"),
			} {
				if _, err := os.Stat(bad); err == nil {
					t.Errorf("unexpected extracted file %q", bad)
				}
			}
		})
	}
}
