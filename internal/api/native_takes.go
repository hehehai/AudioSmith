package api

// AudioSmith's own API (/api): generation, takes and transcripts; split
// from native.go.

import (
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/services"
)

// generateBody is POST /api/generate: a take's whole recipe.
type generateBody struct {
	Text         string   `json:"text"`
	Model        string   `json:"model"`
	Voice        voiceRef `json:"voice"`
	Instructions string   `json:"instructions"`
	Language     string   `json:"language"`
	Speed        float64  `json:"speed"`
	Seed         int64    `json:"seed"`
	Duration     float64  `json:"duration"`
	Steps        int      `json:"steps"`
	Temperature  float64  `json:"temperature"`
	TopP         float64  `json:"top_p"`
	Guidance     float64  `json:"guidance"`
	Exaggeration float64  `json:"exaggeration"`
	// Normalize ("Even out volume") is on unless false, as in the Studio.
	Normalize *bool `json:"normalize"`
	Bits      int   `json:"bits"`
	// Wait for the take (default); false answers 202 with the job at once.
	Wait       *bool `json:"wait"`
	Background bool  `json:"background"`
}

func (s *Server) generate(w http.ResponseWriter, r *http.Request) {
	var b generateBody
	if err := readJSON(w, r, 2<<20, &b); err != nil {
		writeError(w, err)
		return
	}
	if b.Bits != 0 && b.Bits != 16 && b.Bits != 24 && b.Bits != 32 {
		writeError(w, errInvalid("bits", "bits is 16, 24 or 32."))
		return
	}
	p := SpeechParams{Text: b.Text, Model: b.Model, Voice: string(b.Voice), Instructions: b.Instructions, Language: b.Language,
		Speed: b.Speed, Seed: b.Seed, Duration: b.Duration, Steps: b.Steps, Temperature: b.Temperature, TopP: b.TopP,
		Guidance: b.Guidance, Exaggeration: b.Exaggeration, Normalize: b.Normalize == nil || *b.Normalize, Bits: b.Bits,
		Client: r.Header.Get(HeaderClient), Background: b.Background}
	sp, err := s.StartSpeech(r.Context(), p)
	if err != nil {
		writeError(w, err)
		return
	}
	if b.Wait != nil && !*b.Wait {
		sp.Detach()
		out := map[string]any{"take_id": sp.Recipe.ID, "job_id": sp.JobID, "model": sp.Model.ID,
			"ignored": sp.Ignored, "job_url": "/api/jobs/" + sp.JobID, "events_url": "/api/jobs/" + sp.JobID + "/events",
			"audio_url": "/api/takes/" + sp.Recipe.ID + "/audio"}
		sp.addReplaced(out)
		writeJSON(w, http.StatusAccepted, out)
		return
	}
	res, err := sp.Wait(r.Context(), nil)
	if err != nil {
		writeError(w, err)
		return
	}
	speechHeaders(w.Header(), sp)
	out := map[string]any{"take": takeJSON(res.Take), "job_id": sp.JobID, "ignored": sp.Ignored}
	sp.addReplaced(out)
	writeJSON(w, http.StatusOK, out)
}

// addReplaced adds replaced_by and warning to an answer when the request
// named a removed model.
func (sp *Speech) addReplaced(out map[string]any) {
	if r := sp.Replaced; r != nil {
		out["replaced_by"], out["warning"] = r.ReplacedBy, r.Warning
	}
}

// Detach stops following a speech request without cancelling it.
func (sp *Speech) Detach() { sp.x.stop() }

// MaxOffset is the largest offset a list takes, over REST and MCP alike.
const MaxOffset = 1 << 30

// queryInt reads an integer query parameter within [lo, hi].
func queryInt(r *http.Request, name string, def, lo, hi int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return min(max(n, lo), hi)
}

func (s *Server) listTakes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	tq := core.TakeQuery{Search: q.Get("search"), VoiceID: q.Get("voice_id"), Starred: q.Get("starred") == "true",
		Limit: queryInt(r, "limit", 50, 1, 500), Offset: queryInt(r, "offset", 0, 0, MaxOffset)}
	if m := q.Get("model"); m != "" {
		if e, ok := findEntry(m, catalog.TaskTTS); ok {
			tq.Model = e.Repo
		} else if rm, ok := catalog.Builtin().RemovedModel(m); ok {
			tq.Model = rm.Repo // the takes it made keep its name
		} else {
			tq.Model = m
		}
	}
	ts, err := s.b.Takes(r.Context(), tq)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]TakeJSON, len(ts))
	for i, t := range ts {
		out[i] = takeJSON(t)
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": out})
}

func (s *Server) getTake(w http.ResponseWriter, r *http.Request) {
	t, err := s.b.Take(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, takeJSON(t))
}

func (s *Server) takeAudio(w http.ResponseWriter, r *http.Request) {
	t, err := s.b.Take(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	serveFile(w, r, s.b.TakeAudio(t))
}

func (s *Server) deleteTake(w http.ResponseWriter, r *http.Request) {
	if err := s.b.DeleteTake(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": r.PathValue("id"), "deleted": true})
}

// serveFile serves an audio file (ranges, If-Modified-Since).
func serveFile(w http.ResponseWriter, r *http.Request, path string) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(w, errNotFound(CodeNotFound, "The audio file is missing."))
			return
		}
		writeError(w, err)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		writeError(w, err)
		return
	}
	if strings.HasSuffix(strings.ToLower(path), ".wav") {
		w.Header().Set("Content-Type", "audio/wav")
	}
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}

// --- transcripts -----------------------------------------------------------

func (s *Server) listTranscripts(w http.ResponseWriter, r *http.Request) {
	q := core.TranscriptQuery{Search: r.URL.Query().Get("search"), Limit: queryInt(r, "limit", 50, 1, 500), Offset: queryInt(r, "offset", 0, 0, MaxOffset)}
	ts, err := s.b.Transcripts(r.Context(), q)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]TranscriptJSON, len(ts))
	for i, t := range ts {
		out[i] = transcriptJSON(t, false)
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": out})
}

func (s *Server) getTranscript(w http.ResponseWriter, r *http.Request) {
	t, err := s.b.Transcript(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	switch f := r.URL.Query().Get("format"); f {
	case "", "json":
		writeJSON(w, http.StatusOK, transcriptJSON(t, true))
	case "txt", "text":
		writeTranscript(w, t, "text", "transcribe", false, false)
	case "srt", "vtt":
		writeTranscript(w, t, f, "transcribe", false, false)
	default:
		writeError(w, &Error{Status: http.StatusBadRequest, Code: CodeUnsupportedFormat, Param: "format", Message: "format is json, txt, srt or vtt."})
	}
}

func (s *Server) createTranscript(w http.ResponseWriter, r *http.Request) {
	path, name, err := s.formFile(r, "file")
	if err != nil {
		writeError(w, err)
		return
	}
	defer os.Remove(path)
	words := r.FormValue("words") != "false" // word timings unless asked not to
	tr, err := s.Transcribe(r.Context(), TranscribeParams{File: path, Title: firstNonEmpty(r.FormValue("title"), name), Model: r.FormValue("model"),
		Language: r.FormValue("language"), Prompt: r.FormValue("prompt"), Words: words, Speakers: formBool(r, "speakers")})
	if err != nil {
		writeError(w, err)
		return
	}
	out := map[string]any{"transcript": transcriptJSON(tr.Transcript, true), "warnings": tr.Warnings}
	if r := tr.Replaced; r != nil {
		out["replaced_by"], out["warning"] = r.ReplacedBy, r.Warning
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) deleteTranscript(w http.ResponseWriter, r *http.Request) {
	if err := s.b.DeleteTranscript(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": r.PathValue("id"), "deleted": true})
}

// sanity: services is used for the transcript owner and results.
var _ = services.TranscriptOwner
