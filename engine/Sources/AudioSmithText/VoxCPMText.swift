import Foundation

// VoxCPM2's text prompt (thread #165). The model reads a voice description
// or a delivery instruction from parentheses that lead the text:
//
//     (A young woman, gentle and sweet voice)Hello…      voice design
//     (稍快，开心)今天天气真好。                             a clone, restyled
//
// Pure functions without MLX, so the engine's tests check them.

/// The instruction VoxCPM2 is given: the voice field (a design
/// description) and the delivery instruction, joined; nil when both are
/// empty. Parentheses inside become commas, so the instruction ends where
/// the model expects it to.
public func voxCPMInstruction(voice: String?, instruct: String?) -> String? {
    var parts: [String] = []
    for s in [voice, instruct] {
        guard var t = s?.trimmingCharacters(in: .whitespacesAndNewlines), !t.isEmpty else { continue }
        for p in ["(", ")", "（", "）"] { t = t.replacingOccurrences(of: p, with: ", ") }
        t = t.split(separator: ",").map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }.joined(separator: ", ")
        if !t.isEmpty, !parts.contains(t) { parts.append(t) }
    }
    return parts.isEmpty ? nil : parts.joined(separator: ", ")
}

/// The text with the instruction leading it in parentheses; the text alone
/// without one.
public func voxCPMPrompt(text: String, instruction: String?) -> String {
    guard let i = instruction, !i.isEmpty else { return text }
    return "(\(i))\(text)"
}
