import AVFoundation
import Foundation

// A recording's resampling to its file's rate (capture.start). The input's
// rate may change while it runs: a restart after a configuration change,
// voice processing coming on (48 kHz → 16/24 kHz → 48 kHz). Each change
// flushes the converter of the old rate (what it still held is written)
// and drops it; a stretch at the file's rate passes through. Code review
// 1103 F6: the converter was kept across a pass-through stretch and used
// again later, splicing its stale tail in, and a new rate dropped the old
// converter's tail.

/// Converts a recording's buffers (deinterleaved Float32, the file's
/// channel count, any rate) to the file's rate.
public final class CaptureResampler {
    public let format: AVAudioFormat
    private var converter: AVAudioConverter?
    private var converterRate: Double = 0

    /// format: the file's (deinterleaved Float32).
    public init(format: AVAudioFormat) {
        self.format = format
    }

    /// The buffers to write for buffer, in order: what the previous rate's
    /// converter still held when the rate changed, then buffer at the
    /// file's rate.
    public func convert(_ buffer: AVAudioPCMBuffer) -> [AVAudioPCMBuffer] {
        let rate = buffer.format.sampleRate
        var out: [AVAudioPCMBuffer] = []
        if let old = converter, rate == format.sampleRate || converterRate != rate {
            if let tail = run(old, rate: converterRate, input: nil) { out.append(tail) }
            converter = nil
            converterRate = 0
        }
        if rate == format.sampleRate { return out + [buffer] }
        if converter == nil {
            converter = AVAudioConverter(from: buffer.format, to: format)
            converterRate = rate
        }
        if let converter, let converted = run(converter, rate: rate, input: buffer) { out.append(converted) }
        return out
    }

    /// Drops the converter and what it holds (the recording starts over).
    public func reset() {
        converter = nil
        converterRate = 0
    }

    /// Converts input (nil: flushes the converter's tail, the end of its
    /// stream) to the file's format.
    private func run(_ converter: AVAudioConverter, rate: Double, input: AVAudioPCMBuffer?) -> AVAudioPCMBuffer? {
        let frames = Double(input?.frameLength ?? 0)
        let capacity = AVAudioFrameCount((frames + 256) * format.sampleRate / max(rate, 1) + 32)
        guard let converted = AVAudioPCMBuffer(pcmFormat: format, frameCapacity: capacity) else { return nil }
        var given = false
        var error: NSError?
        converter.convert(to: converted, error: &error) { _, status in
            guard let input else {
                status.pointee = .endOfStream
                return nil
            }
            if given {
                status.pointee = .noDataNow
                return nil
            }
            given = true
            status.pointee = .haveData
            return input
        }
        return converted.frameLength > 0 ? converted : nil
    }
}
