package core

import "time"

// Take is one generated piece of speech: its audio file and the recipe
// that made it, so it can be replayed, reused or regenerated.
//
// The JSON names are the history.json format of the MVP and stay stable:
// the SQLite store keeps the recipe in the same shape (takes.recipe JSON)
// and the history.json importer decodes old files with this type.
type Take struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	// File is the audio's name inside the store's audio directory; see
	// store.Takes.AudioPath. Never an absolute path.
	File string `json:"file"`

	// The recipe. Zero values mean "the model's default" and were not sent.
	Text        string  `json:"text"`
	Model       string  `json:"model"` // Hugging Face repo id (catalog.Entry.Repo)
	Voice       string  `json:"voice,omitempty"`
	Language    string  `json:"language,omitempty"`
	Speed       float64 `json:"speed,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
	TopP        float64 `json:"topP,omitempty"`
	MaxTokens   int     `json:"maxTokens,omitempty"`
	RefAudio    string  `json:"refAudio,omitempty"` // reference clip for cloning (absolute path)
	RefText     string  `json:"refText,omitempty"`
	Seed        int64   `json:"seed,omitempty"`     // M1: the seed used, so the take can be reproduced
	Duration    float64 `json:"duration,omitempty"` // target length in seconds (OmniVoice); 0 = natural
	Steps       int     `json:"steps,omitempty"`    // diffusion steps (OmniVoice); 0 = the model's
	// M1 engine parameters (protocol 2, M1); zero = the model's default.
	Guidance float64 `json:"guidance,omitempty"` // classifier-free guidance scale
	// Exaggeration is Chatterbox's emotion exaggeration (stories set it
	// from a line's emotion intensity); 0 = the model's.
	Exaggeration float64 `json:"exaggeration,omitempty"`
	VoicePrompt  string  `json:"voicePrompt,omitempty"` // tts.encodeVoice file of the clone reference (absolute path)
	// CloneRef and CloneRefText are the voice's reference for a model that
	// clones it while this recipe does not (a designed voice spoken by a
	// model that designs): Stories' whole dialogue clones from them. Never
	// sent with the recipe.
	CloneRef     string `json:"cloneRef,omitempty"`
	CloneRefText string `json:"cloneRefText,omitempty"`
	// Scales are the reading of a VOICEVOX-compatible engine (thread
	// #171): its AudioQuery scales and silences; nil = the engine's.
	Scales *Scales `json:"scales,omitempty"`

	// Credit is the attribution the take's audio must carry when it is
	// published ("VOICEVOX:ずんだもん"; thread #171): set by the render from
	// the voice's terms, shown with the take and written into exports.
	Credit string `json:"credit,omitempty"`

	// Relations (M1+). Empty for MVP takes.
	VoiceID   string `json:"voiceId,omitempty"`   // Voice profile it was spoken with
	ProjectID string `json:"projectId,omitempty"` // project (story, audiobook, dub) it belongs to
	JobID     string `json:"jobId,omitempty"`     // job that produced it
	// Origin is where the take was asked for: TakeStudio (also ""),
	// TakeVoice (a voice page's preview or sample), TakeConvert (Tools →
	// change the voice) or TakeAPI (the Local API or MCP, Client naming
	// the agent when it said). The Export dialog offers per origin.
	Origin string `json:"origin,omitempty"`
	Client string `json:"client,omitempty"`

	// Library state (M1+).
	Starred bool `json:"starred,omitempty"`

	// Render is how the script became audio (M1 render pipeline): output
	// options, chunks and findings. nil for takes made by one engine call
	// (MVP, M0).
	Render *TakeRender `json:"render,omitempty"`

	// The result.
	SampleRate   int     `json:"sampleRate"`
	AudioSeconds float64 `json:"audioSeconds"`
	GenSeconds   float64 `json:"genSeconds"`
}

// Scales tune a VOICEVOX-compatible engine's reading; 0 = the engine's
// own. Pitch, Intonation and Volume are its AudioQuery's scales
// (TempoDynamics AivisSpeech's own); PreSilence and PostSilence are
// seconds of silence the render adds before and after the take.
type Scales struct {
	Pitch         float64 `json:"pitch,omitempty"`
	Intonation    float64 `json:"intonation,omitempty"`
	Volume        float64 `json:"volume,omitempty"`
	TempoDynamics float64 `json:"tempoDynamics,omitempty"`
	PreSilence    float64 `json:"preSilence,omitempty"`
	PostSilence   float64 `json:"postSilence,omitempty"`
}

// Take origins (Take.Origin).
const (
	TakeStudio  = "studio"
	TakeVoice   = "voice"
	TakeConvert = "convert"
	TakeAPI     = "api"
)

// TakeRender is a take's render: the output options of its recipe (which
// Reuse restores) and what the render found.
type TakeRender struct {
	// Recipe.
	Bits        int    `json:"bits,omitempty"`        // 16 (0 = 16), 24 or 32 (float)
	Master      string `json:"master,omitempty"`      // mastering preset ("broadcast" = Even out volume); "" = none
	NoNormalize bool   `json:"noNormalize,omitempty"` // numbers, dates… left as written
	SeedPinned  bool   `json:"seedPinned,omitempty"`  // Seed was chosen by the user

	// Result.
	Channels int          `json:"channels,omitempty"`
	Bytes    int64        `json:"bytes,omitempty"` // size of the audio file
	Chunks   []TakeChunk  `json:"chunks,omitempty"`
	Warnings []TakeNotice `json:"warnings,omitempty"` // markup lowered, chunks that may have dropped words…
	// Issues are the findings of Check audio (dsp/qc); QCDone says the
	// check ran (no issues then means none were found).
	Issues []TakeNotice `json:"issues,omitempty"`
	QCDone bool         `json:"qcDone,omitempty"`
	// Watermarked: the file carries the invisible watermark (AudioSeal).
	Watermarked bool `json:"watermarked,omitempty"`
}

// TakeChunk is one engine call of a render.
type TakeChunk struct {
	Text       string  `json:"text"`             // as the engine got it (lowered, normalised)
	Start      float64 `json:"start"`            // seconds into the take
	End        float64 `json:"end"`              //
	Seed       int64   `json:"seed,omitempty"`   // the seed the engine used
	Cached     bool    `json:"cached,omitempty"` // reused from the chunk cache
	GenSeconds float64 `json:"genSeconds,omitempty"`
}

// TakeNotice is a finding about a take, by code (translated by the UI)
// with its parameters and, where it has one, its place in the audio.
type TakeNotice struct {
	Code     string            `json:"code"`
	Severity string            `json:"severity,omitempty"` // "info", "warning", "error"
	Chunk    int               `json:"chunk,omitempty"`    // 1-based; 0 = none
	Start    float64           `json:"start,omitempty"`    // seconds
	End      float64           `json:"end,omitempty"`
	Value    float64           `json:"value,omitempty"`
	Params   map[string]string `json:"params,omitempty"`
}

// RTF is how many seconds of audio one second of generation made.
func (t *Take) RTF() float64 {
	if t.GenSeconds <= 0 {
		return 0
	}
	return t.AudioSeconds / t.GenSeconds
}

// TakeQuery selects takes. The zero value is every take, newest first.
type TakeQuery struct {
	Search    string // full-text over the script (SQLite FTS5); "" matches all
	Model     string
	VoiceID   string
	ProjectID string
	Starred   bool // only starred takes
	Limit     int  // 0 = no limit
	Offset    int
}
