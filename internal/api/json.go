package api

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/lang"
)

// The JSON the native API answers with: snake_case, as OpenAI's.

// VoiceJSON is a saved voice.
type VoiceJSON struct {
	ID         string    `json:"id"`
	Object     string    `json:"object"` // "voice"
	Name       string    `json:"name"`
	Kind       string    `json:"kind"`            // "cloned" | "designed" | "builtin"
	Model      string    `json:"model,omitempty"` // repo it is for; "" = any model that can
	Speaker    string    `json:"speaker,omitempty"`
	Language   string    `json:"language,omitempty"`
	Style      string    `json:"style,omitempty"`
	Design     string    `json:"design,omitempty"`
	Seed       int64     `json:"seed,omitempty"`
	RefText    string    `json:"ref_text,omitempty"`
	RefSeconds float64   `json:"ref_seconds,omitempty"`
	Locked     bool      `json:"locked,omitempty"`
	Verified   bool      `json:"verified,omitempty"`
	Tags       []string  `json:"tags,omitempty"`
	Origin     string    `json:"origin,omitempty"`
	HasAudio   bool      `json:"has_audio"` // GET /api/voices/{id}/audio plays a sample or the reference
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// VoiceOf is a voice as the API answers it.
func VoiceOf(v core.Voice) VoiceJSON { return voiceJSON(v) }

func voiceJSON(v core.Voice) VoiceJSON {
	return VoiceJSON{ID: v.ID, Object: "voice", Name: v.Name, Kind: string(v.Kind), Model: v.Model, Speaker: v.Speaker,
		Language: v.Language, Style: v.Style, Design: v.Design, Seed: v.Seed, RefText: v.RefText, RefSeconds: v.RefSeconds,
		Locked: v.Locked, Verified: v.Verified(), Tags: v.Tags, Origin: v.Origin, HasAudio: v.Sample != "" || v.RefAudio != "",
		CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
}

// TakeJSON is a take of the history.
type TakeJSON struct {
	ID           string    `json:"id"`
	Object       string    `json:"object"` // "take"
	CreatedAt    time.Time `json:"created_at"`
	Text         string    `json:"text"`
	Model        string    `json:"model"`
	ModelID      string    `json:"model_id,omitempty"` // catalogue id
	Voice        string    `json:"voice,omitempty"`    // what the engine got (speaker, design tags…)
	VoiceID      string    `json:"voice_id,omitempty"`
	Language     string    `json:"language,omitempty"`
	Speed        float64   `json:"speed,omitempty"`
	Seed         int64     `json:"seed,omitempty"`
	Duration     float64   `json:"duration,omitempty"`
	Steps        int       `json:"steps,omitempty"`
	SampleRate   int       `json:"sample_rate"`
	AudioSeconds float64   `json:"audio_seconds"`
	GenSeconds   float64   `json:"gen_seconds"`
	RTF          float64   `json:"rtf,omitempty"`
	Starred      bool      `json:"starred,omitempty"`
	ProjectID    string    `json:"project_id,omitempty"`
	JobID        string    `json:"job_id,omitempty"`
	AudioURL     string    `json:"audio_url"`
}

func takeJSON(t core.Take) TakeJSON {
	j := TakeJSON{ID: t.ID, Object: "take", CreatedAt: t.CreatedAt, Text: t.Text, Model: t.Model, Voice: t.Voice,
		VoiceID: t.VoiceID, Language: t.Language, Speed: t.Speed, Seed: t.Seed, Duration: t.Duration, Steps: t.Steps,
		SampleRate: t.SampleRate, AudioSeconds: t.AudioSeconds, GenSeconds: t.GenSeconds, RTF: round2(t.RTF()),
		Starred: t.Starred, ProjectID: t.ProjectID, JobID: t.JobID, AudioURL: "/api/takes/" + t.ID + "/audio"}
	if e, ok := catalog.Builtin().ByRepo(t.Model); ok {
		j.ModelID = e.ID
	}
	return j
}

func round2(f float64) float64 { return float64(int64(f*100+0.5)) / 100 }

// ModelJSON is a catalogue entry and its state.
type ModelJSON struct {
	ID     string `json:"id"`
	Object string `json:"object"` // "model"
	Label  string `json:"label"`  // technical name: "Qwen3-TTS 0.6B CustomVoice"
	// DisplayName and Description are the friendly name and one line in
	// English ("Standard voices"); UIName is the name AudioSmith's UI
	// shows, in its language ("标准音色"); Tags are words of a fixed
	// vocabulary ("chinese", "clone", "fast", "non-commercial"…), thread
	// #45.
	DisplayName  string        `json:"display_name"`
	UIName       string        `json:"ui_name"`
	Description  string        `json:"description,omitempty"`
	Tags         []string      `json:"tags"`
	Repo         string        `json:"repo"`
	Family       string        `json:"family"`
	Tasks        []string      `json:"tasks"`
	Capabilities []string      `json:"capabilities"`
	Languages    []string      `json:"languages,omitempty"` // BCP-47
	Voices       []SpeakerJSON `json:"voices,omitempty"`    // built-in speakers
	SampleRate   int           `json:"sample_rate,omitempty"`
	MaxChars     int           `json:"max_chars,omitempty"`
	RTF          float64       `json:"rtf,omitempty"`
	Size         int64         `json:"size"`
	MinMemoryGB  float64       `json:"min_memory_gb,omitempty"`
	// NeedsReference: the model only clones voices; speech requests need
	// ref_audio or a saved cloned voice (else 400 needs_reference).
	NeedsReference bool        `json:"needs_reference,omitempty"`
	Downloaded     bool        `json:"downloaded"`
	Default        bool        `json:"default,omitempty"` // what OpenAI's names map to
	Hidden         bool        `json:"hidden,omitempty"`
	About          string      `json:"about,omitempty"`
	Licence        LicenceJSON `json:"licence"`
}

// RemovedModelJSON answers GET /v1/models/{id} for a model AudioSmith
// removed (thread #43): requests naming it are answered by ReplacedBy
// (CloneReplacedBy when they clone a voice), with a warning.
type RemovedModelJSON struct {
	ID              string `json:"id"`
	Object          string `json:"object"` // "model"
	Label           string `json:"label"`
	Repo            string `json:"repo"`
	Removed         bool   `json:"removed"`
	ReplacedBy      string `json:"replaced_by"`
	CloneReplacedBy string `json:"clone_replaced_by,omitempty"`
	Warning         string `json:"warning"`
}

func removedModelJSON(r catalog.Removed) RemovedModelJSON {
	by, _ := catalog.Builtin().ByID(r.ReplacedBy)
	j := RemovedModelJSON{ID: r.ID, Object: "model", Label: r.Label, Repo: r.Repo, Removed: true, ReplacedBy: r.ReplacedBy,
		Warning: r.Label + " was removed from AudioSmith; requests that name it are answered by " + by.Label + "."}
	if r.CloneReplacedBy != r.ReplacedBy {
		j.CloneReplacedBy = r.CloneReplacedBy
	}
	return j
}

// SpeakerJSON is a built-in speaker.
type SpeakerJSON struct {
	ID       string `json:"id"`
	Language string `json:"language,omitempty"`
	Gender   string `json:"gender,omitempty"`
}

// LicenceJSON is a model's licence.
type LicenceJSON struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	URL        string `json:"url,omitempty"`
	Commercial bool   `json:"commercial"`
	// Needed: the licence must be accepted in AudioSmith before use (403
	// licence_required until then).
	Needed bool `json:"acceptance_needed"`
}

func modelJSON(m Model, isDefault bool) ModelJSON {
	e := m.Entry
	j := ModelJSON{ID: e.ID, Object: "model", Label: e.Label, DisplayName: e.Name, UIName: e.DisplayName(), Description: firstNonEmpty(e.Summary, e.About), Tags: []string{}, Repo: e.Repo, Family: e.Family, SampleRate: e.SampleRate,
		MaxChars: e.MaxChars, RTF: e.RTF, Size: e.Size, MinMemoryGB: e.MinMemoryGB, Downloaded: m.Downloaded, NeedsReference: e.NeedsReference,
		Default: isDefault, Hidden: e.Hidden, About: e.About, Tasks: []string{}, Capabilities: []string{},
		Licence: LicenceJSON{ID: e.Licence.ID, Name: e.Licence.Name, URL: e.Licence.URL, Commercial: e.Licence.Commercial, Needed: m.LicenceNeeded}}
	for _, t := range e.Tasks {
		j.Tasks = append(j.Tasks, string(t))
	}
	for _, t := range e.DisplayTags(0) {
		j.Tags = append(j.Tags, string(t))
	}
	for _, c := range e.Capabilities {
		j.Capabilities = append(j.Capabilities, string(c))
	}
	j.Languages = entryLanguages(e)
	for _, v := range e.Voices {
		if !v.Hidden {
			j.Voices = append(j.Voices, SpeakerJSON{ID: v.ID, Language: v.Language, Gender: v.Gender})
		}
	}
	return j
}

// JobJSON is a job of the queue.
type JobJSON struct {
	ID         string          `json:"id"`
	Object     string          `json:"object"` // "job"
	Kind       string          `json:"kind"`
	Lane       string          `json:"lane"`
	State      string          `json:"state"`
	Title      string          `json:"title,omitempty"`
	Owner      string          `json:"owner,omitempty"`
	Attempt    int             `json:"attempt,omitempty"`
	Progress   *ProgressJSON   `json:"progress,omitempty"`
	Error      *JobErrorJSON   `json:"error,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	StartedAt  *time.Time      `json:"started_at,omitempty"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
	// Done: it ended (succeeded, failed, cancelled or interrupted).
	Done bool `json:"done"`
	// Files are the absolute paths of what it made (thread #35).
	Files []string `json:"files,omitempty"`
	// Jobs are the queue jobs of a group job ("longform:<id>"…).
	Jobs []string `json:"jobs,omitempty"`
}

// ProgressJSON is a running job's progress.
type ProgressJSON struct {
	Stage        string  `json:"stage,omitempty"`
	Fraction     float64 `json:"fraction"` // -1 = unknown
	Message      string  `json:"message,omitempty"`
	AudioSeconds float64 `json:"audio_seconds,omitempty"`
	Bytes        int64   `json:"bytes,omitempty"`
	TotalBytes   int64   `json:"total_bytes,omitempty"`
}

// JobErrorJSON is why a job failed.
type JobErrorJSON struct {
	Code      string          `json:"code"`
	Message   string          `json:"message"`
	Data      json.RawMessage `json:"data,omitempty"`
	Retryable bool            `json:"retryable,omitempty"`
}

func jobJSON(j jobs.Job, withResult bool) JobJSON {
	out := JobJSON{ID: j.ID, Object: "job", Kind: j.Kind, Lane: string(j.Lane), State: string(j.State), Title: j.Title,
		Owner: j.Owner, Attempt: j.Attempt, CreatedAt: j.Created, Done: ended(j)}
	if !j.Started.IsZero() {
		t := j.Started
		out.StartedAt = &t
	}
	if !j.Finished.IsZero() {
		t := j.Finished
		out.FinishedAt = &t
	}
	if j.State == jobs.StateRunning {
		p := j.Progress
		out.Progress = &ProgressJSON{Stage: p.Stage, Fraction: p.Fraction, Message: p.Message, AudioSeconds: p.AudioSeconds, Bytes: p.Bytes, TotalBytes: p.TotalBytes}
	}
	if j.Err != nil {
		out.Error = &JobErrorJSON{Code: j.Err.Code, Message: j.Err.Message, Data: j.Err.Data, Retryable: j.Err.Retryable}
	}
	if withResult && j.State == jobs.StateSucceeded && len(j.Result) > 0 {
		out.Result = j.Result
	}
	return out
}

// TranscriptJSON is a transcript.
type TranscriptJSON struct {
	ID        string    `json:"id"`
	Object    string    `json:"object"` // "transcript"
	CreatedAt time.Time `json:"created_at"`
	Title     string    `json:"title,omitempty"`
	Source    string    `json:"source"`
	Model     string    `json:"model"`
	Language  string    `json:"language,omitempty"`
	Duration  float64   `json:"duration"`
	Text      string    `json:"text"`
	Clean     string    `json:"clean,omitempty"`
	// Speakers names speaker labels ("S1" → "Ana") the user named.
	Speakers map[string]string `json:"speakers,omitempty"`
	Segments []SegmentJSON     `json:"segments,omitempty"`
}

// SegmentJSON is a sentence of a transcript.
type SegmentJSON struct {
	Start   float64    `json:"start"`
	End     float64    `json:"end"`
	Text    string     `json:"text"`
	Speaker string     `json:"speaker,omitempty"`
	Words   []WordJSON `json:"words,omitempty"`
}

// WordJSON is a timed word (OpenAI's shape).
type WordJSON struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

func transcriptJSON(t core.Transcript, segments bool) TranscriptJSON {
	j := TranscriptJSON{ID: t.ID, Object: "transcript", CreatedAt: t.CreatedAt, Title: t.Title, Source: string(t.Source),
		Model: t.Model, Language: t.Language, Duration: t.Duration, Text: t.Text(), Clean: t.Clean, Speakers: t.Speakers}
	if segments {
		for _, s := range t.Segments {
			sj := SegmentJSON{Start: s.Start, End: s.End, Text: s.Text, Speaker: s.Speaker}
			for _, w := range s.Words {
				sj.Words = append(sj.Words, WordJSON{Word: w.Text, Start: w.Start, End: w.End})
			}
			j.Segments = append(j.Segments, sj)
		}
	}
	return j
}

// languageName is OpenAI's language name in verbose_json: the English
// name, lower case ("english").
func languageName(code string) string {
	if code == "" {
		return ""
	}
	if l, ok := lang.Lookup(code); ok {
		return strings.ToLower(l.Name)
	}
	return code
}
