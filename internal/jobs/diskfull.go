package jobs

import (
	"encoding/json"
	"errors"

	"github.com/hehehai/AudioSmith/internal/core"
)

// DiskFull is err as a disk_full job error when it says the disk is full
// (a pre-check's core.DiskFullError, a write's ENOSPC, an engine error
// whose message says so), else nil. The message is kept; the error is
// not retryable (the next attempt would fail the same way).
func DiskFull(err error) *Error {
	if err == nil {
		return nil
	}
	var je *Error
	if errors.As(err, &je) {
		if je.Code == core.CodeDiskFull {
			return je
		}
		if !core.NoSpaceText(je.Message) {
			return nil
		}
		return &Error{Code: core.CodeDiskFull, Message: je.Message}
	}
	if !core.NoSpace(err) {
		return nil
	}
	out := &Error{Code: core.CodeDiskFull, Message: err.Error()}
	if d := core.DiskFullDataOf(err); d != (core.DiskFullData{}) {
		out.Data, _ = json.Marshal(d)
	}
	return out
}
