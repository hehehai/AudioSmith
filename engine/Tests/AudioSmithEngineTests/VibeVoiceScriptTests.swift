import Foundation
import XCTest
@testable import AudioSmithDialogue

/// VibeVoice's script (thread #166): speakers by first appearance, one
/// line per turn that mlx-audio-swift's VibeVoiceTTSPrompt.parse reads as
/// that speaker's, English punctuation, and the frame budget.
final class VibeVoiceScriptTests: XCTestCase {
    /// VibeVoiceTTSPrompt.parse's line pattern (upstream fe2c182).
    let line = try! NSRegularExpression(pattern: #"^Speaker\s+(\d+)\s*:\s*(.*)$"#, options: .caseInsensitive)

    func parsed(_ text: String) -> [(Int, String)] {
        text.split(separator: "\n").map { l in
            let s = String(l)
            guard let m = line.firstMatch(in: s, range: NSRange(s.startIndex..., in: s)),
                  let a = Range(m.range(at: 1), in: s), let b = Range(m.range(at: 2), in: s) else { return (-1, s) }
            return (Int(s[a])!, String(s[b]))
        }
    }

    func testSpeakersInOrderOfFirstAppearance() {
        // The caller's speakers 2, 0, 2, 1: speaker 3 (index 3) says nothing.
        let s = VibeVoiceScript(turns: [(2, "Hello there."), (0, "Hi!"), (2, "How are you?"), (1, "Fine.")])
        XCTAssertEqual(s.speakers, [2, 0, 1])
        let lines = parsed(s.text)
        XCTAssertEqual(lines.map(\.0), [0, 1, 0, 2])
        XCTAssertEqual(lines.map(\.1), ["Hello there.", "Hi!", "How are you?", "Fine."])
        XCTAssertEqual(s.turns, ["Hello there.", "Hi!", "How are you?", "Fine."])
    }

    func testOneLinePerTurn() {
        let s = VibeVoiceScript(turns: [(0, "First line\nsecond line\r\n  third")])
        XCTAssertEqual(s.text, "Speaker 0: First line second line third")
        // A turn that itself says "Speaker 1:" stays inside its line.
        let t = VibeVoiceScript(turns: [(0, "Read this:\nSpeaker 1: not a turn")])
        XCTAssertEqual(parsed(t.text).count, 1)
    }

    func testChinesePunctuation() {
        XCTAssertEqual(VibeVoiceScript.normalize("你好，世界。真的吗？“是的”！《书名》：好；一、二"),
                       "你好,世界.真的吗?是的!书名,好,一,二")
        XCTAssertEqual(VibeVoiceScript.normalize("等等——好吧……"), "等等,好吧...")
        XCTAssertEqual(VibeVoiceScript.normalize("Plain English, unchanged."), "Plain English, unchanged.")
    }

    func testFrameBudget() {
        // 20 characters ≈ 5 s → (5 × 2 + 10) × 7.5 = 150 frames.
        let zh = VibeVoiceScript(turns: [(0, String(repeating: "好", count: 20))])
        XCTAssertEqual(zh.expectedSeconds, 5, accuracy: 1e-9)
        XCTAssertEqual(zh.frameBudget, 150)
        // 10 words ≈ 4 s → 135 frames; the floor is 2 s.
        let en = VibeVoiceScript(turns: [(0, "one two three four five six seven eight nine ten")])
        XCTAssertEqual(en.frameBudget, 135)
        XCTAssertEqual(VibeVoiceScript(turns: [(0, "Hi.")]).frameBudget, 105)
        let long = VibeVoiceScript(turns: [(0, String(repeating: "好", count: 4000))])
        XCTAssertEqual(long.frameBudget, VibeVoiceScript.maxFrames)
    }
}
