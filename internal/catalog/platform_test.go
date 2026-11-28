package catalog

import (
	"slices"
	"strings"
	"testing"
)

// TestMacOSCatalogUnchanged verifies that macOS ("darwin") keeps 100% of
// existing entries, repos, revisions, sizes, files and task lists intact.
func TestMacOSCatalogUnchanged(t *testing.T) {
	c := Builtin()
	for _, e := range c.Entries {
		if !e.AvailableOn("darwin") {
			t.Errorf("%s: not available on darwin", e.ID)
		}
		if got := e.EffectiveRepo("darwin"); got != e.Repo {
			t.Errorf("%s: EffectiveRepo(darwin) = %q, want %q", e.ID, got, e.Repo)
		}
		if got := e.EffectiveRevision("darwin"); got != e.Revision {
			t.Errorf("%s: EffectiveRevision(darwin) = %q, want %q", e.ID, got, e.Revision)
		}
		if got := e.EffectiveSize("darwin"); got != e.Size {
			t.Errorf("%s: EffectiveSize(darwin) = %d, want %d", e.ID, got, e.Size)
		}
		if !slices.Equal(e.EffectiveFiles("darwin"), e.Files) {
			t.Errorf("%s: EffectiveFiles(darwin) differs from e.Files", e.ID)
		}
		if _, ok := e.PlatformSourceFor("darwin"); ok {
			t.Errorf("%s: unexpected PlatformSourceFor(darwin)", e.ID)
		}
	}
	if len(c.ForTaskOn(TaskTTS, "darwin")) != len(c.ForTask(TaskTTS)) {
		t.Errorf("ForTaskOn(TTS, darwin) != ForTask(TTS)")
	}
}

// TestWindowsCatalogSources verifies the everyday/iGPU-first core Windows
// sources resolve to non-MLX repos with pinned revisions & files, and that
// MLX-only entries are explicitly unavailable and never expose MLX repos.
func TestWindowsCatalogSources(t *testing.T) {
	c := Builtin()
	wantAvailable := map[string]Backend{
		"kokoro-82m-bf16":             BackendONNXDirectML,
		"whisper-large-v3-turbo-4bit": BackendLlamaCpp,
		"silero-vad":                  BackendONNXCPU,
		"deepfilternet-v3":            BackendONNXCPU,
		"qwen3-4b-instruct-2507-4bit": BackendLlamaCpp,
		"audioseal-16bits":            BackendONNXCPU,
		"voicevox":                    BackendExternal,
	}

	for id, wantBackend := range wantAvailable {
		e, ok := c.ByID(id)
		if !ok {
			t.Fatalf("missing entry %s", id)
		}
		if !e.AvailableOn("windows") {
			t.Errorf("%s: want AvailableOn(windows) == true", id)
		}
		if got := e.EffectiveBackend("windows"); got != wantBackend {
			t.Errorf("%s: backend = %q, want %q", id, got, wantBackend)
		}
		repo := e.EffectiveRepo("windows")
		if repo == "" {
			t.Errorf("%s: EffectiveRepo(windows) is empty", id)
		}
		if strings.HasPrefix(repo, "mlx-community/") {
			t.Errorf("%s: Windows repo %q must not be an MLX repo", id, repo)
		}
		if !e.IsExternal() {
			if e.EffectiveRevision("windows") == "" {
				t.Errorf("%s: Windows source must pin a revision", id)
			}
			if e.EffectiveSize("windows") <= 0 {
				t.Errorf("%s: Windows source must have positive size", id)
			}
			files := e.EffectiveFiles("windows")
			if len(files) == 0 {
				t.Errorf("%s: Windows source must list files", id)
			}
			var sum int64
			for _, f := range files {
				sum += f.Size
			}
			if sum != e.EffectiveSize("windows") {
				t.Errorf("%s: Windows file sum %d != size %d", id, sum, e.EffectiveSize("windows"))
			}
			if found, ok := c.ByEffectiveRepo("windows", repo); !ok || found.ID != id {
				t.Errorf("ByEffectiveRepo(windows, %q) = (%s, %v), want %s", repo, found.ID, ok, id)
			}
		}
	}

	// Optional Python/PyTorch neural TTS models (Chinese & Japanese):
	// available on Windows with explicit optionalRuntime: "python",
	// runsOnCPU: true, and honest VRAM/RAM requirements.
	wantPyTorch := map[string]struct {
		repo      string
		minVRAMGB float64
	}{
		"qwen3-tts-0.6b-customvoice-8bit": {"Qwen/Qwen3-TTS-12Hz-0.6B-CustomVoice", 4},
		"qwen3-tts-1.7b-base-8bit":        {"Qwen/Qwen3-TTS-12Hz-1.7B-Base", 6},
		"irodori-tts-v4.1-small-mf-8bit":  {"Aratako/Irodori-TTS-v4.1-Small-MF", 4},
	}
	for id, want := range wantPyTorch {
		e, ok := c.ByID(id)
		if !ok {
			t.Fatalf("missing entry %s", id)
		}
		if !e.AvailableOn("windows") {
			t.Errorf("%s: want AvailableOn(windows) == true", id)
		}
		if got := e.EffectiveBackend("windows"); got != BackendPyTorch {
			t.Errorf("%s: backend = %q, want %q", id, got, BackendPyTorch)
		}
		if got := e.EffectiveOptionalRuntime("windows"); got != "python" {
			t.Errorf("%s: optionalRuntime = %q, want python", id, got)
		}
		if got := e.EffectiveRepo("windows"); got != want.repo {
			t.Errorf("%s: repo = %q, want %q", id, got, want.repo)
		}
		if e.EffectiveSize("windows") <= 0 {
			t.Errorf("%s: size must be positive", id)
		}
		if !e.Windows.RunsOnCPU || e.Windows.RunsOnIGPU {
			t.Errorf("%s: want RunsOnCPU=true, RunsOnIGPU=false, got %+v", id, e.Windows)
		}
		if e.Windows.MinVRAMGB != want.minVRAMGB {
			t.Errorf("%s: minVRAMGB = %g, want %g", id, e.Windows.MinVRAMGB, want.minVRAMGB)
		}
		if found, ok := c.ByEffectiveRepo("windows", want.repo); !ok || found.ID != id {
			t.Errorf("ByEffectiveRepo(windows, %q) = (%s, %v), want %s", want.repo, found.ID, ok, id)
		}
	}

	// Heavier MLX-only entries must be marked unavailable on Windows and
	// must never expose their MLX repo or size on Windows.
	for _, id := range []string{
		"moss-ttsd-v1.0-8bit",
		"soulx-podcast-1.7b-8bit",
		"voxcpm2-8bit",
		"chatterbox-turbo-8bit",
		"omnivoice-bf16",
	} {
		e, ok := c.ByID(id)
		if !ok {
			t.Fatalf("missing entry %s", id)
		}
		if e.AvailableOn("windows") {
			t.Errorf("%s: MLX-only model should not be available on Windows", id)
		}
		if repo := e.EffectiveRepo("windows"); repo != "" {
			t.Errorf("%s: EffectiveRepo(windows) = %q, want empty", id, repo)
		}
		if sz := e.EffectiveSize("windows"); sz != 0 {
			t.Errorf("%s: EffectiveSize(windows) = %d, want 0", id, sz)
		}
	}

	// ForTaskOn(TaskTTS, "windows") returns Kokoro + optional PyTorch zh/ja TTS models.
	winTTS := c.ForTaskOn(TaskTTS, "windows")
	var winTTSIDs []string
	for _, e := range winTTS {
		winTTSIDs = append(winTTSIDs, e.ID)
	}
	wantTTS := []string{
		"kokoro-82m-bf16",
		"qwen3-tts-0.6b-customvoice-8bit",
		"qwen3-tts-1.7b-base-8bit",
		"irodori-tts-v4.1-small-mf-8bit",
	}
	if !slices.Equal(winTTSIDs, wantTTS) {
		t.Errorf("ForTaskOn(TTS, windows) = %v, want %v", winTTSIDs, wantTTS)
	}
}

// TestHardwareFit verifies classification across everyday Windows iGPU,
// CPU-only PC/VM, 8 GB NVIDIA GPU, low-VRAM discrete GPU, low-RAM PC,
// and macOS Apple Silicon.
func TestHardwareFit(t *testing.T) {
	c := Builtin()
	kokoro, _ := c.ByID("kokoro-82m-bf16")
	vad, _ := c.ByID("silero-vad")
	dfn, _ := c.ByID("deepfilternet-v3")
	whisper, _ := c.ByID("whisper-large-v3-turbo-4bit")
	qwen4b, _ := c.ByID("qwen3-4b-instruct-2507-4bit")
	audioseal, _ := c.ByID("audioseal-16bits")
	voicevox, _ := c.ByID("voicevox")
	moss, _ := c.ByID("moss-ttsd-v1.0-8bit")
	qwenTTS06, _ := c.ByID("qwen3-tts-0.6b-customvoice-8bit")
	qwenTTS17, _ := c.ByID("qwen3-tts-1.7b-base-8bit")
	irodori, _ := c.ByID("irodori-tts-v4.1-small-mf-8bit")

	// Synthetic PyTorch CUDA-only model (e.g. future Phase 2 heavy TTS).
	heavyCUDA := Entry{
		ID:          "synthetic-heavy-tts",
		MinMemoryGB: 12,
		Windows: &PlatformSource{
			Available:  true,
			Backend:    BackendPyTorch,
			Repo:       "example/heavy-tts",
			Size:       4_000_000_000,
			MinVRAMGB:  8,
			MinRAMGB:   16,
			RunsOnIGPU: false,
			RunsOnCPU:  false,
		},
	}

	hwWinIGPU := Hardware{MemoryGB: 16, HasIGPU: true}
	hwWinCPU := Hardware{MemoryGB: 16}
	hwWinNVIDIA8GB := Hardware{MemoryGB: 16, VRAMGB: 8, HasDiscreteGPU: true, HasCUDA: true}
	hwWinDiscrete4GB := Hardware{MemoryGB: 16, VRAMGB: 4, HasDiscreteGPU: true, HasCUDA: true}
	hwWinLowRAM := Hardware{MemoryGB: 4, HasIGPU: true}
	hwMacM416GB := Hardware{MemoryGB: 16}
	hwMacM424GB := Hardware{MemoryGB: 24}

	cases := []struct {
		name string
		e    Entry
		goos string
		hw   Hardware
		want FitLevel
	}{
		// Windows Intel/AMD iGPU (16 GB): core models run comfortably.
		{"win-igpu/kokoro", kokoro, "windows", hwWinIGPU, FitComfortable},
		{"win-igpu/vad", vad, "windows", hwWinIGPU, FitComfortable},
		{"win-igpu/dfn", dfn, "windows", hwWinIGPU, FitComfortable},
		{"win-igpu/whisper", whisper, "windows", hwWinIGPU, FitComfortable},
		{"win-igpu/qwen4b", qwen4b, "windows", hwWinIGPU, FitComfortable},
		{"win-igpu/audioseal", audioseal, "windows", hwWinIGPU, FitComfortable},
		{"win-igpu/voicevox", voicevox, "windows", hwWinIGPU, FitComfortable},
		{"win-igpu/moss-mlx", moss, "windows", hwWinIGPU, FitUnavailable},
		{"win-igpu/heavy-cuda", heavyCUDA, "windows", hwWinIGPU, FitNeedsDiscrete},
		{"win-igpu/qwen3-tts-0.6b", qwenTTS06, "windows", hwWinIGPU, FitSlowOnCPU},
		{"win-igpu/qwen3-tts-1.7b", qwenTTS17, "windows", hwWinIGPU, FitSlowOnCPU},
		{"win-igpu/irodori", irodori, "windows", hwWinIGPU, FitSlowOnCPU},

		// Windows CPU-only / VM (16 GB): light models comfortable, larger ones slow-on-CPU.
		{"win-cpu/kokoro", kokoro, "windows", hwWinCPU, FitComfortable},
		{"win-cpu/vad", vad, "windows", hwWinCPU, FitComfortable},
		{"win-cpu/dfn", dfn, "windows", hwWinCPU, FitComfortable},
		{"win-cpu/audioseal", audioseal, "windows", hwWinCPU, FitComfortable},
		{"win-cpu/voicevox", voicevox, "windows", hwWinCPU, FitComfortable},
		{"win-cpu/whisper", whisper, "windows", hwWinCPU, FitSlowOnCPU},
		{"win-cpu/qwen4b", qwen4b, "windows", hwWinCPU, FitSlowOnCPU},
		{"win-cpu/qwen3-tts-0.6b", qwenTTS06, "windows", hwWinCPU, FitSlowOnCPU},
		{"win-cpu/irodori", irodori, "windows", hwWinCPU, FitSlowOnCPU},
		{"win-cpu/moss-mlx", moss, "windows", hwWinCPU, FitUnavailable},
		{"win-cpu/heavy-cuda", heavyCUDA, "windows", hwWinCPU, FitNeedsDiscrete},

		// Windows NVIDIA 8 GB: core models + 8 GB CUDA model comfortable, MLX-only still unavailable.
		{"win-rtx8g/kokoro", kokoro, "windows", hwWinNVIDIA8GB, FitComfortable},
		{"win-rtx8g/qwen4b", qwen4b, "windows", hwWinNVIDIA8GB, FitComfortable},
		{"win-rtx8g/qwen3-tts-0.6b", qwenTTS06, "windows", hwWinNVIDIA8GB, FitComfortable},
		{"win-rtx8g/qwen3-tts-1.7b", qwenTTS17, "windows", hwWinNVIDIA8GB, FitComfortable},
		{"win-rtx8g/irodori", irodori, "windows", hwWinNVIDIA8GB, FitComfortable},
		{"win-rtx8g/heavy-cuda", heavyCUDA, "windows", hwWinNVIDIA8GB, FitComfortable},
		{"win-rtx8g/moss-mlx", moss, "windows", hwWinNVIDIA8GB, FitUnavailable},

		// Windows 4 GB discrete GPU: 4 GB models fit, 6/8 GB models need more VRAM.
		{"win-4g/qwen4b", qwen4b, "windows", hwWinDiscrete4GB, FitComfortable},
		{"win-4g/qwen3-tts-0.6b", qwenTTS06, "windows", hwWinDiscrete4GB, FitComfortable},
		{"win-4g/qwen3-tts-1.7b", qwenTTS17, "windows", hwWinDiscrete4GB, FitNeedsDiscrete},
		{"win-4g/irodori", irodori, "windows", hwWinDiscrete4GB, FitComfortable},
		{"win-4g/heavy-cuda", heavyCUDA, "windows", hwWinDiscrete4GB, FitNeedsDiscrete},

		// Windows 4 GB RAM: Qwen3 4B (needs 8 GB RAM) reports low memory; Kokoro (2 GB) fits.
		{"win-4gram/kokoro", kokoro, "windows", hwWinLowRAM, FitComfortable},
		{"win-4gram/qwen4b", qwen4b, "windows", hwWinLowRAM, FitLowMemory},

		// macOS Apple Silicon: unified memory semantics unchanged.
		{"mac-16g/kokoro", kokoro, "darwin", hwMacM416GB, FitComfortable},
		{"mac-16g/moss", moss, "darwin", hwMacM416GB, FitLowMemory},
		{"mac-24g/moss", moss, "darwin", hwMacM424GB, FitComfortable},
	}

	for _, tc := range cases {
		if got := tc.e.HardwareFit(tc.goos, tc.hw); got != tc.want {
			t.Errorf("%s: HardwareFit = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestValidatePlatformSource checks that invalid Windows metadata is rejected.
func TestValidatePlatformSource(t *testing.T) {
	base := *Builtin()
	bad := func(ps *PlatformSource) error {
		c := base
		c.Entries = slices.Clone(base.Entries)
		c.Entries[0].Windows = ps
		return c.Validate()
	}
	if err := bad(&PlatformSource{Available: true, Backend: "unknown", Repo: "a/b", Size: 1}); err == nil {
		t.Error("unknown backend validated")
	}
	if err := bad(&PlatformSource{Available: true, Backend: BackendONNXCPU, Repo: "", Size: 1}); err == nil {
		t.Error("missing repo validated")
	}
	if err := bad(&PlatformSource{Available: true, Backend: BackendONNXCPU, Repo: "a/b", Size: 0}); err == nil {
		t.Error("missing size and files validated")
	}
	if err := bad(&PlatformSource{Available: false, MinVRAMGB: -1}); err == nil {
		t.Error("negative minVRAMGB validated")
	}
}
