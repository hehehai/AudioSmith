import AudioSmithProtocol
import AudioSmithStream
import AVFoundation
import CoreAudio
import Foundation

// The listen worker's microphone: capture.devices, capture.start (a job
// that records until capture.stop), with an input meter about 20 times a
// second. CaptureRun.swift keeps the microphone running (configuration
// changes, voice processing); CaptureSession.swift writes and meters.

// MARK: Devices (Core Audio)

private func property<T>(_ object: AudioObjectID, _ selector: AudioObjectPropertySelector,
                         scope: AudioObjectPropertyScope = kAudioObjectPropertyScopeGlobal, _ value: inout T) -> Bool {
    var address = AudioObjectPropertyAddress(mSelector: selector, mScope: scope, mElement: kAudioObjectPropertyElementMain)
    var size = UInt32(MemoryLayout<T>.size)
    return AudioObjectGetPropertyData(object, &address, 0, nil, &size, &value) == noErr
}

private func stringProperty(_ object: AudioObjectID, _ selector: AudioObjectPropertySelector) -> String? {
    var value: Unmanaged<CFString>?
    guard property(object, selector, &value), let s = value?.takeRetainedValue() else { return nil }
    return s as String
}

private func inputChannelCount(_ device: AudioObjectID) -> Int {
    var address = AudioObjectPropertyAddress(mSelector: kAudioDevicePropertyStreamConfiguration,
                                             mScope: kAudioDevicePropertyScopeInput, mElement: kAudioObjectPropertyElementMain)
    var size: UInt32 = 0
    guard AudioObjectGetPropertyDataSize(device, &address, 0, nil, &size) == noErr, size > 0 else { return 0 }
    let raw = UnsafeMutableRawPointer.allocate(byteCount: Int(size), alignment: MemoryLayout<AudioBufferList>.alignment)
    defer { raw.deallocate() }
    guard AudioObjectGetPropertyData(device, &address, 0, nil, &size, raw) == noErr else { return 0 }
    let list = UnsafeMutableAudioBufferListPointer(raw.assumingMemoryBound(to: AudioBufferList.self))
    return list.reduce(0) { $0 + Int($1.mNumberChannels) }
}

private func transportName(_ t: UInt32) -> String? {
    switch t {
    case kAudioDeviceTransportTypeBuiltIn: "builtin"
    case kAudioDeviceTransportTypeUSB: "usb"
    case kAudioDeviceTransportTypeBluetooth, kAudioDeviceTransportTypeBluetoothLE: "bluetooth"
    case kAudioDeviceTransportTypeAggregate, kAudioDeviceTransportTypeAutoAggregate: "aggregate"
    case kAudioDeviceTransportTypeVirtual: "virtual"
    default: nil
    }
}

/// Core Audio ids of the devices with inputs, and the default input.
private func inputDeviceIDs() -> (all: [AudioObjectID], defaultID: AudioObjectID) {
    var address = AudioObjectPropertyAddress(mSelector: kAudioHardwarePropertyDevices,
                                             mScope: kAudioObjectPropertyScopeGlobal, mElement: kAudioObjectPropertyElementMain)
    var size: UInt32 = 0
    var ids: [AudioObjectID] = []
    if AudioObjectGetPropertyDataSize(AudioObjectID(kAudioObjectSystemObject), &address, 0, nil, &size) == noErr {
        ids = [AudioObjectID](repeating: 0, count: Int(size) / MemoryLayout<AudioObjectID>.size)
        AudioObjectGetPropertyData(AudioObjectID(kAudioObjectSystemObject), &address, 0, nil, &size, &ids)
    }
    var def = AudioObjectID(0)
    _ = property(AudioObjectID(kAudioObjectSystemObject), kAudioHardwarePropertyDefaultInputDevice, &def)
    return (ids.filter { inputChannelCount($0) > 0 }, def)
}

func captureDevices() -> [CaptureDevice] {
    let (ids, def) = inputDeviceIDs()
    var out: [CaptureDevice] = []
    for id in ids {
        // Voice processing's private aggregates (another app's, or ours)
        // are not inputs to choose.
        guard let uid = stringProperty(id, kAudioDevicePropertyDeviceUID), !isVoiceProcessingAggregate(uid: uid) else { continue }
        let name = stringProperty(id, kAudioObjectPropertyName) ?? uid
        var rate = Float64(0)
        _ = property(id, kAudioDevicePropertyNominalSampleRate, &rate)
        var transport = UInt32(0)
        _ = property(id, kAudioDevicePropertyTransportType, &transport)
        out.append(CaptureDevice(id: uid, name: name, channels: inputChannelCount(id), sampleRate: Int(rate),
                                 default: id == def ? true : nil, transport: transportName(transport)))
    }
    return out.sorted { ($0.default ?? false) && !($1.default ?? false) }
}

private func deviceID(uid: String) -> AudioObjectID? {
    inputDeviceIDs().all.first { stringProperty($0, kAudioDevicePropertyDeviceUID) == uid }
}

func deviceUID(_ id: AudioObjectID) -> String? {
    stringProperty(id, kAudioDevicePropertyDeviceUID)
}

/// The UIDs of an aggregate device's sub-devices; nil when id is not an
/// aggregate (voice processing's CADefaultDeviceAggregate is one).
func aggregateSubDevices(_ id: AudioObjectID) -> [String]? {
    var value: Unmanaged<CFArray>?
    guard property(id, kAudioAggregateDevicePropertyFullSubDeviceList, &value),
          let list = value?.takeRetainedValue() as? [String] else { return nil }
    return list
}

private func transport(_ id: AudioObjectID) -> String? {
    var t = UInt32(0)
    _ = property(id, kAudioDevicePropertyTransportType, &t)
    return transportName(t)
}

// MARK: Recording

/// Runs the listen worker's recordings (one at a time: concurrency 1).
final class Recorder: @unchecked Sendable {
    let out: Writer
    private let lock = NSLock()
    private var stops: [String: () -> Void] = [:]

    init(out: Writer) {
        self.out = out
    }

    /// Ends the recording of job; false when none runs. The job answers
    /// before this returns true (Server waits for it).
    func stop(_ job: String) -> Bool {
        lock.lock()
        let stop = stops.removeValue(forKey: job)
        lock.unlock()
        stop?()
        return stop != nil
    }

    func record(_ p: CaptureParams, job: String) async throws -> CaptureResult {
        let (url, bits) = try outputURL(p.output)
        let fileChannels = p.channels ?? 1
        guard fileChannels == 1 || fileChannels == 2 else { throw EngineError.badRequest("channels must be 1 or 2") }

        // Microphone access: the first time macOS asks the user.
        switch AVCaptureDevice.authorizationStatus(for: .audio) {
        case .authorized:
            break
        case .notDetermined:
            if !(await AVCaptureDevice.requestAccess(for: .audio)) { throw EngineError.permissionDenied("microphone") }
        default:
            throw EngineError.permissionDenied("microphone")
        }
        try Task.checkCancellation()

        // The device.
        let (ids, def) = inputDeviceIDs()
        let device: AudioObjectID
        if let uid = nonEmpty(p.device) {
            guard let id = deviceID(uid: uid) else { throw EngineError.deviceUnavailable(uid) }
            device = id
        } else {
            guard def != 0, ids.contains(def) else { throw EngineError.deviceUnavailable("default input") }
            device = def
        }
        let uid = stringProperty(device, kAudioDevicePropertyDeviceUID) ?? p.device ?? ""
        let deviceChannels = inputChannelCount(device)
        var picks = (p.inputChannels ?? []).map { $0 - 1 }
        if picks.isEmpty { picks = fileChannels == 2 && deviceChannels >= 2 ? [0, 1] : [0] }
        for c in picks where c < 0 || c >= deviceChannels {
            throw EngineError.badRequest("The input has \(deviceChannels) channel(s); there is no channel \(c + 1)")
        }

        let name = stringProperty(device, kAudioObjectPropertyName) ?? uid
        let deviceRate: Int = {
            var rate = Float64(0)
            _ = property(device, kAudioDevicePropertyNominalSampleRate, &rate)
            return Int(rate)
        }()
        let fileRate = (p.sampleRate ?? 0) > 0 ? p.sampleRate! : (deviceRate > 0 ? deviceRate : 48000)
        let writer = try WAVWriter(url: url, sampleRate: fileRate, channels: fileChannels, bits: bits)
        let session = CaptureSession(out: out, job: job, writer: writer, fileChannels: fileChannels,
                                     fileRate: fileRate, maxSeconds: p.maxSeconds ?? 0)
        // Loopback/virtual and aggregate inputs, picked channels and a
        // Bluetooth headset's warm-up change what the watch does (code
        // review 1103 F1).
        let policy = CapturePolicy.forInput(transport: transport(device), pickedChannels: !(p.inputChannels ?? []).isEmpty)
        let run = CaptureRun(session: session, device: device, uid: uid, name: name, picks: picks, aec: p.aec ?? false,
                             policy: policy, isDefaultInput: device == def)
        do {
            try run.start()
        } catch {
            log("capture \(job): \(name) did not start\(p.aec == true ? " with voice processing" : ""): \(error)")
            run.finish()
            writer.cancel()
            throw EngineError.deviceUnavailable(uid)
        }
        out.notify(Method.jobProgress, ProgressParams(job: job, stage: "recording"))

        var reason: String = await withTaskCancellationHandler {
            await withCheckedContinuation { (k: CheckedContinuation<String, Never>) in
                session.setOnEnd { k.resume(returning: $0) }
                lock.withLock { stops[job] = { session.end(CaptureStopped) } }
            }
        } onCancel: {
            session.end("cancelled")
        }
        lock.withLock { _ = stops.removeValue(forKey: job) }
        run.finish()
        session.drain()
        if reason == "cancelled" || Task.isCancelled {
            writer.cancel()
            throw CancellationError()
        }
        if let error = session.writeError {
            writer.cancel()
            throw RPCError(Code.internal, "Could not write \(url.path): \(error.localizedDescription)")
        }
        let state = run.state
        if reason == CaptureNoInput {
            // Given up: what came before is kept when it had sound.
            guard state.everHeard else {
                writer.cancel()
                throw EngineError.noInput(device: uid, name: name, reason: (state.failure ?? .noSound).rawValue, aec: state.aec)
            }
            reason = CaptureInputStopped
        } else if reason != CaptureDeviceLost, run.silentThroughout {
            writer.cancel()
            throw EngineError.noInput(device: uid, name: name, reason: CaptureRecovery.Reason.noSound.rawValue, aec: state.aec)
        }
        try writer.finish()
        return CaptureResult(output: url.path, sampleRate: fileRate, channels: fileChannels, frames: writer.frames,
                             audioSeconds: Double(writer.frames) / Double(fileRate), device: uid,
                             peak: session.peakDB, clipped: session.clipped > 0 ? session.clipped : nil, reason: reason,
                             aec: state.aec ? true : nil, aecAuto: state.auto != nil ? true : nil, aecReason: state.auto?.rawValue,
                             aecFallback: session.fallback, aecMono: run.voiceProcessingMono(fileChannels: fileChannels) ? true : nil)
    }
}
