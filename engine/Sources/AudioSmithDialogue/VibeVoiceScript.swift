import Foundation

// VibeVoice's script (thread #166), without MLX so the tests can check it.
// The model reads a conversation as `Speaker N: text` lines and takes one
// reference clip per speaker, in the order the speakers first speak
// (mlx-audio-swift's VibeVoiceTTSPrompt; Microsoft's demo scripts):
//
//   - speakers are numbered by first appearance from 0, and the clips go
//     in that order: a speaker the app named but no turn uses gets none;
//   - a turn is one line: its line breaks become spaces;
//   - Chinese punctuation becomes English (，。 → , .; ；：、 → ,), and
//     quotes and book-title marks go: the model card's advice for stable
//     Chinese ("use English punctuation, preferably only commas and
//     periods").
//
// The model makes 7.5 frames a second; frameBudget caps a take at twice
// the text's expected length plus 10 s (as MOSS-TTSD's, thread #82), so a
// run that never ends still stops.

public struct VibeVoiceScript: Equatable {
    public static let maxSpeakers = 4
    public static let frameRate = 7.5
    /// The most frames one call makes (400 s).
    public static let maxFrames = 3000

    /// The model's input: one `Speaker N: text` line per turn.
    public let text: String
    /// The speakers' indices (into the caller's list) in order of first
    /// appearance: speaker N's reference is speakers[N]'s.
    public let speakers: [Int]
    /// Each turn's text as the model reads it.
    public let turns: [String]
    /// Expected seconds of speech (about 4 Chinese characters or 2.5 words
    /// a second).
    public let expectedSeconds: Double

    /// turns: (index of the turn's speaker in the caller's list, text).
    public init(turns: [(speaker: Int, text: String)]) {
        var order: [Int] = []
        var number: [Int: Int] = [:]
        var lines: [String] = []
        var said: [String] = []
        var seconds = 0.0
        for t in turns {
            if number[t.speaker] == nil {
                number[t.speaker] = order.count
                order.append(t.speaker)
            }
            let s = Self.normalize(t.text)
            said.append(s)
            lines.append("Speaker \(number[t.speaker]!): \(s)")
            seconds += Self.seconds(s)
        }
        text = lines.joined(separator: "\n")
        speakers = order
        self.turns = said
        expectedSeconds = max(seconds, 2)
    }

    /// The frames a call may make: twice the expected length plus 10 s.
    public var frameBudget: Int {
        min(Int(((expectedSeconds * 2) + 10) * Self.frameRate), Self.maxFrames)
    }

    static let replaced: [Character: String] = [
        "，": ",", "。": ".", "！": "!", "？": "?", "；": ",", "：": ",", "、": ",",
        "（": "(", "）": ")", "…": "...", "—": ",",
    ]
    static let removed: Set<Character> = Set("“”‘’「」『』《》【】\"")

    /// A turn's text as the model reads it: one line, English punctuation.
    public static func normalize(_ input: String) -> String {
        var out = ""
        for c in input {
            if removed.contains(c) { continue }
            if let r = replaced[c] {
                out += r
            } else if c.isNewline {
                out += " "
            } else {
                out.append(c)
            }
        }
        // ",," from "，，" or "——", "......" from "……", and runs of spaces.
        while out.contains(",,") { out = out.replacingOccurrences(of: ",,", with: ",") }
        while out.contains("......") { out = out.replacingOccurrences(of: "......", with: "...") }
        let words = out.split(whereSeparator: { $0 == " " || $0 == "\t" })
        return words.joined(separator: " ")
    }

    /// Expected seconds of s: 0.25 per Chinese character, 0.4 per word.
    static func seconds(_ s: String) -> Double {
        var units = 0.0
        for u in s.unicodeScalars where u.properties.isIdeographic { units += 0.25 }
        let words = s.split(whereSeparator: { $0.isWhitespace || $0.isPunctuation })
            .filter { $0.unicodeScalars.contains { !$0.properties.isIdeographic && $0.properties.isAlphabetic } }
        return units + Double(words.count) * 0.4
    }
}
