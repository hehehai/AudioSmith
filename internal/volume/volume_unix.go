//go:build darwin || linux

package volume

import (
	"os"
	"path/filepath"
	"syscall"
)

// existing is dir or its nearest parent that exists.
func existing(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	for {
		if _, err := os.Stat(dir); err == nil {
			return dir, true
		} else if !os.IsNotExist(err) && !os.IsPermission(err) {
			return "", false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// bavail is statfs's space free to the user of the volume of path, -1
// when unknown.
func bavail(path string) int64 {
	var st syscall.Statfs_t
	if syscall.Statfs(path, &st) != nil {
		return -1
	}
	return int64(uint64(st.Bavail) * uint64(st.Bsize))
}

func size(path string) int64 {
	var st syscall.Statfs_t
	if syscall.Statfs(path, &st) != nil {
		return 0
	}
	return int64(uint64(st.Blocks) * uint64(st.Bsize))
}

func device(path string) (uint64, bool) {
	var st syscall.Stat_t
	if syscall.Stat(path, &st) != nil {
		return 0, false
	}
	return uint64(st.Dev), true
}
