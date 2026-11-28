package engine

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEngineBinaryName(t *testing.T) {
	if got := engineBinaryName("windows"); got != "audiosmith-engine.exe" {
		t.Errorf("windows: %q", got)
	}
	if got := engineBinaryName("darwin"); got != "audiosmith-engine" {
		t.Errorf("darwin: %q", got)
	}
	if got := engineBinaryName("linux"); got != "audiosmith-engine" {
		t.Errorf("linux: %q", got)
	}
}

func TestFindEngine(t *testing.T) {
	t.Setenv("AUDIOSMITH_ENGINE", "")
	if got := Find(t.TempDir()); got != "" {
		t.Errorf("found %q in an empty directory", got)
	}
	dir := t.TempDir()
	bin := engineBinaryName(runtime.GOOS)
	if err := os.MkdirAll(filepath.Join(dir, "res", "engine"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "res", "engine", bin), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	got := Find("res")
	if !filepath.IsAbs(got) || filepath.Base(got) != bin {
		t.Fatalf("Find = %q", got)
	}
	t.Setenv("AUDIOSMITH_ENGINE", "bin/engine")
	if got := Find("res"); got != filepath.Join(dir, "bin", "engine") {
		t.Errorf("with AUDIOSMITH_ENGINE: %q", got)
	}
}
