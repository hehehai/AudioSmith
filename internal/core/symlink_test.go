package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSymlinkOrFallbackFileTiers(t *testing.T) {
	t.Run("normal symlink", func(t *testing.T) {
		dir := t.TempDir()
		blob := filepath.Join(dir, "blobs", "abc")
		if err := os.MkdirAll(filepath.Dir(blob), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(blob, []byte("weights"), 0o644); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "snapshots", "rev1", "model.bin")
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := SymlinkOrFallback("../../blobs/abc", link); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(link)
		if err != nil || string(got) != "weights" {
			t.Fatalf("got %q err %v", got, err)
		}
	})

	t.Run("hardlink fallback when symlink denied", func(t *testing.T) {
		saved := trySymlink
		t.Cleanup(func() { trySymlink = saved })
		trySymlink = func(string, string) error { return os.ErrPermission }

		dir := t.TempDir()
		blob := filepath.Join(dir, "blobs", "abc")
		if err := os.MkdirAll(filepath.Dir(blob), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(blob, []byte("hardlink-data"), 0o644); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "snapshots", "rev1", "model.bin")
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := SymlinkOrFallback("../../blobs/abc", link); err != nil {
			t.Fatal(err)
		}
		st1, err := os.Stat(blob)
		if err != nil {
			t.Fatal(err)
		}
		st2, err := os.Stat(link)
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(st1, st2) {
			t.Error("expected hardlink to reference the same underlying file")
		}
	})

	t.Run("copy fallback when symlink and hardlink denied", func(t *testing.T) {
		savedSym, savedHard := trySymlink, tryHardlink
		t.Cleanup(func() { trySymlink, tryHardlink = savedSym, savedHard })
		trySymlink = func(string, string) error { return os.ErrPermission }
		tryHardlink = func(string, string) error { return os.ErrPermission }

		dir := t.TempDir()
		blob := filepath.Join(dir, "blobs", "abc")
		if err := os.MkdirAll(filepath.Dir(blob), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(blob, []byte("copied-data"), 0o644); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "snapshots", "rev1", "model.bin")
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := SymlinkOrFallback("../../blobs/abc", link); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(link)
		if err != nil || string(got) != "copied-data" {
			t.Fatalf("got %q err %v", got, err)
		}
	})

	t.Run("directory copy fallback when symlink and junction denied", func(t *testing.T) {
		savedSym, savedDir := trySymlink, tryDirLink
		t.Cleanup(func() { trySymlink, tryDirLink = savedSym, savedDir })
		trySymlink = func(string, string) error { return os.ErrPermission }
		tryDirLink = func(string, string) error { return os.ErrPermission }

		dir := t.TempDir()
		srcDir := filepath.Join(dir, "src")
		if err := os.MkdirAll(filepath.Join(srcDir, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(srcDir, "sub", "a.txt"), []byte("nested"), 0o644); err != nil {
			t.Fatal(err)
		}
		dstDir := filepath.Join(dir, "dst")
		if err := SymlinkOrFallback(srcDir, dstDir); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dstDir, "sub", "a.txt"))
		if err != nil || string(got) != "nested" {
			t.Fatalf("got %q err %v", got, err)
		}
	})
}
