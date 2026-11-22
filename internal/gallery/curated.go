package gallery

// The curated voices (thread #94): data/curated.json, a small set chosen
// by #91's audit and measured on the Mac (scripts/make-curated-voices.sh).
// A designed one has a persona (the description sent, as written), a
// tested seed, a preview line and the text of the reference that ships
// with the app (the app's assets/gallery-voices), and the model that made
// them when not Qwen3-TTS (DesignModel); a built-in one names a catalogue
// model and its speaker.

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
)

// tx is a Text literal (en, zh).
func tx(en, zh string) vd.Text { return vd.Text{En: en, Zh: zh} }

// Category is a use case of the gallery (the filter beside the search).
type Category struct {
	ID          string  `json:"id"`
	Name        vd.Text `json:"name"`
	Description vd.Text `json:"description"`
}

// The four use cases (thread #91 §6.3: the nine categories were too many
// for about 35 voices).
var categories = []Category{
	{"narration", tx("Narration & audiobooks", "旁白 / 有声书"), tx("Stories, novels, documentaries and long reads.", "故事、小说、纪录片和长文朗读。")},
	{"conversational", tx("Conversation & podcasts", "对话 / 播客"), tx("Hosts, chats, dialogue and everyday talk.", "主持、闲聊、对话和日常说话。")},
	{"characters", tx("Characters & kids", "角色 / 童声"), tx("Characters, children, elders and villains.", "角色、孩子、老人和反派。")},
	{"announcing", tx("Ads & announcing", "广告 / 播报"), tx("News, ads, trailers, guides and explainers.", "新闻、广告、预告片、导览和讲解。")},
}

// categoryAliases map the earlier gallery's categories to the use cases,
// so a query that names one still filters.
var categoryAliases = map[string]string{
	"social": "conversational", "advertising": "announcing", "education": "announcing",
	"news": "announcing", "games": "characters", "wellness": "narration",
}

// Categories lists the use cases in display order.
func Categories() []Category { return slices.Clone(categories) }

// CategoryID is the use case a category id stands for: itself, or the use
// case an earlier gallery category maps to; "" when unknown.
func CategoryID(id string) string {
	if categoryIndex(id) >= 0 {
		return id
	}
	return categoryAliases[id]
}

func categoryIndex(id string) int {
	for i, c := range categories {
		if c.ID == id {
			return i
		}
	}
	return -1
}

// Languages lists the language facet: the languages voices speak, in
// display order.
var Languages = []string{"zh", "en", "ja", "ko", "es", "fr", "de", "it", "pt", "ru", "hi"}

// curatedSpec is one entry of data/curated.json.
type curatedSpec struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Uses        []string `json:"uses"`
	Gender      string   `json:"gender"`
	Age         string   `json:"age"`
	Language    string   `json:"language"`
	Name        vd.Text  `json:"name"`
	Description vd.Text  `json:"description"`
	Persona     string   `json:"persona"`
	Seed        int64    `json:"seed"`
	Sample      string   `json:"sample"`
	RefText     string   `json:"ref"`
	RefGrade    string   `json:"refGrade"`
	Model       string   `json:"model"`
	Speaker     string   `json:"speaker"`
	DesignModel string   `json:"designModel"`
	Tags        []string `json:"tags"`
}

// BuildCurated reads the curated voices, in display order.
func BuildCurated() ([]Archetype, error) {
	var specs []curatedSpec
	if err := readJSON("data/curated.json", &specs); err != nil {
		return nil, err
	}
	out := make([]Archetype, 0, len(specs))
	for _, s := range specs {
		a := Archetype{
			ID: CuratedPrefix + s.ID, Kind: s.Kind, Featured: true, Uses: s.Uses,
			Name: s.Name, Description: s.Description, Gender: s.Gender, Age: s.Age,
			Languages: []string{s.Language}, Tags: s.Tags, Seed: s.Seed,
			Persona: strings.TrimSpace(s.Persona), Line: s.Sample, RefText: s.RefText, RefGrade: s.RefGrade,
			Model: s.Model, Speaker: s.Speaker, DesignModel: s.DesignModel,
		}
		if len(s.Uses) > 0 {
			a.Category = s.Uses[0]
		}
		if a.Persona != "" {
			// The attributes the persona says: with them the recipe's
			// description is sent as written (vd.Lower adds nothing).
			a.Attrs = vd.Parse(a.Persona).Attrs.Canonical()
		}
		out = append(out, a)
	}
	return out, nil
}

func validateCurated(a Archetype) error {
	if !strings.HasPrefix(a.ID, CuratedPrefix) || len(a.ID) == len(CuratedPrefix) {
		return fmt.Errorf("gallery: curated id %q needs the %q prefix", a.ID, CuratedPrefix)
	}
	if len(a.Uses) == 0 {
		return fmt.Errorf("gallery: %s: no use case", a.ID)
	}
	for _, u := range a.Uses {
		if categoryIndex(u) < 0 {
			return fmt.Errorf("gallery: %s: unknown use case %q", a.ID, u)
		}
	}
	switch {
	case a.Name.En == "" || a.Name.Zh == "" || a.Description.En == "" || a.Description.Zh == "":
		return fmt.Errorf("gallery: %s: name and description need en and zh", a.ID)
	case !vd.Valid(vd.Gender, a.Gender) || !vd.Valid(vd.Age, a.Age):
		return fmt.Errorf("gallery: %s: gender %q or age %q unknown", a.ID, a.Gender, a.Age)
	case len(a.Languages) != 1 || !slices.Contains(Languages, a.Languages[0]):
		return fmt.Errorf("gallery: %s: languages %v", a.ID, a.Languages)
	}
	switch a.Kind {
	case KindDesigned:
		switch {
		case a.Persona == "" || utf8.RuneCountInString(a.Persona) > vd.MaxDescription:
			return fmt.Errorf("gallery: %s: persona missing or too long", a.ID)
		case a.Seed <= 0 || a.Seed > vd.MaxSeed:
			return fmt.Errorf("gallery: %s: no tested seed", a.ID)
		case strings.TrimSpace(a.Line) == "":
			return fmt.Errorf("gallery: %s: no preview line", a.ID)
		case strings.TrimSpace(a.RefText) == "":
			return fmt.Errorf("gallery: %s: no reference text", a.ID)
		case a.RefGrade != "good" && a.RefGrade != "ok":
			return fmt.Errorf("gallery: %s: reference grade %q (want good or ok)", a.ID, a.RefGrade)
		case a.Model != "" || a.Speaker != "":
			return fmt.Errorf("gallery: %s: a designed voice names no speaker", a.ID)
		}
	case KindBuiltin:
		if a.Model == "" || a.Speaker == "" || a.Persona != "" || a.DesignModel != "" {
			return fmt.Errorf("gallery: %s: a built-in voice needs a model and a speaker, and no persona", a.ID)
		}
	default:
		return fmt.Errorf("gallery: %s: unknown kind %q", a.ID, a.Kind)
	}
	return nil
}
