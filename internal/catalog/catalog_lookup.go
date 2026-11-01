package catalog

// Reading a catalogue: text length, finding entries by id, repo,
// label and task, custom models, voices and languages of an entry,
// removed and replaced models, and the names and sizes the UI shows;
// split from catalog.go.

import (
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// TextLength measures text against Entry.MaxChars: one per character,
// three per CJK character (which a model's phonemiser turns into several
// phonemes).
func TextLength(text string) int {
	n := 0
	for _, r := range text {
		if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) {
			n += 3
		} else {
			n++
		}
	}
	return n
}

// ByID, ByRepo and ByLabel find an entry.
func (c *Catalog) ByID(id string) (Entry, bool) {
	return c.find(func(e *Entry) bool { return e.ID == id })
}

func (c *Catalog) ByRepo(repo string) (Entry, bool) {
	return c.find(func(e *Entry) bool { return e.Repo == repo })
}

func (c *Catalog) ByLabel(label string) (Entry, bool) {
	return c.find(func(e *Entry) bool { return e.Label == label })
}

func (c *Catalog) find(match func(*Entry) bool) (Entry, bool) {
	for i := range c.Entries {
		if match(&c.Entries[i]) {
			return c.Entries[i], true
		}
	}
	return Entry{}, false
}

// ForTask returns the entries that do task, in catalogue order, without
// hidden ones.
func (c *Catalog) ForTask(task Task) []Entry {
	var out []Entry
	for _, e := range c.Entries {
		if !e.Hidden && slices.Contains(e.Tasks, task) {
			out = append(out, e)
		}
	}
	return out
}

// Labels returns the labels of the entries that do task, then CustomLabel:
// the options of a model picker.
func (c *Catalog) Labels(task Task) []string {
	var labels []string
	for _, e := range c.ForTask(task) {
		labels = append(labels, e.Label)
	}
	return append(labels, CustomLabel)
}

// CustomLabel is the picker option for a repo that is not in the catalogue.
const CustomLabel = "Custom Hugging Face repo"

// Custom describes an unknown TTS repo: every control is offered, since
// we cannot tell which ones the model reads. Its licence is unknown, so it
// is treated as requiring no acceptance (the user brought it).
func Custom(repo string) Entry {
	return Entry{
		ID:           "custom",
		Label:        CustomLabel,
		Family:       "",
		Repo:         repo,
		Tasks:        []Task{TaskTTS},
		About:        "Any TTS repo mlx-audio-swift supports (it reads model_type from config.json).",
		Capabilities: []Capability{CapSampling, CapClone, CapSpeed, CapLanguage},
		Licence:      Licence{ID: "unknown", Name: "Unknown", Commercial: true},
	}
}

// Has reports whether the entry has capability c.
func (e Entry) Has(c Capability) bool { return slices.Contains(e.Capabilities, c) }

// VoiceIDs lists the built-in speakers pickers offer, default first
// (hidden ones left out).
func (e Entry) VoiceIDs() []string {
	var ids []string
	for _, v := range e.Voices {
		if !v.Hidden {
			ids = append(ids, v.ID)
		}
	}
	return ids
}

// VoiceIDsIn lists the built-in speakers pickers offer in interface
// language locale: VoiceIDs without those hidden in it (HideIn).
func (e Entry) VoiceIDsIn(locale string) []string {
	base := BaseLanguage(locale)
	var ids []string
	for _, v := range e.Voices {
		if !v.Hidden && !slices.Contains(v.HideIn, base) {
			ids = append(ids, v.ID)
		}
	}
	return ids
}

// SpeakerLanguage is the language of built-in speaker id (BCP-47 base,
// "zh", "en"): the one it speaks, else its native one; "" when unknown.
func (e Entry) SpeakerLanguage(id string) string {
	for _, v := range e.Voices {
		if v.ID == id {
			return BaseLanguage(firstNonEmpty(v.Language, v.Native))
		}
	}
	return ""
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// AllVoiceIDs lists every built-in speaker, hidden ones too: what a
// request may name.
func (e Entry) AllVoiceIDs() []string {
	ids := make([]string, len(e.Voices))
	for i, v := range e.Voices {
		ids[i] = v.ID
	}
	return ids
}

// STTFor is the speech-to-text model for language (BCP-47; "" or "auto"
// detects it): DefaultSTT when it knows the language (or detects it),
// else FallbackSTT; FallbackSTT too when the work translates speech into
// English (translate), which only it does.
func (c *Catalog) STTFor(language string, translate bool) Entry {
	def, ok := c.ByID(DefaultSTT)
	fb, fok := c.ByID(FallbackSTT)
	switch {
	case !fok:
		return def
	case !ok, translate, !def.Knows(language):
		return fb
	}
	return def
}

// Knows reports whether the model takes language (BCP-47, matched by
// its base language: "zh-Hans" is "zh"); "" and "auto" (detection) and
// models that list no languages take any.
func (e Entry) Knows(language string) bool {
	language = strings.TrimSpace(language)
	if language == "" || strings.EqualFold(language, "auto") || len(e.Languages) == 0 {
		return true
	}
	base := BaseLanguage(language)
	for _, l := range e.Languages {
		if BaseLanguage(l.Code) == base || strings.EqualFold(l.Value, language) {
			return true
		}
	}
	return false
}

// RemovedModel finds a removed model by id, repo, short repo or label
// (case-insensitive).
func (c *Catalog) RemovedModel(ref string) (Removed, bool) {
	n := strings.TrimSpace(ref)
	if n == "" {
		return Removed{}, false
	}
	for _, r := range c.Removed {
		if strings.EqualFold(r.ID, n) || strings.EqualFold(r.Repo, n) || strings.EqualFold(ShortRepo(r.Repo), n) || strings.EqualFold(r.Label, n) {
			return r, true
		}
	}
	return Removed{}, false
}

// Replace maps ref, when it names a removed speech model, to the entry
// that replaces it: CloneReplacedBy when the request clones a voice (and
// the model has one), else ReplacedBy. ok is false for anything else,
// removed models of other tasks too (ReplaceTask).
func (c *Catalog) Replace(ref string, clone bool) (e Entry, r Removed, ok bool) {
	r, ok = c.RemovedModel(ref)
	if !ok || r.Did() != TaskTTS {
		return Entry{}, Removed{}, false
	}
	id := r.ReplacedBy
	if clone && r.CloneReplacedBy != "" {
		id = r.CloneReplacedBy
	}
	e, ok = c.ByID(id)
	return e, r, ok
}

// ReplaceTask maps ref, when it names a removed model of task, to the
// entry that replaces it. A removed speech-to-text model is replaced by
// the model STTFor gives language (Qwen3-ASR, or Whisper for a language
// it does not know); speech models by Replace (without cloning).
func (c *Catalog) ReplaceTask(ref string, task Task, language string) (e Entry, r Removed, ok bool) {
	r, ok = c.RemovedModel(ref)
	if !ok || r.Did() != task {
		return Entry{}, Removed{}, false
	}
	if task == TaskSTT {
		if e = c.STTFor(language, false); e.ID != "" {
			return e, r, true
		}
	}
	e, ok = c.ByID(r.ReplacedBy)
	return e, r, ok
}

// LanguageOptions lists the values the language parameter takes, default
// first, or nil when the model takes none (CapLanguage unset: Kokoro's
// language follows the voice).
func (e Entry) LanguageOptions() []string {
	if !e.Has(CapLanguage) {
		return nil
	}
	var out []string
	for _, l := range e.Languages {
		if l.Value != "" {
			out = append(out, l.Value)
		} else {
			out = append(out, l.Code)
		}
	}
	return out
}

// HumanSize formats a download size as the picker shows it: "≈ 390 MB",
// "≈ 2 GB", "≈ 1.5 GB" (decimal units); "" for 0.
func HumanSize(bytes int64) string {
	switch {
	case bytes <= 0:
		return ""
	case bytes >= 1e9:
		return "≈ " + strings.TrimSuffix(strconv.FormatFloat(float64(bytes)/1e9, 'f', 1, 64), ".0") + " GB"
	default:
		return "≈ " + strconv.FormatInt((bytes+5e5)/1e6, 10) + " MB"
	}
}

// ShortRepo is a repo id without its owner: "Kokoro-82M-bf16".
func ShortRepo(repo string) string {
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		return repo[i+1:]
	}
	return repo
}

// DisplayName is how the UI names the model of repo: its friendly name
// in the UI's language when the catalogue has it, its label when the
// catalogue had it (removed models), else the repo without its owner.
func (c *Catalog) DisplayName(repo string) string {
	if e, ok := c.ByRepo(repo); ok {
		return e.DisplayName()
	}
	return c.TechnicalName(repo)
}

// TechnicalName is the model of repo's technical name, for details,
// tooltips and the API: its label when the catalogue has (or had) it,
// else the repo without its owner.
func (c *Catalog) TechnicalName(repo string) string {
	if e, ok := c.ByRepo(repo); ok {
		return e.Label
	}
	if r, ok := c.RemovedModel(repo); ok && strings.EqualFold(r.Repo, repo) {
		return r.Label
	}
	return ShortRepo(repo)
}

// SpeakersModel is the model that transcribes and tells speakers apart
// (DefaultSpeakers); ok is false when the catalogue has none.
func (c *Catalog) SpeakersModel() (Entry, bool) {
	e, ok := c.ByID(DefaultSpeakers)
	return e, ok && e.Has(CapSpeakers)
}

// LabelsSpeakers reports whether the model of repo (a repo, id or label)
// tells speakers apart (CapSpeakers).
func (c *Catalog) LabelsSpeakers(repo string) bool {
	for _, e := range c.Entries {
		if e.Has(CapSpeakers) && (e.Repo == repo || e.ID == repo || e.Label == repo) {
			return true
		}
	}
	return false
}
