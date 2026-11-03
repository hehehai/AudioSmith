package main

// Dubbing's jobs and runs: job updates, run / start / cancel / resume,
// and stale tracks; split from dub.go.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
)

// dubJobUpdated follows the open project's jobs (and the batch's).
func (a *app) dubJobUpdated(j jobs.Job) {
	a.batchJobUpdated(j)
	if strings.HasPrefix(j.Owner, services.DubExportOwner) {
		a.dubExportUpdated(j)
		return
	}
	if a.dub == nil || j.Owner != services.DubOwner+a.dub.ID {
		return
	}
	if j.State.Terminal() || j.State == jobs.StateInterrupted {
		delete(a.dubJobs, j.ID)
		if j.State == jobs.StateFailed && j.Err != nil {
			a.dubFailed(j.Err)
		}
		return
	}
	a.dubJobs[j.ID] = j
}

// dubFailed shows why a stage failed, with its fix.
func (a *app) dubFailed(e *jobs.Error) {
	if e.Code == jobs.CodeDependency && a.dubErr != "" {
		return // the stage that failed first already said why
	}
	a.dubErr, a.dubErrCode = dubErrorText(e), e.Code
	if e.Code == "model_not_downloaded" {
		var d struct{ Model string }
		json.Unmarshal(e.Data, &d)
		if d.Model != "" {
			en := entryFor(d.Model)
			a.dubNeed = &en
		}
	}
}

// dubBusy reports whether the open project has jobs running or queued.
func (a *app) dubBusy() bool { return len(a.dubJobs) > 0 }

// dubRunning is the job of the open project running now (zero if none).
func (a *app) dubRunning() jobs.Job {
	var best jobs.Job
	for _, j := range a.dubJobs {
		if j.State == jobs.StateRunning {
			return j
		}
		if best.ID == "" || j.Created.Before(best.Created) {
			best = j
		}
	}
	return best
}

// dubLocked reports whether the transcript may not be edited now (a stage
// that writes it runs).
func (a *app) dubLocked() bool {
	for _, j := range a.dubJobs {
		switch j.Kind {
		case services.KindDubTranscribe, services.KindDubTranslate, services.KindDubFetch, services.KindDubIngest:
			return true
		}
	}
	return false
}

// dubRun starts the pipeline through a stage, after checking what it
// needs (the TTS model downloaded and its licence accepted, a
// translator).
func (a *app) dubRun(req services.RunRequest) {
	p := a.dub
	if p == nil || a.dubBusy() {
		return
	}
	a.dubErr, a.dubErrCode, a.dubNeed = "", "", nil
	if req.Through == dub.StageMix || req.Through == dub.StageSynthesize {
		e := entryFor(p.Settings.Model)
		if ready, known := a.modelOnDisk(e); known && !ready {
			a.dubErr = i18n.T("%s is not downloaded yet.", modelRef(e))
			a.dubNeed = &e
			return
		}
		if a.lib.licences != nil {
			if err := a.lib.licences.Check(context.Background(), e.Repo); err != nil {
				var lic *core.LicenceRequiredError
				if errors.As(err, &lic) {
					a.askLicence(e, func() { a.dubRun(req) })
					return
				}
			}
		}
	}
	if (req.Through == dub.StageTranslate || req.Through == dub.StageMix) && len(p.Segments) > 0 {
		if ok, _ := a.dubTranslatorReady(p.Settings); !ok {
			missing := false
			for _, l := range p.Languages() {
				if len(p.Untranslated(l)) > 0 {
					missing = true
				}
			}
			if missing {
				a.dubErr, a.dubErrCode = i18n.T("Translating needs a translation engine: a local model (Ollama, LM Studio) or a provider in Settings → Writing help."), services.DubCodeNoTranslator
				return
			}
		}
	}
	if p.Source.Kind == dub.SourceURL && a.dubCookies != "" {
		a.dubSvc.SetCookies(p.ID, a.dubCookies)
		a.dubCookies = ""
	}
	svc, sched := a.dubSvc, a.lib.sched
	id := p.ID
	a.async(func() {
		ids, err := svc.Run(context.Background(), id, req)
		active := dubUnfinished(sched, ids)
		a.post(func() {
			if err != nil {
				a.dubErr = engineErrorText(err)
				return
			}
			for _, j := range active {
				if _, ok := a.dubJobs[j.ID]; !ok {
					a.dubJobs[j.ID] = j
				}
			}
			if len(ids) == 0 {
				a.toast(i18n.T("Nothing to do: everything is up to date."))
			}
		})
	})
}

// dubUnfinished are the jobs of ids that have not ended yet (their
// updates may have come already).
func dubUnfinished(sched jobs.Scheduler, ids map[string]string) []jobs.Job {
	var out []jobs.Job
	for _, id := range ids {
		j, err := sched.Get(context.Background(), id)
		if err == nil && !j.State.Terminal() && j.State != jobs.StateInterrupted {
			out = append(out, j)
		}
	}
	return out
}

// dubStart runs the next part of the pipeline: up to the transcript with
// review on, else the whole dub.
func (a *app) dubStart() {
	p := a.dub
	if p == nil {
		return
	}
	through := dub.StageMix
	if p.Settings.Review && len(p.Segments) == 0 {
		through = dub.StageReferences
	}
	a.dubRun(services.RunRequest{Through: through})
}

// dubTranslate translates (the lines that need it).
func (a *app) dubTranslate(force bool) {
	req := services.RunRequest{Through: dub.StageTranslate}
	if force {
		req.Force = []dub.Stage{dub.StageTranslate}
	}
	a.dubRun(req)
}

// dubGenerate makes the dubbed tracks (the stale lines).
func (a *app) dubGenerate() { a.dubRun(services.RunRequest{Through: dub.StageMix}) }

// dubVerify runs the second-pass check of the language shown.
func (a *app) dubVerify() {
	if a.dub == nil {
		return
	}
	a.dubRun(services.RunRequest{Through: dub.StageQC, Langs: []string{a.dubLang}})
}

// dubCancel cancels the open project's jobs.
func (a *app) dubCancel() {
	if a.dub == nil {
		return
	}
	svc, id := a.dubSvc, a.dub.ID
	for k, j := range a.dubJobs {
		j.State = jobs.StateCancelled
		a.dubJobs[k] = j
	}
	a.async(func() {
		svc.Cancel(context.Background(), id)
		a.post(func() {
			for k, j := range a.dubJobs {
				if j.State == jobs.StateCancelled {
					delete(a.dubJobs, k)
				}
			}
		})
	})
}

// dubResume continues an interrupted run.
func (a *app) dubResume() {
	if a.dub == nil {
		return
	}
	svc, id := a.dubSvc, a.dub.ID
	a.async(func() {
		ids, err := svc.Resume(context.Background(), id)
		active := dubUnfinished(a.lib.sched, ids)
		a.post(func() {
			if err != nil {
				a.dubErr = engineErrorText(err)
				return
			}
			for _, j := range active {
				a.dubJobs[j.ID] = j
			}
		})
	})
}

// dubInterrupted reports whether the open project's last run stopped.
func (a *app) dubInterrupted() bool {
	return a.dub != nil && services.Interrupted(a.dub, nil) && !a.dubBusy()
}

// dubStaleCount is how many lines of lang changed since they were rendered.
func (a *app) dubStaleCount(l string) int {
	p := a.dub
	if p == nil {
		return 0
	}
	t := p.Track(l)
	if t.File == "" && len(t.Lines) == 0 {
		return 0
	}
	n := 0
	for _, s := range p.Segments {
		if strings.TrimSpace(p.TextFor(s, l)) == "" {
			continue
		}
		r, ok := t.Lines[s.ID]
		if !ok || r.Err != "" || r.Start != s.Start || r.Text != p.TextFor(s, l) {
			n++
		}
	}
	return n
}
