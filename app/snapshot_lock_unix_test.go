//go:build unix

package main

import (
	"os"
	"syscall"
	"testing"
)

func lockSnapFile(t *testing.T, lock *os.File, root string) {
	t.Helper()
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		t.Logf("waiting for another snapshot run to release %s.lock", root)
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
			t.Fatal(err)
		}
	}
}
