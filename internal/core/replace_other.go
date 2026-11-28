//go:build !windows

package core

import "os"

// ReplaceFile atomically replaces dst with src.
func ReplaceFile(src, dst string) error {
	return os.Rename(src, dst)
}
