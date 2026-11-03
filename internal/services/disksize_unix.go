//go:build darwin || linux

package services

import (
	"path/filepath"
	"syscall"
)

// diskSize is the size of the volume of dir (or of its nearest existing
// parent), 0 when unknown.
func diskSize(dir string) int64 {
	for {
		var st syscall.Statfs_t
		if err := syscall.Statfs(dir, &st); err == nil {
			return int64(uint64(st.Blocks) * uint64(st.Bsize))
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return 0
		}
		dir = parent
	}
}

// diskFree is the space free to the user on the volume of dir (or of its
// nearest existing parent), -1 when unknown.
func diskFree(dir string) int64 {
	for {
		var st syscall.Statfs_t
		if err := syscall.Statfs(dir, &st); err == nil {
			return int64(uint64(st.Bavail) * uint64(st.Bsize))
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return -1
		}
		dir = parent
	}
}
