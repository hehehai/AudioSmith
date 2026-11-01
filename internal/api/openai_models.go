package api

// OpenAI's audio API (/v1): models, and /v1/audio/voices; split from
// openai.go.

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
)

// openAIModel is an entry of GET /v1/models.
type openAIModel struct {
	ID         string         `json:"id"`
	Object     string         `json:"object"`
	Created    int64          `json:"created"`
	OwnedBy    string         `json:"owned_by"`
	AudioSmith map[string]any `json:"audiosmith"`
}

func (s *Server) openAIModels(ctx context.Context) ([]openAIModel, error) {
	st := s.b.Settings()
	models, err := s.b.Models(ctx)
	if err != nil {
		return nil, err
	}
	tts, stt, fast := defaultTTS(st), defaultSTT(st, false, ""), defaultSTT(st, true, "")
	var out []openAIModel
	for _, a := range ttsAliases {
		out = append(out, openAIModel{ID: a, Object: "model", OwnedBy: "audiosmith",
			AudioSmith: map[string]any{"kind": "tts", "alias_for": tts.ID}})
	}
	for _, a := range sttAliases {
		target := stt.ID
		if fastSTTAlias(a) {
			target = fast.ID
		}
		out = append(out, openAIModel{ID: a, Object: "model", OwnedBy: "audiosmith",
			AudioSmith: map[string]any{"kind": "stt", "alias_for": target}})
	}
	for _, m := range models {
		e := m.Entry
		if e.Hidden {
			continue
		}
		kind := ""
		switch {
		case slices.Contains(e.Tasks, catalog.TaskTTS):
			kind = "tts"
		case slices.Contains(e.Tasks, catalog.TaskSTT):
			kind = "stt"
		default:
			continue
		}
		out = append(out, openAIModel{ID: e.ID, Object: "model", OwnedBy: ownerOf(e.Repo),
			AudioSmith: map[string]any{"kind": kind, "repo": e.Repo, "label": e.Label, "display_name": e.Name, "ui_name": e.DisplayName(), "downloaded": m.Downloaded,
				"licence": e.Licence.ID, "acceptance_needed": m.LicenceNeeded}})
	}
	return out, nil
}

func ownerOf(repo string) string {
	if i := strings.IndexByte(repo, '/'); i > 0 {
		return repo[:i]
	}
	return "audiosmith"
}

// v1Models is GET /v1/models.
func (s *Server) v1Models(w http.ResponseWriter, r *http.Request) {
	list, err := s.openAIModels(r.Context())
	writeList(w, list, err)
}

// v1Model is GET /v1/models/{id}.
func (s *Server) v1Model(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	list, err := s.openAIModels(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	for _, m := range list {
		if strings.EqualFold(m.ID, id) {
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

// v1Voices is GET /v1/audio/voices: what "voice" may name — OpenAI's
// names, saved voices (by id, or by name when it is unique), and the
// models' built-in speakers.
func (s *Server) v1Voices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	voices, err := s.b.Voices(ctx, core.VoiceQuery{})
	if err != nil {
		writeError(w, err)
		return
	}
	st := s.b.Settings()
	def := defaultTTS(st)
	data := []map[string]any{}
	for _, n := range OpenAIVoiceNames() {
		g := openAIVoices[n]
		data = append(data, map[string]any{"id": n, "name": n, "type": "openai", "gender": g,
			"description": "OpenAI's name: a built-in speaker of the default model" + map[bool]string{true: " (" + g + ")", false: ""}[g != ""]})
	}
	names := map[string]int{}
	for _, v := range voices {
		names[strings.ToLower(strings.TrimSpace(v.Name))]++
	}
	for _, v := range voices {
		item := map[string]any{"id": v.ID, "name": v.Name, "type": "saved", "kind": string(v.Kind),
			"addressable_by_name": names[strings.ToLower(strings.TrimSpace(v.Name))] == 1}
		if v.Language != "" {
			item["language"] = v.Language
		}
		if e, ok := catalog.Builtin().ByRepo(v.Model); ok {
			item["model"] = e.ID
		}
		data = append(data, item)
	}
	for _, e := range catalog.Builtin().ForTask(catalog.TaskTTS) {
		for _, sp := range e.Voices {
			item := map[string]any{"id": sp.ID, "name": sp.ID, "type": "builtin", "model": e.ID}
			if sp.Language != "" {
				item["language"] = sp.Language
			}
			if sp.Gender != "" {
				item["gender"] = sp.Gender
			}
			data = append(data, item)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "default_model": def.ID, "data": data})
}

// speakerName is a speaker label's name (the transcript's Speakers), or
// the label.
func speakerName(t core.Transcript, label string) string {
	if n := t.Speakers[label]; n != "" {
		return n
	}
	return label
}
