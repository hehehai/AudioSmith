// Package sysinfo probes local hardware and OS details (OS version, CPU,
// physical RAM, and GPU adapters) without blocking on slow subprocesses.
// On Windows it uses Win32 APIs (GlobalMemoryStatusEx, RtlGetVersion,
// registry, and DXGI) and distinguishes discrete GPUs, Intel/AMD/Qualcomm
// integrated GPUs, and CPU-only / software-rendered systems.
package sysinfo

import (
	"runtime"
	"strings"
)

// GPUVendor identifies a GPU adapter's hardware vendor.
type GPUVendor string

const (
	VendorUnknown   GPUVendor = ""
	VendorNVIDIA    GPUVendor = "nvidia"
	VendorAMD       GPUVendor = "amd"
	VendorIntel     GPUVendor = "intel"
	VendorQualcomm  GPUVendor = "qualcomm"
	VendorApple     GPUVendor = "apple"
	VendorMicrosoft GPUVendor = "microsoft"
	VendorVMware    GPUVendor = "vmware"
)

// GPUClass summarises the machine's primary GPU tier for model selection
// and runtime defaults.
type GPUClass string

const (
	GPUClassCPUOnly    GPUClass = "cpu"
	GPUClassIntegrated GPUClass = "integrated"
	GPUClassDiscrete   GPUClass = "discrete"
	GPUClassApple      GPUClass = "apple"
)

// GPU describes one detected graphics adapter.
type GPU struct {
	Name          string
	Vendor        GPUVendor
	VendorID      uint32
	DedicatedVRAM int64 // bytes
	SharedMemory  int64 // bytes
	Integrated    bool  // true for iGPU / unified memory
	Software      bool  // true for software / virtualised renderers (WARP, SVGA)
}

// Info describes the local machine.
type Info struct {
	OS          string // e.g. "macOS 15.4", "Windows 11 (26100)", "Ubuntu 24.04"
	Chip        string // CPU brand string
	Memory      int64  // total physical RAM in bytes
	AvailMemory int64  // available physical RAM in bytes (0 when unknown)
	Cores       int
	Arch        string // runtime.GOARCH
	GPUs        []GPU
	GPUClass    GPUClass
}

// Read probes the current machine. Call off the UI thread.
func Read() Info {
	info := Info{
		Cores: runtime.NumCPU(),
		Arch:  runtime.GOARCH,
	}
	readOS(&info)
	if info.GPUClass == "" {
		info.GPUClass = ClassifyGPUs(info.GPUs)
	}
	return info
}

// MemoryGB returns total physical RAM rounded to the nearest GiB.
func (i Info) MemoryGB() int { return int((i.Memory + (1 << 29)) >> 30) }

// AvailMemoryGB returns available physical RAM rounded to the nearest GiB.
func (i Info) AvailMemoryGB() int { return int((i.AvailMemory + (1 << 29)) >> 30) }

// BestGPU returns the strongest non-software GPU adapter, if any.
// Discrete GPUs are preferred over integrated GPUs, breaking ties by
// dedicated VRAM and then shared memory.
func (i Info) BestGPU() (GPU, bool) { return BestGPU(i.GPUs) }

// HasCUDA reports whether a non-software NVIDIA GPU is present.
func (i Info) HasCUDA() bool {
	for _, g := range i.GPUs {
		if !g.Software && g.Vendor == VendorNVIDIA {
			return true
		}
	}
	return false
}

// IsIntegratedOnly reports whether the machine has an integrated GPU
// (Intel/AMD/Qualcomm iGPU) and no discrete GPU.
func (i Info) IsIntegratedOnly() bool {
	return i.GPUClass == GPUClassIntegrated
}

// VendorFromPCI maps a PCI vendor ID (as reported by DXGI) to a GPUVendor.
func VendorFromPCI(vendorID uint32) GPUVendor {
	switch vendorID {
	case 0x10DE:
		return VendorNVIDIA
	case 0x1002, 0x1022:
		return VendorAMD
	case 0x8086:
		return VendorIntel
	case 0x5143, 0x4D4F, 0x17CB:
		return VendorQualcomm
	case 0x106B:
		return VendorApple
	case 0x1414:
		return VendorMicrosoft
	case 0x15AD:
		return VendorVMware
	default:
		return VendorUnknown
	}
}

// ClassifyAdapter classifies a raw adapter (vendor ID, name, VRAM, shared
// memory, software flag) into a GPU struct.
func ClassifyAdapter(name string, vendorID uint32, dedicatedVRAM, sharedMem int64, dxgiSoftware bool) GPU {
	vendor := VendorFromPCI(vendorID)
	lower := strings.ToLower(name)
	software := dxgiSoftware ||
		vendor == VendorMicrosoft ||
		vendor == VendorVMware ||
		strings.Contains(lower, "basic render") ||
		strings.Contains(lower, "vmware") ||
		strings.Contains(lower, "virtualbox") ||
		strings.Contains(lower, "llvmpipe")

	integrated := false
	if !software {
		const discreteThreshold = 2 << 30 // 2 GiB dedicated VRAM
		switch vendor {
		case VendorApple, VendorQualcomm:
			integrated = true
		case VendorIntel:
			// Intel UHD / Iris / Arc integrated graphics report small carved-out
			// dedicated VRAM (typically 128 MiB–512 MiB); Arc dGPUs have >= 4 GiB.
			integrated = dedicatedVRAM < discreteThreshold
		case VendorAMD:
			// AMD APU iGPUs (Radeon Graphics, Vega, 680M/780M/890M) report
			// <= 512 MiB–1 GiB dedicated VRAM carved from system RAM.
			integrated = dedicatedVRAM < discreteThreshold
		case VendorNVIDIA:
			integrated = false
		default:
			integrated = dedicatedVRAM < discreteThreshold && sharedMem > 0
		}
	}

	return GPU{
		Name:          strings.TrimSpace(name),
		Vendor:        vendor,
		VendorID:      vendorID,
		DedicatedVRAM: dedicatedVRAM,
		SharedMemory:  sharedMem,
		Integrated:    integrated,
		Software:      software,
	}
}

// BestGPU picks the strongest non-software GPU from gpus.
func BestGPU(gpus []GPU) (GPU, bool) {
	var best GPU
	found := false
	for _, g := range gpus {
		if g.Software {
			continue
		}
		if !found || gpuBetter(g, best) {
			best = g
			found = true
		}
	}
	return best, found
}

func gpuBetter(a, b GPU) bool {
	if a.Integrated != b.Integrated {
		return !a.Integrated // discrete beats integrated
	}
	if a.DedicatedVRAM != b.DedicatedVRAM {
		return a.DedicatedVRAM > b.DedicatedVRAM
	}
	return a.SharedMemory > b.SharedMemory
}

// ClassifyGPUs returns the primary GPUClass for a list of adapters.
func ClassifyGPUs(gpus []GPU) GPUClass {
	best, ok := BestGPU(gpus)
	if !ok {
		return GPUClassCPUOnly
	}
	if best.Vendor == VendorApple {
		return GPUClassApple
	}
	if best.Integrated {
		return GPUClassIntegrated
	}
	return GPUClassDiscrete
}

// formatWindowsVersion formats major/minor/build into a human-readable OS string.
func formatWindowsVersion(major, minor, build uint32) string {
	switch {
	case major >= 10 && build >= 22000:
		return "Windows 11 (" + itoaUint32(build) + ")"
	case major >= 10:
		return "Windows 10 (" + itoaUint32(build) + ")"
	case major > 0:
		return "Windows " + itoaUint32(major) + "." + itoaUint32(minor)
	default:
		return "Windows"
	}
}

func itoaUint32(v uint32) string {
	if v == 0 {
		return "0"
	}
	var buf [10]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
