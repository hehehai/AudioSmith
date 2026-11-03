import AudioSmithProtocol
import AudioSmithStream
import Foundation

/// The gpu worker's watch over the system's memory pressure (code review
/// 1102 M1): at "warning" and "critical" the models no job uses are
/// unloaded and MLX's buffer cache returned (ModelStore.relieve); at
/// "critical" loads are refused (ModelStore.acquire). Each change is
/// logged and sent to the app (engine.memory), whose supervisor then
/// unloads the other workers' idle models and refreshes its view of this
/// one's.
final class MemoryWatch: @unchecked Sendable {
    private let source: DispatchSourceMemoryPressure
    private var signals: [DispatchSourceSignal] = []
    private let queue = DispatchQueue(label: "audiosmith.memory-pressure")

    init(store: ModelStore, out: Writer) {
        source = DispatchSource.makeMemoryPressureSource(eventMask: [.normal, .warning, .critical], queue: queue)
        source.setEventHandler { [source] in
            let event = source.data
            let level: MemoryPressure = event.contains(.critical) ? .critical : event.contains(.warning) ? .warning : .normal
            Self.handle(level, store: store, out: out)
        }
        source.activate()
        // Tests: with AUDIOSMITH_MEMORY_PRESSURE_SIGNALS=1, SIGUSR1 plays a
        // "warning" event and SIGUSR2 a "normal" one (simulating the
        // system's pressure, memory_pressure -S, needs root).
        if ProcessInfo.processInfo.environment["AUDIOSMITH_MEMORY_PRESSURE_SIGNALS"] == "1" {
            for (sig, level) in [(SIGUSR1, MemoryPressure.warning), (SIGUSR2, .normal)] {
                signal(sig, SIG_IGN)
                let s = DispatchSource.makeSignalSource(signal: sig, queue: queue)
                s.setEventHandler { Self.handle(level, store: store, out: out) }
                s.activate()
                signals.append(s)
            }
        }
    }

    deinit {
        source.cancel()
        signals.forEach { $0.cancel() }
    }

    /// Reacts to level: relieves the store, logs, tells the app.
    static func handle(_ level: MemoryPressure, store: ModelStore, out: Writer) {
        let unloaded = store.relieve(level)
        var message = "Memory pressure \(level.rawValue)"
        if !unloaded.isEmpty { message += ": unloaded \(unloaded.joined(separator: ", "))" }
        if level.refusesLoads { message += "; no model loads until it eases" }
        out.logLine(level == .normal ? "info" : "warning", message)
        out.notify(Method.engineMemory, MemoryParams(level: level.rawValue, unloaded: unloaded))
    }
}
