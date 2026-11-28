//go:build windows

package core

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

func isOSSpaceErr(err error) bool {
	return errors.Is(err, syscall.ENOSPC) ||
		errors.Is(err, windows.ERROR_DISK_FULL) ||
		errors.Is(err, windows.ERROR_HANDLE_DISK_FULL)
}

// IsAddrInUse reports whether err indicates a TCP/UDP bind address is already in use.
func IsAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE) ||
		errors.Is(err, windows.WSAEADDRINUSE)
}
