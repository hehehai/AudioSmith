package api

import (
	"fmt"
	"net/http"
	"os"
	"syscall"
	"testing"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

// Code review 1102 M4: a full disk is a 507 disk_full with the bytes
// needed and free, not a 500 internal.
func TestErrorDiskFull(t *testing.T) {
	pre := fmt.Errorf("dub x: %w", &core.DiskFullError{What: "the dub", Path: "/data", Need: 3e9, Free: 1e9})
	e := errorOf(pre)
	if e.Status != http.StatusInsufficientStorage || e.Code != CodeDiskFull || e.Data["need"] != float64(3e9) || e.Data["free"] != float64(1e9) {
		t.Fatalf("pre-check: %+v", e)
	}
	body := errorBody(e)["error"].(map[string]any)
	if body["code"] != "disk_full" || body["need"] != float64(3e9) {
		t.Errorf("body %v", body)
	}
	for _, err := range []error{
		&os.PathError{Op: "write", Path: "/x", Err: syscall.ENOSPC},
		&jobs.Error{Code: "internal", Message: "write /x: no space left on device"},
		&jobs.Error{Code: core.CodeDiskFull, Message: "full"},
	} {
		if e := errorOf(err); e.Status != http.StatusInsufficientStorage || e.Code != CodeDiskFull {
			t.Errorf("%v: %+v", err, e)
		}
	}
	// A model download's refusal keeps its code.
	if e := errorOf(&jobs.Error{Code: "disk_space", Message: "x needs 3 GB more: not enough free disk space"}); e.Code != "disk_space" || e.Status != http.StatusInsufficientStorage {
		t.Errorf("download: %+v", e)
	}
}
