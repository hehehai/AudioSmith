//go:build windows

package sysinfo

import (
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	kernel32               = windows.NewLazySystemDLL("kernel32.dll")
	procGlobalMemoryStatus = kernel32.NewProc("GlobalMemoryStatusEx")

	dxgiDLL                = windows.NewLazySystemDLL("dxgi.dll")
	procCreateDXGIFactory1 = dxgiDLL.NewProc("CreateDXGIFactory1")
)

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

func readOS(s *Info) {
	v := windows.RtlGetVersion()
	if v != nil {
		s.OS = formatWindowsVersion(v.MajorVersion, v.MinorVersion, v.BuildNumber)
	} else {
		s.OS = "Windows"
	}

	s.Chip = readCPUName()

	var mem memoryStatusEx
	mem.Length = uint32(unsafe.Sizeof(mem))
	if r, _, _ := procGlobalMemoryStatus.Call(uintptr(unsafe.Pointer(&mem))); r != 0 {
		s.Memory = int64(mem.TotalPhys)
		s.AvailMemory = int64(mem.AvailPhys)
	}

	s.GPUs = enumDXGIAdapters()
	s.GPUClass = ClassifyGPUs(s.GPUs)
}

func readCPUName() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DESCRIPTION\System\CentralProcessor\0`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	name, _, err := k.GetStringValue("ProcessorNameString")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(name)
}

// DXGI COM definitions for lightweight GPU enumeration without WMI/subprocesses.

var iidIDXGIFactory1 = windows.GUID{
	Data1: 0x770aae78,
	Data2: 0xf26f,
	Data3: 0x4dba,
	Data4: [8]byte{0xa8, 0x29, 0x25, 0x3c, 0x83, 0xd1, 0xb3, 0x87},
}

const (
	dxgiErrorNotFound     = 0x887A0002
	dxgiAdapterFlagSoft   = 0x2
	vtableRelease         = 2  // IUnknown::Release
	vtableEnumAdapters1   = 12 // IDXGIFactory1::EnumAdapters1
	vtableAdapterGetDesc1 = 10 // IDXGIAdapter1::GetDesc1
)

type dxgiAdapterDesc1 struct {
	Description           [128]uint16
	VendorID              uint32
	DeviceID              uint32
	SubSysID              uint32
	Revision              uint32
	DedicatedVideoMemory  uintptr
	DedicatedSystemMemory uintptr
	SharedSystemMemory    uintptr
	AdapterLuid           uint64
	Flags                 uint32
}

type comObj struct {
	vtbl *[16]uintptr
}

func comRelease(obj *comObj) {
	if obj == nil {
		return
	}
	_, _, _ = syscall.SyscallN(obj.vtbl[vtableRelease], uintptr(unsafe.Pointer(obj)))
}

func enumDXGIAdapters() []GPU {
	if err := procCreateDXGIFactory1.Find(); err != nil {
		return nil
	}
	var factory *comObj
	hr, _, _ := procCreateDXGIFactory1.Call(
		uintptr(unsafe.Pointer(&iidIDXGIFactory1)),
		uintptr(unsafe.Pointer(&factory)),
	)
	if hr != 0 || factory == nil {
		return nil
	}
	defer comRelease(factory)

	var gpus []GPU
	for i := uintptr(0); i < 32; i++ {
		var adapter *comObj
		hr, _, _ := syscall.SyscallN(
			factory.vtbl[vtableEnumAdapters1],
			uintptr(unsafe.Pointer(factory)),
			i,
			uintptr(unsafe.Pointer(&adapter)),
		)
		if uint32(hr) == dxgiErrorNotFound || hr != 0 || adapter == nil {
			break
		}
		var desc dxgiAdapterDesc1
		hrDesc, _, _ := syscall.SyscallN(
			adapter.vtbl[vtableAdapterGetDesc1],
			uintptr(unsafe.Pointer(adapter)),
			uintptr(unsafe.Pointer(&desc)),
		)
		comRelease(adapter)
		if hrDesc != 0 {
			continue
		}
		name := windows.UTF16ToString(desc.Description[:])
		gpus = append(gpus, ClassifyAdapter(
			name,
			desc.VendorID,
			int64(desc.DedicatedVideoMemory),
			int64(desc.SharedSystemMemory),
			desc.Flags&dxgiAdapterFlagSoft != 0,
		))
	}
	return gpus
}
