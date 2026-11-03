package services

// The plumbing every dub stage shares: its context (stageCtx: warnings,
// files, saving the project, engine calls, progress), running a stage as
// a job and ending the run, and the stages' error codes; split from
// dub.go.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

// stageFunc does a stage's work on a copy of the project and returns the
// change to save (applied to the latest saved project) and warnings.
type stageFunc func(ctx context.Context, sc *stageCtx) error

// stageCtx is a running stage.
type stageCtx struct {
	s        *Dubs
	job      jobs.Job
	params   StageParams
	p        *dub.Project // as it was when the stage started
	report   func(jobs.Progress)
	warnings []string
	// skipped: the stage could not run (the engine lacks it): code.
	skipped string
}

func (sc *stageCtx) warn(code string) {
	if !slices.Contains(sc.warnings, code) {
		sc.warnings = append(sc.warnings, code)
	}
}

func (sc *stageCtx) dir() string { return sc.s.Dir(sc.p.ID) }

func (sc *stageCtx) path(name string) string { return sc.s.Path(sc.p.ID, name) }

// save applies fn to the latest project.
func (sc *stageCtx) save(ctx context.Context, fn func(*dub.Project)) error {
	_, err := sc.s.save(ctx, sc.p.ID, func(p *dub.Project) error { // no copy back (dub_cache.go)
		fn(p)
		return nil
	}, true)
	return err
}

// stage wraps a stage's work as an executor: it marks the stage running,
// then done (or skipped, or failed with the error's code), and finishes
// the run when it was its last stage.
func (s *Dubs) stage(st dub.Stage, fn stageFunc) jobs.Executor {
	return jobs.ExecutorFunc(func(ctx context.Context, job jobs.Job, report func(jobs.Progress), _ func(json.RawMessage)) (json.RawMessage, error) {
		var params StageParams
		if err := json.Unmarshal(job.Params, &params); err != nil || params.Dub == "" {
			return nil, &jobs.Error{Code: proto.CodeBadRequest, Message: "dub stage: bad params"}
		}
		p, err := s.Get(ctx, params.Dub)
		if err != nil {
			if errors.Is(err, core.ErrNotFound) {
				return nil, &jobs.Error{Code: DubCodeNoSource, Message: err.Error()}
			}
			return nil, err
		}
		// A stage planned before AutoRetarget switched its language runs in the new one.
		params.Lang = p.RetargetedLang(params.Lang)
		key := dub.StageKey(st, params.Lang)
		mark := func(state dub.StageState) {
			state.Job, state.At = job.ID, s.cfg.Now()
			s.Update(context.WithoutCancel(ctx), p.ID, func(p *dub.Project) error {
				p.SetStage(st, params.Lang, state)
				if state.Status == dub.StatusDone || state.Status == dub.StatusSkipped {
					s.endRun(p)
					if !p.Freed.IsZero() && !s.WorkGone(p) {
						p.Freed = time.Time{} // made again (dub_free.go)
					}
				}
				return nil
			})
		}
		mark(dub.StageState{Status: dub.StatusRunning})
		sc := &stageCtx{s: s, job: job, params: params, p: p, report: report}
		err = fn(ctx, sc)
		switch {
		case err == nil && sc.skipped != "":
			mark(dub.StageState{Status: dub.StatusSkipped, Code: sc.skipped, Warnings: sc.warnings})
			return json.Marshal(map[string]any{"stage": key, "skipped": sc.skipped, "warnings": sc.warnings})
		case err == nil:
			mark(dub.StageState{Status: dub.StatusDone, Warnings: sc.warnings})
			return json.Marshal(map[string]any{"stage": key, "warnings": sc.warnings})
		case ctx.Err() != nil:
			mark(dub.StageState{Status: dub.StatusNone, Code: proto.CodeCancelled})
			return nil, ctx.Err()
		}
		je := jobs.DiskFull(err) // a full disk says so (code review 1102 M4)
		if je == nil && !errors.As(err, &je) {
			je = &jobs.Error{Code: DubCodeFailed, Message: err.Error()}
		}
		mark(dub.StageState{Status: dub.StatusFailed, Code: je.Code, Message: je.Message, Warnings: sc.warnings})
		return nil, je
	})
}

// endRun forgets the run once nothing of it is left to do.
func (s *Dubs) endRun(p *dub.Project) {
	if p.Run == nil {
		return
	}
	for _, st := range p.Stages {
		if st.Status == dub.StatusRunning || st.Status == dub.StatusFailed {
			return
		}
	}
	through := p.Run.Through
	switch through {
	case dub.StageIngest:
		if st := p.Stage(dub.StageIngest, "").Status; st != dub.StatusDone {
			return
		}
	case dub.StageReferences:
		if p.Stage(dub.StageReferences, "").Status != dub.StatusDone && p.Stage(dub.StageReferences, "").Status != dub.StatusSkipped {
			return
		}
	case dub.StageTranslate:
		for _, l := range langsOf(p, p.Run.Langs) {
			if len(p.Untranslated(l)) > 0 && p.Stage(dub.StageTranslate, l).Status != dub.StatusDone {
				return
			}
		}
	case dub.StageQC:
		for _, l := range langsOf(p, p.Run.Langs) {
			if p.Stage(dub.StageQC, l).Status != dub.StatusDone {
				return
			}
		}
	default:
		for _, l := range langsOf(p, p.Run.Langs) {
			if p.Stage(dub.StageMix, l).Status != dub.StatusDone {
				return
			}
		}
	}
	p.Run = nil
}

// call runs an engine method inside a stage's job; with model set, its
// local snapshot is filled into *modelPath first.
func (sc *stageCtx) call(ctx context.Context, method, model string, params any, modelPath *string, out any, report func(jobs.Progress)) error {
	return engineCaller{sc.s.cfg.Engine, sc.s.cfg.ModelPath}.call(ctx, sc.job, method, model, params, modelPath, out, report)
}

// missing reports an engine that lacks a method (or a model it needs is
// not downloaded): the stage is skipped rather than failed.
func missing(err error) bool {
	var je *jobs.Error
	if !errors.As(err, &je) {
		return false
	}
	return je.Code == proto.CodeMethodNotFound || je.Code == proto.CodeModelNotDownloaded || je.Code == proto.CodeUnsupported
}

// code is a stage error's code.
func code(err error) string {
	var je *jobs.Error
	if errors.As(err, &je) {
		return je.Code
	}
	return ""
}

func stageErr(code, format string, args ...any) error {
	return &jobs.Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// progress reports a fraction of a stage.
func (sc *stageCtx) progress(stage string, fraction float64, msg string) {
	if sc.report != nil {
		sc.report(jobs.Progress{Stage: stage, Fraction: fraction, Message: msg})
	}
}

// dubFirst is the first non-empty string.
func dubFirst(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}
