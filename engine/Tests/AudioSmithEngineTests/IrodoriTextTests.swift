import AudioSmithText
import XCTest

final class IrodoriTextTests: XCTestCase {
    func testTagsBecomeEmoji() {
        XCTAssertEqual(irodoriText("そうなんだ [laughter] 本当に？"), "そうなんだ🤭本当に？")
        XCTAssertEqual(irodoriText("はぁ [sigh] 疲れた。"), "はぁ😮‍💨疲れた。")
        XCTAssertEqual(irodoriText("[Surprise-Wa] すごい！"), "😲すごい！")
    }

    func testUnknownTagsAreDropped() {
        XCTAssertEqual(irodoriText("こんにちは [cough] 皆さん。"), "こんにちは皆さん。")
        XCTAssertEqual(irodoriText("閉じない [タグ"), "閉じない [タグ")
    }

    func testEmojiTypedByTheUserStay() {
        XCTAssertEqual(irodoriText("👂ねえ、聞いて。"), "👂ねえ、聞いて。")
    }

    func testCaption() {
        XCTAssertNil(irodoriCaption(voice: nil, cloning: false))
        XCTAssertEqual(irodoriCaption(voice: "  ", cloning: true), "")
        XCTAssertEqual(irodoriCaption(voice: "低い男性の声", cloning: true), "低い男性の声")
    }
}
