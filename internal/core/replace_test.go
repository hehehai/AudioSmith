package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceFile(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "data.txt")

	// Creating a new destination.
	src1 := filepath.Join(dir, "tmp1")
	if err := os.WriteFile(src1, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceFile(src1, dst); err != nil {
		t.Fatalf("ReplaceFile new: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "v1" {
		t.Fatalf("got %q, err %v", got, err)
	}

	// Replacing an existing destination.
	src2 := filepath.Join(dir, "tmp2")
	if err := os.WriteFile(src2, []byte("v2-updated"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceFile(src2, dst); err != nil {
		t.Fatalf("ReplaceFile existing: %v", err)
	}
	got, err = os.ReadFile(dst)
	if err != nil || string(got) != "v2-updated" {
		t.Fatalf("got %q, err %v", got, err)
	}
}
