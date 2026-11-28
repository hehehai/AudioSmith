package dub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hehehai/actnowkit/mirror"
)

// fakeSource serves body at path (a GitHub release source: github.com or
// the mirror), after delay; hits counts the requests.
type fakeSource struct {
	*httptest.Server
	hits atomic.Int32
}

func newFakeSource(t *testing.T, path string, body []byte, delay time.Duration) *fakeSource {
	t.Helper()
	s := &fakeSource{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
		w.Write(body)
	}))
	t.Cleanup(s.Close)
	return s
}

// downSource is the URL of a source that refuses connections.
func downSource(t *testing.T) string {
	t.Helper()
	s := httptest.NewServer(http.NotFoundHandler())
	u := s.URL
	s.Close()
	return u
}

func testPin(body []byte) ytdlpPin {
	sum := sha256.Sum256(body)
	return ytdlpPin{path: YtDlpPath, sha256: hex.EncodeToString(sum[:]), size: int64(len(body))}
}

func TestInstallYtDlpGitHubDown(t *testing.T) {
	body := []byte("#!/bin/sh\necho 2026.08.19\n")
	mirrorSrc := newFakeSource(t, YtDlpPath, body, 0)
	dir := filepath.Join(t.TempDir(), "tools")
	c := &mirror.Client{Sources: []string{downSource(t), mirrorSrc.URL}}
	var last, total int64
	path, from, err := installYtDlp(context.Background(), c, dir, testPin(body), func(d, tot int64) { last, total = d, tot })
	if err != nil {
		t.Fatal(err)
	}
	wantName := "yt-dlp"
	if runtime.GOOS == "windows" {
		wantName = "yt-dlp.exe"
	}
	if from != mirrorSrc.URL || path != filepath.Join(dir, wantName) {
		t.Errorf("from %s path %s", from, path)
	}
	if last != int64(len(body)) || total != int64(len(body)) {
		t.Errorf("progress %d/%d", last, total)
	}
	if fi, err := os.Stat(path); err != nil || (runtime.GOOS != "windows" && fi.Mode()&0o111 == 0) {
		t.Errorf("not executable: %v %v", fi, err)
	}
	if data, _ := os.ReadFile(path); string(data) != string(body) {
		t.Errorf("installed %q", data)
	}
}

func TestInstallYtDlpTamperedMirrorFallsBack(t *testing.T) {
	body := []byte("the real yt-dlp")
	// The mirror answers first with another file; GitHub, slower, has the
	// right one.
	mirrorSrc := newFakeSource(t, YtDlpPath, []byte("a tampered yt-dlp"), 0)
	github := newFakeSource(t, YtDlpPath, body, 300*time.Millisecond)
	dir := t.TempDir()
	c := &mirror.Client{Sources: []string{github.URL, mirrorSrc.URL}}
	path, from, err := installYtDlp(context.Background(), c, dir, testPin(body), nil)
	if err != nil {
		t.Fatal(err)
	}
	if from != github.URL || mirrorSrc.hits.Load() == 0 {
		t.Errorf("from %s (mirror asked %d times)", from, mirrorSrc.hits.Load())
	}
	if data, _ := os.ReadFile(path); string(data) != string(body) {
		t.Errorf("installed %q", data)
	}
}

func TestInstallYtDlpOnlyTampered(t *testing.T) {
	// The pinned build itself: every source serves something else.
	a := newFakeSource(t, YtDlpPath, []byte("not yt-dlp"), 0)
	b := newFakeSource(t, YtDlpPath, []byte("not yt-dlp either"), 0)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "yt-dlp"), []byte("old"), 0o755)
	_, _, err := InstallYtDlp(context.Background(), &mirror.Client{Sources: []string{a.URL, b.URL}}, dir, nil)
	var ue *URLError
	if !errors.As(err, &ue) || ue.Code != CodeChecksum || len(ue.Detail) != 64 {
		t.Fatalf("want %s, got %v", CodeChecksum, err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "yt-dlp")); string(data) != "old" {
		t.Errorf("a failed install replaced yt-dlp: %q", data)
	}
	// The temporary files go (the race's losers are removed as they end).
	deadline := time.Now().Add(2 * time.Second)
	for {
		ents, _ := os.ReadDir(dir)
		if len(ents) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("left %d files", len(ents))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestInstallYtDlpAllDown(t *testing.T) {
	_, _, err := InstallYtDlp(context.Background(), &mirror.Client{Sources: []string{downSource(t), downSource(t)}}, t.TempDir(), nil)
	var ue *URLError
	if err == nil || errors.As(err, &ue) {
		t.Fatalf("want a download error, got %v", err)
	}
}
