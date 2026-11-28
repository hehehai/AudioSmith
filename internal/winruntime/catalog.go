// Package winruntime discovers and manages lightweight native inference
// runtimes on Windows (ONNX Runtime DirectML, whisper.cpp, llama.cpp) so
// everyday/iGPU PC users never have to manually hunt for DLLs or EXEs.
package winruntime

import "slices"

// Name identifies a managed native runtime.
type Name string

const (
	// RuntimeONNX is ONNX Runtime with DirectML + CPU Execution Providers.
	RuntimeONNX Name = "onnx"
	// RuntimeWhisper is whisper.cpp CLI/server for local STT.
	RuntimeWhisper Name = "whisper"
	// RuntimeLlama is llama.cpp server/CLI for local GGUF LLMs.
	RuntimeLlama Name = "llama"
)

// OrderedNames returns the lightweight native runtimes managed by winruntime
// in deterministic display order. Optional PyTorch/Python runtimes are never
// included here so everyday iGPU/CPU PCs are never forced to download them.
func OrderedNames() []Name {
	return []Name{RuntimeONNX, RuntimeWhisper, RuntimeLlama}
}

// RequiredForBackend returns the lightweight native runtime needed to run a
// Windows model with the given catalog backend and task list, or "" when no
// managed native runtime is required (e.g. external engines or opt-in PyTorch).
func RequiredForBackend(backend string, tasks []string) Name {
	switch backend {
	case "onnx_directml", "onnx_cpu":
		return RuntimeONNX
	case "llamacpp":
		if slices.Contains(tasks, "stt") {
			return RuntimeWhisper
		}
		return RuntimeLlama
	default:
		return ""
	}
}

// ExtractSpec maps an archive entry (slash-separated path, matched
// case-insensitively or by exact suffix) to a flat filename in the
// runtime's install directory.
type ExtractSpec struct {
	ArchivePath string // exact slash path inside ZIP (case-insensitive match)
	DestName    string // destination filename inside <runtimes>/<name>/
	Optional    bool   // when true, absence in the archive is not an error
}

// Asset is one downloadable pinned archive for a specific CPU architecture.
type Asset struct {
	URL     string
	SHA256  string // lowercase hex; verified when non-empty
	Size    int64  // approximate size in bytes (for UI/progress fallback)
	Extract []ExtractSpec
}

// Spec describes a managed runtime across Windows architectures.
type Spec struct {
	Name          Name
	Version       string
	DisplayName   string
	RequiredFiles []string         // filenames that must exist for Status().Installed == true
	Aliases       []string         // alternate subdir names searched by Discover
	Assets        map[string]Asset // keyed by GOARCH ("amd64", "arm64")
}

// DefaultCatalog returns the pinned official runtime releases for Windows.
// Callers and tests may clone and override entries via Options.Catalog.
func DefaultCatalog() map[Name]Spec {
	const onnxVer = "1.24.4"
	const onnxURL = "https://www.nuget.org/api/v2/package/Microsoft.ML.OnnxRuntime.DirectML/" + onnxVer
	const onnxSHA256 = "57e9f11b73437bef7a309496135d4c1f96b1a8e9ddba60013fa27bfc1d788681"

	return map[Name]Spec{
		RuntimeONNX: {
			Name:          RuntimeONNX,
			Version:       onnxVer,
			DisplayName:   "ONNX Runtime (DirectML + CPU)",
			RequiredFiles: []string{"onnxruntime.dll"},
			Aliases:       []string{"onnxruntime"},
			Assets: map[string]Asset{
				"amd64": {
					URL:    onnxURL,
					SHA256: onnxSHA256,
					Size:   55 << 20,
					Extract: []ExtractSpec{
						{ArchivePath: "runtimes/win-x64/native/onnxruntime.dll", DestName: "onnxruntime.dll"},
						{ArchivePath: "runtimes/win-x64/native/DirectML.dll", DestName: "DirectML.dll", Optional: true},
					},
				},
				"arm64": {
					URL:    onnxURL,
					SHA256: onnxSHA256,
					Size:   55 << 20,
					Extract: []ExtractSpec{
						{ArchivePath: "runtimes/win-arm64/native/onnxruntime.dll", DestName: "onnxruntime.dll"},
						{ArchivePath: "runtimes/win-arm64/native/DirectML.dll", DestName: "DirectML.dll", Optional: true},
					},
				},
			},
		},
		RuntimeWhisper: {
			Name:          RuntimeWhisper,
			Version:       "1.7.6",
			DisplayName:   "whisper.cpp",
			RequiredFiles: []string{"whisper-cli.exe"},
			Aliases:       []string{"whisper.cpp"},
			Assets: map[string]Asset{
				"amd64": {
					URL:  "https://github.com/ggml-org/whisper.cpp/releases/download/v1.7.6/whisper-bin-x64.zip",
					Size: 12 << 20,
					Extract: []ExtractSpec{
						{ArchivePath: "Release/whisper-cli.exe", DestName: "whisper-cli.exe"},
						{ArchivePath: "Release/whisper.dll", DestName: "whisper.dll", Optional: true},
						{ArchivePath: "Release/ggml.dll", DestName: "ggml.dll", Optional: true},
						{ArchivePath: "Release/ggml-base.dll", DestName: "ggml-base.dll", Optional: true},
						{ArchivePath: "Release/ggml-cpu.dll", DestName: "ggml-cpu.dll", Optional: true},
					},
				},
			},
		},
		RuntimeLlama: {
			Name:          RuntimeLlama,
			Version:       "b5400",
			DisplayName:   "llama.cpp (Vulkan + CPU)",
			RequiredFiles: []string{"llama-server.exe"},
			Aliases:       []string{"llama.cpp"},
			Assets: map[string]Asset{
				"amd64": {
					URL:  "https://github.com/ggml-org/llama.cpp/releases/download/b5400/llama-b5400-bin-win-vulkan-x64.zip",
					Size: 40 << 20,
					Extract: []ExtractSpec{
						{ArchivePath: "llama-server.exe", DestName: "llama-server.exe"},
						{ArchivePath: "llama-cli.exe", DestName: "llama-cli.exe", Optional: true},
						{ArchivePath: "llama.dll", DestName: "llama.dll", Optional: true},
						{ArchivePath: "ggml.dll", DestName: "ggml.dll", Optional: true},
						{ArchivePath: "ggml-base.dll", DestName: "ggml-base.dll", Optional: true},
						{ArchivePath: "ggml-cpu.dll", DestName: "ggml-cpu.dll", Optional: true},
						{ArchivePath: "ggml-vulkan.dll", DestName: "ggml-vulkan.dll", Optional: true},
					},
				},
				"arm64": {
					URL:  "https://github.com/ggml-org/llama.cpp/releases/download/b5400/llama-b5400-bin-win-cpu-arm64.zip",
					Size: 25 << 20,
					Extract: []ExtractSpec{
						{ArchivePath: "llama-server.exe", DestName: "llama-server.exe"},
						{ArchivePath: "llama-cli.exe", DestName: "llama-cli.exe", Optional: true},
						{ArchivePath: "llama.dll", DestName: "llama.dll", Optional: true},
						{ArchivePath: "ggml.dll", DestName: "ggml.dll", Optional: true},
						{ArchivePath: "ggml-base.dll", DestName: "ggml-base.dll", Optional: true},
						{ArchivePath: "ggml-cpu.dll", DestName: "ggml-cpu.dll", Optional: true},
					},
				},
			},
		},
	}
}
