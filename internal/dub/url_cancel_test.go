//go:build unix

package dub

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeLauncher is a yt-dlp stand-in shaped like the macOS build: a
// launcher that runs the real work in a child (which ignores SIGTERM,
// keeps the output open and reports progress) and waits for it. The
// child writes its pid to pidFile.
func fakeLauncher(t *testing.T) (path, pidFile string) {
	t.Helper()
	dir := t.TempDir()
	pidFile = filepath.Join(dir, "child.pid")
	path = filepath.Join(dir, "yt-dlp")
	script := `#!/bin/sh
sh -c 'trap "" TERM PIPE; echo $$ > "` + pidFile + `"; n=0; while :; do n=$((n+1000)); echo "ASP downloading $n 100000000 NA 50000 NA /tmp/x/source.f137.mp4"; sleep 0.05; done' &
wait
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, pidFile
}

// Cancelling a download ends it at once and kills what yt-dlp started:
// the macOS build's launcher alone was killed before, its child kept
// downloading and holding the pipe, and the job never ended.
func TestDownloadCancelKillsTheWholeGroup(t *testing.T) {
	bin, pidFile := fakeLauncher(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan DownloadProgress, 100)
	done := make(chan error, 1)
	go func() {
		_, _, err := YtDlp{Path: bin}.Download(ctx, "https://example.com/v", filepath.Join(t.TempDir(), "dl"), DownloadOptions{}, func(p DownloadProgress) {
			select {
			case got <- p:
			default:
			}
		})
		done <- err
	}()
	select {
	case p := <-got:
		if p.Fraction <= 0 || p.Total != 100000000 {
			t.Errorf("progress %+v", p)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no progress")
	}
	cancel()
	at := time.Now()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err %v, want cancelled", err)
		}
		if d := time.Since(at); d > time.Second {
			t.Errorf("returned %v after the cancel: the pipes stayed open", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Download did not return after the cancel")
	}
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	deadline := time.Now().Add(time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("yt-dlp's child %d still runs", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
