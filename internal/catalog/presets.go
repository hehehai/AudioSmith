package catalog

// Presets are the tuned settings of the recommended models (thread #41's
// evaluation, research/model-eval.md): which models each workflow
// recommends, in which order and for which content language; a speed and
// a quality hint; and the parameters to use when
// the user (or an API request) sets none. presets.json is embedded and
// validated against the catalogue by the tests.

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Workflow is a page (or API use) that picks a speech model.
type Workflow string

const (
	WorkflowStudio    Workflow = "studio"
	WorkflowStory     Workflow = "story"
	WorkflowAudiobook Workflow = "audiobook"
	WorkflowDub       Workflow = "dub"
	WorkflowDesign    Workflow = "design"
)

// Params are generation parameters; nil = not set (the model's own).
type Params struct {
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"topP,omitempty"`
	Steps       *int     `json:"steps,omitempty"`
	Guidance    *float64 `json:"guidance,omitempty"`
	Speed       *float64 `json:"speed,omitempty"`
}

// merge returns p with o's set values on top.
func (p Params) merge(o Params) Params {
	if o.Temperature != nil {
		p.Temperature = o.Temperature
	}
	if o.TopP != nil {
		p.TopP = o.TopP
	}
	if o.Steps != nil {
		p.Steps = o.Steps
	}
	if o.Guidance != nil {
		p.Guidance = o.Guidance
	}
	if o.Speed != nil {
		p.Speed = o.Speed
	}
	return p
}

// PresetDefaults are a model's recommended settings.
type PresetDefaults struct {
	Params
	// Voice is the default built-in speaker by content language ("zh",
	// "en"); a missing language falls back to "en", then any.
	Voice    map[string]string `json:"voice,omitempty"`
	Language string            `json:"language,omitempty"` // "auto" or a code
}

// Preset is one model's entry of presets.json.
type Preset struct {
	// The one line about the model is the catalogue's (Entry.Summary,
	// thread #45).
	Speed   int `json:"speed"`   // 1–3
	Quality int `json:"quality"` // 1–3
	// Languages the model is evaluated for; HideLanguages are those it
	// must not be offered for (Kokoro's Chinese voices).
	Languages     []string                 `json:"languages,omitempty"`
	HideLanguages []string                 `json:"hideLanguages,omitempty"`
	Defaults      PresetDefaults           `json:"defaults"`
	Workflows     map[string]Params        `json:"workflows,omitempty"` // per-workflow overrides of Defaults
	Dub           *struct{ Timing string } `json:"dub,omitempty"`
	// NeedsVoice says the model speaks only with a saved voice (Base):
	// it is no default for a user who has none.
	NeedsVoice string `json:"needsVoice,omitempty"`
}

// ParamsFor are the parameters to use for workflow w.
func (p Preset) ParamsFor(w Workflow) Params {
	return p.Defaults.Params.merge(p.Workflows[string(w)])
}

// VoiceFor is the default built-in speaker for content language lang.
func (p Preset) VoiceFor(lang string) string {
	if v := p.Defaults.Voice[BaseLanguage(lang)]; v != "" {
		return v
	}
	if v := p.Defaults.Voice["en"]; v != "" {
		return v
	}
	for _, v := range p.Defaults.Voice {
		return v
	}
	return ""
}

// Presets is presets.json.
type Presets struct {
	Version int `json:"version"`
	// DefaultsByLanguage: the model a new user gets per content language
	// and workflow; "noVoiceFallback" replaces a model that needs a saved
	// voice when there is none, "<workflow>WithVoice" is the choice once
	// the user has saved voices.
	DefaultsByLanguage map[string]map[string]string `json:"defaultsByLanguage"`
	// RecommendByLanguage lists each workflow's recommended models, best
	// first, per content language ("en" serves the languages not listed).
	RecommendByLanguage map[string]map[string][]string `json:"recommendByLanguage"`
	// RecommendByUse lists, per use (presets_use.go: other languages, a
	// directed clone, a Chinese dialect) and workflow, models recommended
	// on top of the language's when the content calls for that use. They
	// are recommendations only: no default comes from them.
	RecommendByUse map[string]map[string][]string `json:"recommendByUse"`
	Models         map[string]Preset              `json:"models"`
}

//go:embed presets.json
var presetsJSON []byte

var builtinPresets = func() *Presets {
	var p Presets
	if err := json.Unmarshal(presetsJSON, &p); err != nil {
		panic("presets.json: " + err.Error()) // caught by TestPresets
	}
	return &p
}()

// BuiltinPresets returns the embedded presets. Do not modify them.
func BuiltinPresets() *Presets { return builtinPresets }

// PresetFor is the preset of the model with id.
func PresetFor(id string) (Preset, bool) {
	p, ok := builtinPresets.Models[id]
	return p, ok
}

// BaseLanguage is a content language as presets name it: "zh-Hans" → "zh".
func BaseLanguage(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if i := strings.IndexAny(tag, "-_"); i >= 0 {
		tag = tag[:i]
	}
	return tag
}

func (p *Presets) byLanguage(lang string) (rec map[string][]string, defs map[string]string) {
	l := BaseLanguage(lang)
	rec, ok := p.RecommendByLanguage[l]
	if !ok {
		rec = p.RecommendByLanguage["en"]
	}
	defs, ok = p.DefaultsByLanguage[l]
	if !ok {
		defs = p.DefaultsByLanguage["en"]
	}
	return rec, defs
}

// Recommended are workflow w's recommended models for content language
// lang, best first (hidden ones left out). A language presets don't list
// gets the "en" list, or, when no model there clones or designs in it,
// the models recommended for other languages (presets_use.go).
func (c *Catalog) Recommended(w Workflow, lang string) []Entry {
	rec, _ := builtinPresets.byLanguage(lang)
	out := c.entries(rec[string(w)])
	if _, listed := builtinPresets.RecommendByLanguage[BaseLanguage(lang)]; !listed {
		out = c.coverLanguages(w, out, []string{lang})
	}
	return out
}

// entries are the visible entries of ids, in order.
func (c *Catalog) entries(ids []string) []Entry {
	var out []Entry
	for _, id := range ids {
		if e, ok := c.ByID(id); ok && !e.Hidden {
			out = append(out, e)
		}
	}
	return out
}

// IsRecommended reports whether id is among Recommended(w, lang).
func (c *Catalog) IsRecommended(w Workflow, lang, id string) bool {
	return slices.ContainsFunc(c.Recommended(w, lang), func(e Entry) bool { return e.ID == id })
}

// DefaultModel is the model a new user gets for workflow w in content
// language lang; haveVoice says whether they have saved voices (models
// that need one fall back to one that does not).
func (c *Catalog) DefaultModel(w Workflow, lang string, haveVoice bool) (Entry, bool) {
	rec, defs := builtinPresets.byLanguage(lang)
	id := defs[string(w)]
	if haveVoice {
		if v := defs[string(w)+"WithVoice"]; v != "" {
			id = v
		}
	} else if p, ok := PresetFor(id); ok && p.NeedsVoice != "" && w != WorkflowDub {
		if f := defs["noVoiceFallback"]; f != "" {
			id = f
		}
	}
	if e, ok := c.ByID(id); ok {
		return e, true
	}
	// The language's own list: models recommended by use (VoxCPM2 for
	// other languages) are never a default (thread #167).
	if l := c.entries(rec[string(w)]); len(l) > 0 {
		return l[0], true
	}
	return Entry{}, false
}

// Validate checks the presets name catalogue models that speak, that
// every workflow lists models, and that hints are in range.
func (p *Presets) Validate(c *Catalog) error {
	tts := func(id string) error {
		e, ok := c.ByID(id)
		switch {
		case !ok:
			return fmt.Errorf("unknown model %q", id)
		case !slices.Contains(e.Tasks, TaskTTS):
			return fmt.Errorf("%s does not speak", id)
		case e.Hidden:
			return fmt.Errorf("%s is hidden", id)
		}
		return nil
	}
	for id, m := range p.Models {
		if _, ok := c.ByID(id); !ok {
			return fmt.Errorf("presets: unknown model %q", id)
		}
		if m.Speed < 1 || m.Speed > 3 || m.Quality < 1 || m.Quality > 3 {
			return fmt.Errorf("presets: %s: speed and quality (1–3) are required", id)
		}
	}
	for lang, ws := range p.RecommendByLanguage {
		for w, ids := range ws {
			if len(ids) == 0 || len(ids) > 3 {
				return fmt.Errorf("presets: %s %s: 1–3 recommended models", lang, w)
			}
			for _, id := range ids {
				if err := tts(id); err != nil {
					return fmt.Errorf("presets: %s %s: %w", lang, w, err)
				}
				if _, ok := p.Models[id]; !ok {
					return fmt.Errorf("presets: %s %s: %s has no preset", lang, w, id)
				}
			}
		}
	}
	for use, ws := range p.RecommendByUse {
		for w, ids := range ws {
			if len(ids) == 0 || len(ids) > 3 {
				return fmt.Errorf("presets: use %s %s: 1–3 recommended models", use, w)
			}
			for _, id := range ids {
				if err := tts(id); err != nil {
					return fmt.Errorf("presets: use %s %s: %w", use, w, err)
				}
			}
		}
	}
	for lang, defs := range p.DefaultsByLanguage {
		for k, id := range defs {
			if err := tts(id); err != nil {
				return fmt.Errorf("presets: default %s %s: %w", lang, k, err)
			}
		}
	}
	return nil
}
