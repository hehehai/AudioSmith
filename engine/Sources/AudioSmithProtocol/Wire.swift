import Foundation

// Protocol v2 between AudioSmith (Go) and audiosmith-engine: the Swift
// mirror of internal/engine/proto. The spec is docs/protocol-v2.md; the
// golden transcripts in engine/Fixtures are decoded by both sides
// (Tests/AudioSmithEngineTests/FixtureTests.swift here), so a field added
// on one side and not the other fails a test.
//
// One JSON object per line. Requests {id, method, params} go app → engine;
// responses {id, result | error} and notifications {method, params} go
// engine → app. Ids and error codes are strings. Unknown members are
// ignored, missing optional members take their defaults.

public let protocolVersion = 2

/// Longest line either side accepts, in bytes.
public let maxLine = 4 << 20

public enum Role: String, Codable, Sendable {
    case gpu, listen, media
}

public enum Method {
    public static let hello = "engine.hello"
    public static let stats = "engine.stats"
    public static let flush = "engine.flush"
    public static let shutdown = "engine.shutdown"
    public static let modelsLoad = "models.load"
    public static let modelsUnload = "models.unload"
    public static let modelsLoaded = "models.loaded"
    public static let ttsSynthesize = "tts.synthesize"
    public static let jobCancel = "job.cancel"
    // M1 (docs/protocol-v2.md §5.2); the role serving each is in role(of:).
    public static let ttsEncodeVoice = "tts.encodeVoice"
    public static let sttTranscribe = "stt.transcribe"
    public static let sttAlign = "stt.align"
    public static let vadDetect = "vad.detect"
    public static let enhanceRun = "enhance.run"
    public static let mediaProbe = "media.probe"
    public static let mediaDecode = "media.decode"
    public static let mediaEncode = "media.encode"
    // M4 (docs/protocol-v2.md §5.3).
    public static let separateRun = "separate.run"
    public static let diarizeRun = "diarize.run"
    public static let mediaMux = "media.mux"
    public static let mediaThumbnail = "media.thumbnail"
    public static let mediaTranscode = "media.transcode"
    public static let llmGenerate = "llm.generate"
    public static let watermarkEmbed = "watermark.embed"
    public static let watermarkDetect = "watermark.detect"
    public static let captureDevices = "capture.devices"
    public static let captureStart = "capture.start"
    public static let captureStop = "capture.stop"
    // Notifications, engine → app.
    public static let jobProgress = "job.progress"
    public static let ttsChunk = "tts.chunk"
    public static let log = "log"
    public static let captureLevel = "capture.level"
    public static let llmToken = "llm.token"
    // Code review 1102 M1: the system's memory pressure changed (MemoryParams).
    public static let engineMemory = "engine.memory"
    // Code review 1103 F4/F5: the app's memory budget and holds (EngineConfigParams).
    public static let engineConfig = "engine.config"

    /// Long-running, cancellable requests that count against concurrency.
    public static func isJob(_ method: String) -> Bool {
        switch method {
        case modelsLoad, ttsSynthesize, ttsEncodeVoice, sttTranscribe, sttAlign, vadDetect, enhanceRun,
             mediaDecode, mediaEncode, captureStart, separateRun, diarizeRun, mediaMux, mediaThumbnail, mediaTranscode,
             llmGenerate, watermarkEmbed, watermarkDetect, ttsDialogue:
            return true
        default:
            return false
        }
    }

    /// The role whose worker serves method (engine.*, job.cancel: every role).
    public static func role(of method: String) -> Role {
        switch method {
        case mediaProbe, mediaDecode, mediaEncode, mediaMux, mediaThumbnail, mediaTranscode, watermarkEmbed, watermarkDetect: return .media
        case captureDevices, captureStart, captureStop: return .listen
        default: return .gpu
        }
    }
}

public enum Code {
    public static let badRequest = "bad_request"
    public static let methodNotFound = "method_not_found"
    public static let notInitialized = "not_initialized"
    public static let protocolMismatch = "protocol_mismatch"
    public static let unsupported = "unsupported"
    public static let modelNotDownloaded = "model_not_downloaded"
    public static let modelLoadFailed = "model_load_failed"
    public static let licenceRequired = "licence_required"
    public static let oom = "oom"
    public static let busy = "busy"
    public static let cancelled = "cancelled"
    public static let `internal` = "internal"
    // M1.
    public static let inputTooLong = "input_too_long"
    public static let fileNotFound = "file_not_found"
    public static let unsupportedMedia = "unsupported_media"
    public static let permissionDenied = "permission_denied"
    public static let deviceUnavailable = "device_unavailable"
    public static let contextTooLong = "context_too_long"
    // Thread #153: a recording brought no sound (data: device, name, reason).
    public static let noInput = "no_input"
    // ActNow family spec §7.1: several snapshots of the model are in the
    // cache and the job named none (snapshotChoice).
    public static let modelPathRequired = "model_path_required"

    public static let all: Set<String> = [
        badRequest, methodNotFound, notInitialized, protocolMismatch, unsupported, modelNotDownloaded,
        modelLoadFailed, licenceRequired, oom, busy, cancelled, `internal`,
        inputTooLong, fileNotFound, unsupportedMedia, permissionDenied, deviceUnavailable,
        contextTooLong, modelPathRequired, noInput,
    ]
}

// MARK: - Arbitrary JSON (error data, unknown params)

/// Any JSON value; integers stay integers.
public enum JSONValue: Codable, Equatable, Sendable {
    case null
    case bool(Bool)
    case int(Int64)
    case double(Double)
    case string(String)
    case array([JSONValue])
    case object([String: JSONValue])

    public init(from decoder: Decoder) throws {
        let c = try decoder.singleValueContainer()
        if c.decodeNil() {
            self = .null
        } else if let b = try? c.decode(Bool.self) {
            self = .bool(b)
        } else if let i = try? c.decode(Int64.self) {
            self = .int(i)
        } else if let d = try? c.decode(Double.self) {
            self = .double(d)
        } else if let s = try? c.decode(String.self) {
            self = .string(s)
        } else if let a = try? c.decode([JSONValue].self) {
            self = .array(a)
        } else {
            self = .object(try c.decode([String: JSONValue].self))
        }
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        switch self {
        case .null: try c.encodeNil()
        case .bool(let b): try c.encode(b)
        case .int(let i): try c.encode(i)
        case .double(let d): try c.encode(d)
        case .string(let s): try c.encode(s)
        case .array(let a): try c.encode(a)
        case .object(let o): try c.encode(o)
        }
    }
}

// MARK: - Envelope

public struct RPCError: Codable, Error, Sendable {
    public var code: String
    public var message: String
    public var data: JSONValue?

    public init(_ code: String, _ message: String, data: JSONValue? = nil) {
        self.code = code
        self.message = message
        self.data = data
    }
}

/// The text of an error for the app: the error's own words. A Swift error
/// that is not a LocalizedError has only Cocoa's generic
/// localizedDescription ("The operation couldn’t be completed. (Module.Type
/// error 0.)"), which loses its message: take errorDescription, else
/// description (CustomStringConvertible), else String(describing:); a real
/// NSError keeps its localizedDescription.
public func errorMessage(_ error: Error) -> String {
    if let e = error as? LocalizedError, let d = e.errorDescription, !d.isEmpty {
        return d
    }
    if type(of: error) is NSError.Type {
        return (error as NSError).localizedDescription
    }
    let ns = error as NSError
    if let d = ns.userInfo[NSLocalizedDescriptionKey] as? String, !d.isEmpty {
        return d
    }
    if let e = error as? CustomStringConvertible {
        let d = e.description
        if !d.isEmpty { return d }
    }
    let d = String(describing: error)
    return d.isEmpty ? ns.localizedDescription : d
}

/// A decoded line. Params and result stay raw JSON until the method is known.
public struct Incoming: Sendable {
    public var id: String?
    public var method: String?
    public var params: Data?
    public var result: Data?
    public var error: RPCError?

    public enum Kind { case request, notification, response, invalid }

    public var kind: Kind {
        let m = !(method ?? "").isEmpty, i = !(id ?? "").isEmpty
        if m && result == nil && error == nil { return i ? .request : .notification }
        if !m && i && ((result == nil) != (error == nil)) { return .response }
        return .invalid
    }

    /// Parses one line; fails on malformed JSON or a non-object.
    public static func decode(_ line: Data) throws -> Incoming {
        guard let obj = try JSONSerialization.jsonObject(with: line, options: [.fragmentsAllowed]) as? [String: Any] else {
            throw RPCError(Code.badRequest, "A message must be a JSON object")
        }
        var m = Incoming()
        m.id = obj["id"] as? String
        m.method = obj["method"] as? String
        if let p = obj["params"], !(p is NSNull) {
            m.params = try JSONSerialization.data(withJSONObject: p, options: [.fragmentsAllowed])
        }
        if let r = obj["result"], !(r is NSNull) {
            m.result = try JSONSerialization.data(withJSONObject: r, options: [.fragmentsAllowed])
        }
        if let e = obj["error"], !(e is NSNull) {
            let data = try JSONSerialization.data(withJSONObject: e, options: [.fragmentsAllowed])
            m.error = try JSONDecoder().decode(RPCError.self, from: data)
        }
        return m
    }

    /// Decodes params as T; absent params decode from {}.
    public func decodeParams<T: Decodable>(_ type: T.Type) throws -> T {
        try JSONDecoder().decode(T.self, from: params ?? Data("{}".utf8))
    }

    public func decodeResult<T: Decodable>(_ type: T.Type) throws -> T {
        try JSONDecoder().decode(T.self, from: result ?? Data("{}".utf8))
    }
}

/// An outgoing message. Exactly one of the shapes: see Incoming.Kind.
public struct Outgoing<P: Encodable>: Encodable {
    public var id: String?
    public var method: String?
    public var params: P?
    public var result: P?
    public var error: RPCError?

    public static func request(_ id: String, _ method: String, _ params: P?) -> Outgoing {
        Outgoing(id: id, method: method, params: params)
    }

    public static func notification(_ method: String, _ params: P) -> Outgoing {
        Outgoing(method: method, params: params)
    }

    public static func response(_ id: String, _ result: P) -> Outgoing {
        Outgoing(id: id, result: result)
    }
}

extension Outgoing where P == Empty {
    public static func failure(_ id: String, _ error: RPCError) -> Outgoing {
        Outgoing(id: id, error: error)
    }
}

public func makeEncoder() -> JSONEncoder {
    let e = JSONEncoder()
    e.outputFormatting = [.withoutEscapingSlashes]
    return e
}

// MARK: - Methods

/// Params or result of methods that take or return nothing.
public struct Empty: Codable, Sendable {
    public init() {}
}

public struct HelloParams: Codable, Sendable {
    public var `protocol`: Int
    public var app: String
    public var role: Role
    public var cacheDir: String?
    public var offline: Bool?
    public var logLevel: String?
}

public struct Device: Codable, Sendable {
    public var name: String
    public var memory: Int64
    public var os: String
    public var mlx: String?

    public init(name: String, memory: Int64, os: String, mlx: String?) {
        self.name = name
        self.memory = memory
        self.os = os
        self.mlx = mlx
    }
}

public struct HelloResult: Codable, Sendable {
    public var `protocol`: Int
    public var version: String
    public var role: Role
    public var pid: Int
    public var concurrency: Int
    public var methods: [String]
    public var families: [String]
    public var device: Device

    public init(protocol: Int, version: String, role: Role, pid: Int, concurrency: Int, methods: [String], families: [String], device: Device) {
        self.protocol = `protocol`
        self.version = version
        self.role = role
        self.pid = pid
        self.concurrency = concurrency
        self.methods = methods
        self.families = families
        self.device = device
    }
}

public struct Memory: Codable, Sendable {
    public var active: Int64
    public var cache: Int64
    public var peak: Int64

    public init(active: Int64, cache: Int64, peak: Int64) {
        self.active = active
        self.cache = cache
        self.peak = peak
    }
}

public struct LoadedModel: Codable, Sendable {
    public var model: String
    public var task: String
    public var sampleRate: Int?
    public var bytes: Int64?
    public var idleSeconds: Double?

    public init(model: String, task: String, sampleRate: Int?, bytes: Int64?, idleSeconds: Double?) {
        self.model = model
        self.task = task
        self.sampleRate = sampleRate
        self.bytes = bytes
        self.idleSeconds = idleSeconds
    }
}

public struct StatsResult: Codable, Sendable {
    public var memory: Memory
    public var models: [LoadedModel]
    public var jobs: [String]

    public init(memory: Memory, models: [LoadedModel], jobs: [String]) {
        self.memory = memory
        self.models = models
        self.jobs = jobs
    }
}

public struct LoadParams: Codable, Sendable {
    public var model: String
    public var path: String?
    public var task: String?
}

public struct LoadResult: Codable, Sendable {
    public var model: String
    public var task: String
    public var sampleRate: Int
    public var seconds: Double
    public var bytes: Int64?
    public var voices: [String]?
    public var languages: [String]?
    public var capabilities: [String]?

    public init(model: String, task: String, sampleRate: Int, seconds: Double, bytes: Int64? = nil,
                voices: [String]? = nil, languages: [String]? = nil, capabilities: [String]? = nil) {
        self.model = model
        self.task = task
        self.sampleRate = sampleRate
        self.seconds = seconds
        self.bytes = bytes
        self.voices = voices
        self.languages = languages
        self.capabilities = capabilities
    }
}

public struct UnloadParams: Codable, Sendable {
    public var model: String?
    /// Seconds: drop only models no job uses and unused for at least this
    /// long (the app's idle and budget unloads); nil drops them whatever.
    public var idleAtLeast: Double?
}

public struct UnloadResult: Codable, Sendable {
    public var unloaded: [String]
    public init(unloaded: [String]) { self.unloaded = unloaded }
}

public struct LoadedResult: Codable, Sendable {
    public var models: [LoadedModel]
    public init(models: [LoadedModel]) { self.models = models }
}

public struct Output: Codable, Sendable {
    public var path: String
    public var format: String?
    public var bits: Int?

    public init(path: String, format: String? = nil, bits: Int? = nil) {
        self.path = path
        self.format = format
        self.bits = bits
    }
}

public struct SynthesizeParams: Codable, Sendable {
    public var model: String
    /// Local snapshot directory of the model; nil resolves `model` in the cache.
    public var modelPath: String?
    public var text: String
    public var voice: String?
    public var language: String?
    public var instruct: String?
    public var speed: Double?
    public var temperature: Double?
    public var topP: Double?
    public var maxTokens: Int?
    public var seed: Int64?
    public var steps: Int?
    public var duration: Double?
    public var refAudio: String?
    public var refText: String?
    /// A file tts.encodeVoice wrote; wins over refAudio, which is the fallback.
    public var voicePrompt: String?
    public var guidance: Double?
    public var exaggeration: Double?
    public var output: Output
    public var stream: Bool?

    public init(model: String, text: String, output: Output) {
        self.model = model
        self.text = text
        self.output = output
    }
}

public struct SynthesizeResult: Codable, Sendable {
    public var output: String
    public var model: String
    public var sampleRate: Int
    public var channels: Int
    public var frames: Int64
    public var audioSeconds: Double
    public var seconds: Double
    public var rtf: Double
    public var seed: Int64?

    public init(output: String, model: String, sampleRate: Int, channels: Int, frames: Int64,
                audioSeconds: Double, seconds: Double, rtf: Double, seed: Int64?) {
        self.output = output
        self.model = model
        self.sampleRate = sampleRate
        self.channels = channels
        self.frames = frames
        self.audioSeconds = audioSeconds
        self.seconds = seconds
        self.rtf = rtf
        self.seed = seed
    }
}

public struct CancelParams: Codable, Sendable {
    public var job: String
}

public struct CancelResult: Codable, Sendable {
    public var cancelled: Bool
    public init(cancelled: Bool) { self.cancelled = cancelled }
}

public struct ProgressParams: Codable, Sendable {
    public var job: String
    public var stage: String
    public var fraction: Double?
    public var audioSeconds: Double?
    public var message: String?

    public init(job: String, stage: String, fraction: Double? = nil, audioSeconds: Double? = nil, message: String? = nil) {
        self.job = job
        self.stage = stage
        self.fraction = fraction
        self.audioSeconds = audioSeconds
        self.message = message
    }
}

public struct ChunkParams: Codable, Sendable {
    public var job: String
    public var seq: Int
    public var sampleRate: Int
    public var channels: Int
    public var encoding: String
    public var data: Data
    public var `final`: Bool?

    public init(job: String, seq: Int, sampleRate: Int, channels: Int, encoding: String, data: Data, final: Bool?) {
        self.job = job
        self.seq = seq
        self.sampleRate = sampleRate
        self.channels = channels
        self.encoding = encoding
        self.data = data
        self.final = `final`
    }
}

public struct LogParams: Codable, Sendable {
    public var level: String
    public var message: String
    public var job: String?

    public init(level: String, message: String, job: String? = nil) {
        self.level = level
        self.message = message
        self.job = job
    }
}

// MARK: - Typed round trips (fixture tests, and the engine's request decoding)

/// The params type of a method, or nil when unknown.
public func paramsType(_ method: String) -> (Decodable & Encodable).Type? {
    switch method {
    case Method.hello: HelloParams.self
    case Method.stats, Method.flush, Method.shutdown, Method.modelsLoaded: Empty.self
    case Method.modelsLoad: LoadParams.self
    case Method.modelsUnload: UnloadParams.self
    case Method.ttsSynthesize: SynthesizeParams.self
    case Method.jobCancel: CancelParams.self
    case Method.jobProgress: ProgressParams.self
    case Method.ttsChunk: ChunkParams.self
    case Method.log: LogParams.self
    case Method.ttsEncodeVoice: EncodeVoiceParams.self
    case Method.sttTranscribe: TranscribeParams.self
    case Method.sttAlign: AlignParams.self
    case Method.vadDetect: VADParams.self
    case Method.enhanceRun: EnhanceParams.self
    case Method.mediaProbe: ProbeParams.self
    case Method.mediaDecode: DecodeParams.self
    case Method.mediaEncode: EncodeParams.self
    case Method.separateRun: SeparateParams.self
    case Method.diarizeRun: DiarizeParams.self
    case Method.mediaMux: MuxParams.self
    case Method.mediaThumbnail: ThumbnailParams.self
    case Method.mediaTranscode: TranscodeParams.self
    case Method.captureDevices: Empty.self
    case Method.captureStart: CaptureParams.self
    case Method.captureStop: CaptureStopParams.self
    case Method.captureLevel: LevelParams.self
    case Method.llmGenerate: LLMGenerateParams.self
    case Method.llmToken: LLMTokenParams.self
    case Method.watermarkEmbed: WatermarkEmbedParams.self
    case Method.watermarkDetect: WatermarkDetectParams.self
    case Method.ttsDialogue: DialogueParams.self
    case Method.ttsTurn: TurnParams.self
    case Method.engineMemory: MemoryParams.self
    case Method.engineConfig: EngineConfigParams.self
    default: nil
    }
}

/// The result type of a method, or nil when unknown.
public func resultType(_ method: String) -> (Decodable & Encodable).Type? {
    switch method {
    case Method.hello: HelloResult.self
    case Method.stats: StatsResult.self
    case Method.flush, Method.shutdown: Empty.self
    case Method.modelsLoad: LoadResult.self
    case Method.modelsUnload: UnloadResult.self
    case Method.modelsLoaded: LoadedResult.self
    case Method.ttsSynthesize: SynthesizeResult.self
    case Method.jobCancel: CancelResult.self
    case Method.ttsEncodeVoice: EncodeVoiceResult.self
    case Method.sttTranscribe: TranscribeResult.self
    case Method.sttAlign: AlignResult.self
    case Method.vadDetect: VADResult.self
    case Method.enhanceRun: EnhanceResult.self
    case Method.mediaProbe: ProbeResult.self
    case Method.mediaDecode: DecodeResult.self
    case Method.mediaEncode: EncodeResult.self
    case Method.separateRun: SeparateResult.self
    case Method.diarizeRun: DiarizeResult.self
    case Method.mediaMux: MuxResult.self
    case Method.mediaThumbnail: ThumbnailResult.self
    case Method.mediaTranscode: TranscodeResult.self
    case Method.llmGenerate: LLMGenerateResult.self
    case Method.watermarkEmbed: WatermarkEmbedResult.self
    case Method.watermarkDetect: WatermarkDetectResult.self
    case Method.captureDevices: DevicesResult.self
    case Method.captureStart: CaptureResult.self
    case Method.captureStop: CaptureStopResult.self
    case Method.ttsDialogue: DialogueResult.self
    case Method.engineConfig: EngineConfigResult.self
    default: nil
    }
}
