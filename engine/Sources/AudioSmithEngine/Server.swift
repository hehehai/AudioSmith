import AudioSmithProtocol
import Foundation
@preconcurrency import MLX

/// Writes protocol lines to the original stdout, which it takes over at
/// startup, one whole line at a time.
final class Writer: @unchecked Sendable {
    private let handle: FileHandle
    private let lock = NSLock()
    private let encoder = makeEncoder()

    /// Duplicates stdout for the protocol, then points stdout at stderr so
    /// stray prints of the engine's libraries cannot corrupt the stream.
    init() {
        let fd = dup(STDOUT_FILENO)
        dup2(STDERR_FILENO, STDOUT_FILENO)
        // Libraries print diagnostics; unbuffered, they reach the log even
        // when the process is killed.
        setvbuf(stdout, nil, _IONBF, 0)
        handle = FileHandle(fileDescriptor: fd, closeOnDealloc: true)
    }

    func send<P: Encodable>(_ message: Outgoing<P>) {
        guard var data = try? encoder.encode(message) else {
            log("could not encode a \(message.method ?? "response") message")
            return
        }
        data.append(0x0A)
        lock.lock()
        defer { lock.unlock() }
        try? handle.write(contentsOf: data)
    }

    func result<P: Encodable>(_ id: String, _ value: P) { send(Outgoing.response(id, value)) }
    func fail(_ id: String, _ error: RPCError) { send(Outgoing<Empty>.failure(id, error)) }
    func notify<P: Encodable>(_ method: String, _ params: P) { send(Outgoing.notification(method, params)) }

    func logLine(_ level: String, _ message: String, job: String? = nil) {
        notify(Method.log, LogParams(level: level, message: message, job: job))
    }
}

/// Free-form diagnostics on stderr (the app keeps them in engine-<role>.log).
func log(_ message: String) {
    FileHandle.standardError.write(Data("[engine] \(message)\n".utf8))
}

/// Errors with a protocol code.
enum EngineError: Error, LocalizedError {
    case badRequest(String)
    case unsupported(String, capability: String)
    case notDownloaded(model: String, missing: [String])
    case pathRequired(model: String, snapshots: [String])
    case loadFailed(String)
    case inputTooLong(limit: Int, actual: Int, unit: String)
    case fileNotFound(String)
    case unsupportedMedia(String, String)
    case permissionDenied(String)
    case deviceUnavailable(String)
    /// A recording brought no sound: the device's id and name, and why
    /// (CaptureRecovery.Reason: "noSound", "deviceChanges").
    case noInput(device: String, name: String, reason: String)

    var errorDescription: String? {
        switch self {
        case .badRequest(let m), .loadFailed(let m): m
        case .unsupported(let m, _): m
        case .notDownloaded(let model, _): "\(model) is not downloaded"
        case .pathRequired(let model, let snapshots): "\(model) has \(snapshots.count) snapshots in the model folder; the job must name the one to load"
        case .inputTooLong(let limit, let actual, let unit): "The text is \(actual) \(unit); the model takes at most \(limit) per request"
        case .fileNotFound(let path): "No such file: \(path)"
        case .unsupportedMedia(let path, let why): "Cannot read \(path): \(why)"
        case .permissionDenied(let what): "\(what.capitalized) access is denied for AudioSmith"
        case .deviceUnavailable(let device): "The audio input \(device) is not available"
        case .noInput(_, let name, let reason):
            reason == "deviceChanges" ? "\(name) kept stopping as the audio devices changed: no sound was recorded"
                : "No sound from \(name)"
        }
    }

    var rpc: RPCError {
        let m = errorDescription!
        return switch self {
        case .badRequest: RPCError(Code.badRequest, m)
        case .unsupported(_, let cap): RPCError(Code.unsupported, m, data: .object(["capability": .string(cap)]))
        case .notDownloaded(let model, let missing):
            RPCError(Code.modelNotDownloaded, m,
                     data: .object(["model": .string(model), "missing": .array(missing.map { .string($0) })]))
        case .pathRequired(let model, let snapshots):
            RPCError(Code.modelPathRequired, m,
                     data: .object(["model": .string(model), "snapshots": .array(snapshots.map { .string($0) })]))
        case .loadFailed: RPCError(Code.modelLoadFailed, m)
        case .inputTooLong(let limit, let actual, let unit):
            RPCError(Code.inputTooLong, m, data: .object(["limit": .int(Int64(limit)), "actual": .int(Int64(actual)), "unit": .string(unit)]))
        case .fileNotFound(let path): RPCError(Code.fileNotFound, m, data: .object(["path": .string(path)]))
        case .unsupportedMedia(let path, _): RPCError(Code.unsupportedMedia, m, data: .object(["path": .string(path)]))
        case .permissionDenied(let what): RPCError(Code.permissionDenied, m, data: .object(["permission": .string(what)]))
        case .deviceUnavailable(let device): RPCError(Code.deviceUnavailable, m, data: .object(["device": .string(device)]))
        case .noInput(let device, let name, let reason):
            RPCError(Code.noInput, m, data: .object(["device": .string(device), "name": .string(name), "reason": .string(reason)]))
        }
    }
}

func rpcError(_ error: Error) -> RPCError {
    if let e = error as? RPCError { return e }
    if let e = error as? EngineError { return e.rpc }
    if error is CancellationError { return RPCError(Code.cancelled, "Cancelled") }
    return RPCError(Code.internal, errorMessage(error))
}

/// The session: answers requests from stdin, runs jobs by id.
final class Server: @unchecked Sendable {
    let role: Role
    let out: Writer
    let synth: Synthesizer?
    let analyzer: Analyzer?
    let media: MediaWorker?
    let recorder: Recorder?
    private let lock = NSLock()
    private var hello: HelloParams?
    private var jobs: [String: Task<Void, Never>] = [:]
    private var cancelled: Set<String> = []

    /// Jobs it runs at once; one more gets "busy". The gpu worker runs one
    /// (MLX models must not generate from two jobs at once), the listen
    /// worker one recording, the media worker two decodes.
    let concurrency: Int
    let store: ModelStore?
    /// The gpu worker's watch over memory pressure (the listen worker's
    /// one small model is the app's to keep: its Hold).
    private var memoryWatch: MemoryWatch?

    init(role: Role, out: Writer) {
        self.role = role
        self.out = out
        // The listen worker keeps one small speech-to-text model of its own
        // (dictation), beside recordings: concurrency 2.
        store = role == .gpu ? ModelStore(out: out) : role == .listen ? ModelStore(out: out, maxResident: 1) : nil
        synth = role == .gpu ? store.map { Synthesizer(out: out, store: $0) } : nil
        analyzer = store.map { Analyzer(out: out, store: $0) }
        media = role == .media ? MediaWorker(out: out) : nil
        recorder = role == .listen ? Recorder(out: out) : nil
        concurrency = role == .gpu ? 1 : 2
        if role == .gpu, let store { memoryWatch = MemoryWatch(store: store, out: out) }
    }

    var methods: [String] {
        var m = [Method.hello, Method.stats, Method.flush, Method.shutdown]
        if store != nil { m.append(Method.engineConfig) }
        switch role {
        case .gpu:
            m += [Method.modelsLoad, Method.modelsUnload, Method.modelsLoaded, Method.ttsSynthesize, Method.ttsEncodeVoice,
                  Method.sttTranscribe, Method.sttAlign, Method.vadDetect, Method.enhanceRun, Method.separateRun, Method.diarizeRun,
                  Method.llmGenerate, Method.ttsDialogue]
        case .media:
            m += [Method.mediaProbe, Method.mediaDecode, Method.mediaEncode, Method.mediaMux, Method.mediaThumbnail, Method.mediaTranscode,
                  Method.watermarkEmbed, Method.watermarkDetect]
        case .listen:
            m += [Method.captureDevices, Method.captureStart, Method.captureStop,
                  Method.modelsLoad, Method.modelsUnload, Method.modelsLoaded, Method.sttTranscribe]
        }
        return m + [Method.jobCancel]
    }

    /// Reads requests until stdin closes, then cancels the jobs and exits.
    func run() async {
        do {
            for try await line in FileHandle.standardInput.bytes.lines {
                let trimmed = line.trimmingCharacters(in: .whitespaces)
                if trimmed.isEmpty { continue }
                let msg: Incoming
                do {
                    msg = try Incoming.decode(Data(trimmed.utf8))
                } catch {
                    log("bad line: \(error.localizedDescription)")
                    continue
                }
                guard msg.kind == .request, let id = msg.id, let method = msg.method else {
                    log("ignored a message that is not a request")
                    continue
                }
                handle(id: id, method: method, msg)
            }
        } catch {
            log("stdin failed: \(error)")
        }
        // The app closed our stdin: it quit or crashed. Don't outlive it.
        cancelAll()
        exit(0)
    }

    func handle(id: String, method: String, _ msg: Incoming) {
        if method == Method.hello {
            return answerHello(id, msg)
        }
        lock.lock()
        let ready = hello != nil
        lock.unlock()
        guard ready else {
            return out.fail(id, RPCError(Code.notInitialized, "engine.hello must come first"))
        }
        guard methods.contains(method) else {
            return out.fail(id, RPCError(Code.methodNotFound, "Unknown method \(method)"))
        }
        do {
            // A model job's auxiliary repos load from the app's snapshots
            // (auxPaths, AuxLinks.swift), not from what the cache's ref names.
            if let aux = (try? msg.decodeParams(AuxParams.self))?.auxPaths, !aux.isEmpty {
                do {
                    try linkAuxRepos(aux, hub: hubCacheDirectory())
                } catch AuxLinkError.missing(let repo, let path) {
                    throw EngineError.notDownloaded(model: repo, missing: [path])
                } catch AuxLinkError.badRepo(let repo) {
                    throw EngineError.badRequest("Bad auxiliary repo \(repo)")
                }
            }
            switch method {
            case Method.stats:
                out.result(id, stats())
            case Method.flush:
                MLX.Memory.clearCache()
                out.result(id, Empty())
            case Method.shutdown:
                cancelAll()
                out.result(id, Empty())
                exit(0)
            case Method.jobCancel:
                let p = try msg.decodeParams(CancelParams.self)
                out.result(id, CancelResult(cancelled: cancel(p.job)))
            case Method.engineConfig:
                let p = try msg.decodeParams(EngineConfigParams.self)
                guard let store else { throw EngineError.badRequest("The \(role.rawValue) worker keeps no models") }
                out.result(id, store.configure(p))
            case Method.modelsLoaded:
                out.result(id, LoadedResult(models: store?.loaded() ?? []))
            case Method.modelsUnload:
                let p = try msg.decodeParams(UnloadParams.self)
                out.result(id, UnloadResult(unloaded: store?.unload(p.model, idleAtLeast: p.idleAtLeast) ?? []))
            case Method.modelsLoad:
                let p = try msg.decodeParams(LoadParams.self)
                guard !p.model.isEmpty else { throw EngineError.badRequest("Missing model") }
                let offline = self.offline
                if let analyzer, ["stt", "align", "vad", "enhance", "llm"].contains(p.task ?? "tts") {
                    if role == .listen, p.task != "stt" { throw EngineError.badRequest("The listen worker loads speech-to-text models only") }
                    startJob(id) { try await analyzer.load(p, job: id, offline: offline) }
                } else {
                    guard let synth else { throw EngineError.badRequest("The \(role.rawValue) worker loads speech-to-text models only (task \"stt\")") }
                    let aux = (try? msg.decodeParams(AuxParams.self))?.auxPaths
                    startJob(id) { try await synth.load(p, job: id, offline: offline, auxPaths: aux) }
                }
            case Method.ttsSynthesize:
                let p = try msg.decodeParams(SynthesizeParams.self)
                guard let synth else { throw RPCError(Code.methodNotFound, "Unknown method \(method)") }
                let offline = self.offline
                startJob(id) { try await synth.synthesize(p, job: id, offline: offline) }
            case Method.ttsDialogue:
                let p = try msg.decodeParams(DialogueParams.self)
                guard let synth else { throw RPCError(Code.methodNotFound, "Unknown method \(method)") }
                let offline = self.offline
                let aux = (try? msg.decodeParams(AuxParams.self))?.auxPaths
                startJob(id) { try await synth.dialogue(p, job: id, offline: offline, auxPaths: aux) }
            case Method.ttsEncodeVoice:
                let p = try msg.decodeParams(EncodeVoiceParams.self)
                guard let synth else { throw RPCError(Code.methodNotFound, "Unknown method \(method)") }
                let offline = self.offline
                startJob(id) { try await synth.encodeVoice(p, job: id, offline: offline) }
            case Method.sttTranscribe:
                let p = try msg.decodeParams(TranscribeParams.self)
                guard let analyzer else { throw RPCError(Code.methodNotFound, "Unknown method \(method)") }
                let offline = self.offline
                startJob(id) { try await analyzer.transcribe(p, job: id, offline: offline) }
            case Method.sttAlign:
                let p = try msg.decodeParams(AlignParams.self)
                guard let analyzer else { throw RPCError(Code.methodNotFound, "Unknown method \(method)") }
                let offline = self.offline
                startJob(id) { try await analyzer.align(p, job: id, offline: offline) }
            case Method.vadDetect:
                let p = try msg.decodeParams(VADParams.self)
                guard let analyzer else { throw RPCError(Code.methodNotFound, "Unknown method \(method)") }
                let offline = self.offline
                startJob(id) { try await analyzer.vad(p, job: id, offline: offline) }
            case Method.enhanceRun:
                let p = try msg.decodeParams(EnhanceParams.self)
                guard let analyzer else { throw RPCError(Code.methodNotFound, "Unknown method \(method)") }
                let offline = self.offline
                startJob(id) { try await analyzer.enhance(p, job: id, offline: offline) }
            case Method.mediaProbe:
                let p = try msg.decodeParams(ProbeParams.self)
                guard let media else { throw RPCError(Code.methodNotFound, "Unknown method \(method)") }
                let out = self.out
                // A control request, answered from its own task.
                Task {
                    do { out.result(id, try await media.probe(p)) } catch { out.fail(id, rpcError(error)) }
                }
            case Method.mediaDecode:
                let p = try msg.decodeParams(DecodeParams.self)
                guard let media else { throw RPCError(Code.methodNotFound, "Unknown method \(method)") }
                startJob(id) { try await media.decode(p, job: id) }
            case Method.separateRun:
                let p = try msg.decodeParams(SeparateParams.self)
                guard let analyzer, role == .gpu else { throw RPCError(Code.methodNotFound, "Unknown method \(method)") }
                let offline = self.offline
                startJob(id) { try await analyzer.separate(p, job: id, offline: offline) }
            case Method.diarizeRun:
                let p = try msg.decodeParams(DiarizeParams.self)
                guard let analyzer, role == .gpu else { throw RPCError(Code.methodNotFound, "Unknown method \(method)") }
                let offline = self.offline
                startJob(id) { try await analyzer.diarize(p, job: id, offline: offline) }
            case Method.llmGenerate:
                let p = try msg.decodeParams(LLMGenerateParams.self)
                guard let analyzer, role == .gpu else { throw RPCError(Code.methodNotFound, "Unknown method \(method)") }
                let offline = self.offline
                startJob(id) { try await analyzer.generate(p, job: id, offline: offline) }
            case Method.watermarkEmbed:
                let p = try msg.decodeParams(WatermarkEmbedParams.self)
                guard let media else { throw RPCError(Code.methodNotFound, "Unknown method \(method)") }
                startJob(id) { try await media.watermarkEmbed(p, job: id) }
            case Method.watermarkDetect:
                let p = try msg.decodeParams(WatermarkDetectParams.self)
                guard let media else { throw RPCError(Code.methodNotFound, "Unknown method \(method)") }
                startJob(id) { try await media.watermarkDetect(p, job: id) }
            case Method.mediaMux:
                let p = try msg.decodeParams(MuxParams.self)
                guard let media else { throw RPCError(Code.methodNotFound, "Unknown method \(method)") }
                startJob(id) { try await media.mux(p, job: id) }
            case Method.mediaThumbnail:
                let p = try msg.decodeParams(ThumbnailParams.self)
                guard let media else { throw RPCError(Code.methodNotFound, "Unknown method \(method)") }
                startJob(id) { try await media.thumbnail(p, job: id) }
            case Method.mediaTranscode:
                let p = try msg.decodeParams(TranscodeParams.self)
                guard let media else { throw RPCError(Code.methodNotFound, "Unknown method \(method)") }
                startJob(id) { try await media.transcode(p, job: id) }
            case Method.mediaEncode:
                let p = try msg.decodeParams(EncodeParams.self)
                guard let media else { throw RPCError(Code.methodNotFound, "Unknown method \(method)") }
                startJob(id) { try await media.encode(p, job: id) }
            case Method.captureDevices:
                out.result(id, DevicesResult(devices: captureDevices()))
            case Method.captureStart:
                let p = try msg.decodeParams(CaptureParams.self)
                guard let recorder else { throw RPCError(Code.methodNotFound, "Unknown method \(method)") }
                startJob(id) { try await recorder.record(p, job: id) }
            case Method.captureStop:
                let p = try msg.decodeParams(CaptureStopParams.self)
                guard let recorder else { throw RPCError(Code.methodNotFound, "Unknown method \(method)") }
                lock.lock()
                let task = jobs[p.job]
                lock.unlock()
                guard let task, recorder.stop(p.job) else {
                    out.result(id, CaptureStopResult(stopped: false))
                    return
                }
                // The recording's job answers first, with the file.
                let out = self.out
                Task {
                    await task.value
                    out.result(id, CaptureStopResult(stopped: true))
                }
            default:
                out.fail(id, RPCError(Code.methodNotFound, "Unknown method \(method)"))
            }
        } catch let error as DecodingError {
            out.fail(id, RPCError(Code.badRequest, "Bad params for \(method): \(describe(error))"))
        } catch {
            out.fail(id, rpcError(error))
        }
    }

    private var offline: Bool {
        lock.lock()
        defer { lock.unlock() }
        return hello?.offline ?? false
    }

    private func answerHello(_ id: String, _ msg: Incoming) {
        let p: HelloParams
        do {
            p = try msg.decodeParams(HelloParams.self)
        } catch {
            // A peer of another protocol may not even match the shape.
            return out.fail(id, RPCError(Code.protocolMismatch, "This engine speaks protocol \(protocolVersion)",
                                         data: .object(["supported": .array([.int(Int64(protocolVersion))])])))
        }
        guard p.protocol == protocolVersion else {
            return out.fail(id, RPCError(Code.protocolMismatch, "This engine speaks protocol \(protocolVersion)",
                                         data: .object(["supported": .array([.int(Int64(protocolVersion))])])))
        }
        if let dir = p.cacheDir, !dir.isEmpty {
            // swift-huggingface resolves its cache from the environment on first use.
            setenv("HF_HUB_CACHE", dir, 1)
        }
        lock.lock()
        hello = p
        lock.unlock()
        if p.role != role {
            log("hello asks for role \(p.role.rawValue); this process runs as \(role.rawValue)")
        }
        out.result(id, HelloResult(
            protocol: protocolVersion, version: engineVersion, role: role, pid: Int(getpid()),
            concurrency: concurrency, methods: methods,
            families: role == .gpu ? Synthesizer.families + Synthesizer.dialogueFamilies + Analyzer.families + Analyzer.m4Families + Analyzer.llmFamilies : role == .listen ? Analyzer.listenFamilies : MediaWorker.watermarkFamilies,
            device: deviceInfo()))
    }

    private func stats() -> StatsResult {
        lock.lock()
        let running = jobs.keys.sorted()
        lock.unlock()
        return StatsResult(
            memory: AudioSmithProtocol.Memory(
                active: Int64(MLX.Memory.activeMemory), cache: Int64(MLX.Memory.cacheMemory), peak: Int64(MLX.Memory.peakMemory)),
            models: store?.loaded() ?? [], jobs: running)
    }

    /// Runs work as job id: its response is written when it ends, after
    /// every notification it sent.
    private func startJob<R: Encodable>(_ id: String, _ work: @escaping @Sendable () async throws -> R) {
        lock.lock()
        defer { lock.unlock() }
        if jobs[id] != nil {
            out.fail(id, RPCError(Code.badRequest, "Job \(id) is already running"))
            return
        }
        if jobs.count >= concurrency {
            out.fail(id, RPCError(Code.busy, "The engine runs \(concurrency) job(s) at a time"))
            return
        }
        let out = self.out
        jobs[id] = Task { [weak self] in
            let started = Date()
            do {
                let result = try await work()
                self?.finish(id)
                out.result(id, result)
            } catch {
                let cancelled = Task.isCancelled || error is CancellationError || (self?.wasCancelled(id) ?? false)
                self?.finish(id)
                if cancelled {
                    out.fail(id, RPCError(Code.cancelled, "Cancelled"))
                } else {
                    let e = rpcError(error)
                    log("job \(id) failed after \(String(format: "%.1f", Date().timeIntervalSince(started))) s: \(e.code): \(e.message)")
                    out.fail(id, e)
                }
            }
        }
    }

    private func wasCancelled(_ id: String) -> Bool {
        lock.lock()
        defer { lock.unlock() }
        return cancelled.contains(id)
    }

    /// Forgets job id. Called before its response is written, so a
    /// job.cancel that arrives after it is answered false.
    private func finish(_ id: String) {
        lock.lock()
        jobs[id] = nil
        cancelled.remove(id)
        lock.unlock()
    }

    private func cancel(_ id: String) -> Bool {
        lock.lock()
        defer { lock.unlock() }
        guard let task = jobs[id] else { return false }
        cancelled.insert(id)
        task.cancel()
        return true
    }

    private func cancelAll() {
        lock.lock()
        let tasks = Array(jobs.values)
        cancelled.formUnion(jobs.keys)
        lock.unlock()
        tasks.forEach { $0.cancel() }
    }
}

func describe(_ error: DecodingError) -> String {
    switch error {
    case .keyNotFound(let key, _): "missing \(key.stringValue)"
    case .typeMismatch(_, let ctx), .valueNotFound(_, let ctx), .dataCorrupted(let ctx):
        "\(ctx.codingPath.map(\.stringValue).joined(separator: ".")): \(ctx.debugDescription)"
    @unknown default: "\(error)"
    }
}

func deviceInfo() -> AudioSmithProtocol.Device {
    var size = 0
    sysctlbyname("machdep.cpu.brand_string", nil, &size, nil, 0)
    var name = [CChar](repeating: 0, count: max(size, 1))
    sysctlbyname("machdep.cpu.brand_string", &name, &size, nil, 0)
    let v = ProcessInfo.processInfo.operatingSystemVersion
    return AudioSmithProtocol.Device(
        name: String(cString: name), memory: Int64(ProcessInfo.processInfo.physicalMemory),
        os: "macOS \(v.majorVersion).\(v.minorVersion).\(v.patchVersion)", mlx: mlxVersion)
}
