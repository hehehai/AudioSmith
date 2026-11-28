//go:build windows

package main

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func lockSnapFile(t *testing.T, lock *os.File, root string) {
	t.Helper()
	h := windows.Handle(lock.Fd())
	var ol windows.Overlapped
	const all = ^uint32(0)
	err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, all, all, &ol)
	if err != nil {
		t.Logf("waiting for another snapshot run to release %s.lock", root)
		if err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, all, all, &ol); err != nil {
			t.Fatal(err)
		}
	}
}
