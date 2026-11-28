package catalog

// Multi-platform weight sources and hardware fit classification for
// Windows (everyday/iGPU/CPU-first) while keeping macOS 100% unchanged.

import "slices"

// Backend identifies how a model runs on a platform.
type Backend string

const (
	// BackendONNXDirectML runs via ONNX Runtime with DirectML on Intel/AMD/
	// Qualcomm/NVIDIA GPUs and falls back to CPU automatically.
	BackendONNXDirectML Backend = "onnx_directml"
	// BackendONNXCPU runs via ONNX Runtime on CPU (lightweight always-ready
	// models such as Silero VAD, DeepFilterNet 3 and AudioSeal).
	BackendONNXCPU Backend = "onnx_cpu"
	// BackendLlamaCpp runs GGUF/GGML weights via llama.cpp or whisper.cpp
	// (Vulkan on iGPU, CUDA on NVIDIA, CPU fallback).
	BackendLlamaCpp Backend = "llamacpp"
	// BackendPyTorch runs via the optional on-demand Python/PyTorch worker.
	BackendPyTorch Backend = "pytorch"
	// BackendExternal runs in a user-installed local engine over HTTP
	// (VOICEVOX / AivisSpeech).
	BackendExternal Backend = "external"
)

var knownBackends = []Backend{
	BackendONNXDirectML,
	BackendONNXCPU,
	BackendLlamaCpp,
	BackendPyTorch,
	BackendExternal,
}

// PlatformSource describes how a model is obtained and run on a specific
// OS (such as Windows), overriding the default macOS/MLX repo and size.
type PlatformSource struct {
	// Available reports whether the model can run on this platform.
	// False (or a nil PlatformSource on Windows) marks the entry as
	// macOS/MLX-only so Windows never downloads unusable MLX weights.
	Available bool    `json:"available"`
	Backend   Backend `json:"backend,omitempty"`
	Repo      string  `json:"repo,omitempty"`
	Revision  string  `json:"revision,omitempty"`
	Files     []File  `json:"files,omitempty"`
	Size      int64   `json:"size,omitempty"`
	// MinVRAMGB is the dedicated VRAM needed for comfortable discrete-GPU
	// execution (0 when the model runs well on iGPU/CPU without VRAM).
	MinVRAMGB float64 `json:"minVRAMGB,omitempty"`
	// MinRAMGB is the system RAM needed on this platform (defaults to
	// Entry.MinMemoryGB when 0).
	MinRAMGB   float64 `json:"minRAMGB,omitempty"`
	RunsOnIGPU bool    `json:"runsOnIGPU,omitempty"`
	RunsOnCPU  bool    `json:"runsOnCPU,omitempty"`
	Notes      string  `json:"notes,omitempty"`
}

// Hardware summarises the local machine's memory and GPU tier for
// HardwareFit, keeping package catalog free of OS-probing dependencies.
type Hardware struct {
	MemoryGB       float64 // total physical / unified RAM in GiB (0 when unknown)
	VRAMGB         float64 // dedicated VRAM of the primary discrete GPU in GiB
	HasDiscreteGPU bool    // true when a non-software discrete GPU is present
	HasIGPU        bool    // true when an integrated GPU (Intel/AMD/Qualcomm) is present
	HasCUDA        bool    // true when a non-software NVIDIA GPU is present
}

// FitLevel classifies how well a model fits the local machine on a given OS.
type FitLevel string

const (
	// FitComfortable means the model runs well on this machine.
	FitComfortable FitLevel = "comfortable"
	// FitSlowOnCPU means the model works on this machine, but will be
	// slower without GPU acceleration.
	FitSlowOnCPU FitLevel = "slow_on_cpu"
	// FitNeedsDiscrete means the model needs a discrete GPU (or more
	// dedicated VRAM) than this machine has.
	FitNeedsDiscrete FitLevel = "needs_discrete"
	// FitLowMemory means the machine has less system / unified memory
	// than the model needs.
	FitLowMemory FitLevel = "low_memory"
	// FitUnavailable means the model is not yet available on this OS
	// (e.g. an MLX-only checkpoint on Windows).
	FitUnavailable FitLevel = "unavailable"
)

// IsUsable reports whether the model can run on the machine (possibly
// slowly), as opposed to being unavailable on the OS.
func (f FitLevel) IsUsable() bool {
	return f == FitComfortable || f == FitSlowOnCPU
}

// PlatformSourceFor returns the platform-specific source metadata for goos,
// when present. On macOS ("darwin") the entry's top-level MLX fields apply,
// so this returns (nil, false).
func (e Entry) PlatformSourceFor(goos string) (*PlatformSource, bool) {
	if goos == "windows" && e.Windows != nil {
		return e.Windows, true
	}
	return nil, false
}

// AvailableOn reports whether the model can run on goos.
// Non-Windows platforms (macOS Apple Silicon and Linux dev) keep 100% of
// existing catalogue entries available. On Windows, only entries with an
// explicit Windows source marked Available are available.
func (e Entry) AvailableOn(goos string) bool {
	if goos != "windows" {
		return true
	}
	return e.Windows != nil && e.Windows.Available
}

// EffectiveRepo returns the Hugging Face repo (or external identifier) to
// download/load on goos. On Windows it returns "" for MLX-only entries so
// callers never download unusable MLX weights.
func (e Entry) EffectiveRepo(goos string) string {
	if goos != "windows" {
		return e.Repo
	}
	if !e.AvailableOn(goos) {
		return ""
	}
	if e.Windows.Repo != "" {
		return e.Windows.Repo
	}
	if e.IsExternal() || e.Windows.Backend == BackendExternal {
		return e.Repo
	}
	return ""
}

// EffectiveRevision returns the pinned commit revision for goos.
func (e Entry) EffectiveRevision(goos string) string {
	if goos != "windows" {
		return e.Revision
	}
	if !e.AvailableOn(goos) {
		return ""
	}
	return e.Windows.Revision
}

// EffectiveSize returns the download size in bytes for goos (0 when
// unavailable or external).
func (e Entry) EffectiveSize(goos string) int64 {
	if goos != "windows" {
		return e.Size
	}
	if !e.AvailableOn(goos) {
		return 0
	}
	if e.Windows.Size > 0 {
		return e.Windows.Size
	}
	if e.IsExternal() || e.Windows.Backend == BackendExternal {
		return 0
	}
	var sum int64
	for _, f := range e.Windows.Files {
		sum += f.Size
	}
	return sum
}

// EffectiveFiles returns the file list to download for goos.
func (e Entry) EffectiveFiles(goos string) []File {
	if goos != "windows" {
		return e.Files
	}
	if !e.AvailableOn(goos) {
		return nil
	}
	return e.Windows.Files
}

// EffectiveBackend returns the inference backend on goos ("" on macOS MLX).
func (e Entry) EffectiveBackend(goos string) Backend {
	if goos == "windows" {
		if e.AvailableOn(goos) {
			return e.Windows.Backend
		}
		return ""
	}
	if e.IsExternal() {
		return BackendExternal
	}
	return ""
}

// EffectiveMinRAMGB returns the minimum system/unified RAM in GiB for goos.
func (e Entry) EffectiveMinRAMGB(goos string) float64 {
	if goos == "windows" && e.Windows != nil && e.Windows.MinRAMGB > 0 {
		return e.Windows.MinRAMGB
	}
	return e.MinMemoryGB
}

// HardwareFit classifies how well the model runs on goos with hardware hw.
// On macOS it checks unified memory against MinMemoryGB (unchanged).
// On Windows it respects iGPU and CPU-only PCs without assuming NVIDIA.
func (e Entry) HardwareFit(goos string, hw Hardware) FitLevel {
	if goos != "windows" {
		if hw.MemoryGB > 0 && e.MinMemoryGB > 0 && hw.MemoryGB < e.MinMemoryGB {
			return FitLowMemory
		}
		return FitComfortable
	}
	if !e.AvailableOn(goos) {
		return FitUnavailable
	}
	w := e.Windows
	if e.IsExternal() || w.Backend == BackendExternal {
		return FitComfortable
	}
	minRAM := e.EffectiveMinRAMGB(goos)
	if hw.MemoryGB > 0 && minRAM > 0 && hw.MemoryGB < minRAM {
		return FitLowMemory
	}

	switch {
	case hw.HasDiscreteGPU:
		if w.Backend == BackendPyTorch && !hw.HasCUDA {
			if w.RunsOnCPU {
				return FitSlowOnCPU
			}
			return FitNeedsDiscrete
		}
		if w.MinVRAMGB > 0 && hw.VRAMGB > 0 && hw.VRAMGB < w.MinVRAMGB {
			return FitNeedsDiscrete
		}
		return FitComfortable

	case hw.HasIGPU:
		if w.RunsOnIGPU {
			return FitComfortable
		}
		if w.RunsOnCPU {
			return FitSlowOnCPU
		}
		return FitNeedsDiscrete

	default: // CPU-only or software renderer (e.g. VM)
		if w.Backend == BackendONNXCPU {
			return FitComfortable
		}
		// Small models with no VRAM requirement (e.g. Kokoro 82M ONNX)
		// run faster than real-time on everyday CPUs.
		const lightCPUSize = 400_000_000
		if w.RunsOnCPU && w.MinVRAMGB == 0 && w.Size > 0 && w.Size <= lightCPUSize {
			return FitComfortable
		}
		if w.RunsOnCPU {
			return FitSlowOnCPU
		}
		return FitNeedsDiscrete
	}
}

// ForTaskOn returns the entries that do task and are available on goos,
// in catalogue order, without hidden ones.
func (c *Catalog) ForTaskOn(task Task, goos string) []Entry {
	var out []Entry
	for _, e := range c.Entries {
		if !e.Hidden && slices.Contains(e.Tasks, task) && e.AvailableOn(goos) {
			out = append(out, e)
		}
	}
	return out
}

// ByEffectiveRepo finds an entry matching repo on goos (checking the
// platform-specific repo first, then the default repo).
func (c *Catalog) ByEffectiveRepo(goos, repo string) (Entry, bool) {
	if repo == "" {
		return Entry{}, false
	}
	if e, ok := c.find(func(e *Entry) bool { return e.EffectiveRepo(goos) == repo }); ok {
		return e, true
	}
	return c.ByRepo(repo)
}
