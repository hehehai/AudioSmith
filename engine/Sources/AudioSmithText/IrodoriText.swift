import Foundation

// Irodori-TTS (thread #170): a Japanese-only model that takes its voice
// as a Japanese caption ("落ち着いた女性の声") and its non-verbal sounds and
// delivery as emoji in the text (Aratako's EMOJI_ANNOTATIONS.md: 🤭 a
// chuckle, 😮‍💨 a sigh, …). AudioSmith's scripts carry non-verbal tags
// ("[laughter]"); these become the emoji the model was trained on.
//
// Pure functions without MLX, so the engine's tests check them.

/// The emoji each AudioSmith non-verbal tag becomes (markup's
/// DefaultNonVerbal); tags not listed are left out.
public let irodoriEmoji: [String: String] = [
    "laughter": "🤭",
    "sigh": "😮‍💨",
    "breath": "🌬️",
    "confirmation-en": "👌",
    "dissatisfaction-hnn": "😠",
    "question-en": "🤔", "question-ah": "🤔", "question-oh": "🤔", "question-ei": "🤔", "question-yi": "🤔",
    "surprise-ah": "😲", "surprise-oh": "😲", "surprise-wa": "😲", "surprise-yo": "😲",
]

/// The text Irodori reads: each "[tag]" becomes its emoji (unknown ones are
/// dropped: the model would read the letters), and the spaces the script
/// put around a tag go with it (Japanese has none between words).
public func irodoriText(_ text: String) -> String {
    var out = ""
    var rest = Substring(text)
    while let open = rest.firstIndex(of: "[") {
        guard let close = rest[open...].firstIndex(of: "]") else { break }
        let tag = rest[rest.index(after: open) ..< close].trimmingCharacters(in: .whitespaces).lowercased()
        out += rest[..<open].trimmingTrailingSpaces()
        out += irodoriEmoji[tag] ?? ""
        rest = rest[rest.index(after: close)...].drop(while: { $0 == " " || $0 == "\u{3000}" })
    }
    out += rest
    return out.trimmingCharacters(in: .whitespacesAndNewlines)
}

/// The caption Irodori is given. A designed voice is its description (or
/// the model's default caption when nil). A clone without an instruction
/// gets an empty caption, which the model masks out: its default caption
/// ("落ち着いた自然な声で…") would pull the clone away from the reference.
public func irodoriCaption(voice: String?, cloning: Bool) -> String? {
    let v = voice?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
    if !v.isEmpty { return v }
    return cloning ? "" : nil
}

extension Substring {
    fileprivate func trimmingTrailingSpaces() -> Substring {
        var s = self
        while let last = s.last, last == " " || last == "\u{3000}" { s = s.dropLast() }
        return s
    }
}
