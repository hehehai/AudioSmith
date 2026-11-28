package main

// Platform availability and everyday/iGPU hardware fit helpers for the
// model picker and Models settings page. On macOS ("darwin") and Linux
// dev builds, behaviour is 100% unchanged; on Windows, MLX-only entries
// are clearly labelled as unavailable and everyday iGPU / CPU-only PCs
// receive honest, gentle guidance without assuming an NVIDIA card.

import (
	"runtime"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/sysinfo"
)

// currentOS returns the active operating system; tests override it to
// simulate Windows environments on macOS or Linux.
var currentOS = func() string { return runtime.GOOS }

func (a *app) platformOS() string { return currentOS() }

func (a *app) hardwareInfo() sysinfo.Info {
	if a.sys == nil {
		return sysinfo.Info{}
	}
	return a.sys.toCatalogHW()
}

func (a *app) catalogHardware() catalog.Hardware {
	if a.sys == nil {
		return catalog.Hardware{}
	}
	hw := a.sys.toCatalogHW()
	cls := hw.GPUClass
	if cls == "" {
		cls = sysinfo.ClassifyGPUs(hw.GPUs)
	}
	var vramGB float64
	if best, ok := hw.BestGPU(); ok && !best.Integrated && !best.Software && best.DedicatedVRAM > 0 {
		vramGB = float64(best.DedicatedVRAM) / float64(1<<30)
	}
	return catalog.Hardware{
		MemoryGB:       float64(hw.MemoryGB()),
		VRAMGB:         vramGB,
		HasDiscreteGPU: cls == sysinfo.GPUClassDiscrete,
		HasIGPU:        cls == sysinfo.GPUClassIntegrated,
		HasCUDA:        hw.HasCUDA(),
	}
}

func (a *app) modelAvailable(e catalog.Entry) bool {
	return e.AvailableOn(a.platformOS())
}

func (a *app) modelFit(e catalog.Entry) catalog.FitLevel {
	return e.HardwareFit(a.platformOS(), a.catalogHardware())
}

func (a *app) effectiveModelSize(e catalog.Entry) int64 {
	return e.EffectiveSize(a.platformOS())
}

func (a *app) effectiveModelRepo(e catalog.Entry) string {
	return e.EffectiveRepo(a.platformOS())
}

func (a *app) effectiveModelRevision(e catalog.Entry) string {
	return e.EffectiveRevision(a.platformOS())
}

// modelFitNote returns a concise, gentle hardware hint for e on Windows,
// or "" when the model runs comfortably (and always "" on non-Windows so
// macOS pickers and cards stay unchanged).
func (a *app) modelFitNote(e catalog.Entry) string {
	goos := a.platformOS()
	if goos != "windows" {
		return ""
	}
	switch a.modelFit(e) {
	case catalog.FitUnavailable:
		return i18n.T("Not yet available on Windows")
	case catalog.FitSlowOnCPU:
		return i18n.T("Runs on CPU (slower without GPU acceleration)")
	case catalog.FitNeedsDiscrete:
		if w, ok := e.PlatformSourceFor(goos); ok && w.MinVRAMGB > 0 {
			return i18n.T("Needs a GPU with %g GB of video memory", w.MinVRAMGB)
		}
		return i18n.T("Needs a dedicated GPU")
	case catalog.FitLowMemory:
		minRAM := e.EffectiveMinRAMGB(goos)
		if a.sys != nil && a.sys.MemoryGB() > 0 {
			return i18n.T("Needs %g GB of memory; this PC has %d GB", minRAM, a.sys.MemoryGB())
		}
		return i18n.T("Needs %g GB of memory", minRAM)
	default:
		return ""
	}
}

// windowsHardwareBannerText describes the detected Windows hardware in a
// friendly, everyday-PC-first sentence ("" on non-Windows or before sysinfo
// is loaded).
func (a *app) windowsHardwareBannerText() string {
	if a.platformOS() != "windows" || a.sys == nil {
		return ""
	}
	hw := a.hardwareInfo()
	cls := hw.GPUClass
	if cls == "" {
		cls = sysinfo.ClassifyGPUs(hw.GPUs)
	}
	switch cls {
	case sysinfo.GPUClassIntegrated:
		if best, ok := hw.BestGPU(); ok && best.Name != "" {
			return i18n.T("Integrated graphics (%s) — lightweight models like Kokoro, Whisper, Qwen3 4B, and VOICEVOX run smoothly.", best.Name)
		}
		return i18n.T("Integrated graphics detected — lightweight models like Kokoro, Whisper, Qwen3 4B, and VOICEVOX run smoothly.")
	case sysinfo.GPUClassCPUOnly:
		return i18n.T("Running on CPU — lightweight models like Kokoro, Silero VAD, DeepFilterNet, and VOICEVOX work out of the box.")
	case sysinfo.GPUClassDiscrete:
		if best, ok := hw.BestGPU(); ok && best.Name != "" {
			vramGB := int((best.DedicatedVRAM + (1 << 29)) >> 30)
			if vramGB > 0 {
				return i18n.T("Graphics card detected (%s, %d GB VRAM).", best.Name, vramGB)
			}
			return i18n.T("Graphics card detected (%s).", best.Name)
		}
	}
	return ""
}

// windowsHardwareBanner draws a subtle hardware summary card at the top of
// the Models settings list on Windows.
func (a *app) windowsHardwareBanner(c *ui.Context) {
	text := a.windowsHardwareBannerText()
	if text == "" {
		return
	}
	t := c.Theme()
	k := tok(t)
	fillCard(c.Key("win-hw-banner")).Padding(px(t, sp2), px(t, sp3)).Children(func() {
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.InfoCircle.Linear).Size(px(t, 16), px(t, 16)).TextColor(k.Text2).Shrink(0)
			ui.Text(c, text).FontSize(px(t, fsCaption)).TextColor(k.Text2).Grow(1).MinWidth(0)
		})
	})
}
