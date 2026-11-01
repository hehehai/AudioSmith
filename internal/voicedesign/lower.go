package voicedesign

// Lowering a recipe for an engine: targets, options, the engine registry
// and Lower. Files: lower.go, one per target: lower_freeform.go (Qwen3-TTS
// VoiceDesign), lower_omnivoice.go (OmniVoice), lower_customvoice.go
// (Qwen3-TTS CustomVoice).

import (
	"fmt"
	"strings"
	"sync"
	"unicode"
)

// Target names one way of feeding a recipe to an engine.
type Target string

const (
	// TargetQwen3Design: Qwen3-TTS VoiceDesign takes a free-form English or
	// Chinese description in its voice field.
	TargetQwen3Design Target = "qwen3-voicedesign"
	// TargetOmniVoice: OmniVoice takes comma-separated attribute tags from
	// a closed vocabulary, one per category; other text is ignored.
	TargetOmniVoice Target = "omnivoice"
	// TargetQwen3CustomVoice: Qwen3-TTS CustomVoice takes "Speaker,
	// instruction": a named speaker plus a style/emotion instruction.
	TargetQwen3CustomVoice Target = "qwen3-customvoice"
)

// Vocabulary is how an engine reads the voice field.
type Vocabulary string

const (
	VocabFreeform Vocabulary = "freeform" // the description as written + explicit picks
	VocabTags     Vocabulary = "tags"     // attribute tags only
	VocabSpeaker  Vocabulary = "speaker"  // a fixed speaker + delivery instruction
)

// Options tune a lowering.
type Options struct {
	// Lang is the language of the generated voice text: "en", "zh" or ""
	// (auto: the description's language, else zh for a dialect, else en).
	Lang string
	// Speaker forces a named speaker (CustomVoice).
	Speaker string
	// Speakers limits the choice to the speakers the model has (CustomVoice;
	// empty = all known speakers).
	Speakers []string
}

// Dropped is a value an engine can't express.
type Dropped struct {
	Value
	Reason string `json:"reason"`
}

// Lowered is a recipe in an engine's terms.
type Lowered struct {
	Target  Target    `json:"target"`
	Voice   string    `json:"voice"`             // the voice/instruct field; "" = the model's auto voice
	Lang    string    `json:"lang"`              // language of Voice: "en" or "zh"
	Speaker string    `json:"speaker,omitempty"` // CustomVoice: the speaker chosen
	Seed    int64     `json:"seed"`
	Used    []Value   `json:"used,omitempty"`    // values that made it into Voice
	Dropped []Dropped `json:"dropped,omitempty"` // values the engine can't express
	// DescriptionDropped: the engine ignores free text, so only the mapped
	// attributes count (UI: "This engine only understands the details").
	DescriptionDropped bool `json:"descriptionDropped,omitempty"`
}

// Engine is one row of the lowering table. New design-capable engines add
// a row with Register.
type Engine struct {
	Target     Target
	Label      string
	Vocabulary Vocabulary
	// Match reports whether a catalogue model (its engine family and repo)
	// uses this lowering.
	Match func(family, repo string) bool
	Lower func(r Recipe, o Options) Lowered
}

var (
	engMu   sync.RWMutex
	engines = []Engine{
		{
			Target: TargetQwen3Design, Label: "Qwen3-TTS VoiceDesign", Vocabulary: VocabFreeform,
			Match: func(f, repo string) bool {
				return f == "qwen3_tts" && strings.Contains(strings.ToLower(repo), "voicedesign")
			},
			Lower: lowerFreeform,
		},
		{
			Target: TargetOmniVoice, Label: "OmniVoice", Vocabulary: VocabTags,
			Match: func(f, _ string) bool { return f == "omnivoice" },
			Lower: lowerOmniVoice,
		},
		{
			Target: TargetQwen3CustomVoice, Label: "Qwen3-TTS CustomVoice", Vocabulary: VocabSpeaker,
			Match: func(f, repo string) bool {
				return f == "qwen3_tts" && strings.Contains(strings.ToLower(repo), "customvoice")
			},
			Lower: lowerCustomVoice,
		},
	}
)

// Register adds or replaces (same Target) a lowering.
func Register(e Engine) {
	engMu.Lock()
	defer engMu.Unlock()
	for i := range engines {
		if engines[i].Target == e.Target {
			engines[i] = e
			return
		}
	}
	engines = append(engines, e)
}

// EngineFor finds the lowering for a catalogue model; false when the
// model can't design voices.
func EngineFor(family, repo string) (Engine, bool) {
	engMu.RLock()
	defer engMu.RUnlock()
	for _, e := range engines {
		if e.Match != nil && e.Match(family, repo) {
			return e, true
		}
	}
	return Engine{}, false
}

// Lower turns a recipe into the voice field for target.
func Lower(t Target, r Recipe, o Options) (Lowered, error) {
	engMu.RLock()
	var e *Engine
	for i := range engines {
		if engines[i].Target == t {
			e = &engines[i]
		}
	}
	engMu.RUnlock()
	if e == nil {
		return Lowered{}, fmt.Errorf("voicedesign: no lowering for %q", t)
	}
	r = r.Normalize()
	l := e.Lower(r, o)
	l.Target, l.Seed = t, r.Seed
	return l, nil
}

// TextLang guesses whether a text is Chinese ("zh") or not ("en"): more
// Han characters than Latin letters.
func TextLang(s string) string {
	han, latin := 0, 0
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Han, r):
			han++
		case r < 0x250 && unicode.IsLetter(r):
			latin++
		}
	}
	if han > 0 && han*2 >= latin {
		return "zh"
	}
	return "en"
}

func pickLang(r Recipe, o Options) string {
	switch o.Lang {
	case "en", "zh":
		return o.Lang
	}
	if r.Description != "" {
		return TextLang(r.Description)
	}
	if r.Attrs.Dialect != "" {
		return "zh"
	}
	return "en"
}

func allValues(a Attributes) []Value { return a.Diff(Attributes{}) }
