import Foundation
import XCTest
@testable import AudioSmithStream

/// enhance.run's streaming windows (code review 1102 M5) against the
/// whole-track loop they replace, kept here as the reference: the same
/// windows reach the model, and the same samples come out, however the
/// input arrives in pieces.
final class OverlapStreamTests: XCTestCase {
    // The reference: Analysis.swift's enhance before streaming.
    private func reference(_ samples: [Float], piece: Int, overlap: Int, strength: Float,
                           process: ([Float]) -> [Float]) -> (out: [Float], windows: [[Float]]) {
        var windows: [[Float]] = []
        var result = [Float](repeating: 0, count: samples.count)
        var start = 0
        while start < samples.count {
            let end = min(start + piece, samples.count)
            let input = Array(samples[start ..< end])
            windows.append(input)
            let e = process(input)
            for i in 0 ..< min(e.count, end - start) {
                let j = start + i
                var v = strength * e[i] + (1 - strength) * samples[j]
                if start > 0, i < overlap {
                    let w = Float(i) / Float(overlap)
                    v = w * v + (1 - w) * result[j]
                }
                result[j] = v
            }
            if end == samples.count { break }
            start = end - overlap
        }
        return (result, windows)
    }

    private func streamed(_ samples: [Float], chunk: Int, piece: Int, overlap: Int, strength: Float,
                          process: ([Float]) -> [Float]) -> (out: [Float], windows: [[Float]], emitted: Int) {
        var s = OverlapStream(piece: piece, overlap: overlap, strength: strength)
        var out: [Float] = []
        var windows: [[Float]] = []
        let run = { (x: [Float]) -> [Float] in
            windows.append(x)
            return process(x)
        }
        var i = 0
        while i < samples.count {
            let n = min(chunk, samples.count - i)
            s.push(Array(samples[i ..< i + n]), process: run) { out += $0 }
            i += n
        }
        s.finish(process: run) { out += $0 }
        return (out, windows, s.emitted)
    }

    private func signal(_ n: Int) -> [Float] {
        (0 ..< n).map { Float(sin(Double($0) * 0.37) * 0.8 + cos(Double($0) * 0.011) * 0.1) }
    }

    /// A "model" whose output depends on the whole window (its mean), so
    /// different windows would give different samples.
    private func model(_ x: [Float]) -> [Float] {
        let mean = x.reduce(0, +) / Float(max(x.count, 1))
        return x.enumerated().map { 0.6 * $0.element - mean + Float($0.offset % 7) * 0.001 }
    }

    func testSameAsWholeTrack() {
        let piece = 100, overlap = 10
        let lengths = [1, 9, 10, 11, 99, 100, 101, 189, 190, 191, 200, 280, 281, 1000, 1234]
        for n in lengths {
            let x = signal(n)
            for strength: Float in [1, 0.7, 0] {
                let ref = reference(x, piece: piece, overlap: overlap, strength: strength, process: model)
                for chunk in [1, 7, 100, 101, 4096] {
                    let got = streamed(x, chunk: chunk, piece: piece, overlap: overlap, strength: strength, process: model)
                    XCTAssertEqual(got.windows, ref.windows, "n \(n) chunk \(chunk): windows")
                    XCTAssertEqual(got.out, ref.out, "n \(n) chunk \(chunk) strength \(strength)")
                    XCTAssertEqual(got.emitted, n)
                }
            }
        }
    }

    /// A model that returns fewer samples than it got: the gaps are as
    /// the whole-track loop left them (the previous window's result in
    /// the overlap, else 0).
    func testShortModelOutput() {
        let short = { (x: [Float]) -> [Float] in Array(self.model(x).prefix(max(x.count - 13, 3))) }
        for n in [50, 150, 333] {
            let x = signal(n)
            let ref = reference(x, piece: 60, overlap: 20, strength: 1, process: short)
            let got = streamed(x, chunk: 17, piece: 60, overlap: 20, strength: 1, process: short)
            XCTAssertEqual(got.out, ref.out, "n \(n)")
        }
    }

    func testNoOverlapAndEmpty() {
        let x = signal(250)
        let ref = reference(x, piece: 100, overlap: 0, strength: 0.5, process: model)
        XCTAssertEqual(streamed(x, chunk: 33, piece: 100, overlap: 0, strength: 0.5, process: model).out, ref.out)
        let none = streamed([], chunk: 1, piece: 100, overlap: 10, strength: 1, process: model)
        XCTAssertTrue(none.out.isEmpty && none.windows.isEmpty)
    }

    /// It holds at most a window and a read buffer, whatever the length.
    func testBoundedBuffer() {
        var s = OverlapStream(piece: 1000, overlap: 50, strength: 1)
        var peak = 0
        for _ in 0 ..< 500 {
            s.push([Float](repeating: 0.1, count: 333), process: { x in
                peak = max(peak, x.count)
                return x
            }, emit: { _ in })
        }
        XCTAssertEqual(peak, 1000)
        XCTAssertLessThanOrEqual(500 * 333 - s.emitted, 1000, "held back more than a window")
    }

    func testMemoryPressureLevels() {
        XCTAssertEqual(MemoryPressure(kernelLevel: 1), .normal)
        XCTAssertEqual(MemoryPressure(kernelLevel: 2), .warning)
        XCTAssertEqual(MemoryPressure(kernelLevel: 4), .critical)
        XCTAssertEqual(MemoryPressure(kernelLevel: 0), .normal)
        XCTAssertFalse(MemoryPressure.normal.unloadsIdle)
        XCTAssertTrue(MemoryPressure.warning.unloadsIdle && !MemoryPressure.warning.refusesLoads)
        XCTAssertTrue(MemoryPressure.critical.unloadsIdle && MemoryPressure.critical.refusesLoads)
        // The kernel's level is the truth; the event's without it; a forced one over both.
        XCTAssertEqual(MemoryPressure.effective(event: .critical, kernel: .normal, forced: nil), .normal)
        XCTAssertEqual(MemoryPressure.effective(event: .critical, kernel: nil, forced: nil), .critical)
        XCTAssertEqual(MemoryPressure.effective(event: .normal, kernel: .normal, forced: "critical"), .critical)
        XCTAssertEqual(MemoryPressure.effective(event: .warning, kernel: nil, forced: "bogus"), .warning)
        XCTAssertNotNil(MemoryPressure.current(), "the kernel reports its level on macOS")
    }
}
