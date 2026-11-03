package volume

import (
	"path/filepath"
	"testing"
)

// Free and Size measure the volume of the nearest existing parent; Same
// knows a folder's volume is its own.
func TestVolume(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "not", "yet")
	free, size := Free(dir), Size(dir)
	if free <= 0 || size <= 0 || free > size*2 {
		t.Fatalf("free %d of %d", free, size)
	}
	if Free(missing) <= 0 || Size(missing) != size {
		t.Errorf("a missing folder: free %d, size %d", Free(missing), Size(missing))
	}
	if !Same(dir, missing) {
		t.Error("a folder and the one it would hold are on two volumes")
	}
	if Free("") != -1 || Size("") != 0 || Same("", dir) {
		t.Error("an empty path is measured")
	}
}
