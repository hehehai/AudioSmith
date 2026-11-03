import Foundation

// enhance.run in bounded memory (code review 1102, performance M5): the
// input used to be decoded whole, a result buffer of the same size
// allocated beside it and written at the end (2 h of 48 kHz mono: about
// 2.8 GB next to the gpu worker's models). OverlapStream takes the
// samples as they are decoded and hands the model the same windows the
// whole-track loop did (`piece` samples, each overlapping the one before
// by `overlap`), mixes and crossfades them the same way, and emits each
// window's finished samples at once, so the output is the same, sample
// for sample, and at most a window and a bit is held.

/// Windows a mono stream for a model and crossfades the results.
public struct OverlapStream {
    /// Samples per window, and how many each overlaps the one before.
    public let piece: Int
    public let overlap: Int
    /// The model's output mixed over the input (1: the model's only).
    public let strength: Float

    /// Samples read and not yet given to the model; it starts at the
    /// current window's first sample.
    private var buffer: [Float] = []
    /// The previous window's last `overlap` results, which the current
    /// window's head crossfades with (empty for the first window).
    private var tail: [Float] = []
    /// Samples emitted so far.
    public private(set) var emitted = 0

    public init(piece: Int, overlap: Int, strength: Float) {
        precondition(piece > 0 && overlap >= 0 && overlap < piece, "bad windows")
        self.piece = piece
        self.overlap = overlap
        self.strength = strength
    }

    /// Takes samples as read. A window goes to process (the model: input
    /// window → output, which may be shorter) once more samples than it
    /// holds are known, so that a window is known not to be the last;
    /// emit gets its finished samples.
    public mutating func push(_ samples: [Float], process: ([Float]) throws -> [Float], emit: ([Float]) throws -> Void) rethrows {
        buffer.append(contentsOf: samples)
        while buffer.count > piece {
            let window = Array(buffer[0 ..< piece])
            let out = try run(window, process: process)
            try emit(Array(out[0 ..< piece - overlap]))
            emitted += piece - overlap
            tail = Array(out[(piece - overlap)...])
            buffer.removeFirst(piece - overlap)
        }
    }

    /// Processes what is left as the last window (nothing when no sample
    /// came) and emits all of it.
    public mutating func finish(process: ([Float]) throws -> [Float], emit: ([Float]) throws -> Void) rethrows {
        guard !buffer.isEmpty else { return }
        let out = try run(buffer, process: process)
        try emit(out)
        emitted += out.count
        buffer = []
        tail = []
    }

    /// One window: the model's output mixed with the input by strength;
    /// the head crossfades with the previous window's tail. A position
    /// the model gave nothing for keeps the previous window's result (0
    /// past it), as the whole-track loop left its buffer.
    private func run(_ window: [Float], process: ([Float]) throws -> [Float]) rethrows -> [Float] {
        let e = try process(window)
        var out = [Float](repeating: 0, count: window.count)
        for i in 0 ..< min(tail.count, window.count) { out[i] = tail[i] }
        for i in 0 ..< min(e.count, window.count) {
            var v = strength * e[i] + (1 - strength) * window[i]
            if !tail.isEmpty, i < overlap {
                let w = Float(i) / Float(overlap)
                v = w * v + (1 - w) * out[i]
            }
            out[i] = v
        }
        return out
    }
}
