package proto

// Params and results of the methods M1 adds (docs/protocol-v2.md §5.2).
//
// Conventions shared by all of them:
//   - Paths are absolute. Inputs ("audio", "input", "refAudio") may be any
//     file AVFoundation reads (WAV, AIFF, CAF, MP3, M4A/AAC, ALAC, FLAC,
//     and the audio of MOV/MP4/M4V); the engine converts to what the model
//     needs (rate, channels). A missing input fails with file_not_found, an
//     unreadable one with unsupported_media.
//   - Outputs are WAV files written to a temporary name and renamed
//     (Output, as for tts.synthesize).
//   - Times are seconds from the start of the input, as float64.
//   - Language codes are BCP-47 as in internal/lang ("en", "zh", "yue",
//     "pt-BR"); the engine maps them to what the model takes. That differs
//     from tts.synthesize, whose language is the catalogue's Value.
//   - Model jobs take Model (repo id) and ModelPath (snapshot directory,
//     filled by the app's executors) like tts.synthesize, and load the
//     model first if needed (stage "loading").

// AudioOut describes a WAV file the engine wrote.
type AudioOut struct {
	Output       string  `json:"output"` // the path written (= the request's output.path)
	SampleRate   int     `json:"sampleRate"`
	Channels     int     `json:"channels"`
	Frames       int64   `json:"frames"`
	AudioSeconds float64 `json:"audioSeconds"`
}

// ---- tts.encodeVoice (job, gpu) ----

// EncodeVoiceParams encodes a clone reference once, so later takes skip
// it: the engine writes the model's own conditioning (speaker embedding,
// codec tokens…) to PromptPath. Models that cannot fail with unsupported,
// data {"capability":"voicePrompt"} (catalogue capability "voicePrompt").
type EncodeVoiceParams struct {
	Model     string `json:"model"`
	ModelPath string `json:"modelPath,omitempty"`
	RefAudio  string `json:"refAudio"`
	RefText   string `json:"refText,omitempty"`  // transcript of RefAudio; some models need it
	Language  string `json:"language,omitempty"` // as tts.synthesize's
	// PromptPath is where to write the prompt (the app owns the
	// directory; by convention "<voice dir>/<model id>.voiceprompt").
	PromptPath string `json:"promptPath"`
}

// EncodeVoiceResult describes the prompt file. Format names the model
// family and the file's version ("qwen3_tts/1"); a synthesize whose
// prompt has another format falls back to refAudio.
type EncodeVoiceResult struct {
	VoicePrompt  string  `json:"voicePrompt"` // = PromptPath
	Model        string  `json:"model"`
	Format       string  `json:"format"`
	Bytes        int64   `json:"bytes"`
	AudioSeconds float64 `json:"audioSeconds"` // reference audio used (models may trim it)
	Seconds      float64 `json:"seconds"`
}

// ---- stt.transcribe (job, gpu) ----

// TranscribeParams transcribes a file.
type TranscribeParams struct {
	Model     string `json:"model"`
	ModelPath string `json:"modelPath,omitempty"`
	Audio     string `json:"audio"`
	// Language is a BCP-47 code; "" or "auto" detects it.
	Language string `json:"language,omitempty"`
	// Task is "transcribe" (default) or "translate" (into English;
	// Whisper only, else unsupported with capability "translate").
	Task string `json:"task,omitempty"`
	// Words asks for word timings. Models without them (catalogue
	// "wordTimes" unset) ignore it: run stt.align on the text.
	Words bool `json:"words,omitempty"`
	// Prompt is context or vocabulary the model should expect (Whisper's
	// initial prompt, Qwen3-ASR's context); models without one ignore it.
	Prompt string `json:"prompt,omitempty"`
}

// TranscribeResult is the transcript. Segments are in time order and
// cover Text (joined with spaces, or nothing for CJK).
type TranscribeResult struct {
	Model    string    `json:"model"`
	Text     string    `json:"text"`
	Language string    `json:"language"` // BCP-47: the one detected, or the one given
	Segments []Segment `json:"segments"`
	// AudioSeconds is the input's duration; Seconds the processing time
	// (without model load); RTF = AudioSeconds / Seconds.
	AudioSeconds float64 `json:"audioSeconds"`
	Seconds      float64 `json:"seconds"`
	RTF          float64 `json:"rtf"`
}

// Segment is a stretch of the transcript. Whisper gives about one per
// 30 s window, Qwen3-ASR one per utterance, MOSS-Transcribe-Diarize one
// per speaker turn.
type Segment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
	Words []Word  `json:"words,omitempty"` // when asked and the model has them
	// Speaker is the model's anonymous label of who says it ("S01",
	// "S02"…, numbered in order of appearance within one call) for models
	// that tell speakers apart (catalogue capability "speakers"); "" else.
	Speaker string `json:"speaker,omitempty"`
}

// Word is one timed word (one character for Chinese/Japanese, as aligners
// emit them). Prob is the model's confidence, 0–1, when it reports one.
type Word struct {
	Text  string   `json:"text"`
	Start float64  `json:"start"`
	End   float64  `json:"end"`
	Prob  *float64 `json:"prob,omitempty"`
}

// ---- stt.align (job, gpu) ----

// AlignParams aligns a known transcript to its audio (Qwen3-ForcedAligner).
type AlignParams struct {
	Model     string `json:"model"`
	ModelPath string `json:"modelPath,omitempty"`
	Audio     string `json:"audio"`
	Text      string `json:"text"`
	Language  string `json:"language,omitempty"` // BCP-47; "" lets the model guess from the text
}

// AlignResult lists Text's words with times, in order. Punctuation is not
// a word; the app maps words back onto its lines.
type AlignResult struct {
	Model        string  `json:"model"`
	Words        []Word  `json:"words"`
	AudioSeconds float64 `json:"audioSeconds"`
	Seconds      float64 `json:"seconds"`
}

// ---- vad.detect (job, gpu) ----

// VADParams finds speech (Silero VAD). Unset tuning uses the model's
// defaults: threshold 0.5, minSpeech 0.25 s, minSilence 0.1 s, pad 0.03 s.
type VADParams struct {
	Model      string   `json:"model"`
	ModelPath  string   `json:"modelPath,omitempty"`
	Audio      string   `json:"audio"`
	Threshold  *float64 `json:"threshold,omitempty"`  // speech probability, 0–1
	MinSpeech  *float64 `json:"minSpeech,omitempty"`  // shorter speech is dropped, seconds
	MinSilence *float64 `json:"minSilence,omitempty"` // shorter pauses are bridged, seconds
	Pad        *float64 `json:"pad,omitempty"`        // added on both sides of a region, seconds
}

// VADResult lists the speech regions in time order, not overlapping.
type VADResult struct {
	Model        string   `json:"model"`
	Regions      []Region `json:"regions"`
	AudioSeconds float64  `json:"audioSeconds"`
	Seconds      float64  `json:"seconds"`
}

// Region is a stretch of speech; Prob its mean speech probability.
type Region struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Prob  float64 `json:"prob,omitempty"`
}

// ---- enhance.run (job, gpu) ----

// EnhanceParams denoises a file (DeepFilterNet v3).
type EnhanceParams struct {
	Model     string `json:"model"`
	ModelPath string `json:"modelPath,omitempty"`
	Audio     string `json:"audio"`
	Output    Output `json:"output"`
	// Strength mixes the enhanced signal over the original, 0–1 (default
	// 1, fully enhanced); lower keeps some room tone.
	Strength *float64 `json:"strength,omitempty"`
}

// EnhanceResult describes the output: mono, at the model's rate (48 kHz
// for DeepFilterNet); the app resamples if it needs another.
type EnhanceResult struct {
	AudioOut
	Model   string  `json:"model"`
	Seconds float64 `json:"seconds"`
}

// ---- media.probe (control, media) ----

// ProbeParams names a file to inspect.
type ProbeParams struct {
	Input string `json:"input"`
}

// ProbeResult describes a media file. Audio fields describe its first
// audio track (zero when HasAudio is false).
type ProbeResult struct {
	Format      string     `json:"format"`   // container: "wav", "aiff", "caf", "mp3", "m4a", "mp4", "mov", "flac"…
	Duration    float64    `json:"duration"` // seconds
	HasAudio    bool       `json:"hasAudio"`
	HasVideo    bool       `json:"hasVideo"`
	AudioTracks int        `json:"audioTracks,omitempty"`
	Codec       string     `json:"codec,omitempty"` // audio: "lpcm", "aac", "mp3", "alac", "flac", "opus"…
	SampleRate  int        `json:"sampleRate,omitempty"`
	Channels    int        `json:"channels,omitempty"`
	Bits        int        `json:"bits,omitempty"`    // PCM bit depth; 0 for compressed audio
	BitRate     int        `json:"bitRate,omitempty"` // bits per second, when known
	Video       *VideoInfo `json:"video,omitempty"`   // the first video track
}

// VideoInfo describes a video track.
type VideoInfo struct {
	Codec  string  `json:"codec"` // "h264", "hevc", "prores"…
	Width  int     `json:"width"`
	Height int     `json:"height"`
	FPS    float64 `json:"fps"`
}

// ---- media.decode (job, media) ----

// DecodeParams decodes (a window of) a file's audio to WAV.
type DecodeParams struct {
	Input  string `json:"input"`
	Output Output `json:"output"`
	// SampleRate and Channels of the output; 0 keeps the source's.
	// Channels 1 mixes down; 2 duplicates a mono source.
	SampleRate int `json:"sampleRate,omitempty"`
	Channels   int `json:"channels,omitempty"`
	// Start and Duration cut a window, seconds; Duration 0 = to the end.
	Start    float64 `json:"start,omitempty"`
	Duration float64 `json:"duration,omitempty"`
	// Track is the audio track's index (0 = the first).
	Track int `json:"track,omitempty"`
}

// DecodeResult describes the WAV written, and the source as media.probe
// would.
type DecodeResult struct {
	AudioOut
	Source  ProbeResult `json:"source"`
	Seconds float64     `json:"seconds"`
}

// ---- media.encode (job, media; M3) ----

// EncodeParams encode a WAV (RF64 allowed) into an M4B/M4A audiobook or
// track: AAC-LC, with a QuickTime chapter track (what Apple Books and
// QuickTime show), iTunes tags and cover art. services.EncodeParams is the
// same JSON.
type EncodeParams struct {
	Input  string `json:"input"`
	Output Output `json:"output"`          // path; format "m4b" or "m4a" ("mp3": unsupported, capability "mp3")
	Codec  string `json:"codec,omitempty"` // "aac" (default, the only one)
	// Bitrate in bits per second; 0 = 64 kb/s mono, 96 kb/s stereo.
	Bitrate    int             `json:"bitrate,omitempty"`
	SampleRate int             `json:"sampleRate,omitempty"` // 0 = the input's (at most 48 kHz)
	Chapters   []EncodeChapter `json:"chapters,omitempty"`
	Metadata   EncodeMetadata  `json:"metadata"`
	Cover      string          `json:"cover,omitempty"` // PNG or JPEG path, written as covr
}

// EncodeChapter is a chapter mark, seconds into the input.
type EncodeChapter struct {
	Title string  `json:"title"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// EncodeMetadata are the file's tags: ©nam (title), ©ART and aART
// (author), ©alb (title), ©wrt (narrator), ©day, ©gen, desc and ©cmt.
type EncodeMetadata struct {
	Title       string `json:"title,omitempty"`
	Author      string `json:"author,omitempty"`
	Narrator    string `json:"narrator,omitempty"`
	Year        string `json:"year,omitempty"`
	Genre       string `json:"genre,omitempty"`
	Description string `json:"description,omitempty"`
}

// EncodeResult describes the file written.
type EncodeResult struct {
	Output   string  `json:"output"`
	Format   string  `json:"format"`
	Bytes    int64   `json:"bytes"`
	Duration float64 `json:"duration"`
	Bitrate  int     `json:"bitrate,omitempty"`
}

// ---- capture.* (listen) ----

// DevicesResult answers capture.devices: the audio inputs, the system
// default first.
type DevicesResult struct {
	Devices []CaptureDevice `json:"devices"`
}

// CaptureDevice is an audio input.
type CaptureDevice struct {
	ID         string `json:"id"`   // Core Audio device UID: stable across launches
	Name       string `json:"name"` // "MacBook Air Microphone"
	Channels   int    `json:"channels"`
	SampleRate int    `json:"sampleRate"` // nominal rate
	Default    bool   `json:"default,omitempty"`
	// Transport is "builtin", "usb", "bluetooth", "aggregate", "virtual"
	// or "" (unknown).
	Transport string `json:"transport,omitempty"`
}

// CaptureParams starts recording. The capture.start job runs until
// capture.stop (or MaxSeconds, or the device going away) and answers with
// the CaptureResult; job.cancel discards the recording.
type CaptureParams struct {
	Device string `json:"device,omitempty"` // CaptureDevice.ID; "" = the system default input
	// InputChannels are the device's channels to record, numbered from 1
	// as in Audio MIDI Setup; default [1] (or [1,2] when Channels is 2).
	// With Channels 1, several are mixed down.
	InputChannels []int  `json:"inputChannels,omitempty"`
	Channels      int    `json:"channels,omitempty"`   // in the file: 1 (default) or 2
	SampleRate    int    `json:"sampleRate,omitempty"` // of the file; 0 = the device's
	Output        Output `json:"output"`
	// MaxSeconds stops the recording by itself; 0 = no limit.
	MaxSeconds float64 `json:"maxSeconds,omitempty"`
	// AEC records with macOS voice processing: echo cancellation, with
	// noise suppression and automatic gain (the voice may sound
	// processed). Off, the engine still turns it on by itself once when
	// the input brings no sound or keeps stopping (LevelParams.AECAuto),
	// except on a virtual or aggregate input (loopback: its silence is
	// exact zeros), when InputChannels are given, or on a Bluetooth input
	// before its warm-up (code review 1103 F1). When voice processing does
	// not start on the input, or would record another input, the engine
	// records without it (LevelParams.AECFallback).
	AEC bool `json:"aec,omitempty"`
}

// Why the engine recorded without voice processing though AEC asked for
// it (LevelParams.AECFallback, CaptureResult.AECFallback).
const (
	AECFallbackFailed = "failed" // voice processing did not start on this input
	AECFallbackDevice = "device" // voice processing would record another input (the system default)
)

// LevelParams is the input meter, about 20 times a second while
// recording, over the recorded channels. Levels are dBFS, at least -120.
type LevelParams struct {
	Job     string  `json:"job"`
	Seconds float64 `json:"seconds"` // recorded so far
	RMS     float64 `json:"rms"`
	Peak    float64 `json:"peak"`
	Clipped bool    `json:"clipped,omitempty"` // a sample reached full scale since the last level
	// AECAuto: the engine turned voice processing on during this
	// recording, for AECReason (CaptureNoSound, CaptureDeviceChanges);
	// every level after it says so.
	AECAuto   bool   `json:"aecAuto,omitempty"`
	AECReason string `json:"aecReason,omitempty"`
	// AECFallback: AEC was asked for but the recording runs without voice
	// processing (AECFallbackFailed, AECFallbackDevice); every level says so.
	AECFallback string `json:"aecFallback,omitempty"`
}

// CaptureResult describes the recording.
type CaptureResult struct {
	AudioOut
	Device string  `json:"device"` // the device's ID
	Peak   float64 `json:"peak"`   // loudest sample, dBFS
	// Clipped counts samples at full scale.
	Clipped int `json:"clipped,omitempty"`
	// Reason is why it ended: "stopped" (capture.stop), "maxSeconds",
	// "deviceLost" (unplugged: what was recorded is kept) or
	// "inputStopped" (the input stopped for good after it brought sound:
	// what was recorded is kept).
	Reason string `json:"reason"`
	// AEC: voice processing was on at the end; AECAuto: the engine turned
	// it on, for AECReason (as in LevelParams).
	AEC       bool   `json:"aec,omitempty"`
	AECAuto   bool   `json:"aecAuto,omitempty"`
	AECReason string `json:"aecReason,omitempty"`
	// AECFallback as in LevelParams.
	AECFallback string `json:"aecFallback,omitempty"`
	// AECMono: voice processing gives one channel, so the stereo file
	// holds it twice, and InputChannels did not apply.
	AECMono bool `json:"aecMono,omitempty"`
}

// Capture end reasons.
const (
	CaptureStopped    = "stopped"
	CaptureMaxSeconds = "maxSeconds"
	CaptureDeviceLost = "deviceLost"
	// CaptureInputStopped: the input stopped for good (it kept stopping
	// as the audio devices changed) after it brought sound.
	CaptureInputStopped = "inputStopped"
)

// Why the engine turned voice processing on, or a recording failed with
// CodeNoInput (its data's "reason").
const (
	CaptureNoSound       = "noSound"       // no buffers, or only digital silence, from the start
	CaptureDeviceChanges = "deviceChanges" // it kept stopping as the audio devices changed
)

// NoInputData is CodeNoInput's data.
type NoInputData struct {
	Device string `json:"device"` // the device's ID
	Name   string `json:"name"`
	Reason string `json:"reason"` // CaptureNoSound or CaptureDeviceChanges
	AEC    bool   `json:"aec,omitempty"` // voice processing was on (and did not help)
}

// CaptureStopParams names the capture.start job to stop.
type CaptureStopParams struct {
	Job string `json:"job"`
}

// CaptureStopResult says whether the capture was running; its job then
// answers with the CaptureResult (before this response).
type CaptureStopResult struct {
	Stopped bool `json:"stopped"`
}
