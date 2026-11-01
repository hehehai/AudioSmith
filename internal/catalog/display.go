package catalog

// How the UI presents a model (thread #45): a friendly name that says
// what it is for ("Standard voices", not "Qwen3-TTS 0.6B CustomVoice"), a
// one-line description and a few tags from a fixed vocabulary, in the
// UI's language. The family, version, size, quantisation, repo and
// licence stay available (Label, TechnicalDetails) for details, tooltips
// and the API; ids, repos and stored data never change.

import (
	"strings"

	"github.com/hehehai/AudioSmith/internal/i18n"
)

// Tag is a word of the fixed vocabulary that describes models.
type Tag string

const (
	// Static tags, set in catalog.json.
	TagChinese      Tag = "chinese"
	TagEnglish      Tag = "english"
	TagMultilingual Tag = "multilingual"
	TagClone        Tag = "clone"
	TagDesign       Tag = "design"
	TagVoices       Tag = "voices" // built-in voices
	TagFast         Tag = "fast"
	TagQuality      Tag = "quality"
	TagLight        Tag = "light"    // little memory
	TagFallback     Tag = "fallback" // used where the default can't (Whisper: other languages)
	TagSpeakers     Tag = "speakers" // tells speakers apart

	// Computed tags: recommended for a workflow (presets.json), a
	// non-commercial licence, not downloaded yet.
	TagRecommended   Tag = "recommended"
	TagNonCommercial Tag = "non-commercial"
	TagDownload      Tag = "download"
)

var staticTags = []Tag{TagChinese, TagEnglish, TagMultilingual, TagClone, TagDesign, TagVoices, TagFast, TagQuality, TagLight, TagFallback, TagSpeakers}

// AllTags is the whole vocabulary, static then computed.
func AllTags() []Tag {
	return append(append([]Tag(nil), staticTags...), TagRecommended, TagNonCommercial, TagDownload)
}

// Label is the tag's name in the UI's language.
func (t Tag) Label() string {
	switch t {
	case TagChinese:
		return i18n.X("model tag", "Chinese")
	case TagEnglish:
		return i18n.X("model tag", "English")
	case TagMultilingual:
		return i18n.X("model tag", "Multilingual")
	case TagClone:
		return i18n.X("model tag", "Cloning")
	case TagDesign:
		return i18n.X("model tag", "Design")
	case TagVoices:
		return i18n.X("model tag", "Built-in voices")
	case TagFast:
		return i18n.X("model tag", "Fast")
	case TagQuality:
		return i18n.X("model tag", "High quality")
	case TagLight:
		return i18n.X("model tag", "Low memory")
	case TagFallback:
		return i18n.X("model tag", "Fallback")
	case TagSpeakers:
		return i18n.X("model tag", "Speakers")
	case TagRecommended:
		return i18n.X("model tag", "Recommended")
	case TagNonCommercial:
		return i18n.X("model tag", "Non-commercial")
	case TagDownload:
		return i18n.X("model tag", "Not downloaded")
	}
	return string(t)
}

// DisplayName is the model's friendly name in the UI's language
// ("标准音色"); its label when it has none (a custom repo).
func (e Entry) DisplayName() string {
	switch {
	case e.ID == "custom":
		return i18n.T("Custom Hugging Face repo")
	case e.Name == "":
		return e.Label
	}
	return i18n.X("model name", e.Name)
}

// Description is the model's one line in the UI's language: its summary,
// else its about text.
func (e Entry) Description() string {
	if e.Summary != "" {
		return i18n.T(e.Summary)
	}
	if e.About != "" {
		return i18n.T(e.About)
	}
	return ""
}

// DisplayTags are the model's tags as pickers show them: Non-commercial
// first when its licence says so, then its own, at most n (0 = all).
func (e Entry) DisplayTags(n int) []Tag {
	var out []Tag
	if !e.Licence.Commercial {
		out = append(out, TagNonCommercial)
	}
	out = append(out, e.Tags...)
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// TechnicalSummary is the model's technical identity in one line, for a
// tooltip or a details row: "Qwen3-TTS 0.6B CustomVoice · 8bit · ≈ 2 GB ·
// Apache 2.0".
func (e Entry) TechnicalSummary() string {
	parts := []string{e.Label}
	if e.Quantization != "" {
		parts = append(parts, e.Quantization)
	}
	if s := HumanSize(e.Size); s != "" {
		parts = append(parts, s)
	}
	if e.Licence.Name != "" && e.ID != "custom" {
		parts = append(parts, e.Licence.Name)
	}
	return strings.Join(parts, " · ")
}

// Matches reports whether a search query finds the model: every word of
// it must appear (ignoring case) in its friendly name (in the UI's
// language and in English), technical name, id, repo, family or tags.
func (e Entry) Matches(query string) bool {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return true
	}
	hay := []string{e.DisplayName(), e.Name, e.Label, e.ID, e.Repo, e.Family, e.Quantization}
	for _, t := range e.DisplayTags(0) {
		hay = append(hay, t.Label(), string(t))
	}
	all := strings.ToLower(strings.Join(hay, "\n"))
	for _, w := range words {
		if !strings.Contains(all, w) {
			return false
		}
	}
	return true
}

// ByName finds an entry by its friendly name, in the UI's language or in
// English: what a select of display names hands back.
func (c *Catalog) ByName(name string) (Entry, bool) {
	return c.find(func(e *Entry) bool { return e.DisplayName() == name || e.Name == name })
}

// Named reports whether ref names the model, ignoring case and spaces
// around it: its id, repo, repo without owner, label, or friendly name in
// English or in the UI's language. What API and MCP requests may give.
func (e Entry) Named(ref string) bool {
	n := strings.TrimSpace(ref)
	if n == "" {
		return false
	}
	for _, s := range []string{e.ID, e.Repo, ShortRepo(e.Repo), e.Label, e.Name, e.DisplayName()} {
		if s != "" && strings.EqualFold(s, n) {
			return true
		}
	}
	return false
}
