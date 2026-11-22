// Package gallery is the data side of Discover voices: about 35 curated
// voices (thread #94, after #91's audit) with their use cases, facets,
// search, favorites, the Design page's starting-point presets, and (in
// package persona) the .asvoice voice-file format.
//
// The curated voices come from data/curated.json: designed voices with a
// written persona (the description the model gets), a tested seed and a
// reference shipped with the app, and the built-in speakers worth
// offering. The earlier gallery (data/featured.json and data/roles.json:
// about 1,100 voices, mostly attribute combinations) is no longer listed,
// but its ids still resolve (ByID), so dub casts and agents that name one
// keep working; a voice saved from one carries its own recipe.
package gallery

import (
	"embed"
	"fmt"
	"slices"
	"sync"

	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
)

// Kinds of curated voices (Archetype.Kind; "" = an earlier gallery voice).
const (
	KindDesigned = "designed" // a persona, a seed and a bundled reference
	KindBuiltin  = "builtin"  // a model's built-in speaker
)

// CuratedPrefix starts the ids of curated voices.
const CuratedPrefix = "c-"

// Archetype is one ready-made voice: a curated one, or one of the earlier
// gallery's (Legacy).
type Archetype struct {
	ID       string `json:"id"`
	Kind     string `json:"kind,omitempty"`
	Featured bool   `json:"featured,omitempty"`
	// Category is the first use case (curated) or the earlier gallery's
	// category; Uses are all of a curated voice's use cases.
	Category    string        `json:"category"`
	Uses        []string      `json:"uses,omitempty"`
	Role        string        `json:"role,omitempty"` // generated archetypes: the role id
	Name        vd.Text       `json:"name"`
	Description vd.Text       `json:"description"`
	Sample      vd.Text       `json:"sample"`          // preview line in English and Chinese (earlier gallery)
	Local       *LocalLine    `json:"local,omitempty"` // preview line for a language other than en/zh
	Attrs       vd.Attributes `json:"attrs"`
	Gender      string        `json:"gender,omitempty"` // facet value (vd.Gender)
	Age         string        `json:"age,omitempty"`    // facet value (vd.Age)
	Languages   []string      `json:"languages"`        // recommended, BCP-47; the first is the preview language
	Tags        []string      `json:"tags,omitempty"`
	Seed        int64         `json:"seed"`
	// Persona is a curated designed voice's description: what the design
	// model gets, as written (thread #91: the earlier gallery's own
	// descriptions were never sent). Line is what its preview says, in
	// its language; RefText what its bundled reference says.
	// RefGrade is the reference checker's grade of it ("good", "ok").
	Persona  string `json:"persona,omitempty"`
	Line     string `json:"line,omitempty"`
	RefText  string `json:"refText,omitempty"`
	RefGrade string `json:"refGrade,omitempty"`
	// Model (catalogue id) and Speaker: a curated built-in speaker.
	Model   string `json:"model,omitempty"`
	Speaker string `json:"speaker,omitempty"`
	// DesignModel is the catalogue id of the model that designed a
	// curated designed voice's reference and cloned its preview ("": Qwen3
	// 1.7B VoiceDesign and Base; Irodori-TTS for Japanese ones, #172).
	DesignModel string `json:"designModel,omitempty"`
	// Legacy marks a voice of the earlier gallery: found by id, not listed.
	Legacy bool `json:"legacy,omitempty"`
}

// LocalLine is a sample line in another language.
type LocalLine struct {
	Lang string `json:"lang"`
	Text string `json:"text"`
}

// Curated reports whether a is one of the curated voices.
func (a Archetype) Curated() bool { return a.Kind != "" }

// Designed reports whether a speaks through a design recipe (a curated
// designed voice, or any voice of the earlier gallery).
func (a Archetype) Designed() bool { return a.Kind != KindBuiltin }

// Recipe is the voice as a design recipe ("Use voice" saves it as a
// designed voice profile): a curated voice's persona as written, with the
// attributes it says (so it is sent as it is) and its seed; an earlier
// gallery voice's attributes and seed. A built-in speaker has none.
func (a Archetype) Recipe() vd.Recipe {
	switch {
	case a.Kind == KindBuiltin:
		return vd.Recipe{}
	case a.Persona != "":
		return vd.Recipe{Description: a.Persona, Attrs: a.Attrs, Seed: a.Seed}
	}
	return vd.Recipe{Attrs: a.Attrs, Seed: a.Seed}
}

// PreviewLine is the text and language a preview of the archetype speaks:
// its first recommended language, with the matching line.
func (a Archetype) PreviewLine() (text, lang string) {
	lang = "en"
	if len(a.Languages) > 0 {
		lang = a.Languages[0]
	}
	switch {
	case a.Line != "":
		return a.Line, lang
	case lang == "zh":
		return a.Sample.Zh, "zh"
	case lang == "en":
		return a.Sample.En, "en"
	case a.Local != nil && a.Local.Lang == lang:
		return a.Local.Text, lang
	}
	return a.Sample.En, "en"
}

// Gallery is an immutable, searchable set of archetypes. It is safe for
// concurrent use.
type Gallery struct {
	items []Archetype
	byID  map[string]int
	hay   []string // folded search text per item
	// fallback finds the ids not listed (the earlier gallery's).
	fallback func(id string) (Archetype, bool)
}

//go:embed data/*.json
var dataFS embed.FS

var builtin = sync.OnceValues(func() (*Gallery, error) {
	items, err := BuildCurated()
	if err != nil {
		return nil, err
	}
	g, err := New(items)
	if err != nil {
		return nil, err
	}
	g.fallback = LegacyByID
	return g, nil
})

// Default returns the built-in gallery: the curated voices (built once,
// on first use). Its ByID also finds the earlier gallery's ids.
func Default() *Gallery {
	g, err := builtin()
	if err != nil {
		panic("gallery: " + err.Error()) // caught by TestDefault
	}
	return g
}

// New indexes archetypes after validating them: unique ids, known use
// cases, a persona, seed, preview line and reference text for a designed
// voice, a model and speaker for a built-in one (curated); valid
// attributes and a preview line (earlier gallery). Order is kept.
func New(items []Archetype) (*Gallery, error) {
	g := &Gallery{items: slices.Clone(items), byID: make(map[string]int, len(items)), hay: make([]string, len(items))}
	for i, a := range g.items {
		var err error
		if a.Curated() {
			err = validateCurated(a)
		} else {
			err = validate(a)
		}
		if err != nil {
			return nil, err
		}
		if _, dup := g.byID[a.ID]; dup {
			return nil, fmt.Errorf("gallery: duplicate id %q", a.ID)
		}
		g.byID[a.ID] = i
		g.hay[i] = haystack(a)
	}
	return g, nil
}

// Len is the number of archetypes listed.
func (g *Gallery) Len() int { return len(g.items) }

// All returns the archetypes listed, in display order (a copy).
func (g *Gallery) All() []Archetype { return slices.Clone(g.items) }

// ByID finds an archetype: a listed one, else (the built-in gallery) one
// of the earlier gallery's, which are not listed but still resolve.
func (g *Gallery) ByID(id string) (Archetype, bool) {
	if i, ok := g.byID[id]; ok {
		return g.items[i], true
	}
	if g.fallback != nil {
		return g.fallback(id)
	}
	return Archetype{}, false
}
