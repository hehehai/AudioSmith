//go:build !windows

package server

import (
	"os"
	"syscall"
)

// HijackStdout duplicates file descriptor 1 for protocol output and redirects
// fd 1 / os.Stdout to stderr so stray prints from libraries cannot corrupt
// the NDJSON protocol stream.
func HijackStdout() (*os.File, error) {
	fd, err := syscall.Dup(int(os.Stdout.Fd()))
	if err != nil {
		return os.Stdout, err
	}
	_ = syscall.Dup2(int(os.Stderr.Fd()), int(os.Stdout.Fd()))
	os.Stdout = os.Stderr
	return os.NewFile(uintptr(fd), "protocol-stdout"), nil
}
