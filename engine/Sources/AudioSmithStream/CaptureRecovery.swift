import Foundation

// What a capture.start recording does when its microphone stops or stays
// silent (pure logic, so the tests check it; the engine's CaptureRun.swift
// drives it with the tap's buffers, AVAudioEngineConfigurationChange and a
// timer).
//
// macOS, another app or a virtual audio driver may reconfigure the audio
// devices while AudioSmith records: AVAudioEngine then stops without an
// error and the take would be silent. A device that runs but delivers only
// digital silence (zeros) is the other symptom; voice processing (echo
// cancellation) cures both on many Macs, since its aggregate device holds
// up where the raw device does not.

/// One recording's watch over its microphone.
public struct CaptureRecovery {
    /// Why the microphone was given up on, or voice processing turned on.
    public enum Reason: String, Equatable, Sendable {
        /// No buffers, or only digital silence, since it started.
        case noSound
        /// It kept stopping as the audio devices changed.
        case deviceChanges
    }

    public enum Action: Equatable, Sendable {
        case none
        /// Start the same engine again in place (device chosen again, tap
        /// installed again with the input's format now).
        case restart
        /// Start again with voice processing: once per recording, and
        /// only when it was off.
        case voiceProcessing(Reason)
        /// Give up: the recording ends.
        case fail(Reason)
    }

    /// Without voice processing, a start must bring a sound (a sample
    /// above `silence`) within `quiet` seconds; with it, within
    /// `quietAEC` (its noise suppression may hold a quiet room near zero).
    public var quiet = 1.5
    public var quietAEC = 4.0
    /// A sample at least this loud is a sound; below, digital silence.
    public static let silence: Float = 1e-6
    /// Once sound came, buffers stopping for `stall` seconds count as a
    /// stop (the engine stopped without a configuration change notice).
    public var stall = 1.0
    /// A configuration change is harmless when buffers still came within
    /// `delivering` seconds when it is checked.
    public var delivering = 0.3
    /// Restarts within `window` seconds: past `limit`, voice processing
    /// (once), then giving up.
    public var window = 30.0
    public var limit = 6
    /// The check after a configuration change waits `base` seconds
    /// (notifications coalesce meanwhile), twice as long for each recent
    /// restart, at most `maxDelay`.
    public var base = 0.4
    public var maxDelay = 5.0

    /// Voice processing is on now.
    public private(set) var aec: Bool
    /// Why the watch turned voice processing on; nil while it has not.
    public private(set) var autoAEC: Reason?
    /// A sound came since the current mode started; since the recording did.
    public private(set) var heard = false
    public private(set) var everHeard = false
    /// Restarts since the recording started.
    public private(set) var restarts = 0
    /// The recording has been given up on.
    public private(set) var failed = false

    private var recent: [Double] = []
    private var startedAt: Double
    private var lastBuffer: Double?
    private var checkAt: Double?

    /// A watch over a microphone started at `now` (seconds), with voice
    /// processing or not.
    public init(aec: Bool, now: Double) {
        self.aec = aec
        startedAt = now
    }

    /// The tap delivered a buffer at `now`; silent when every sample was
    /// below `silence`.
    public mutating func buffer(now: Double, silent: Bool) {
        lastBuffer = now
        if !silent {
            heard = true
            everHeard = true
        }
    }

    /// The engine's configuration changed at `now`: a check is due later
    /// (one for a burst of notifications).
    public mutating func configChanged(now: Double) {
        guard checkAt == nil, !failed else { return }
        prune(now)
        checkAt = now + min(maxDelay, base * pow(2, Double(recent.count)))
    }

    /// Whether a configuration change check is waiting.
    public var checkPending: Bool { checkAt != nil }

    /// What to do at `now` (the timer calls it a few times a second).
    public mutating func tick(now: Double) -> Action {
        guard !failed else { return .none }
        if let due = checkAt {
            if now < due { return .none }
            checkAt = nil
            if let last = lastBuffer, now - last < delivering { return .none }
            return attempt(now)
        }
        if !heard {
            return now - startedAt >= (aec ? quietAEC : quiet) ? noSound() : .none
        }
        if now - (lastBuffer ?? startedAt) >= stall { return attempt(now) }
        return .none
    }

    /// The engine was started again at `now` after .restart (whether that
    /// worked or not: a failed start shows as no buffers).
    public mutating func restarted(now: Double) {
        startedAt = now
        lastBuffer = nil
    }

    /// The microphone started again with voice processing at `now`, after
    /// .voiceProcessing: a fresh start with a new window.
    public mutating func voiceProcessingStarted(now: Double) {
        aec = true
        heard = false
        startedAt = now
        lastBuffer = nil
        checkAt = nil
        recent = []
    }

    /// Whether a recording that ran `seconds` and stops now is only
    /// digital silence: an error rather than a silent file.
    public func silentThroughout(seconds: Double) -> Bool {
        !everHeard && seconds >= quiet
    }

    private mutating func noSound() -> Action {
        if !aec, autoAEC == nil {
            autoAEC = .noSound
            return .voiceProcessing(.noSound)
        }
        failed = true
        return .fail(.noSound)
    }

    private mutating func attempt(_ now: Double) -> Action {
        prune(now)
        if recent.count < limit {
            recent.append(now)
            restarts += 1
            return .restart
        }
        if !aec, autoAEC == nil {
            autoAEC = .deviceChanges
            return .voiceProcessing(.deviceChanges)
        }
        failed = true
        return .fail(.deviceChanges)
    }

    private mutating func prune(_ now: Double) {
        recent.removeAll { now - $0 >= window }
    }
}

/// Process-private aggregate devices Core Audio makes for voice processing
/// (CADefaultDeviceAggregate-<pid>-<n>): they come and go with the apps
/// using them and are not inputs to choose.
public func isVoiceProcessingAggregate(uid: String) -> Bool {
    uid.hasPrefix("CADefaultDeviceAggregate-")
}
