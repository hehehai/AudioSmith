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

// engine.config (docs/protocol-v2.md §4, code review 1103 F4/F5): the app
// tells a worker with models (gpu, listen) its memory budget (the memory
// limit setting, or the family's share) and the models it holds, at start
// and when either changes. The Swift mirror of proto.EngineConfigParams.

public struct EngineConfigParams: Codable, Sendable {
    /// Bytes resident models may take; 0 = the engine's default (60 % of
    /// the Mac's memory). nil leaves it as it was.
    public var memoryBudget: Int64?
    /// The models the app holds (a dictation's recogniser, a call's
    /// models): a "warning" leaves them, makeRoom drops them last. nil
    /// leaves the list as it was.
    public var held: [String]?
    /// The app holds every model of this worker (a live call).
    public var holdAll: Bool?

    public init(memoryBudget: Int64? = nil, held: [String]? = nil, holdAll: Bool? = nil) {
        self.memoryBudget = memoryBudget
        self.held = held
        self.holdAll = holdAll
    }
}

/// What applies after engine.config.
public struct EngineConfigResult: Codable, Sendable {
    public var memoryBudget: Int64
    public var held: [String]
    public var holdAll: Bool?

    public init(memoryBudget: Int64, held: [String], holdAll: Bool? = nil) {
        self.memoryBudget = memoryBudget
        self.held = held
        self.holdAll = holdAll
    }
}
