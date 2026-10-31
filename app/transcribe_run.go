package main

// Transcribing: choosing or dropping a file, queueing the job, following it
// and cancelling; split from transcribe.go.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// transcribeExts are the files the page takes.
var transcribeExts = []string{"wav", "mp3", "m4a", "aac", "flac", "ogg", "opus", "aiff", "aif", "caf", "webm", "mp4", "mov", "m4v", "mkv"}

// chooseTranscribeFile asks for an audio or video file.
func (a *app) chooseTranscribeFile() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:  a.win,
			Title:   i18n.T("Transcribe a file"),
			Filters: []mygo.FileFilter{{Name: i18n.T("Audio or video"), Extensions: transcribeExts}},
		})
		if err != nil || len(paths) == 0 {
			return
		}
		a.post(func() { a.transcribeFile(paths[0]) })
	}()
}

// transcribeDropped takes the first audio or video file dropped.
func (a *app) transcribeDropped(paths []string) {
	for _, p := range paths {
		if slices.Contains(transcribeExts, strings.ToLower(strings.TrimPrefix(filepath.Ext(p), "."))) {
			a.transcribeFile(p)
			return
		}
	}
	a.toast(i18n.T("Drop an audio or video file."))
}

// transcribeFile transcribes a file in the page's mode and language.
func (a *app) transcribeFile(path string) {
	a.startTranscription(services.TranscribeRequest{Input: path, Source: core.SourceFile, Title: filepath.Base(path)})
}

// transcribeRecording transcribes what the page's recorder recorded.
func (a *app) transcribeRecording(path string) {
	a.startTranscription(services.TranscribeRequest{Input: path, Source: core.SourceRecording, RemoveInput: true})
}

// startTranscription fills the request with the page's mode, model,
// aligner and language and queues it; the page then follows its job.
func (a *app) startTranscription(req services.TranscribeRequest) {
	if a.trJob != nil {
		a.toast(i18n.T("A transcription is running. Wait for it, or cancel it."))
		return
	}
	fast := a.trMode == 0
	e := a.trModel(fast)
	req.Model, req.Mode, req.Language = e.Repo, settings.TranscribeFast, a.prefs.Transcribe.Language
	req.Words = e.Has(catalog.CapWordTimes)
	if e.Has(catalog.CapSpeakers) {
		// Its vocabulary: the names and terms dictation expects.
		req.Prompt = a.prefs.Dictation.Vocabulary
	}
	if !fast {
		req.Mode = settings.TranscribeAccurate
		if al := trAligner(e); al.Repo != "" {
			if ready, known := a.modelOnDisk(al); ready || !known {
				req.Aligner = al.Repo
			}
		}
	}
	a.queueTranscription(req, false)
}

// queueTranscription submits req; retry says it is the same request again.
func (a *app) queueTranscription(req services.TranscribeRequest, retry bool) {
	if req.ID == "" || retry {
		req.ID = core.NewID(time.Now())
	}
	a.trErr, a.trRetry, a.trNeed, a.trWarn = "", nil, nil, ""
	title := req.Title
	if title == "" {
		title = i18n.T("Recording")
	}
	owner := services.TranscriptOwner + req.ID
	a.trJob = &trJob{owner: owner, trID: req.ID, title: title, stage: i18n.T("Starting…"), fraction: -1, started: time.Now()}
	a.selectTranscript("")
	svc := a.trSvc
	a.async(func() {
		_, id, err := svc.Transcribe(context.Background(), req)
		a.post(func() {
			j := a.trJob
			if j == nil || j.owner != owner {
				return
			}
			if err != nil {
				a.trJob = nil
				a.transcriptRefused(req, err)
				return
			}
			if j.id == "" {
				j.id = id
			}
			if j.cancelling {
				a.cancelJob(id)
			}
		})
	})
}

// transcriptRefused explains why a transcription did not start, and
// offers the fix (a download, the licence).
func (a *app) transcriptRefused(req services.TranscribeRequest, err error) {
	var lic *core.LicenceRequiredError
	switch {
	case errors.As(err, &lic):
		e := entryFor(lic.Model)
		a.askLicence(e, func() { a.queueTranscription(req, true) })
	case errors.Is(err, core.ErrNotDownloaded):
		repo := req.Model
		if strings.HasPrefix(err.Error(), "aligner:") {
			repo = req.Aligner
		}
		e := entryFor(repo)
		a.trErr = i18n.T("%s is not downloaded yet.", modelRef(e))
		a.trNeed = &e
		a.trRetry = &req
	default:
		a.trErr = err.Error()
		a.trRetry = &req
	}
	if req.RemoveInput && a.trRetry == nil {
		os.Remove(req.Input)
	}
}

// transcriptJobUpdated follows transcription jobs: the page's, and
// dictation's (dictation_run.go).
func (a *app) transcriptJobUpdated(u jobs.Job) {
	if a.dictJobUpdated(u) { // dictation_run.go
		return
	}
	j := a.trJob
	if j == nil || u.Owner != j.owner {
		return
	}
	j.id = u.ID
	switch u.State {
	case jobs.StateWaiting, jobs.StateQueued:
		if !j.cancelling {
			j.stage = i18n.T("Waiting for the engine…")
		}
	case jobs.StateRunning:
		if !j.cancelling && u.Progress.Stage != "" {
			j.stage = stageText(u.Progress.Stage)
		}
		j.fraction = u.Progress.Fraction
	case jobs.StateSucceeded:
		a.trJob = nil
		var res services.TranscribeResult
		if json.Unmarshal(u.Result, &res) == nil && res.Transcript.ID != "" {
			a.transcriptChanged(core.TranscriptChanged{Transcript: res.Transcript})
			a.selectTranscript("")
			full := res.Transcript
			a.trSel, a.trDetail = full.ID, &full
			if a.router.Path() == "/transcripts" { // the list shows: the transcript opens
				a.navigate("/transcripts/" + full.ID)
			}
			if res.AlignError != nil {
				a.trWarn = i18n.T("Word timings are missing: %s", jobErrorText(res.AlignError))
			}
		}
	case jobs.StateCancelled:
		a.trJob = nil
		a.toast(i18n.T("Cancelled"))
	case jobs.StateFailed, jobs.StateInterrupted:
		a.trJob = nil
		a.trErr = jobErrorText(u.Err)
		var req services.TranscribeRequest
		if json.Unmarshal(u.Params, &req) == nil {
			if req.RemoveInput {
				// The recording is still there unless its audio was kept.
				if _, err := os.Stat(req.Input); err == nil {
					a.trRetry = &req
				}
			} else {
				a.trRetry = &req
			}
			if u.Err != nil && u.Err.Code == proto.CodeModelNotDownloaded {
				var d struct{ Model string }
				json.Unmarshal(u.Err.Data, &d)
				if d.Model == "" {
					d.Model = req.Model
				}
				e := entryFor(d.Model)
				a.trNeed = &e
			}
		}
	}
}

// cancelTranscription cancels the page's transcription.
func (a *app) cancelTranscription() {
	j := a.trJob
	if j == nil {
		return
	}
	j.cancelling, j.stage = true, i18n.T("Cancelling…")
	if j.id != "" {
		a.cancelJob(j.id)
	}
}
