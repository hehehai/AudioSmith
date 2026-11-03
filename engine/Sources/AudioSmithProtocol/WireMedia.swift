import Foundation

// The methods M1 adds (docs/protocol-v2.md §5.2): the Swift mirror of
// internal/engine/proto/media.go. Optional here = omitempty there; the
// fixtures (engine/Fixtures/{stt,analysis,media,capture,voice}.jsonl)
// round-trip through both.

// MARK: - tts.encodeVoice (job, gpu)

public struct EncodeVoiceParams: Codable, Sendable {
    public var model: String
    public var modelPath: String?
    public var refAudio: String
    public var refText: String?
    public var language: String?
    public var promptPath: String
}

public struct EncodeVoiceResult: Codable, Sendable {
    public var voicePrompt: String
    public var model: String
    public var format: String
    public var bytes: Int64
    public var audioSeconds: Double
    public var seconds: Double

    public init(voicePrompt: String, model: String, format: String, bytes: Int64, audioSeconds: Double, seconds: Double) {
        self.voicePrompt = voicePrompt
        self.model = model
        self.format = format
        self.bytes = bytes
        self.audioSeconds = audioSeconds
        self.seconds = seconds
    }
}

// MARK: - stt.transcribe, stt.align (jobs, gpu)

public struct TranscribeParams: Codable, Sendable {
    public var model: String
    public var modelPath: String?
    public var audio: String
    /// BCP-47; nil, "" or "auto" detects.
    public var language: String?
    /// "transcribe" (default) or "translate".
    public var task: String?
    public var words: Bool?
    public var prompt: String?
}

public struct Word: Codable, Sendable, Equatable {
    public var text: String
    public var start: Double
    public var end: Double
    public var prob: Double?

    public init(text: String, start: Double, end: Double, prob: Double? = nil) {
        self.text = text
        self.start = start
        self.end = end
        self.prob = prob
    }
}

public struct Segment: Codable, Sendable, Equatable {
    public var start: Double
    public var end: Double
    public var text: String
    public var words: [Word]?
    /// Who says it: the model's anonymous label ("S01", "S02"…, in order of
    /// appearance within one call), for models that tell speakers apart
    /// (MOSS-Transcribe-Diarize); nil else.
    public var speaker: String?

    public init(start: Double, end: Double, text: String, words: [Word]? = nil, speaker: String? = nil) {
        self.start = start
        self.end = end
        self.text = text
        self.words = words
        self.speaker = speaker
    }
}

public struct TranscribeResult: Codable, Sendable {
    public var model: String
    public var text: String
    public var language: String
    public var segments: [Segment]
    public var audioSeconds: Double
    public var seconds: Double
    public var rtf: Double

    public init(model: String, text: String, language: String, segments: [Segment], audioSeconds: Double, seconds: Double, rtf: Double) {
        self.model = model
        self.text = text
        self.language = language
        self.segments = segments
        self.audioSeconds = audioSeconds
        self.seconds = seconds
        self.rtf = rtf
    }
}

public struct AlignParams: Codable, Sendable {
    public var model: String
    public var modelPath: String?
    public var audio: String
    public var text: String
    public var language: String?
}

public struct AlignResult: Codable, Sendable {
    public var model: String
    public var words: [Word]
    public var audioSeconds: Double
    public var seconds: Double

    public init(model: String, words: [Word], audioSeconds: Double, seconds: Double) {
        self.model = model
        self.words = words
        self.audioSeconds = audioSeconds
        self.seconds = seconds
    }
}

// MARK: - vad.detect, enhance.run (jobs, gpu)

public struct VADParams: Codable, Sendable {
    public var model: String
    public var modelPath: String?
    public var audio: String
    public var threshold: Double?
    public var minSpeech: Double?
    public var minSilence: Double?
    public var pad: Double?
}

public struct Region: Codable, Sendable, Equatable {
    public var start: Double
    public var end: Double
    public var prob: Double?

    public init(start: Double, end: Double, prob: Double? = nil) {
        self.start = start
        self.end = end
        self.prob = prob
    }
}

public struct VADResult: Codable, Sendable {
    public var model: String
    public var regions: [Region]
    public var audioSeconds: Double
    public var seconds: Double

    public init(model: String, regions: [Region], audioSeconds: Double, seconds: Double) {
        self.model = model
        self.regions = regions
        self.audioSeconds = audioSeconds
        self.seconds = seconds
    }
}

public struct EnhanceParams: Codable, Sendable {
    public var model: String
    public var modelPath: String?
    public var audio: String
    public var output: Output
    /// 0–1 mix of enhanced over original; nil = 1.
    public var strength: Double?
}

public struct EnhanceResult: Codable, Sendable {
    public var output: String
    public var sampleRate: Int
    public var channels: Int
    public var frames: Int64
    public var audioSeconds: Double
    public var model: String
    public var seconds: Double

    public init(output: String, sampleRate: Int, channels: Int, frames: Int64, audioSeconds: Double, model: String, seconds: Double) {
        self.output = output
        self.sampleRate = sampleRate
        self.channels = channels
        self.frames = frames
        self.audioSeconds = audioSeconds
        self.model = model
        self.seconds = seconds
    }
}

// MARK: - media.probe (control), media.decode (job); role media

public struct ProbeParams: Codable, Sendable {
    public var input: String
}

public struct VideoInfo: Codable, Sendable {
    public var codec: String
    public var width: Int
    public var height: Int
    public var fps: Double

    public init(codec: String, width: Int, height: Int, fps: Double) {
        self.codec = codec
        self.width = width
        self.height = height
        self.fps = fps
    }
}

public struct ProbeResult: Codable, Sendable {
    public var format: String
    public var duration: Double
    public var hasAudio: Bool
    public var hasVideo: Bool
    public var audioTracks: Int?
    public var codec: String?
    public var sampleRate: Int?
    public var channels: Int?
    public var bits: Int?
    public var bitRate: Int?
    public var video: VideoInfo?

    public init(format: String, duration: Double, hasAudio: Bool, hasVideo: Bool, audioTracks: Int? = nil, codec: String? = nil,
                sampleRate: Int? = nil, channels: Int? = nil, bits: Int? = nil, bitRate: Int? = nil, video: VideoInfo? = nil) {
        self.format = format
        self.duration = duration
        self.hasAudio = hasAudio
        self.hasVideo = hasVideo
        self.audioTracks = audioTracks
        self.codec = codec
        self.sampleRate = sampleRate
        self.channels = channels
        self.bits = bits
        self.bitRate = bitRate
        self.video = video
    }
}

public struct DecodeParams: Codable, Sendable {
    public var input: String
    public var output: Output
    public var sampleRate: Int?
    public var channels: Int?
    public var start: Double?
    public var duration: Double?
    public var track: Int?
}

public struct DecodeResult: Codable, Sendable {
    public var output: String
    public var sampleRate: Int
    public var channels: Int
    public var frames: Int64
    public var audioSeconds: Double
    public var source: ProbeResult
    public var seconds: Double

    public init(output: String, sampleRate: Int, channels: Int, frames: Int64, audioSeconds: Double, source: ProbeResult, seconds: Double) {
        self.output = output
        self.sampleRate = sampleRate
        self.channels = channels
        self.frames = frames
        self.audioSeconds = audioSeconds
        self.source = source
        self.seconds = seconds
    }
}

// MARK: - capture.* (role listen)

public struct CaptureDevice: Codable, Sendable {
    public var id: String
    public var name: String
    public var channels: Int
    public var sampleRate: Int
    public var `default`: Bool?
    public var transport: String?

    public init(id: String, name: String, channels: Int, sampleRate: Int, default isDefault: Bool? = nil, transport: String? = nil) {
        self.id = id
        self.name = name
        self.channels = channels
        self.sampleRate = sampleRate
        self.default = isDefault
        self.transport = transport
    }
}

public struct DevicesResult: Codable, Sendable {
    public var devices: [CaptureDevice]
    public init(devices: [CaptureDevice]) { self.devices = devices }
}

public struct CaptureParams: Codable, Sendable {
    /// Core Audio device UID; nil = the system default input.
    public var device: String?
    /// 1-based device channels; default [1] (or [1, 2] for stereo).
    public var inputChannels: [Int]?
    public var channels: Int?
    public var sampleRate: Int?
    public var output: Output
    public var maxSeconds: Double?
    /// Voice processing (echo cancellation, noise suppression, AGC).
    public var aec: Bool?
}

public struct LevelParams: Codable, Sendable {
    public var job: String
    public var seconds: Double
    public var rms: Double
    public var peak: Double
    public var clipped: Bool?
    /// The engine turned voice processing on during this recording
    /// ("noSound" or "deviceChanges"): in every level after it did.
    public var aecAuto: Bool?
    public var aecReason: String?

    public init(job: String, seconds: Double, rms: Double, peak: Double, clipped: Bool? = nil,
                aecAuto: Bool? = nil, aecReason: String? = nil) {
        self.job = job
        self.seconds = seconds
        self.rms = rms
        self.peak = peak
        self.clipped = clipped
        self.aecAuto = aecAuto
        self.aecReason = aecReason
    }
}

public struct CaptureResult: Codable, Sendable {
    public var output: String
    public var sampleRate: Int
    public var channels: Int
    public var frames: Int64
    public var audioSeconds: Double
    public var device: String
    public var peak: Double
    public var clipped: Int?
    /// "stopped", "maxSeconds", "deviceLost" or "inputStopped".
    public var reason: String
    /// Voice processing was on at the end; the engine turned it on, and why.
    public var aec: Bool?
    public var aecAuto: Bool?
    public var aecReason: String?

    public init(output: String, sampleRate: Int, channels: Int, frames: Int64, audioSeconds: Double,
                device: String, peak: Double, clipped: Int? = nil, reason: String,
                aec: Bool? = nil, aecAuto: Bool? = nil, aecReason: String? = nil) {
        self.aec = aec
        self.aecAuto = aecAuto
        self.aecReason = aecReason
        self.output = output
        self.sampleRate = sampleRate
        self.channels = channels
        self.frames = frames
        self.audioSeconds = audioSeconds
        self.device = device
        self.peak = peak
        self.clipped = clipped
        self.reason = reason
    }
}

public struct CaptureStopParams: Codable, Sendable {
    public var job: String
}

public struct CaptureStopResult: Codable, Sendable {
    public var stopped: Bool
    public init(stopped: Bool) { self.stopped = stopped }
}

// MARK: - media.encode (job, media; M3)

public struct EncodeChapter: Codable, Sendable {
    public var title: String
    public var start: Double
    public var end: Double
}

public struct EncodeMetadata: Codable, Sendable {
    public var title: String?
    public var author: String?
    public var narrator: String?
    public var year: String?
    public var genre: String?
    public var description: String?
}

public struct EncodeParams: Codable, Sendable {
    public var input: String
    public var output: Output
    public var codec: String?
    public var bitrate: Int?
    public var sampleRate: Int?
    public var chapters: [EncodeChapter]?
    public var metadata: EncodeMetadata
    public var cover: String?
}

public struct EncodeResult: Codable, Sendable {
    public var output: String
    public var format: String
    public var bytes: Int64
    public var duration: Double
    public var bitrate: Int?

    public init(output: String, format: String, bytes: Int64, duration: Double, bitrate: Int?) {
        self.output = output
        self.format = format
        self.bytes = bytes
        self.duration = duration
        self.bitrate = bitrate
    }
}
