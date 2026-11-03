import Foundation
import XCTest
@testable import AudioSmithStream

/// The microphone watch of capture.start (thread #153): restarts after
/// configuration changes with backoff, voice processing once when there is
/// no sound or the restarts run out, then giving up.
final class CaptureRecoveryTests: XCTestCase {
    /// Feeds buffers every 20 ms from `from` to `to` (seconds), ticking
    /// every 0.25 s; returns the first action that is not .none.
    private func run(_ r: inout CaptureRecovery, from: Double, to: Double, silent: Bool, buffers: Bool = true) -> (CaptureRecovery.Action, Double)? {
        var t = from
        var nextTick = from + 0.25
        while t <= to {
            if buffers { r.buffer(now: t, silent: silent) }
            if t >= nextTick {
                nextTick += 0.25
                let a = r.tick(now: t)
                if a != .none { return (a, t) }
            }
            t += 0.02
        }
        return nil
    }

    func testSoundKeepsRecording() {
        var r = CaptureRecovery(aec: false, now: 0)
        XCTAssertNil(run(&r, from: 0, to: 20, silent: false))
        XCTAssertTrue(r.everHeard)
        XCTAssertNil(r.autoAEC)
    }

    func testDigitalSilenceTurnsVoiceProcessingOnThenFails() throws {
        var r = CaptureRecovery(aec: false, now: 0)
        let (a, t) = try XCTUnwrap(run(&r, from: 0, to: 5, silent: true))
        XCTAssertEqual(a, .voiceProcessing(.noSound))
        XCTAssertEqual(t, 1.5, accuracy: 0.3)
        XCTAssertEqual(r.autoAEC, .noSound)
        r.voiceProcessingStarted(now: t)
        XCTAssertTrue(r.aec)
        let (b, t2) = try XCTUnwrap(run(&r, from: t, to: t + 10, silent: true))
        XCTAssertEqual(b, .fail(.noSound))
        XCTAssertEqual(t2 - t, r.quietAEC, accuracy: 0.3)
        XCTAssertTrue(r.failed)
        XCTAssertEqual(r.tick(now: t2 + 1), .none)
    }

    func testNoBuffersCountsAsNoSound() throws {
        var r = CaptureRecovery(aec: false, now: 0)
        let (a, _) = try XCTUnwrap(run(&r, from: 0, to: 5, silent: false, buffers: false))
        XCTAssertEqual(a, .voiceProcessing(.noSound))
    }

    func testVoiceProcessingBringsSound() throws {
        var r = CaptureRecovery(aec: false, now: 0)
        let (_, t) = try XCTUnwrap(run(&r, from: 0, to: 5, silent: true))
        r.voiceProcessingStarted(now: t)
        XCTAssertNil(run(&r, from: t, to: t + 30, silent: false))
        XCTAssertEqual(r.autoAEC, .noSound)
        XCTAssertTrue(r.everHeard)
    }

    func testAECFromTheStartNeverFallsBack() throws {
        var r = CaptureRecovery(aec: true, now: 0)
        let (a, t) = try XCTUnwrap(run(&r, from: 0, to: 10, silent: true))
        XCTAssertEqual(a, .fail(.noSound))
        XCTAssertEqual(t, r.quietAEC, accuracy: 0.3)
        XCTAssertNil(r.autoAEC)
    }

    func testConfigChangeHarmlessWhileDelivering() {
        var r = CaptureRecovery(aec: false, now: 0)
        XCTAssertNil(run(&r, from: 0, to: 2, silent: false))
        r.configChanged(now: 2)
        XCTAssertTrue(r.checkPending)
        XCTAssertNil(run(&r, from: 2, to: 4, silent: false))
        XCTAssertFalse(r.checkPending)
        XCTAssertEqual(r.restarts, 0)
    }

    func testConfigChangeStopRestartsAfterDelay() {
        var r = CaptureRecovery(aec: false, now: 0)
        _ = run(&r, from: 0, to: 2, silent: false)
        r.configChanged(now: 2)
        r.configChanged(now: 2.1) // coalesced
        XCTAssertEqual(r.tick(now: 2.3), .none) // the check waits base (0.4 s)
        XCTAssertEqual(r.tick(now: 2.45), .restart)
        XCTAssertEqual(r.restarts, 1)
        r.restarted(now: 2.45)
        XCTAssertNil(run(&r, from: 2.5, to: 10, silent: false))
    }

    func testBackoffDoublesWithRecentRestarts() {
        var r = CaptureRecovery(aec: false, now: 0)
        r.buffer(now: 0.1, silent: false)
        var t = 1.0
        var delays: [Double] = []
        for _ in 0 ..< 4 {
            r.configChanged(now: t)
            var due = t
            while r.tick(now: due) != .restart { due += 0.05 }
            delays.append(due - t)
            r.restarted(now: due)
            r.buffer(now: due + 0.1, silent: false)
            t = due + 0.2
        }
        for (i, d) in delays.enumerated() {
            XCTAssertEqual(d, 0.4 * pow(2, Double(i)), accuracy: 0.06, "restart \(i + 1)")
        }
    }

    func testRestartsRunOutThenVoiceProcessingThenGiveUp() {
        var r = CaptureRecovery(aec: false, now: 0)
        r.buffer(now: 0.1, silent: false)
        var t = 1.0
        var actions: [CaptureRecovery.Action] = []
        for _ in 0 ..< 20 {
            r.configChanged(now: t)
            var a = CaptureRecovery.Action.none
            while a == .none { t += 0.05; a = r.tick(now: t) }
            actions.append(a)
            switch a {
            case .restart: r.restarted(now: t)
            case .voiceProcessing: r.voiceProcessingStarted(now: t)
            default: break
            }
            if case .fail = a { break }
            r.buffer(now: t + 0.05, silent: false) // it delivers a little, then stops again
            t += 0.1
        }
        let restarts = actions.prefix { $0 == .restart }.count
        XCTAssertEqual(restarts, r.limit)
        XCTAssertEqual(actions[restarts], .voiceProcessing(.deviceChanges))
        XCTAssertEqual(actions.last, .fail(.deviceChanges))
        XCTAssertEqual(actions.count, 2 * r.limit + 2)
        XCTAssertEqual(r.autoAEC, .deviceChanges)
        XCTAssertTrue(r.everHeard)
    }

    func testOldRestartsLeaveTheWindow() {
        var r = CaptureRecovery(aec: false, now: 0)
        r.buffer(now: 0.1, silent: false)
        var t = 1.0
        for _ in 0 ..< 30 {
            r.configChanged(now: t)
            var a = CaptureRecovery.Action.none
            while a == .none { t += 0.05; a = r.tick(now: t) }
            XCTAssertEqual(a, .restart)
            r.restarted(now: t)
            r.buffer(now: t + 0.05, silent: false)
            t += r.window / Double(r.limit - 1) // slower than the limit allows
        }
        XCTAssertNil(r.autoAEC)
    }

    func testStallWithoutNoticeRestarts() throws {
        var r = CaptureRecovery(aec: false, now: 0)
        _ = run(&r, from: 0, to: 3, silent: false)
        let (a, t) = try XCTUnwrap(run(&r, from: 3, to: 6, silent: false, buffers: false))
        XCTAssertEqual(a, .restart)
        XCTAssertEqual(t - 3, r.stall, accuracy: 0.3)
    }

    func testSilentThroughout() {
        var r = CaptureRecovery(aec: false, now: 0)
        XCTAssertFalse(r.silentThroughout(seconds: 1)) // too short to tell
        XCTAssertTrue(r.silentThroughout(seconds: 2))
        r.buffer(now: 0.5, silent: false)
        XCTAssertFalse(r.silentThroughout(seconds: 2))
    }

    // Code review 1103 F1: a loopback/virtual or aggregate input delivers
    // exact zeros while nothing plays. That is its signal: no voice
    // processing, no giving up, the silent take kept.
    func testVirtualInputZerosAreItsSignal() {
        var r = CaptureRecovery(aec: false, now: 0, policy: .forInput(transport: "virtual", pickedChannels: false))
        XCTAssertNil(run(&r, from: 0, to: 30, silent: true))
        XCTAssertNil(r.autoAEC)
        XCTAssertTrue(r.everHeard)
        XCTAssertFalse(r.silentThroughout(seconds: 30))
        let agg = CapturePolicy.forInput(transport: "aggregate", pickedChannels: false)
        XCTAssertFalse(agg.autoAEC)
        XCTAssertTrue(agg.zerosAreSignal)
    }

    // ... and only no buffers at all is no sound: then it gives up, without
    // voice processing, after as long as it would have taken with it.
    func testVirtualInputWithoutBuffersGivesUp() throws {
        var r = CaptureRecovery(aec: false, now: 0, policy: .forInput(transport: "virtual", pickedChannels: false))
        let (a, t) = try XCTUnwrap(run(&r, from: 0, to: 10, silent: true, buffers: false))
        XCTAssertEqual(a, .fail(.noSound))
        XCTAssertEqual(t, r.quiet + r.quietAEC, accuracy: 0.3)
        XCTAssertNil(r.autoAEC)
        XCTAssertFalse(r.everHeard)
        XCTAssertTrue(r.silentThroughout(seconds: 10))
    }

    // Restarts running out on a virtual input give up without voice
    // processing; sound came, so the take is kept (inputStopped).
    func testVirtualInputRestartsRunOutWithoutVoiceProcessing() {
        var r = CaptureRecovery(aec: false, now: 0, policy: .forInput(transport: "aggregate", pickedChannels: false))
        r.buffer(now: 0.1, silent: true)
        var t = 1.0
        var actions: [CaptureRecovery.Action] = []
        for _ in 0 ..< 20 {
            r.configChanged(now: t)
            var a = CaptureRecovery.Action.none
            while a == .none { t += 0.05; a = r.tick(now: t) }
            actions.append(a)
            if case .restart = a { r.restarted(now: t) } else { break }
            r.buffer(now: t + 0.05, silent: true)
            t += 0.1
        }
        XCTAssertEqual(actions.last, .fail(.deviceChanges))
        XCTAssertEqual(actions.count, r.limit + 1)
        XCTAssertNil(r.autoAEC)
        XCTAssertTrue(r.everHeard)
    }

    // Picked channels: voice processing would give its first channel
    // only, so the watch never turns it on; digital silence gives up.
    func testPickedChannelsNeverTurnVoiceProcessingOn() throws {
        var r = CaptureRecovery(aec: false, now: 0, policy: .forInput(transport: "usb", pickedChannels: true))
        let (a, t) = try XCTUnwrap(run(&r, from: 0, to: 10, silent: true))
        XCTAssertEqual(a, .fail(.noSound))
        XCTAssertEqual(t, r.quiet + r.quietAEC, accuracy: 0.3)
        XCTAssertNil(r.autoAEC)
    }

    // A Bluetooth headset's warm-up (zeros while it switches profile) does
    // not count: voice processing only after 3 s.
    func testBluetoothWarmUp() throws {
        var r = CaptureRecovery(aec: false, now: 0, policy: .forInput(transport: "bluetooth", pickedChannels: false))
        XCTAssertNil(run(&r, from: 0, to: 2.5, silent: true))
        var w = CaptureRecovery(aec: false, now: 0, policy: .forInput(transport: "bluetooth", pickedChannels: false))
        let (a, t) = try XCTUnwrap(run(&w, from: 0, to: 6, silent: true))
        XCTAssertEqual(a, .voiceProcessing(.noSound))
        XCTAssertEqual(t, 3.0, accuracy: 0.3)
        var s = CaptureRecovery(aec: false, now: 0, policy: .forInput(transport: "bluetooth", pickedChannels: false))
        XCTAssertNil(run(&s, from: 0, to: 2, silent: true))
        XCTAssertNil(run(&s, from: 2, to: 20, silent: false)) // the microphone came up
        XCTAssertFalse(s.silentThroughout(seconds: 2.5))
        let plain = CaptureRecovery(aec: false, now: 0)
        XCTAssertTrue(plain.silentThroughout(seconds: 2))
        let bt = CaptureRecovery(aec: false, now: 0, policy: .forInput(transport: "bluetooth", pickedChannels: false))
        XCTAssertFalse(bt.silentThroughout(seconds: 2))
    }

    // Code review 1103 F3: voice processing asked for but not starting on
    // the input: the recording goes on without it, and the watch never
    // turns it on again.
    func testVoiceProcessingUnavailable() throws {
        var r = CaptureRecovery(aec: true, now: 0)
        r.voiceProcessingUnavailable()
        XCTAssertFalse(r.aec)
        let (a, t) = try XCTUnwrap(run(&r, from: 0, to: 10, silent: true))
        XCTAssertEqual(a, .fail(.noSound))
        XCTAssertEqual(t, r.quiet + r.quietAEC, accuracy: 0.3)
        XCTAssertNil(r.autoAEC)
        var s = CaptureRecovery(aec: true, now: 0)
        s.voiceProcessingUnavailable()
        XCTAssertNil(run(&s, from: 0, to: 20, silent: false))
    }

    func testPolicies() {
        XCTAssertEqual(CapturePolicy.forInput(transport: "builtin", pickedChannels: false), CapturePolicy())
        XCTAssertEqual(CapturePolicy.forInput(transport: nil, pickedChannels: false), CapturePolicy())
        XCTAssertFalse(CapturePolicy.forInput(transport: "builtin", pickedChannels: true).autoAEC)
        XCTAssertEqual(CapturePolicy.forInput(transport: "bluetooth", pickedChannels: false).warmUp, 1.5)
    }

    func testVoiceProcessingAggregatesHidden() {
        XCTAssertTrue(isVoiceProcessingAggregate(uid: "CADefaultDeviceAggregate-1234-0"))
        XCTAssertFalse(isVoiceProcessingAggregate(uid: "BuiltInMicrophoneDevice"))
        XCTAssertFalse(isVoiceProcessingAggregate(uid: "AppleUSBAudioEngine:Focusrite:Scarlett 2i2 USB:1234:1"))
    }
}
