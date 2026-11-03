import Foundation

// engine.memory (docs/protocol-v2.md §4, code review 1102 M1): the gpu
// worker tells the app when the system's memory pressure changes, and
// which idle models it unloaded for it. The Swift mirror of
// internal/engine/proto/memory.go.

public struct MemoryParams: Codable, Sendable {
    /// "normal", "warning" or "critical".
    public var level: String
    /// Models unloaded for it (none at "normal").
    public var unloaded: [String]

    public init(level: String, unloaded: [String]) {
        self.level = level
        self.unloaded = unloaded
    }
}
