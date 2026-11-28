package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

// Downloads run as jobs of kind KindDownload on jobs.LaneNet: they queue
// behind each other (three at a time), survive a restart (the job is
// idempotent, so the scheduler re-runs it and Download resumes the partial
// files), and are cancelled by job id like any other work.

// KindDownload is the job kind of model downloads.
const KindDownload = "model.download"

// OwnerPrefix starts the owner of a download job: "model:<repo>".
const OwnerPrefix = "model:"

// Job error codes of downloads (jobs.Error.Code).
const (
	CodeGated               = "gated"                // needs a token and the accepted terms
	CodeDiskSpace           = "disk_space"           // not enough free space
	CodeOffline             = "offline"              // no endpoint answered (retryable)
	CodeChecksum            = "checksum"             // a file arrived damaged (retryable)
	CodeNotFound            = "not_found"            // no such repo or revision
	CodeUnsupportedPlatform = "unsupported_platform" // model not available on this OS
	CodeFailed              = "download_failed"
)

// DownloadParams are a download job's params.
type DownloadParams struct {
	ID   string `json:"id,omitempty"` // catalogue id; "" for a repo outside the catalogue
	Repo string `json:"repo"`
}

// DownloadResult is a finished download job's result.
type DownloadResult struct {
	Snapshot string `json:"snapshot"`
	Bytes    int64  `json:"bytes"`
}

// DownloadSpec is the job that downloads e; title is shown in the jobs list.
func DownloadSpec(e catalog.Entry, title string) jobs.Spec {
	params, _ := json.Marshal(DownloadParams{ID: e.ID, Repo: e.Repo})
	return jobs.Spec{
		Kind:     KindDownload,
		Lane:     jobs.LaneNet,
		Priority: jobs.PriorityInteractive,
		Params:   params,
		Title:    title,
		Owner:    OwnerPrefix + e.Repo,
		// Dropped connections are retried inside Download; a whole attempt
		// failing (offline, a damaged file) gets one more.
		MaxAttempts: 2,
		Idempotent:  true,
	}
}

// EntryOf is the catalogue entry a download job's params name: the
// built-in entry with that id, else a custom entry for the repo.
func EntryOf(p DownloadParams) (catalog.Entry, error) {
	if p.ID != "" {
		if e, ok := catalog.Builtin().ByID(p.ID); ok {
			return e, nil
		}
	}
	if p.Repo == "" || !strings.Contains(p.Repo, "/") {
		return catalog.Entry{}, fmt.Errorf("download: repo %q: %w", p.Repo, jobs.ErrInvalid)
	}
	if e, ok := catalog.Builtin().ByRepo(p.Repo); ok {
		return e, nil
	}
	return catalog.Custom(p.Repo), nil
}

// Stages of a download job's progress: downloading (Message: the
// endpoint in use), or waiting for another app that downloads a file it
// needs (Message: the app's name, "" when unknown; Progress.WaitingFor).
const (
	StageDownloading = "downloading"
	StageWaiting     = "waiting"
)

// DownloadExecutor runs model.download jobs with the downloader client()
// returns when the job starts (the settings decide endpoints, proxy and
// folder, and may change between jobs). Progress is reported as bytes and
// a fraction, with the endpoint in use as the message; every report is
// also passed to progress (may be nil), e.g. to publish DownloadProgress.
func DownloadExecutor(client func() Downloader, progress func(Progress)) jobs.Executor {
	return jobs.ExecutorFunc(func(ctx context.Context, job jobs.Job, report func(jobs.Progress), _ func(json.RawMessage)) (json.RawMessage, error) {
		var p DownloadParams
		if err := json.Unmarshal(job.Params, &p); err != nil {
			return nil, &jobs.Error{Code: CodeFailed, Message: "params: " + err.Error()}
		}
		e, err := EntryOf(p)
		if err != nil {
			return nil, &jobs.Error{Code: CodeFailed, Message: err.Error()}
		}
		d := client()
		report(jobs.Progress{Stage: StageDownloading, Fraction: -1})
		err = d.Download(ctx, e, func(pr Progress) {
			frac := -1.0
			if pr.TotalBytes > 0 {
				frac = float64(pr.Bytes) / float64(pr.TotalBytes)
			}
			jp := jobs.Progress{Stage: StageDownloading, Fraction: frac, Bytes: pr.Bytes, TotalBytes: pr.TotalBytes, Message: pr.Mirror}
			if pr.Waiting {
				jp.Stage, jp.Message = StageWaiting, pr.WaitingFor
			}
			report(jp)
			if progress != nil {
				progress(pr)
			}
		})
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, downloadJobError(err)
		}
		l, err := d.Status(ctx, e)
		if err != nil {
			return nil, &jobs.Error{Code: CodeFailed, Message: err.Error()}
		}
		return json.Marshal(DownloadResult{Snapshot: l.Snapshot, Bytes: l.Bytes})
	})
}

// downloadJobError types a Download error for the job, keeping the error
// text as the message.
func downloadJobError(err error) *jobs.Error {
	je := &jobs.Error{Code: CodeFailed, Message: err.Error()}
	switch {
	case errors.Is(err, ErrGated):
		je.Code = CodeGated
	case errors.Is(err, ErrDiskSpace):
		je.Code = CodeDiskSpace
	case errors.Is(err, ErrOffline):
		je.Code, je.Retryable = CodeOffline, true
	case errors.Is(err, ErrChecksum):
		je.Code, je.Retryable = CodeChecksum, true
	case errors.Is(err, ErrNotFound):
		je.Code = CodeNotFound
	case errors.Is(err, ErrUnsupportedPlatform):
		je.Code = CodeUnsupportedPlatform
	}
	return je
}

// ErrorOf turns a failed download job's error back into the hub's error,
// for messages that explain it (errors.Is(err, ErrGated)…).
func ErrorOf(e *jobs.Error) error {
	if e == nil {
		return errors.New("download failed")
	}
	var base error
	switch e.Code {
	case CodeGated:
		base = ErrGated
	case CodeDiskSpace:
		base = ErrDiskSpace
	case CodeOffline:
		base = ErrOffline
	case CodeChecksum:
		base = ErrChecksum
	case CodeNotFound:
		base = ErrNotFound
	default:
		return errors.New(e.Message)
	}
	return jobErr{msg: e.Message, base: base}
}

// jobErr is a download job's message wrapping the hub error it came from.
type jobErr struct {
	msg  string
	base error
}

func (e jobErr) Error() string { return e.msg }
func (e jobErr) Unwrap() error { return e.base }
