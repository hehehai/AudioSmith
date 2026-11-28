//go:build windows

package core

import (
	"errors"
	"os"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32DLL      = windows.NewLazySystemDLL("kernel32.dll")
	procReplaceFileW = kernel32DLL.NewProc("ReplaceFileW")
)

const (
	replacefileIgnoreMergeErrors = 0x2
	replacefileIgnoreACLErrors   = 0x4
	errUnableToRemoveReplaced    = syscall.Errno(1175)
	errUnableToMoveReplacement   = syscall.Errno(1176)
	errUnableToMoveReplacement2  = syscall.Errno(1177)
	errSharingViolation          = syscall.Errno(32)
	errLockViolation             = syscall.Errno(33)
)

// ReplaceFile replaces dst with src on Windows, using ReplaceFileW when dst
// already exists and MoveFileExW otherwise, with brief retries on transient
// sharing violations (Windows Defender, search indexer, etc.).
func ReplaceFile(src, dst string) error {
	srcInfo, err := os.Lstat(src)
	if err != nil {
		return err
	}
	dstInfo, dstErr := os.Lstat(dst)
	if os.IsNotExist(dstErr) || srcInfo.IsDir() || (dstErr == nil && dstInfo.IsDir()) {
		return moveFileWithRetry(src, dst)
	}
	t, err := windows.UTF16PtrFromString(dst)
	if err != nil {
		return err
	}
	r, err := windows.UTF16PtrFromString(src)
	if err != nil {
		return err
	}
	for attempt := 0; ; attempt++ {
		ok, _, callErr := procReplaceFileW.Call(
			uintptr(unsafe.Pointer(t)),
			uintptr(unsafe.Pointer(r)),
			0,
			replacefileIgnoreMergeErrors|replacefileIgnoreACLErrors,
			0,
			0,
		)
		if ok != 0 {
			return nil
		}
		errno, _ := callErr.(syscall.Errno)
		if errno == errUnableToMoveReplacement || errno == errUnableToMoveReplacement2 || errno == windows.ERROR_FILE_NOT_FOUND {
			return moveFileWithRetry(src, dst)
		}
		if !isTransientLock(errno) || attempt == 9 {
			return &os.LinkError{Op: "replace", Old: src, New: dst, Err: callErr}
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func moveFileWithRetry(src, dst string) error {
	s, err := windows.UTF16PtrFromString(src)
	if err != nil {
		return err
	}
	d, err := windows.UTF16PtrFromString(dst)
	if err != nil {
		return err
	}
	const flags = windows.MOVEFILE_REPLACE_EXISTING | windows.MOVEFILE_WRITE_THROUGH
	for attempt := 0; ; attempt++ {
		err := windows.MoveFileEx(s, d, flags)
		if err == nil {
			return nil
		}
		var errno syscall.Errno
		if !errors.As(err, &errno) || !isTransientLock(errno) || attempt == 9 {
			return &os.LinkError{Op: "rename", Old: src, New: dst, Err: err}
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func isTransientLock(errno syscall.Errno) bool {
	return errno == errSharingViolation ||
		errno == errLockViolation ||
		errno == windows.ERROR_ACCESS_DENIED ||
		errno == errUnableToRemoveReplaced
}
