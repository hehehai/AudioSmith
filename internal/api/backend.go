// Package api is AudioSmith's Local API (M5): an HTTP server inside the
// app process that speaks OpenAI's audio API under /v1, AudioSmith's own
// API under /api (voices, takes, generation, transcripts, jobs, models,
// tools), job events as Server-Sent Events, streaming speech over a
// WebSocket (/ws/tts), its OpenAPI 3.1 description (/openapi.json, with a
// bundled reference viewer at /docs), and mounts the MCP server (/mcp,
// internal/mcp).
//
// Every request goes through the app's services and its one job queue
// (interactive priority), as the UI's own work does: the API is a second
// client of the same domain, not a second engine. The engine may restart
// under it: requests then wait in the queue, or get 503 with Retry-After
// while no worker can run them. The licence gate applies (403).
//
// Security (auth.go): the server listens on 127.0.0.1 only; Host and
// Origin are checked (DNS rebinding, browsers); state-changing /api routes
// need a CSRF header; an API key (Bearer) is optional on loopback and
// required by the "Require a key" setting; LAN sharing opens a second
// listener whose clients need a one-time 8-digit PIN (consumption routes)
// or the key.
//
// The app (package main, api*.go) implements Backend over its library and
// engine; tests use a fake over the real job scheduler.
//
// Imports: core, jobs, services (job params and results), catalog,
// settings, engine/proto, translate, lang, dsp/{wav,resample}.
package api

import (
	"context"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/translate"
)

// Backend is what the API needs from the app. Every method is
// goroutine-safe and may block on I/O.
type Backend interface {
	// Status is the state of the engine (cheap, from memory).
	Status() Status
	// Settings are the app's settings (memory, no I/O): the API section,
	// the Studio's and Transcribe's default models.
	Settings() settings.Settings

	// Models are the catalogue's entries with their local state.
	Models(ctx context.Context) ([]Model, error)
	// DownloadModel queues the download of a catalogue entry (licence
	// accepted or not: the gate is for running) and returns its job id.
	DownloadModel(ctx context.Context, id string) (string, error)

	Voices(ctx context.Context, q core.VoiceQuery) ([]core.Voice, error)
	Voice(ctx context.Context, id string) (core.Voice, error)
	// SaveVoice creates (empty ID) a profile, copying files into it.
	SaveVoice(ctx context.Context, v core.Voice, f core.VoiceFiles) (core.Voice, error)
	DeleteVoice(ctx context.Context, id string) error
	// VoiceFile is the absolute path of one of a voice's files.
	VoiceFile(v core.Voice, name string) string
	// VoiceFits reports whether model e can speak with v, and why not
	// (English).
	VoiceFits(v core.Voice, e catalog.Entry) (bool, string)

	// Generate builds the take recipe of req (the voice applied for the
	// model, instructions mapped to what the model takes) and queues it
	// as the take req.ID (job owner "take:<req.ID>"), interactive unless
	// req.Background. It returns the recipe and the job id, or refuses
	// at once: *core.LicenceRequiredError, core.ErrNotDownloaded,
	// core.ErrInvalid.
	Generate(ctx context.Context, req SpeakRequest) (core.Take, string, error)

	Takes(ctx context.Context, q core.TakeQuery) ([]core.Take, error)
	Take(ctx context.Context, id string) (core.Take, error)
	TakeAudio(t core.Take) string
	// TakeSaved waits until the take id that Generate queued is in the
	// history (its job ends just before), or will never be.
	TakeSaved(ctx context.Context, id string) error
	DeleteTake(ctx context.Context, id string) error

	// Transcribe queues a transcription (owner "transcript:<req.ID>",
	// req.ID set by the caller) and returns its job id.
	Transcribe(ctx context.Context, req services.TranscribeRequest) (string, error)
	Transcripts(ctx context.Context, q core.TranscriptQuery) ([]core.Transcript, error)
	Transcript(ctx context.Context, id string) (core.Transcript, error)
	DeleteTranscript(ctx context.Context, id string) error

	// Translate translates with the language models of Settings →
	// Writing help; core.ErrUnsupported when none is set up.
	Translate(ctx context.Context, req translate.Request) (translate.Result, error)
	// Direct parses a performance note (never fails: keywords without
	// a language model).
	Direct(ctx context.Context, note string) translate.Direction
	// Fit predicts whether a line fits a slot (and may shorten it).
	Fit(ctx context.Context, req translate.FitRequest) (translate.FitResult, error)

	// Probe describes a media file (the engine's media worker).
	Probe(ctx context.Context, path string) (proto.ProbeResult, error)
	// CanEncode reports whether media.encode runs (M4A/AAC output).
	CanEncode() bool
	// DetectWatermark checks a file for the invisible watermark
	// (AudioSeal, thread #14): core.ErrNotDownloaded without the model.
	DetectWatermark(ctx context.Context, path string) (proto.WatermarkDetectResult, error)

	// TempDir holds uploads and encoded responses (removed after use);
	// DataDir is the app's data folder (the MCP sandbox's default parent).
	TempDir() string
	DataDir() string
}

// FormatEncoder is implemented by backends that also encode MP3, Opus
// or FLAC (M7a: through the optional ffmpeg, behind media.encode). A
// Backend without it answers those formats 400 unsupported_format.
type FormatEncoder interface {
	EncodesFormat(format string) bool
}

// Status is the engine's state for the API.
type Status struct {
	// State: "ready", "starting", "restarting", "failed", "stopped".
	State string `json:"state"`
	// Detail says why it is not ready (English).
	Detail        string `json:"detail,omitempty"`
	Version       string `json:"version"`                 // the app's
	EngineVersion string `json:"engineVersion,omitempty"` // the engine's
	Device        string `json:"device,omitempty"`        // "Apple M4"
	Memory        int64  `json:"memory,omitempty"`        // unified memory, bytes
	// Families the engine can load; nil until it is ready.
	Families []string `json:"families,omitempty"`
	// Loaded are the repos of resident models.
	Loaded []string `json:"loaded,omitempty"`
	// RetryAfter is a hint, in seconds, while not ready (restart planned).
	RetryAfter int `json:"-"`
}

// Engine states.
const (
	StateReady      = "ready"
	StateStarting   = "starting"
	StateRestarting = "restarting"
	StateFailed     = "failed"
	StateStopped    = "stopped"
)

// Model is a catalogue entry and its local state.
type Model struct {
	Entry      catalog.Entry
	Downloaded bool
	// Licence says whether the licence must still be accepted before use.
	LicenceNeeded bool
}

// SpeakRequest is a speech request the API resolved: its model and voice
// are known.
type SpeakRequest struct {
	ID    string // the take's id (core.NewID)
	Text  string
	Model catalog.Entry
	// Voice is a saved voice to speak with; nil = none.
	Voice *core.Voice
	// Speaker is a built-in speaker of the model; "" = its default, or
	// one of Gender when that is set.
	Speaker string
	Gender  string // "female" | "male" | ""
	// Instructions describe the delivery (instruct models), or the voice
	// to design (design models without a saved voice).
	Instructions string
	// Language is BCP-47 or the model's own value; "" = the voice's.
	Language string
	// RefAudio / RefText clone from a clip without saving a voice.
	RefAudio, RefText string

	Speed, Temperature, TopP, Guidance, Exaggeration, Duration float64
	Seed                                                       int64
	Steps                                                      int
	// Normalize applies "Even out volume" (mastering): off for API
	// requests unless asked, so streamed audio equals the take.
	Normalize bool
	Bits      int // 16 (0), 24, 32 (float)
	// Client names the agent asking (MCP, X-AudioSmith-Client), "" when
	// it did not say.
	Client string
	// Background queues behind interactive work.
	Background bool
}
