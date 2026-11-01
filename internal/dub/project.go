// Package dub is the domain of video and audio dubbing (M4): a dub
// project (the source, its transcript as segments, the cast, the target
// languages and their translations, the settings), the edits the
// workspace makes to it (split, merge, insert, bulk changes, undo), the
// pure parts of the pipeline (speaker assignment, reference windows,
// timing strategies, the mix, QC, subtitles and exports), the batch
// queue's watch folder, URL import rules, and the engine methods the
// pipeline needs that protocol 2 does not have yet (engineapi.go,
// proposed to the engine thread).
//
// Everything here is plain data and functions over it: no jobs, no
// store, no engine calls. internal/services/dub*.go runs the pipeline as
// jobs over these pieces, and package main shows it.
//
// Times are seconds from the start of the source. Language codes are
// BCP-47, with the dialect when one was chosen ("es-MX", "pt-BR").
package dub

// A dub project's types: the project, its source, segments, cast,
// settings, stages, tracks, renders and QC report. Its methods are in
// project_methods.go.

import (
	"time"

	"github.com/hehehai/AudioSmith/internal/translate"
)

// Version is the project document's format version.
const Version = 1

// Project is a dub: everything the workspace shows and the pipeline
// needs, saved as one document (store.Dubs) and autosaved on every edit.
type Project struct {
	Version   int       `json:"version"`
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// Batch is the batch queue that made the project ("" = made on the
	// Dub page); batch projects run without review checkpoints.
	Batch string `json:"batch,omitempty"`

	Source Source `json:"source"`
	// SourceLang is the spoken language as chosen ("" = detect);
	// Detected is what transcription found.
	SourceLang string `json:"sourceLang,omitempty"`
	Detected   string `json:"detected,omitempty"`
	// Speakers is how many people speak (1–20; 0 = detect).
	Speakers int `json:"speakers,omitempty"`
	// Targets are the languages to dub into, the primary first.
	Targets []string `json:"targets"`
	// TargetsAuto: Targets are the default (DefaultTarget; nobody chose
	// them), so AutoRetarget may switch them when the video turns out to
	// be in that language; TargetSwitched is the default it replaced.
	// Choosing a target clears both (ChooseTargets).
	TargetsAuto    bool   `json:"targetsAuto,omitempty"`
	TargetSwitched string `json:"targetSwitched,omitempty"`

	Segments []Segment `json:"segments"`
	// NextSeg numbers new segments ("s<n>").
	NextSeg int `json:"nextSeg"`
	// Names are the speakers' display names by label ("S1" → "Ana").
	Names map[string]string `json:"names,omitempty"`
	// Cast is each speaker's voice, by label.
	Cast map[string]string `json:"cast,omitempty"`
	// References are the clips each speaker's automatic clone uses.
	References map[string]Reference `json:"references,omitempty"`
	// RefTried are the speakers the last pick of references considered
	// (a speaker added since needs one picked).
	RefTried []string           `json:"refTried,omitempty"`
	Glossary translate.Glossary `json:"glossary,omitempty"`

	Settings Settings `json:"settings"`
	Media    Media    `json:"media"`
	// Stages are the pipeline's stages as they last ran (StageKey).
	Stages map[string]StageState `json:"stages,omitempty"`
	// Tracks are the dubbed tracks, by language.
	Tracks map[string]Track `json:"tracks,omitempty"`
	// Run is the run asked for last, until it ends (Resume).
	Run *Run `json:"run,omitempty"`
}

// Source is what is dubbed.
type Source struct {
	Kind string `json:"kind"` // SourceFile or SourceURL
	// Path is the local file: the one picked, or the download of URL.
	Path string `json:"path,omitempty"`
	URL  string `json:"url,omitempty"`
	Name string `json:"name"` // shown: the file's name, or the video's title
	// Captions is a caption file downloaded with a URL ("" = none);
	// WantCaptions asks the download for the source language's.
	Captions     string `json:"captions,omitempty"`
	WantCaptions bool   `json:"wantCaptions,omitempty"`

	Size     int64      `json:"size,omitempty"`
	Duration float64    `json:"duration,omitempty"`
	HasVideo bool       `json:"hasVideo,omitempty"`
	Video    *VideoInfo `json:"video,omitempty"`
	Audio    string     `json:"audio,omitempty"` // audio codec
	// Meta is what a URL's site says of the video (Dubs.Probe, before
	// the download); nil when not read (a file, a project made before).
	Meta *SourceMeta `json:"meta,omitempty"`
}

// Source kinds.
const (
	SourceFile = "file"
	SourceURL  = "url"
)

// VideoInfo describes the source's video track.
type VideoInfo struct {
	Codec  string  `json:"codec"`
	Width  int     `json:"width"`
	Height int     `json:"height"`
	FPS    float64 `json:"fps,omitempty"`
}

// Segment is one line of speech: who says what, when, and how it is
// dubbed into each language.
type Segment struct {
	ID      string  `json:"id"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Speaker string  `json:"speaker,omitempty"`
	Text    string  `json:"text"` // what is said, in the source language
	// Words are the source's word timings while Text is as transcribed.
	Words []Word `json:"words,omitempty"`
	// Lines are the translations, by target language.
	Lines map[string]Line `json:"lines,omitempty"`

	// Voice overrides the speaker's voice for this line (a VoiceRef).
	Voice string `json:"voice,omitempty"`
	// Volume of the dubbed line, 0–2 (0 mutes it).
	Volume float64 `json:"volume"`
	// Speed overrides the project's speed, 0.5–2 (0 = the project's).
	Speed float64 `json:"speed,omitempty"`
	// Direction is how to say it ("whispered", "angry"); it becomes the
	// model's instruction where it takes one.
	Direction string `json:"direction,omitempty"`
	// Lang makes this line speak another language in every track (a line
	// kept in the source's language, a song); "" = the track's.
	Lang string `json:"lang,omitempty"`
	// Locked keeps the timing: automatic clean-up does not move it.
	Locked bool `json:"locked,omitempty"`
	// NoDialogue keeps the line out of the dialogue model's
	// conversations (「这一句不用多人对话」): it is made line by line and
	// ends the stretch it was in (dialogue.go).
	NoDialogue bool `json:"noDialogue,omitempty"`
	// Parts records the speakers of merged lines, so splitting restores
	// them: the speaker from rune Offset of Text on.
	Parts []Part `json:"parts,omitempty"`
}

// Part is a stretch of a merged segment's text said by Speaker.
type Part struct {
	Offset  int    `json:"offset"`
	Speaker string `json:"speaker"`
}

// Word is a timed word of the source.
type Word struct {
	Text  string  `json:"text"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// Line is a segment's text in one target language.
type Line struct {
	Text string `json:"text"`
	// Draft is the first pass when a later one replaced it; Shorter a
	// shorter rewrite suggested for a line that won't fit.
	Draft   string         `json:"draft,omitempty"`
	Shorter string         `json:"shorter,omitempty"`
	Fit     *translate.Fit `json:"fit,omitempty"`
	// Err is why the translation failed (a translate code); the line
	// then has no text.
	Err      string   `json:"error,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	Edited   bool     `json:"edited,omitempty"` // written or changed by hand (or pasted)
	Skipped  bool     `json:"skipped,omitempty"`
}

// Duration is the segment's slot.
func (s Segment) Duration() float64 { return s.End - s.Start }

// Reference is the clip a speaker's automatic clone uses: the best 5–15 s
// of their speech.
type Reference struct {
	File  string  `json:"file"` // in the project's directory
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
	Score float64 `json:"score"`
	// Enhanced: noise was removed (enhance.run), as it was noisy; Tidied:
	// its edges trimmed, long pauses shortened and evened out
	// (refcheck.Tidy). Grade and Hints are what the reference checker
	// found in the cut (refcheck codes).
	Enhanced bool     `json:"enhanced,omitempty"`
	Tidied   bool     `json:"tidied,omitempty"`
	Grade    string   `json:"grade,omitempty"`
	Hints    []string `json:"hints,omitempty"`
}

// Media are the files the pipeline made, in the project's directory.
type Media struct {
	Audio      string `json:"audio,omitempty"`      // the source's audio, WAV at its rate (≤ 2 channels)
	Speech     string `json:"speech,omitempty"`     // 16 kHz mono, for speech models
	Vocals     string `json:"vocals,omitempty"`     // separated voices
	Background string `json:"background,omitempty"` // separated music and effects
	Proxy      string `json:"proxy,omitempty"`      // H.264 preview of a video the web view cannot play
	Poster     string `json:"poster,omitempty"`     // a frame of the video
	Peaks      string `json:"peaks,omitempty"`      // waveform pyramid (PeaksFile)
	// Onsets are speech onsets for the timeline's snapping.
	Onsets []float64 `json:"onsets,omitempty"`
	// Separator is the model that separated the stems.
	Separator string `json:"separator,omitempty"`
}

// Settings are the project's production choices.
type Settings struct {
	// Model is the TTS model (repo) the dub speaks with.
	Model string `json:"model,omitempty"`
	// Translator: "llm" (the language-model router), or an online service
	// ("deepl", "google", "microsoft").
	Translator     string              `json:"translator,omitempty"`
	Quality        translate.Quality   `json:"quality,omitempty"`
	Style          string              `json:"style,omitempty"`
	Formality      translate.Formality `json:"formality,omitempty"`
	AutoGlossary   bool                `json:"autoGlossary,omitempty"`
	SuggestShorter bool                `json:"suggestShorter,omitempty"`

	Timing Timing `json:"timing,omitempty"`
	// KeepBackground keeps the music and effects under the dub.
	KeepBackground bool `json:"keepBackground"`
	// VoiceMatch: "consistent" (one reference per speaker) or "line"
	// (each line clones its own source clip).
	VoiceMatch string `json:"voiceMatch,omitempty"`

	Steps    int     `json:"steps,omitempty"`    // 0 = the model's
	Guidance float64 `json:"guidance,omitempty"` // 0 = the model's
	Speed    float64 `json:"speed,omitempty"`    // 0 = 1
	Instruct string  `json:"instruct,omitempty"` // a style for every line

	// Review stops after the transcript and after the translation, so
	// they can be checked before the next stage.
	Review bool `json:"review"`

	// Dialogue: DialogueAuto speaks the conversations of a track in
	// Chinese or English with a dialogue model where it can (new projects,
	// thread #89); DialogueOff or "" (projects made before it) line by
	// line. DialogueModel is the model's repo ("": the first the app offers,
	// SoulX-Podcast).
	Dialogue      string `json:"dialogue,omitempty"`
	DialogueModel string `json:"dialogueModel,omitempty"`

	Export ExportPrefs `json:"export"`
}

// Voice match modes.
const (
	MatchConsistent = "consistent"
	MatchLine       = "line"
)

// ExportPrefs are the export panel's choices, kept with the project.
type ExportPrefs struct {
	Format string `json:"format,omitempty"` // an Export* format
	// Tracks are the languages an MP4 carries ("orig" = the original).
	Tracks       []string `json:"tracks,omitempty"`
	DefaultTrack string   `json:"defaultTrack,omitempty"`
	Lang         string   `json:"lang,omitempty"` // the language of single-track exports
	Dual         bool     `json:"dual,omitempty"`
	Subtitles    bool     `json:"subtitles,omitempty"` // MP4: soft subtitle tracks
	Bits         int      `json:"bits,omitempty"`      // WAV: 16, 24 or 32 (float)
}

// OrigTrack names the original audio among an MP4's tracks.
const OrigTrack = "orig"

// Stage is a step of the pipeline.
type Stage string

const (
	StageIngest     Stage = "ingest"     // probe, decode, peaks, onsets, poster, proxy
	StageSeparate   Stage = "separate"   // voices and background
	StageTranscribe Stage = "transcribe" // diarise, transcribe, align
	StageReferences Stage = "references" // each speaker's clone reference
	StageTranslate  Stage = "translate"  // per language
	StageSynthesize Stage = "synthesize" // per language: every line's audio, fitted
	StageMix        Stage = "mix"        // per language: the dubbed track
	StageQC         Stage = "qc"         // per language: second-pass ASR
)

// PerLanguage reports whether a stage runs once per target language.
func (s Stage) PerLanguage() bool {
	return s == StageTranslate || s == StageSynthesize || s == StageMix || s == StageQC
}

// StageKey is the key of a stage in Project.Stages: "translate:es".
func StageKey(s Stage, lang string) string {
	if lang == "" || !s.PerLanguage() {
		return string(s)
	}
	return string(s) + ":" + lang
}

// StageStatus is where a stage is.
type StageStatus string

const (
	StatusNone    StageStatus = ""
	StatusRunning StageStatus = "running"
	StatusDone    StageStatus = "done"
	StatusFailed  StageStatus = "failed"
	StatusSkipped StageStatus = "skipped" // could not run (the engine lacks it); the pipeline went on
)

// StageState is a stage's last run.
type StageState struct {
	Status StageStatus `json:"status"`
	Job    string      `json:"job,omitempty"`
	// Code and Message say why it failed or was skipped.
	Code     string    `json:"code,omitempty"`
	Message  string    `json:"message,omitempty"`
	Warnings []string  `json:"warnings,omitempty"`
	At       time.Time `json:"at,omitzero"`
}

// Track is a dubbed track: the mix of one language, and how each line
// was rendered into it.
type Track struct {
	Lang string `json:"lang"`
	// File is the mixed track (WAV, the source's rate, stereo), "" until
	// mixed; Speech is the dubbed speech alone (for stems).
	File    string  `json:"file,omitempty"`
	Speech  string  `json:"speech,omitempty"`
	Seconds float64 `json:"seconds,omitempty"`
	// Mixed is when File was written; Background whether it carries the
	// separated background (false: the original, ducked, or none).
	Mixed      time.Time `json:"mixed,omitzero"`
	Background string    `json:"background,omitempty"` // BackgroundStem, BackgroundDucked or ""
	// Lines are the rendered lines, by segment id.
	Lines map[string]Render `json:"lines,omitempty"`
	QC    *QCReport         `json:"qc,omitempty"`
	// DialogueTakes count the times a dialogue stretch was made again
	// whole (「整段重录」: another seed), by its first line's id.
	DialogueTakes map[string]int `json:"dialogueTakes,omitempty"`
}

// How a track's background was made.
const (
	BackgroundStem   = "stem"   // the separated music and effects
	BackgroundDucked = "ducked" // the original audio, lowered under the dub (no separation)
)

// Render is how a line was rendered for a track.
type Render struct {
	// Key is the line's fingerprint when rendered (Fingerprint): a line
	// whose fingerprint changed is stale.
	Key  string `json:"key"`
	File string `json:"file,omitempty"` // fitted audio, in the project's renders/ directory
	// Raw is the length the model made, Length the fitted one; Ratio the
	// time-stretch applied (output/input: < 1 is faster).
	Raw    float64 `json:"raw"`
	Length float64 `json:"length"`
	Ratio  float64 `json:"ratio,omitempty"`
	// Start is where it plays (the segment's start).
	Start   float64 `json:"start"`
	Verdict Verdict `json:"verdict,omitempty"`
	// Over is how far it runs past the room it had (seconds).
	Over float64 `json:"over,omitempty"`
	Err  string  `json:"error,omitempty"`
	// Text is what was said (for QC and subtitles of the dub).
	Text string `json:"text,omitempty"`

	// Dialogue is the dialogue stretch the line belongs to (Stretch.N,
	// from 1; 0: none). Via is how it was made: ViaDialogue (by the
	// dialogue model), ViaFallback (by the dub's model after the dialogue
	// model did not make it: FallbackReason, a services.Dialogue* reason),
	// "" (by the dub's model, as every line before thread #89).
	Dialogue       int    `json:"dialogue,omitempty"`
	Via            string `json:"via,omitempty"`
	FallbackReason string `json:"fallbackReason,omitempty"`
	// Flag: a line failed its check (FlagHeard: the recognizer heard
	// Heard; FlagLength: far shorter or longer than its text;
	// FlagLanguage: heard in another language's script, Heard). Checked
	// are a line made on the dub's model in place of the dialogue
	// model's, and one cloned from a clip in another language (thread
	// #90; made once more before it is flagged). It is kept and shown
	// with a warning, to listen to.
	Flag  string `json:"flag,omitempty"`
	Heard string `json:"heard,omitempty"`
}

// How a line was made (Render.Via).
const (
	ViaDialogue = "dialogue"
	ViaFallback = "fallback"
)

// Why a line is flagged (Render.Flag).
const (
	FlagHeard    = "heard"
	FlagLength   = "length"
	FlagLanguage = "language"
)

// QCReport is a second-pass check of a track.
type QCReport struct {
	At      time.Time `json:"at"`
	Lines   []QCLine  `json:"lines"`
	Flagged int       `json:"flagged"`
	Overlap []string  `json:"overlap,omitempty"` // segment ids overlapping the next line
	Keys    QCKeys    `json:"keys,omitempty"`    // the render keys checked
	Model   string    `json:"model,omitempty"`
}

// QCKeys are the render keys a QC report checked, by segment id.
type QCKeys map[string]string

// QCLine is one line's check.
type QCLine struct {
	ID         string  `json:"id"`
	Heard      string  `json:"heard"`
	Drift      float64 `json:"drift"` // error rate 0–1 (characters for CJK and Thai, else words)
	Flagged    bool    `json:"flagged"`
	SyncRatio  float64 `json:"syncRatio,omitempty"` // rendered / slot
	MeasuredAt float64 `json:"measuredAt,omitempty"`
}

// DefaultTarget is the language a dub is made in when nobody chose one
// (Settings → Dubbing, the project, the API): Chinese (user,
// 2026-10-16). FallbackTarget replaces it when the video itself is in
// Chinese (AutoRetarget).
const (
	DefaultTarget  = "zh-Hans"
	FallbackTarget = "en"
)
