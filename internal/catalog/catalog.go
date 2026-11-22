// Package catalog describes the models AudioSmith offers: what each one
// does (tasks, languages, voices, capabilities), what it costs (files,
// sizes, memory) and under which licence. It is data, not code: the
// built-in catalogue is catalog.json, embedded, versioned, and validated
// by the tests; a newer one may later be fetched and cached (M7).
//
// The UI reads entries to decide which controls to show; the hub
// (internal/hub) downloads an entry's files; the licence gate reads its
// Licence; the engine confirms capabilities when it loads the model
// (proto.LoadResult).
//
// Imports: lang (to validate language codes), i18n (display names,
// descriptions and tags in the UI's language: display.go).
package catalog

// The catalogue's types, the built-in catalogue and Parse. Files:
// catalog.go, catalog_validate.go (Validate), catalog_lookup.go (reading
// entries), display.go (how the UI presents a model).

import (
	_ "embed"
	"encoding/json"
)

// Task is a kind of work a model does. One model may do several.
type Task string

const (
	TaskTTS       Task = "tts"       // text → speech
	TaskSTT       Task = "stt"       // speech → text
	TaskAlign     Task = "align"     // forced alignment, word timings
	TaskVAD       Task = "vad"       // voice activity
	TaskDiarize   Task = "diarize"   // who spoke when
	TaskEnhance   Task = "enhance"   // denoise / dereverb
	TaskSeparate  Task = "separate"  // stems (vocals / background)
	TaskLID       Task = "lid"       // language identification
	TaskLLM       Task = "llm"       // text generation (cleanup, direction)
	TaskWatermark Task = "watermark" // invisible watermark: embed and detect (AudioSeal)
	TaskTranslate Task = "translate" // machine translation
)

var knownTasks = []Task{TaskTTS, TaskSTT, TaskAlign, TaskVAD, TaskDiarize, TaskEnhance, TaskSeparate, TaskLID, TaskLLM, TaskTranslate, TaskWatermark}

// Capability is an optional feature of a model, which shows or hides a
// control and lets the API reject a request early.
type Capability string

const (
	CapSpeed      Capability = "speed"      // honours a speed factor
	CapSampling   Capability = "sampling"   // honours temperature / top-p / max tokens
	CapClone      Capability = "clone"      // clones from reference audio (+ transcript)
	CapVoiceStyle Capability = "voiceStyle" // the voice field takes a free-form style prompt
	CapLanguage   Capability = "language"   // takes a language parameter (else it follows the voice)
	CapInstruct   Capability = "instruct"   // takes a style/emotion instruction
	CapDesign     Capability = "design"     // makes a voice from a description
	CapSeed       Capability = "seed"       // deterministic with a seed
	CapDuration   Capability = "duration"   // can target a duration
	CapStream     Capability = "stream"     // emits audio chunks while generating
	CapWordTimes  Capability = "wordTimes"  // STT: word-level timestamps

	// M1.
	CapVoicePrompt Capability = "voicePrompt" // tts.encodeVoice: a clone reference can be encoded once and reused
	CapSteps       Capability = "steps"       // honours steps (diffusion / refinement steps)
	CapGuidance    Capability = "guidance"    // honours guidance (classifier-free guidance scale)
	CapNonVerbal   Capability = "nonVerbal"   // renders inline non-verbal tags (Entry.NonVerbal)
	CapTranslate   Capability = "translate"   // STT: can translate speech into English (task "translate")
	// CapSpeakers: STT tells speakers apart, labelling each segment
	// (proto.Segment.Speaker; MOSS-Transcribe-Diarize, thread #48).
	CapSpeakers Capability = "speakers"
	// CapDialogue: speaks a whole dialogue in one job, each line hearing
	// the ones before it (tts.dialogue; SoulX-Podcast, thread #80): the
	// stories' 「整场对话」 mode. Such models clone every speaker from a
	// reference clip and its transcript.
	CapDialogue Capability = "dialogue"
	// CapCloneAudioOnly: clones from the reference audio alone, without
	// its transcript (Qwen3-TTS Base: the clip's speaker embedding,
	// upstream's x_vector_only_mode). A dub line in another language than
	// its speaker's clip is cloned this way (thread #90): given the
	// transcript, the model goes on in the clip's language.
	CapCloneAudioOnly Capability = "cloneAudioOnly"
)

// Catalog is a versioned list of entries.
type Catalog struct {
	// Version increases with every published change of the schema or data.
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
	// Removed are the models AudioSmith offered once and no longer does
	// (threads #43, #46): what still names them maps to their replacement.
	Removed []Removed `json:"removed,omitempty"`
	// External are models that run in an engine the user installed and
	// AudioSmith only connects to (VOICEVOX and AivisSpeech, thread #171):
	// nothing to download, and offered only while the engine answers, so
	// they are not among Entries (which pickers, downloads and the Models
	// list walk) but ByID, ByRepo and ByLabel find them.
	External []Entry `json:"external,omitempty"`
}

// Removed is a model taken out of the catalogue. Projects, drafts,
// settings, history recipes, workflows and API or MCP calls that still
// name it (by id, repo, short repo or label) use ReplacedBy instead, or
// CloneReplacedBy when the request clones a voice. Its files stay on
// disk until the user removes them (Models → Clean up).
type Removed struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Repo  string `json:"repo"`
	// Task is what the model did ("" = tts): its replacement does the
	// same, and only work of that task maps to it.
	Task Task `json:"task,omitempty"`
	// Aux are auxiliary repos only this model used: removed with it.
	Aux             []string `json:"aux,omitempty"`
	ReplacedBy      string   `json:"replacedBy"`
	CloneReplacedBy string   `json:"cloneReplacedBy,omitempty"`
	Reason          string   `json:"reason"` // English, for reviewers
}

// Warning tells an API client that by answers for the removed model.
func (r Removed) Warning(by Entry) string {
	return r.Label + " was removed from AudioSmith; " + by.Label + " (" + by.ID + ") answered instead."
}

// Repos are the repos a removed model leaves on disk: its own, then its
// auxiliary ones.
func (r Removed) Repos() []string { return append([]string{r.Repo}, r.Aux...) }

// Did is the task the removed model did.
func (r Removed) Did() Task {
	if r.Task == "" {
		return TaskTTS
	}
	return r.Task
}

// Entry is one downloadable model.
//
// Revision pins the commit the hub downloads and the engine loads (by the
// snapshot's path, offline): an update of the repo never changes a model
// under the user. Moving a pin is a catalogue change, tested on the Mac.
type Entry struct {
	ID string `json:"id"` // stable slug, used in settings and the DB: "kokoro-82m-bf16"
	// Label is the technical name (family, size, variant: "Qwen3-TTS 0.6B
	// CustomVoice"): stored in old settings, matched by the API, shown
	// only in details and tooltips. The UI names a model by DisplayName.
	Label string `json:"label"`
	// Name is the friendly name in English, which says what the model is
	// for, not its family or version ("Standard voices"); translated via
	// i18n (context "model name", DisplayName). Summary is its one line
	// (what it is for, when to pick it; English, translated), About when
	// unset. Tags are a few words from a fixed vocabulary (Tag), most
	// telling first; pickers show the first ones.
	Name     string `json:"name"`
	Summary  string `json:"summary,omitempty"`
	Tags     []Tag  `json:"tags,omitempty"`
	Family   string `json:"family"`             // engine model family (proto.HelloResult.Families): "kokoro", "qwen3_tts"…
	Repo     string `json:"repo"`               // Hugging Face repo id
	Revision string `json:"revision,omitempty"` // commit to download; "" = main
	Tasks    []Task `json:"tasks"`
	About    string `json:"about"` // one or two sentences, English (translated via i18n catalogs)

	// NeedsReference: the model only clones: a take needs
	// a reference clip (or a saved voice that carries one), else the
	// engine refuses it. The app does not send such a job, and pickers
	// say so. Requires CapClone.
	NeedsReference bool `json:"needsReference,omitempty"`

	Languages    []Language    `json:"languages,omitempty"`
	Voices       []VoiceOption `json:"voices,omitempty"` // built-in speakers; the first is the default
	Capabilities []Capability  `json:"capabilities,omitempty"`
	SampleRate   int           `json:"sampleRate,omitempty"` // Hz; 0 = learned at load

	// MaxChars is the longest text one tts.synthesize may carry, measured
	// with TextLength; the app splits longer scripts into chunks. Longer
	// requests fail with input_too_long. 0 = no known limit.
	MaxChars int `json:"maxChars,omitempty"`
	// NonVerbal lists the inline tags the model renders (CapNonVerbal),
	// as written in the text: "[laughter]", "[sigh]"…
	NonVerbal []string `json:"nonVerbal,omitempty"`
	// RTF is the model's typical real-time factor (seconds of audio per
	// second of generation) measured by the smoke suite on a MacBook Air
	// M4 with 24 GB (e2e_smoke_test.go); the Studio warns about slow
	// renders with it. 0 = not measured.
	RTF float64 `json:"rtf,omitempty"`
	// Deterministic models make the same audio for the same input without
	// a seed (no sampling): their chunk cache ignores the seed.
	Deterministic bool `json:"deterministic,omitempty"`

	// Size is the download size in bytes: the sum of Files when they are
	// listed, else an estimate until the hub lists the repo.
	Size  int64  `json:"size"`
	Files []File `json:"files,omitempty"`
	// MinMemoryGB is the unified memory the model needs to run comfortably.
	MinMemoryGB  float64 `json:"minMemoryGB,omitempty"`
	Quantization string  `json:"quantization,omitempty"` // "bf16", "8bit", "4bit"…

	// Aux are other repos the model loads at run time (Kokoro's G2P
	// models): downloaded with it, before it, and needed for it to count
	// as downloaded. Their sizes are in Size.
	Aux []Aux `json:"aux,omitempty"`

	// Hidden entries are listed on the Models page (download, licence)
	// but not offered by pickers (ForTask, Labels) until the app can run
	// them end to end (none at the end of M0).
	Hidden bool `json:"hidden,omitempty"`

	Licence Licence `json:"licence"`

	// Runtime names the outside engine an External entry runs in
	// ("voicevox": VOICEVOX ENGINE or AivisSpeech Engine over HTTP); ""
	// for the models of the app's own engine.
	Runtime string `json:"runtime,omitempty"`
}

// IsExternal reports whether the model runs in an engine the user
// installed (Runtime): nothing to download, its own terms per voice.
func (e Entry) IsExternal() bool { return e.Runtime != "" }

// Aux is an auxiliary repo of an entry.
type Aux struct {
	Repo     string `json:"repo"`
	Revision string `json:"revision,omitempty"`
	Files    []File `json:"files,omitempty"`
	Licence  string `json:"licence,omitempty"` // SPDX-like, for the credits
}

// Language is one language a model speaks or understands.
type Language struct {
	Code  string `json:"code"`            // BCP-47: "en", "en-GB", "zh", "pt-BR"
	Value string `json:"value,omitempty"` // what the model's language parameter takes, when not Code ("Chinese")
}

// VoiceOption is a built-in speaker.
type VoiceOption struct {
	ID       string `json:"id"`                 // what the voice parameter takes
	Language string `json:"language,omitempty"` // BCP-47: the only language it speaks (Kokoro's)
	// Native is the language a multilingual speaker is native in (BCP-47;
	// Qwen3-TTS CustomVoice's, from its model card): what it sounds best
	// in, while it speaks the model's other languages too, so the text
	// pipeline does not assume it (Language does).
	Native string `json:"native,omitempty"`
	Gender string `json:"gender,omitempty"` // "female", "male"
	// Hidden speakers are not offered by pickers (Kokoro's Chinese
	// voices: unintelligible, thread #41; af_nicole and bm_lewis, the
	// weakest of #91's audit) but still work when named. HideIn are
	// interface languages (base codes) whose pickers don't offer it
	// (Ryan in Chinese: #91 heard him give up on Chinese numbers).
	Hidden bool     `json:"hidden,omitempty"`
	HideIn []string `json:"hideIn,omitempty"`
}

// File is one file of the repo to download.
type File struct {
	Path   string `json:"path"` // inside the repo
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"` // LFS files: verified after download
}

// Licence is the model weights' licence (from the upstream model card;
// mlx-community's tags are not reliable).
type Licence struct {
	ID         string `json:"id"`   // SPDX-like: "apache-2.0", "cc-by-nc-4.0"
	Name       string `json:"name"` // "Apache 2.0"
	URL        string `json:"url,omitempty"`
	Commercial bool   `json:"commercial"` // allows commercial use
	// RequiresAcceptance gates first use behind a dialog (non-commercial
	// and other restrictive licences); see core.LicenceService.
	RequiresAcceptance bool `json:"requiresAcceptance,omitempty"`
	// Gated means Hugging Face requires accepting terms on the website
	// and a token to download.
	Gated bool `json:"gated,omitempty"`
	// Verified is false until someone checked the upstream model card.
	Verified bool `json:"verified"`
	// Note explains a licence that differs from the repo's own tag (for
	// the credits and reviewers; English, not shown in pickers).
	Note string `json:"note,omitempty"`
}

//go:embed catalog.json
var builtinJSON []byte

var builtin = func() *Catalog {
	c, err := Parse(builtinJSON)
	if err != nil {
		panic("catalog.json: " + err.Error()) // caught by TestBuiltin
	}
	return c
}()

// Builtin returns the embedded catalogue. Do not modify it.
func Builtin() *Catalog { return builtin }

// Swap makes c the built-in catalogue until restore is called. Tests use
// it for entries no shipped model has (a model that only clones voices,
// since thread #43 removed MOSS-TTS-Nano); it is not safe while anything
// else reads the catalogue.
func Swap(c *Catalog) (restore func()) {
	old := builtin
	builtin = c
	return func() { builtin = old }
}

// Parse decodes and validates a catalogue.
func Parse(data []byte) (*Catalog, error) {
	var c Catalog
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, c.Validate()
}

// Default models of the analysis tasks. Their entries may be hidden from
// pickers; find them with ByID.
const (
	// DefaultSTT is the speech recognition of every workflow (thread
	// #44): dictation, Transcribe's Fast and Accurate modes, dubbing, the
	// dub's read-back check, clone references. Qwen3-ASR 0.6B: Chinese
	// 0.5–3.7 % errors where Whisper makes 1–20 %, with punctuation, 24–30×
	// real time, 0.2–0.33 s for 5 s of dictation on a MacBook Air M4.
	// STTFor picks FallbackSTT for the languages it does not know.
	DefaultSTT = "qwen3-asr-0.6b-4bit"
	// FallbackSTT transcribes the languages DefaultSTT does not know, and
	// translates speech into English (task "translate"): Whisper large-v3
	// turbo, 99 languages.
	FallbackSTT    = "whisper-large-v3-turbo-4bit"
	DefaultAlign   = "qwen3-forcedaligner-0.6b-4bit"
	DefaultVAD     = "silero-vad"
	DefaultEnhance = "deepfilternet-v3"
	// DefaultDictation is the speech-to-text model dictation starts with
	// (DefaultSTT; STTFor for a language it does not know).
	DefaultDictation = DefaultSTT
	// DefaultSeparate and DefaultDiarize serve dubbing (M4): HTDemucs and
	// Sortformer (≤ 4 speakers). Sortformer only tells speakers apart when
	// DefaultSpeakers is not downloaded.
	DefaultSeparate = "htdemucs"
	DefaultDiarize  = "sortformer-4spk-v2.1"
	// DefaultSpeakers transcribes and tells speakers apart in one pass
	// (thread #48): the dub's source analysis and Transcribe's Speakers
	// option. MOSS-Transcribe-Diarize 0.9B, 8-bit: sentences given the
	// right speaker 0.98–1.00 of the time on 2–4 speaker dialogues, where
	// speech recognition + Sortformer managed 0.42–0.71 (thread #44,
	// research/model-eval-stt-llm.md §3). Not for dictation (4× slower
	// than Qwen3-ASR).
	DefaultSpeakers = "moss-transcribe-diarize-8bit"
	// DefaultLLM is the local language model (llm.generate) for offline
	// translation and writing help: Qwen3 4B Instruct 2507, 4-bit
	// (Apache-2.0), the only one since thread #46.
	DefaultLLM = "qwen3-4b-instruct-2507-4bit"
	// DefaultWatermark marks generated speech (AudioSeal, 16 bits).
	DefaultWatermark = "audioseal-16bits"
)
