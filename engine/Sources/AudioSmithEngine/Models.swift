import AudioSmithProtocol
import AudioSmithStream
import Foundation
@preconcurrency import MLX

/// The models a gpu worker keeps resident, of every task (TTS, STT,
/// alignment, VAD, enhancement). Memory decides what stays: a load first
/// drops idle models to fit the budget (makeRoom), and the app's supervisor
/// unloads idle ones to stay within its memory budget. A cap on their
/// number (AUDIOSMITH_MAX_MODELS, default 6) is a safety net that small
/// models (under smallModelBytes: aligners, VAD) do not count against: a
/// story render turns between TTS, the aligner, the recognizer and the
/// dialogue model at every segment, and a cap of 3 reloaded one each time
/// (review #102). The system's memory pressure (MemoryWatch.swift) unloads
/// idle models at "warning" and refuses loads at "critical" (code review
/// 1102 M1). The app tells it its memory budget and the models it holds
/// (engine.config, code review 1103 F4/F5): makeRoom keeps to that budget,
/// and a "warning" spares held models. Jobs are serialized
/// by the Server (gpu concurrency 1): MLX models must not run from two jobs
/// at once.
final class ModelStore: @unchecked Sendable {
    struct Resident {
        var model: Any
        var task: String
        var sampleRate: Int
        var bytes: Int64
        var lastUsed: Date
        var inUse: Int
    }

    private let out: Writer
    private let lock = NSLock()
    private var resident: [String: Resident] = [:]
    private let maxResident: Int
    /// The level of the last memory-pressure event (MemoryWatch).
    private var pressureEvent = MemoryPressure.normal
    /// The app's memory budget (engine.config); 0 = the default.
    private var appBudget: Int64 = 0
    /// Models the app holds (engine.config): every model when holdAll.
    private var held: Set<String> = []
    private var holdAll = false

    /// maxResident overrides AUDIOSMITH_MAX_MODELS (the listen worker keeps one).
    init(out: Writer, maxResident: Int? = nil) {
        self.out = out
        self.maxResident = maxResident ?? Int(ProcessInfo.processInfo.environment["AUDIOSMITH_MAX_MODELS"] ?? "") ?? 6
        // Keep MLX's buffer cache small: this process lives as long as the
        // app, and an uncapped cache grows to gigabytes (spike: OmniVoice 6–8 GB → 4.9 GB).
        MLX.Memory.cacheLimit = 256 * 1024 * 1024
    }

    func loaded() -> [LoadedModel] {
        lock.lock()
        defer { lock.unlock() }
        let now = Date()
        return resident.sorted { $0.value.lastUsed > $1.value.lastUsed }.map { id, r in
            LoadedModel(model: id, task: r.task, sampleRate: r.sampleRate, bytes: r.bytes,
                        idleSeconds: r.inUse > 0 ? 0 : max(now.timeIntervalSince(r.lastUsed), 0.001))
        }
    }

    /// Drops model (all when nil) and returns what was dropped. A model a
    /// job is using stays alive until the job ends. With idleAtLeast
    /// (seconds) only models no job uses and unused that long are dropped:
    /// the app's supervisor decides from engine.stats, and a job may have
    /// taken the model since. MLX's buffer cache is cleared after, so the
    /// weights' memory goes back to the system.
    func unload(_ model: String?, idleAtLeast: Double? = nil) -> [String] {
        lock.lock()
        let now = Date()
        let ids = resident.filter { id, r in
            guard model == nil || model == "" || id == model else { return false }
            guard let idle = idleAtLeast, idle > 0 else { return true }
            return r.inUse == 0 && now.timeIntervalSince(r.lastUsed) >= idle
        }.keys.sorted()
        for id in ids { resident[id] = nil }
        lock.unlock()
        if !ids.isEmpty { MLX.Memory.clearCache() }
        return ids
    }

    /// Returns model as T, loading it with load (stage "loading") when it
    /// is not resident. Call release(model) when the job is done with it.
    /// estimate (bytes the load adds: its weights on disk, weightBytes)
    /// first drops idle models, least recently used first, until it fits
    /// in the memory budget: a model evicts others when memory is tight,
    /// without waiting for the app's supervisor. Every load passes one
    /// (code review 1102 M1: only the dialogue models and the LLM did).
    /// Under critical memory pressure it loads nothing: a retryable oom.
    func acquire<T>(
        _ model: String, task: String, job: String, as type: T.Type,
        sampleRate: (T) -> Int, estimate: @autoclosure () -> Int64, load: () async throws -> T
    ) async throws -> (T, Resident) {
        if let r = take(model) {
            guard let m = r.model as? T else {
                release(model)
                throw EngineError.badRequest("\(model) is loaded as a \(r.task) model, not for \(task)")
            }
            return (m, r)
        }
        let level = pressure()
        if level.refusesLoads {
            out.logLine("warning", "Did not load \(model): the system is critically low on memory", job: job)
            throw RPCError(Code.oom, "The Mac is critically low on memory: \(shortName(model)) was not loaded",
                           data: .object(["reason": .string("memory_pressure"), "level": .string(level.rawValue), "model": .string(model)]))
        }
        out.notify(Method.jobProgress, ProgressParams(job: job, stage: "loading", message: "Loading \(shortName(model))"))
        // Only now: a resident model needs no estimate (code review 1103 F10).
        let bytes = estimate()
        if bytes > 0 { makeRoom(for: bytes) }
        let started = Date()
        let before = MLX.Memory.activeMemory
        let loaded: T
        do {
            loaded = try await load()
        } catch let error as EngineError {
            throw error
        } catch let error as RPCError {
            throw error
        } catch {
            if Task.isCancelled { throw CancellationError() }
            throw EngineError.loadFailed("\(shortName(model)): \(errorMessage(error))")
        }
        try Task.checkCancellation()
        let r = Resident(model: loaded, task: task, sampleRate: sampleRate(loaded),
                         bytes: Int64(max(MLX.Memory.activeMemory - before, 0)), lastUsed: Date(), inUse: 1)
        insert(model, r)
        out.logLine("info", String(format: "Loaded %@ in %.1f s", model, Date().timeIntervalSince(started)), job: job)
        return (loaded, r)
    }

    /// The bytes resident models may take when the app has not said
    /// (AUDIOSMITH_MEMORY_BUDGET, else 60 % of the Mac's memory, as the
    /// app's supervisor assumes).
    static var defaultBudget: Int64 {
        if let b = Int64(ProcessInfo.processInfo.environment["AUDIOSMITH_MEMORY_BUDGET"] ?? ""), b > 0 { return b }
        return Int64(ProcessInfo.processInfo.physicalMemory) * 6 / 10
    }

    /// The bytes resident models may take: the app's budget (its memory
    /// limit setting, engine.config), else defaultBudget.
    var memoryBudget: Int64 {
        let b = lock.withLock { appBudget }
        return b > 0 ? b : Self.defaultBudget
    }

    /// engine.config: the app's budget (0 = the default) and the models it
    /// holds now (nil leaves either as it was). Returns what applies.
    func configure(_ p: EngineConfigParams) -> EngineConfigResult {
        lock.withLock {
            if let b = p.memoryBudget { appBudget = max(b, 0) }
            if let h = p.held { held = Set(h) }
            if let all = p.holdAll { holdAll = all }
        }
        let (h, all) = lock.withLock { (held.sorted(), holdAll) }
        return EngineConfigResult(memoryBudget: memoryBudget, held: h, holdAll: all ? true : nil)
    }

    /// Whether the app holds model. Caller holds lock.
    private func isHeld(_ model: String) -> Bool {
        holdAll || held.contains(model)
    }

    /// Drops idle models, least recently used first, until bytes more fit
    /// in the memory budget (or nothing idle is left). Models the app holds
    /// go last, only when the others are not enough.
    func makeRoom(for bytes: Int64) {
        let budget = memoryBudget
        var dropped: [String] = []
        while Int64(MLX.Memory.activeMemory) + bytes > budget {
            let victim: String? = lock.withLock {
                let idle = resident.filter { $0.value.inUse == 0 }
                let pick = idle.filter { !isHeld($0.key) }.isEmpty ? idle : idle.filter { !isHeld($0.key) }
                guard let lru = pick.min(by: { $0.value.lastUsed < $1.value.lastUsed })?.key else { return nil }
                resident[lru] = nil
                return lru
            }
            guard let victim else { break }
            dropped.append(victim)
            MLX.Memory.clearCache()
        }
        if !dropped.isEmpty {
            out.logLine("info", "Unloaded \(dropped.joined(separator: ", ")) to make room for a \(bytes >> 20) MB model", job: nil)
        }
    }

    /// The memory pressure a load goes by (MemoryPressure.effective).
    func pressure() -> MemoryPressure {
        let event = lock.withLock { pressureEvent }
        return MemoryPressure.effective(event: event, kernel: MemoryPressure.current(),
                                        forced: ProcessInfo.processInfo.environment["AUDIOSMITH_MEMORY_PRESSURE"])
    }

    /// A memory-pressure event: from "warning" up, every model no job
    /// uses is dropped and MLX's buffer cache cleared; at "warning" the
    /// models the app holds stay (a live call's, a dictation's: code
    /// review 1103 F5). Returns the dropped models.
    func relieve(_ level: MemoryPressure) -> [String] {
        lock.lock()
        pressureEvent = level
        guard level.unloadsIdle else {
            lock.unlock()
            return []
        }
        let ids = resident.filter { $0.value.inUse == 0 && (level == .critical || !isHeld($0.key)) }.keys.sorted()
        for id in ids { resident[id] = nil }
        lock.unlock()
        MLX.Memory.clearCache()
        return ids
    }

    private func take(_ model: String) -> Resident? {
        lock.withLock {
            guard var r = resident[model] else { return nil }
            r.inUse += 1
            r.lastUsed = Date()
            resident[model] = r
            return r
        }
    }

    /// Models smaller than this do not count against maxResident (except
    /// for a worker that keeps one).
    static let smallModelBytes: Int64 = 512 << 20

    private func insert(_ model: String, _ r: Resident) {
        lock.withLock {
            resident[model] = r
            let counted = { (r: Resident) in self.maxResident <= 1 || r.bytes >= Self.smallModelBytes }
            while resident.values.filter(counted).count > maxResident,
                  let lru = resident.filter({ $0.value.inUse == 0 && counted($0.value) }).min(by: { $0.value.lastUsed < $1.value.lastUsed })?.key {
                resident[lru] = nil
                log("dropped \(lru): more than \(maxResident) models resident")
            }
        }
    }

    func release(_ model: String) {
        lock.lock()
        if var r = resident[model] {
            r.inUse = max(r.inUse - 1, 0)
            r.lastUsed = Date()
            resident[model] = r
        }
        lock.unlock()
    }
}

/// The bytes of a model's weights on disk (*.safetensors, in its folder
/// and the folders under it, two deep: DeepFilterNet's v3/, Qwen3-TTS's
/// speech_tokenizer/; Kokoro's voices/ add some 27 MB): what loading it
/// adds, about. 0 without a folder. A folder's size is kept: snapshots do
/// not change (code review 1103 F10).
func weightBytes(_ dir: URL?) -> Int64 {
    guard let dir else { return 0 }
    let key = dir.standardizedFileURL.path
    if let known = weightBytesCache.withLock({ $0[key] }) { return known }
    let total = walkWeightBytes(dir)
    if total > 0 { weightBytesCache.withLock { $0[key] = total } }
    return total
}

private let weightBytesCache = LockedValue<[String: Int64]>([:])

/// A value behind a lock.
final class LockedValue<V>: @unchecked Sendable {
    private let lock = NSLock()
    private var value: V
    init(_ value: V) { self.value = value }
    func withLock<R>(_ body: (inout V) -> R) -> R {
        lock.lock()
        defer { lock.unlock() }
        return body(&value)
    }
}

private func walkWeightBytes(_ dir: URL) -> Int64 {
    let fm = FileManager.default
    func walk(_ dir: URL, depth: Int) -> Int64 {
        guard let names = try? fm.contentsOfDirectory(atPath: dir.path) else { return 0 }
        var total: Int64 = 0
        for n in names where !n.hasPrefix(".") {
            // Snapshots hold symlinks to blobs: follow them.
            let url = dir.appendingPathComponent(n)
            let path = url.resolvingSymlinksInPath().path
            if n.hasSuffix(".safetensors") {
                total += ((try? fm.attributesOfItem(atPath: path)[.size]) as? NSNumber)?.int64Value ?? 0
            } else if depth > 0, (try? fm.attributesOfItem(atPath: path)[.type]) as? FileAttributeType == .typeDirectory {
                total += walk(url, depth: depth - 1)
            }
        }
        return total
    }
    return walk(dir, depth: 2)
}

/// The folder a model loads from: path when set, else its one snapshot in
/// the cache (nil when there is none, or several).
func weightsDirectory(_ model: String, path: String?) -> URL? {
    if let path, !path.isEmpty { return URL(fileURLWithPath: path) }
    return try? modelDirectory(model, path: nil, offline: false)
}

func shortName(_ model: String) -> String {
    model.split(separator: "/").last.map(String.init) ?? model
}

/// What is missing from the HF cache for model, or nil when a complete
/// copy is there (the layouts mlx-audio-swift resolves offline).
func missingFromCache(_ model: String) -> [String]? {
    let fm = FileManager.default
    let hub = hubCacheDirectory()
    let flat = hub.appendingPathComponent("mlx-audio").appendingPathComponent(model.replacingOccurrences(of: "/", with: "_"))
    if fm.fileExists(atPath: flat.appendingPathComponent("config.json").path) {
        return nil
    }
    let snapshots = hub.appendingPathComponent("models--" + model.replacingOccurrences(of: "/", with: "--"))
        .appendingPathComponent("snapshots")
    if let shas = try? fm.contentsOfDirectory(atPath: snapshots.path),
       shas.contains(where: { !((try? fm.contentsOfDirectory(atPath: snapshots.appendingPathComponent($0).path)) ?? []).isEmpty }) {
        return nil
    }
    return ["config.json"]
}

/// mlx-audio-swift's own folder for a repo: <hub>/mlx-audio/<owner>_<name>.
func engineCopyDirectory(_ model: String) -> URL {
    hubCacheDirectory().appendingPathComponent("mlx-audio").appendingPathComponent(model.replacingOccurrences(of: "/", with: "_"))
}

/// The model_type of a snapshot's config.json, nil when unreadable.
func modelType(_ dir: URL) -> String? {
    guard let data = try? Data(contentsOf: dir.appendingPathComponent("config.json")),
          let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return nil }
    return json["model_type"] as? String
}

/// The engine's folder for a model whose loader writes beside the weights
/// (Qwen3-ASR and its forced aligner add a tokenizer.json): symbolic links
/// to the snapshot's files, so the app's snapshot stays as downloaded and
/// nothing is copied. Loading these by repo id made mlx-audio-swift copy
/// the whole model there, doubling its disk use; a copy left from that is
/// replaced by links here.
func linkedModelDirectory(_ model: String, snapshot: URL) throws -> URL {
    let dir = engineCopyDirectory(model)
    try linkSnapshot(snapshot, into: dir)
    return dir
}

/// Auxiliary repos mlx-audio-swift resolves by id without a config.json
/// (Kokoro's English G2P). Its resolver accepts a cached copy only when
/// config.json is there, so without one it listed the repo on
/// huggingface.co at every Kokoro load (and copied it again). An empty
/// config.json beside a complete copy makes it use the copy, offline.
let configlessAuxRepos = ["beshkenadze/kitten-tts-g2p"]

func markAuxCopies() {
    let fm = FileManager.default
    for repo in configlessAuxRepos {
        let dir = engineCopyDirectory(repo)
        let config = dir.appendingPathComponent("config.json")
        guard !fm.fileExists(atPath: config.path),
              let names = try? fm.contentsOfDirectory(atPath: dir.path),
              names.contains(where: { $0.hasSuffix(".safetensors") }) else { continue }
        try? Data("{}".utf8).write(to: config)
    }
}

/// Removes mlx-audio-swift's full copy of a model that has just loaded
/// from the app's snapshot (older engines and the MVP loaded by repo id,
/// which copied the model): the copy is never read again. Folders of
/// links (linkedModelDirectory) are kept.
func dropEngineCopy(_ model: String) {
    let fm = FileManager.default
    let dir = engineCopyDirectory(model)
    guard let names = try? fm.contentsOfDirectory(atPath: dir.path) else { return }
    let copied = names.contains { name in
        let path = dir.appendingPathComponent(name).path
        guard name.hasSuffix(".safetensors"), (try? fm.destinationOfSymbolicLink(atPath: path)) == nil else { return false }
        return true
    }
    if copied {
        try? fm.removeItem(at: dir)
        log("\(model): removed the engine's old copy (it loads from the app's snapshot)")
    }
}

/// The Hugging Face hub cache (HF_HUB_CACHE, set from hello.cacheDir).
func hubCacheDirectory() -> URL {
    let env = ProcessInfo.processInfo.environment
    return env["HF_HUB_CACHE"].map { URL(fileURLWithPath: $0) }
        ?? env["HF_HOME"].map { URL(fileURLWithPath: $0).appendingPathComponent("hub") }
        ?? FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent(".cache/huggingface/hub")
}

/// Resolves the local directory of a model job: its modelPath when set,
/// else the snapshot of model in the HF cache when there is exactly one
/// (with several, which may be other apps' commits, the job must name
/// one: model_path_required); offline and missing fails with
/// model_not_downloaded.
func modelDirectory(_ model: String, path: String?, offline: Bool) throws -> URL? {
    if let path, !path.isEmpty { return URL(fileURLWithPath: path) }
    if offline, let missing = missingFromCache(model) {
        throw EngineError.notDownloaded(model: model, missing: missing)
    }
    let snapshots = hubCacheDirectory().appendingPathComponent("models--" + model.replacingOccurrences(of: "/", with: "--"))
        .appendingPathComponent("snapshots")
    switch snapshotChoice(snapshots) {
    case .one(let dir): return dir
    case .several(let names): throw EngineError.pathRequired(model: model, snapshots: names)
    case .none: return nil
    }
}
