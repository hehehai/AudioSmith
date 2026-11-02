package watermark

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMessage(t *testing.T) {
	m := Message(0x3C)
	if m != 0xA53C {
		t.Fatalf("%x", m)
	}
	if ours, id := Decode(m); !ours || id != 0x3C {
		t.Fatal(ours, id)
	}
	if ours, _ := Decode(0x1234); ours {
		t.Fatal("not ours")
	}
}

// A conversion of another revision goes (code review 1102 L6); folders
// outside audiosmith-converted are left alone.
func TestDropOtherRevisions(t *testing.T) {
	cache := t.TempDir()
	old, cur := ConvertedDir(cache, "facebook/audioseal", "r1"), ConvertedDir(cache, "facebook/audioseal", "r2")
	other := ConvertedDir(cache, "someone/else", "r1")
	for _, d := range []string{old, cur, other} {
		os.MkdirAll(d, 0o755)
	}
	dropOtherRevisions(cur)
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("the old revision stayed")
	}
	for _, d := range []string{cur, other} {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("%s went", d)
		}
	}
	stray := filepath.Join(cache, "x", "y")
	os.MkdirAll(filepath.Join(cache, "x", "z"), 0o755)
	dropOtherRevisions(stray)
	if _, err := os.Stat(filepath.Join(cache, "x", "z")); err != nil {
		t.Error("a folder outside audiosmith-converted went")
	}
}
