package hub

// The local cache: status of other tools' downloads, engine copies.

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/catalog"
)

func TestStatusOfOtherToolsCache(t *testing.T) {
	// A snapshot with only config.json (a tool fetched metadata) is not a
	// model yet.
	dir := t.TempDir()
	e := catalog.Entry{Repo: "a/b"}
	repo := RepoDir(dir, "a/b")
	os.MkdirAll(filepath.Join(repo, "blobs"), 0o755)
	os.MkdirAll(filepath.Join(repo, "snapshots", testCommit), 0o755)
	os.MkdirAll(filepath.Join(repo, "refs"), 0o755)
	os.WriteFile(filepath.Join(repo, "refs", "main"), []byte(testCommit), 0o644)
	os.WriteFile(filepath.Join(repo, "blobs", "abc"), []byte("{}"), 0o644)
	os.Symlink("../../blobs/abc", filepath.Join(repo, "snapshots", testCommit, "config.json"))
	if l, _ := StatusFor(dir, "darwin", e); l.State != Partial {
		t.Errorf("config only: %+v", l)
	}
	os.WriteFile(filepath.Join(repo, "blobs", "w"), []byte("weights"), 0o644)
	os.Symlink("../../blobs/w", filepath.Join(repo, "snapshots", testCommit, "model.safetensors"))
	if l, _ := StatusFor(dir, "darwin", e); l.State != Downloaded || l.Bytes != 9 {
		t.Errorf("with weights: %+v", l)
	}
}

// TestCleanEngineCopies: only empty copies older than ten minutes go; a
// younger one may be another family app's engine at work (spec §7.8).
func TestCleanEngineCopies(t *testing.T) {
	dir := t.TempDir()
	if got, err := CleanEngineCopies(dir); err != nil || got != nil {
		t.Fatalf("no dir: %v %v", got, err)
	}
	copies := EngineCopiesDir(dir)
	old := time.Now().Add(-11 * time.Minute)
	for _, name := range []string{"mlx-community_Empty", "mlx-community_Full", "mlx-community_Fresh"} {
		os.MkdirAll(filepath.Join(copies, name), 0o755)
	}
	os.WriteFile(filepath.Join(copies, "mlx-community_Full", "config.json"), []byte("{}"), 0o644)
	for _, name := range []string{"mlx-community_Empty", "mlx-community_Full"} {
		if err := os.Chtimes(filepath.Join(copies, name), old, old); err != nil {
			t.Fatal(err)
		}
	}
	got, err := CleanEngineCopies(dir)
	if err != nil || !slices.Equal(got, []string{"mlx-community_Empty"}) {
		t.Fatalf("removed %v %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(copies, "mlx-community_Full", "config.json")); err != nil {
		t.Error("removed a full copy")
	}
	if _, err := os.Stat(filepath.Join(copies, "mlx-community_Fresh")); err != nil {
		t.Error("removed an empty copy made a moment ago")
	}
}

// AuxSnapshots names the pinned snapshot of each auxiliary repo there,
// whatever other commits of it the folder holds (another app's).
func TestAuxSnapshots(t *testing.T) {
	cache := t.TempDir()
	const g2p, tok = "acme/g2p", "acme/tok"
	pinned, other := strings.Repeat("a", 40), strings.Repeat("b", 40)
	for _, rev := range []string{pinned, other} {
		os.MkdirAll(filepath.Join(RepoDir(cache, g2p), "snapshots", rev), 0o755)
	}
	os.MkdirAll(filepath.Join(RepoDir(cache, g2p), "refs"), 0o755)
	os.WriteFile(filepath.Join(RepoDir(cache, g2p), "refs", "main"), []byte(other), 0o644)
	e := catalog.Entry{Repo: "acme/m", Aux: []catalog.Aux{{Repo: g2p, Revision: pinned}, {Repo: tok, Revision: pinned}}}
	got := AuxSnapshotsInFor([]string{cache}, "darwin", e)
	if len(got) != 1 || got[g2p] != filepath.Join(RepoDir(cache, g2p), "snapshots", pinned) {
		t.Errorf("aux snapshots %v", got)
	}
}

// #101 M2: a move that stopped part-way leaves a model's repos in either
// folder: StatusIn and AuxSnapshotsIn find each in the folder that has it.
func TestStatusInAcrossFolders(t *testing.T) {
	put := func(dir, repo string, weights bool) {
		r := RepoDir(dir, repo)
		os.MkdirAll(filepath.Join(r, "blobs"), 0o755)
		os.MkdirAll(filepath.Join(r, "snapshots", testCommit), 0o755)
		os.MkdirAll(filepath.Join(r, "refs"), 0o755)
		os.WriteFile(filepath.Join(r, "refs", "main"), []byte(testCommit), 0o644)
		os.WriteFile(filepath.Join(r, "blobs", "c"), []byte("{}"), 0o644)
		os.Symlink("../../blobs/c", filepath.Join(r, "snapshots", testCommit, "config.json"))
		if weights {
			os.WriteFile(filepath.Join(r, "blobs", "w"), []byte("weights"), 0o644)
			os.Symlink("../../blobs/w", filepath.Join(r, "snapshots", testCommit, "model.safetensors"))
		}
	}
	from, to := t.TempDir(), t.TempDir()
	e := catalog.Entry{Repo: "acme/m", Aux: []catalog.Aux{{Repo: "acme/g2p"}}}
	put(to, "acme/m", true)      // moved
	put(from, "acme/g2p", false) // not yet
	if l, _ := StatusFor(from, "darwin", e); l.State == Downloaded {
		t.Fatal("the old folder alone has the whole model")
	}
	l, err := StatusInFor([]string{from, to}, "darwin", e)
	if err != nil || l.State != Downloaded || !strings.HasPrefix(l.Snapshot, to) {
		t.Fatalf("across folders: %+v %v", l, err)
	}
	aux := AuxSnapshotsInFor([]string{from, to}, "darwin", e)
	if !strings.HasPrefix(aux["acme/g2p"], from) {
		t.Errorf("aux %v", aux)
	}
	c := New(Options{CacheDir: from, AlsoIn: []string{to}, GOOS: "darwin"})
	if l, _ := c.Status(t.Context(), e); l.State != Downloaded {
		t.Errorf("client: %+v", l)
	}
}

// #101 L10: the clients made per use drop their idle connections.
func TestHTTPClientIdleTimeout(t *testing.T) {
	tr, ok := NewHTTPClient("direct", time.Minute).Transport.(*http.Transport)
	if !ok || tr.IdleConnTimeout <= 0 {
		t.Fatalf("transport %+v", tr)
	}
}
