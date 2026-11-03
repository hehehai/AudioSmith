import AudioSmithProtocol
import DemucsMLX
import MLXAudioVAD
import Foundation
@preconcurrency import MLX
import SwiftRoFormer

// separate.run (M4): vocals / background stems with HTDemucs (default) or
// Mel-RoFormer (quality option). Any input length: the input is read and
// separated in 60 s windows that overlap by 2 s and crossfade, so memory
// stays bounded and the stems come out as long as the input, aligned with
// it sample for sample.

/// A loaded separation model.
final class StemModel: @unchecked Sendable {
    enum Kind {
        case demucs(DemucsSeparator)
        case roformer(RoFormerSeparator)
    }

    let kind: Kind
    init(_ kind: Kind) { self.kind = kind }

    var sampleRate: Int {
        switch kind {
        case .demucs(let s): s.sampleRate
        case .roformer: 44100
        }
    }

    /// Splits one window (interleaved stereo at sampleRate) into vocals and
    /// background (interleaved stereo). onProgress gets 0–1 within it.
    func separate(_ samples: [Float], shifts: Int, overlap: Double, onProgress: @escaping @Sendable (Double) -> Void) async throws -> ([Float], [Float]) {
        let frames = samples.count / 2
        switch kind {
        case .demucs(let sep):
            try sep.updateParameters(DemucsSeparationParameters(shifts: max(shifts, 1), overlap: Float(overlap)))
            var planar = [Float](repeating: 0, count: samples.count)
            for i in 0 ..< frames {
                planar[i] = samples[2 * i]
                planar[frames + i] = samples[2 * i + 1]
            }
            let audio = try DemucsAudio(channelMajor: planar, channels: 2, sampleRate: sep.sampleRate)
            let token = DemucsCancelToken()
            let result: DemucsSeparationResult = try await withTaskCancellationHandler {
                try await withCheckedThrowingContinuation { k in
                    sep.separate(audio: audio, cancelToken: token, interpolateProgress: false,
                                 progress: { p in onProgress(Double(p.fraction)) },
                                 completion: { k.resume(with: $0) })
                }
            } onCancel: {
                token.cancel()
            }
            try Task.checkCancellation()
            var vocals = [Float](repeating: 0, count: samples.count)
            var background = [Float](repeating: 0, count: samples.count)
            for (name, stem) in result.stems {
                let s = stem.channelMajorSamples
                let n = min(stem.frameCount, frames)
                let ch = stem.channels
                for i in 0 ..< n {
                    for c in 0 ..< 2 {
                        let v = s[min(c, ch - 1) * stem.frameCount + i]
                        if name == "vocals" { vocals[2 * i + c] += v } else { background[2 * i + c] += v }
                    }
                }
            }
            return (vocals, background)
        case .roformer(let sep):
            var planar = [Float](repeating: 0, count: samples.count)
            for i in 0 ..< frames {
                planar[i] = samples[2 * i]
                planar[frames + i] = samples[2 * i + 1]
            }
            let input = MLXArray(planar, [1, 2, frames])
            let out: MLXArray = try await withTaskCancellationHandler {
                try await sep.separate(samples: input)
            } onCancel: {
                sep.cancel()
            }
            eval(out)
            let v = out.asType(.float32).asArray(Float.self) // [1, 2, frames], planar
            var vocals = [Float](repeating: 0, count: samples.count)
            var background = [Float](repeating: 0, count: samples.count)
            let n = min(v.count / 2, frames)
            for i in 0 ..< n {
                for c in 0 ..< 2 {
                    let x = v[c * (v.count / 2) + i]
                    vocals[2 * i + c] = x
                    background[2 * i + c] = samples[2 * i + c] - x
                }
            }
            onProgress(1)
            return (vocals, background)
        }
    }
}

extension Analyzer {
    /// Model families of separation and diarization.
    static let m4Families = ["htdemucs", "mel_roformer", "sortformer"]

    func stemModel(_ model: String, path: String?, job: String, offline: Bool) async throws -> StemModel {
        let dir = try modelDirectory(model, path: path, offline: offline)
        let (m, _) = try await store.acquire(model, task: "separate", job: job, as: StemModel.self, sampleRate: { $0.sampleRate },
                                             estimate: weightBytes(dir)) {
            guard let dir else { throw EngineError.notDownloaded(model: model, missing: ["model files"]) }
            let lower = model.lowercased()
            if lower.contains("roformer") {
                let config: RoFormerConfiguration = lower.contains("zfturbo") ? .zfturboVocalsV1 : .kimVocal2
                let net = MelRoFormer(config: config)
                try WeightLoader.loadWeights(into: net, from: dir.appendingPathComponent("model.safetensors"))
                let sep = RoFormerSeparator(model: net, configuration: config)
                MLX.Memory.cacheLimit = 256 * 1024 * 1024 // it sets its own; the engine keeps the cap small
                return StemModel(.roformer(sep))
            }
            // HTDemucs (iky1e/demucs-mlx: <name>.safetensors + <name>_config.json).
            let name = ["htdemucs_ft", "htdemucs_6s", "htdemucs"].first {
                FileManager.default.fileExists(atPath: dir.appendingPathComponent("\($0).safetensors").path)
            } ?? "htdemucs"
            return StemModel(.demucs(try DemucsSeparator(modelName: name, modelDirectory: dir)))
        }
        return m
    }

    func separate(_ p: SeparateParams, job: String, offline: Bool) async throws -> SeparateResult {
        guard !p.model.isEmpty else { throw EngineError.badRequest("Missing model") }
        let vocalsOut = p.vocals.path.isEmpty ? nil : try outputURL(p.vocals)
        let backgroundOut = p.background.path.isEmpty ? nil : try outputURL(p.background)
        guard vocalsOut != nil || backgroundOut != nil else { throw EngineError.badRequest("Neither stem has a path") }
        let media = try await openMedia(p.audio)
        guard media.probe.hasAudio else { throw EngineError.unsupportedMedia(media.url.path, "it has no audio") }
        let model = try await stemModel(p.model, path: p.modelPath, job: job, offline: offline)
        defer { store.release(p.model) }
        let rate = model.sampleRate
        let total = media.probe.duration
        let progress = JobProgress(out: out, job: job)
        progress.send("separating", force: true)
        let started = Date()

        let vw = try vocalsOut.map { try WAVWriter(url: $0.0, sampleRate: rate, channels: 2, bits: $0.1) }
        let bw = try backgroundOut.map { try WAVWriter(url: $0.0, sampleRate: rate, channels: 2, bits: $0.1) }
        do {
            let window = 60.0, fade = 2.0
            let fadeFrames = Int(fade * Double(rate))
            let windows = max(Int((total / window).rounded(.up)), 1)
            var tailV: [Float] = [], tailB: [Float] = []
            var frames: Int64 = 0
            for k in 0 ..< windows {
                try Task.checkCancellation()
                var samples: [Float] = []
                _ = try readAudio(media, sampleRate: rate, channels: 2, start: Double(k) * window,
                                  duration: k == windows - 1 ? 0 : window + fade) { samples.append(contentsOf: $0) }
                if samples.isEmpty { break }
                let done = Double(k), n = Double(windows)
                let (v, b) = try await model.separate(samples, shifts: p.shifts ?? 1, overlap: p.overlap ?? 0.25) { f in
                    progress.fraction = min((done + f) / n, 1)
                    progress.audioSeconds = min((done + f) * window, total)
                    progress.send("separating")
                }
                let windowFrames = samples.count / 2
                let bodyEnd = k == windows - 1 ? windowFrames : min(windowFrames, Int(window * Double(rate)))
                // The head crossfades with the previous window's tail.
                let head = min(tailV.count / 2, bodyEnd)
                func emit(_ w: WAVWriter?, _ x: [Float], _ tail: [Float]) throws {
                    guard let w else { return }
                    var out = [Float](repeating: 0, count: bodyEnd * 2)
                    for i in 0 ..< bodyEnd {
                        for c in 0 ..< 2 {
                            var s = x[2 * i + c]
                            if i < head {
                                let t = Float(i) / Float(max(head, 1))
                                s = t * s + (1 - t) * tail[2 * i + c]
                            }
                            out[2 * i + c] = s
                        }
                    }
                    try w.append(out[...])
                }
                try emit(vw, v, tailV)
                try emit(bw, b, tailB)
                frames += Int64(bodyEnd)
                tailV = bodyEnd < windowFrames ? Array(v[(bodyEnd * 2)...].prefix(fadeFrames * 2)) : []
                tailB = bodyEnd < windowFrames ? Array(b[(bodyEnd * 2)...].prefix(fadeFrames * 2)) : []
                MLX.Memory.clearCache()
            }
            progress.fraction = 1
            progress.send("writing", force: true)
            try vw?.finish()
            try bw?.finish()
            return SeparateResult(vocals: vocalsOut?.0.path, background: backgroundOut?.0.path, sampleRate: rate, channels: 2,
                                  frames: frames, audioSeconds: Double(frames) / Double(rate), model: p.model,
                                  seconds: Date().timeIntervalSince(started))
        } catch {
            vw?.cancel()
            bw?.cancel()
            throw error
        }
    }

    // MARK: diarize.run

    func diarize(_ p: DiarizeParams, job: String, offline: Bool) async throws -> DiarizeResult {
        guard !p.model.isEmpty else { throw EngineError.badRequest("Missing model") }
        let maxSpeakers = 4 // Sortformer 4spk
        if (p.speakers ?? 0) > maxSpeakers || (p.minSpeakers ?? 0) > maxSpeakers {
            throw RPCError(Code.unsupported, "The model tells at most \(maxSpeakers) speakers apart",
                           data: .object(["capability": .string("speakers"), "max": .int(Int64(maxSpeakers))]))
        }
        let dir = try modelDirectory(p.model, path: p.modelPath, offline: offline)
        let (model, _) = try await store.acquire(p.model, task: "diarize", job: job, as: SortformerModel.self, sampleRate: { _ in 16000 },
                                                 estimate: weightBytes(dir)) {
            if let dir { return try SortformerModel.fromModelDirectory(dir) }
            return try await SortformerModel.fromPretrained(p.model)
        }
        defer { store.release(p.model) }
        let samples = try await decodeMono(p.audio, sampleRate: 16000)
        let audioSeconds = Double(samples.count) / 16000
        let progress = JobProgress(out: out, job: job)
        progress.audioSeconds = audioSeconds
        progress.send("diarizing", force: true)
        let started = Date()
        let output = try await model.generate(audio: MLXArray(samples), sampleRate: 16000, threshold: 0.5, minDuration: 0.25, mergeGap: 0.5)
        try Task.checkCancellation()
        MLX.Memory.clearCache()
        var turns = output.segments.map { Turn(start: round3(Double($0.start)), end: round3(Double($0.end)), speaker: String($0.speaker)) }
        turns.sort { ($0.start, $0.end) < ($1.start, $1.end) }
        let speakers = Set(turns.map(\.speaker)).count
        progress.fraction = 1
        progress.send("diarizing", force: true)
        return DiarizeResult(turns: turns, speakers: speakers, model: p.model, audioSeconds: audioSeconds,
                             seconds: Date().timeIntervalSince(started))
    }
}
