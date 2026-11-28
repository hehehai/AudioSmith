package hub

// The Client against repos: deleting, pinned revisions, the mirror race,
// gated and missing repos, the disk preflight and auxiliary repos. The
// fake hub is in client_test.go.

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/catalog"
)

func TestDelete(t *testing.T) {
	h := newFakeHub(t)
	c := h.client(t, nil)
	ctx := context.Background()
	if err := c.Download(ctx, h.entry(), nil); err != nil {
		t.Fatal(err)
	}
	repo := RepoDir(c.opts.CacheDir, testRepo)
	// Another snapshot (an older revision) shares config.json's blob and
	// keeps it.
	l, _ := c.Status(ctx, h.entry())
	shared, _ := os.Readlink(filepath.Join(l.Snapshot, "config.json"))
	old := filepath.Join(repo, "snapshots", strings.Repeat("f", 40))
	os.MkdirAll(old, 0o755)
	os.Symlink(shared, filepath.Join(old, "config.json"))
	// The engine's copy of the repo goes with the repo's last snapshot
	// (spec §7.8: it belongs to the repo, which other apps' engines read).
	cp := EngineCopy(c.opts.CacheDir, testRepo)
	os.MkdirAll(cp, 0o755)
	os.WriteFile(filepath.Join(cp, "config.json"), []byte("{}"), 0o644)
	if err := c.Delete(ctx, h.entry()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cp); err != nil {
		t.Errorf("engine copy gone with a snapshot left: %v", err)
	}
	blobs, _ := os.ReadDir(filepath.Join(repo, "blobs"))
	if len(blobs) != 1 || blobs[0].Name() != filepath.Base(shared) {
		t.Errorf("blobs left: %v", blobs)
	}
	if _, err := os.Stat(filepath.Join(repo, "refs", "main")); err == nil {
		t.Error("refs/main left")
	}
	if l, _ := c.Status(ctx, h.entry()); l.State != Partial {
		t.Errorf("with an older snapshot left: %+v", l)
	}
	// Without other snapshots the repo directory goes.
	os.RemoveAll(old)
	if err := c.Delete(ctx, h.entry()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(repo); !os.IsNotExist(err) {
		t.Errorf("repo dir left: %v", err)
	}
	if _, err := os.Stat(cp); !os.IsNotExist(err) {
		t.Errorf("engine copy left: %v", err)
	}
	if l, _ := c.Status(ctx, h.entry()); l.State != Missing {
		t.Errorf("after delete: %+v", l)
	}
}

func TestPinnedRevision(t *testing.T) {
	h := newFakeHub(t)
	c := h.client(t, nil)
	e := h.entry()
	e.Revision = testCommit
	if err := c.Download(context.Background(), e, nil); err != nil {
		t.Fatal(err)
	}
	repo := RepoDir(c.opts.CacheDir, testRepo)
	// refs/main points at it, for tools that resolve the repo by id…
	if ref, err := os.ReadFile(filepath.Join(repo, "refs", "main")); err != nil || string(ref) != testCommit {
		t.Errorf("refs/main %q %v", ref, err)
	}
	// …unless it names another snapshot that is there.
	other := strings.Repeat("b", 40)
	os.MkdirAll(filepath.Join(repo, "snapshots", other), 0o755)
	os.WriteFile(filepath.Join(repo, "refs", "main"), []byte(other), 0o644)
	if err := c.Download(context.Background(), e, nil); err != nil {
		t.Fatal(err)
	}
	if ref, _ := os.ReadFile(filepath.Join(repo, "refs", "main")); string(ref) != other {
		t.Errorf("refs/main moved to %q", ref)
	}
	if l, _ := c.Status(context.Background(), e); l.State != Downloaded || filepath.Base(l.Snapshot) != testCommit {
		t.Errorf("status %+v", l)
	}
	// The catalogue's file list restricts the download and pins hashes.
	e2 := h.entry()
	e2.Repo = testRepo
	e2.Files = []catalog.File{{Path: "config.json"}, {Path: "model.safetensors", SHA256: strings.Repeat("0", 64)}}
	if _, err := c.Plan(context.Background(), e2); !errors.Is(err, ErrChecksum) {
		t.Errorf("a pinned hash that differs: %v", err)
	}
	e2.Files[1].SHA256 = sha(h.files["model.safetensors"])
	p, err := c.Plan(context.Background(), e2)
	if err != nil || len(p.Files) != 2 {
		t.Errorf("listed files: %+v %v", p, err)
	}
	e2.Files = append(e2.Files, catalog.File{Path: "nope.bin"})
	if _, err := c.Plan(context.Background(), e2); !errors.Is(err, ErrNotFound) {
		t.Errorf("a listed file the repo lacks: %v", err)
	}
}

func TestMirrorRace(t *testing.T) {
	official := newFakeHub(t)
	mirror := newFakeHub(t)
	official.apiDelay.Store(int64(300 * time.Millisecond))
	c := official.client(t, func(o *Options) {
		o.Endpoints = []string{official.srv.URL, mirror.srv.URL}
		o.Token = func() string { return testToken }
	})
	var last Progress
	if err := c.Download(context.Background(), official.entry(), func(p Progress) { last = p }); err != nil {
		t.Fatal(err)
	}
	if last.Mirror != mirror.srv.URL {
		t.Errorf("used %s, want the faster mirror", last.Mirror)
	}
	for _, a := range mirror.authSeen() {
		if strings.Contains(a, "Bearer") {
			t.Errorf("the mirror saw the token: %s", a)
		}
	}
	if official.count("served") != 0 || mirror.count("served") == 0 {
		t.Errorf("served: official %d mirror %d", official.count("served"), mirror.count("served"))
	}
	// The winner is remembered: the official is not asked again.
	calls := official.count("api")
	c.Plan(context.Background(), official.entry())
	if official.count("api") != calls {
		t.Error("raced again")
	}
	// When the winner goes down, race again.
	mirror.apiFail.Store(true)
	official.apiDelay.Store(int64(0))
	p, err := c.Plan(context.Background(), official.entry())
	if err != nil || p.Endpoint != official.srv.URL {
		t.Errorf("after the mirror failed: %+v %v", p, err)
	}
	// Every endpoint down.
	official.apiFail.Store(true)
	if _, err := c.Plan(context.Background(), official.entry()); !errors.Is(err, ErrOffline) {
		t.Errorf("all down: %v", err)
	}
}

func TestGated(t *testing.T) {
	official := newFakeHub(t)
	mirror := newFakeHub(t)
	official.gated, mirror.gated = true, true
	token := ""
	c := official.client(t, func(o *Options) {
		o.Endpoints = []string{mirror.srv.URL, official.srv.URL}
		o.Token = func() string { return token }
	})
	mirror.apiDelay.Store(int64(0))
	official.apiDelay.Store(int64(200 * time.Millisecond))
	if _, err := c.Plan(context.Background(), official.entry()); !errors.Is(err, ErrGated) {
		t.Fatalf("without a token: %v", err)
	}
	token = testToken
	p, err := c.Plan(context.Background(), official.entry())
	if err != nil || !p.Gated || p.Endpoint != official.srv.URL {
		t.Fatalf("with a token: %+v %v", p, err)
	}
	if err := c.Download(context.Background(), official.entry(), nil); err != nil {
		t.Fatal(err)
	}
	checkSnapshot(t, c, official)
	if mirror.count("served") != 0 {
		t.Error("a gated repo came from the mirror")
	}
	for _, a := range mirror.authSeen() {
		if strings.Contains(a, "Bearer") {
			t.Errorf("the mirror saw the token: %s", a)
		}
	}
}

func TestDiskPreflight(t *testing.T) {
	h := newFakeHub(t)
	c := h.client(t, func(o *Options) { o.ReserveBytes = 1 << 62 })
	_, err := c.Plan(context.Background(), h.entry())
	if !errors.Is(err, ErrDiskSpace) {
		t.Fatalf("plan: %v", err)
	}
	if err := c.Download(context.Background(), h.entry(), nil); !errors.Is(err, ErrDiskSpace) {
		t.Fatalf("download: %v", err)
	}
	if h.count("served") != 0 {
		t.Error("downloaded despite the preflight")
	}
	if freeBytes(t.TempDir()) <= 0 || freeBytes(filepath.Join(t.TempDir(), "not", "there")) <= 0 {
		t.Error("freeBytes")
	}
}

func TestNotFound(t *testing.T) {
	h := newFakeHub(t)
	c := h.client(t, nil)
	e := h.entry()
	e.Revision = "nope"
	if _, err := c.Plan(context.Background(), e); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown revision: %v", err)
	}
}

func TestAuxRepos(t *testing.T) {
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
	c := New(Options{CacheDir: t.TempDir(), Endpoints: []string{both.URL}, Official: both.URL, ChunkSize: 1024, ReserveBytes: -1, GOOS: "darwin"})
	c.progressEvery = 0
	e := h.entry()
	e.Aux = []catalog.Aux{{Repo: "acme/g2p"}}
	ctx := context.Background()
	p, err := c.Plan(ctx, e)
	if err != nil || p.TotalBytes != sumSizes(h.files)+sumSizes(g2p.files) || len(p.Files) != len(h.files) {
		t.Fatalf("plan %+v %v", p, err)
	}
	var last Progress
	if err := c.Download(ctx, e, func(pr Progress) {
		if pr.Bytes < last.Bytes || pr.Repo != testRepo {
			t.Errorf("progress went from %+v to %+v", last, pr)
		}
		last = pr
	}); err != nil {
		t.Fatal(err)
	}
	if last.Bytes != p.TotalBytes {
		t.Errorf("last progress %+v", last)
	}
	l, _ := c.Status(ctx, e)
	if l.State != Downloaded || l.Bytes != p.TotalBytes {
		t.Errorf("status %+v", l)
	}
	if bad, err := c.Verify(ctx, e); err != nil || len(bad) != 0 {
		t.Errorf("verify %v %v", bad, err)
	}
	// Without its aux repo the model is not usable.
	os.RemoveAll(RepoDir(c.opts.CacheDir, "acme/g2p"))
	l, _ = c.Status(ctx, e)
	if l.State != Partial || !slices.Contains(l.Missing, "acme/g2p:") {
		t.Errorf("status without aux %+v", l)
	}
	if bad, _ := c.Verify(ctx, e); !slices.Equal(bad, []string{"acme/g2p:"}) {
		t.Errorf("verify without aux %v", bad)
	}
	// Deleting the model keeps aux repos (shared, small).
	c.Download(ctx, e, nil)
	c.Delete(ctx, e)
	if _, err := os.Stat(RepoDir(c.opts.CacheDir, "acme/g2p")); err != nil {
		t.Error("aux repo deleted")
	}
}
