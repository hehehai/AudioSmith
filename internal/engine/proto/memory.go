package proto

// engine.memory (docs/protocol-v2.md §6, code review 1102 M1): the gpu
// worker reports a change of the system's memory pressure and the idle
// models it unloaded for it; at "critical" it refuses loads with
// CodeOOM (data MemoryPressureData) until the pressure eases.

// MethodEngineMemory is the notification, engine → app.
const MethodEngineMemory = "engine.memory"

// Memory pressure levels.
const (
	PressureNormal   = "normal"
	PressureWarning  = "warning"
	PressureCritical = "critical"
)

// MemoryParams are engine.memory's params.
type MemoryParams struct {
	Level    string   `json:"level"`    // PressureNormal, PressureWarning or PressureCritical
	Unloaded []string `json:"unloaded"` // models the engine unloaded for it
}

// ReasonMemoryPressure is the "reason" in an oom error's data when the
// engine refused a load under critical memory pressure (data also has
// "level" and "model").
const ReasonMemoryPressure = "memory_pressure"

// MethodEngineConfig is a request, app → a worker with models (gpu,
// listen; it lists it in hello.methods): the app's memory budget and the
// models it holds (code review 1103 F4/F5). The supervisor sends it after
// engine.hello and whenever either changes.
const MethodEngineConfig = "engine.config"

// EngineConfigParams are engine.config's params; a nil field leaves what
// the engine had.
type EngineConfigParams struct {
	// MemoryBudget is the bytes resident models may take (the memory limit
	// setting, or the family's share); 0 = the engine's default (60 % of
	// the Mac's memory). The engine's makeRoom keeps to it before a load.
	MemoryBudget *int64 `json:"memoryBudget,omitempty"`
	// Held are the models the app holds (HoldModels): a "warning" memory
	// pressure leaves them, and makeRoom drops them only last.
	Held *[]string `json:"held,omitempty"`
	// HoldAll: the app holds every model of the worker (Hold).
	HoldAll *bool `json:"holdAll,omitempty"`
}

// EngineConfigResult is what applies after engine.config.
type EngineConfigResult struct {
	MemoryBudget int64    `json:"memoryBudget"`
	Held         []string `json:"held"`
	HoldAll      bool     `json:"holdAll,omitempty"`
}
