package services

import (
	"slices"
	"strings"
)

// Traits are what the render pipeline needs to know about a model beyond
// its catalogue capabilities: how much text one engine call takes, and
// how its markup is lowered. They are keyed by the model's family
// (catalog.Entry.Family), with the catalogue's capability names.
//
// The catalogue does not carry these yet (see the M1-G handoff: chunk
// limits, non-verbal tags, steps and determinism belong in catalog.json);
// until it does, this table is their home.
type Traits struct {
	Family string `json:"family"`
	// NonVerbal: renders non-verbal tags ([laughter]…) and bracketed
	// phonemes itself (OmniVoice).
	NonVerbal bool `json:"nonVerbal,omitempty"`
	// Speed: takes a speed factor ("speed" capability).
	Speed bool `json:"speed,omitempty"`
	// Steps: takes a number of diffusion steps ("Voice refinement",
	// performance tiers).
	Steps bool `json:"steps,omitempty"`
	// Duration: can target a duration ("duration" capability).
	Duration bool `json:"duration,omitempty"`
	// Deterministic models say the same text the same way whatever the
	// seed, so the seed is not part of their chunk keys (Kokoro).
	Deterministic bool `json:"deterministic,omitempty"`
	// MaxChars caps the runes of one engine call for spaced scripts,
	// MaxCharsCJK for scripts mostly in Han, kana or Hangul.
	MaxChars    int `json:"maxChars"`
	MaxCharsCJK int `json:"maxCharsCJK"`
	// NeedsReference: the model only clones (catalog
	// Entry.NeedsReference): a recipe without a reference clip or a voice
	// prompt is refused before it reaches the engine.
	NeedsReference bool `json:"needsReference,omitempty"`
	// NonVerbalTags are the tags the model renders ("laughter"…, without
	// brackets); nil with NonVerbal = every tag markup knows.
	NonVerbalTags []string `json:"nonVerbalTags,omitempty"`
	// Instruct: takes a delivery instruction in its voice field
	// (TakesInstruct): stories send their scene, personas and directions.
	Instruct bool `json:"instruct,omitempty"`
	// CloneInstruct: its clones take the instruction too (VoxCPM2,
	// thread #165: capabilities instruct and clone); other models' clones
	// follow the reference alone.
	CloneInstruct bool `json:"cloneInstruct,omitempty"`
	// Exaggeration: takes an emotion exaggeration (Chatterbox, capability
	// "exaggeration"): stories send their lines' emotion intensities.
	Exaggeration bool `json:"exaggeration,omitempty"`
}

// WithCatalog applies what the catalogue knows of the model (v3): its
// chunk limit (catalog.TextLength units: CJK characters count three) and
// the non-verbal tags it renders. Data the catalogue lacks keeps the
// family's defaults.
func (t Traits) WithCatalog(maxChars int, nonVerbal []string) Traits {
	if maxChars > 0 {
		t.MaxChars, t.MaxCharsCJK = maxChars, max(maxChars/3, 1)
	}
	if len(nonVerbal) > 0 {
		t.NonVerbal = true
		t.NonVerbalTags = nil
		for _, tag := range nonVerbal {
			t.NonVerbalTags = append(t.NonVerbalTags, strings.Trim(strings.ToLower(tag), "[] "))
		}
	}
	return t
}

// Step counts of the performance tiers, for models with Steps.
const (
	StepsFast     = 16
	StepsBalanced = 32 // OmniVoice's default
	StepsQuality  = 48
)

// StepTiers are the Fast, Balanced and Quality steps of a model whose
// recommended steps are def (0: StepsBalanced): half, def itself, and
// one and a half times (OmniVoice: 16, 32, 48; VoxCPM2: 5, 10, 15).
func StepTiers(def int) [3]int {
	if def <= 0 {
		def = StepsBalanced
	}
	return [3]int{max(def/2, 1), def, def + def/2}
}

// TraitsFor returns the traits of a model of family with capabilities caps.
func TraitsFor(family string, caps []string) Traits {
	t := Traits{
		Family:    family,
		Speed:     slices.Contains(caps, "speed"),
		Duration:  slices.Contains(caps, "duration"),
		Steps:     slices.Contains(caps, "steps"),
		NonVerbal: slices.Contains(caps, "nonVerbal"),
		MaxChars:  400, MaxCharsCJK: 200,
		Instruct:      TakesInstruct(caps),
		CloneInstruct: CloneTakesInstruct(caps),
		Exaggeration:  slices.Contains(caps, "exaggeration"),
	}
	switch family {
	case "kokoro":
		// Kokoro takes at most 510 phonemes a call: about 300 letters
		// of English, or 100 Chinese characters (pinyin phonemes).
		t.MaxChars, t.MaxCharsCJK = 300, 100
		t.Deterministic = true
	case "qwen3_tts", "qwen3":
		t.MaxChars, t.MaxCharsCJK = 500, 250
	case "omnivoice":
		// Until the catalogue lists OmniVoice's tags (CapNonVerbal).
		t.NonVerbal, t.Steps = true, true
		t.MaxChars, t.MaxCharsCJK = 600, 250
	}
	return t
}
