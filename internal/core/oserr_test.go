package core

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"
)

func TestNoSpaceClassification(t *testing.T) {
	if NoSpace(nil) {
		t.Error("nil should not be NoSpace")
	}
	if NoSpace(errors.New("permission denied")) {
		t.Error("permission denied should not be NoSpace")
	}
	cases := []error{
		ErrDiskFull,
		&DiskFullError{What: "render", Path: "/tmp", Need: 100, Free: 10},
		fmt.Errorf("write: %w", &os.PathError{Op: "write", Path: "a.wav", Err: syscall.ENOSPC}),
		errors.New("write C:\\out.wav: There is not enough space on the disk."),
		errors.New("磁盘空间不足"),
	}
	for _, err := range cases {
		if !NoSpace(err) {
			t.Errorf("NoSpace(%v) = false, want true", err)
		}
	}
}

func TestIsAddrInUse(t *testing.T) {
	if IsAddrInUse(nil) {
		t.Error("nil should not be IsAddrInUse")
	}
	if IsAddrInUse(errors.New("other error")) {
		t.Error("other error should not be IsAddrInUse")
	}
	if !IsAddrInUse(fmt.Errorf("listen: %w", syscall.EADDRINUSE)) {
		t.Error("EADDRINUSE should be IsAddrInUse")
	}
}
