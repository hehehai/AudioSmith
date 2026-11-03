package proto

import "strings"

// Role is what an engine process is for; one Swift binary runs as any of
// them (audiosmith-engine --role gpu|listen|media).
type Role string

const (
	RoleGPU    Role = "gpu"    // MLX models: TTS, STT, LLM, VAD, diarization, separation
	RoleListen Role = "listen" // microphone capture + small streaming ASR (M2)
	RoleMedia  Role = "media"  // AVFoundation/PDFKit/Vision, no MLX (M4)
)

// Methods of protocol v2 implemented in M0. Requests marked "job" are
// long-running: the engine may send job.progress (and tts.chunk) for them,
// job.cancel stops them, and they count against hello's concurrency.
// Other requests are control requests: answered promptly, even while jobs
// run, and not cancellable.
const (
	MethodHello    = "engine.hello"    // request; must be first
	MethodStats    = "engine.stats"    // request
	MethodFlush    = "engine.flush"    // request: drop MLX caches
	MethodShutdown = "engine.shutdown" // request: cancel jobs, answer, exit 0

	MethodModelsLoad   = "models.load"   // request, job
	MethodModelsUnload = "models.unload" // request
	MethodModelsLoaded = "models.loaded" // request

	MethodTTSSynthesize = "tts.synthesize" // request, job

	MethodJobCancel = "job.cancel" // request

	// Notifications, engine → app.
	MethodJobProgress = "job.progress"
	MethodTTSChunk    = "tts.chunk"
	MethodLog         = "log"
)

// Methods added in M1 (docs/protocol-v2.md §5.2). Role: see RoleOf.
const (
	MethodTTSEncodeVoice = "tts.encodeVoice" // request, job (gpu): reference → cached voice prompt file

	MethodSTTTranscribe = "stt.transcribe" // request, job (gpu)
	MethodSTTAlign      = "stt.align"      // request, job (gpu): forced alignment
	MethodVADDetect     = "vad.detect"     // request, job (gpu)
	MethodEnhanceRun    = "enhance.run"    // request, job (gpu): denoise

	MethodMediaProbe  = "media.probe"  // request (media)
	MethodMediaDecode = "media.decode" // request, job (media): any audio/video → WAV
	MethodMediaEncode = "media.encode" // request, job (media): WAV → M4B/M4A (AAC) with chapters, tags, cover (M3)

	MethodCaptureDevices = "capture.devices" // request (listen)
	MethodCaptureStart   = "capture.start"   // request, job (listen): records until capture.stop
	MethodCaptureStop    = "capture.stop"    // request (listen)
	MethodCaptureLevel   = "capture.level"   // notification, engine → app: input meter, ~20 Hz
)

// Methods added in M4, dubbing (docs/protocol-v2.md §5.3; types in dub.go).
const (
	MethodSeparateRun    = "separate.run"    // request, job (gpu): vocals / background stems
	MethodDiarizeRun     = "diarize.run"     // request, job (gpu): who speaks when
	MethodMediaMux       = "media.mux"       // request, job (media): MP4 with the source video, N audio tracks, subtitles
	MethodMediaThumbnail = "media.thumbnail" // request, job (media): video frames as images
	MethodMediaTranscode = "media.transcode" // request, job (media): an H.264 preview
)

// Reserved method names for later milestones (see docs/protocol-v2.md §8).
// Listed so names are not reused for something else; not implemented.
var Reserved = []string{
	"tts.batch",
	"stt.stream.open", "stt.stream.push", "stt.stream.close", "stt.partial", "stt.final",
	"lid.detect",
	"translate.run",
	"media.timePitch", "media.ocr", "doc.extractText",
	"capture.pcm",
}

// ListenServes reports whether the listen worker also serves method: it
// keeps one small speech-to-text model of its own, so dictation does not
// wait behind the gpu worker's renders.
func ListenServes(method string) bool {
	switch method {
	case MethodSTTTranscribe, MethodModelsLoad, MethodModelsUnload, MethodModelsLoaded:
		return true
	}
	return false
}

// KindFor is the job kind (and executor) that runs method on the worker of
// role: the method itself on its usual worker (RoleOf), else "role:method"
// ("listen:stt.transcribe"). A role that does not serve method gets the
// usual worker's kind.
func KindFor(role Role, method string) string {
	if role == "" || role == RoleOf(method) || role == RoleListen && !ListenServes(method) {
		return method
	}
	return string(role) + ":" + method
}

// SplitKind is the inverse of KindFor: the role and method of a job kind.
func SplitKind(kind string) (Role, string) {
	if r, m, ok := strings.Cut(kind, ":"); ok {
		return Role(r), m
	}
	return RoleOf(kind), kind
}

// RoleOf is the role whose worker serves method: the app sends it there,
// and the engine answers method_not_found to it in any other role.
// Methods every role answers (engine.*, job.cancel) are RoleGPU here;
// models.* go to whichever worker holds the model (gpu in M1).
func RoleOf(method string) Role {
	switch method {
	case MethodMediaProbe, MethodMediaDecode, MethodMediaEncode, MethodMediaMux, MethodMediaThumbnail, MethodMediaTranscode,
		MethodWatermarkEmbed, MethodWatermarkDetect:
		return RoleMedia
	case MethodCaptureDevices, MethodCaptureStart, MethodCaptureStop:
		return RoleListen
	}
	return RoleGPU
}

// HelloParams opens a session. The app sends it first, right after
// spawning the process; until it is answered every other request fails
// with not_initialized.
type HelloParams struct {
	Protocol int    `json:"protocol"`           // Version
	App      string `json:"app"`                // app version, for logs
	Role     Role   `json:"role"`               // must match the process's --role
	CacheDir string `json:"cacheDir,omitempty"` // Hugging Face hub cache the app manages; "" = engine default
	Offline  bool   `json:"offline,omitempty"`  // never download: missing files → model_not_downloaded
	LogLevel string `json:"logLevel,omitempty"` // "debug" | "info" (default) | "warning" | "error"
}

// HelloResult describes the engine. The app refuses (and reports) an
// engine whose Protocol differs from Version or whose Role differs.
type HelloResult struct {
	Protocol    int      `json:"protocol"`
	Version     string   `json:"version"` // engine build version
	Role        Role     `json:"role"`
	PID         int      `json:"pid"`
	Concurrency int      `json:"concurrency"` // jobs it runs at once; more → busy
	Methods     []string `json:"methods"`     // requests it implements
	Families    []string `json:"families"`    // model families it can load (catalog.Entry.Family)
	Device      Device   `json:"device"`
}

// Device is the machine the engine runs on.
type Device struct {
	Name   string `json:"name"`   // "Apple M2 Pro"
	Memory int64  `json:"memory"` // physical memory, bytes
	OS     string `json:"os"`     // "macOS 15.1"
	MLX    string `json:"mlx,omitempty"`
}

// StatsResult answers engine.stats: memory for the supervisor's budget and
// the ⌘E switcher.
type StatsResult struct {
	Memory Memory        `json:"memory"`
	Models []LoadedModel `json:"models"`
	Jobs   []string      `json:"jobs"` // ids of running jobs
}

// Memory is MLX's accounting, in bytes.
type Memory struct {
	Active int64 `json:"active"`
	Cache  int64 `json:"cache"`
	Peak   int64 `json:"peak"`
}

// LoadedModel is a resident model.
type LoadedModel struct {
	Model       string  `json:"model"` // repo id
	Task        string  `json:"task"`  // "tts", "stt"… (catalog task)
	SampleRate  int     `json:"sampleRate,omitempty"`
	Bytes       int64   `json:"bytes,omitempty"`       // estimated resident size
	IdleSeconds float64 `json:"idleSeconds,omitempty"` // since last use, for LRU eviction
}

// LoadParams loads a model ahead of use (jobs load what they need anyway).
type LoadParams struct {
	Model string `json:"model"`          // repo id
	Path  string `json:"path,omitempty"` // local snapshot directory; "" = resolve Model in the cache
	Task  string `json:"task,omitempty"` // "" = "tts"
}

// LoadResult reports what the model turned out to support; it can confirm
// or extend the catalogue entry.
type LoadResult struct {
	Model        string   `json:"model"`
	Task         string   `json:"task"`
	SampleRate   int      `json:"sampleRate"`
	Seconds      float64  `json:"seconds"` // load time
	Bytes        int64    `json:"bytes,omitempty"`
	Voices       []string `json:"voices,omitempty"`
	Languages    []string `json:"languages,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"` // catalog capability names
}

// UnloadParams frees one model, or all with Model "". With IdleAtLeast
// (seconds) only models no job uses and unused for at least that long
// are dropped (the supervisor's idle and budget unloads: a model a job
// took since its last engine.stats stays); 0 drops them whatever (a
// model a job uses stays alive until the job ends).
type UnloadParams struct {
	Model       string  `json:"model,omitempty"`
	IdleAtLeast float64 `json:"idleAtLeast,omitempty"`
}

// UnloadResult lists what was unloaded.
type UnloadResult struct {
	Unloaded []string `json:"unloaded"`
}

// LoadedResult answers models.loaded.
type LoadedResult struct {
	Models []LoadedModel `json:"models"`
}

// SynthesizeParams makes speech from text into a file. Unset optional
// parameters use the model's defaults. The model loads first if needed
// (stage "loading"); a model that cannot honour a set parameter ignores it
// unless that parameter is the point of the request (refAudio on a model
// that cannot clone → unsupported).
type SynthesizeParams struct {
	Model string `json:"model"`
	// ModelPath is the model's local snapshot directory (hub.Local.Snapshot),
	// as in LoadParams.Path; "" resolves Model in the cache. The app fills
	// it when the job runs, so engines started offline never download.
	ModelPath string `json:"modelPath,omitempty"`
	Text      string `json:"text"`
	// Voice is a built-in speaker id; for design models (catalogue
	// "design"/"voiceStyle": Qwen3-TTS VoiceDesign, OmniVoice) the voice
	// description, as voicedesign.Lowered.Voice makes it; for Qwen3-TTS
	// CustomVoice "Speaker" or "Speaker, instruction".
	Voice    string `json:"voice,omitempty"`
	Language string `json:"language,omitempty"` // value the model takes ("en", "Chinese"…)
	// Instruct is a style/emotion instruction (catalogue "instruct"):
	// CustomVoice appends it to the speaker ("Vivian, 非常开心"); design
	// models take it as the description when Voice is empty.
	Instruct    string   `json:"instruct,omitempty"`
	Speed       *float64 `json:"speed,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"topP,omitempty"`
	MaxTokens   int      `json:"maxTokens,omitempty"`
	Seed        *int64   `json:"seed,omitempty"`
	Steps       int      `json:"steps,omitempty"`    // diffusion/refine steps (OmniVoice, M1)
	Duration    *float64 `json:"duration,omitempty"` // target seconds (OmniVoice, M1)
	RefAudio    string   `json:"refAudio,omitempty"` // absolute path of a reference clip (cloning)
	RefText     string   `json:"refText,omitempty"`
	// VoicePrompt is a file tts.encodeVoice wrote for this model: the
	// clone reference, already encoded. It wins over RefAudio/RefText;
	// when it cannot be used (another model, an older format) the engine
	// falls back to RefAudio and logs a warning, or fails with
	// bad_request when there is none.
	VoicePrompt string `json:"voicePrompt,omitempty"`
	// Guidance is the classifier-free guidance scale (OmniVoice
	// guidance_scale, Chatterbox cfg_weight); Exaggeration is
	// Chatterbox's emotion exaggeration. Both M1, catalogue capabilities
	// "guidance" and "exaggeration".
	Guidance     *float64 `json:"guidance,omitempty"`
	Exaggeration *float64 `json:"exaggeration,omitempty"`
	Output       Output   `json:"output"`
	// Stream asks for tts.chunk notifications as audio is generated, in
	// addition to the file.
	Stream bool `json:"stream,omitempty"`
}

// Output says where and how to write audio. The engine writes to a
// temporary name beside Path and renames it, so Path never holds a partial file.
type Output struct {
	Path   string `json:"path"`             // absolute; the app owns the directory
	Format string `json:"format,omitempty"` // "wav" (default; the only one in M0)
	Bits   int    `json:"bits,omitempty"`   // 16 (default) | 24 | 32 (float)
}

// SynthesizeResult describes the written file.
type SynthesizeResult struct {
	Output       string  `json:"output"`
	Model        string  `json:"model"`
	SampleRate   int     `json:"sampleRate"`
	Channels     int     `json:"channels"`
	Frames       int64   `json:"frames"`
	AudioSeconds float64 `json:"audioSeconds"`
	Seconds      float64 `json:"seconds"` // generation time, excluding model load
	RTF          float64 `json:"rtf"`     // audioSeconds / seconds
	Seed         *int64  `json:"seed,omitempty"`
}

// CancelParams names the job (the id of its request) to cancel.
type CancelParams struct {
	Job string `json:"job"`
}

// CancelResult says whether a running or queued job was found. The job's
// own request then answers with a cancelled error.
type CancelResult struct {
	Cancelled bool `json:"cancelled"`
}

// Empty is the params/result of methods that take or return nothing ({}).
type Empty struct{}

// ProgressParams reports a job's progress. All of a job's notifications
// are written before its response.
type ProgressParams struct {
	Job          string   `json:"job"`
	Stage        string   `json:"stage"`              // "loading" | "generating" | "writing" (open set)
	Fraction     *float64 `json:"fraction,omitempty"` // 0–1; absent when unknown
	AudioSeconds float64  `json:"audioSeconds,omitempty"`
	Message      string   `json:"message,omitempty"` // English, for logs and the status line
}

// ChunkParams carries generated audio while a streaming job runs. Seq
// counts from 0 without gaps; Final marks the last chunk (which may be
// empty). Data is base64 in JSON (Go []byte, Swift Data).
type ChunkParams struct {
	Job        string `json:"job"`
	Seq        int    `json:"seq"`
	SampleRate int    `json:"sampleRate"`
	Channels   int    `json:"channels"`
	Encoding   string `json:"encoding"` // "pcm16le" (the only one in M0)
	Data       []byte `json:"data"`
	Final      bool   `json:"final,omitempty"`
}

// LogParams is a structured log line (stderr remains free-form text).
type LogParams struct {
	Level   string `json:"level"` // "debug" | "info" | "warning" | "error"
	Message string `json:"message"`
	Job     string `json:"job,omitempty"`
}

// ParamsFor returns a new value of the params type of method, or nil for
// an unknown method. ResultFor does the same for results. The supervisor
// and the fixture tests use them.
func ParamsFor(method string) any {
	switch method {
	case MethodTTSEncodeVoice:
		return new(EncodeVoiceParams)
	case MethodSTTTranscribe:
		return new(TranscribeParams)
	case MethodSTTAlign:
		return new(AlignParams)
	case MethodVADDetect:
		return new(VADParams)
	case MethodEnhanceRun:
		return new(EnhanceParams)
	case MethodMediaProbe:
		return new(ProbeParams)
	case MethodMediaDecode:
		return new(DecodeParams)
	case MethodMediaEncode:
		return new(EncodeParams)
	case MethodSeparateRun:
		return new(SeparateParams)
	case MethodDiarizeRun:
		return new(DiarizeParams)
	case MethodMediaMux:
		return new(MuxParams)
	case MethodMediaThumbnail:
		return new(ThumbnailParams)
	case MethodMediaTranscode:
		return new(TranscodeParams)
	case MethodCaptureDevices:
		return new(Empty)
	case MethodCaptureStart:
		return new(CaptureParams)
	case MethodCaptureStop:
		return new(CaptureStopParams)
	case MethodCaptureLevel:
		return new(LevelParams)
	case MethodLLMGenerate:
		return new(LLMGenerateParams)
	case MethodWatermarkEmbed:
		return new(WatermarkEmbedParams)
	case MethodWatermarkDetect:
		return new(WatermarkDetectParams)
	case MethodLLMToken:
		return new(LLMTokenParams)
	case MethodTTSDialogue:
		return new(DialogueParams)
	case MethodTTSTurn:
		return new(TurnParams)
	case MethodEngineMemory:
		return new(MemoryParams)
	case MethodHello:
		return new(HelloParams)
	case MethodStats, MethodFlush, MethodShutdown, MethodModelsLoaded:
		return new(Empty)
	case MethodModelsLoad:
		return new(LoadParams)
	case MethodModelsUnload:
		return new(UnloadParams)
	case MethodTTSSynthesize:
		return new(SynthesizeParams)
	case MethodJobCancel:
		return new(CancelParams)
	case MethodJobProgress:
		return new(ProgressParams)
	case MethodTTSChunk:
		return new(ChunkParams)
	case MethodLog:
		return new(LogParams)
	}
	return nil
}

// ResultFor: see ParamsFor.
func ResultFor(method string) any {
	switch method {
	case MethodTTSEncodeVoice:
		return new(EncodeVoiceResult)
	case MethodSTTTranscribe:
		return new(TranscribeResult)
	case MethodSTTAlign:
		return new(AlignResult)
	case MethodVADDetect:
		return new(VADResult)
	case MethodEnhanceRun:
		return new(EnhanceResult)
	case MethodMediaProbe:
		return new(ProbeResult)
	case MethodMediaDecode:
		return new(DecodeResult)
	case MethodMediaEncode:
		return new(EncodeResult)
	case MethodSeparateRun:
		return new(SeparateResult)
	case MethodDiarizeRun:
		return new(DiarizeResult)
	case MethodMediaMux:
		return new(MuxResult)
	case MethodMediaThumbnail:
		return new(ThumbnailResult)
	case MethodMediaTranscode:
		return new(TranscodeResult)
	case MethodLLMGenerate:
		return new(LLMGenerateResult)
	case MethodTTSDialogue:
		return new(DialogueResult)
	case MethodWatermarkEmbed:
		return new(WatermarkEmbedResult)
	case MethodWatermarkDetect:
		return new(WatermarkDetectResult)
	case MethodCaptureDevices:
		return new(DevicesResult)
	case MethodCaptureStart:
		return new(CaptureResult)
	case MethodCaptureStop:
		return new(CaptureStopResult)
	case MethodHello:
		return new(HelloResult)
	case MethodStats:
		return new(StatsResult)
	case MethodFlush, MethodShutdown:
		return new(Empty)
	case MethodModelsLoad:
		return new(LoadResult)
	case MethodModelsUnload:
		return new(UnloadResult)
	case MethodModelsLoaded:
		return new(LoadedResult)
	case MethodTTSSynthesize:
		return new(SynthesizeResult)
	case MethodJobCancel:
		return new(CancelResult)
	}
	return nil
}

// IsJob reports whether method is a long-running, cancellable job.
func IsJob(method string) bool {
	switch method {
	case MethodModelsLoad, MethodTTSSynthesize, MethodTTSEncodeVoice,
		MethodSTTTranscribe, MethodSTTAlign, MethodVADDetect, MethodEnhanceRun,
		MethodMediaDecode, MethodMediaEncode, MethodCaptureStart,
		MethodSeparateRun, MethodDiarizeRun, MethodMediaMux, MethodMediaThumbnail, MethodMediaTranscode,
		MethodLLMGenerate, MethodWatermarkEmbed, MethodWatermarkDetect, MethodTTSDialogue:
		return true
	}
	return false
}

// JobMethods lists the job methods, in protocol order.
var JobMethods = []string{
	MethodModelsLoad, MethodTTSSynthesize, MethodTTSEncodeVoice,
	MethodSTTTranscribe, MethodSTTAlign, MethodVADDetect, MethodEnhanceRun,
	MethodMediaDecode, MethodMediaEncode, MethodCaptureStart,
	MethodSeparateRun, MethodDiarizeRun, MethodMediaMux, MethodMediaThumbnail, MethodMediaTranscode,
	MethodLLMGenerate, MethodWatermarkEmbed, MethodWatermarkDetect, MethodTTSDialogue,
}
