import AVFoundation
import Foundation
import XCTest
@testable import AudioSmithStream

/// Code review 1103 F6: a recording's input rate changing A → the file's
/// rate → A (voice processing coming on and a restart) flushes each
/// converter and never reuses one.
final class CaptureResamplerTests: XCTestCase {
    private func sine(rate: Double, seconds: Double, phase: inout Double) -> AVAudioPCMBuffer {
        let format = AVAudioFormat(commonFormat: .pcmFormatFloat32, sampleRate: rate, channels: 1, interleaved: false)!
        let n = AVAudioFrameCount(rate * seconds)
        let b = AVAudioPCMBuffer(pcmFormat: format, frameCapacity: n)!
        b.frameLength = n
        for i in 0 ..< Int(n) {
            b.floatChannelData![0][i] = Float(0.5 * sin(phase))
            phase += 2 * .pi * 440 / rate
        }
        return b
    }

    private func frames(_ bs: [AVAudioPCMBuffer]) -> Int { bs.reduce(0) { $0 + Int($1.frameLength) } }

    func testRateChangesFlushAndPassThrough() {
        let file = AVAudioFormat(commonFormat: .pcmFormatFloat32, sampleRate: 24000, channels: 1, interleaved: false)!
        let r = CaptureResampler(format: file)
        var phase = 0.0
        var out: [AVAudioPCMBuffer] = []
        for _ in 0 ..< 10 { out += r.convert(sine(rate: 48000, seconds: 0.1, phase: &phase)) } // 1 s at 48 kHz
        let first = frames(out)
        let through = sine(rate: 24000, seconds: 0.5, phase: &phase)
        let mid = r.convert(through) // the 48 kHz converter's tail, then the buffer as is
        XCTAssertTrue(mid.last === through, "a buffer at the file's rate passes through")
        out += mid
        // All of the first second is out once its converter is flushed.
        XCTAssertEqual(first + frames(mid) - Int(through.frameLength), 24000, accuracy: 4)
        for _ in 0 ..< 10 { out += r.convert(sine(rate: 48000, seconds: 0.1, phase: &phase)) }
        out += r.convert(sine(rate: 24000, seconds: 0.01, phase: &phase)) // flushes the second converter
        XCTAssertEqual(frames(out), 24000 + 12000 + 24000 + 240, accuracy: 8)
    }

    func testResetDropsTheTail() {
        let file = AVAudioFormat(commonFormat: .pcmFormatFloat32, sampleRate: 16000, channels: 1, interleaved: false)!
        let r = CaptureResampler(format: file)
        var phase = 0.0
        _ = r.convert(sine(rate: 48000, seconds: 0.1, phase: &phase))
        r.reset()
        let next = r.convert(sine(rate: 16000, seconds: 0.1, phase: &phase))
        XCTAssertEqual(next.count, 1, "nothing of the dropped converter comes out")
    }
}
