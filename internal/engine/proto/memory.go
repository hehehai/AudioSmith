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
