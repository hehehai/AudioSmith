import AudioSmithProtocol
import AudioSmithStream
import Foundation
@preconcurrency import MLX
import MLXAudioCore
import MLXAudioSTS
import MLXAudioSTT
import MLXAudioVAD

/// The gpu worker's analysis jobs: stt.transcribe, stt.align, vad.detect,
/// enhance.run. Models stay resident in the shared ModelStore.
final class Analyzer: @unchecked Sendable {
    let out: Writer
    let store: ModelStore

    /// Model families this build loads for analysis (catalog.Entry.Family).
    static let families = ["whisper", "qwen3_asr", "parakeet", "moss_transcribe_diarize", "qwen3_forced_aligner", "silero_vad", "deepfilternet"]
    /// The ones the listen worker loads (speech-to-text only; not MOSS,
    /// which is too slow for dictation).
    static let listenFamilies = ["whisper", "qwen3_asr", "parakeet"]

    init(out: Writer, store: ModelStore) {
        self.out = out
        self.store = store
    }

    // MARK: stt.transcribe

    func transcribe(_ p: TranscribeParams, job: String, offline: Bool) async throws -> TranscribeResult {
        guard !p.model.isEmpty else { throw EngineError.badRequest("Missing model") }
        let task = p.task ?? "transcribe"
        guard task == "transcribe" || task == "translate" else { throw EngineError.badRequest("Unknown task \(task)") }
        let lower = p.model.lowercased()
        if task == "translate", !lower.contains("whisper") {
            throw EngineError.unsupported("\(shortName(p.model)) cannot translate", capability: "translate")
        }
        let model = try await sttModel(p.model, path: p.modelPath, job: job, offline: offline)
        defer { store.release(p.model) }

        let samples = try await decodeMono(p.audio, sampleRate: 16000)
        let audioSeconds = Double(samples.count) / 16000
        let progress = JobProgress(out: out, job: job)
        progress.audioSeconds = audioSeconds
        progress.send("transcribing", force: true)
        let started = Date()
        let audio = MLXArray(samples)
        let language = whisperOrNameLanguage(p.language, whisper: model is WhisperModel)

        var text = ""
        var segments: [Segment] = []
        var detected: String?
        if let whisper = model as? WhisperModel {
            whisper.task = task
            defer { whisper.task = "transcribe" }
            var params = whisper.defaultGenerationParameters
            params = STTGenerateParameters(maxTokens: params.maxTokens, temperature: 0, topP: 1, topK: 0,
                                           language: language, chunkDuration: params.chunkDuration,
                                           minChunkDuration: params.minChunkDuration)
            let output = try await runCancellable { whisper.generate(audio: audio, generationParameters: params) }
            (text, segments, detected) = convert(output)
        } else if let moss = model as? MossTranscribeDiarizeModel {
            (text, segments, detected) = try transcribeMoss(moss, samples: samples, prompt: p.prompt, progress: progress)
        } else if let qwen = model as? Qwen3ASRModel {
            // 30 s chunks give segments of a useful size; its own default
            // is one per 20 minutes.
            let output = try await runCancellable {
                qwen.generate(audio: audio, context: p.prompt ?? "", language: language, chunkDuration: 30, minChunkDuration: 1)
            }
            (text, segments, detected) = convert(output)
        } else {
            var params = model.defaultGenerationParameters
            params = STTGenerateParameters(maxTokens: params.maxTokens, temperature: params.temperature, topP: params.topP,
                                           topK: params.topK, language: language, chunkDuration: params.chunkDuration,
                                           minChunkDuration: params.minChunkDuration)
            let output = try await runCancellable { model.generate(audio: audio, generationParameters: params) }
            (text, segments, detected) = convert(output)
        }
        try Task.checkCancellation()
        MLX.Memory.clearCache()
        let seconds = Date().timeIntervalSince(started)
        let code = languageCode(detected) ?? languageCode(p.language) ?? "und"
        if segments.isEmpty, !text.isEmpty {
            segments = [Segment(start: 0, end: audioSeconds, text: text)]
        }
        return TranscribeResult(model: p.model, text: text, language: code, segments: segments,
                                audioSeconds: audioSeconds, seconds: seconds, rtf: seconds > 0 ? audioSeconds / seconds : 0)
    }

    /// The speech-to-text model, loaded when not resident (release it after).
    private func sttModel(_ model: String, path: String?, job: String, offline: Bool) async throws -> any STTGenerationModel {
        let dir = try modelDirectory(model, path: path, offline: offline)
        let moss = Self.isMoss(model) || dir.map { modelType($0) == Self.mossModelType } ?? false
        let (m, _) = try await store.acquire(model, task: "stt", job: job, as: (any STTGenerationModel).self,
                                             sampleRate: { _ in 16000 }, estimate: moss ? Self.mossEstimate : weightBytes(dir)) {
            if moss {
                return try await self.loadMoss(model, dir: dir)
            }
            if model.lowercased().contains("whisper"), let dir {
                let m = try await WhisperModel.fromDirectory(dir)
                dropEngineCopy(model)
                return m
            }
            if let dir, modelType(dir) == "qwen3_asr" {
                return try await Qwen3ASRModel.fromModelDirectory(linkedModelDirectory(model, snapshot: dir))
            }
            return try await STT.loadModel(modelRepo: model)
        }
        return m
    }

    /// models.load of an analysis model (task stt, align, vad or enhance).
    func load(_ p: LoadParams, job: String, offline: Bool) async throws -> LoadResult {
        guard !p.model.isEmpty else { throw EngineError.badRequest("Missing model") }
        let started = Date()
        let task = p.task ?? "stt"
        var rate = 16000
        switch task {
        case "stt":
            _ = try await sttModel(p.model, path: p.path, job: job, offline: offline)
        case "align":
            let dir = try modelDirectory(p.model, path: p.path, offline: offline)
            _ = try await store.acquire(p.model, task: "align", job: job, as: Qwen3ForcedAlignerModel.self, sampleRate: { _ in 16000 },
                                        estimate: weightBytes(dir)) {
                try await loadAligner(p.model, dir: dir)
            }
        case "vad":
            let dir = try modelDirectory(p.model, path: p.path, offline: offline)
            _ = try await store.acquire(p.model, task: "vad", job: job, as: SileroVAD.self, sampleRate: { _ in 16000 },
                                        estimate: weightBytes(dir)) {
                if let dir { return try SileroVAD.fromModelDirectory(dir) }
                return try await SileroVAD.fromPretrained(p.model)
            }
        case "enhance":
            let dir = try modelDirectory(p.model, path: p.path, offline: offline)
            let (m, _) = try await store.acquire(p.model, task: "enhance", job: job, as: DeepFilterNetModel.self, sampleRate: { $0.sampleRate },
                                                 estimate: weightBytes(dir)) {
                if let dir { return try DeepFilterNetModel.fromLocal(dir, subfolder: DeepFilterNetModel.defaultSubfolder) }
                return try await DeepFilterNetModel.fromPretrained(p.model)
            }
            rate = m.sampleRate
        case "llm":
            _ = try await llmModel(p.model, path: p.path, job: job, offline: offline)
            rate = 0
        default:
            throw EngineError.badRequest("Unknown task \(task)")
        }
        store.release(p.model)
        let bytes = store.loaded().first { $0.model == p.model }?.bytes
        return LoadResult(model: p.model, task: task, sampleRate: rate, seconds: Date().timeIntervalSince(started),
                          bytes: (bytes ?? 0) > 0 ? bytes : nil)
    }

    /// The model's language parameter for a BCP-47 code: the code for
    /// Whisper, the English name for Qwen3-ASR and others; nil detects.
    private func whisperOrNameLanguage(_ code: String?, whisper: Bool) -> String? {
        guard let code = nonEmpty(code), code.lowercased() != "auto" else { return nil }
        if whisper { return baseLanguage(code) }
        return languageName(code) ?? code
    }

    private func convert(_ o: STTOutput) -> (String, [Segment], String?) {
        var segs: [Segment] = []
        for s in o.segments ?? [] {
            let text = ((s["text"] as? String) ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            let start = (s["start"] as? Double) ?? Double((s["start"] as? Float) ?? 0)
            let end = (s["end"] as? Double) ?? Double((s["end"] as? Float) ?? Float(start))
            if text.isEmpty { continue }
            segs.append(Segment(start: round3(start), end: round3(end), text: text))
        }
        return (o.text.trimmingCharacters(in: .whitespacesAndNewlines), segs, o.language)
    }

    // MARK: stt.align

    func align(_ p: AlignParams, job: String, offline: Bool) async throws -> AlignResult {
        guard !p.model.isEmpty else { throw EngineError.badRequest("Missing model") }
        guard let text = nonEmpty(p.text) else { throw EngineError.badRequest("Missing text") }
        let dir = try modelDirectory(p.model, path: p.modelPath, offline: offline)
        let (model, _) = try await store.acquire(p.model, task: "align", job: job, as: Qwen3ForcedAlignerModel.self,
                                                 sampleRate: { _ in 16000 }, estimate: weightBytes(dir)) {
            try await loadAligner(p.model, dir: dir)
        }
        defer { store.release(p.model) }
        let samples = try await decodeMono(p.audio, sampleRate: 16000)
        let audioSeconds = Double(samples.count) / 16000
        let progress = JobProgress(out: out, job: job)
        progress.send("aligning", force: true)
        let started = Date()
        // Its word splitting knows Chinese (per character) and
        // space-separated languages; Japanese goes per character too.
        let lang = baseLanguage(p.language ?? "") ?? (containsCJK(text) ? "zh" : "en")
        let modelLanguage = (lang == "zh" || lang == "ja" || lang == "yue") ? "Chinese" : (languageName(lang) ?? "English")
        let audio = MLXArray(samples)
        let result = try await runCancellable { model.generate(audio: audio, text: text, language: modelLanguage) }
        MLX.Memory.clearCache()
        let words = result.items.map { Word(text: $0.text, start: round3($0.startTime), end: round3($0.endTime)) }
        return AlignResult(model: p.model, words: words, audioSeconds: audioSeconds, seconds: Date().timeIntervalSince(started))
    }

    /// The forced aligner from the app's snapshot through a folder of links
    /// (its loader writes a tokenizer.json beside the weights, which must
    /// not go into the snapshot); by repo id when there is no snapshot.
    private func loadAligner(_ model: String, dir: URL?) async throws -> Qwen3ForcedAlignerModel {
        if let dir { return try await Qwen3ForcedAlignerModel.fromModelDirectory(linkedModelDirectory(model, snapshot: dir)) }
        return try await Qwen3ForcedAlignerModel.fromPretrained(model)
    }

    // MARK: vad.detect

    func vad(_ p: VADParams, job: String, offline: Bool) async throws -> VADResult {
        guard !p.model.isEmpty else { throw EngineError.badRequest("Missing model") }
        let dir = try modelDirectory(p.model, path: p.modelPath, offline: offline)
        let (model, _) = try await store.acquire(p.model, task: "vad", job: job, as: SileroVAD.self, sampleRate: { _ in 16000 },
                                                 estimate: weightBytes(dir)) {
            if let dir { return try SileroVAD.fromModelDirectory(dir) }
            return try await SileroVAD.fromPretrained(p.model)
        }
        defer { store.release(p.model) }
        let samples = try await decodeMono(p.audio, sampleRate: 16000)
        let audioSeconds = Double(samples.count) / 16000
        JobProgress(out: out, job: job).send("detecting", force: true)
        let started = Date()
        let audio = MLXArray(samples)
        let probs = try model.predictProba(audio, sampleRate: 16000)
        eval(probs)
        try Task.checkCancellation()
        let stamps = SileroVAD.probsToTimestamps(
            probs, audioLen: samples.count, sampleRate: 16000,
            threshold: Float(p.threshold ?? Double(model.config.threshold)),
            minSpeechDurationMs: p.minSpeech.map { Int($0 * 1000) } ?? model.config.minSpeechDurationMs,
            minSilenceDurationMs: p.minSilence.map { Int($0 * 1000) } ?? model.config.minSilenceDurationMs,
            speechPadMs: p.pad.map { Int($0 * 1000) } ?? model.config.speechPadMs)
        let row = (probs.ndim == 2 ? probs[0] : probs).asArray(Float.self)
        let regions = stamps.map { s -> Region in
            let a = min(s.start / 512, row.count), b = min(max(s.end / 512, a + 1), row.count)
            let mean = a < b ? row[a ..< b].reduce(0, +) / Float(b - a) : 0
            return Region(start: round3(Double(s.start) / 16000), end: round3(Double(s.end) / 16000),
                          prob: Double((mean * 1000).rounded() / 1000))
        }
        return VADResult(model: p.model, regions: regions, audioSeconds: audioSeconds, seconds: Date().timeIntervalSince(started))
    }

    // MARK: enhance.run

    func enhance(_ p: EnhanceParams, job: String, offline: Bool) async throws -> EnhanceResult {
        guard !p.model.isEmpty else { throw EngineError.badRequest("Missing model") }
        let (url, bits) = try outputURL(p.output)
        let dir = try modelDirectory(p.model, path: p.modelPath, offline: offline)
        let (model, _) = try await store.acquire(p.model, task: "enhance", job: job, as: DeepFilterNetModel.self,
                                                 sampleRate: { $0.sampleRate }, estimate: weightBytes(dir)) {
            if let dir { return try DeepFilterNetModel.fromLocal(dir, subfolder: DeepFilterNetModel.defaultSubfolder) }
            return try await DeepFilterNetModel.fromPretrained(p.model)
        }
        defer { store.release(p.model) }
        let rate = model.sampleRate
        let media = try await openMedia(p.audio)
        let progress = JobProgress(out: out, job: job)
        progress.send("enhancing", force: true)
        let started = Date()
        let strength = Float(min(max(p.strength ?? 1, 0), 1))

        // Long inputs go in 60 s pieces that overlap by 1 s and crossfade,
        // read, enhanced and written as they come (code review 1102 M5:
        // the whole track was held twice), the same samples as before.
        var stream = OverlapStream(piece: 60 * rate, overlap: rate, strength: strength)
        let expected = max(media.probe.duration * Double(rate), 1)
        let cannotWrite = { (error: Error) in RPCError(Code.internal, "Could not write \(url.path): \(error.localizedDescription)") }
        let writer: WAVWriter
        do {
            writer = try WAVWriter(url: url, sampleRate: rate, channels: 1, bits: bits)
        } catch {
            throw cannotWrite(error)
        }
        do {
            let enhance = { (x: [Float]) throws -> [Float] in
                try Task.checkCancellation()
                let enhanced = try model.enhance(MLXArray(x))
                eval(enhanced)
                return enhanced.asArray(Float.self)
            }
            let emit = { (y: [Float]) throws in
                do { try writer.append(y[...]) } catch { throw cannotWrite(error) }
                progress.fraction = min(Double(writer.frames) / expected, 1)
                progress.send("enhancing")
            }
            _ = try readAudio(media, sampleRate: rate, channels: 1) { try stream.push($0, process: enhance, emit: emit) }
            try stream.finish(process: enhance, emit: emit)
            MLX.Memory.clearCache()
            if writer.frames == 0 { throw EngineError.unsupportedMedia(media.url.path, "it has no audio samples") }
            progress.send("writing", force: true)
            do { try writer.finish() } catch { throw cannotWrite(error) }
        } catch {
            writer.cancel()
            MLX.Memory.clearCache()
            throw error
        }
        let frames = writer.frames
        return EnhanceResult(output: url.path, sampleRate: rate, channels: 1, frames: frames,
                             audioSeconds: Double(frames) / Double(rate), model: p.model,
                             seconds: Date().timeIntervalSince(started))
    }
}

/// Runs synchronous model work in the job's task: MLX releases the thread
/// between ops, but the work itself cannot stop midway, so a cancelled job
/// ends when it does.
func runCancellable<T>(_ work: () throws -> T) async throws -> T {
    try Task.checkCancellation()
    let r = try work()
    try Task.checkCancellation()
    return r
}

func round3(_ x: Double) -> Double { (x * 1000).rounded() / 1000 }

func containsCJK(_ s: String) -> Bool {
    s.unicodeScalars.contains { (0x3040 ... 0x30FF).contains($0.value) || (0x3400 ... 0x9FFF).contains($0.value) || (0xAC00 ... 0xD7AF).contains($0.value) }
}

// MARK: Languages

/// BCP-47 base codes and English names of the languages the STT models
/// know (Qwen3-ASR takes names; Whisper takes codes).
let languageNames: [String: String] = [
    "zh": "Chinese", "en": "English", "yue": "Cantonese", "ar": "Arabic", "de": "German", "fr": "French",
    "es": "Spanish", "pt": "Portuguese", "id": "Indonesian", "it": "Italian", "ko": "Korean", "ru": "Russian",
    "th": "Thai", "vi": "Vietnamese", "ja": "Japanese", "tr": "Turkish", "hi": "Hindi", "ms": "Malay",
    "nl": "Dutch", "sv": "Swedish", "da": "Danish", "fi": "Finnish", "pl": "Polish", "cs": "Czech",
    "fil": "Filipino", "fa": "Persian", "el": "Greek", "ro": "Romanian", "hu": "Hungarian", "mk": "Macedonian",
    "uk": "Ukrainian", "he": "Hebrew", "no": "Norwegian", "nb": "Norwegian", "bg": "Bulgarian", "hr": "Croatian",
    "sk": "Slovak", "sl": "Slovenian", "sr": "Serbian", "ca": "Catalan", "ta": "Tamil", "ur": "Urdu",
    "bn": "Bengali", "te": "Telugu", "mr": "Marathi", "gu": "Gujarati", "kn": "Kannada", "ml": "Malayalam",
    "pa": "Punjabi", "sw": "Swahili", "af": "Afrikaans", "lt": "Lithuanian", "lv": "Latvian", "et": "Estonian",
]

/// "zh-Hans" → "zh"; nil for "" and "auto".
func baseLanguage(_ code: String) -> String? {
    let c = code.trimmingCharacters(in: .whitespaces).lowercased()
    if c.isEmpty || c == "auto" { return nil }
    if c.hasPrefix("yue") { return "yue" }
    if c == "tl" { return "fil" }
    return String(c.split(whereSeparator: { $0 == "-" || $0 == "_" }).first ?? Substring(c))
}

func languageName(_ code: String) -> String? {
    baseLanguage(code).flatMap { languageNames[$0] }
}

/// A model's language (a code, a name, "Chinese,English") as a BCP-47 code.
func languageCode(_ value: String?) -> String? {
    guard let v = nonEmpty(value)?.split(separator: ",").first.map(String.init)?.trimmingCharacters(in: .whitespaces),
          !v.isEmpty else { return nil }
    let lower = v.lowercased()
    if let match = languageNames.first(where: { $0.value.lowercased() == lower }) {
        return match.key == "nb" ? "no" : match.key
    }
    if lower.count <= 3 || lower.contains("-") { return baseLanguage(lower) }
    if lower == "mandarin" { return "zh" }
    return nil
}
