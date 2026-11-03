package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEngineLogRotates: an engine log is rotated while it is written,
// once it passes its limit (code review 1102 L3: it was checked only
// when a worker started), keeping one previous file; no line is split
// or lost across a rotation.
func TestEngineLogRotates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "engine-gpu.log")
	l := openLog(path, 1000)
	if l == nil {
		t.Fatal("not opened")
	}
	line := strings.Repeat("x", 99) // 100 bytes with its newline
	for range 25 {                  // 2500 bytes: two rotations
		l.line(line)
	}
	l.line("last")
	l.Close()
	cur, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	prev, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatal(err)
	}
	if len(cur) > 1000 || len(prev) != 1000 {
		t.Errorf("sizes %d and %d, want at most 1000 and 1000", len(cur), len(prev))
	}
	if !strings.HasSuffix(string(cur), "\nlast\n") || len(cur) != 5*100+5 {
		t.Errorf("current file %d bytes, ends %q", len(cur), cur[max(0, len(cur)-10):])
	}
	for _, f := range [][]byte{cur, prev} {
		for _, ln := range strings.Split(strings.TrimSuffix(string(f), "\n"), "\n") {
			if ln != line && ln != "last" {
				t.Fatalf("split line %q", ln)
			}
		}
	}
	if _, err := os.Stat(path + ".2"); err == nil {
		t.Error("kept more than one previous file")
	}

	// A restart appends to the current file, and rotates one already
	// past the limit at once, as before.
	l = openLog(path, 1000)
	l.line("again")
	l.Close()
	if b, _ := os.ReadFile(path); !strings.HasSuffix(string(b), "last\nagain\n") {
		t.Errorf("restart did not append: %q", b[max(0, len(b)-20):])
	}
	os.WriteFile(path, []byte(strings.Repeat("y", 1500)), 0o644)
	l = openLog(path, 1000)
	l.line("fresh")
	l.Close()
	if b, _ := os.ReadFile(path); string(b) != "fresh\n" {
		t.Errorf("an oversized log was not rotated at start: %d bytes", len(b))
	}
}
