package api

// The REST routes of the toolkit (kit.go, thread #135). Every route
// reads or writes files or the library, so it is for this Mac (or the
// key) only, except the bundled preview clips.

import (
	"net/http"
	"strconv"
)

// kitRoutes registers the routes of kit.go (routes.go calls it).
func (s *Server) kitRoutes() {
	s.handle("POST /api/isolations", accessAdmin, s.httpStartIsolation)
	s.handle("GET /api/isolations", accessAdmin, s.httpIsolations)
	s.handle("GET /api/isolations/{id}", accessAdmin, s.httpIsolation)
	s.handle("DELETE /api/isolations/{id}", accessAdmin, s.httpDelete(DeleteIsolation))

	s.handle("GET /api/assets", accessAdmin, s.httpAssets)
	s.handle("GET /api/samples", accessAdmin, s.httpSamples)
	s.handle("POST /api/samples/{id}/open", accessAdmin, s.httpOpenSample)

	s.handle("PATCH /api/voices/{id}", accessAdmin, s.httpUpdateVoice)
	s.handle("POST /api/voices/{id}/duplicate", accessAdmin, s.httpDuplicateVoice)
	s.handle("POST /api/voices/{id}/export", accessAdmin, s.httpExportVoice)
	s.handle("POST /api/voices/import", accessAdmin, s.httpImportVoice)
	s.handle("GET /api/gallery/{id}/preview", accessConsume, s.httpPreview(func(r *http.Request) VoicePreviewQuery {
		return VoicePreviewQuery{Gallery: r.PathValue("id")}
	}))
	s.handle("GET /api/models/{id}/voices/{speaker}/preview", accessConsume, s.httpPreview(func(r *http.Request) VoicePreviewQuery {
		return VoicePreviewQuery{Model: r.PathValue("id"), Speaker: r.PathValue("speaker")}
	}))

	s.handle("POST /api/tools/reference-check", accessAdmin, s.httpCheckReference)
	s.handle("POST /api/stories/draft", accessAdmin, s.httpWriteStory)
}

func (s *Server) httpStartIsolation(w http.ResponseWriter, r *http.Request) {
	var req IsolateRequest
	if err := readJSON(w, r, 64<<10, &req); err != nil {
		writeError(w, err)
		return
	}
	x, err := s.StartIsolation(r.Context(), req)
	writeResult(w, http.StatusAccepted, x, err)
}

func (s *Server) httpIsolations(w http.ResponseWriter, r *http.Request) {
	l, err := s.Isolations(r.Context())
	writeList(w, l, err)
}

func (s *Server) httpIsolation(w http.ResponseWriter, r *http.Request) {
	x, err := s.Isolation(r.Context(), r.PathValue("id"))
	writeResult(w, http.StatusOK, x, err)
}

func (s *Server) httpAssets(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	l, err := s.Assets(r.Context(), AssetQuery{Kind: q.Get("kind"), Search: q.Get("search"),
		Limit: queryInt(r, "limit", 50, 1, 500), Offset: queryInt(r, "offset", 0, 0, MaxOffset)})
	writeList(w, l, err)
}

func (s *Server) httpSamples(w http.ResponseWriter, r *http.Request) {
	l, err := s.Samples(r.Context())
	writeList(w, l, err)
}

func (s *Server) httpOpenSample(w http.ResponseWriter, r *http.Request) {
	o, err := s.OpenSample(r.Context(), r.PathValue("id"))
	writeResult(w, http.StatusCreated, o, err)
}

func (s *Server) httpUpdateVoice(w http.ResponseWriter, r *http.Request) {
	var req VoiceUpdate
	if err := readJSON(w, r, 64<<10, &req); err != nil {
		writeError(w, err)
		return
	}
	req.ID = r.PathValue("id")
	v, err := s.UpdateVoice(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, voiceJSON(v))
}

func (s *Server) httpDuplicateVoice(w http.ResponseWriter, r *http.Request) {
	var req VoiceDuplicate
	if err := body(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	req.ID = r.PathValue("id")
	v, err := s.DuplicateVoice(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, voiceJSON(v))
}

func (s *Server) httpExportVoice(w http.ResponseWriter, r *http.Request) {
	var req VoiceExportRequest
	if err := body(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	req.ID = r.PathValue("id")
	out, err := s.ExportVoice(r.Context(), req)
	writeResult(w, http.StatusOK, out, err)
}

func (s *Server) httpImportVoice(w http.ResponseWriter, r *http.Request) {
	var req VoiceImportRequest
	if err := readJSON(w, r, 64<<10, &req); err != nil {
		writeError(w, err)
		return
	}
	v, err := s.ImportVoice(r.Context(), req)
	writeResult(w, http.StatusCreated, v, err)
}

// httpPreview serves a bundled preview clip.
func (s *Server) httpPreview(query func(*http.Request) VoicePreviewQuery) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := s.VoicePreview(r.Context(), query(r))
		if err != nil {
			writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", p.MIME)
		w.Header().Set("Content-Length", strconv.Itoa(len(p.Data)))
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		w.Write(p.Data)
	}
}

func (s *Server) httpCheckReference(w http.ResponseWriter, r *http.Request) {
	var req RefCheckRequest
	if err := readJSON(w, r, 64<<10, &req); err != nil {
		writeError(w, err)
		return
	}
	out, err := s.CheckReference(r.Context(), req)
	writeResult(w, http.StatusOK, out, err)
}

func (s *Server) httpWriteStory(w http.ResponseWriter, r *http.Request) {
	var req StoryDraftRequest
	if err := readJSON(w, r, 64<<10, &req); err != nil {
		writeError(w, err)
		return
	}
	out, err := s.WriteStory(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusOK
	if out.Project != nil {
		status = http.StatusCreated
	}
	writeJSON(w, status, out)
}
