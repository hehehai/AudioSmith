import Foundation
import XCTest
@testable import AudioSmithText

/// VoxCPM2's "(instruction)text" prompt (thread #165).
final class VoxCPMTextTests: XCTestCase {
    func testInstruction() {
        XCTAssertNil(voxCPMInstruction(voice: nil, instruct: "  "))
        XCTAssertEqual(voxCPMInstruction(voice: "A warm young woman", instruct: nil), "A warm young woman")
        XCTAssertEqual(voxCPMInstruction(voice: "温柔女声", instruct: "稍快，开心"), "温柔女声, 稍快，开心")
        // Parentheses would end the instruction early.
        XCTAssertEqual(voxCPMInstruction(voice: nil, instruct: "calm (almost whispering)"), "calm, almost whispering")
        XCTAssertEqual(voxCPMInstruction(voice: "（低沉）男声", instruct: nil), "低沉, 男声")
        XCTAssertEqual(voxCPMInstruction(voice: "calm", instruct: "calm"), "calm")
    }

    func testPrompt() {
        XCTAssertEqual(voxCPMPrompt(text: "Hello.", instruction: nil), "Hello.")
        XCTAssertEqual(voxCPMPrompt(text: "你好。", instruction: "开心"), "(开心)你好。")
    }
}
