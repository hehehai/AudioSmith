//go:build windows

package server

import (
	"os"
	"syscall"
)

var (
	modKernel32      = syscall.NewLazyDLL("kernel32.dll")
	procSetStdHandle = modKernel32.NewProc("SetStdHandle")
)

// HijackStdout duplicates STD_OUTPUT_HANDLE for protocol output and points
// STD_OUTPUT_HANDLE / os.Stdout to stderr so stray prints from libraries or
// runtime DLLs cannot corrupt the NDJSON protocol stream.
func HijackStdout() (*os.File, error) {
	curProc, err := syscall.GetCurrentProcess()
	if err != nil {
		return os.Stdout, err
	}
	origOut, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err != nil || origOut == 0 || origOut == syscall.InvalidHandle {
		return os.Stdout, err
	}
	var dup syscall.Handle
	err = syscall.DuplicateHandle(curProc, origOut, curProc, &dup, 0, false, syscall.DUPLICATE_SAME_ACCESS)
	if err != nil {
		return os.Stdout, err
	}
	if errH, err := syscall.GetStdHandle(syscall.STD_ERROR_HANDLE); err == nil && errH != 0 && errH != syscall.InvalidHandle {
		const stdOutputHandle = ^uintptr(10) // (DWORD)-11
		_, _, _ = procSetStdHandle.Call(stdOutputHandle, uintptr(errH))
	}
	os.Stdout = os.Stderr
	return os.NewFile(uintptr(dup), "protocol-stdout"), nil
}
