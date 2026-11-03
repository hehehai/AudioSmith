import AudioSmithStream
import AVFoundation
import CoreAudio
import Foundation

// A recording's microphone, kept running: AVAudioEngine on the chosen
// input, with voice processing (echo cancellation) when asked, watched by
// CaptureRecovery. When a configuration change stops the engine (macOS,
// another app or a virtual driver reconfiguring the devices) it starts the
// same engine again in place; when the input brings no sound or keeps
// stopping it turns voice processing on once, then gives up.

/// AVAudioEngine on one input device.
final class CaptureMic {
    let device: AudioObjectID
    let aec: Bool
    private let engine = AVAudioEngine()
    private var observer: NSObjectProtocol?
    private var tapped = false
    private var tap: ((AVAudioPCMBuffer) -> Void)?
    private(set) var formatText = "none"

    init(device: AudioObjectID, aec: Bool) {
        self.device = device
        self.aec = aec
    }

    /// Starts the input: tap gets its buffers (on the audio thread),
    /// onChange the engine's configuration changes (on any thread).
    func start(tap: @escaping (AVAudioPCMBuffer) -> Void, onChange: @escaping () -> Void) throws {
        self.tap = tap
        try selectDevice()
        if aec {
            // The output side of the graph must exist before voice
            // processing is turned on, or it fails (-10875) and the input
            // has no channels. Other apps' audio is ducked as little as
            // the system allows.
            _ = engine.mainMixerNode
            _ = engine.outputNode
            try engine.inputNode.setVoiceProcessingEnabled(true)
            engine.inputNode.voiceProcessingOtherAudioDuckingConfiguration =
                AVAudioVoiceProcessingOtherAudioDuckingConfiguration(enableAdvancedDucking: false, duckingLevel: .min)
        }
        try installTap()
        observer = NotificationCenter.default.addObserver(forName: .AVAudioEngineConfigurationChange, object: engine, queue: nil) { _ in
            onChange()
        }
        engine.prepare()
        do {
            try engine.start()
        } catch {
            stop()
            throw error
        }
    }

    /// Starts the same engine again after a configuration change (Apple's
    /// guidance): the device chosen again, the tap installed again with
    /// the input's format now. The device stays open, and voice processing
    /// keeps its aggregate. Returns the format before and after.
    func restart() throws -> (before: String, after: String) {
        let before = formatText
        if engine.isRunning { engine.stop() }
        if !aec { try selectDevice() } // voice processing manages its own device
        try installTap()
        engine.prepare()
        try engine.start()
        return (before, formatText)
    }

    func stop() {
        if let observer { NotificationCenter.default.removeObserver(observer) }
        observer = nil
        engine.stop()
        if tapped { engine.inputNode.removeTap(onBus: 0) }
        tapped = false
    }

    private func selectDevice() throws {
        guard let unit = engine.inputNode.audioUnit else { return }
        var current = AudioObjectID(0)
        var size = UInt32(MemoryLayout<AudioObjectID>.size)
        if AudioUnitGetProperty(unit, kAudioOutputUnitProperty_CurrentDevice, kAudioUnitScope_Global, 0, &current, &size) == noErr,
           current == device { return }
        var id = device
        let status = AudioUnitSetProperty(unit, kAudioOutputUnitProperty_CurrentDevice, kAudioUnitScope_Global, 0,
                                          &id, UInt32(MemoryLayout<AudioObjectID>.size))
        if status != noErr { throw CaptureMicError(message: "selecting the input failed (\(status))") }
    }

    private func installTap() throws {
        let input = engine.inputNode
        if tapped { input.removeTap(onBus: 0) }
        tapped = false
        let format = input.outputFormat(forBus: 0)
        guard format.sampleRate > 0, format.channelCount > 0, let tap else {
            throw CaptureMicError(message: "the input has no format")
        }
        input.installTap(onBus: 0, bufferSize: 1024, format: format) { buffer, _ in tap(buffer) }
        tapped = true
        formatText = "\(Int(format.sampleRate)) Hz × \(format.channelCount)"
    }
}

struct CaptureMicError: Error {
    let message: String
}

/// One recording's microphone and its watch. The control queue starts,
/// restarts and stops the engine; the tap and the configuration notices
/// only update the watch.
final class CaptureRun: @unchecked Sendable {
    let session: CaptureSession
    let device: AudioObjectID
    let uid: String
    let name: String
    let picks: [Int]
    let startedAt = ProcessInfo.processInfo.systemUptime
    private let q = DispatchQueue(label: "audiosmith.capture.control")
    private let lock = NSLock()
    private var recovery: CaptureRecovery
    private var mic: CaptureMic?
    private var timer: DispatchSourceTimer?
    private var failure: CaptureRecovery.Reason?

    init(session: CaptureSession, device: AudioObjectID, uid: String, name: String, picks: [Int], aec: Bool) {
        self.session = session
        self.device = device
        self.uid = uid
        self.name = name
        self.picks = picks
        recovery = CaptureRecovery(aec: aec, now: startedAt)
    }

    private var now: Double { ProcessInfo.processInfo.systemUptime }

    /// Starts the microphone and its watch; throws when the input does not start.
    func start() throws {
        let m = CaptureMic(device: device, aec: recovery.aec)
        try startMic(m)
        mic = m
        let t = DispatchSource.makeTimerSource(queue: q)
        t.schedule(deadline: .now() + 0.25, repeating: 0.25)
        t.setEventHandler { [weak self] in self?.tick() }
        t.resume()
        timer = t
    }

    /// Stops the watch and the microphone.
    func finish() {
        q.sync {
            timer?.cancel()
            timer = nil
            mic?.stop()
            mic = nil
        }
    }

    /// What the recording ends with: voice processing on, why the engine
    /// turned it on, why it gave up, whether any sound came.
    var state: (aec: Bool, auto: CaptureRecovery.Reason?, failure: CaptureRecovery.Reason?, everHeard: Bool) {
        lock.withLock { (recovery.aec, recovery.autoAEC, failure, recovery.everHeard) }
    }

    /// Whether a recording stopping now was digital silence throughout.
    var silentThroughout: Bool {
        let seconds = now - startedAt
        return lock.withLock { recovery.silentThroughout(seconds: seconds) }
    }

    private func startMic(_ m: CaptureMic) throws {
        let picks = m.aec ? [0] : self.picks // voice processing's signal is its first channel
        try m.start(tap: { [weak self] buffer in
            guard let self else { return }
            let peak = self.session.receive(buffer, picks: picks)
            let t = self.now
            self.lock.withLock { self.recovery.buffer(now: t, silent: peak < CaptureRecovery.silence) }
        }, onChange: { [weak self] in
            guard let self else { return }
            let t = self.now
            self.lock.withLock { self.recovery.configChanged(now: t) }
        })
    }

    private func tick() {
        guard !session.isEnded, let m = mic else { return }
        if !isAlive(device) {
            session.end(CaptureDeviceLost)
            return
        }
        let t = now
        let action = lock.withLock { recovery.tick(now: t) }
        switch action {
        case .none:
            break
        case .restart:
            let n = lock.withLock {
                recovery.restarted(now: t)
                return recovery.restarts
            }
            do {
                let (before, after) = try m.restart()
                if n <= 3 || n % 10 == 0 { log("capture \(session.job): \(name) stopped; started again (\(n)), \(before) → \(after)") }
            } catch {
                log("capture \(session.job): \(name) did not start again (\(n)): \(error)")
            }
        case .voiceProcessing(let reason):
            voiceProcessing(reason, stopping: m)
        case .fail(let reason):
            log("capture \(session.job): \(name) gave no sound (\(reason.rawValue)); giving up")
            lock.withLock { failure = reason }
            session.end(CaptureNoInput)
        }
    }

    /// Starts the microphone again with voice processing, once.
    private func voiceProcessing(_ reason: CaptureRecovery.Reason, stopping old: CaptureMic) {
        log("capture \(session.job): \(name) \(reason == .noSound ? "brings no sound" : "keeps stopping"); turning voice processing on")
        old.stop()
        mic = nil
        let heard = lock.withLock { recovery.everHeard }
        if !heard { session.rewind() } // only silence so far
        lock.withLock { recovery.voiceProcessingStarted(now: now) }
        let m = CaptureMic(device: device, aec: true)
        do {
            try startMic(m)
            mic = m
            session.voiceProcessingOn(reason.rawValue)
        } catch {
            log("capture \(session.job): \(name) did not start with voice processing: \(error)")
            lock.withLock { failure = reason }
            session.end(CaptureNoInput)
        }
    }
}

func isAlive(_ id: AudioObjectID) -> Bool {
    var alive = UInt32(0)
    var address = AudioObjectPropertyAddress(mSelector: kAudioDevicePropertyDeviceIsAlive,
                                             mScope: kAudioObjectPropertyScopeGlobal, mElement: kAudioObjectPropertyElementMain)
    var size = UInt32(MemoryLayout<UInt32>.size)
    return AudioObjectGetPropertyData(id, &address, 0, nil, &size, &alive) == noErr && alive != 0
}
