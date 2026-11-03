package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// Options.ClassifyError types an executor's failure first; errors it
// does not know (nil) are typed as before.
func TestClassifyError(t *testing.T) {
	full := errors.New("no room")
	s, _, rec := newTestSched(t, nil)
	s.opts.ClassifyError = func(err error) *Error {
		if errors.Is(err, full) {
			return &Error{Code: "disk_full", Message: err.Error()}
		}
		return nil
	}
	ctx := context.Background()
	for kind, err := range map[string]error{"full": full, "plain": errors.New("plain"), "typed": &Error{Code: "oom", Message: "oom"}} {
		s.Register(kind, ExecutorFunc(func(context.Context, Job, func(Progress), func(json.RawMessage)) (json.RawMessage, error) {
			return nil, err
		}))
	}
	s.Start(ctx)
	for kind, want := range map[string]string{"full": "disk_full", "plain": CodeInternal, "typed": "oom"} {
		id, _ := s.Submit(ctx, Spec{Kind: kind, Lane: LaneCPU})
		if j := rec.waitState(t, id, StateFailed); j.Err.Code != want {
			t.Errorf("%s: %+v", kind, j.Err)
		}
	}
}
