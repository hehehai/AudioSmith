package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"syscall"
	"testing"

	"github.com/hehehai/AudioSmith/internal/core"
)

// Code review 1102 M4: a job whose write finds the disk full fails with
// disk_full (not internal), whether the executor returned Go's ENOSPC, a
// pre-check's refusal or the engine's words; it is not retried.
func TestJobDiskFull(t *testing.T) {
	s, _, rec := newTestSched(t, nil)
	ctx := context.Background()
	errs := map[string]error{
		"enospc":   fmt.Errorf("write: %w", &os.PathError{Op: "write", Path: "/x/a.wav", Err: syscall.ENOSPC}),
		"precheck": &core.DiskFullError{What: "the dub", Path: "/x", Need: 3e9, Free: 1e9},
		"engine":   &Error{Code: CodeInternal, Message: "The file “a.wav” couldn’t be saved because there isn’t enough space.", Retryable: true},
	}
	for kind, err := range errs {
		s.Register(kind, ExecutorFunc(func(context.Context, Job, func(Progress), func(json.RawMessage)) (json.RawMessage, error) {
			return nil, err
		}))
	}
	s.Start(ctx)
	for kind := range errs {
		id, _ := s.Submit(ctx, Spec{Kind: kind, Lane: LaneCPU, MaxAttempts: 3})
		j := rec.waitState(t, id, StateFailed)
		if j.Err.Code != core.CodeDiskFull || j.Attempt != 1 {
			t.Errorf("%s: %+v after %d attempts", kind, j.Err, j.Attempt)
		}
		if kind == "precheck" {
			var d core.DiskFullData
			if json.Unmarshal(j.Err.Data, &d); d.Need != 3e9 || d.Free != 1e9 {
				t.Errorf("data %s", j.Err.Data)
			}
		}
	}
}
