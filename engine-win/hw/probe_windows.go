//go:build windows

package hw

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	modKernel32 = syscall.NewLazyDLL("kernel32.dll")
	modNtdll    = syscall.NewLazyDLL("ntdll.dll")
	modDXGI     = syscall.NewLazyDLL("dxgi.dll")

	procGlobalMemoryStatusEx = modKernel32.NewProc("GlobalMemoryStatusEx")
	procRtlGetVersion        = modNtdll.NewProc("RtlGetVersion")
	procCreateDXGIFactory1   = modDXGI.NewProc("CreateDXGIFactory1")
)

type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

type osVersionInfoExW struct {
	OSVersionInfoSize uint32
	MajorVersion      uint32
	MinorVersion      uint32
	BuildNumber       uint32
	PlatformID        uint32
	CSDVersion        [128]uint16
	ServicePackMajor  uint16
	ServicePackMinor  uint16
	SuiteMask         uint16
	ProductType       byte
	Reserved          byte
}

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

// dxgiAdapterDesc1 matches Microsoft's DXGI_ADAPTER_DESC1 on 64-bit Windows
// (amd64 and arm64).
type dxgiAdapterDesc1 struct {
	Description           [128]uint16
	VendorID              uint32
	DeviceID              uint32
	SubSysID              uint32
	Revision              uint32
	DedicatedVideoMemory  uintptr
	DedicatedSystemMemory uintptr
	SharedSystemMemory    uintptr
	AdapterLuidLow        uint32
	AdapterLuidHigh       int32
	Flags                 uint32
	_                     uint32
}

const dxgiAdapterFlagSoftware = 2

// IID_IDXGIFactory1 = {770aae78-f26f-4dba-a829-253c83d1b387}
var iidIDXGIFactory1 = guid{
	Data1: 0x770aae78,
	Data2: 0xf26f,
	Data3: 0x4dba,
	Data4: [8]byte{0xa8, 0x29, 0x25, 0x3c, 0x83, 0xd1, 0xb3, 0x87},
}

func probeHost() Info {
	totalRAM, availRAM := queryMemory()
	osName := queryOSVersion()
	adapters := enumDXGIAdapters()

	caps := Capabilities{
		CUDA:     dllPresent("nvcuda.dll"),
		DirectML: dllPresent("DirectML.dll") || hasHardwareD3DAdapter(adapters),
		Vulkan:   dllPresent("vulkan-1.dll"),
		CPU:      true,
	}
	return Classify(osName, runtime.GOARCH, runtime.NumCPU(), totalRAM, availRAM, adapters, caps)
}

func queryMemory() (total, avail int64) {
	var ms memoryStatusEx
	ms.Length = uint32(unsafe.Sizeof(ms))
	r, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&ms)))
	if r == 0 {
		return 0, 0
	}
	return int64(ms.TotalPhys), int64(ms.AvailPhys)
}

func queryOSVersion() string {
	var v osVersionInfoExW
	v.OSVersionInfoSize = uint32(unsafe.Sizeof(v))
	if r, _, _ := procRtlGetVersion.Call(uintptr(unsafe.Pointer(&v))); r != 0 {
		return "Windows"
	}
	name := "Windows 10"
	if v.MajorVersion > 10 || (v.MajorVersion == 10 && v.BuildNumber >= 22000) {
		name = "Windows 11"
	}
	return fmt.Sprintf("%s (%d.%d.%d)", name, v.MajorVersion, v.MinorVersion, v.BuildNumber)
}

func dllPresent(name string) bool {
	h, err := syscall.LoadLibrary(name)
	if err != nil {
		return false
	}
	_ = syscall.FreeLibrary(h)
	return true
}

func hasHardwareD3DAdapter(adapters []Adapter) bool {
	for _, a := range adapters {
		if !a.Software && !isVirtualAdapter(a.VendorID, a.Name) {
			return true
		}
	}
	return false
}

type comObject struct {
	vtbl *[32]uintptr
}

func comRelease(obj *comObject) {
	if obj == nil {
		return
	}
	_, _, _ = syscall.SyscallN(obj.vtbl[2], uintptr(unsafe.Pointer(obj)))
}

func enumDXGIAdapters() []Adapter {
	if err := procCreateDXGIFactory1.Find(); err != nil {
		return nil
	}
	var factory *comObject
	hr, _, _ := procCreateDXGIFactory1.Call(
		uintptr(unsafe.Pointer(&iidIDXGIFactory1)),
		uintptr(unsafe.Pointer(&factory)),
	)
	if int32(hr) < 0 || factory == nil {
		return nil
	}
	defer comRelease(factory)

	// IDXGIFactory1::EnumAdapters1 is vtable slot 12.
	enumAdapters1 := factory.vtbl[12]
	var out []Adapter
	for idx := uintptr(0); idx < 16; idx++ {
		var adapter *comObject
		hr, _, _ := syscall.SyscallN(enumAdapters1, uintptr(unsafe.Pointer(factory)), idx, uintptr(unsafe.Pointer(&adapter)))
		if int32(hr) < 0 || adapter == nil {
			break
		}
		var desc dxgiAdapterDesc1
		// IDXGIAdapter1::GetDesc1 is vtable slot 10.
		getDesc1 := adapter.vtbl[10]
		hr2, _, _ := syscall.SyscallN(getDesc1, uintptr(unsafe.Pointer(adapter)), uintptr(unsafe.Pointer(&desc)))
		comRelease(adapter)
		if int32(hr2) < 0 {
			continue
		}
		out = append(out, Adapter{
			Name:                  syscall.UTF16ToString(desc.Description[:]),
			VendorID:              desc.VendorID,
			DeviceID:              desc.DeviceID,
			DedicatedVideoMemory:  int64(desc.DedicatedVideoMemory),
			DedicatedSystemMemory: int64(desc.DedicatedSystemMemory),
			SharedSystemMemory:    int64(desc.SharedSystemMemory),
			Software:              desc.Flags&dxgiAdapterFlagSoftware != 0,
		})
	}
	return out
}
