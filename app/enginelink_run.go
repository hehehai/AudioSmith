package main

// The Studio's own jobs through the engine link: a take (TakeService,
// owned by "take:<id>"), a preload ("studio:load:…"), the refusals
// before either, cancelling, and the job's updates (jobUpdated).

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/licence"
	"github.com/hehehai/AudioSmith/internal/oplog"
)

// startTake asks TakeService for the take whose recipe is t (its id
// chosen) and follows its job, owned by "take:<id>". The take arrives with
// TakeCreated (takeCreated), after the service saved it.
func (a *app) startTake(t core.Take) {
	status := i18n.T("Starting…")
	if a.loadedModel != t.Model {
		status = i18n.T("Loading model…")
	}
	if t.Origin == "" {
		t.Origin = core.TakeStudio
	}
	owner := takeOwner + t.ID
	a.jobError, a.needDownload = "", nil
	a.awaitTake = t.ID
	a.job = &job{owner: owner, kind: "synthesize", model: t.Model, started: time.Now(), status: status, fraction: -1, takeID: t.ID}
	takes, post := a.lib.svc.Takes, a.enginePost
	a.async(func() {
		id, err := takes.Generate(context.Background(), core.TakeRequest{Recipe: t})
		post(func() {
			j := a.job
			if j == nil || j.owner != owner {
				return
			}
			if err != nil {
				a.job = nil
				if a.awaitTake == t.ID {
					a.awaitTake = ""
				}
				a.generateRefused(t, err)
				return
			}
			j.id = id
			if j.cancelling {
				a.cancelJob(id)
			}
		})
	})
}

// generateRefused explains why Generate did not queue the take, and
// offers the fix: the licence dialog, or a download.
func (a *app) generateRefused(t core.Take, err error) {
	e := entryFor(t.Model)
	var lic *core.LicenceRequiredError
	switch {
	case errors.As(err, &lic):
		a.askLicence(e, func() { a.generate() })
	case errors.Is(err, core.ErrNotDownloaded):
		a.jobError = i18n.T("%s is not downloaded yet.", modelRef(e))
		a.needDownload = &e
	case errors.As(err, new(*core.NeedsReferenceError)):
		a.jobError = engineErrorText(err)
	default:
		a.jobError = err.Error()
	}
}

// preload loads the chosen model without generating, so the first take is quick.
func (a *app) preload() {
	e := a.preset()
	repo := e.Repo
	if a.engState != engineReady || a.job != nil || repo == "" || repo == a.loadedModel || e.IsExternal() {
		return
	}
	if licence.Required(e) != nil && !licence.Covers(a.accepted, e) {
		a.askLicence(e, a.preload)
		return
	}
	params, _ := json.Marshal(proto.LoadParams{Model: repo})
	a.jobError, a.needDownload = "", nil
	a.job = &job{owner: studioOwner + "load:" + core.NewID(time.Now()), kind: "load", model: repo, started: time.Now(),
		status: i18n.T("Loading model…"), fraction: -1}
	a.submit(jobs.Spec{Kind: proto.MethodModelsLoad, Lane: jobs.LaneGPU, Priority: jobs.PriorityInteractive,
		Params: params, Title: i18n.T("Load model"), Owner: a.job.owner})
}

// submit queues the Studio's own job (a preload) in the background; its
// id arrives with the result or with its first update, whichever is first.
func (a *app) submit(spec jobs.Spec) {
	sched, owner, post := a.eng.sched, spec.Owner, a.enginePost
	a.async(func() {
		id, err := sched.Submit(context.Background(), spec)
		post(func() {
			j := a.job
			if j == nil || j.owner != owner {
				return
			}
			if err != nil {
				a.job, a.jobError = nil, err.Error()
				return
			}
			j.id = id
			if j.cancelling {
				a.cancelJob(id)
			}
		})
	})
}

func (a *app) cancel() {
	if a.job == nil {
		return
	}
	a.job.status = i18n.T("Cancelling…")
	a.job.cancelling = true
	if a.job.id != "" {
		a.cancelJob(a.job.id)
	}
}

func (a *app) cancelJob(id string) {
	sched := a.eng.sched
	a.async(func() { oplog.Err("job.cancel.ask", sched.Cancel(context.Background(), id), "id", id) })
}

// jobUpdated applies an update of the Studio's job; it runs on the main thread.
func (a *app) jobUpdated(u jobs.Job) {
	j := a.job
	if j == nil || u.Owner != j.owner {
		return
	}
	j.id = u.ID
	switch u.State {
	case jobs.StateWaiting, jobs.StateQueued:
		if !j.cancelling {
			j.status = i18n.T("Waiting for the engine…")
		}
	case jobs.StateRunning:
		if j.cancelling {
			return
		}
		if u.Progress.Stage != "" {
			j.status = stageText(u.Progress.Stage)
		}
		j.fraction = u.Progress.Fraction
		if u.Progress.AudioSeconds > 0 {
			j.audioSeconds = u.Progress.AudioSeconds
		}
	case jobs.StateSucceeded:
		a.job = nil
		a.loadedModel = j.model
		if j.kind == "load" {
			var res proto.LoadResult
			json.Unmarshal(u.Result, &res)
			a.toast(i18n.T("Loaded %s in %.1f s", catalog.ShortRepo(j.model), res.Seconds))
		}
		// A take arrives with TakeCreated, once the service saved it.
	case jobs.StateCancelled:
		a.job = nil
		a.dropAwait(j)
		a.toast(i18n.T("Cancelled"))
	case jobs.StateFailed, jobs.StateInterrupted:
		a.job = nil
		a.dropAwait(j)
		a.jobError = jobErrorText(u.Err)
		if u.Err != nil && u.Err.Code == proto.CodeModelNotDownloaded {
			e := entryFor(j.model)
			a.needDownload = &e
		}
	}
}

// dropAwait stops waiting for the take of a job that will not make it.
func (a *app) dropAwait(j *job) {
	if j.takeID != "" && a.awaitTake == j.takeID {
		a.awaitTake = ""
	}
}
