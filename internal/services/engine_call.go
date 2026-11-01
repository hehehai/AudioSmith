package services

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

// engineCaller runs engine methods as sub-jobs of a service's job (dubs,
// transcripts, isolations, workflow steps).
type engineCaller struct {
	engine    func(method string) jobs.Executor                      // nil: no engine
	modelPath func(ctx context.Context, repo string) (string, error) // nil: paths are not filled
}

// call runs method inside job: with model and modelPath set, the model's
// local snapshot is filled into *modelPath first; the result is decoded
// into out (nil: dropped). method may name its role ("listen:stt.transcribe").
func (e engineCaller) call(ctx context.Context, job jobs.Job, method, model string, params any, modelPath *string, out any, report func(jobs.Progress)) error {
	if model != "" && modelPath != nil && e.modelPath != nil {
		dir, err := e.modelPath(ctx, model)
		if err != nil {
			if errors.Is(err, core.ErrNotDownloaded) {
				data, _ := json.Marshal(map[string]string{"model": model})
				return &jobs.Error{Code: proto.CodeModelNotDownloaded, Message: err.Error(), Data: data}
			}
			return &jobs.Error{Code: proto.CodeInternal, Message: err.Error()}
		}
		*modelPath = dir
	}
	var ex jobs.Executor
	if e.engine != nil {
		ex = e.engine(method)
	}
	if ex == nil {
		return &jobs.Error{Code: CodeEngineUnavailable, Message: "The engine is not connected.", Retryable: true}
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if report == nil {
		report = func(jobs.Progress) {}
	}
	role, m := proto.SplitKind(method)
	if m == method {
		role = dub.RoleOf(method) // dub's own methods (time-pitch) run on the media worker
	}
	sub := jobs.Job{ID: job.ID + "." + m, Spec: jobs.Spec{Kind: method, Lane: jobs.Lane(role), Params: raw, Owner: job.Owner}}
	res, err := ex.Run(ctx, sub, report, nil)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(res, out)
}
