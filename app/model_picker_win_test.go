package main

import (
	"strings"
	"testing"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/sysinfo"
)

func simulateWindows(t *testing.T) {
	t.Helper()
	prev := currentOS
	currentOS = func() string { return "windows" }
	t.Cleanup(func() { currentOS = prev })
}

// TestModelPickerWindowsIGPU verifies that on a Windows iGPU laptop
// (Intel Iris Xe, 16 GB RAM), the Studio picker recommends Kokoro,
// labels MLX-only models as unavailable on Windows, and blocks picking
// MLX-only models.
func TestModelPickerWindowsIGPU(t *testing.T) {
	simulateWindows(t)
	a, _, tt := newTestApp(t)
	a.sys = &sysInfo{
		OS:       "Windows 11 (26100)",
		Chip:     "Intel Core Ultra 7 155H",
		Memory:   16 << 30,
		Arch:     "amd64",
		GPUClass: sysinfo.GPUClassIntegrated,
		GPUs: []sysinfo.GPU{{
			Name:          "Intel Iris Xe Graphics",
			Vendor:        sysinfo.VendorIntel,
			DedicatedVRAM: 128 << 20,
			SharedMemory:  8 << 30,
			Integrated:    true,
		}},
	}
	tt.SetSize(1100, 1000)
	tt.Frame()

	// Open the Studio model picker.
	a.pickerOpen = map[string]bool{"model": true}
	tt.Frame()
	texts := strings.Join(tt.Texts(), "|")
	if !strings.Contains(texts, "Recommended") || !strings.Contains(texts, "Fast English voices") {
		t.Fatalf("expected Kokoro in Recommended on Windows iGPU: %s", texts)
	}
	// Expand "Show all models": MLX-only entries are labelled and disabled.
	a.pickerAll = map[string]bool{"model": true}
	tt.Frame()
	texts = strings.Join(tt.Texts(), "|")
	if !strings.Contains(texts, "Not yet available on Windows") {
		t.Errorf("expected unavailable label for MLX-only models: %s", texts)
	}
	// Clicking an MLX-only model must not switch away from the current model.
	before := a.preset().ID
	_ = tt.Click("High-fidelity clone")
	tt.Frame()
	if a.preset().ID != before {
		t.Errorf("MLX-only model was picked on Windows: %s -> %s", before, a.preset().ID)
	}

	// Stories dialogue section also marks MLX-only dialogue models as unavailable.
	a.pickerOpen = nil
	a.navigate("/stories")
	kittenDoc(&a.lfStories.doc)
	tt.Frame()
	a.pickerOpen = map[string]bool{"lf-model": true}
	tt.Frame()
	if !tt.HasText("Not yet available on Windows") {
		t.Errorf("Stories dialogue section missing Windows unavailable notice: %q", tt.Texts())
	}
	_ = tt.Click("Dialogue · Light")
	tt.Frame()
	if a.lfStories.doc.Production.Dialogue {
		t.Error("MLX-only dialogue model was turned on under Windows")
	}
}

// TestModelsPageWindowsCPUOnly verifies that on a Windows CPU-only PC
// (or ARM64 VM with software renderer), the Models page shows the
// friendly CPU hardware banner, displays gentle slow-on-CPU hints for
// larger models, shows Windows ONNX sizes, and refuses to download
// MLX-only weights.
func TestModelsPageWindowsCPUOnly(t *testing.T) {
	simulateWindows(t)
	a, _, tt := newTestApp(t)
	f := useFakeHub(t, a)
	a.sys = &sysInfo{
		OS:       "Windows 11 (26100)",
		Chip:     "ARMv8 Virtual CPU",
		Memory:   16 << 30,
		Arch:     "arm64",
		GPUClass: sysinfo.GPUClassCPUOnly,
		GPUs: []sysinfo.GPU{{
			Name:     "Microsoft Basic Render Driver",
			Vendor:   sysinfo.VendorMicrosoft,
			Software: true,
		}},
	}
	openModels(a)
	tt.SetSize(1100, 2400)
	tt.Frame()

	texts := strings.Join(tt.Texts(), "|")
	for _, want := range []string{
		"Running on CPU — lightweight models like Kokoro, Silero VAD, DeepFilterNet, and VOICEVOX work out of the box.",
		"Runs on CPU (slower without GPU acceleration)",
		"Windows planned",
		"Not yet available on Windows",
		"≈ 326 MB download", // Kokoro Windows ONNX size (not 481 MB MLX size)
	} {
		if !strings.Contains(texts, want) {
			t.Errorf("Windows CPU-only Models page missing %q", want)
		}
	}

	// Attempting to download an MLX-only model on Windows must be a no-op.
	mlxOnly := entry(t, "moss-ttsd-v1.0-8bit")
	a.download(mlxOnly)
	tt.Frame()
	if m := a.models[mlxOnly.ID]; m != nil && m.downloading {
		t.Error("MLX-only model started downloading on Windows")
	}
	if f.served[mlxOnly.Repo] != 0 {
		t.Errorf("hub served %d files for MLX-only repo on Windows", f.served[mlxOnly.Repo])
	}
}

// TestModelsPageWindowsIGPUBanner verifies the iGPU banner and that
// everyday models have no CPU slowdown warning on an iGPU PC.
func TestModelsPageWindowsIGPUBanner(t *testing.T) {
	simulateWindows(t)
	a, _, tt := newTestApp(t)
	useFakeHub(t, a)
	a.sys = &sysInfo{
		OS:       "Windows 11 (26100)",
		Memory:   16 << 30,
		GPUClass: sysinfo.GPUClassIntegrated,
		GPUs: []sysinfo.GPU{{
			Name:          "AMD Radeon 780M Graphics",
			Vendor:        sysinfo.VendorAMD,
			DedicatedVRAM: 512 << 20,
			SharedMemory:  8 << 30,
			Integrated:    true,
		}},
	}
	openModels(a)
	tt.SetSize(1100, 2400)
	tt.Frame()

	texts := strings.Join(tt.Texts(), "|")
	wantBanner := "Integrated graphics (AMD Radeon 780M Graphics) — lightweight models like Kokoro, Whisper, Qwen3 4B, and VOICEVOX run smoothly."
	if !strings.Contains(texts, wantBanner) {
		t.Errorf("expected iGPU banner %q in %s", wantBanner, texts)
	}
	if strings.Contains(texts, "Runs on CPU (slower without GPU acceleration)") {
		t.Error("unexpected CPU slowdown warning on an iGPU PC where core models run on iGPU")
	}
}

// TestWindowsHardwareFitNotes verifies gentle VRAM and RAM warning strings
// without assuming an NVIDIA card.
func TestWindowsHardwareFitNotes(t *testing.T) {
	simulateWindows(t)
	a, _, _ := newTestApp(t)
	a.sys = &sysInfo{
		OS:       "Windows 11 (26100)",
		Memory:   4 << 30,
		GPUClass: sysinfo.GPUClassIntegrated,
	}
	qwen4b := entry(t, "qwen3-4b-instruct-2507-4bit")
	if got := a.modelFitNote(qwen4b); got != "Needs 8 GB of memory; this PC has 4 GB" {
		t.Errorf("low-RAM note = %q", got)
	}
	heavyVRAM := catalog.Entry{
		ID:          "synthetic-8gb",
		MinMemoryGB: 12,
		Windows: &catalog.PlatformSource{
			Available: true,
			Backend:   catalog.BackendPyTorch,
			Repo:      "example/heavy",
			Size:      4_000_000_000,
			MinVRAMGB: 8,
			MinRAMGB:  16,
		},
	}
	a.sys = &sysInfo{
		OS:       "Windows 11 (26100)",
		Memory:   16 << 30,
		GPUClass: sysinfo.GPUClassDiscrete,
		GPUs: []sysinfo.GPU{{
			Name:          "NVIDIA GeForce GTX 1650",
			Vendor:        sysinfo.VendorNVIDIA,
			DedicatedVRAM: 4 << 30,
		}},
	}
	if got := a.modelFitNote(heavyVRAM); got != "Needs a GPU with 8 GB of video memory" {
		t.Errorf("low-VRAM note = %q", got)
	}
}
