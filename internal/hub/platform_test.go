package hub

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/actnowkit/models"
)

func TestResolveEntryPlatform(t *testing.T) {
	cat := catalog.Builtin()
	kokoro, ok := cat.ByID("kokoro-82m-bf16")
	if !ok {
		t.Fatal("kokoro-82m-bf16 missing from builtin catalog")
	}

	// macOS keeps MLX repo, revision, files and aux untouched.
	mac, err := ResolveEntry(kokoro, "darwin")
	if err != nil {
		t.Fatalf("ResolveEntry(darwin): %v", err)
	}
	if mac.Repo != kokoro.Repo || mac.Revision != kokoro.Revision || len(mac.Aux) != len(kokoro.Aux) {
		t.Fatalf("darwin entry altered: got repo=%s rev=%s aux=%d", mac.Repo, mac.Revision, len(mac.Aux))
	}

	// Windows resolves to ONNX repo, revision, files and drops MLX aux.
	win, err := ResolveEntry(kokoro, "windows")
	if err != nil {
		t.Fatalf("ResolveEntry(windows): %v", err)
	}
	if win.Repo != kokoro.EffectiveRepo("windows") || win.Repo == kokoro.Repo {
		t.Fatalf("windows repo = %q, want %q", win.Repo, kokoro.EffectiveRepo("windows"))
	}
	if win.Revision != kokoro.EffectiveRevision("windows") {
		t.Fatalf("windows rev = %q, want %q", win.Revision, kokoro.EffectiveRevision("windows"))
	}
	if len(win.Files) != len(kokoro.EffectiveFiles("windows")) || len(win.Files) == 0 {
		t.Fatalf("windows files = %d, want %d", len(win.Files), len(kokoro.EffectiveFiles("windows")))
	}
	if len(win.Aux) != 0 {
		t.Fatalf("windows should drop MLX aux repos, got %d", len(win.Aux))
	}

	// MLX-only entry fails cleanly on Windows.
	mlxOnly, ok := cat.ByID("moss-ttsd-v1.0-8bit")
	if !ok {
		t.Fatal("moss-ttsd-v1.0-8bit missing from builtin catalog")
	}
	if _, err := ResolveEntry(mlxOnly, "windows"); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("expected ErrUnsupportedPlatform for MLX-only entry on windows, got %v", err)
	}
	if got, err := ResolveEntry(mlxOnly, "darwin"); err != nil || got.Repo != mlxOnly.Repo {
		t.Fatalf("MLX-only entry should work on darwin, got %v / %s", err, got.Repo)
	}
}

type multiRepoHub struct {
	mu       sync.Mutex
	repos    map[string]map[string][]byte // repo -> path -> content
	commits  map[string]string            // repo -> commit sha
	requests []string
	srv      *httptest.Server
}

func newMultiRepoHub(t *testing.T, repos map[string]map[string][]byte, commits map[string]string) *multiRepoHub {
	t.Helper()
	m := &multiRepoHub{repos: repos, commits: commits}
	m.srv = httptest.NewServer(http.HandlerFunc(m.serve))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *multiRepoHub) serve(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.requests = append(m.requests, r.URL.Path)
	m.mu.Unlock()

	if rest, ok := strings.CutPrefix(r.URL.Path, "/api/models/"); ok {
		repo, rev, ok := strings.Cut(rest, "/revision/")
		if !ok {
			http.NotFound(w, r)
			return
		}
		files, exists := m.repos[repo]
		if !exists {
			http.NotFound(w, r)
			return
		}
		commit := m.commits[repo]
		if rev != "main" && rev != commit {
			http.NotFound(w, r)
			return
		}
		type lfsInfo struct {
			SHA256 string `json:"sha256"`
			Size   int64  `json:"size"`
		}
		type sib struct {
			Path   string   `json:"rfilename"`
			BlobID string   `json:"blobId"`
			Size   int64    `json:"size"`
			LFS    *lfsInfo `json:"lfs,omitempty"`
		}
		var sibs []sib
		for p, data := range files {
			if strings.HasSuffix(p, ".onnx") || strings.HasSuffix(p, ".safetensors") {
				sum := sha256.Sum256(data)
				sibs = append(sibs, sib{
					Path:   p,
					BlobID: hex.EncodeToString(sum[:]),
					Size:   int64(len(data)),
					LFS:    &lfsInfo{SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data))},
				})
			} else {
				h := sha1.New()
				fmt.Fprintf(h, "blob %d\x00", len(data))
				h.Write(data)
				sibs = append(sibs, sib{
					Path:   p,
					BlobID: hex.EncodeToString(h.Sum(nil)),
					Size:   int64(len(data)),
				})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":       repo,
			"sha":      commit,
			"gated":    false,
			"siblings": sibs,
		})
		return
	}

	// /{owner}/{name}/resolve/{commit}/{path}
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 5)
	if len(parts) == 5 && parts[2] == "resolve" {
		repo := parts[0] + "/" + parts[1]
		if files, ok := m.repos[repo]; ok {
			if data, ok := files[parts[4]]; ok {
				w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
				_, _ = w.Write(data)
				return
			}
		}
	}
	http.NotFound(w, r)
}

func TestClientWindowsVsDarwinDownloadAndCache(t *testing.T) {
	macCommit := "1111111111111111111111111111111111111111"
	auxCommit := "2222222222222222222222222222222222222222"
	winCommit := "3333333333333333333333333333333333333333"

	onnxData := []byte("fake-onnx-model-weights-0123456789")
	onnxSum := sha256.Sum256(onnxData)
	onnxSHA := hex.EncodeToString(onnxSum[:])

	entry := catalog.Entry{
		ID:       "kokoro-test",
		Repo:     "mlx-community/Kokoro-Test",
		Revision: macCommit,
		Aux: []catalog.Aux{
			{Repo: "mlx-community/Kokoro-Aux", Revision: auxCommit},
		},
		Windows: &catalog.PlatformSource{
			Available: true,
			Backend:   catalog.BackendONNXDirectML,
			Repo:      "onnx-community/Kokoro-Test-ONNX",
			Revision:  winCommit,
			Size:      int64(len(onnxData) + 12),
			Files: []catalog.File{
				{Path: "onnx/model.onnx", Size: int64(len(onnxData)), SHA256: onnxSHA},
				{Path: "config.json", Size: 12},
			},
		},
	}
	mlxOnly := catalog.Entry{
		ID:       "mlx-only-test",
		Repo:     "mlx-community/Only-MLX",
		Revision: macCommit,
		Windows:  &catalog.PlatformSource{Available: false},
	}

	srv := newMultiRepoHub(t,
		map[string]map[string][]byte{
			"mlx-community/Kokoro-Test": {
				"model.safetensors": []byte("mlx-weights-bytes"),
				"config.json":       []byte(`{"mlx":true}`),
			},
			"mlx-community/Kokoro-Aux": {
				"g2p.json": []byte(`{"aux":true}`),
			},
			"onnx-community/Kokoro-Test-ONNX": {
				"onnx/model.onnx": onnxData,
				"config.json":     []byte(`{"ok":12345}`),
				"README.md":       []byte("unrequested readme"),
			},
		},
		map[string]string{
			"mlx-community/Kokoro-Test":       macCommit,
			"mlx-community/Kokoro-Aux":        auxCommit,
			"onnx-community/Kokoro-Test-ONNX": winCommit,
		},
	)

	cacheDir := t.TempDir()
	winClient := New(Options{
		CacheDir:     cacheDir,
		Endpoints:    []string{srv.srv.URL},
		Official:     srv.srv.URL,
		ReserveBytes: -1,
		GOOS:         "windows",
	})
	macClient := New(Options{
		CacheDir:     cacheDir,
		Endpoints:    []string{srv.srv.URL},
		Official:     srv.srv.URL,
		ReserveBytes: -1,
		GOOS:         "darwin",
	})

	ctx := context.Background()

	// 1. Windows refuses MLX-only entries without touching the network.
	if _, err := winClient.Plan(ctx, mlxOnly); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("win Plan(mlxOnly) = %v, want ErrUnsupportedPlatform", err)
	}
	if err := winClient.Download(ctx, mlxOnly, nil); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("win Download(mlxOnly) = %v, want ErrUnsupportedPlatform", err)
	}
	if loc, err := winClient.Status(ctx, mlxOnly); err != nil || loc.State != Missing {
		t.Fatalf("win Status(mlxOnly) = (%+v, %v), want Missing", loc, err)
	}
	if len(srv.requests) != 0 {
		t.Fatalf("expected 0 network calls for unsupported entry, got %v", srv.requests)
	}

	// 2. Windows Plan + Download fetches ONLY the Windows ONNX repo & listed files.
	p, err := winClient.Plan(ctx, entry)
	if err != nil {
		t.Fatalf("win Plan(entry): %v", err)
	}
	if p.Revision != winCommit {
		t.Fatalf("win Plan revision = %q, want %q", p.Revision, winCommit)
	}
	if len(p.Files) != 2 {
		t.Fatalf("win Plan files = %d, want 2 (onnx/model.onnx + config.json)", len(p.Files))
	}
	var lastProg Progress
	if err := winClient.Download(ctx, entry, func(pr Progress) { lastProg = pr }); err != nil {
		t.Fatalf("win Download(entry): %v", err)
	}
	if lastProg.Repo != entry.Repo {
		t.Fatalf("progress Repo = %q, want canonical %q", lastProg.Repo, entry.Repo)
	}
	for _, req := range srv.requests {
		if strings.Contains(req, "mlx-community/") {
			t.Fatalf("Windows download must never touch MLX repos, saw %q", req)
		}
	}

	// 3. Windows Status finds the ONNX snapshot; macOS Status still reports Missing.
	winLoc, err := winClient.Status(ctx, entry)
	if err != nil || winLoc.State != Downloaded {
		t.Fatalf("win Status(entry) = (%+v, %v), want Downloaded", winLoc, err)
	}
	wantSnap := filepath.Join(RepoDir(cacheDir, "onnx-community/Kokoro-Test-ONNX"), "snapshots", winCommit)
	if winLoc.Snapshot != wantSnap {
		t.Fatalf("win Snapshot = %q, want %q", winLoc.Snapshot, wantSnap)
	}
	if _, err := os.Stat(filepath.Join(winLoc.Snapshot, "onnx", "model.onnx")); err != nil {
		t.Fatalf("expected onnx/model.onnx in snapshot: %v", err)
	}
	if macLoc, err := macClient.Status(ctx, entry); err != nil || macLoc.State != Missing {
		t.Fatalf("mac Status(entry) before mac download = (%+v, %v), want Missing", macLoc, err)
	}
	if aux := AuxSnapshotsInFor([]string{cacheDir}, "windows", entry); len(aux) != 0 {
		t.Fatalf("windows AuxSnapshots should be empty, got %v", aux)
	}

	// 4. Windows Verify passes.
	if bad, err := winClient.Verify(ctx, entry); err != nil || len(bad) != 0 {
		t.Fatalf("win Verify(entry) = (%v, %v), want clean", bad, err)
	}

	// 5. macOS Download fetches MLX repo + MLX aux repo, unaffected by Windows cache.
	if err := macClient.Download(ctx, entry, nil); err != nil {
		t.Fatalf("mac Download(entry): %v", err)
	}
	macLoc, err := macClient.Status(ctx, entry)
	if err != nil || macLoc.State != Downloaded {
		t.Fatalf("mac Status(entry) = (%+v, %v), want Downloaded", macLoc, err)
	}
	wantMacSnap := filepath.Join(RepoDir(cacheDir, "mlx-community/Kokoro-Test"), "snapshots", macCommit)
	if macLoc.Snapshot != wantMacSnap {
		t.Fatalf("mac Snapshot = %q, want %q", macLoc.Snapshot, wantMacSnap)
	}
	if aux := AuxSnapshotsInFor([]string{cacheDir}, "darwin", entry); aux["mlx-community/Kokoro-Aux"] == "" {
		t.Fatalf("darwin AuxSnapshots missing aux repo: %v", aux)
	}

	// 6. Windows Remove deletes the ONNX repo while leaving macOS MLX repo intact.
	rem, err := winClient.Remove(ctx, entry, models.ThisApp)
	if err != nil || !rem.Deleted {
		t.Fatalf("win Remove(entry) = (%+v, %v), want Deleted", rem, err)
	}
	if winLoc, _ := winClient.Status(ctx, entry); winLoc.State != Missing {
		t.Fatalf("win Status after Remove = %+v, want Missing", winLoc)
	}
	if macLoc, _ := macClient.Status(ctx, entry); macLoc.State != Downloaded {
		t.Fatalf("mac Status after win Remove = %+v, want still Downloaded", macLoc)
	}

	// 7. Job error mapping for ErrUnsupportedPlatform.
	je := downloadJobError(fmt.Errorf("foo: %w", ErrUnsupportedPlatform))
	if je.Code != CodeUnsupportedPlatform || je.Retryable {
		t.Fatalf("downloadJobError = %+v, want non-retryable %s", je, CodeUnsupportedPlatform)
	}
}
