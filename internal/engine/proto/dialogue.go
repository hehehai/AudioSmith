package proto

// Dialogue models (docs/protocol-v2.md §5.6, thread #80): tts.dialogue
// speaks a whole conversation in one job, each turn hearing the ones before
// it. Per-turn models (SoulX-Podcast) write each turn to its own file and
// send tts.turn as each one is written; one-track models (MOSS-TTSD, thread
// #82; VibeVoice 1.5B, #166) write the whole dialogue to one file, which the
// app cuts at the turns.
// The Swift mirror is engine/Sources/AudioSmithProtocol/WireDialogue.swift.

const (
	MethodTTSDialogue = "tts.dialogue" // request, job (gpu)
	MethodTTSTurn     = "tts.turn"     // notification, engine → app: one turn is written
)

// DialogueSpeaker is a voice of the dialogue: a reference clip (any format
// AVFoundation reads) and its exact transcript.
type DialogueSpeaker struct {
	ID       string `json:"id"`
	RefAudio string `json:"refAudio"`
	RefText  string `json:"refText"`
}

// DialogueTurn is one line. A turn with Codes (DialogueTurnOut.Codes from an
// earlier result of the same model and speakers; for a one-track model, the
// turn's slice of DialogueMixed.Codes) is context only: the model hears it
// but does not speak it again, and it gets no file. Redoing one turn sends
// the turns before it with their codes and the turn without. One-track
// models take context turns only before the first turn to speak.
type DialogueTurn struct {
	Speaker string `json:"speaker"` // a DialogueSpeaker.ID
	Text    string `json:"text"`
	Codes   []int  `json:"codes,omitempty"`
}

// DialogueParams are tts.dialogue's params. Unset sampling fields take the
// model's defaults (SoulX-Podcast: temperature 0.6, top-p 0.9, top-k 100,
// repetition penalty 1.25, at most 3000 tokens a turn, 15 flow steps;
// MOSS-TTSD: audio temperature 1.1, top-p 0.9, top-k 50, repetition
// penalty 1.1, at most 2000 frames in all, 160 s).
type DialogueParams struct {
	Model     string            `json:"model"`
	ModelPath string            `json:"modelPath,omitempty"`
	Speakers  []DialogueSpeaker `json:"speakers"`
	Turns     []DialogueTurn    `json:"turns"`
	// OutputDir is where the files go: turn-<index, 3 digits>.wav per turn,
	// or dialogue.wav for a one-track model. The app owns it.
	OutputDir         string   `json:"outputDir"`
	Bits              int      `json:"bits,omitempty"` // 16 (default), 24, 32 (float)
	Language          string   `json:"language,omitempty"`
	Seed              *int64   `json:"seed,omitempty"`
	Temperature       *float64 `json:"temperature,omitempty"`
	TopP              *float64 `json:"topP,omitempty"`
	TopK              int      `json:"topK,omitempty"`
	RepetitionPenalty *float64 `json:"repetitionPenalty,omitempty"`
	MaxTokens         int      `json:"maxTokens,omitempty"` // per turn (one-track: in all)
	Steps             int      `json:"steps,omitempty"`     // flow-matching steps
}

// DialogueTurnOut is a turn of the result: per-turn models fill Output,
// Frames, AudioSeconds and Codes; one-track models give Index, Speaker and
// Text only.
type DialogueTurnOut struct {
	Index        int     `json:"index"` // in DialogueParams.Turns
	Speaker      string  `json:"speaker"`
	Text         string  `json:"text,omitempty"`
	Output       string  `json:"output,omitempty"`
	Frames       int64   `json:"frames,omitempty"`
	AudioSeconds float64 `json:"audioSeconds,omitempty"`
	Codes        []int   `json:"codes,omitempty"`
	Seconds      float64 `json:"seconds,omitempty"` // generation time of the turn
}

// DialogueMixed is a one-track model's file (VibeVoice's without codes:
// its turns cannot be redone by continuation). MOSS-TTSD adds its codec
// frames (Codebooks codes each, flattened, 12.5 frames a second): the first
// comes just before the audio, frame k+1 lies under samples
// [k·1920, (k+1)·1920); a redo sends the frames before the turn it redoes
// as the earlier turns' Codes.
type DialogueMixed struct {
	Output       string  `json:"output"`
	Frames       int64   `json:"frames"`
	AudioSeconds float64 `json:"audioSeconds"`
	Codes        []int   `json:"codes,omitempty"`
	Codebooks    int     `json:"codebooks,omitempty"`
}

// DialogueResult is tts.dialogue's result: the generated turns in order
// (context-only turns are left out), or the mixed track and every turn.
type DialogueResult struct {
	Model        string            `json:"model"`
	SampleRate   int               `json:"sampleRate"`
	Channels     int               `json:"channels"`
	Turns        []DialogueTurnOut `json:"turns"`
	Mixed        *DialogueMixed    `json:"mixed,omitempty"`
	AudioSeconds float64           `json:"audioSeconds"`
	Seconds      float64           `json:"seconds"`
	RTF          float64           `json:"rtf"`
	Seed         *int64            `json:"seed,omitempty"`
}

// TurnParams is tts.turn's params: a turn's file is written.
type TurnParams struct {
	Job          string  `json:"job"`
	Index        int     `json:"index"`
	Speaker      string  `json:"speaker"`
	Output       string  `json:"output"`
	Frames       int64   `json:"frames"`
	AudioSeconds float64 `json:"audioSeconds"`
	SampleRate   int     `json:"sampleRate"`
}
