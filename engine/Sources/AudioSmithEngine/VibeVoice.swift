import AudioSmithDialogue
import AudioSmithProtocol
import Foundation
@preconcurrency import MLX
import MLXAudioCore
import MLXAudioTTS
import MLXLMCommon

// Microsoft's VibeVoice (thread #166; upstream mlx-audio-swift's ports,
// backported by engine patch 0011):
//
//   - VibeVoice 1.5B (model_type "vibevoice", microsoft/VibeVoice-1.5B as
//     published): clones each speaker from a reference clip alone and
//     speaks a whole conversation of up to four speakers in one take. A
//     TTS take is one speaker; tts.dialogue gives the turns as one script
//     and returns the one track (mixed, as MOSS-TTSD's: the app cuts it at
//     the lines). It has no codes, so a line cannot be made again after
//     the ones before it: the app leaves such lines to its project model.
//   - VibeVoice Realtime 0.5B ("vibevoice_streaming"): English with 25
//     preset voices, streamed; it cannot clone.
//
// Neither repo has a tokenizer: the app fetches Qwen/Qwen2.5-1.5B's
// tokenizer files as an auxiliary repo, and the model loads from a folder
// of links to its snapshot and those files (the loader would otherwise
// download them at every load).

extension Synthesizer {
    static let vibeVoiceTokenizerRepo = "Qwen/Qwen2.5-1.5B"
    static let vibeVoiceTokenizerFiles = ["tokenizer.json", "tokenizer_config.json", "vocab.json", "merges.txt"]
    static let vibeVoiceTypes: Set<String> = ["vibevoice", "vibevoice_streaming"]

    /// The folder a VibeVoice model loads from (its snapshot when that has
    /// a tokenizer, else a folder of links to the snapshot and the
    /// tokenizer's files); nil for other models.
    func vibeVoiceFolder(_ model: String, path: String?, offline: Bool) throws -> String? {
        guard model.lowercased().contains("vibevoice") || (path ?? "").lowercased().contains("vibevoice") else { return nil }
        guard let dir = try modelDirectory(model, path: path, offline: offline),
              let type = modelType(dir), Self.vibeVoiceTypes.contains(type) else { return nil }
        let fm = FileManager.default
        if fm.fileExists(atPath: dir.appendingPathComponent("tokenizer.json").path) { return dir.path }
        let tokenizer = try vibeVoiceTokenizerDirectory(offline: offline)
        let linked = try linkedModelDirectory(model, snapshot: dir)
        for name in Self.vibeVoiceTokenizerFiles {
            let target = tokenizer.appendingPathComponent(name).resolvingSymlinksInPath().path
            guard fm.fileExists(atPath: target) else {
                if name == "tokenizer.json" {
                    throw EngineError.notDownloaded(model: Self.vibeVoiceTokenizerRepo, missing: [name])
                }
                continue
            }
            let link = linked.appendingPathComponent(name).path
            if (try? fm.destinationOfSymbolicLink(atPath: link)) == target { continue }
            try? fm.removeItem(atPath: link)
            try fm.createSymbolicLink(atPath: link, withDestinationPath: target)
        }
        return linked.path
    }

    /// The Qwen2.5 tokenizer's folder: the engine's link folder (the app's
    /// snapshot, linked before the job from auxPaths), else the cache's.
    func vibeVoiceTokenizerDirectory(offline: Bool) throws -> URL {
        let linked = engineCopyDirectory(Self.vibeVoiceTokenizerRepo)
        if FileManager.default.fileExists(atPath: linked.appendingPathComponent("tokenizer.json").path) { return linked }
        guard let dir = try modelDirectory(Self.vibeVoiceTokenizerRepo, path: nil, offline: offline) else {
            throw EngineError.notDownloaded(model: Self.vibeVoiceTokenizerRepo, missing: ["tokenizer.json"])
        }
        return dir
    }

    /// A TTS take on VibeVoice 1.5B: one speaker, cloned from refAudio,
    /// in the job's task (its frames report the progress).
    func vibeVoiceTake(_ vv: VibeVoiceTTSModel, text: String, refAudio: MLXArray?, params: GenerateParameters,
                       p: SynthesizeParams, progress: JobProgress) async throws -> MLXArray {
        guard let refAudio else {
            throw EngineError.badRequest("\(shortName(p.model)) only clones voices: give it a reference clip (refAudio)")
        }
        let script = VibeVoiceScript(turns: [(speaker: 0, text: text)])
        var params = params
        if params.maxTokens == nil { params.maxTokens = script.frameBudget }
        let options = vibeVoiceOptions(steps: p.steps, guidance: p.guidance)
        let expected = script.expectedSeconds * VibeVoiceScript.frameRate
        vv.onFrame = { n, _ in
            progress.fraction = min(Double(n) / expected, 0.95)
            progress.audioSeconds = Double(n) / VibeVoiceScript.frameRate
            progress.send("generating")
        }
        defer { vv.onFrame = nil }
        return try await vv.generate(text: script.text, referenceAudio: [refAudio], referenceSampleRate: vv.sampleRate,
                                     options: options, generationParameters: params)
    }

    func vibeVoiceOptions(steps: Int?, guidance: Double?) -> VibeVoiceTTSGenerationOptions {
        var o = VibeVoiceTTSGenerationOptions()
        if let s = steps, s > 0 { o.diffusionSteps = min(s, 100) }
        if let g = guidance, g > 0 { o.guidanceScale = Float(g) }
        return o
    }

    /// Whether model is VibeVoice 1.5B (a dialogue model).
    func isVibeVoiceDialogue(_ model: String, path: String?, offline: Bool) throws -> Bool {
        guard model.lowercased().contains("vibevoice") || (path ?? "").lowercased().contains("vibevoice"),
              let dir = try modelDirectory(model, path: path, offline: offline) else { return false }
        return modelType(dir) == "vibevoice"
    }

    /// tts.dialogue on VibeVoice 1.5B: the turns as one script, one track.
    func vibeVoiceDialogue(_ p: DialogueParams, index: [String: Int], bits: Int, job: String, offline: Bool) async throws -> DialogueResult {
        guard p.turns.allSatisfy({ $0.codes == nil }) else {
            throw EngineError.unsupported("\(shortName(p.model)) cannot go on from earlier turns", capability: "continuation")
        }
        let m = try await acquire(p.model, path: p.modelPath, job: job, offline: offline)
        defer { store.release(p.model) }
        guard let vv = m as? VibeVoiceTTSModel else {
            throw EngineError.unsupported("\(shortName(p.model)) cannot speak dialogues", capability: "dialogue")
        }
        let seed = p.seed ?? Int64.random(in: 0 ... Int64(Int32.max))
        MLXRandom.seed(UInt64(bitPattern: seed))
        let script = VibeVoiceScript(turns: p.turns.map { (speaker: index[$0.speaker]!, text: $0.text) })

        let progress = JobProgress(out: out, job: job)
        progress.send("encoding", force: true)
        let started = Date()
        var refs: [MLXArray] = []
        for i in script.speakers {
            refs.append(MLXArray(try await decodeMono(p.speakers[i].refAudio, sampleRate: vv.sampleRate)))
            try Task.checkCancellation()
        }
        var params = vv.defaultGenerationParameters
        params.maxTokens = min(p.maxTokens.flatMap { $0 > 0 ? $0 : nil } ?? script.frameBudget, VibeVoiceScript.maxFrames)
        if let t = p.temperature, t > 0 { params.temperature = Float(t) }
        if let v = p.topP, v > 0, v <= 1 { params.topP = Float(v) }
        let expected = script.expectedSeconds * VibeVoiceScript.frameRate
        progress.fraction = 0
        progress.send("generating", force: true)
        vv.onFrame = { n, _ in
            progress.fraction = min(Double(n) / expected, 0.95)
            progress.audioSeconds = Double(n) / VibeVoiceScript.frameRate
            progress.send("generating")
        }
        defer { vv.onFrame = nil }
        let genStart = Date()
        let audio: MLXArray
        do {
            audio = try await vv.generate(text: script.text, referenceAudio: refs, referenceSampleRate: vv.sampleRate,
                                          options: vibeVoiceOptions(steps: p.steps, guidance: nil), generationParameters: params)
        } catch AudioGenerationError.invalidInput(let message) {
            throw EngineError.badRequest(message)
        }
        try Task.checkCancellation()
        let samples = try monoSamples(audio)
        guard !samples.isEmpty else { throw RPCError(Code.internal, "VibeVoice produced no audio") }
        let seconds = Double(samples.count) / Double(vv.sampleRate)
        out.logLine("info", String(format: "VibeVoice: %d turns, %d speakers, %.1f s of speech in %.1f s (%.2f× real time)",
                                   p.turns.count, script.speakers.count, seconds, Date().timeIntervalSince(genStart),
                                   seconds / max(Date().timeIntervalSince(genStart), 1e-6)), job: job)

        let outDir = URL(fileURLWithPath: p.outputDir)
        try FileManager.default.createDirectory(at: outDir, withIntermediateDirectories: true)
        let url = outDir.appendingPathComponent("dialogue.wav")
        do {
            try writeWAV(samples, sampleRate: vv.sampleRate, bits: bits, to: url)
        } catch {
            throw RPCError(Code.internal, "Could not write \(url.path): \(error.localizedDescription)")
        }
        var turns: [DialogueTurnOut] = []
        for (i, t) in p.turns.enumerated() {
            var o = DialogueTurnOut(index: i, speaker: t.speaker)
            o.text = script.turns[i]
            turns.append(o)
        }
        MLX.Memory.clearCache()
        let total = Date().timeIntervalSince(started)
        let mixed = DialogueMixed(output: url.path, frames: Int64(samples.count), audioSeconds: seconds)
        return DialogueResult(model: p.model, sampleRate: vv.sampleRate, channels: 1, turns: turns, mixed: mixed,
                              audioSeconds: seconds, seconds: total, rtf: total > 0 ? seconds / total : 0, seed: seed)
    }
}
