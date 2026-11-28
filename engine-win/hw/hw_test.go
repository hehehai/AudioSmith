package hw

import "testing"

func TestClassify_EverydayIntelIGPU(t *testing.T) {
	const gb = 1 << 30
	adapters := []Adapter{
		{
			Name:                 "Intel(R) Iris(R) Xe Graphics",
			VendorID:             VendorIntel,
			DedicatedVideoMemory: 128 << 20,
			SharedSystemMemory:   8 * gb,
		},
		{
			Name:                 "Microsoft Basic Render Driver",
			VendorID:             VendorMicrosoft,
			DedicatedVideoMemory: 0,
			SharedSystemMemory:   8 * gb,
			Software:             true,
		},
	}
	info := Classify("Windows 11 (10.0.22631)", "amd64", 12, 16*gb, 10*gb, adapters, Capabilities{
		DirectML: true,
		Vulkan:   true,
	})
	if info.GPUTier != TierDirectML {
		t.Fatalf("GPUTier = %q, want %q", info.GPUTier, TierDirectML)
	}
	if info.PreferredAdapter == nil || info.PreferredAdapter.Name != "Intel(R) Iris(R) Xe Graphics" {
		t.Fatalf("unexpected preferred adapter: %+v", info.PreferredAdapter)
	}
	if !info.PreferredAdapter.Integrated {
		t.Fatalf("expected Intel Iris Xe to be classified as integrated")
	}
	if info.Budget.RecommendedModelBudget != 8*gb {
		t.Fatalf("RecommendedModelBudget = %d, want %d", info.Budget.RecommendedModelBudget, 8*gb)
	}
}

func TestClassify_DiscreteNVIDIA(t *testing.T) {
	const gb = 1 << 30
	adapters := []Adapter{
		{
			Name:                 "AMD Radeon(TM) Graphics",
			VendorID:             VendorAMD,
			DedicatedVideoMemory: 512 << 20,
			SharedSystemMemory:   16 * gb,
		},
		{
			Name:                 "NVIDIA GeForce RTX 4060",
			VendorID:             VendorNVIDIA,
			DedicatedVideoMemory: 8 * gb,
			SharedSystemMemory:   16 * gb,
		},
	}
	info := Classify("Windows 11 (10.0.26100)", "amd64", 16, 32*gb, 20*gb, adapters, Capabilities{
		CUDA:     true,
		DirectML: true,
		Vulkan:   true,
	})
	if info.GPUTier != TierCUDA {
		t.Fatalf("GPUTier = %q, want %q", info.GPUTier, TierCUDA)
	}
	if info.PreferredAdapter == nil || info.PreferredAdapter.Name != "NVIDIA GeForce RTX 4060" {
		t.Fatalf("unexpected preferred adapter: %+v", info.PreferredAdapter)
	}
	if info.Budget.RecommendedModelBudget != 6*gb {
		t.Fatalf("RecommendedModelBudget = %d, want 6 GiB", info.Budget.RecommendedModelBudget)
	}
}

func TestClassify_VMwareARM64FallsBackToCPU(t *testing.T) {
	const gb = 1 << 30
	adapters := []Adapter{
		{
			Name:                 "VMware SVGA 3D",
			VendorID:             VendorVMware,
			DedicatedVideoMemory: 128 << 20,
			SharedSystemMemory:   3 * gb,
		},
		{
			Name:                 "Microsoft Basic Render Driver",
			VendorID:             VendorMicrosoft,
			DedicatedVideoMemory: 0,
			SharedSystemMemory:   3 * gb,
			Software:             true,
		},
	}
	info := Classify("Windows 11 (10.0.26300)", "arm64", 4, 6*gb, 4*gb, adapters, Capabilities{
		DirectML: false,
	})
	if info.GPUTier != TierCPU {
		t.Fatalf("GPUTier = %q, want %q", info.GPUTier, TierCPU)
	}
	if info.PreferredAdapter == nil || !info.PreferredAdapter.Virtual {
		t.Fatalf("expected virtual adapter flag on VMware SVGA 3D: %+v", info.PreferredAdapter)
	}
	if info.Budget.RecommendedModelBudget != 3*gb {
		t.Fatalf("RecommendedModelBudget = %d, want 3 GiB", info.Budget.RecommendedModelBudget)
	}
	dev := info.ToProtoDevice()
	if dev.Memory != 6*gb || dev.OS != "Windows 11 (10.0.26300)" {
		t.Fatalf("unexpected proto.Device: %+v", dev)
	}
}

func TestClassify_CPUOnlyNoAdapters(t *testing.T) {
	const gb = 1 << 30
	info := Classify("Windows 10 (10.0.19045)", "amd64", 8, 8*gb, 4*gb, nil, Capabilities{})
	if info.GPUTier != TierCPU {
		t.Fatalf("GPUTier = %q, want %q", info.GPUTier, TierCPU)
	}
	dev := info.ToProtoDevice()
	if dev.Name != "CPU (amd64)" {
		t.Fatalf("dev.Name = %q, want %q", dev.Name, "CPU (amd64)")
	}
}
