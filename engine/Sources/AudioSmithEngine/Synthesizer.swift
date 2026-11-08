import AudioSmithProtocol
import AudioSmithText
import Foundation
@preconcurrency import MLX
import MLXAudioCore
import MLXAudioTTS
import MLXLMCommon

/// Runs the TTS jobs (tts.synthesize, tts.encodeVoice, models.load of TTS
/// models) on models kept in the shared ModelStore.
final class Synthesizer: @unchecked Sendable {
    /// Model families this build loads (catalog.Entry.Family names).
    static let families = [
        "kokoro", "kitten", "qwen3_tts", "qwen3", "marvis", "soprano", "omnivoice", "chatterbox", "pocket_tts",
        "llama_tts", "fish_speech", "echo_tts", "irodori_tts", "moss_tts", "moss_tts_nano", "indextts", "spark", "breeze",
        "voxcpm2", "dia",
    ]

    let out: Writer
    let store: ModelStore

    init(out: Writer, store: ModelStore) {
        self.out = out
        self.store = store
    }

    /// Returns the TTS model, loading it (stage "loading") when it is not resident.
    private func acquire(_ model: String, path: String?, job: String, offline: Bool) async throws -> SpeechGenerationModel {
        if offline && (path ?? "").isEmpty, let missing = missingFromCache(model) {
            throw EngineError.notDownloaded(model: model, missing: missing)
        }
        let token = ProcessInfo.processInfo.environment["HF_TOKEN"]
        markAuxCopies()
        let (m, _) = try await store.acquire(model, task: "tts", job: job, as: SpeechGenerationModel.self,
                                             sampleRate: { $0.sampleRate },
                                             estimate: weightBytes(weightsDirectory(model, path: path))) {
            if let path, !path.isEmpty {
                do {
                    let m = try await TTS.loadModel(modelRepo: path, hfToken: token)
                    dropEngineCopy(model)
                    return m
                } catch TTSModelError.unsupportedModelType(let type) where (type ?? "").hasSuffix("from local directory") {
                    // Families without a directory loader in mlx-audio-swift
                    // (Marvis, OmniVoice, Spark, Dia) resolve the repo id in
                    // the HF cache instead, where the app downloaded the same
                    // snapshot (standard layout, found without the network).
                    if offline, let missing = missingFromCache(model) {
                        throw EngineError.notDownloaded(model: model, missing: missing)
                    }
                    log("\(model): no directory loader for its family, loading it from the cache by id")
                    return try await TTS.loadModel(modelRepo: model, hfToken: token)
                }
            }
            return try await TTS.loadModel(modelRepo: model, hfToken: token)
        }
        return m
    }

    // MARK: Jobs

    func load(_ p: LoadParams, job: String, offline: Bool, auxPaths: [String: String]? = nil) async throws -> LoadResult {
        if p.task == "dialogue" { return try await loadDialogue(p, job: job, offline: offline, auxPaths: auxPaths) }
        let started = Date()
        let model = try await acquire(p.model, path: p.path, job: job, offline: offline)
        defer { store.release(p.model) }
        let bytes = store.loaded().first { $0.model == p.model }?.bytes
        return LoadResult(model: p.model, task: p.task ?? "tts", sampleRate: model.sampleRate,
                          seconds: Date().timeIntervalSince(started), bytes: (bytes ?? 0) > 0 ? bytes : nil,
                          capabilities: capabilities(model, repo: p.model))
    }

    /// What the loaded model supports, named as in catalog.Capability.
    func capabilities(_ model: SpeechGenerationModel, repo: String) -> [String] {
        var caps = ["seed"]
        switch model {
        case is KokoroModel, is KittenTTSModel:
            caps.append("speed")
        case is OmniVoiceModel:
            caps += ["stream", "speed", "duration", "steps", "guidance", "clone", "design", "voiceStyle", "language", "nonVerbal"]
        case is Qwen3TTSModel:
            caps += ["stream", "sampling", "language"]
            let l = repo.lowercased()
            if l.contains("base") { caps += ["clone", "cloneAudioOnly", "voicePrompt"] }
            if l.contains("voicedesign") { caps += ["design", "voiceStyle", "instruct"] }
            if l.contains("customvoice") { caps += ["voiceStyle", "instruct"] }
        case is ChatterboxModel:
            caps += ["stream", "sampling", "clone", "guidance", "exaggeration"]
        case is VoxCPM2Model:
            // VoxCPM2 (thread #165): clones with or without the transcript,
            // designs from a "(description)" and restyles a clone with a
            // "(instruction)" (voxCPMPrompt); 48 kHz.
            caps += ["clone", "cloneAudioOnly", "design", "voiceStyle", "instruct", "steps", "guidance"]
        default:
            caps += ["stream", "sampling"]
        }
        return caps
    }

    func synthesize(_ p: SynthesizeParams, job: String, offline: Bool) async throws -> SynthesizeResult {
        guard !p.model.isEmpty else { throw EngineError.badRequest("Missing model") }
        let text = p.text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { throw EngineError.badRequest("Missing text") }
        let (outURL, bits) = try outputURL(p.output)

        let model = try await acquire(p.model, path: p.modelPath, job: job, offline: offline)
        defer { store.release(p.model) }

        var refPath = nonEmpty(p.refAudio)
        var refText = nonEmpty(p.refText)
        // A voice prompt from tts.encodeVoice replaces the reference.
        var conditioning: Qwen3TTSModel.Qwen3TTSReferenceConditioning?
        if let prompt = nonEmpty(p.voicePrompt) {
            do {
                conditioning = try loadVoicePrompt(prompt, model: p.model, into: model)
                refPath = nil
                refText = nil
            } catch {
                guard refPath != nil else {
                    throw EngineError.badRequest("Voice prompt \(prompt) is not usable: \(errorMessage(error))")
                }
                out.logLine("warning", "Voice prompt not usable, using the reference audio: \(errorMessage(error))", job: job)
            }
        }
        if let kokoro = model as? KokoroModel {
            if refPath != nil {
                throw EngineError.unsupported("\(shortName(p.model)) cannot clone voices", capability: "clone")
            }
            kokoro.speed = Float(p.speed ?? 1.0)
        }
        if let qwen = model as? Qwen3TTSModel, refPath != nil, !p.model.lowercased().contains("base") {
            _ = qwen
            throw EngineError.unsupported("\(shortName(p.model)) cannot clone voices", capability: "clone")
        }
        var params = model.defaultGenerationParameters
        if let maxTokens = p.maxTokens, maxTokens > 0 { params.maxTokens = maxTokens }
        if let temperature = p.temperature { params.temperature = Float(temperature) }
        if let topP = p.topP { params.topP = Float(topP) }
        // Seeded every time, so a take can be reproduced from its result.
        let seed = p.seed ?? Int64.random(in: 0 ... Int64(Int32.max))
        MLXRandom.seed(UInt64(bitPattern: seed))

        // MOSS-TTS-Nano only clones: without a reference it fails inside
        // the model; refuse the request before loading anything else.
        if model is MossTTSNanoModel, refPath == nil {
            throw EngineError.badRequest("\(shortName(p.model)) only clones voices: give it a reference clip (refAudio)")
        }
        var refAudio: MLXArray?
        if let refPath {
            refAudio = MLXArray(try await decodeMono(refPath, sampleRate: model.sampleRate))
        }

        // The voice field: a speaker, or a design description; instruct is
        // CustomVoice's instruction, or the description when voice is empty.
        var voice = nonEmpty(p.voice)
        if let instruct = nonEmpty(p.instruct) {
            if let v = voice, p.model.lowercased().contains("customvoice") {
                voice = "\(v), \(instruct)"
            } else if voice == nil {
                voice = instruct
            }
        }

        let sampleRate = model.sampleRate
        let progress = JobProgress(out: out, job: job)
        progress.send("generating", force: true)
        let started = Date()
        var samples: [Float] = []
        var seq = 0
        func audio(_ chunk: MLXArray) throws {
            let mono = try monoSamples(chunk)
            if p.stream == true {
                // Up to one second per tts.chunk.
                var i = 0
                while i < mono.count {
                    let end = min(i + sampleRate, mono.count)
                    out.notify(Method.ttsChunk, ChunkParams(job: job, seq: seq, sampleRate: sampleRate, channels: 1,
                                                            encoding: "pcm16le", data: pcm16(mono[i ..< end]), final: nil))
                    seq += 1
                    i = end
                }
            }
            samples.append(contentsOf: mono)
            progress.audioSeconds = Double(samples.count) / Double(sampleRate)
            progress.send("generating")
        }
        func consume(_ stream: AsyncThrowingStream<AudioGeneration, Error>) async throws {
            do {
                for try await event in stream {
                    try Task.checkCancellation()
                    switch event {
                    case .audio(let chunk):
                        try audio(chunk)
                    case .progress(let fraction):
                        progress.fraction = min(max(fraction, 0), 1)
                        progress.send("generating")
                    case .token:
                        progress.send("generating")
                    case .info(let info):
                        log(info.summary)
                    }
                }
            } catch {
                if Task.isCancelled {
                    // Ending the stream cancels the model's own Task, which
                    // stops at its next step: give it that step before the
                    // engine takes another job.
                    await settle()
                }
                throw error
            }
        }

        do {
            if let omni = model as? OmniVoiceModel {
                // OmniVoice takes its own parameters (diffusion steps, a
                // target duration, speed, guidance, seed), which only this
                // entry point accepts. It runs in the job's task and checks
                // for cancellation at each diffusion step.
                var ov = OmniVoiceGenerateParameters()
                if let steps = p.steps, steps > 0 { ov.numStep = steps }
                if let duration = p.duration, duration > 0 { ov.duration = Float(duration) }
                if let speed = p.speed, speed > 0 { ov.speed = Float(speed) }
                if let g = p.guidance, g > 0 { ov.guidanceScale = Float(g) }
                ov.seed = UInt64(bitPattern: seed)
                let (instruct, dropped) = omniVoiceInstruct(voice, text: text)
                if !dropped.isEmpty {
                    out.logLine("warning", "OmniVoice does not know the voice attributes \(dropped.joined(separator: ", ")): ignored", job: job)
                }
                let chunk = try await omni.generate(
                    text: text, voice: instruct, refAudio: refAudio, refText: refText,
                    language: nonEmpty(p.language), ovParameters: ov)
                try Task.checkCancellation()
                try audio(chunk)
            } else if let vox = model as? VoxCPM2Model {
                // VoxCPM2 takes its diffusion steps, guidance and length cap
                // per request, retries a take that never stops (upstream's
                // retry_badcase) and runs in the job's task, checking for
                // cancellation at each patch. A description or instruction
                // leads the text in parentheses; with one, the reference is
                // cloned in isolation, not continued from its transcript
                // (the continued prompt would read the instruction aloud).
                let instruction = voxCPMInstruction(voice: p.voice, instruct: p.instruct)
                let chunk = try vox.generate(text: voxCPMPrompt(text: text, instruction: instruction), refAudio: refAudio,
                                             refText: instruction == nil ? refText : nil,
                                             parameters: voxCPMParameters(p, seed: seed))
                if vox.lastHitCap {
                    out.logLine("warning", "VoxCPM2 never ended the take: cut at its length cap after \(vox.lastAttempts) tries", job: job)
                } else if vox.lastAttempts > 1 {
                    log("VoxCPM2: take \(vox.lastAttempts) ended normally (earlier ones ran to the cap)")
                }
                try Task.checkCancellation()
                try audio(chunk)
            } else if model is KokoroModel || model is KittenTTSModel {
                // These make the whole take in one call; their generateStream
                // only runs it in a Task of its own, which a cancelled job
                // would leave computing on the GPU while the next job starts.
                // Run here instead, so the job ends when the computation does.
                let chunk = try await model.generate(
                    text: text, voice: voice, refAudio: refAudio, refText: refText,
                    language: nonEmpty(p.language), generationParameters: params)
                try Task.checkCancellation()
                try audio(chunk)
            } else if let qwen = model as? Qwen3TTSModel, let conditioning {
                try await consume(qwen.generateStream(text: text, conditioning: conditioning,
                                                      generationParameters: params, streamingInterval: 1.0))
            } else {
                if let cb = model as? ChatterboxModel {
                    cb.cfgWeightOverride = p.guidance.map { Float($0) }
                    cb.emotionAdvOverride = p.exaggeration.map { Float($0) }
                }
                defer {
                    if let cb = model as? ChatterboxModel {
                        cb.cfgWeightOverride = nil
                        cb.emotionAdvOverride = nil
                    }
                }
                try await consume(model.generateStream(
                    text: text, voice: voice, refAudio: refAudio, refText: refText,
                    language: nonEmpty(p.language), generationParameters: params, streamingInterval: 1.0))
            }
        } catch AudioGenerationError.invalidInput(let message) {
            if let tooLong = inputTooLong(message) { throw tooLong }
            throw EngineError.badRequest(message)
        } catch MossTTSNanoError.invalidInput(let message) {
            if let tooLong = inputTooLong(message) { throw tooLong }
            throw EngineError.badRequest(message)
        }
        try Task.checkCancellation()
        guard !samples.isEmpty else { throw RPCError(Code.internal, "The model produced no audio") }
        if p.stream == true {
            out.notify(Method.ttsChunk, ChunkParams(job: job, seq: seq, sampleRate: sampleRate, channels: 1,
                                                    encoding: "pcm16le", data: Data(), final: true))
        }
        let seconds = Date().timeIntervalSince(started)
        progress.fraction = 1
        progress.send("writing", force: true)
        do {
            try writeWAV(samples, sampleRate: sampleRate, bits: bits, to: outURL)
        } catch {
            throw RPCError(Code.internal, "Could not write \(p.output.path): \(error.localizedDescription)")
        }
        MLX.Memory.clearCache()

        let audioSeconds = Double(samples.count) / Double(sampleRate)
        return SynthesizeResult(output: p.output.path, model: p.model, sampleRate: sampleRate, channels: 1,
                                frames: Int64(samples.count), audioSeconds: audioSeconds, seconds: seconds,
                                rtf: seconds > 0 ? audioSeconds / seconds : 0, seed: seed)
    }

    // MARK: Voice prompts

    static let qwenPromptFormat = "qwen3_tts/1"

    func encodeVoice(_ p: EncodeVoiceParams, job: String, offline: Bool) async throws -> EncodeVoiceResult {
        guard !p.model.isEmpty else { throw EngineError.badRequest("Missing model") }
        guard p.promptPath.hasPrefix("/") else { throw EngineError.badRequest("promptPath must be an absolute path") }
        guard p.model.lowercased().contains("base") else {
            throw EngineError.unsupported("\(shortName(p.model)) cannot encode voices", capability: "voicePrompt")
        }
        let model = try await acquire(p.model, path: p.modelPath, job: job, offline: offline)
        defer { store.release(p.model) }
        guard let qwen = model as? Qwen3TTSModel else {
            throw EngineError.unsupported("\(shortName(p.model)) cannot encode voices", capability: "voicePrompt")
        }
        guard let refText = nonEmpty(p.refText) else {
            throw EngineError.badRequest("\(shortName(p.model)) needs the reference's transcript (refText)")
        }
        let samples = try await decodeMono(p.refAudio, sampleRate: model.sampleRate)
        JobProgress(out: out, job: job).send("encoding", force: true)
        let started = Date()
        let c = try qwen.prepareReferenceConditioning(refAudio: MLXArray(samples), refText: refText, language: nonEmpty(p.language))
        var arrays = ["referenceSpeechCodes": c.referenceSpeechCodes, "referenceTextTokenIDs": c.referenceTextTokenIDs]
        if let e = c.speakerEmbedding { arrays["speakerEmbedding"] = e }
        var meta = ["format": Self.qwenPromptFormat, "model": p.model, "resolvedLanguage": c.resolvedLanguage]
        if let id = c.codecLanguageID { meta["codecLanguageID"] = String(id) }
        let url = URL(fileURLWithPath: p.promptPath)
        let dir = url.deletingLastPathComponent()
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let tmp = dir.appendingPathComponent(".\(url.lastPathComponent).\(getpid()).safetensors")
        do {
            try MLX.save(arrays: arrays, metadata: meta, url: tmp)
            guard rename(tmp.path, url.path) == 0 else { throw RPCErrorWrap.rename(String(cString: strerror(errno))) }
        } catch {
            try? FileManager.default.removeItem(at: tmp)
            throw RPCError(Code.internal, "Could not write \(p.promptPath): \(error.localizedDescription)")
        }
        let bytes = ((try? FileManager.default.attributesOfItem(atPath: url.path)[.size]) as? NSNumber)?.int64Value ?? 0
        return EncodeVoiceResult(voicePrompt: url.path, model: p.model, format: Self.qwenPromptFormat, bytes: bytes,
                                 audioSeconds: Double(samples.count) / Double(model.sampleRate),
                                 seconds: Date().timeIntervalSince(started))
    }

    /// Reads a voice prompt made for model; throws when it is not one.
    private func loadVoicePrompt(_ path: String, model: String, into m: SpeechGenerationModel) throws -> Qwen3TTSModel.Qwen3TTSReferenceConditioning {
        guard m is Qwen3TTSModel else { throw EngineError.badRequest("\(shortName(model)) takes no voice prompts") }
        let data = try Data(contentsOf: URL(fileURLWithPath: path))
        let (arrays, meta) = try MLX.loadArraysAndMetadata(data: data)
        guard meta["format"] == Self.qwenPromptFormat else { throw EngineError.badRequest("format \(meta["format"] ?? "?")") }
        guard meta["model"] == model else { throw EngineError.badRequest("made for \(meta["model"] ?? "?")") }
        guard let codes = arrays["referenceSpeechCodes"], let ids = arrays["referenceTextTokenIDs"] else {
            throw EngineError.badRequest("incomplete")
        }
        return Qwen3TTSModel.Qwen3TTSReferenceConditioning(
            speakerEmbedding: arrays["speakerEmbedding"], referenceSpeechCodes: codes, referenceTextTokenIDs: ids,
            resolvedLanguage: meta["resolvedLanguage"] ?? "auto", codecLanguageID: meta["codecLanguageID"].flatMap { Int($0) })
    }
}

/// VoxCPM2's per-request settings from a tts.synthesize request.
func voxCPMParameters(_ p: SynthesizeParams, seed: Int64) -> VoxCPM2Model.VoxGenerateParameters {
    var v = VoxCPM2Model.VoxGenerateParameters()
    if let steps = p.steps, steps > 0 { v.inferenceTimesteps = min(steps, 64) }
    if let g = p.guidance, g > 0 { v.cfgValue = Float(g) }
    if let maxTokens = p.maxTokens, maxTokens > 0 { v.maxPatches = maxTokens }
    v.seed = UInt64(bitPattern: seed)
    return v
}

/// Recognises Kokoro's "Input too long: 731 tokens exceeds max 510".
func inputTooLong(_ message: String) -> EngineError? {
    let numbers = message.split(whereSeparator: { !$0.isNumber }).compactMap { Int($0) }
    guard message.lowercased().contains("too long"), numbers.count >= 2 else { return nil }
    return .inputTooLong(limit: numbers[1], actual: numbers[0], unit: "phonemes")
}

/// Waits ~200 ms even in a cancelled task.
func settle() async {
    await Task.detached { try? await Task.sleep(nanoseconds: 200_000_000) }.value
}

/// Sends a job's job.progress, at most 10 per second unless forced.
final class JobProgress {
    let out: Writer
    let job: String
    var fraction: Double?
    var audioSeconds: Double?
    private var last = Date.distantPast
    private var lastStage = ""

    init(out: Writer, job: String) {
        self.out = out
        self.job = job
    }

    func send(_ stage: String, force: Bool = false) {
        let now = Date()
        guard force || stage != lastStage || now.timeIntervalSince(last) >= 0.1 else { return }
        last = now
        lastStage = stage
        out.notify(Method.jobProgress, ProgressParams(job: job, stage: stage, fraction: fraction, audioSeconds: audioSeconds))
    }
}

func nonEmpty(_ s: String?) -> String? {
    guard let s = s?.trimmingCharacters(in: .whitespacesAndNewlines), !s.isEmpty else { return nil }
    return s
}

/// Flattens a generated chunk to mono samples. Models return [N], [1, N],
/// [1, 1, N], [C, N] or [N, C] depending on their vocoder.
func monoSamples(_ audio: MLXArray) throws -> [Float] {
    var a = audio.asType(.float32)
    while a.ndim > 2, a.dim(0) == 1 {
        a = a[0]
    }
    switch a.ndim {
    case 0:
        return []
    case 1:
        return a.asArray(Float.self)
    case 2:
        let (rows, cols) = (a.dim(0), a.dim(1))
        if rows == 1 { return a.reshaped([cols]).asArray(Float.self) }
        if cols == 1 { return a.reshaped([rows]).asArray(Float.self) }
        // Channels are the short axis.
        let mono = rows <= 8 ? a.mean(axis: 0) : a.mean(axis: 1)
        return mono.asArray(Float.self)
    default:
        throw RPCError(Code.internal, "Unexpected audio shape \(a.shape)")
    }
}
