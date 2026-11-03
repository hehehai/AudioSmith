import Foundation

/// Little-endian 16-bit PCM of samples in [-1, 1] (tts.chunk's "pcm16le").
func pcm16(_ samples: ArraySlice<Float>) -> Data {
    var data = Data(capacity: samples.count * 2)
    for s in samples {
        let v = Int16(max(-1, min(1, s)) * 32767)
        withUnsafeBytes(of: v.littleEndian) { data.append(contentsOf: $0) }
    }
    return data
}

/// Writes a WAV file of 16- or 24-bit PCM, or 32-bit float, while samples
/// arrive (recordings), into a temporary file beside the target that
/// finish() renames into place: the target never holds a partial file.
final class WAVWriter {
    let url: URL
    let sampleRate: Int
    let channels: Int
    let bits: Int
    private let tmp: URL
    private let handle: FileHandle
    private(set) var frames: Int64 = 0
    private var done = false

    init(url: URL, sampleRate: Int, channels: Int, bits: Int) throws {
        guard [16, 24, 32].contains(bits) else { throw EngineError.badRequest("Unsupported bits \(bits)") }
        self.url = url
        self.sampleRate = sampleRate
        self.channels = channels
        self.bits = bits
        let dir = url.deletingLastPathComponent()
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        tmp = dir.appendingPathComponent(".\(url.lastPathComponent).\(getpid()).part")
        guard FileManager.default.createFile(atPath: tmp.path, contents: Self.header(sampleRate: sampleRate, channels: channels, bits: bits, dataSize: 0)) else {
            throw CocoaError(.fileWriteUnknown, userInfo: [NSFilePathErrorKey: tmp.path])
        }
        handle = try FileHandle(forWritingTo: tmp)
        try handle.seekToEnd()
    }

    deinit {
        if !done { cancel() }
    }

    /// Appends interleaved samples in [-1, 1].
    func append(_ samples: ArraySlice<Float>) throws {
        var d = Data(capacity: samples.count * bits / 8)
        switch bits {
        case 32:
            for s in samples { withUnsafeBytes(of: s.bitPattern.littleEndian) { d.append(contentsOf: $0) } }
        case 24:
            for s in samples {
                let v = Int32(max(-1, min(1, s)) * 8_388_607)
                d.append(UInt8(truncatingIfNeeded: v))
                d.append(UInt8(truncatingIfNeeded: v >> 8))
                d.append(UInt8(truncatingIfNeeded: v >> 16))
            }
        default:
            d.append(pcm16(samples))
        }
        try handle.write(contentsOf: d)
        frames += Int64(samples.count / channels)
    }

    /// Drops the samples written so far (a recording's digital silence
    /// before voice processing came on).
    func rewind() throws {
        try handle.truncate(atOffset: 44)
        try handle.seekToEnd()
        frames = 0
    }

    /// Writes the header's sizes and renames the file into place.
    func finish() throws {
        done = true
        let dataSize = Int(frames) * channels * bits / 8
        try handle.seek(toOffset: 0)
        try handle.write(contentsOf: Self.header(sampleRate: sampleRate, channels: channels, bits: bits, dataSize: dataSize))
        try handle.close()
        guard rename(tmp.path, url.path) == 0 else {
            let err = String(cString: strerror(errno))
            try? FileManager.default.removeItem(at: tmp)
            throw RPCErrorWrap.rename(err)
        }
    }

    /// Discards the file.
    func cancel() {
        done = true
        try? handle.close()
        try? FileManager.default.removeItem(at: tmp)
    }

    private static func header(sampleRate: Int, channels: Int, bits: Int, dataSize: Int) -> Data {
        let bytesPerSample = bits / 8
        var d = Data(capacity: 44)
        func u32(_ v: Int) { withUnsafeBytes(of: UInt32(truncatingIfNeeded: v).littleEndian) { d.append(contentsOf: $0) } }
        func u16(_ v: Int) { withUnsafeBytes(of: UInt16(v).littleEndian) { d.append(contentsOf: $0) } }
        d.append(contentsOf: Array("RIFF".utf8))
        u32(36 + dataSize)
        d.append(contentsOf: Array("WAVEfmt ".utf8))
        u32(16)
        u16(bits == 32 ? 3 : 1) // IEEE float, or PCM
        u16(channels)
        u32(sampleRate)
        u32(sampleRate * channels * bytesPerSample)
        u16(channels * bytesPerSample)
        u16(bits)
        d.append(contentsOf: Array("data".utf8))
        u32(dataSize)
        return d
    }
}

/// Writes interleaved samples as a WAV file (see WAVWriter).
func writeWAV(_ samples: [Float], sampleRate: Int, channels: Int = 1, bits: Int, to url: URL) throws {
    let w = try WAVWriter(url: url, sampleRate: sampleRate, channels: channels, bits: bits)
    do {
        try w.append(samples[...])
        try w.finish()
    } catch {
        w.cancel()
        throw error
    }
}

enum RPCErrorWrap: Error, LocalizedError {
    case rename(String)
    var errorDescription: String? {
        switch self {
        case .rename(let e): "rename: \(e)"
        }
    }
}
