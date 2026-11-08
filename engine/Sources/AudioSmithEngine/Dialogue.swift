import AudioSmithDialogue
import AudioSmithProtocol
import Foundation
@preconcurrency import MLX

// tts.dialogue (docs/protocol-v2.md §5.6): a conversation in one job on a
// dialogue model. SoulX-Podcast writes the turns one by one, each hearing
// the ones before it, and each turn is its own file (tts.turn as it is
// written). MOSS-TTSD speaks them all in one track (MossDialogue.swift),
// and so does VibeVoice 1.5B (VibeVoice.swift).
// Turns that carry codes from an earlier result are context only: the app
// redoes a single turn by sending the turns before it with their codes and
// the turn itself without.

extension Synthesizer {
    /// Model families of dialogue models (catalog.Entry.Family).
    /// (VibeVoice, a TTS family too, is in Synthesizer.families.)
    static let dialogueFamilies = ["soulx_podcast", "moss_ttsd"]

    func dialogue(_ p: DialogueParams, job: String, offline: Bool, auxPaths: [String: String]?) async throws -> DialogueResult {
        guard !p.model.isEmpty else { throw EngineError.badRequest("Missing model") }
        guard p.outputDir.hasPrefix("/") else { throw EngineError.badRequest("outputDir must be an absolute path") }
        guard !p.speakers.isEmpty else { throw EngineError.badRequest("No speakers") }
        // VibeVoice clones from the clip alone (VibeVoice.swift).
        let vibe = try isVibeVoiceDialogue(p.model, path: p.modelPath, offline: offline)
        let moss = try !vibe && isMossDialogue(p.model, path: p.modelPath, offline: offline)
        let maxSpeakers = vibe ? VibeVoiceScript.maxSpeakers : moss ? MossTTSDTokens.maxSpeakers : SoulXPrompt.maxSpeakers
        guard p.speakers.count <= maxSpeakers else {
            throw EngineError.unsupported("\(shortName(p.model)) takes at most \(maxSpeakers) speakers", capability: "speakers")
        }
        var index: [String: Int] = [:]
        for (i, s) in p.speakers.enumerated() {
            guard !s.id.isEmpty, index[s.id] == nil else { throw EngineError.badRequest("Speaker ids must be unique and not empty") }
            guard vibe || nonEmpty(s.refText) != nil else {
                throw EngineError.badRequest("Speaker \(s.id) needs the reference's transcript (refText)")
            }
            index[s.id] = i
        }
        guard !p.turns.isEmpty else { throw EngineError.badRequest("No turns") }
        for (i, t) in p.turns.enumerated() {
            guard index[t.speaker] != nil else { throw EngineError.badRequest("Turn \(i) names an unknown speaker \(t.speaker)") }
            guard nonEmpty(t.text) != nil else { throw EngineError.badRequest("Turn \(i) has no text") }
        }
        guard p.turns.contains(where: { $0.codes == nil }) else { throw EngineError.badRequest("Every turn has codes: nothing to generate") }
        let bits = p.bits ?? 16
        guard [16, 24, 32].contains(bits) else { throw EngineError.badRequest("bits must be 16, 24 or 32") }
        if vibe {
            return try await vibeVoiceDialogue(p, index: index, bits: bits, job: job, offline: offline)
        }
        if moss {
            return try await mossDialogue(p, index: index, bits: bits, job: job, offline: offline)
        }

        let model = try await acquireDialogue(p.model, path: p.modelPath, job: job, offline: offline, auxPaths: auxPaths)
        defer { store.release(p.model) }

        let seed = p.seed ?? Int64.random(in: 0 ... Int64(Int32.max))
        MLXRandom.seed(UInt64(bitPattern: seed))
        var sampling = SoulXSampling()
        if let t = p.temperature, t > 0 { sampling.temperature = Float(t) }
        if let v = p.topP, v > 0, v <= 1 { sampling.topP = Float(v) }
        if let k = p.topK, k > 0 { sampling.topK = k }
        if let r = p.repetitionPenalty, r > 0 { sampling.repetitionPenalty = Float(r) }
        if let m = p.maxTokens, m > 0 { sampling.maxTokens = min(m, 3000) }
        var sampler = SoulXSampler(config: sampling, seed: UInt64(bitPattern: seed) &+ 0x5EED)
        let steps = (p.steps ?? 0) > 0 ? p.steps! : 15

        let progress = JobProgress(out: out, job: job)
        progress.send("encoding", force: true)
        var refs: [SoulXReference] = []
        var blocks: [[Int]] = []
        for (i, s) in p.speakers.enumerated() {
            let r = try await model.reference(s.refAudio)
            refs.append(r)
            blocks.append(model.encode(SoulXPrompt.reference(speaker: i, text: s.refText))
                + r.tokens.map { $0 + SoulXVocab.speechOffset } + [SoulXVocab.eos])
            try Task.checkCancellation()
        }

        let outDir = URL(fileURLWithPath: p.outputDir)
        try FileManager.default.createDirectory(at: outDir, withIntermediateDirectories: true)
        let started = Date()
        var context = SoulXContext(references: blocks)
        var cache = try model.model.newCache(parameters: nil)
        var cached = 0
        var turns: [DialogueTurnOut] = []
        var total = 0.0
        let toGenerate = p.turns.filter { $0.codes == nil }.count
        progress.fraction = 0
        progress.send("generating", force: true)
        for (i, t) in p.turns.enumerated() {
            let spk = index[t.speaker]!
            let text = model.encode(SoulXPrompt.turn(speaker: spk, text: t.text))
            if context.begin(text: text) {
                cache = try model.model.newCache(parameters: nil)
                cached = 0
            }
            if let codes = t.codes {
                guard codes.allSatisfy({ $0 >= 0 && $0 < SoulXVocab.speechCount }) else {
                    throw EngineError.badRequest("Turn \(i) has codes outside 0..<\(SoulXVocab.speechCount)")
                }
                context.end(text: text, generated: codes.map { $0 + SoulXVocab.speechOffset } + [SoulXVocab.eos])
                continue
            }
            let turnStart = Date()
            let done = Double(turns.count)
            let generated = try model.generate(context, cache: cache, cached: &cached, sampler: &sampler) { n in
                // About 25 tokens a second of speech; a turn rarely runs past 30 s.
                progress.fraction = min((done + min(Double(n) / 750, 0.9)) / Double(toGenerate), 1)
                progress.send("generating")
            }
            context.end(text: text, generated: generated)
            let lmSeconds = Date().timeIntervalSince(turnStart)
            let codes = generated.compactMap(SoulXVocab.local).filter { $0 < SoulXVocab.speechCount }
            let flowStart = Date()
            let samples = model.render(codes, reference: refs[spk], steps: steps)
            out.logLine("info", String(format: "SoulX-Podcast turn %d: %d tokens in %.2f s (%.1f tok/s), flow+HiFT %.2f s", i, generated.count,
                                       lmSeconds, Double(generated.count) / max(lmSeconds, 1e-6), Date().timeIntervalSince(flowStart)), job: job)
            try Task.checkCancellation()
            guard !samples.isEmpty else { throw RPCError(Code.internal, "Turn \(i) produced no audio") }
            let url = outDir.appendingPathComponent(String(format: "turn-%03d.wav", i))
            do {
                try writeWAV(samples, sampleRate: SoulXModel.sampleRate, bits: bits, to: url)
            } catch {
                throw RPCError(Code.internal, "Could not write \(url.path): \(error.localizedDescription)")
            }
            let seconds = Double(samples.count) / Double(SoulXModel.sampleRate)
            total += seconds
            var o = DialogueTurnOut(index: i, speaker: t.speaker)
            o.output = url.path
            o.frames = Int64(samples.count)
            o.audioSeconds = seconds
            o.codes = codes
            o.seconds = Date().timeIntervalSince(turnStart)
            turns.append(o)
            out.notify(Method.ttsTurn, TurnParams(job: job, index: i, speaker: t.speaker, output: url.path,
                                                  frames: Int64(samples.count), audioSeconds: seconds, sampleRate: SoulXModel.sampleRate))
            progress.audioSeconds = total
            progress.fraction = Double(turns.count) / Double(toGenerate)
            progress.send("generating", force: true)
        }
        if sampler.rasFallbacks > 0 || context.rebuilds > 0 {
            out.logLine("info", "SoulX-Podcast: \(sampler.rasFallbacks) repetition fallbacks, context rebuilt \(context.rebuilds) times", job: job)
        }
        cache = []
        MLX.Memory.clearCache()
        let seconds = Date().timeIntervalSince(started)
        return DialogueResult(model: p.model, sampleRate: SoulXModel.sampleRate, channels: 1, turns: turns, mixed: nil,
                              audioSeconds: total, seconds: seconds, rtf: seconds > 0 ? total / seconds : 0, seed: seed)
    }

    /// models.load with task "dialogue".
    func loadDialogue(_ p: LoadParams, job: String, offline: Bool, auxPaths: [String: String]?) async throws -> LoadResult {
        let started = Date()
        if try isVibeVoiceDialogue(p.model, path: p.path, offline: offline) {
            let m = try await acquire(p.model, path: p.path, job: job, offline: offline)
            defer { store.release(p.model) }
            let bytes = store.loaded().first { $0.model == p.model }?.bytes
            return LoadResult(model: p.model, task: "dialogue", sampleRate: m.sampleRate,
                              seconds: Date().timeIntervalSince(started), bytes: (bytes ?? 0) > 0 ? bytes : nil,
                              capabilities: ["dialogue", "clone", "seed", "steps"])
        }
        if try isMossDialogue(p.model, path: p.path, offline: offline) {
            _ = try await acquireMoss(p.model, path: p.path, job: job, offline: offline)
            defer { store.release(p.model) }
            let bytes = store.loaded().first { $0.model == p.model }?.bytes
            return LoadResult(model: p.model, task: "dialogue", sampleRate: MossTTSDModel.sampleRate,
                              seconds: Date().timeIntervalSince(started), bytes: (bytes ?? 0) > 0 ? bytes : nil,
                              capabilities: ["dialogue", "clone", "seed", "sampling"])
        }
        _ = try await acquireDialogue(p.model, path: p.path, job: job, offline: offline, auxPaths: auxPaths)
        defer { store.release(p.model) }
        let bytes = store.loaded().first { $0.model == p.model }?.bytes
        return LoadResult(model: p.model, task: "dialogue", sampleRate: SoulXModel.sampleRate,
                          seconds: Date().timeIntervalSince(started), bytes: (bytes ?? 0) > 0 ? bytes : nil,
                          capabilities: ["dialogue", "clone", "seed", "sampling", "steps"])
    }

    /// Whether model is MOSS-TTSD (else SoulX-Podcast); fails when it is
    /// not downloaded or not a dialogue model.
    func isMossDialogue(_ model: String, path: String?, offline: Bool) throws -> Bool {
        guard let dir = try modelDirectory(model, path: path, offline: offline) else {
            throw EngineError.notDownloaded(model: model, missing: ["config.json"])
        }
        if MossTTSDModel.isModel(dir) { return true }
        guard SoulXModel.isModel(dir) else {
            throw EngineError.unsupported("\(shortName(model)) cannot speak dialogues", capability: "dialogue")
        }
        return false
    }

    /// The dialogue model, loaded (stage "loading") when it is not resident.
    /// Call store.release(model) when done.
    func acquireDialogue(_ model: String, path: String?, job: String, offline: Bool, auxPaths: [String: String]?) async throws -> SoulXModel {
        guard let dir = try modelDirectory(model, path: path, offline: offline) else {
            throw EngineError.notDownloaded(model: model, missing: ["config.json"])
        }
        guard SoulXModel.isModel(dir) else {
            throw EngineError.unsupported("\(shortName(model)) cannot speak dialogues", capability: "dialogue")
        }
        let s3Dir = try s3TokenizerDirectory(auxPaths, offline: offline)
        let (m, _) = try await store.acquire(model, task: "dialogue", job: job, as: SoulXModel.self,
                                             sampleRate: { _ in SoulXModel.sampleRate }, estimate: weightBytes(dir)) {
            try await SoulXModel.load(dir, s3Tokenizer: s3Dir)
        }
        return m
    }

    /// The S3 tokenizer's folder: the app's snapshot (auxPaths), else the
    /// engine's link folder or the cache.
    func s3TokenizerDirectory(_ auxPaths: [String: String]?, offline: Bool) throws -> URL {
        if let path = auxPaths?[SoulXModel.s3TokenizerRepo], !path.isEmpty { return URL(fileURLWithPath: path) }
        let linked = engineCopyDirectory(SoulXModel.s3TokenizerRepo)
        if FileManager.default.fileExists(atPath: linked.path) { return linked }
        guard let dir = try modelDirectory(SoulXModel.s3TokenizerRepo, path: nil, offline: offline) else {
            throw EngineError.notDownloaded(model: SoulXModel.s3TokenizerRepo, missing: ["model.safetensors"])
        }
        return dir
    }
}
