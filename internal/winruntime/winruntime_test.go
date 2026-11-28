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
