package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hehehai/AudioSmith/internal/settings"
)

// TestSaveNoPlantedSymlink (code review 11-02, N2): saving a stem and the
// diagnostic bundle into a shared folder does not follow a symlink
// planted at the old temporary names.
func TestSaveNoPlantedSymlink(t *testing.T) {
	shared, home := t.TempDir(), t.TempDir()
	victim := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(victim, []byte("safe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "vocals.wav")
	if err := os.WriteFile(src, []byte("RIFF"), 0o644); err != nil {
		t.Fatal(err)
	}
	stem, bundle := filepath.Join(shared, "vocals.wav"), filepath.Join(shared, "diag.zip")
	for _, p := range []string{stem + ".part", bundle + ".tmp"} {
		if err := os.Symlink(victim, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := copyFile(src, stem); err != nil {
		t.Fatal(err)
	}
	if err := writeBundle(bundle, "diag", settings.Settings{}, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(victim); string(got) != "safe\n" {
		t.Fatalf("followed: %q", got)
	}
	for _, p := range []string{stem, bundle} {
		if fi, err := os.Lstat(p); err != nil || !fi.Mode().IsRegular() {
			t.Errorf("%s: %v", p, err)
		}
	}
}
