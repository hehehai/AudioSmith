package ffmpeg

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestRunPartFile (code review 11-02, N2): ffmpeg writes to a temporary
// file of a random name it did not have to create, so a symlink planted
// at the old predictable name is not followed; the output keeps its
// extension and lands with mode 0644.
func TestRunPartFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script")
	}
	dir, home := t.TempDir(), t.TempDir()
	victim := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(victim, []byte("safe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "film.mp4")
	if err := os.Symlink(victim, filepath.Join(dir, ".film.part.mp4")); err != nil {
		t.Fatal(err)
	}
	// A stand-in ffmpeg: writes "made" to its last argument, after checking
	// it is a regular file that exists already.
	fake := filepath.Join(t.TempDir(), "ffmpeg")
	script := "#!/bin/sh\nfor a; do last=$a; done\ncase \"$last\" in *.part.mp4) ;; *) exit 3;; esac\n[ -f \"$last\" ] && [ ! -L \"$last\" ] || exit 4\necho made > \"$last\"\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := (&Tool{FFmpeg: fake}).run(context.Background(), []string{"-i", "x"}, out, 0, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(victim); string(got) != "safe\n" {
		t.Fatalf("the planted symlink was followed: %q", got)
	}
	got, _ := os.ReadFile(out)
	fi, _ := os.Stat(out)
	if strings.TrimSpace(string(got)) != "made" || fi.Mode().Perm() != 0o644 {
		t.Errorf("output %q mode %v", got, fi.Mode())
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 2 { // the output and the plant
		t.Errorf("left behind: %v", ents)
	}
}

// TestRunStartFailsLeavesNoPart (code review 1103 L2): an ffmpeg that
// cannot start (missing, not executable) leaves no empty hidden .part
// file in the output folder.
func TestRunStartFailsLeavesNoPart(t *testing.T) {
	dir := t.TempDir()
	notExec := filepath.Join(dir, "bin", "ffmpeg")
	if err := os.MkdirAll(filepath.Dir(notExec), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notExec, []byte("#!/bin/sh\n"), 0o644); err != nil { // not executable
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	for _, bin := range []string{notExec, filepath.Join(dir, "missing")} {
		if err := (&Tool{FFmpeg: bin}).run(context.Background(), []string{"-i", "x"}, filepath.Join(outDir, "a.m4a"), 0, nil); err == nil {
			t.Fatalf("%s: no error", bin)
		}
		if es, _ := os.ReadDir(outDir); len(es) != 0 {
			t.Errorf("%s: left %v", bin, es)
		}
	}
}
