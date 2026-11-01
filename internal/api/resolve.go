package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// OpenAI's model names map to AudioSmith's defaults.
var (
	ttsAliases   = []string{"tts-1", "tts-1-hd", "gpt-4o-mini-tts"}
	sttAliases   = []string{"whisper-1", "gpt-4o-transcribe", "gpt-4o-mini-transcribe"}
	ttsAliasExpr = regexp.MustCompile(`^gpt-[a-z0-9.\-]*-tts(-\d{4}-\d{2}-\d{2})?$`)
	sttAliasExpr = regexp.MustCompile(`^gpt-[a-z0-9.\-]*-transcribe(-\d{4}-\d{2}-\d{2})?$`)
)

// isTTSAlias reports whether name stands for "the default speech model".
func isTTSAlias(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	return n == "" || n == "default" || slices.Contains(ttsAliases, n) || ttsAliasExpr.MatchString(n)
}

// isSTTAlias reports whether name stands for "the default speech-to-text model".
func isSTTAlias(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	return n == "" || n == "default" || slices.Contains(sttAliases, n) || sttAliasExpr.MatchString(n)
}

// isSpeakersAlias reports whether name asks for transcription with
// speakers by OpenAI's name ("gpt-4o-transcribe-diarize"): AudioSmith's
// speakers model answers.
func isSpeakersAlias(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	return strings.HasPrefix(n, "gpt-") && strings.Contains(n, "-transcribe-diarize")
}

// fastSTTAlias: the "mini" names pick the fast model.
func fastSTTAlias(name string) bool { return strings.Contains(strings.ToLower(name), "mini") }

// openAIVoices are OpenAI's voice names, with the gender of the built-in
// speaker each maps to ("" = the model's default speaker).
var openAIVoices = map[string]string{
	"alloy": "", "ash": "male", "ballad": "male", "coral": "female", "echo": "male", "fable": "male",
	"nova": "female", "onyx": "male", "sage": "female", "shimmer": "female", "verse": "male",
	"marin": "female", "cedar": "male",
}

// OpenAIVoiceNames lists them, sorted.
func OpenAIVoiceNames() []string {
	var out []string
	for k := range openAIVoices {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// findEntry finds a catalogue entry by id, repo, short repo, label or
// friendly name (case-insensitive, Entry.Named) that does task.
func findEntry(name string, task catalog.Task) (catalog.Entry, bool) {
	n := strings.TrimSpace(name)
	if n == "" {
		return catalog.Entry{}, false
	}
	for _, e := range catalog.Builtin().Entries {
		if !slices.Contains(e.Tasks, task) {
			continue
		}
		if e.Named(n) {
			return e, true
		}
	}
	return catalog.Entry{}, false
}

// Replacement says a request named a model AudioSmith removed (thread
// #43) and which model answered instead.
type Replacement struct {
	Model      string `json:"model"`       // the removed model's id
	ReplacedBy string `json:"replaced_by"` // the catalogue id that answered
	Warning    string `json:"warning"`
}

// replaceRemoved maps name, when it names a removed model, to its
// replacement (clone: the request clones a voice).
func replaceRemoved(name string, clone bool) (catalog.Entry, *Replacement, bool) {
	e, r, ok := catalog.Builtin().Replace(name, clone)
	if !ok {
		return e, nil, false
	}
	return e, &Replacement{Model: r.ID, ReplacedBy: e.ID, Warning: r.Warning(e)}, true
}

// errRemovedVoice explains a voice the replacement of a removed model
// can't speak with (a built-in speaker of the old model).
func errRemovedVoice(rep *Replacement, e catalog.Entry, why string) *Error {
	r, _ := catalog.Builtin().RemovedModel(rep.Model)
	return &Error{Status: http.StatusBadRequest, Code: CodeModelRemoved, Param: "voice",
		Message: r.Label + " was removed from AudioSmith (" + e.Label + " replaces it): " + why +
			" GET /v1/models/" + e.ID + " lists its voices.",
		Data: map[string]any{"model": rep.Model, "replaced_by": rep.ReplacedBy}}
}

// defaultTTS is the Studio's model (Settings → Studio; a removed one's
// replacement), else the first speech model of the catalogue.
func defaultTTS(st settings.Settings) catalog.Entry {
	if e, ok := catalog.Builtin().ByID(st.Studio.Model); ok && slices.Contains(e.Tasks, catalog.TaskTTS) {
		return e
	}
	if e, _, ok := catalog.Builtin().Replace(st.Studio.Model, false); ok {
		return e
	}
	if l := catalog.Builtin().ForTask(catalog.TaskTTS); len(l) > 0 {
		return l[0]
	}
	return catalog.Entry{}
}

// DefaultModels are the models a request without one gets: speech,
// Transcribe's Accurate and Fast (MCP's get_settings shows them).
func DefaultModels(st settings.Settings) (tts, accurate, fast string) {
	return defaultTTS(st).ID, defaultSTT(st, false, "").ID, defaultSTT(st, true, "").ID
}

// DubTargets are the languages a new dub is made in: Settings →
// Dubbing's, else the default (services.DefaultBatchSettings').
func DubTargets(st settings.Settings) []string {
	if len(st.Dubbing.Targets) > 0 {
		return st.Dubbing.Targets
	}
	return services.DefaultDubTargets()
}

// defaultSTT is Transcribe's Accurate model (fast: its Fast model) when
// Settings → Transcribe names one, else the default for language (""
// detects it): Qwen3-ASR, or Whisper for a language Qwen3-ASR does not
// know (catalog.STTFor, thread #46).
func defaultSTT(st settings.Settings, fast bool, language string) catalog.Entry {
	id := st.Transcribe.AccurateModel
	if fast {
		id = st.Transcribe.FastModel
	}
	if e, ok := catalog.Builtin().ByID(id); ok && slices.Contains(e.Tasks, catalog.TaskSTT) {
		return e
	}
	return catalog.Builtin().STTFor(language, false)
}

// voiceRef is a request's voice: a string, or OpenAI's {"id": "…"}.
type voiceRef string

func (v *voiceRef) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*v = voiceRef(s)
		return nil
	}
	var o struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return err
	}
	*v = voiceRef(firstNonEmpty(o.ID, o.Name))
	return nil
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

// voiceChoice is a resolved voice.
type voiceChoice struct {
	Saved   *core.Voice // a saved voice
	Speaker string      // a built-in speaker of the model
	Gender  string      // an OpenAI voice's gender, for the model to pick a speaker
	Named   string      // the OpenAI name used, if any
}

// resolveVoice finds what a request's voice names, for model e (nil
// while the model follows from the voice): a saved voice by id, then by
// name (case-insensitive; two of that name are ambiguous), a built-in
// speaker of the model, "default" or nothing, or an OpenAI voice name.
func (s *Server) resolveVoice(ctx context.Context, name string, e *catalog.Entry) (voiceChoice, error) {
	n := strings.TrimSpace(name)
	if n == "" || strings.EqualFold(n, "default") {
		return voiceChoice{}, nil
	}
	if v, err := s.b.Voice(ctx, n); err == nil {
		return voiceChoice{Saved: &v}, nil
	}
	all, err := s.b.Voices(ctx, core.VoiceQuery{})
	if err != nil {
		return voiceChoice{}, err
	}
	var named []core.Voice
	for _, v := range all {
		if strings.EqualFold(strings.TrimSpace(v.Name), n) {
			named = append(named, v)
		}
	}
	if len(named) > 1 {
		ids := make([]string, len(named))
		for i, v := range named {
			ids[i] = v.ID
		}
		return voiceChoice{}, &Error{Status: http.StatusConflict, Code: CodeAmbiguousVoice, Param: "voice",
			Message: "Several saved voices are called " + n + ": use one's id.", Data: map[string]any{"voices": ids}}
	}
	if len(named) == 1 {
		return voiceChoice{Saved: &named[0]}, nil
	}
	if e != nil {
		for _, sp := range e.Voices {
			if strings.EqualFold(sp.ID, n) {
				return voiceChoice{Speaker: sp.ID}, nil
			}
		}
	}
	if g, ok := openAIVoices[strings.ToLower(n)]; ok {
		return voiceChoice{Gender: g, Named: strings.ToLower(n)}, nil
	}
	return voiceChoice{}, &Error{Status: http.StatusBadRequest, Code: CodeVoiceNotFound, Param: "voice",
		Message: "No voice is called " + n + ": GET /v1/audio/voices lists them."}
}

// resolveTTS picks the model of a speech request: a catalogue model by
// name (a removed model's replacement: rep says so), or, for OpenAI's
// names, the default model — unless a saved voice needs another (a clone
// on a model that cannot clone): then the voice's own model, or the
// first downloaded model that can speak with it. clone says the request
// gives a reference clip.
func (s *Server) resolveTTS(ctx context.Context, model string, voice string, clone bool) (e catalog.Entry, vc voiceChoice, rep *Replacement, err error) {
	st := s.b.Settings()
	if !isTTSAlias(model) {
		e, ok := findEntry(model, catalog.TaskTTS)
		if !ok {
			if _, removed := catalog.Builtin().RemovedModel(model); removed {
				// A saved voice that is not a built-in speaker clones.
				if v, err := s.resolveVoice(ctx, voice, nil); err == nil && v.Saved != nil && v.Saved.Kind != core.VoiceBuiltin {
					clone = true
				}
				e, rep, ok = replaceRemoved(model, clone)
			}
		}
		if !ok {
			return e, voiceChoice{}, nil, errNotFound(CodeModelNotFound, "There is no speech model %q: GET /v1/models lists them.", model)
		}
		vc, err := s.resolveVoice(ctx, voice, &e)
		if err != nil {
			if rep != nil && apiCode(err) == CodeVoiceNotFound {
				return e, vc, rep, errRemovedVoice(rep, e, e.Label+" has no voice called "+strings.TrimSpace(voice)+".")
			}
			return e, vc, rep, err
		}
		if vc.Saved != nil {
			if ok, why := s.b.VoiceFits(*vc.Saved, e); !ok {
				if rep != nil {
					return e, vc, rep, errRemovedVoice(rep, e, why)
				}
				return e, vc, rep, &Error{Status: http.StatusBadRequest, Code: CodeUnsupportedModel, Param: "voice", Message: why}
			}
		}
		return e, vc, rep, nil
	}
	def := defaultTTS(st)
	vc, err = s.resolveVoice(ctx, voice, &def)
	if err != nil || vc.Saved == nil {
		return def, vc, nil, err
	}
	if ok, _ := s.b.VoiceFits(*vc.Saved, def); ok {
		return def, vc, nil, nil
	}
	var candidates []catalog.Entry
	for _, repo := range []string{vc.Saved.Model, vc.Saved.CreatedWith} {
		if e, ok := catalog.Builtin().ByRepo(repo); ok {
			candidates = append(candidates, e)
		} else if e, _, ok := catalog.Builtin().Replace(repo, vc.Saved.Kind != core.VoiceBuiltin); ok {
			candidates = append(candidates, e)
		}
	}
	candidates = append(candidates, catalog.Builtin().ForTask(catalog.TaskTTS)...)
	models, _ := s.b.Models(ctx)
	downloaded := map[string]bool{}
	for _, m := range models {
		downloaded[m.Entry.Repo] = m.Downloaded
	}
	var fallback *catalog.Entry
	for i := range candidates {
		e := candidates[i]
		if ok, _ := s.b.VoiceFits(*vc.Saved, e); !ok {
			continue
		}
		if downloaded[e.Repo] {
			return e, vc, nil, nil
		}
		if fallback == nil {
			fallback = &candidates[i]
		}
	}
	if fallback != nil {
		return *fallback, vc, nil, nil
	}
	_, why := s.b.VoiceFits(*vc.Saved, def)
	return def, vc, nil, &Error{Status: http.StatusBadRequest, Code: CodeUnsupportedModel, Param: "voice", Message: why}
}

// apiCode is the code of an *Error, or "".
func apiCode(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// resolveSTT picks the speech-to-text model for speech in language (""
// detects it): OpenAI's names and "" the default, a catalogue model by
// any of its names, a removed one (Parakeet) the default for the
// language (rep says so).
func (s *Server) resolveSTT(model, language string) (catalog.Entry, *Replacement, error) {
	st := s.b.Settings()
	if isSTTAlias(model) {
		return defaultSTT(st, fastSTTAlias(model), language), nil, nil
	}
	e, ok := findEntry(model, catalog.TaskSTT)
	if ok {
		return e, nil, nil
	}
	if e, r, ok := catalog.Builtin().ReplaceTask(model, catalog.TaskSTT, language); ok {
		return e, &Replacement{Model: r.ID, ReplacedBy: e.ID, Warning: r.Warning(e)}, nil
	}
	return e, nil, errNotFound(CodeModelNotFound, "There is no speech-to-text model %q: GET /v1/models lists them.", model)
}
