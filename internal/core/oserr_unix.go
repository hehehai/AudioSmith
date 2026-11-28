//go:build !windows

package core

import (
	"errors"
	"syscall"
)

func isOSSpaceErr(err error) bool {
	return errors.Is(err, syscall.ENOSPC)
}

// IsAddrInUse reports whether err indicates a TCP/UDP bind address is already in use.
func IsAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}
