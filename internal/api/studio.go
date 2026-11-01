package api

// The app's modules for agents (thread #35): Stories and Audiobook
// projects, dubbing and the batch queue, voice discovery (the gallery),
// exports with the Export audio dialog's options, the project library and
// the History, and saved workflows. The app implements Studio over the
// same services its pages use; this file has the shared types and the
// operations that REST (studio_http.go) and MCP (internal/mcp) both call.
//
// Long work follows one pattern: a start call returns a job id at once
// and the work shows in the app's job list. The id is a queue job's, or a
// group of jobs named by their owner: "longform:<render id>" (a Stories or
// Audiobook render), "dub:<dub id>" (a dub's pipeline), "workflow:<id>"
// (a workflow's run). Job (GET /api/jobs/{id}, MCP get_job) answers both,
// long-polling up to MaxWait; CancelJob stops both.
//
// Paths: inputs are absolute paths of readable files (~/ works); outputs
// go to the agents' folder (OutputDir) or an absolute folder the caller
// names, never elsewhere. Deleting needs confirm (ErrConfirm).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/hehehai/AudioSmith/internal/jobs"
)

// Studio is what the app offers agents beyond speech and transcription.
// Every method is goroutine-safe. A Backend that also implements Studio
// enables the routes and tools of this file; without it they answer 501
// unsupported.
type Studio interface {
	// Projects lists the project library (the Projects page).
	Projects(ctx context.Context, q ProjectQuery) ([]ProjectJSON, error)

	// CreateLongform makes and saves a Stories or Audiobook project.
	CreateLongform(ctx context.Context, req LongformCreate) (LongformJSON, error)
	Longform(ctx context.Context, id string) (LongformJSON, error)
	// UpdateLongform changes a project's voices and settings.
	UpdateLongform(ctx context.Context, req LongformUpdate) (LongformJSON, error)
	// RenderLongform starts a render: job "longform:<render id>".
	RenderLongform(ctx context.Context, req LongformRender) (JobJSON, error)
	// ExportLongform copies or encodes a finished render's files into
	// req.Dir (already checked: absolute, created).
	ExportLongform(ctx context.Context, req LongformExport) (ExportJSON, error)

	CreateDub(ctx context.Context, req DubCreate) (DubJSON, error)
	Dub(ctx context.Context, req DubQuery) (DubJSON, error)
	// RunDub runs the pipeline: job "dub:<id>".
	RunDub(ctx context.Context, req DubRun) (JobJSON, error)
	EditDub(ctx context.Context, req DubEdit) (DubJSON, error)
	// ExportDub queues an export: a queue job (req.Output already checked).
	ExportDub(ctx context.Context, req DubExport) (JobJSON, error)
	BatchDub(ctx context.Context, req BatchRequest) (BatchJSON, error)
	Batch(ctx context.Context) (BatchJSON, error)

	SearchGallery(ctx context.Context, q GalleryQuery) (GalleryPage, error)
	// UseGalleryVoice saves an archetype as a designed voice, as the
	// gallery's "Use voice" does (once: a second use finds it).
	UseGalleryVoice(ctx context.Context, id string) (GalleryUse, error)

	// ExportTake writes a take with the Export audio dialog's options
	// (req.Output already checked).
	ExportTake(ctx context.Context, req TakeExport) (ExportJSON, error)

	Workflows(ctx context.Context) ([]WorkflowJSON, error)
	// RunWorkflow runs a saved workflow: job "workflow:<id>".
	RunWorkflow(ctx context.Context, req WorkflowRun) (JobJSON, error)

	// Task is a group job's state (ids with a GroupPrefixes prefix);
	// CancelTask stops it.
	Task(ctx context.Context, id string) (JobJSON, error)
	CancelTask(ctx context.Context, id string) error

	// Delete deletes a Stories or Audiobook project (kind "project") or a
	// dub ("dub"). Callers checked the confirmation.
	Delete(ctx context.Context, kind, id string) error
}

// GroupPrefixes are the prefixes of group job ids: the owners of their
// jobs.
var GroupPrefixes = []string{"longform:", "dub:", "workflow:"}

// IsGroupJob reports whether id names a group of jobs.
func IsGroupJob(id string) bool {
	for _, p := range GroupPrefixes {
		if strings.HasPrefix(id, p) && len(id) > len(p) {
			return true
		}
	}
	return false
}

// MaxWait bounds a job request's long poll.
const MaxWait = 60 * time.Second

// Codes of this file's errors.
const (
	CodeConfirmRequired = "confirm_required"
	CodeUnsupported     = "unsupported"
	CodeLLMUnavailable  = "llm_unavailable" // the AI director has no language model
	CodeDirectorFailed  = "director_failed" // the language model failed (timeout, unreadable answer…)
)

// ErrConfirm is the error of a delete without confirm.
func ErrConfirm(what string) *Error {
	return &Error{Status: http.StatusBadRequest, Code: CodeConfirmRequired, Param: "confirm",
		Message: "Deleting " + what + " cannot be undone: ask the user, then call again with confirm: true."}
}

func errNoStudio() *Error {
	return &Error{Status: http.StatusNotImplemented, Code: CodeUnsupported, Message: "This AudioSmith does not offer that."}
}

// Studio is the backend's Studio, or an error.
func (s *Server) Studio() (Studio, error) {
	if st, ok := s.b.(Studio); ok {
		return st, nil
	}
	return nil, errNoStudio()
}

// --- paths ------------------------------------------------------------------------

// OutputDir is where agents' files go unless they name a folder: the MCP
// folder of Settings → Local API (default <data>/mcp).
func (s *Server) OutputDir() string {
	if d := strings.TrimSpace(s.b.Settings().API.MCPDir); d != "" {
		return d
	}
	return filepath.Join(s.b.DataDir(), "mcp")
}

// expandHome turns "~/x" into the home folder's x.
func expandHome(p string) string {
	p = strings.TrimSpace(p)
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p[1:], "/"))
		}
	}
	return p
}

// InputFile resolves an input path: absolute (~/ works), an existing,
// readable regular file. It returns the path with symlinks resolved.
func InputFile(param, path string) (string, error) {
	p := expandHome(path)
	if p == "" {
		return "", errInvalid(param, "%s is empty: give the absolute path of a file.", param)
	}
	if !filepath.IsAbs(p) {
		return "", errInvalid(param, "%s must be an absolute path (%s).", param, path)
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", &Error{Status: http.StatusNotFound, Code: CodeNotFound, Param: param, Message: "There is no file " + p + "."}
		}
		return "", errInvalid(param, "%s cannot be read: %v", p, err)
	}
	fi, err := os.Stat(real)
	if err != nil {
		return "", errInvalid(param, "%s cannot be read: %v", p, err)
	}
	if !fi.Mode().IsRegular() {
		return "", errInvalid(param, "%s is not a file.", p)
	}
	f, err := os.Open(real)
	if err != nil {
		return "", errInvalid(param, "%s cannot be read: %v", p, err)
	}
	f.Close()
	return real, nil
}

// InputDir resolves a folder to read (batch): absolute, existing.
func InputDir(param, path string) (string, error) {
	p := expandHome(path)
	if !filepath.IsAbs(p) {
		return "", errInvalid(param, "%s must be an absolute path (%s).", param, path)
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", &Error{Status: http.StatusNotFound, Code: CodeNotFound, Param: param, Message: "There is no folder " + p + "."}
	}
	if fi, err := os.Stat(real); err != nil || !fi.IsDir() {
		return "", errInvalid(param, "%s is not a folder.", p)
	}
	return real, nil
}

// OutputFolder is the folder an export writes to: dir (absolute; made if
// missing), or the agents' folder for "".
func (s *Server) OutputFolder(dir string) (string, error) {
	d := expandHome(dir)
	if d == "" {
		d = s.OutputDir()
	}
	if !filepath.IsAbs(d) {
		return "", errInvalid("directory", "directory must be an absolute path (%s).", dir)
	}
	d = filepath.Clean(d)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", errInvalid("directory", "cannot make %s: %v", d, err)
	}
	return d, nil
}

// SafeName is a file name for name ("" or unusable: fallback) with the
// extension ext (added unless it has it); never a path.
func SafeName(name, fallback, ext string) string {
	name = filepath.Base(strings.TrimSpace(name))
	clean := strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == 0 || unicode.IsControl(r):
			return '-'
		}
		return r
	}, name)
	clean = strings.TrimLeft(strings.TrimSpace(clean), ".")
	if clean == "" || clean == "-" {
		clean = fallback
	}
	if r := []rune(clean); len(r) > 120 {
		clean = string(r[:120])
	}
	if ext != "" && !strings.EqualFold(filepath.Ext(clean), "."+ext) {
		clean += "." + ext
	}
	return clean
}

// OutputFile is the file an export writes: name (a file name only) with
// ext in dir (OutputFolder).
func (s *Server) OutputFile(dir, name, fallback, ext string) (string, error) {
	d, err := s.OutputFolder(dir)
	if err != nil {
		return "", err
	}
	p := filepath.Join(d, SafeName(name, fallback, ext))
	if filepath.Dir(p) != d {
		return "", errInvalid("filename", "filename must be a file name, not a path.")
	}
	return p, nil
}

// --- jobs --------------------------------------------------------------------------

// jobState is a job's state now: a queue job, or a group's.
func (s *Server) jobState(ctx context.Context, id string) (JobJSON, error) {
	if IsGroupJob(id) {
		st, err := s.Studio()
		if err != nil {
			return JobJSON{}, err
		}
		return st.Task(ctx, id)
	}
	j, err := s.job(ctx, id)
	if err != nil {
		return JobJSON{}, err
	}
	out := jobJSON(j, true)
	out.Files = filesOf(j.Result)
	return out, nil
}

// Job is a job's state, after waiting up to wait (≤ MaxWait) for it to
// end; it answers as soon as it ends.
func (s *Server) Job(ctx context.Context, id string, wait time.Duration) (JobJSON, error) {
	wait = min(max(wait, 0), MaxWait)
	var x *watch
	if wait > 0 {
		if IsGroupJob(id) {
			x = s.w.owner(id)
		} else {
			x = s.w.job(id)
		}
		defer x.stop()
	}
	deadline := time.Now().Add(wait)
	for {
		j, err := s.jobState(ctx, id)
		if err != nil || j.Done || wait <= 0 {
			return j, err
		}
		left := time.Until(deadline)
		if left <= 0 {
			return j, nil
		}
		// An event wakes it; a group's state also changes after its jobs'
		// events are applied, so it looks again at least every half second.
		if _, _, err := x.next(ctx, min(left, 500*time.Millisecond)); err != nil {
			return j, nil
		}
	}
}

// CancelJob cancels a queue job or stops a group, and returns its state.
func (s *Server) CancelJob(ctx context.Context, id string) (JobJSON, error) {
	if IsGroupJob(id) {
		st, err := s.Studio()
		if err != nil {
			return JobJSON{}, err
		}
		if _, err := st.Task(ctx, id); err != nil {
			return JobJSON{}, err
		}
		if err := st.CancelTask(ctx, id); err != nil {
			return JobJSON{}, err
		}
		return s.jobState(ctx, id)
	}
	if _, err := s.job(ctx, id); err != nil {
		return JobJSON{}, err
	}
	if err := s.sched.Cancel(ctx, id); err != nil {
		return JobJSON{}, err
	}
	return s.jobState(ctx, id)
}

// filesOf finds the absolute paths a job's result names (output, file,
// book, encoded, files…).
func filesOf(result json.RawMessage) []string {
	if len(result) == 0 {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(result, &m) != nil {
		return nil
	}
	var out []string
	add := func(v any) {
		if s, ok := v.(string); ok && filepath.IsAbs(s) {
			out = append(out, s)
		}
	}
	for _, k := range []string{"output", "file", "path", "encoded", "book"} {
		add(m[k])
	}
	if l, ok := m["files"].([]any); ok {
		for _, v := range l {
			add(v)
		}
	}
	return out
}

// GroupJob is a group job's JSON from its parts (for Studio.Task).
func GroupJob(id, kind, title string, state jobs.State, stage string, fraction float64, message string, err *jobs.Error, files []string, result any, parts []string) JobJSON {
	j := JobJSON{ID: id, Object: "job", Kind: kind, State: string(state), Title: title, Owner: id, Files: files, Jobs: parts,
		Done: state.Terminal() || state == jobs.StateInterrupted}
	if !j.Done || stage != "" {
		j.Progress = &ProgressJSON{Stage: stage, Fraction: fraction, Message: message}
	}
	if err != nil {
		j.Error = &JobErrorJSON{Code: err.Code, Message: err.Message, Data: err.Data, Retryable: err.Retryable}
	}
	if result != nil {
		if data, e := json.Marshal(result); e == nil {
			j.Result = data
		}
	}
	return j
}

// Accepted is the answer of a call that started a job.
func Accepted(j JobJSON) map[string]any {
	return map[string]any{"job_id": j.ID, "state": j.State, "title": j.Title, "job_url": "/api/jobs/" + j.ID,
		"events_url": "/api/jobs/" + j.ID + "/events"}
}

// --- deleting ----------------------------------------------------------------------

// Delete kinds.
const (
	DeleteTake       = "take"
	DeleteVoice      = "voice"
	DeleteTranscript = "transcript"
	DeleteProject    = "project" // a Stories or Audiobook project
	DeleteDub        = "dub"
	DeleteIsolation  = "isolation" // a Voice isolator's stems (kit.go)
)

// DeleteKinds lists them.
var DeleteKinds = []string{DeleteTake, DeleteVoice, DeleteTranscript, DeleteProject, DeleteDub, DeleteIsolation}

// Delete deletes an item the app's pages can delete, if confirm.
func (s *Server) Delete(ctx context.Context, kind, id string, confirm bool) error {
	if strings.TrimSpace(id) == "" {
		return errInvalid("id", "id is empty.")
	}
	var what string
	switch kind {
	case DeleteTake:
		what = "a take"
	case DeleteVoice:
		what = "a voice"
	case DeleteTranscript:
		what = "a transcript"
	case DeleteProject:
		what = "a project"
	case DeleteDub:
		what = "a dub"
	case DeleteIsolation:
		what = "an isolation and its stems"
	default:
		return errInvalid("kind", "kind is one of %s.", strings.Join(DeleteKinds, ", "))
	}
	if !confirm {
		return ErrConfirm(what)
	}
	switch kind {
	case DeleteTake:
		return s.b.DeleteTake(ctx, id)
	case DeleteVoice:
		return s.b.DeleteVoice(ctx, id)
	case DeleteTranscript:
		return s.b.DeleteTranscript(ctx, id)
	case DeleteIsolation:
		k, err := s.Toolkit()
		if err != nil {
			return err
		}
		return k.DeleteIsolation(ctx, id)
	}
	st, err := s.Studio()
	if err != nil {
		return err
	}
	return st.Delete(ctx, kind, id)
}

// --- transcripts ------------------------------------------------------------------------

// TranscriptExport writes a transcript as a file.
type TranscriptExport struct {
	ID       string `json:"transcript_id" jsonschema:"The transcript's id (transcribe returns it; GET /api/transcripts lists them)."`
	Format   string `json:"format,omitempty" jsonschema:"txt (default), srt, vtt or json."`
	Dir      string `json:"directory,omitempty" jsonschema:"Absolute folder to write to; empty: AudioSmith's agents' folder."`
	Filename string `json:"filename,omitempty" jsonschema:"The file's name (no folders); empty: from the transcript's title."`
}

// errorf is an error with a code and a status.
func errorf(status int, code, param, format string, args ...any) *Error {
	return &Error{Status: status, Code: code, Param: param, Message: fmt.Sprintf(format, args...)}
}
