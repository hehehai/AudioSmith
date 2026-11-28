// Package hw probes Windows/host hardware capabilities with an everyday /
// iGPU-first mindset: Intel/AMD integrated GPUs and CPU-only PCs are
// first-class citizens alongside discrete NVIDIA GPUs.
package hw

import (
	"fmt"
	"strings"

	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

// GPUTier is the primary acceleration tier selected for the host.
type GPUTier string

const (
	TierCUDA     GPUTier = "cuda"     // NVIDIA discrete GPU with CUDA driver
	TierDirectML GPUTier = "directml" // Windows DirectML (Intel/AMD/Qualcomm iGPU or dGPU)
	TierVulkan   GPUTier = "vulkan"   // Vulkan compute (llama.cpp / GPU fallback)
	TierCPU      GPUTier = "cpu"      // Pure CPU (always available)
)

// PCI vendor IDs for common GPU and virtual display adapters.
const (
	VendorNVIDIA    uint32 = 0x10DE
	VendorIntel     uint32 = 0x8086
	VendorAMD       uint32 = 0x1002
	VendorMicrosoft uint32 = 0x1414
	VendorVMware    uint32 = 0x15AD
	VendorQualcomm  uint32 = 0x5143
	VendorQualcomm2 uint32 = 0x4D4F4351
)

// Adapter describes one display/compute adapter reported by DXGI or the host OS.
type Adapter struct {
	Name                  string `json:"name"`
	VendorID              uint32 `json:"vendorId,omitempty"`
	DeviceID              uint32 `json:"deviceId,omitempty"`
	Vendor                string `json:"vendor,omitempty"`
	DedicatedVideoMemory  int64  `json:"dedicatedVideoMemory"`
	DedicatedSystemMemory int64  `json:"dedicatedSystemMemory,omitempty"`
	SharedSystemMemory    int64  `json:"sharedSystemMemory"`
	Software              bool   `json:"software,omitempty"`
	Integrated            bool   `json:"integrated,omitempty"`
	Virtual               bool   `json:"virtual,omitempty"`
}

// MemoryBudget holds separate VRAM and system RAM budgets (bytes) so the
// engine never overcommits discrete VRAM or starves WDDM shared memory on iGPUs.
type MemoryBudget struct {
	SystemRAMBytes int64 `json:"systemRamBytes"`
	AvailRAMBytes  int64 `json:"availRamBytes,omitempty"`
	VRAMBytes      int64 `json:"vramBytes"`
	SharedRAMBytes int64 `json:"sharedRamBytes"`
	// RecommendedModelBudget is the default byte budget for resident models.
	RecommendedModelBudget int64 `json:"recommendedModelBudget"`
}

// Capabilities lists which acceleration runtimes are detected on the host.
type Capabilities struct {
	CUDA     bool `json:"cuda"`
	DirectML bool `json:"directml"`
	Vulkan   bool `json:"vulkan"`
	CPU      bool `json:"cpu"`
}

// Info is the complete hardware probe result returned by engine.info and
// summarised into engine.hello's proto.Device.
type Info struct {
	OS               string       `json:"os"`
	Arch             string       `json:"arch"`
	NumCPU           int          `json:"numCpu"`
	GPUTier          GPUTier      `json:"gpuTier"`
	PreferredAdapter *Adapter     `json:"preferredAdapter,omitempty"`
	Adapters         []Adapter    `json:"adapters"`
	Budget           MemoryBudget `json:"budget"`
	Capabilities     Capabilities `json:"capabilities"`
	Backends         []string     `json:"backends"`
}

// Probe inspects the current machine's OS, memory, GPU adapters and runtime
// DLLs. It never fails: on unknown or restricted systems it falls back cleanly
// to TierCPU.
func Probe() Info {
	return probeHost()
}

// Classify turns raw adapter and runtime signals into an iGPU-first Info.
// Exported for deterministic unit testing on any OS.
func Classify(osName, arch string, numCPU int, totalRAM, availRAM int64, adapters []Adapter, caps Capabilities) Info {
	caps.CPU = true
	classified := make([]Adapter, 0, len(adapters))
	for _, a := range adapters {
		a.Vendor = vendorName(a.VendorID, a.Name)
		a.Virtual = isVirtualAdapter(a.VendorID, a.Name)
		a.Integrated = isIntegratedAdapter(a)
		classified = append(classified, a)
	}

	preferred := pickPreferredAdapter(classified)
	tier := selectTier(preferred, caps)
	budget := computeBudget(totalRAM, availRAM, preferred, tier)

	backends := []string{"cpu"}
	if tier == TierCUDA {
		backends = append([]string{"cuda", "directml", "vulkan"}, backends...)
	} else if tier == TierDirectML {
		if caps.Vulkan {
			backends = append([]string{"directml", "vulkan"}, backends...)
		} else {
			backends = append([]string{"directml"}, backends...)
		}
	} else if tier == TierVulkan {
		backends = append([]string{"vulkan"}, backends...)
	}

	return Info{
		OS:               osName,
		Arch:             arch,
		NumCPU:           numCPU,
		GPUTier:          tier,
		PreferredAdapter: preferred,
		Adapters:         classified,
		Budget:           budget,
		Capabilities:     caps,
		Backends:         backends,
	}
}

// ToProtoDevice formats Info into protocol-v2's Device field for engine.hello.
func (i Info) ToProtoDevice() proto.Device {
	name := fmt.Sprintf("CPU (%s)", i.Arch)
	if i.PreferredAdapter != nil && i.PreferredAdapter.Name != "" {
		name = fmt.Sprintf("%s (%s)", i.PreferredAdapter.Name, i.GPUTier)
	}
	mem := i.Budget.SystemRAMBytes
	if mem <= 0 {
		mem = i.Budget.RecommendedModelBudget
	}
	return proto.Device{
		Name:   name,
		Memory: mem,
		OS:     i.OS,
	}
}

func vendorName(id uint32, name string) string {
	switch id {
	case VendorNVIDIA:
		return "NVIDIA"
	case VendorIntel:
		return "Intel"
	case VendorAMD:
		return "AMD"
	case VendorMicrosoft:
		return "Microsoft"
	case VendorVMware:
		return "VMware"
	case VendorQualcomm, VendorQualcomm2:
		return "Qualcomm"
	}
	low := strings.ToLower(name)
	switch {
	case strings.Contains(low, "nvidia"):
		return "NVIDIA"
	case strings.Contains(low, "intel"):
		return "Intel"
	case strings.Contains(low, "amd"), strings.Contains(low, "radeon"):
		return "AMD"
	case strings.Contains(low, "qualcomm"), strings.Contains(low, "adreno"):
		return "Qualcomm"
	case strings.Contains(low, "vmware"):
		return "VMware"
	case strings.Contains(low, "microsoft"):
		return "Microsoft"
	}
	return "Unknown"
}

func isVirtualAdapter(vendorID uint32, name string) bool {
	if vendorID == VendorMicrosoft || vendorID == VendorVMware {
		return true
	}
	low := strings.ToLower(name)
	for _, kw := range []string{"basic render", "vmware", "virtualbox", "parallels", "hyper-v", "llvmpipe", "swiftshader"} {
		if strings.Contains(low, kw) {
			return true
		}
	}
	return false
}

func isIntegratedAdapter(a Adapter) bool {
	if a.Software || a.Virtual {
		return false
	}
	// On Windows WDDM, integrated GPUs typically report <= 1 GiB dedicated VRAM
	// and rely primarily on SharedSystemMemory.
	const oneGiB = 1 << 30
	if a.DedicatedVideoMemory <= oneGiB && a.SharedSystemMemory > a.DedicatedVideoMemory {
		return true
	}
	low := strings.ToLower(a.Name)
	for _, kw := range []string{"uhd", "iris", "hd graphics", "radeon(tm) graphics", "adreno"} {
		if strings.Contains(low, kw) {
			return true
		}
	}
	return false
}

func pickPreferredAdapter(adapters []Adapter) *Adapter {
	bestIdx := -1
	bestScore := -1
	for i, a := range adapters {
		score := adapterScore(a)
		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}
	if bestIdx < 0 {
		return nil
	}
	cp := adapters[bestIdx]
	return &cp
}

func adapterScore(a Adapter) int {
	if a.Software {
		return 1
	}
	if a.Virtual {
		return 10
	}
	// Discrete GPU with substantial VRAM ranks highest; iGPUs rank right behind
	// as first-class hardware adapters.
	score := 100
	if !a.Integrated && a.DedicatedVideoMemory >= 2<<30 {
		score += 100
	}
	if a.VendorID == VendorNVIDIA {
		score += 20
	}
	// Tie-break by VRAM (in GiB, capped).
	vramGiB := int(a.DedicatedVideoMemory >> 30)
	if vramGiB > 64 {
		vramGiB = 64
	}
	return score + vramGiB
}

func selectTier(preferred *Adapter, caps Capabilities) GPUTier {
	if preferred == nil || preferred.Software || preferred.Virtual {
		return TierCPU
	}
	const minCUDAVRAM = 4 << 30 // 4 GiB minimum for practical CUDA inference
	if preferred.VendorID == VendorNVIDIA && caps.CUDA && preferred.DedicatedVideoMemory >= minCUDAVRAM {
		return TierCUDA
	}
	if caps.DirectML {
		return TierDirectML
	}
	if caps.Vulkan {
		return TierVulkan
	}
	return TierCPU
}

func computeBudget(totalRAM, availRAM int64, preferred *Adapter, tier GPUTier) MemoryBudget {
	b := MemoryBudget{
		SystemRAMBytes: totalRAM,
		AvailRAMBytes:  availRAM,
	}
	if preferred != nil {
		b.VRAMBytes = preferred.DedicatedVideoMemory
		b.SharedRAMBytes = preferred.SharedSystemMemory
	}
	switch tier {
	case TierCUDA:
		// Reserve ~25% of discrete VRAM for Windows DWM + browser; e.g. 8 GiB → ~6 GiB.
		b.RecommendedModelBudget = (b.VRAMBytes * 3) / 4
	case TierDirectML, TierVulkan:
		// iGPU uses shared system RAM; cap at 50% of shared memory or 50% of system RAM.
		shared := b.SharedRAMBytes
		if shared <= 0 {
			shared = totalRAM / 2
		}
		capRAM := totalRAM / 2
		if capRAM > 0 && shared > capRAM {
			shared = capRAM
		}
		b.RecommendedModelBudget = shared
	default:
		// Pure CPU: use up to 50% of system RAM.
		b.RecommendedModelBudget = totalRAM / 2
	}
	const minBudget = 512 << 20 // 512 MiB floor for lightweight models
	if b.RecommendedModelBudget < minBudget && totalRAM >= 2*minBudget {
		b.RecommendedModelBudget = minBudget
	}
	return b
}
