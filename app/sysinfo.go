package main

// What this Mac is (M7a): macOS version, chip, memory, cores. The first-
// run wizard and Settings → Diagnostics show it; the model packs are
// chosen by memory. Read once, in the background (sysctl and sw_vers on
// macOS; /proc on Linux, for development).

import (
	"strconv"
	"strings"

	"github.com/hehehai/AudioSmith/internal/sysinfo"
)

// sysInfo describes the machine.
type sysInfo struct {
	OS          string // "macOS 15.4", "Windows 11 (26100)"
	Chip        string // "Apple M4"
	Memory      int64  // bytes
	AvailMemory int64  // available bytes (0 when unknown)
	Cores       int
	Arch        string // "arm64"
	GPUs        []sysinfo.GPU
	GPUClass    sysinfo.GPUClass
}

// readSysInfo asks the system (it may run commands: not on the UI thread).
var readSysInfo = func() sysInfo {
	info := sysinfo.Read()
	return sysInfo{
		OS:          info.OS,
		Chip:        info.Chip,
		Memory:      info.Memory,
		AvailMemory: info.AvailMemory,
		Cores:       info.Cores,
		Arch:        info.Arch,
		GPUs:        info.GPUs,
		GPUClass:    info.GPUClass,
	}
}

// MemoryGB is the memory in whole GB (as Apple sells it).
func (s sysInfo) MemoryGB() int { return int((s.Memory + (1 << 29)) >> 30) }

// toCatalogHW converts app's sysInfo into sysinfo.Info for catalog.Entry.HardwareFit.
func (s sysInfo) toCatalogHW() sysinfo.Info {
	return sysinfo.Info{
		OS:          s.OS,
		Chip:        s.Chip,
		Memory:      s.Memory,
		AvailMemory: s.AvailMemory,
		Cores:       s.Cores,
		Arch:        s.Arch,
		GPUs:        s.GPUs,
		GPUClass:    s.GPUClass,
	}
}

// macOSMajor is the macOS major version, 0 when unknown.
func (s sysInfo) macOSMajor() int {
	v, ok := strings.CutPrefix(s.OS, "macOS ")
	if !ok {
		return 0
	}
	major, _, _ := strings.Cut(v, ".")
	n, _ := strconv.Atoi(major)
	return n
}

// appleSilicon reports an Apple chip.
func (s sysInfo) appleSilicon() bool {
	return s.Arch == "arm64" && (strings.HasPrefix(s.Chip, "Apple") || s.Chip == "")
}

// loadSysInfo reads the machine once (in the background).
func (a *app) loadSysInfo() {
	if a.sys != nil || a.sysLoading {
		return
	}
	a.sysLoading = true
	a.background(func() {
		s := readSysInfo()
		a.post(func() { a.sys, a.sysLoading = &s, false })
	})
}
