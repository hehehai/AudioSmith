package api

import (
	"net/http"
)

// Route is one route of the API, for the spec test and the settings
// page's endpoint list.
type Route struct {
	Pattern string // "POST /v1/audio/speech"
	Access  string // "public", "consume", "admin"
}

// routes registers every route. Patterns use net/http's syntax; the
// guard classifies requests by the pattern they match.
func (s *Server) routes() {
	// Discovery.
	s.handle("GET /health", accessPublic, s.health)
	s.handle("GET /.well-known/audiosmith-speech", accessPublic, s.capabilities)
	s.handle("GET /openapi.json", accessPublic, s.openapi)
	s.handle("GET /docs", accessPublic, s.docs)
	s.handle("GET /docs/{file}", accessPublic, s.docsAsset)
	s.handle("GET /{$}", accessPublic, s.root)

	// OpenAI.
	s.handle("POST /v1/audio/speech", accessConsume, s.v1Speech)
	s.handle("POST /v1/audio/transcriptions", accessConsume, s.v1Transcriptions)
	s.handle("POST /v1/audio/translations", accessConsume, s.v1Translations)
	s.handle("GET /v1/models", accessConsume, s.v1Models)
	s.handle("GET /v1/models/{id}", accessConsume, s.v1Model)
	s.handle("GET /v1/audio/voices", accessConsume, s.v1Voices)

	// Native.
	s.handle("GET /api/voices", accessConsume, s.listVoices)
	s.handle("POST /api/voices", accessAdmin, s.createVoice)
	s.handle("POST /api/voices/design", accessAdmin, s.designVoice)
	s.handle("GET /api/voices/{id}", accessConsume, s.getVoice)
	s.handle("GET /api/voices/{id}/audio", accessConsume, s.voiceAudio)
	s.handle("DELETE /api/voices/{id}", accessAdmin, s.deleteVoice)

	s.handle("POST /api/generate", accessConsume, s.generate)
	s.handle("GET /api/takes", accessAdmin, s.listTakes)
	s.handle("GET /api/takes/{id}", accessAdmin, s.getTake)
	s.handle("GET /api/takes/{id}/audio", accessConsume, s.takeAudio)
	s.handle("DELETE /api/takes/{id}", accessAdmin, s.deleteTake)

	s.handle("GET /api/transcripts", accessAdmin, s.listTranscripts)
	s.handle("POST /api/transcripts", accessConsume, s.createTranscript)
	s.handle("GET /api/transcripts/{id}", accessAdmin, s.getTranscript)
	s.handle("DELETE /api/transcripts/{id}", accessAdmin, s.deleteTranscript)

	s.handle("GET /api/jobs", accessAdmin, s.listJobs)
	s.handle("GET /api/jobs/events", accessAdmin, s.allJobEvents)
	s.handle("GET /api/jobs/{id}", accessAdmin, s.getJob)
	s.handle("POST /api/jobs/{id}/cancel", accessAdmin, s.cancelJob)
	s.handle("GET /api/jobs/{id}/events", accessAdmin, s.jobEvents)

	s.handle("GET /api/models", accessConsume, s.listModels)
	s.handle("GET /api/models/{id}", accessConsume, s.getModel)
	s.handle("POST /api/models/{id}/download", accessAdmin, s.downloadModel)
	s.handle("GET /api/languages", accessConsume, s.languages)

	s.handle("POST /api/tools/probe", accessAdmin, s.toolProbe)
	s.handle("POST /api/tools/direction", accessConsume, s.toolDirection)
	s.handle("POST /api/tools/rate-fit", accessConsume, s.toolRateFit)
	s.handle("POST /api/watermark/detect", accessConsume, s.watermarkDetect) // watermark.go (thread #14)

	s.handle("GET /ws/tts", accessConsume, s.wsTTS)

	s.studioRoutes() // thread #35: studio_http.go — books, stories, dubs, batch, gallery, exports, workflows
	s.kitRoutes()    // thread #135: kit_http.go — isolator, assets, samples, voice management, reference check, story draft

	s.handle("/mcp", accessConsume, s.serveMCP)
	s.handle("/mcp/", accessConsume, s.serveMCP)
}

// Routes lists the routes and who may call them.
func (s *Server) Routes() []Route {
	names := map[access]string{accessPublic: "public", accessConsume: "consume", accessAdmin: "admin"}
	var out []Route
	for p, a := range s.access {
		out = append(out, Route{Pattern: p, Access: names[a]})
	}
	return out
}

func (s *Server) serveMCP(w http.ResponseWriter, r *http.Request) {
	h := s.mcp.Load()
	if h == nil {
		writeError(w, &Error{Status: http.StatusServiceUnavailable, Code: "mcp_unavailable", Message: "The MCP server is not running."})
		return
	}
	(*h).ServeHTTP(w, r)
}

func (s *Server) root(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"name": "AudioSmith", "version": s.o.Version, "source_url": SourceURL, "license": License,
		"openai_base_url": "/v1", "openapi": "/openapi.json", "docs": "/docs", "mcp": "/mcp", "health": "/health",
	})
}
