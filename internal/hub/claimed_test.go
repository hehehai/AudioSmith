package hub

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/actnowkit/family"
	"github.com/hehehai/actnowkit/models"
)

// Options.Claimed is called for every repo a download finished (the
// auxiliary ones first), after its ref, while the download still holds
// the repo's lock exclusively: nobody can delete it before the claim, and
// models.Store.ClaimLocked (which takes no lock) works from it.
func TestClaimedInsideTheExclusiveLock(t *testing.T) {
	h := newFakeHub(t)
	g2p := newFakeHub(t)
	g2p.repo = "acme/g2p"
	g2p.files = map[string][]byte{"config.json": []byte(`{"g2p":1}`), "g2p.safetensors": bytes.Repeat([]byte{3}, 3000)}
	g2p.lfs = map[string]bool{"g2p.safetensors": true}
	both := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/acme/g2p/") {
			g2p.serve(w, r)
		} else {
			h.serve(w, r)
		}
	}))
	defer both.Close()

	cache := t.TempDir()
	store := &models.Store{Family: family.At(filepath.Join(t.TempDir(), "ActNow")), Hub: cache,
		BundleID: "dev.hehehai.audiosmith.dev", AppID: "audiosmith"}
	var mu sync.Mutex
	var got []Finished
	claimed := func(ctx context.Context, dir string, d Finished) error {
		mu.Lock()
		got = append(got, d)
		mu.Unlock()
		if dir != cache {
			t.Errorf("claimed in %s, want %s", dir, cache)
		}
		// The repo lock is held exclusively: even a shared lock (another
		// app claiming, or downloading) is refused.
		l, err := models.TryLockRepo(dir, d.Repo, family.Shared, models.Owner{BundleID: "dev.hehehai.dicta", Op: models.OpClaim})
		if !errors.Is(err, family.ErrLocked) {
			l.Release()
			t.Errorf("%s: repo lock not held while claiming: %v", d.Repo, err)
		}
		// The snapshot and its ref are in place.
		ref, err := os.ReadFile(filepath.Join(RepoDir(dir, d.Repo), "refs", "main"))
		if err != nil || string(ref) != d.Commit {
			t.Errorf("%s: ref %q %v before the claim", d.Repo, ref, err)
		}
		return store.ClaimLocked(ctx, models.Claim{Repo: d.Repo, Revision: d.Commit, Model: d.Entry.ID, Files: d.Files, Aux: d.Aux,
			Origin: models.OriginDownloaded})
	}
	c := New(Options{CacheDir: cache, Endpoints: []string{both.URL}, Official: both.URL, ChunkSize: 1024, ReserveBytes: -1, GOOS: "darwin",
		Claimed: claimed})
	c.progressEvery = 0
	e := h.entry()
	e.Aux = []catalog.Aux{{Repo: "acme/g2p", Files: []catalog.File{{Path: "config.json"}, {Path: "g2p.safetensors"}}}}
	if err := c.Download(context.Background(), e, nil); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("claimed %d repos: %+v", len(got), got)
	}
	aux, main := got[0], got[1]
	if aux.Repo != "acme/g2p" || !aux.Aux || aux.Entry.ID != "tiny" || len(aux.Files) != 2 || aux.Commit != testCommit {
		t.Errorf("aux claim %+v", aux)
	}
	if main.Repo != testRepo || main.Aux || main.Entry.ID != "tiny" || main.Files != nil || main.Commit != testCommit {
		t.Errorf("main claim %+v", main)
	}
	mine, err := store.Claims()
	if err != nil || len(mine.Claims) != 2 {
		t.Fatalf("claims file %+v %v", mine, err)
	}
	for _, cl := range mine.Claims {
		if cl.Origin != models.OriginDownloaded || cl.Model != "tiny" {
			t.Errorf("claim %+v", cl)
		}
	}
	// The lock went with the download.
	l, err := models.TryLockRepo(cache, testRepo, family.Exclusive, models.Owner{BundleID: "x", Op: models.OpDelete})
	if err != nil {
		t.Errorf("repo still locked after the download: %v", err)
	}
	l.Release()

	// A failing claim does not fail the download.
	c = New(Options{CacheDir: t.TempDir(), Endpoints: []string{both.URL}, Official: both.URL, ChunkSize: 1024, ReserveBytes: -1, GOOS: "darwin",
		Claimed: func(context.Context, string, Finished) error { return errors.New("disk full") }})
	if err := c.Download(context.Background(), h.entry(), nil); err != nil {
		t.Errorf("download failed with its claim: %v", err)
	}
}
