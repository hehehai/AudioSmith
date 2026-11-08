package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// The licence of each component, as an SPDX expression, read from its
// licence files by hand. A new dependency fails the generator until it is
// added here; component.validate checks the main text still reads as this.

// goLicences are the Go modules' licences ("std": the standard library).
var goLicences = map[string]string{
	"std":                                    "BSD-3-Clause",
	"github.com/ebitengine/purego":           "Apache-2.0",
	"github.com/egoist/mygo":                 "MIT",
	"github.com/go-text/typesetting":         "Unlicense OR BSD-3-Clause",
	"github.com/google/jsonschema-go":        "MIT",
	"github.com/hehehai/actnowkit":           "Apache-2.0",
	"github.com/jfreymuth/oggvorbis":         "MIT",
	"github.com/jfreymuth/vorbis":            "MIT",
	"github.com/modelcontextprotocol/go-sdk": "Apache-2.0 AND MIT",
	"github.com/segmentio/asm":               "MIT-0",
	"github.com/segmentio/encoding":          "MIT",
	"github.com/yosida95/uritemplate/v3":     "BSD-3-Clause",
	"golang.org/x/image":                     "BSD-3-Clause",
	"golang.org/x/oauth2":                    "BSD-3-Clause",
	"golang.org/x/sync":                      "BSD-3-Clause",
	"golang.org/x/sys":                       "BSD-3-Clause",
	"golang.org/x/text":                      "BSD-3-Clause",
	"golang.org/x/time":                      "BSD-3-Clause",
}

// goNotes says more about some modules.
func goNotes(root, path string) []string {
	switch path {
	case "github.com/egoist/mygo":
		patches, _ := filepath.Glob(filepath.Join(root, "third_party", "mygo-patches", "*.patch"))
		return []string{fmt.Sprintf("AudioSmith builds against a copy in third_party/mygo with %d patches of its own (third_party/mygo-patches), made by scripts/vendor-mygo.sh.", len(patches))}
	}
	return nil
}

// swiftLicences are the Swift packages' licences (and their submodules').
var swiftLicences = map[string]string{
	"demucs-mlx-swift":       "MIT",
	"eventsource":            "MIT",
	"mel-roformer-mlx-swift": "MIT",
	"mlx":                    "MIT",
	"mlx-audio-swift":        "MIT",
	"mlx-c":                  "MIT",
	"mlx-swift":              "MIT",
	"mlx-swift-lm":           "MIT",
	"swift-argument-parser":  "Apache-2.0 WITH Swift-exception",
	"swift-asn1":             "Apache-2.0",
	"swift-collections":      "Apache-2.0 WITH Swift-exception",
	"swift-crypto":           "Apache-2.0",
	"swift-huggingface":      "Apache-2.0",
	"swift-jinja":            "Apache-2.0",
	"swift-numerics":         "Apache-2.0 WITH Swift-exception",
	"swift-syntax":           "Apache-2.0 WITH Swift-exception",
	"swift-transformers":     "Apache-2.0",
	"yyjson":                 "MIT",
}

// swiftNotes says more about some packages.
var swiftNotes = map[string][]string{
	"mlx":             {"MLX includes code from PocketFFT, ThreadPool, V8, NVIDIA CCCL (Thrust) and Norbert Juffa: their licence comments follow its licence."},
	"mlx-audio-swift": {"AudioSmith builds it with patches of its own (engine/patches/mlx-audio-swift, applied by scripts/fetch-engine-deps.sh). Patch 0011 backports upstream's VibeVoice ports from revision fe2c182 (MIT, Copyright (c) 2025 Prince Canuma and contributors, after Blaizzy/mlx-audio, MIT), with its Qwen2 backbone."},
	"mlx-swift":       {"It carries fmt, nlohmann/json and metal-cpp (Source/Cmlx), whose licences follow its own."},
	"mlx-swift-lm":    {"It carries xgrammar (Libraries/MLXCXGrammar/xgrammar, Apache-2.0, with its NOTICE) and xgrammar's copy of picojson."},
	"swift-crypto":    {"On macOS it uses Apple's CryptoKit: the BoringSSL and XKCP code in its source is built only on other systems."},
	"swift-syntax":    {"Used when the engine is built, by the Swift macros of mlx-swift-lm; not linked into the engine."},
}

// swiftExcerpts are licence comments in source files that the packages'
// licence files do not carry (fetch.go cuts them at the pin).
var swiftExcerpts = map[string][]excerpt{
	"mlx": {
		{"mlx/3rdparty/pocketfft.h", "This file is part of pocketfft.", "SUCH DAMAGE."},
		{"mlx/backend/metal/kernels/cexpf.h", "Copyright © 2008-2013 NVIDIA Corporation", "can not be used in JIT."},
		{"mlx/backend/metal/kernels/expm1f.h", "Original license copied below:", "SUCH DAMAGE."},
		{"mlx/small_vector.h", "Copyright © 2018 the V8 project authors.", "SUCH DAMAGE."},
		{"mlx/threadpool.h", "This code was modified from https://github.com/progschj/ThreadPool", "distribution."},
	},
	"mlx-swift-lm": {
		{"Libraries/MLXCXGrammar/xgrammar/3rdparty/picojson/picojson.h", "Copyright 2009-2010 Cybozu Labs, Inc.", "POSSIBILITY OF SUCH DAMAGE."},
	},
}

// readFile reads a file of the repository.
func readFile(root, rel string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	return string(data), err
}
