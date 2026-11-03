package api

// The REST routes of the Studio operations (thread #35). Calls that
// start work answer 202 with the job (job_id, job_url, events_url);
// GET /api/jobs/{id}?wait=N long-polls it.

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// studioRoutes registers the routes of studio.go (routes.go calls it).
// Every route reads or writes files or the library, so it is for this
// Mac (or the key) only, except reading the gallery.
func (s *Server) studioRoutes() {
	s.handle("GET /api/projects", accessAdmin, s.httpProjects)
	s.handle("POST /api/audiobooks", accessAdmin, s.httpCreateLongform("audiobook"))
	s.handle("POST /api/stories", accessAdmin, s.httpCreateLongform("story"))
	s.handle("GET /api/longform/{id}", accessAdmin, s.httpLongform)
	s.handle("PATCH /api/longform/{id}", accessAdmin, s.httpUpdateLongform)
	s.handle("POST /api/longform/{id}/render", accessAdmin, s.httpRenderLongform)
	s.handle("POST /api/longform/{id}/export", accessAdmin, s.httpExportLongform)
	s.handle("DELETE /api/longform/{id}", accessAdmin, s.httpDelete(DeleteProject))

	s.handle("POST /api/dubs", accessAdmin, s.httpCreateDub)
	s.handle("GET /api/dubs/{id}", accessAdmin, s.httpDub)
	s.handle("PATCH /api/dubs/{id}", accessAdmin, s.httpEditDub)
	s.handle("POST /api/dubs/{id}/run", accessAdmin, s.httpRunDub)
	s.handle("POST /api/dubs/{id}/export", accessAdmin, s.httpExportDub)
	s.handle("DELETE /api/dubs/{id}", accessAdmin, s.httpDelete(DeleteDub))
	s.handle("GET /api/batch", accessAdmin, s.httpBatch)
	s.handle("POST /api/batch", accessAdmin, s.httpBatchDub)

	s.handle("GET /api/gallery", accessConsume, s.httpGallery)
	s.handle("GET /api/gallery/{id}", accessConsume, s.httpGalleryVoice)
	s.handle("POST /api/gallery/{id}/use", accessAdmin, s.httpUseGallery)

	s.handle("POST /api/takes/{id}/export", accessAdmin, s.httpExportTake)
	s.handle("POST /api/transcripts/{id}/export", accessAdmin, s.httpExportTranscript)

	s.handle("GET /api/agent-skill", accessAdmin, s.httpAgentSkill)

	s.handle("GET /api/workflows", accessAdmin, s.httpWorkflows)
	s.handle("POST /api/workflows/{id}/run", accessAdmin, s.httpRunWorkflow)
}

// body reads an optional JSON body ("" is {}).
func body(w http.ResponseWriter, r *http.Request, v any) error {
	if r.ContentLength == 0 {
		return nil
	}
	return readJSON(w, r, 8<<20, v)
}

func (s *Server) httpProjects(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	l, err := s.Projects(r.Context(), ProjectQuery{Category: q.Get("category"), Search: q.Get("search"),
		Limit: queryInt(r, "limit", 50, 1, 500), Offset: queryInt(r, "offset", 0, 0, MaxOffset)})
	writeList(w, l, err)
}

func (s *Server) httpCreateLongform(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req LongformCreate
		if err := readJSON(w, r, 32<<20, &req); err != nil {
			writeError(w, err)
			return
		}
		req.Kind = kind
		p, err := s.CreateLongform(r.Context(), req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, p)
	}
}

func (s *Server) httpLongform(w http.ResponseWriter, r *http.Request) {
	p, err := s.Longform(r.Context(), r.PathValue("id"))
	writeResult(w, http.StatusOK, p, err)
}

func (s *Server) httpUpdateLongform(w http.ResponseWriter, r *http.Request) {
	var req LongformUpdate
	if err := readJSON(w, r, 1<<20, &req); err != nil {
		writeError(w, err)
		return
	}
	req.ID = r.PathValue("id")
	p, err := s.UpdateLongform(r.Context(), req)
	writeResult(w, http.StatusOK, p, err)
}

func (s *Server) httpRenderLongform(w http.ResponseWriter, r *http.Request) {
	var req LongformRender
	if err := body(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	req.ID = r.PathValue("id")
	j, err := s.RenderLongform(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, Accepted(j))
}

func (s *Server) httpExportLongform(w http.ResponseWriter, r *http.Request) {
	var req LongformExport
	if err := body(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	req.ID = r.PathValue("id")
	out, err := s.ExportLongform(r.Context(), req)
	writeResult(w, http.StatusOK, out, err)
}

// httpDelete deletes a project or a dub: ?confirm=true is required.
func (s *Server) httpDelete(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if err := s.Delete(r.Context(), kind, id, confirmed(r)); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
	}
}

// confirmed reports ?confirm=true.
func confirmed(r *http.Request) bool {
	v, _ := strconv.ParseBool(r.URL.Query().Get("confirm"))
	return v
}

func (s *Server) httpCreateDub(w http.ResponseWriter, r *http.Request) {
	var req DubCreate
	if err := readJSON(w, r, 1<<20, &req); err != nil {
		writeError(w, err)
		return
	}
	d, err := s.CreateDub(r.Context(), req)
	writeResult(w, http.StatusCreated, d, err)
}

func (s *Server) httpDub(w http.ResponseWriter, r *http.Request) {
	req := DubQuery{ID: r.PathValue("id"), Language: r.URL.Query().Get("language")}
	if v := r.URL.Query().Get("segments"); v != "" {
		b, _ := strconv.ParseBool(v)
		req.Segments = &b
	}
	d, err := s.Dub(r.Context(), req)
	writeResult(w, http.StatusOK, d, err)
}

func (s *Server) httpEditDub(w http.ResponseWriter, r *http.Request) {
	var req DubEdit
	if err := readJSON(w, r, 8<<20, &req); err != nil {
		writeError(w, err)
		return
	}
	req.ID = r.PathValue("id")
	d, err := s.EditDub(r.Context(), req)
	writeResult(w, http.StatusOK, d, err)
}

func (s *Server) httpRunDub(w http.ResponseWriter, r *http.Request) {
	var req DubRun
	if err := body(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	req.ID = r.PathValue("id")
	j, err := s.RunDub(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, Accepted(j))
}

func (s *Server) httpExportDub(w http.ResponseWriter, r *http.Request) {
	var req DubExport
	if err := readJSON(w, r, 1<<20, &req); err != nil {
		writeError(w, err)
		return
	}
	req.ID = r.PathValue("id")
	j, err := s.ExportDub(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, Accepted(j))
}

func (s *Server) httpBatch(w http.ResponseWriter, r *http.Request) {
	b, err := s.Batch(r.Context())
	writeResult(w, http.StatusOK, b, err)
}

func (s *Server) httpBatchDub(w http.ResponseWriter, r *http.Request) {
	var req BatchRequest
	if err := readJSON(w, r, 1<<20, &req); err != nil {
		writeError(w, err)
		return
	}
	b, err := s.BatchDub(r.Context(), req)
	writeResult(w, http.StatusAccepted, b, err)
}

func (s *Server) httpGallery(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p, err := s.SearchGallery(r.Context(), GalleryQuery{Query: q.Get("query"), Category: q.Get("category"), Gender: q.Get("gender"),
		Age: q.Get("age"), Language: q.Get("language"),
		Limit: queryInt(r, "limit", 20, 1, 100), Offset: queryInt(r, "offset", 0, 0, 1<<20)})
	writeResult(w, http.StatusOK, p, err)
}

func (s *Server) httpGalleryVoice(w http.ResponseWriter, r *http.Request) {
	p, err := s.SearchGallery(r.Context(), GalleryQuery{ID: r.PathValue("id")})
	if err != nil {
		writeError(w, err)
		return
	}
	if len(p.Items) == 0 {
		writeError(w, errNotFound(CodeNotFound, "There is no gallery voice %q.", r.PathValue("id")))
		return
	}
	writeJSON(w, http.StatusOK, p.Items[0])
}

func (s *Server) httpUseGallery(w http.ResponseWriter, r *http.Request) {
	u, err := s.UseGalleryVoice(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusOK
	if u.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, u)
}

func (s *Server) httpExportTake(w http.ResponseWriter, r *http.Request) {
	var req TakeExport
	if err := body(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	req.ID = r.PathValue("id")
	out, err := s.ExportTake(r.Context(), req)
	writeResult(w, http.StatusOK, out, err)
}

func (s *Server) httpExportTranscript(w http.ResponseWriter, r *http.Request) {
	var req TranscriptExport
	if err := body(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	req.ID = r.PathValue("id")
	out, err := s.ExportTranscript(r.Context(), req)
	writeResult(w, http.StatusOK, out, err)
}

func (s *Server) httpWorkflows(w http.ResponseWriter, r *http.Request) {
	l, err := s.Workflows(r.Context())
	writeList(w, l, err)
}

func (s *Server) httpRunWorkflow(w http.ResponseWriter, r *http.Request) {
	var req WorkflowRun
	if err := body(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	req.ID = r.PathValue("id")
	j, err := s.RunWorkflow(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, Accepted(j))
}

// waitParam is ?wait=N seconds (≤ MaxWait).
func waitParam(r *http.Request) time.Duration {
	n, err := strconv.ParseFloat(strings.TrimSpace(r.URL.Query().Get("wait")), 64)
	if err != nil || n <= 0 {
		return 0
	}
	return min(time.Duration(n*float64(time.Second)), MaxWait)
}

// SkillSource is implemented by backends that serve the agent skill of
// the Integrations page (the setup prompt downloads it). It never holds
// the API key.
type SkillSource interface {
	// AgentSkill is a file of an agent's skill ("SKILL.md",
	// "reference/tools.md") in the UI's language: *Error not_found for an
	// unknown agent, invalid_request for an unknown file.
	AgentSkill(ctx context.Context, agent, file string) (string, error)
}

// httpAgentSkill is GET /api/agent-skill?agent=<slug>&file=<path>.
func (s *Server) httpAgentSkill(w http.ResponseWriter, r *http.Request) {
	src, ok := s.b.(SkillSource)
	if !ok {
		writeError(w, errNoStudio())
		return
	}
	q := r.URL.Query()
	text, err := src.AgentSkill(r.Context(), strings.TrimSpace(q.Get("agent")), firstNonEmpty(strings.TrimSpace(q.Get("file")), "SKILL.md"))
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, text)
}
