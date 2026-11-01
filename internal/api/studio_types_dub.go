package api

// The requests and answers of the dubbing operations (studio.go): the
// same structs are REST bodies and MCP tool inputs, as studio_types.go's.

import "time"

// --- dubbing ------------------------------------------------------------------------------

// DubCreate makes a dub of a file and, unless Through is "none", starts it.
type DubCreate struct {
	Path           string   `json:"path,omitempty" jsonschema:"Absolute path of the video or audio to dub (mp4, mov, mkv, mp3, wav, m4a…)."`
	URL            string   `json:"url,omitempty" jsonschema:"Or a video link (YouTube, Bilibili…; http or https) that AudioSmith downloads with yt-dlp: only when the user turned on Settings → Dubbing → Import from a link."`
	Title          string   `json:"title,omitempty" jsonschema:"The project's title; empty: the file's name."`
	SourceLanguage string   `json:"source_language,omitempty" jsonschema:"BCP-47 code spoken in the file; empty: detected."`
	Targets        []string `json:"target_languages,omitempty" jsonschema:"BCP-47 codes to dub into, the main one first; empty: AudioSmith's Dubbing settings (by default Chinese, or English for a video in Chinese)."`
	Model          string   `json:"model,omitempty" jsonschema:"The speech model (list_models); empty: AudioSmith's dubbing default (it clones each speaker)."`
	Voice          string   `json:"voice,omitempty" jsonschema:"One voice for every speaker (a saved voice's name or id); empty: each speaker's own voice, cloned."`
	Speakers       int      `json:"speakers,omitempty" jsonschema:"How many people speak (1–20); 0: detected."`
	KeepBackground *bool    `json:"keep_background,omitempty" jsonschema:"Keep the music and effects under the dub (default true)."`
	Through        string   `json:"through,omitempty" jsonschema:"How far to run now: transcript, translation, dub (default: the whole dub) or none."`
	Dialogue       string   `json:"dialogue,omitempty" jsonschema:"auto (new dubs' default): the conversations of a Chinese or English track are recorded by the multi-speaker dialogue model (two speakers or more, each line hearing the one before it; slower), the rest line by line; off: every line line by line."`
	DialogueModel  string   `json:"dialogue_model,omitempty" jsonschema:"The dialogue model: soulx (SoulX-Podcast, the default and for now the only one for dubs: a 2.4 GB download, 16 GB of memory, up to four voices a conversation)."`
}

// DubQuery reads a dub.
type DubQuery struct {
	ID       string `json:"dub_id" jsonschema:"The dub (create_dub or list_projects with category dub)."`
	Language string `json:"language,omitempty" jsonschema:"The target language whose translations to show; empty: the main one."`
	Segments *bool  `json:"segments,omitempty" jsonschema:"Include the lines (default true)."`
}

// DubRun runs a dub's pipeline.
type DubRun struct {
	ID           string   `json:"dub_id" jsonschema:"The dub."`
	Through      string   `json:"through,omitempty" jsonschema:"transcript, translation, dub (default) or verify (a second listen that flags lines)."`
	Languages    []string `json:"languages,omitempty" jsonschema:"Only these target languages; empty: all."`
	Retranslate  bool     `json:"retranslate,omitempty" jsonschema:"Translate every line again (edited lines too)."`
	Retranscribe bool     `json:"retranscribe,omitempty" jsonschema:"Transcribe the source again (drops the lines and their edits)."`
	Segments     []string `json:"segments,omitempty" jsonschema:"Only re-render these line ids; empty: the lines that changed."`
}

// DubSegmentEdit changes one line of a dub.
type DubSegmentEdit struct {
	ID         string `json:"id" jsonschema:"The line's id (get_dub)."`
	Text       string `json:"text,omitempty" jsonschema:"New source text (drops its translations, which are made again)."`
	Language   string `json:"language,omitempty" jsonschema:"With translation: its target language; empty: the main one."`
	Trans      string `json:"translation,omitempty" jsonschema:"New translated text in language."`
	Speaker    string `json:"speaker,omitempty" jsonschema:"Another speaker label (S1, S2…)."`
	Voice      string `json:"voice,omitempty" jsonschema:"A voice for this line only (a saved voice's name or id); default: the speaker's."`
	NoDialogue *bool  `json:"no_dialogue,omitempty" jsonschema:"true keeps this line out of the multi-speaker dialogue (made line by line; the conversation is split there); false lets it back in."`
}

// DubEdit edits lines and the cast of a dub.
type DubEdit struct {
	ID            string            `json:"dub_id" jsonschema:"The dub."`
	Segments      []DubSegmentEdit  `json:"segments,omitempty" jsonschema:"Line edits."`
	Cast          map[string]string `json:"cast,omitempty" jsonschema:"Speaker label (S1…) or * (everyone) → voice (name or id); default: the speaker's cloned voice."`
	Names         map[string]string `json:"names,omitempty" jsonschema:"Speaker label → display name."`
	Dialogue      string            `json:"dialogue,omitempty" jsonschema:"auto (new dubs' default): the conversations of a Chinese or English track are recorded by the multi-speaker dialogue model (two speakers or more, each line hearing the one before it; slower), the rest line by line; off: every line line by line."`
	DialogueModel string            `json:"dialogue_model,omitempty" jsonschema:"The dialogue model: soulx (SoulX-Podcast, the default and for now the only one for dubs: a 2.4 GB download, 16 GB of memory, up to four voices a conversation)."`
}

// DubSegmentJSON is a line of a dub.
type DubSegmentJSON struct {
	ID          string  `json:"id"`
	Start       float64 `json:"start"`
	End         float64 `json:"end"`
	Speaker     string  `json:"speaker,omitempty"`
	Text        string  `json:"text"`
	Translation string  `json:"translation,omitempty"`
	Voice       string  `json:"voice,omitempty"`
	Rendered    bool    `json:"rendered,omitempty"`
	Stale       bool    `json:"stale,omitempty"` // changed since its audio was made
	Error       string  `json:"error,omitempty"`
	// Dialogue is the conversation the line is in (dialogue_stretches,
	// from 1; 0: none); MadeBy how its audio was made: "dialogue" (the
	// multi-speaker model) or "single" (line by line); FallbackReason why
	// a conversation line was made line by line (heard, length,
	// segment_failed, unavailable); Flag that line's check failing too
	// (heard: Heard is what the recognizer heard; length): listen to it.
	Dialogue       int    `json:"dialogue,omitempty"`
	MadeBy         string `json:"made_by,omitempty"`
	FallbackReason string `json:"fallback_reason,omitempty"`
	Flag           string `json:"flag,omitempty"`
	Heard          string `json:"heard,omitempty"`
	NoDialogue     bool   `json:"no_dialogue,omitempty"`
}

// DubStretchJSON is a conversation the dialogue model records together.
type DubStretchJSON struct {
	Dialogue int      `json:"dialogue"` // its number, from 1
	Segments []string `json:"segments"` // its lines' ids, in time order
	Speakers int      `json:"speakers"` // its voices
}

// DubStageJSON is a stage's last run.
type DubStageJSON struct {
	Status   string   `json:"status"`
	Code     string   `json:"code,omitempty"`
	Message  string   `json:"message,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// DubTrackJSON is a dubbed track.
type DubTrackJSON struct {
	File    string  `json:"file,omitempty"`
	Speech  string  `json:"speech,omitempty"`
	Seconds float64 `json:"seconds,omitempty"`
	Flagged int     `json:"flagged,omitempty"` // lines a verify flagged
}

// DubSpeakerJSON is a speaker and their voice.
type DubSpeakerJSON struct {
	Label string `json:"label"`
	Name  string `json:"name,omitempty"`
	Voice string `json:"voice,omitempty"` // "" = cloned from the source
}

// DubJSON is a dub project.
type DubJSON struct {
	ID         string                  `json:"dub_id"`
	Title      string                  `json:"title"`
	Source     string                  `json:"source"`
	Duration   float64                 `json:"duration,omitempty"`
	SourceLang string                  `json:"source_language,omitempty"`
	Detected   string                  `json:"detected_language,omitempty"`
	Targets    []string                `json:"target_languages"`
	Phase      string                  `json:"phase"`
	Model      string                  `json:"model,omitempty"`
	Translator string                  `json:"translator,omitempty"`
	Batch      bool                    `json:"batch,omitempty"`
	Running    bool                    `json:"running"`
	JobID      string                  `json:"job_id"` // "dub:<dub_id>"
	Speakers   []DubSpeakerJSON        `json:"speakers,omitempty"`
	Stages     map[string]DubStageJSON `json:"stages,omitempty"`
	Tracks     map[string]DubTrackJSON `json:"tracks,omitempty"`
	Language   string                  `json:"language,omitempty"` // of the translations shown
	Segments   []DubSegmentJSON        `json:"segments,omitempty"`
	Updated    time.Time               `json:"updated_at"`
	// Dialogue is "auto" or "off" (multi-speaker dialogue), DialogueModel
	// its model (soulx); DialogueBlocked why language's track is made line
	// by line all the same (off, voice_match, language, speakers, memory,
	// model); DialogueStretches language's conversations.
	Dialogue          string           `json:"dialogue"`
	DialogueModel     string           `json:"dialogue_model,omitempty"`
	DialogueBlocked   string           `json:"dialogue_blocked,omitempty"`
	DialogueStretches []DubStretchJSON `json:"dialogue_stretches,omitempty"`
	// Warning: the request named a removed model; its replacement is used.
	Warning string `json:"warning,omitempty"`
}

// DubExport saves a dub.
type DubExport struct {
	ID        string   `json:"dub_id" jsonschema:"The dub."`
	Format    string   `json:"format" jsonschema:"mp4 (the video with dubbed audio tracks), wav, m4a or mp3 (a dubbed track; mp3 needs ffmpeg), srt, vtt or ass (subtitles), stems or segments (ZIP), burn (MP4 with burnt-in subtitles; needs ffmpeg)."`
	Language  string   `json:"language,omitempty" jsonschema:"The language of a track or subtitles (orig: the source's); empty: the main target."`
	Tracks    []string `json:"tracks,omitempty" jsonschema:"mp4: the audio tracks, languages and orig; empty: the main target and orig."`
	Default   string   `json:"default_track,omitempty" jsonschema:"mp4: the track players pick; empty: the first."`
	Subtitles bool     `json:"subtitles,omitempty" jsonschema:"mp4: add soft subtitle tracks."`
	Dual      bool     `json:"dual,omitempty" jsonschema:"Subtitles with the translation and the original."`
	Bits      int      `json:"bits,omitempty" jsonschema:"wav: 16, 24 (default) or 32."`
	Dir       string   `json:"directory,omitempty" jsonschema:"Absolute folder to write to; empty: AudioSmith's agents' folder."`
	Filename  string   `json:"filename,omitempty" jsonschema:"The file's name (no folders); empty: from the title and language."`
	Output    string   `json:"-"`
}

// BatchRequest dubs files with the batch queue.
type BatchRequest struct {
	Paths          []string `json:"paths,omitempty" jsonschema:"Absolute paths of videos or audio files."`
	Folder         string   `json:"folder,omitempty" jsonschema:"Or an absolute folder: every video and audio file in it (not its subfolders)."`
	Targets        []string `json:"target_languages,omitempty" jsonschema:"BCP-47 codes to dub into; empty: the Batch page's."`
	SourceLanguage string   `json:"source_language,omitempty" jsonschema:"BCP-47 code spoken; empty: the Batch page's (or detected)."`
	Voice          string   `json:"voice,omitempty" jsonschema:"One voice for every speaker (a saved voice's name or id); empty: the Batch page's (or each speaker cloned)."`
	Model          string   `json:"model,omitempty" jsonschema:"The speech model; empty: the Batch page's (or the dubbing default)."`
	KeepBackground *bool    `json:"keep_background,omitempty" jsonschema:"Keep music and effects; empty: the Batch page's."`
	Dialogue       string   `json:"dialogue,omitempty" jsonschema:"auto: conversations in Chinese or English tracks are recorded by the multi-speaker dialogue model; off: line by line; empty: the Batch page's (auto)."`
}

// BatchItemJSON is a video of the batch queue.
type BatchItemJSON struct {
	ID        string   `json:"dub_id"`
	JobID     string   `json:"job_id"`
	Title     string   `json:"title"`
	Source    string   `json:"source"`
	Languages []string `json:"target_languages"`
	Phase     string   `json:"phase"`
	Updated   string   `json:"updated_at,omitempty"`
}

// BatchJSON is the batch queue (and what a request added).
type BatchJSON struct {
	Added  []BatchItemJSON    `json:"added,omitempty"`
	Errors []BatchErrorJSON   `json:"errors,omitempty"`
	Queue  []BatchItemJSON    `json:"queue,omitempty"`
	Watch  *WatchFolderJSON   `json:"watch_folder,omitempty"`
	Prefs  *BatchSettingsJSON `json:"settings,omitempty"`
	// Warning: the request named a removed model; its replacement is used.
	Warning string `json:"warning,omitempty"`
}

// BatchErrorJSON is a file the batch could not take.
type BatchErrorJSON struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// WatchFolderJSON is the Batch page's watch folder (read only).
type WatchFolderJSON struct {
	Folder string `json:"folder,omitempty"`
	Active bool   `json:"active"`
	Paused bool   `json:"paused,omitempty"`
	Added  int    `json:"added"`
	Error  string `json:"error,omitempty"`
}

// BatchSettingsJSON are the Batch page's settings.
type BatchSettingsJSON struct {
	Targets        []string `json:"target_languages"`
	SourceLanguage string   `json:"source_language,omitempty"`
	Voice          string   `json:"voice,omitempty"`
	Model          string   `json:"model,omitempty"`
	KeepBackground bool     `json:"keep_background"`
	Translator     string   `json:"translator,omitempty"`
	Dialogue       string   `json:"dialogue,omitempty"`
}
