package services

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

// Code review 1102 M4: with DiskFullJob as the scheduler's classifier (as
// the app sets it), a job whose write finds the disk full fails with
// disk_full (not internal), whether the executor returned Go's ENOSPC, a
// pre-check's refusal or the engine's words; it is not retried.
func TestJobDiskFull(t *testing.T) {
	s := jobs.New(jobs.Options{Store: jobs.NewMemStore(), ClassifyError: DiskFullJob,
		RetryDelay: func(int) time.Duration { return time.Millisecond }})
	ctx := context.Background()
	errs := map[string]error{
		"enospc":   fmt.Errorf("write: %w", &os.PathError{Op: "write", Path: "/x/a.wav", Err: syscall.ENOSPC}),
		"precheck": &core.DiskFullError{What: "the dub", Path: "/x", Need: 3e9, Free: 1e9},
		"engine":   &jobs.Error{Code: jobs.CodeInternal, Message: "The file “a.wav” couldn’t be saved because there isn’t enough space.", Retryable: true},
		"other":    fmt.Errorf("model failed"),
	}
	for kind, err := range errs {
		s.Register(kind, jobs.ExecutorFunc(func(context.Context, jobs.Job, func(jobs.Progress), func(json.RawMessage)) (json.RawMessage, error) {
			return nil, err
		}))
	}
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for kind := range errs {
		id, _ := s.Submit(ctx, jobs.Spec{Kind: kind, Lane: jobs.LaneCPU, MaxAttempts: 3})
		var j jobs.Job
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(2 * time.Millisecond) {
			if j, _ = s.Get(ctx, id); j.State.Terminal() {
				break
			}
		}
		want := core.CodeDiskFull
		if kind == "other" {
			want = jobs.CodeInternal
		}
		if j.State != jobs.StateFailed || j.Err == nil || j.Err.Code != want || j.Attempt != 1 {
			t.Errorf("%s: %s %+v after %d attempts", kind, j.State, j.Err, j.Attempt)
		}
		if kind == "precheck" {
			var d core.DiskFullData
			if json.Unmarshal(j.Err.Data, &d); d.Need != 3e9 || d.Free != 1e9 {
				t.Errorf("data %s", j.Err.Data)
			}
		}
	}
}
