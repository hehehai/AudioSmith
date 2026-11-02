package api

// AudioSmith's own API (/api): jobs, models, languages and tools; split
// from native.go.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/lang"
)

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := jobs.Filter{Owner: q.Get("owner"), Kind: q.Get("kind"), Lane: jobs.Lane(q.Get("lane")), Limit: queryInt(r, "limit", 100, 1, 1000)}
	if st := q.Get("state"); st != "" {
		if st == "all" {
			f.States = []jobs.State{jobs.StateWaiting, jobs.StateQueued, jobs.StateRunning, jobs.StateSucceeded,
				jobs.StateFailed, jobs.StateCancelled, jobs.StateInterrupted}
		} else {
			for _, x := range strings.Split(st, ",") {
				f.States = append(f.States, jobs.State(strings.TrimSpace(x)))
			}
		}
	}
	js, err := s.sched.List(r.Context(), f)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]JobJSON, len(js))
	for i, j := range js {
		out[i] = jobJSON(j, false)
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": out})
}

func (s *Server) job(ctx context.Context, id string) (jobs.Job, error) {
	j, err := s.sched.Get(ctx, id)
	if errors.Is(err, jobs.ErrNotFound) {
		return j, errNotFound(CodeNotFound, "There is no job %s.", id)
	}
	return j, err
}

// getJob is a queue job or a group job ("longform:<id>"…, thread #35);
// ?wait=N waits up to N seconds (≤ 60) for it to end.
func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.Job(r.Context(), r.PathValue("id"), waitParam(r))
	writeResult(w, http.StatusOK, j, err)
}

func (s *Server) cancelJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.CancelJob(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	j.Result = nil
	writeJSON(w, http.StatusOK, j)
}

// runEngineJob runs an engine method as a job of the queue (interactive)
// and waits for its result.
func (s *Server) runEngineJob(ctx context.Context, kind string, params any, title string, out any) error {
	data, err := json.Marshal(params)
	if err != nil {
		return err
	}
	owner := "api:" + core.NewID(time.Now())
	x := s.w.owner(owner)
	defer x.stop()
	id, err := s.sched.Submit(ctx, jobs.Spec{Kind: kind, Lane: jobs.Lane(proto.RoleOf(kind)), Priority: jobs.PriorityInteractive,
		Params: data, Title: title, Owner: owner})
	if err != nil {
		return err
	}
	j, err := waitJob(ctx, s.sched, x, id, s.queueTimeout(), nil)
	if err != nil {
		return err
	}
	return jobOutcome(j, out)
}

// --- models and languages --------------------------------------------------

func (s *Server) modelList(ctx context.Context) ([]ModelJSON, error) {
	models, err := s.b.Models(ctx)
	if err != nil {
		return nil, err
	}
	st := s.b.Settings()
	defaults := map[string]bool{defaultTTS(st).ID: true, defaultSTT(st, false, "").ID: true}
	out := make([]ModelJSON, len(models))
	for i, m := range models {
		out[i] = modelJSON(m, defaults[m.Entry.ID])
	}
	return out, nil
}

func (s *Server) listModels(w http.ResponseWriter, r *http.Request) {
	list, err := s.modelList(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	if task := r.URL.Query().Get("task"); task != "" {
		list = slices.DeleteFunc(list, func(m ModelJSON) bool { return !slices.Contains(m.Tasks, task) })
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": list})
}

func (s *Server) getModel(w http.ResponseWriter, r *http.Request) {
	list, err := s.modelList(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	id := r.PathValue("id")
	for _, m := range list {
		if strings.EqualFold(m.ID, id) || strings.EqualFold(m.Repo, id) {
			writeJSON(w, http.StatusOK, m)
			return
		}
	}
	if rm, ok := catalog.Builtin().RemovedModel(id); ok {
		writeJSON(w, http.StatusOK, removedModelJSON(rm))
		return
	}
	writeError(w, errNotFound(CodeModelNotFound, "There is no model %q.", id))
}

func (s *Server) downloadModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	jobID, err := s.DownloadModel(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"model": id, "job_id": jobID, "events_url": "/api/jobs/" + jobID + "/events"})
}

// entryLanguages are the BCP-47 codes a model speaks or understands
// (OmniVoice's 646 and Whisper's 100 come from the language table).
func entryLanguages(e catalog.Entry) []string {
	switch {
	case len(e.Languages) == 0 && e.Family == "omnivoice":
		return lang.OmniVoiceCodes()
	case e.Family == "whisper":
		return lang.WhisperCodes()
	}
	out := make([]string, 0, len(e.Languages))
	for _, l := range e.Languages {
		out = append(out, l.Code)
	}
	return out
}

// LanguageJSON is a language a model speaks.
type LanguageJSON struct {
	Code   string   `json:"code"`
	Name   string   `json:"name"`
	Native string   `json:"native,omitempty"`
	Models []string `json:"models,omitempty"`
}

// Languages lists the languages of a model (catalogue id, repo or
// label), or of every speech model ("").
func (s *Server) Languages(model string) ([]LanguageJSON, error) {
	var entries []catalog.Entry
	if model != "" {
		e, ok := findEntry(model, catalog.TaskTTS)
		if !ok {
			e, _, ok = replaceRemoved(model, false)
		}
		if !ok {
			e, ok = findEntry(model, catalog.TaskSTT)
		}
		if !ok {
			e, _, ok = catalog.Builtin().ReplaceTask(model, catalog.TaskSTT, "")
		}
		if !ok {
			return nil, errNotFound(CodeModelNotFound, "There is no model %q.", model)
		}
		entries = []catalog.Entry{e}
	} else {
		entries = catalog.Builtin().ForTask(catalog.TaskTTS)
	}
	by := map[string]*LanguageJSON{}
	var order []string
	for _, e := range entries {
		for _, code := range entryLanguages(e) {
			j := by[code]
			if j == nil {
				j = &LanguageJSON{Code: code, Name: code}
				if info, ok := lang.Lookup(code); ok {
					j.Name, j.Native = info.Name, info.Native
				}
				by[code] = j
				order = append(order, code)
			}
			if !slices.Contains(j.Models, e.ID) {
				j.Models = append(j.Models, e.ID)
			}
		}
	}
	out := make([]LanguageJSON, 0, len(order))
	for _, c := range order {
		out = append(out, *by[c])
	}
	slices.SortFunc(out, func(a, b LanguageJSON) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

func (s *Server) languages(w http.ResponseWriter, r *http.Request) {
	list, err := s.Languages(r.URL.Query().Get("model"))
	writeList(w, list, err)
}

// --- tools -------------------------------------------------------------------

func (s *Server) toolProbe(w http.ResponseWriter, r *http.Request) {
	var b ProbeRequest
	if err := readJSON(w, r, 64<<10, &b); err != nil {
		writeError(w, err)
		return
	}
	info, err := s.Probe(r.Context(), b)
	writeResult(w, http.StatusOK, info, err)
}

func (s *Server) toolDirection(w http.ResponseWriter, r *http.Request) {
	var b DirectRequest
	if err := readJSON(w, r, 64<<10, &b); err != nil {
		writeError(w, err)
		return
	}
	d, err := s.Direct(r.Context(), b)
	writeResult(w, http.StatusOK, d, err)
}

func (s *Server) toolRateFit(w http.ResponseWriter, r *http.Request) {
	var b FitRequest
	if err := readJSON(w, r, 256<<10, &b); err != nil {
		writeError(w, err)
		return
	}
	// The rewrite asks the writing help, which may be a cloud model on the
	// user's account: not for LAN clients with the PIN only (code review
	// 11-02, N3); the prediction stays open to them.
	if b.Rewrite && r.Header.Get(HeaderVia) == ViaLAN {
		writeError(w, &Error{Status: http.StatusForbidden, Code: CodeUnauthorized,
			Message: "rewrite uses the user's writing help: send the API key (without rewrite the fit is predicted)."})
		return
	}
	res, err := s.Fit(r.Context(), b)
	writeResult(w, http.StatusOK, res, err)
}
