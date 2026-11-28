package hub

// The Client against a fake Hugging Face hub (fakeHub): downloading,
// resuming, retries, checksums, verify and repair. More Client tests in
// client_repo_test.go.

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/catalog"
)

const (
	testRepo   = "acme/tiny-tts"
	testCommit = "0123456789abcdef0123456789abcdef01234567"
	testToken  = "hf_secret"
)

// fakeHub serves one repo as the Hugging Face API and resolve endpoints do,
// LFS files through a redirect to a "CDN" path, with ranges.
type fakeHub struct {
	repo  string
	files map[string][]byte
	lfs   map[string]bool
	gated bool
	// Faults.
	apiDelay atomic.Int64 // ns
	apiFail  atomic.Bool
	corrupt  map[string]bool
	cutPath  string       // abort the next response of this file…
	cutAfter atomic.Int64 // …after this many bytes (0 = off)

	srv *httptest.Server

	mu       sync.Mutex
	served   int64
	ranges   []string
	apiCalls int
	auth     []string // Authorization headers seen, by path
}

func newFakeHub(t *testing.T) *fakeHub {
	t.Helper()
	r := rand.New(rand.NewPCG(1, 2))
	big := make([]byte, 10_000+123)
	for i := range big {
		big[i] = byte(r.Uint32())
	}
	h := &fakeHub{
		repo: testRepo,
		files: map[string][]byte{
			"config.json":            []byte(`{"model_type":"tiny"}`),
			"model.safetensors":      big,
			"voices/af_heart.pt":     bytes.Repeat([]byte("voice"), 300),
			"tokenizer/vocab.txt":    []byte("a\nb\nc\n"),
			"empty.txt":              {},
			"weights/part 2.npz":     bytes.Repeat([]byte{7}, 4000),
			"README.md":              []byte("# tiny"),
			"nested/deeper/x.json":   []byte("{}"),
			"model.safetensors.json": []byte(`{"index":true}`),
		},
		lfs:     map[string]bool{"model.safetensors": true, "voices/af_heart.pt": true, "weights/part 2.npz": true},
		corrupt: map[string]bool{},
	}
	h.srv = httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(h.srv.Close)
	return h
}

func gitBlobID(b []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(b))
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// count reads a counter while requests may run.
func (h *fakeHub) count(what string) int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if what == "api" {
		return int64(h.apiCalls)
	}
	return h.served
}

func (h *fakeHub) authSeen() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.auth)
}

func (h *fakeHub) serve(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.auth = append(h.auth, r.URL.Path+" "+r.Header.Get("Authorization"))
	h.mu.Unlock()
	authed := r.Header.Get("Authorization") == "Bearer "+testToken
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/api/models/"+h.repo+"/revision/"):
		h.mu.Lock()
		h.apiCalls++
		h.mu.Unlock()
		time.Sleep(time.Duration(h.apiDelay.Load()))
		if h.apiFail.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		rev := strings.TrimPrefix(p, "/api/models/"+h.repo+"/revision/")
		if rev != "main" && rev != testCommit {
			http.NotFound(w, r)
			return
		}
		type lfs struct {
			SHA256 string `json:"sha256"`
			Size   int    `json:"size"`
		}
		type sib struct {
			Path   string `json:"rfilename"`
			BlobID string `json:"blobId"`
			Size   int    `json:"size"`
			LFS    *lfs   `json:"lfs,omitempty"`
		}
		var sibs []sib
		for _, path := range slices.Sorted(mapsKeys(h.files)) {
			b := h.files[path]
			s := sib{Path: path, BlobID: gitBlobID(b), Size: len(b)}
			if h.lfs[path] {
				s.LFS = &lfs{SHA256: sha(b), Size: len(b)}
				s.Size = len(b)
			}
			sibs = append(sibs, s)
		}
		gated := any(false)
		if h.gated {
			gated = "manual"
		}
		json.NewEncoder(w).Encode(map[string]any{"id": h.repo, "sha": testCommit, "gated": gated, "siblings": sibs})
	case strings.HasPrefix(p, "/"+h.repo+"/resolve/"+testCommit+"/"):
		if h.gated && !authed {
			http.Error(w, "gated", http.StatusUnauthorized)
			return
		}
		path := strings.TrimPrefix(p, "/"+h.repo+"/resolve/"+testCommit+"/")
		if h.lfs[path] {
			http.Redirect(w, r, "/cdn/"+h.repo+"/"+path, http.StatusFound)
			return
		}
		h.content(w, r, path)
	case strings.HasPrefix(p, "/cdn/"+h.repo+"/"):
		h.content(w, r, strings.TrimPrefix(p, "/cdn/"+h.repo+"/"))
	default:
		http.NotFound(w, r)
	}
}

func mapsKeys(m map[string][]byte) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

func (h *fakeHub) content(w http.ResponseWriter, r *http.Request, path string) {
	b, ok := h.files[path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if h.corrupt[path] {
		b = slices.Clone(b)
		b[len(b)/2] ^= 0xff
	}
	h.mu.Lock()
	if rg := r.Header.Get("Range"); rg != "" {
		h.ranges = append(h.ranges, path+" "+rg)
	}
	h.mu.Unlock()
	cw := &countingWriter{ResponseWriter: w, h: h}
	if path == h.cutPath {
		cw.cut = h.cutAfter.Swap(0)
	}
	w.Header().Set("ETag", `"`+gitBlobID(b)+`"`)
	http.ServeContent(cw, r, "", time.Time{}, bytes.NewReader(b))
}

type countingWriter struct {
	http.ResponseWriter
	h   *fakeHub
	n   int64
	cut int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	if c.cut > 0 && c.n+int64(len(p)) > c.cut {
		p = p[:c.cut-c.n]
		c.ResponseWriter.Write(p)
		c.ResponseWriter.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	}
	c.n += int64(len(p))
	c.h.mu.Lock()
	c.h.served += int64(len(p))
	c.h.mu.Unlock()
	return c.ResponseWriter.Write(p)
}

func (h *fakeHub) entry() catalog.Entry {
	return catalog.Entry{ID: "tiny", Label: "Tiny", Family: "tiny", Repo: testRepo, Tasks: []catalog.Task{catalog.TaskTTS},
		Licence: catalog.Licence{ID: "apache-2.0", Name: "Apache 2.0", Commercial: true}}
}

func (h *fakeHub) client(t *testing.T, mod func(*Options)) *Client {
	t.Helper()
	o := Options{CacheDir: t.TempDir(), Endpoints: []string{h.srv.URL}, Official: h.srv.URL, ChunkSize: 1024, ReserveBytes: -1, GOOS: "darwin"}
	if mod != nil {
		mod(&o)
	}
	c := New(o)
	c.progressEvery = 0
	return c
}

func checkSnapshot(t *testing.T, c *Client, h *fakeHub) {
	t.Helper()
	l, err := c.Status(context.Background(), h.entry())
	if err != nil || l.State != Downloaded {
		t.Fatalf("status %+v %v", l, err)
	}
	for path, want := range h.files {
		got, err := os.ReadFile(filepath.Join(l.Snapshot, filepath.FromSlash(path)))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s: %d bytes, %v", path, len(got), err)
		}
		link, _ := os.Readlink(filepath.Join(l.Snapshot, filepath.FromSlash(path)))
		if !strings.HasPrefix(filepath.ToSlash(link), "../") || filepath.IsAbs(link) {
			t.Errorf("%s links to %q, want a relative link", path, link)
		}
	}
	ref, err := os.ReadFile(filepath.Join(RepoDir(c.opts.CacheDir, testRepo), "refs", "main"))
	if err != nil || string(ref) != testCommit {
		t.Errorf("refs/main %q %v", ref, err)
	}
	blobs, _ := os.ReadDir(filepath.Join(RepoDir(c.opts.CacheDir, testRepo), "blobs"))
	for _, b := range blobs {
		if strings.Contains(b.Name(), ".incomplete") {
			t.Errorf("left %s", b.Name())
		}
	}
	// Bytes on disk: files with the same content share a blob, counted once.
	seen := map[string]bool{}
	var total int64
	for _, data := range h.files {
		if !seen[string(data)] {
			seen[string(data)] = true
			total += int64(len(data))
		}
	}
	if l.Bytes != total {
		t.Errorf("status bytes %d, want %d", l.Bytes, total)
	}
}

func sumSizes(files map[string][]byte) int64 {
	var n int64
	for _, b := range files {
		n += int64(len(b))
	}
	return n
}

func TestDownload(t *testing.T) {
	h := newFakeHub(t)
	c := h.client(t, nil)
	ctx := context.Background()
	if l, _ := c.Status(ctx, h.entry()); l.State != Missing {
		t.Fatalf("before: %+v", l)
	}
	p, err := c.Plan(ctx, h.entry())
	if err != nil || p.Revision != testCommit || len(p.Files) != len(h.files) || p.Bytes != sumSizes(h.files) || p.Bytes != p.TotalBytes {
		t.Fatalf("plan %+v %v", p, err)
	}
	var last Progress
	calls := 0
	if err := c.Download(ctx, h.entry(), func(pr Progress) { last = pr; calls++ }); err != nil {
		t.Fatal(err)
	}
	if last.Bytes != last.TotalBytes || last.TotalBytes != sumSizes(h.files) || last.Mirror != h.srv.URL || calls < 2 {
		t.Errorf("progress %+v after %d calls", last, calls)
	}
	checkSnapshot(t, c, h)
	// The big file came in ranges.
	if n := len(h.ranges); n < 9 {
		t.Errorf("only %d ranged requests: %v", n, h.ranges)
	}
	// Again: nothing to fetch.
	served := h.count("served")
	if err := c.Download(ctx, h.entry(), nil); err != nil {
		t.Fatal(err)
	}
	if h.count("served") != served {
		t.Errorf("re-download fetched %d bytes", h.count("served")-served)
	}
	if p, _ := c.Plan(ctx, h.entry()); p.Bytes != 0 {
		t.Errorf("plan after download: %d bytes to go", p.Bytes)
	}
}

// Files with the same content share a blob: it is fetched once, and both
// paths of the snapshot link to it (some repos ship identical prompts).
func TestDownloadDuplicateContent(t *testing.T) {
	h := newFakeHub(t)
	h.files["prompts/a.txt"] = []byte("the same words")
	h.files["prompts/b.txt"] = []byte("the same words")
	h.files["copy.safetensors"] = h.files["model.safetensors"]
	h.lfs["copy.safetensors"] = true
	c := h.client(t, nil)
	ctx := context.Background()
	p, err := c.Plan(ctx, h.entry())
	if err != nil || p.TotalBytes != sumSizes(h.files)-int64(len(h.files["prompts/b.txt"])+len(h.files["copy.safetensors"])) {
		t.Fatalf("plan %+v %v", p, err)
	}
	if err := c.Download(ctx, h.entry(), nil); err != nil {
		t.Fatal(err)
	}
	checkSnapshot(t, c, h)
}

func TestResume(t *testing.T) {
	h := newFakeHub(t)
	ctx := context.Background()
	// Interrupted half way through, by cancelling.
	c := h.client(t, func(o *Options) { o.Parallel = 1 })
	cctx, cancel := context.WithCancel(ctx)
	err := c.Download(cctx, h.entry(), func(p Progress) {
		if p.Bytes > p.TotalBytes/2 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled download: %v", err)
	}
	l, _ := c.Status(ctx, h.entry())
	if l.State != Partial || l.Bytes == 0 {
		t.Fatalf("after cancel: %+v", l)
	}
	p, _ := c.Plan(ctx, h.entry())
	if p.Bytes >= p.TotalBytes || p.Bytes == 0 {
		t.Fatalf("plan after cancel: %d of %d to go", p.Bytes, p.TotalBytes)
	}
	served := h.count("served")
	if err := c.Download(ctx, h.entry(), nil); err != nil {
		t.Fatal(err)
	}
	checkSnapshot(t, c, h)
	if again := h.count("served") - served; again > p.Bytes+1024 {
		t.Errorf("resumed download fetched %d bytes, plan said %d", again, p.Bytes)
	}
}

func TestConnectionDropsAreRetried(t *testing.T) {
	h := newFakeHub(t)
	c := h.client(t, func(o *Options) { o.Parallel = 1; o.ChunkSize = 1 << 20 }) // whole files
	h.cutPath = "model.safetensors"
	h.cutAfter.Store(3000)
	if err := c.Download(context.Background(), h.entry(), nil); err != nil {
		t.Fatal(err)
	}
	checkSnapshot(t, c, h)
	found := false
	for _, r := range h.ranges {
		found = found || strings.HasSuffix(r, "bytes=3000-10122") || strings.Contains(r, "bytes=3000-")
	}
	if !found {
		t.Errorf("no resumed range after the drop: %v", h.ranges)
	}
}

func TestSequentialPartialFromOtherTools(t *testing.T) {
	// huggingface_hub leaves blobs/<sha>.incomplete written in order.
	h := newFakeHub(t)
	c := h.client(t, nil)
	big := h.files["model.safetensors"]
	blobs := filepath.Join(RepoDir(c.opts.CacheDir, testRepo), "blobs")
	os.MkdirAll(blobs, 0o755)
	os.WriteFile(filepath.Join(blobs, sha(big)+".incomplete"), big[:5000], 0o644)
	p, _ := c.Plan(context.Background(), h.entry())
	if want := sumSizes(h.files) - 5000; p.Bytes != want {
		t.Errorf("plan %d, want %d", p.Bytes, want)
	}
	if err := c.Download(context.Background(), h.entry(), nil); err != nil {
		t.Fatal(err)
	}
	checkSnapshot(t, c, h)
	for _, r := range h.ranges {
		if strings.HasPrefix(r, "model.safetensors bytes=0-") {
			t.Errorf("refetched the first chunk: %v", h.ranges)
		}
	}
}

func TestChecksumMismatch(t *testing.T) {
	h := newFakeHub(t)
	h.corrupt["model.safetensors"] = true
	c := h.client(t, nil)
	err := c.Download(context.Background(), h.entry(), nil)
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("corrupt download: %v", err)
	}
	repo := RepoDir(c.opts.CacheDir, testRepo)
	if _, err := os.Stat(filepath.Join(repo, "refs", "main")); err == nil {
		t.Error("a ref was written for a bad download")
	}
	if l, _ := c.Status(context.Background(), h.entry()); l.State == Downloaded {
		t.Errorf("status %+v", l)
	}
	// Fixed upstream: the next download completes.
	h.corrupt = map[string]bool{}
	if err := c.Download(context.Background(), h.entry(), nil); err != nil {
		t.Fatal(err)
	}
	checkSnapshot(t, c, h)
}

func TestVerifyAndRepair(t *testing.T) {
	h := newFakeHub(t)
	c := h.client(t, nil)
	ctx := context.Background()
	if err := c.Download(ctx, h.entry(), nil); err != nil {
		t.Fatal(err)
	}
	bad, err := c.Verify(ctx, h.entry())
	if err != nil || len(bad) != 0 {
		t.Fatalf("verify clean: %v %v", bad, err)
	}
	// Damage one LFS blob and one small file.
	l, _ := c.Status(ctx, h.entry())
	for _, p := range []string{"model.safetensors", "config.json"} {
		blob, _ := filepath.EvalSymlinks(filepath.Join(l.Snapshot, p))
		data, _ := os.ReadFile(blob)
		data[0] ^= 1
		os.WriteFile(blob, data, 0o644)
	}
	bad, err = c.Verify(ctx, h.entry())
	slices.Sort(bad)
	if err != nil || !slices.Equal(bad, []string{"config.json", "model.safetensors"}) {
		t.Fatalf("verify damaged: %v %v", bad, err)
	}
	if l, _ := c.Status(ctx, h.entry()); l.State != Partial || len(l.Missing) != 2 {
		t.Errorf("status after verify: %+v", l)
	}
	if err := c.Download(ctx, h.entry(), nil); err != nil {
		t.Fatal(err)
	}
	checkSnapshot(t, c, h)
}
