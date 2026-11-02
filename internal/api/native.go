package api

// AudioSmith's own API (/api): voices, generation, takes, transcripts,
// models, languages and tools, plus the capabilities document (/health is
// in health.go).
//
// Files: native.go (capabilities, voices), native_takes.go (generation,
// takes, transcripts), native_models.go (jobs, models, languages, tools).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
)

// capabilities is GET /.well-known/audiosmith-speech: what this server
// offers, for agents and scripts that discover it.
func (s *Server) capabilities(w http.ResponseWriter, r *http.Request) {
	st := s.b.Settings()
	speech := []string{FormatWAV, FormatPCM}
	if s.b.CanEncode() {
		speech = append(speech, FormatAAC)
	}
	for _, f := range []string{FormatMP3, FormatOpus, FormatFLAC} { // M7a: ffmpeg
		if s.encodesFormat(f) {
			speech = append(speech, f)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schema":      "audiosmith.speech-capabilities",
		"version":     1,
		"app_version": s.o.Version,
		"source_url":  SourceURL,
		"license":     License,
		"endpoints": map[string]string{
			"openai_base_url": "/v1", "speech": "/v1/audio/speech", "transcriptions": "/v1/audio/transcriptions",
			"translations": "/v1/audio/translations", "models": "/v1/models", "voices": "/v1/audio/voices",
			"generate": "/api/generate", "tts_stream": "/ws/tts", "job_events": "/api/jobs/{id}/events",
			"mcp": "/mcp", "mcp_stdio": "AudioSmith.app/Contents/MacOS/AudioSmith --mcp-stdio",
			"openapi": "/openapi.json", "docs": "/docs", "health": "/health",
		},
		"formats": map[string]any{
			"speech":         speech,
			"speech_stream":  []string{"audio", "sse"},
			"pcm":            "audio/pcm;rate=24000;encoding=s16le;channels=1",
			"transcriptions": []string{"json", "text", "verbose_json", "srt", "vtt"},
		},
		"features": map[string]bool{
			"streaming_tts": true, "word_timestamps": true, "voice_cloning": true, "voice_design": true,
			"translations_via_llm": true, "mcp": s.mcp.Load() != nil,
		},
		"limits": map[string]int{"openai_input_chars": MaxOpenAIInput, "input_chars": MaxInput, "upload_bytes": MaxUpload},
		"auth": map[string]any{
			"loopback_requires_key": st.API.RequireKey, "scheme": "bearer",
			"csrf_header": HeaderCSRF, "pin_header": HeaderPIN, "client_header": HeaderClient,
		},
		"engine": s.b.Status(),
	})
}

// --- voices ----------------------------------------------------------------

func (s *Server) listVoices(w http.ResponseWriter, r *http.Request) {
	q := core.VoiceQuery{Search: r.URL.Query().Get("search"), Kind: core.VoiceKind(r.URL.Query().Get("kind"))}
	vs, err := s.b.Voices(r.Context(), q)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]VoiceJSON, len(vs))
	for i, v := range vs {
		out[i] = lanVoiceJSON(r, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": out})
}

// lanVoiceJSON is voiceJSON without the reference's transcript for LAN
// clients with the PIN only: like the recording, it stays on the Mac
// (code review 11-02, N1).
func lanVoiceJSON(r *http.Request, v core.Voice) VoiceJSON {
	j := voiceJSON(v)
	if r.Header.Get(HeaderVia) == ViaLAN {
		j.RefText = ""
	}
	return j
}

func (s *Server) getVoice(w http.ResponseWriter, r *http.Request) {
	v, err := s.b.Voice(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, lanVoiceJSON(r, v))
}

func (s *Server) voiceAudio(w http.ResponseWriter, r *http.Request) {
	v, err := s.b.Voice(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	// LAN clients with the PIN only get the sample: the reference
	// recording is a voiceprint (security review #100, L7).
	name := v.Sample
	if name == "" && r.Header.Get(HeaderVia) != ViaLAN {
		name = v.RefAudio
	}
	if name == "" {
		writeError(w, errNotFound(CodeNotFound, "The voice %s has no audio.", v.Name))
		return
	}
	serveFile(w, r, s.b.VoiceFile(v, name))
}

// CloneParams makes a cloned voice.
type CloneParams struct {
	Name     string
	File     string // the reference (any audio AVFoundation reads)
	RefText  string // what it says; "" lets the engine listen
	Language string
	Style    string
}

// CloneVoice saves a voice cloned from a file (copied into the library).
func (s *Server) CloneVoice(ctx context.Context, p CloneParams) (core.Voice, error) {
	name := core.CleanName(p.Name)
	if name == "" {
		return core.Voice{}, errInvalid("name", "The voice needs a name.")
	}
	if p.File == "" {
		return core.Voice{}, errInvalid("file", "The voice needs a reference recording.")
	}
	if _, err := os.Stat(p.File); err != nil {
		return core.Voice{}, errInvalid("file", "The reference cannot be read: %v", err)
	}
	v := core.Voice{Name: name, Kind: core.VoiceCloned, RefText: strings.TrimSpace(p.RefText),
		Language: strings.TrimSpace(p.Language), Style: strings.TrimSpace(p.Style), Origin: "api"}
	return s.b.SaveVoice(ctx, v, core.VoiceFiles{Reference: p.File})
}

func (s *Server) createVoice(w http.ResponseWriter, r *http.Request) {
	path, _, err := s.formFile(r, "file")
	if err != nil {
		writeError(w, err)
		return
	}
	defer os.Remove(path)
	v, err := s.CloneVoice(r.Context(), CloneParams{Name: r.FormValue("name"), File: path, RefText: r.FormValue("ref_text"),
		Language: r.FormValue("language"), Style: r.FormValue("style")})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, voiceJSON(v))
}

// DesignParams makes a designed voice.
type DesignParams struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Language    string `json:"language,omitempty"`
	Seed        int64  `json:"seed,omitempty"`  // 0 = a new one
	Model       string `json:"model,omitempty"` // a design model; "" = the first
}

// Designed is a designed voice and how its description was read.
type Designed struct {
	Voice     core.Voice
	Unmatched []string // words the parser could not map
	Model     catalog.Entry
}

// DesignVoice saves a voice made from a description. The voice renders
// with design models as described; others clone its sample once the app
// renders one.
func (s *Server) DesignVoice(ctx context.Context, p DesignParams) (Designed, error) {
	desc := strings.TrimSpace(p.Description)
	if desc == "" {
		return Designed{}, errInvalid("description", "Describe the voice.")
	}
	var e catalog.Entry
	if p.Model != "" {
		var ok bool
		if e, ok = findEntry(p.Model, catalog.TaskTTS); !ok || !e.Has(catalog.CapDesign) {
			return Designed{}, &Error{Status: http.StatusBadRequest, Code: CodeUnsupportedModel, Param: "model",
				Message: fmt.Sprintf("%q is not a model that designs voices.", p.Model)}
		}
	} else {
		for _, c := range catalog.Builtin().ForTask(catalog.TaskTTS) {
			if c.Has(catalog.CapDesign) {
				e = c
				break
			}
		}
	}
	res := vd.Parse(desc)
	seed := p.Seed
	if seed == 0 {
		seed = vd.NewSeed()
	}
	r := vd.Recipe{Description: desc, Attrs: res.Attrs.Canonical(), Seed: vd.FoldSeed(seed)}.Normalize()
	attrs, _ := json.Marshal(r.Attrs)
	name := core.CleanName(p.Name)
	if name == "" {
		name = core.TruncateName(core.CleanName(desc), 41)
	}
	language := strings.TrimSpace(p.Language)
	if language == "" {
		language = r.Attrs.Language()
	}
	v := core.Voice{Name: name, Kind: core.VoiceDesigned, Design: r.Description, DesignAttrs: attrs, Seed: r.Seed,
		Language: language, CreatedWith: e.Repo, Origin: "api"}
	saved, err := s.b.SaveVoice(ctx, v, core.VoiceFiles{})
	if err != nil {
		return Designed{}, err
	}
	var un []string
	for _, t := range res.Unmatched {
		un = append(un, t.Text)
	}
	return Designed{Voice: saved, Unmatched: un, Model: e}, nil
}

func (s *Server) designVoice(w http.ResponseWriter, r *http.Request) {
	var p DesignParams
	if err := readJSON(w, r, 64<<10, &p); err != nil {
		writeError(w, err)
		return
	}
	d, err := s.DesignVoice(r.Context(), p)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"voice": voiceJSON(d.Voice), "unmatched": d.Unmatched, "model": d.Model.ID})
}

func (s *Server) deleteVoice(w http.ResponseWriter, r *http.Request) {
	if err := s.b.DeleteVoice(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": r.PathValue("id"), "deleted": true})
}
