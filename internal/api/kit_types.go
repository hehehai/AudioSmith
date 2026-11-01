package api

// The requests and answers of the toolkit operations (kit.go, thread
// #135): the Voice isolator, Assets, the samples, voice management, the
// reference check and the story draft. As in studio_types.go, the same
// structs are REST bodies (ids from the path win) and MCP tool inputs.

import (
	"time"

	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/translate"
)

// --- Voice isolator -------------------------------------------------------------------

// IsolateRequest starts an isolation.
type IsolateRequest struct {
	Path string `json:"path" jsonschema:"Absolute path of the audio or video file whose voices to separate (wav, mp3, m4a, mp4, mov…)."`
}

// IsolationQuery reads isolations.
type IsolationQuery struct {
	ID string `json:"isolation_id,omitempty" jsonschema:"One isolation (isolate_voice returns its id); empty: every isolation, newest first."`
	// Stem asks for one stem's audio in the MCP result (inline/file as
	// the output setting says).
	Stem   string `json:"stem,omitempty" jsonschema:"With isolation_id: also return this stem's audio: vocals, background or clean."`
	Output string `json:"output,omitempty" jsonschema:"For stem: inline (base64 audio in the result), file (its path only) or both. Empty: AudioSmith's setting."`
}

// IsolationJSON is an isolation: one file's stems.
type IsolationJSON struct {
	ID        string     `json:"isolation_id"`
	Title     string     `json:"title"`
	Input     string     `json:"input"`
	Video     bool       `json:"video,omitempty"`
	Duration  float64    `json:"duration,omitempty"`
	State     string     `json:"state"` // queued, running, done, failed
	JobID     string     `json:"job_id,omitempty"`
	Error     string     `json:"error,omitempty"`
	ErrorCode string     `json:"error_code,omitempty"`
	Stems     []StemJSON `json:"stems,omitempty"`
	Warnings  []string   `json:"warnings,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	Next      string     `json:"next,omitempty"`
}

// StemJSON is a stem written.
type StemJSON struct {
	Name  string `json:"name"` // vocals, background, clean
	File  string `json:"file"` // absolute path
	About string `json:"about"`
}

// --- Assets ---------------------------------------------------------------------------------

// AssetQuery lists Assets.
type AssetQuery struct {
	Kind   string `json:"kind,omitempty" jsonschema:"speech, story, audiobook, dub, stems or import; empty: all."`
	Search string `json:"search,omitempty" jsonschema:"Words in the title, the text or the file's path."`
	Limit  int    `json:"limit,omitempty" jsonschema:"At most this many, newest first (default 50, up to 500)."`
	Offset int    `json:"offset,omitempty" jsonschema:"Skip this many (paging)."`
}

// AssetKinds are the kinds of Assets.
var AssetKinds = []string{"speech", "story", "audiobook", "dub", "stems", "import"}

// AssetJSON is a file of Assets: an output or an imported file.
type AssetJSON struct {
	ID      string    `json:"id"` // take:…, render:…, export:…, stem:…, in:…
	Kind    string    `json:"kind"`
	Title   string    `json:"title"`
	Detail  string    `json:"detail,omitempty"`
	File    string    `json:"file"`
	Seconds float64   `json:"seconds,omitempty"`
	At      time.Time `json:"at"`
	Page    string    `json:"page"`           // the tool it came from: /studio, /stories…
	Tool    string    `json:"tool,omitempty"` // the MCP tool that opens its source
	Source  string    `json:"source_id,omitempty"`
}

// --- Samples --------------------------------------------------------------------------------

// SampleJSON is a sample project of Home's 示例 tab.
type SampleJSON struct {
	ID          string `json:"sample_id"`
	Kind        string `json:"kind"` // story, audiobook, narration, dub, transcript
	Title       string `json:"title"`
	Description string `json:"description"`
	Available   bool   `json:"available"`      // bundled in this build
	Copy        string `json:"copy,omitempty"` // the copy opened before ("<kind>:<id>")
}

// SampleOpen opens a sample.
type SampleOpen struct {
	ID string `json:"sample_id" jsonschema:"A sample from list_samples: story, chapter, narration, dub or meeting."`
}

// SampleOpened is the copy a sample made in the library.
type SampleOpened struct {
	ID    string   `json:"sample_id"`
	Kind  string   `json:"kind"`
	Ref   string   `json:"ref"`            // "<kind>:<id>"
	Tool  string   `json:"tool,omitempty"` // what reads it: get_longform project_id …
	Files []string `json:"files,omitempty"`
}

// --- voices -----------------------------------------------------------------------------------

// VoiceUpdate changes a saved voice's details; empty fields stay.
type VoiceUpdate struct {
	ID       string    `json:"voice_id" jsonschema:"The saved voice (id or name, see list_voices)."`
	Name     string    `json:"name,omitempty" jsonschema:"A new name (one line, up to 80 characters)."`
	Style    string    `json:"style,omitempty" jsonschema:"A delivery hint kept with the voice (calm, whisper…)."`
	Language string    `json:"language,omitempty" jsonschema:"The speaker's language (BCP-47)."`
	RefText  string    `json:"ref_text,omitempty" jsonschema:"What the voice's reference recording says (cloned voices)."`
	Tags     *[]string `json:"tags,omitempty" jsonschema:"Its tags, replacing the old ones ([] clears them)."`
}

// VoiceDuplicate copies a saved voice.
type VoiceDuplicate struct {
	ID   string `json:"voice_id" jsonschema:"The saved voice to copy (id or name)."`
	Name string `json:"name,omitempty" jsonschema:"The copy's name; empty: '<name> (copy)'."`
}

// VoiceExportRequest writes a .asvoice file.
type VoiceExportRequest struct {
	ID               string `json:"voice_id" jsonschema:"The saved voice (id or name)."`
	IncludeReference bool   `json:"include_reference,omitempty" jsonschema:"Also pack the reference recording, so that the file clones the voice elsewhere (default: the preview only)."`
	Licence          string `json:"licence,omitempty" jsonschema:"An SPDX licence id for the file (CC-BY-4.0…); empty: personal use."`
	Dir              string `json:"directory,omitempty" jsonschema:"Absolute folder to write to; empty: AudioSmith's agents' folder."`
	Filename         string `json:"filename,omitempty" jsonschema:"The file's name (no folders; .asvoice is added); empty: the voice's name."`
}

// VoiceImportRequest reads a voice file.
type VoiceImportRequest struct {
	Path string `json:"path" jsonschema:"Absolute path of an .asvoice (or VoiceStudio .ovsvoice) file."`
}

// VoiceImported is a voice an import saved.
type VoiceImported struct {
	Voice    VoiceJSON `json:"voice"`
	Warnings []string  `json:"warnings,omitempty"`
}

// VoicePreviewQuery asks for a voice's preview clip.
type VoicePreviewQuery struct {
	VoiceID string `json:"voice_id,omitempty" jsonschema:"A saved voice (id or name): the start of its sample or reference."`
	Gallery string `json:"gallery_id,omitempty" jsonschema:"Or a Discover voice's id (search_voices): its bundled preview."`
	Model   string `json:"model,omitempty" jsonschema:"Or a model id with speaker: that built-in speaker's bundled preview."`
	Speaker string `json:"speaker,omitempty" jsonschema:"A built-in speaker of model (list_models → voices)."`
	Output  string `json:"output,omitempty" jsonschema:"inline (base64 audio in the result), file (a copy in the agents' folder) or both. Empty: AudioSmith's setting."`
}

// VoicePreview is a preview clip: its bytes and type, and what it is.
type VoicePreview struct {
	Data  []byte `json:"-"`
	MIME  string `json:"mime"` // audio/ogg, audio/wav
	Ext   string `json:"-"`    // "ogg", "wav"
	Name  string `json:"name"`
	Text  string `json:"text,omitempty"` // what it says, when known
	Model string `json:"model,omitempty"`
}

// --- reference check ----------------------------------------------------------------------------

// RefCheckRequest grades a recording as a clone reference.
type RefCheckRequest struct {
	Path string `json:"path" jsonschema:"Absolute path of the recording (wav, mp3, m4a, mp4…)."`
	Text string `json:"text,omitempty" jsonschema:"What it says, if known: a mismatch with what is heard is reported."`
}

// RefCheckJSON is a reference's grade and what to do.
type RefCheckJSON struct {
	Grade         string     `json:"grade"` // good, fine, needs_work
	Seconds       float64    `json:"seconds"`
	SpeechSeconds float64    `json:"speech_seconds"`
	Noisy         bool       `json:"noisy,omitempty"`
	Hints         []HintJSON `json:"hints,omitempty"`
	Note          string     `json:"note,omitempty"`
}

// HintJSON is a hint of the reference check.
type HintJSON struct {
	Code string `json:"code"`
	Text string `json:"text"`
}

// --- story draft --------------------------------------------------------------------------------

// StoryDraftRequest asks the writing help for a story script (Stories'
// 「帮我写」).
type StoryDraftRequest struct {
	Description string `json:"description" jsonschema:"What the story is about, in one or two sentences, in the language it should be written in."`
	Create      bool   `json:"create,omitempty" jsonschema:"true also makes the Stories project of the draft, cast with voices (as the page does)."`
	Model       string `json:"model,omitempty" jsonschema:"With create: the speech model (list_models); empty: AudioSmith's default."`
}

// StoryDraftJSON is a draft and, with create, its project.
type StoryDraftJSON struct {
	Script  string        `json:"script"` // [voice:Name] lines, narration untagged
	Title   string        `json:"title,omitempty"`
	Project *LongformJSON `json:"project,omitempty"`
}

// --- tools ---------------------------------------------------------------------------------------

// ProbeRequest probes a media file.
type ProbeRequest struct {
	Path string `json:"path" jsonschema:"Absolute path of an audio or video file."`
}

// DirectRequest reads a performance note.
type DirectRequest struct {
	Note string `json:"note" jsonschema:"A performance note in your own words (whispered, getting angry, a tired smile…)."`
}

// FitRequest checks whether a line fits a slot.
type FitRequest struct {
	Text       string  `json:"text" jsonschema:"The line to speak."`
	Language   string  `json:"language,omitempty" jsonschema:"Its language (BCP-47); empty: detected."`
	Slot       float64 `json:"slot" jsonschema:"The time it must fit, in seconds (> 0)."`
	Speed      float64 `json:"speed,omitempty" jsonschema:"The speaking rate it will be spoken at (1 = normal)."`
	Source     string  `json:"source,omitempty" jsonschema:"The original line it translates, if any."`
	SourceLang string  `json:"source_language,omitempty" jsonschema:"The original's language."`
	Rewrite    bool    `json:"rewrite,omitempty" jsonschema:"true asks the writing help for a shorter version when it does not fit."`
}

// FitJSON is a fit's answer.
type FitJSON struct {
	Fit        translate.Fit  `json:"fit"`
	Rewrite    string         `json:"rewrite,omitempty"`
	RewriteFit *translate.Fit `json:"rewrite_fit,omitempty"`
}

// WatermarkRequest checks a file for the watermark.
type WatermarkRequest struct {
	Path string `json:"path" jsonschema:"Absolute path of the audio file to check."`
}

// ProbeJSON is a probe's answer.
type ProbeJSON = proto.ProbeResult
