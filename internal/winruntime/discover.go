package winruntime

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// RuntimesDir returns the default managed runtime root directory:
//  1. $AUDIOSMITH_RUNTIMES_DIR (explicit override for portable/test setups)
//  2. sibling "runtimes" beside $ACTNOW_MODELS_DIR (isolated test/custom dirs)
//  3. $ACTNOW_FAMILY_DIR/runtimes
//  4. %LOCALAPPDATA%\ActNow\runtimes
//  5. <UserCacheDir>/ActNow/runtimes
func RuntimesDir() string {
	if d := strings.TrimSpace(os.Getenv("AUDIOSMITH_RUNTIMES_DIR")); d != "" {
		return filepath.Clean(d)
	}
	if m := strings.TrimSpace(os.Getenv("ACTNOW_MODELS_DIR")); m != "" {
		return filepath.Join(filepath.Dir(filepath.Clean(m)), "runtimes")
	}
	if f := strings.TrimSpace(os.Getenv("ACTNOW_FAMILY_DIR")); f != "" {
		return filepath.Join(filepath.Clean(f), "runtimes")
	}
	if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
		return filepath.Join(local, "ActNow", "runtimes")
	}
	if cache, err := os.UserCacheDir(); err == nil && cache != "" {
		return filepath.Join(cache, "ActNow", "runtimes")
	}
	return filepath.Join(os.TempDir(), "ActNow", "runtimes")
}

// RuntimeDir returns the install directory for runtime name under baseDir
// (or RuntimesDir() when baseDir is empty).
func RuntimeDir(baseDir string, name Name) string {
	if baseDir == "" {
		baseDir = RuntimesDir()
	}
	return filepath.Join(baseDir, string(name))
}

// DiscoverOptions customises binary/DLL discovery (mostly for tests).
type DiscoverOptions struct {
	EnvVar     string   // optional env var pointing directly to the file
	BaseDir    string   // override RuntimesDir()
	ExeDir     string   // override filepath.Dir(os.Executable())
	Aliases    []string // alternate subdirectory names under BaseDir
	SearchPATH bool     // also check system PATH via exec.LookPath
}

// Discover searches for any of binaryNames in order:
//  1. opts.EnvVar (direct file path)
//  2. Beside the running executable (ExeDir and ExeDir/runtimes/<name>)
//  3. Managed runtime directory (<BaseDir>/<name> and <BaseDir>/<alias>)
//  4. System PATH (when opts.SearchPATH is true)
func Discover(name Name, binaryNames []string, opts DiscoverOptions) string {
	if opts.EnvVar != "" {
		if p := strings.TrimSpace(os.Getenv(opts.EnvVar)); p != "" && isRegularFile(p) {
			return p
		}
	}

	exeDir := opts.ExeDir
	if exeDir == "" {
		if exe, err := os.Executable(); err == nil {
			exeDir = filepath.Dir(exe)
		}
	}
	if exeDir != "" {
		for _, dir := range []string{exeDir, filepath.Join(exeDir, "runtimes", string(name))} {
			if found := firstFileInDir(dir, binaryNames); found != "" {
				return found
			}
		}
	}

	baseDir := opts.BaseDir
	if baseDir == "" {
		baseDir = RuntimesDir()
	}
	subdirs := append([]string{string(name)}, opts.Aliases...)
	for _, sub := range subdirs {
		if found := firstFileInDir(filepath.Join(baseDir, sub), binaryNames); found != "" {
			return found
		}
	}

	if opts.SearchPATH {
		for _, n := range binaryNames {
			if p, err := exec.LookPath(n); err == nil {
				return p
			}
		}
	}
	return ""
}

// DiscoverONNXDLL finds onnxruntime.dll (or platform shared lib in dev).
func DiscoverONNXDLL() string {
	return Discover(RuntimeONNX, []string{"onnxruntime.dll", "libonnxruntime.so", "libonnxruntime.dylib"}, DiscoverOptions{
		EnvVar:  "AUDIOSMITH_ONNXRUNTIME",
		Aliases: []string{"onnxruntime"},
	})
}

// DiscoverLlamaServer finds llama-server(.exe).
func DiscoverLlamaServer() string {
	return Discover(RuntimeLlama, exeNames("llama-server"), DiscoverOptions{
		EnvVar:     "AUDIOSMITH_LLAMA_SERVER",
		Aliases:    []string{"llama.cpp"},
		SearchPATH: true,
	})
}

// DiscoverLlamaCLI finds llama-cli(.exe).
func DiscoverLlamaCLI() string {
	return Discover(RuntimeLlama, exeNames("llama-cli"), DiscoverOptions{
		EnvVar:     "AUDIOSMITH_LLAMA_CLI",
		Aliases:    []string{"llama.cpp"},
		SearchPATH: true,
	})
}

// DiscoverWhisperCLI finds whisper-cli(.exe).
func DiscoverWhisperCLI() string {
	return Discover(RuntimeWhisper, exeNames("whisper-cli"), DiscoverOptions{
		EnvVar:     "AUDIOSMITH_WHISPER_CLI",
		Aliases:    []string{"whisper.cpp"},
		SearchPATH: true,
	})
}

// DiscoverWhisperServer finds whisper-server(.exe).
func DiscoverWhisperServer() string {
	return Discover(RuntimeWhisper, exeNames("whisper-server"), DiscoverOptions{
		EnvVar:     "AUDIOSMITH_WHISPER_SERVER",
		Aliases:    []string{"whisper.cpp"},
		SearchPATH: true,
	})
}

func exeNames(base string) []string {
	if runtime.GOOS == "windows" {
		return []string{base + ".exe", base}
	}
	return []string{base, base + ".exe"}
}

func firstFileInDir(dir string, names []string) string {
	for _, n := range names {
		cand := filepath.Join(dir, n)
		if isRegularFile(cand) {
			return cand
		}
	}
	return ""
}

func isRegularFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
