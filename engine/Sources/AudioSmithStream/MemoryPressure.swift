import Darwin
import Foundation

// The system's memory pressure, as the gpu worker reacts to it (code
// review 1102, resources M1): its models were kept by a fixed budget (60 %
// of the Mac's memory) whatever else ran. At "warning" it unloads the
// models no job uses and returns MLX's buffer cache; at "critical" it
// also refuses to load another model (a retryable oom error) until the
// pressure falls. The engine's MemoryWatch.swift feeds it from a
// DispatchSource memory-pressure source; the level is also read from the
// kernel when a load starts, so a missed event cannot keep loads refused.

public enum MemoryPressure: String, Codable, Equatable, Sendable {
    case normal, warning, critical

    /// The level of sysctl kern.memorystatus_vm_pressure_level (1 normal,
    /// 2 warning, 4 critical); an unknown value is normal.
    public init(kernelLevel: Int32) {
        switch kernelLevel {
        case 4: self = .critical
        case 2: self = .warning
        default: self = .normal
        }
    }

    /// The level now (nil when the kernel does not say).
    public static func current() -> MemoryPressure? {
        var level: Int32 = 0
        var size = MemoryLayout<Int32>.size
        guard sysctlbyname("kern.memorystatus_vm_pressure_level", &level, &size, nil, 0) == 0 else { return nil }
        return MemoryPressure(kernelLevel: level)
    }

    /// Idle models are unloaded and MLX's cache cleared at this level.
    public var unloadsIdle: Bool { self != .normal }
    /// No model is loaded at this level.
    public var refusesLoads: Bool { self == .critical }

    /// The level for a load to go by: a level set for tests
    /// (AUDIOSMITH_MEMORY_PRESSURE), else the kernel's now, else the last
    /// event's (which can be stale: the kernel's is the truth when known).
    public static func effective(event: MemoryPressure, kernel: MemoryPressure?, forced: String?) -> MemoryPressure {
        if let forced, let f = MemoryPressure(rawValue: forced) { return f }
        return kernel ?? event
    }
}
