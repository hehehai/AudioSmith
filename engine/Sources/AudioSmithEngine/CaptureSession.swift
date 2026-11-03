import AudioSmithProtocol
import AudioSmithStream
import AVFoundation
import Foundation

// A recording's file and meter: what the microphone's tap delivers is
// picked, resampled to the file's rate, written and metered on a serial
// queue off the audio thread. The input's format may change while it runs
// (a restart after a configuration change, voice processing coming on).

let CaptureStopped = "stopped"
let CaptureDeviceLost = "deviceLost"
let CaptureMaxSeconds = "maxSeconds"
/// The microphone stopped for good after sound came: what was recorded is kept.
let CaptureInputStopped = "inputStopped"
/// Internal: the watch gave up (CaptureRun.failure says why).
let CaptureNoInput = "noInput"

final class CaptureSession: @unchecked Sendable {
    let out: Writer
    let job: String
    let writer: WAVWriter
    let fileChannels: Int
    let fileRate: Int
    let maxFrames: Int64
    private let queue = DispatchQueue(label: "audiosmith.capture")
    private let lock = NSLock()
    private let resampler: CaptureResampler // on the queue

    private var onEnd: ((String) -> Void)?
    private var ended = false
    private var endReason = ""
    private var aecReason: String? // under lock: the engine turned voice processing on
    private var aecFallback: String? // under lock: voice processing was asked for, the input runs without
    var writeError: Error?
    var clipped = 0
    var peakDB: Double = -120
    // Meter state, per ~50 ms of audio.
    private var meterFrames = 0
    private var meterSum: Double = 0
    private var meterPeak: Float = 0
    private var meterClipped = false
    private var written: Int64 = 0

    init(out: Writer, job: String, writer: WAVWriter, fileChannels: Int, fileRate: Int, maxSeconds: Double) {
        self.out = out
        self.job = job
        self.writer = writer
        self.fileChannels = fileChannels
        self.fileRate = fileRate
        maxFrames = maxSeconds > 0 ? Int64(maxSeconds * Double(fileRate)) : 0
        resampler = CaptureResampler(format: AVAudioFormat(commonFormat: .pcmFormatFloat32, sampleRate: Double(fileRate),
                                                           channels: AVAudioChannelCount(fileChannels), interleaved: false)!)
    }

    /// The tap: picks the channels (0-based) into a buffer of the file's
    /// channel count at the input's rate, hands it to the queue, and
    /// returns its loudest sample.
    func receive(_ buffer: AVAudioPCMBuffer, picks: [Int]) -> Float {
        guard let data = buffer.floatChannelData else { return 0 }
        let n = Int(buffer.frameLength)
        let channels = Int(buffer.format.channelCount)
        guard n > 0, channels > 0,
              let format = AVAudioFormat(commonFormat: .pcmFormatFloat32, sampleRate: buffer.format.sampleRate,
                                         channels: AVAudioChannelCount(fileChannels), interleaved: false),
              let picked = AVAudioPCMBuffer(pcmFormat: format, frameCapacity: AVAudioFrameCount(n)),
              let dst = picked.floatChannelData else { return 0 }
        picked.frameLength = AVAudioFrameCount(n)
        let interleavedSource = buffer.format.isInterleaved
        func sample(_ ch: Int, _ i: Int) -> Float {
            let c = min(ch, channels - 1)
            return interleavedSource ? data[0][i * channels + c] : data[c][i]
        }
        var peak: Float = 0
        if fileChannels == 1 {
            let scale = 1 / Float(picks.count)
            for i in 0 ..< n {
                var v: Float = 0
                for c in picks { v += sample(c, i) }
                dst[0][i] = v * scale
                peak = max(peak, abs(dst[0][i]))
            }
        } else {
            let left = picks[0], right = picks.count > 1 ? picks[1] : picks[0]
            for i in 0 ..< n {
                dst[0][i] = sample(left, i)
                dst[1][i] = sample(right, i)
                peak = max(peak, abs(dst[0][i]), abs(dst[1][i]))
            }
        }
        queue.async { self.process(picked) }
        return peak
    }

    /// The engine turned voice processing on (why): the levels say so.
    func voiceProcessingOn(_ reason: String) {
        lock.withLock { aecReason = reason }
    }

    /// Voice processing was asked for but the input runs without it
    /// (why: "failed", "device"): the levels say so.
    func voiceProcessingFellBack(_ why: String) {
        lock.withLock { aecFallback = why }
    }

    var fallback: String? { lock.withLock { aecFallback } }

    /// Drops what was written (digital silence) and the converter's state:
    /// the recording starts over in the same file.
    func rewind() {
        queue.sync {
            do {
                try writer.rewind()
            } catch {
                writeError = error
            }
            written = 0
            meterFrames = 0
            meterSum = 0
            meterPeak = 0
            resampler.reset()
        }
    }

    private func process(_ buffer: AVAudioPCMBuffer) {
        if isEnded { return }
        for b in resampler.convert(buffer) { write(b) }
    }

    private func write(_ outBuffer: AVAudioPCMBuffer) {
        if isEnded { return }
        guard let ch = outBuffer.floatChannelData else { return }
        var n = Int(outBuffer.frameLength)
        if maxFrames > 0 { n = min(n, Int(maxFrames - written)) }
        if n <= 0 { return }
        var interleaved = [Float](repeating: 0, count: n * fileChannels)
        for i in 0 ..< n {
            for c in 0 ..< fileChannels {
                let v = ch[c][i]
                interleaved[i * fileChannels + c] = v
                let a = abs(v)
                meterSum += Double(v * v)
                if a > meterPeak { meterPeak = a }
                if a >= 0.999 {
                    clipped += 1
                    meterClipped = true
                }
            }
        }
        do {
            try writer.append(interleaved[...])
        } catch {
            writeError = error
            end(CaptureStopped)
            return
        }
        written += Int64(n)
        meterFrames += n
        if meterFrames >= fileRate / 20 {
            let rms = sqrt(meterSum / Double(meterFrames * fileChannels))
            let peak = Double(meterPeak)
            peakDB = max(peakDB, db(peak))
            let (why, fellBack) = lock.withLock { (aecReason, aecFallback) }
            out.notify(Method.captureLevel, LevelParams(job: job, seconds: round3(Double(written) / Double(fileRate)),
                                                        rms: round3(db(rms)), peak: round3(db(peak)),
                                                        clipped: meterClipped ? true : nil,
                                                        aecAuto: why != nil ? true : nil, aecReason: why,
                                                        aecFallback: fellBack))
            meterFrames = 0
            meterSum = 0
            meterPeak = 0
            meterClipped = false
        }
        if maxFrames > 0, written >= maxFrames { end(CaptureMaxSeconds) }
    }

    var isEnded: Bool { lock.withLock { ended } }

    /// Sets what runs when the recording ends (at once if it already has).
    func setOnEnd(_ fn: @escaping (String) -> Void) {
        let reason: String? = lock.withLock {
            if ended { return endReason }
            onEnd = fn
            return nil
        }
        if let reason { fn(reason) }
    }

    func end(_ reason: String) {
        let call: ((String) -> Void)? = lock.withLock {
            if ended { return nil }
            ended = true
            endReason = reason
            let f = onEnd
            onEnd = nil
            return f
        }
        call?(reason)
    }

    /// Waits for the buffers already queued.
    func drain() {
        queue.sync {}
        peakDB = max(peakDB, db(Double(meterPeak)))
    }
}

private func db(_ x: Double) -> Double {
    x <= 1e-6 ? -120 : max(-120, 20 * log10(x))
}
