package sysinfo

import (
	"runtime"
	"testing"
)

func TestVendorFromPCI(t *testing.T) {
	cases := []struct {
		id   uint32
		want GPUVendor
	}{
		{0x10DE, VendorNVIDIA},
		{0x1002, VendorAMD},
		{0x8086, VendorIntel},
		{0x5143, VendorQualcomm},
		{0x1414, VendorMicrosoft},
		{0x15AD, VendorVMware},
		{0x9999, VendorUnknown},
	}
	for _, c := range cases {
		if got := VendorFromPCI(c.id); got != c.want {
			t.Errorf("VendorFromPCI(0x%X) = %q, want %q", c.id, got, c.want)
		}
	}
}

func TestGPUClassificationProfiles(t *testing.T) {
	const (
		mib = 1 << 20
		gib = 1 << 30
	)

	t.Run("hybrid laptop NVIDIA + Intel iGPU", func(t *testing.T) {
		gpus := []GPU{
			ClassifyAdapter("Intel(R) UHD Graphics", 0x8086, 128*mib, 8*gib, false),
			ClassifyAdapter("NVIDIA GeForce RTX 4060 Laptop GPU", 0x10DE, 8*gib, 8*gib, false),
			ClassifyAdapter("Microsoft Basic Render Driver", 0x1414, 0, 8*gib, true),
		}
		info := Info{GPUs: gpus, GPUClass: ClassifyGPUs(gpus)}
		if info.GPUClass != GPUClassDiscrete {
			t.Fatalf("GPUClass = %q, want discrete", info.GPUClass)
		}
		if !info.HasCUDA() || info.IsIntegratedOnly() {
			t.Fatalf("HasCUDA=%v IsIntegratedOnly=%v", info.HasCUDA(), info.IsIntegratedOnly())
		}
		best, ok := info.BestGPU()
		if !ok || best.Vendor != VendorNVIDIA {
			t.Fatalf("BestGPU = %+v, ok=%v", best, ok)
		}
	})

	t.Run("everyday Intel Iris Xe iGPU PC", func(t *testing.T) {
		gpus := []GPU{
			ClassifyAdapter("Intel(R) Iris(R) Xe Graphics", 0x8086, 128*mib, 8*gib, false),
			ClassifyAdapter("Microsoft Basic Render Driver", 0x1414, 0, 8*gib, true),
		}
		info := Info{GPUs: gpus, GPUClass: ClassifyGPUs(gpus)}
		if info.GPUClass != GPUClassIntegrated {
			t.Fatalf("GPUClass = %q, want integrated", info.GPUClass)
		}
		if info.HasCUDA() || !info.IsIntegratedOnly() {
			t.Fatalf("HasCUDA=%v IsIntegratedOnly=%v", info.HasCUDA(), info.IsIntegratedOnly())
		}
		best, ok := info.BestGPU()
		if !ok || best.Vendor != VendorIntel || !best.Integrated {
			t.Fatalf("BestGPU = %+v, ok=%v", best, ok)
		}
	})

	t.Run("everyday AMD Ryzen 780M iGPU PC", func(t *testing.T) {
		gpus := []GPU{
			ClassifyAdapter("AMD Radeon(TM) 780M Graphics", 0x1002, 512*mib, 16*gib, false),
		}
		info := Info{GPUs: gpus, GPUClass: ClassifyGPUs(gpus)}
		if info.GPUClass != GPUClassIntegrated || !info.IsIntegratedOnly() {
			t.Fatalf("GPUClass = %q", info.GPUClass)
		}
	})

	t.Run("AMD discrete GPU", func(t *testing.T) {
		gpus := []GPU{
			ClassifyAdapter("AMD Radeon RX 7800 XT", 0x1002, 16*gib, 16*gib, false),
		}
		info := Info{GPUs: gpus, GPUClass: ClassifyGPUs(gpus)}
		if info.GPUClass != GPUClassDiscrete || info.HasCUDA() {
			t.Fatalf("GPUClass = %q HasCUDA = %v", info.GPUClass, info.HasCUDA())
		}
	})

	t.Run("Intel Arc discrete GPU", func(t *testing.T) {
		gpus := []GPU{
			ClassifyAdapter("Intel(R) Arc(TM) A770 Graphics", 0x8086, 16*gib, 16*gib, false),
		}
		info := Info{GPUs: gpus, GPUClass: ClassifyGPUs(gpus)}
		if info.GPUClass != GPUClassDiscrete || info.HasCUDA() {
			t.Fatalf("GPUClass = %q HasCUDA = %v", info.GPUClass, info.HasCUDA())
		}
	})

	t.Run("Qualcomm ARM64 iGPU", func(t *testing.T) {
		gpus := []GPU{
			ClassifyAdapter("Qualcomm(R) Adreno(TM) X1-85 GPU", 0x5143, 128*mib, 8*gib, false),
		}
		info := Info{GPUs: gpus, GPUClass: ClassifyGPUs(gpus)}
		if info.GPUClass != GPUClassIntegrated || !info.IsIntegratedOnly() {
			t.Fatalf("GPUClass = %q", info.GPUClass)
		}
	})

	t.Run("VMware / software only (Win11 VM)", func(t *testing.T) {
		gpus := []GPU{
			ClassifyAdapter("VMware SVGA 3D", 0x15AD, 128*mib, 2*gib, false),
			ClassifyAdapter("Microsoft Basic Render Driver", 0x1414, 0, 3*gib, true),
		}
		info := Info{GPUs: gpus, GPUClass: ClassifyGPUs(gpus)}
		if info.GPUClass != GPUClassCPUOnly {
			t.Fatalf("GPUClass = %q, want cpu", info.GPUClass)
		}
		if _, ok := info.BestGPU(); ok {
			t.Fatal("expected no hardware GPU")
		}
	})
}

func TestFormatWindowsVersion(t *testing.T) {
	if got := formatWindowsVersion(10, 0, 26100); got != "Windows 11 (26100)" {
		t.Errorf("win11: %q", got)
	}
	if got := formatWindowsVersion(10, 0, 19045); got != "Windows 10 (19045)" {
		t.Errorf("win10: %q", got)
	}
	if got := formatWindowsVersion(6, 3, 9600); got != "Windows 6.3" {
		t.Errorf("win8.1: %q", got)
	}
}

func TestReadSmoke(t *testing.T) {
	info := Read()
	if info.Cores <= 0 || info.Arch != runtime.GOARCH {
		t.Fatalf("unexpected info: %+v", info)
	}
	if (runtime.GOOS == "linux" || runtime.GOOS == "darwin" || runtime.GOOS == "windows") && info.Memory <= 0 {
		t.Errorf("expected positive Memory, got %d", info.Memory)
	}
}
