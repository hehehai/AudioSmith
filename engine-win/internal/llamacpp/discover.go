// Package llamacpp drives local llama.cpp (llama-server / llama-cli) and
// whisper.cpp (whisper-cli / whisper-server) subprocesses with iGPU-first
// Vulkan / CPU selection and runtime discovery.
package llamacpp

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/hehehai/AudioSmith/engine-win/hw"
)

// SelectProvider chooses the llama.cpp / whisper.cpp compute provider from hwInfo.
// Everyday Intel/AMD iGPUs use Vulkan when available; otherwise pure CPU.
func SelectProvider(hwInfo hw.Info) string {
	switch hwInfo.GPUTier {
	case hw.TierCUDA:
		return "cuda"
	case hw.TierVulkan:
		return "vulkan"
	case hw.TierDirectML:
		if hwInfo.Capabilities.Vulkan {
			return "vulkan"
		}
	}
	return "cpu"
}

// GPULayers returns the recommended -ngl value for llama.cpp based on hwInfo.
func GPULayers(hwInfo hw.Info) int {
	switch SelectProvider(hwInfo) {
	case "cuda", "vulkan":
		return 99
	default:
		return 0
	}
}

// RecommendedThreads chooses a polite CPU thread count for everyday PCs.
func RecommendedThreads(hwInfo hw.Info) int {
	n := hwInfo.NumCPU
	if n <= 0 {
		n = runtime.NumCPU()
	}
	if n > 8 {
		return n / 2
	}
	if n > 2 {
		return n - 1
	}
	return max(1, n)
}

// DiscoverLlamaServer finds llama-server(.exe).
func DiscoverLlamaServer() string {
	return discoverBinary("AUDIOSMITH_LLAMA_SERVER", "llama", "llama-server")
}

// DiscoverLlamaCLI finds llama-cli(.exe).
func DiscoverLlamaCLI() string {
	return discoverBinary("AUDIOSMITH_LLAMA_CLI", "llama", "llama-cli")
}

// DiscoverWhisperCLI finds whisper-cli(.exe) (or legacy main(.exe) in whisper runtime dir).
func DiscoverWhisperCLI() string {
	return discoverBinary("AUDIOSMITH_WHISPER_CLI", "whisper", "whisper-cli")
}

// DiscoverWhisperServer finds whisper-server(.exe).
func DiscoverWhisperServer() string {
	return discoverBinary("AUDIOSMITH_WHISPER_SERVER", "whisper", "whisper-server")
}

func discoverBinary(envKey, runtimeSubdir, baseName string) string {
	if p := strings.TrimSpace(os.Getenv(envKey)); p != "" {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}

	names := []string{baseName}
	if runtime.GOOS == "windows" {
		names = []string{baseName + ".exe", baseName}
	}

	// 1. Beside the running audiosmith-engine binary.
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		for _, n := range names {
			cand := filepath.Join(exeDir, n)
			if info, err := os.Stat(cand); err == nil && !info.IsDir() {
				return cand
			}
		}
	}

	// 2. Managed runtime directory: %LOCALAPPDATA%\ActNow\runtimes\<subdir>\
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		for _, sub := range []string{runtimeSubdir, runtimeSubdir + ".cpp"} {
			for _, n := range names {
				cand := filepath.Join(local, "ActNow", "runtimes", sub, n)
				if info, err := os.Stat(cand); err == nil && !info.IsDir() {
					return cand
				}
			}
		}
	}

	// 3. System PATH.
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}

// ResolveModelFile resolves a model path (file or directory) to a model file
// matching any of exts (e.g. ".gguf", ".bin").
func ResolveModelFile(modelPath string, exts ...string) string {
	if modelPath == "" {
		return ""
	}
	info, err := os.Stat(modelPath)
	if err != nil {
		return ""
	}
	if !info.IsDir() {
		return modelPath
	}
	entries, err := os.ReadDir(modelPath)
	if err != nil {
		return ""
	}
	for _, ext := range exts {
		for _, e := range entries {
			if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ext) {
				return filepath.Join(modelPath, e.Name())
			}
		}
	}
	return ""
}
