// Command audiosmith-engine is AudioSmith's Windows hybrid engine binary
// implementing docs/protocol-v2.md over stdio for roles gpu, listen, and media.
//
// It cross-compiles with CGO_ENABLED=0 to windows/amd64 and windows/arm64 and
// follows an everyday / iGPU-first design: works out of the box on Intel/AMD
// integrated GPUs and CPU-only PCs without requiring a 2.5 GB CUDA download.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/hehehai/AudioSmith/engine-win/gpu"
	"github.com/hehehai/AudioSmith/engine-win/hw"
	"github.com/hehehai/AudioSmith/engine-win/listen"
	"github.com/hehehai/AudioSmith/engine-win/media"
	"github.com/hehehai/AudioSmith/engine-win/server"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

// Version is overridden at build time via -ldflags "-X main.Version=...".
var Version = "0.1.0-win"

func main() {
	roleFlag := flag.String("role", string(proto.RoleGPU), "engine role: gpu | listen | media")
	showVersion := flag.Bool("version", false, "print engine version and exit")
	probeHW := flag.Bool("probe-hw", false, "print hardware capability JSON and exit")
	ffmpegPath := flag.String("ffmpeg", "", "optional path to ffmpeg executable (media role)")
	flag.Parse()

	if *showVersion {
		fmt.Println(Version)
		return
	}

	hwInfo := hw.Probe()
	if *probeHW {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(hwInfo)
		return
	}

	role := proto.Role(*roleFlag)
	switch role {
	case proto.RoleGPU, proto.RoleListen, proto.RoleMedia:
	default:
		fmt.Fprintf(os.Stderr, "audiosmith-engine: unknown --role %q (want gpu, listen, or media)\n", *roleFlag)
		os.Exit(2)
	}

	// Reserve real stdout strictly for protocol-v2 NDJSON lines; redirect all
	// stray prints and logs to stderr.
	protoOut, err := server.HijackStdout()
	if err != nil {
		fmt.Fprintf(os.Stderr, "audiosmith-engine: stdout hijack warning: %v\n", err)
	}
	logger := log.New(os.Stderr, fmt.Sprintf("[audiosmith-engine:%s] ", role), log.LstdFlags)

	cfg := server.Config{
		Role:     role,
		Version:  Version,
		Hardware: hwInfo,
		Logf:     logger.Printf,
	}

	var srv *server.Server
	switch role {
	case proto.RoleGPU:
		g := gpu.New(hwInfo)
		cfg.Hooks = g.Hooks()
		srv = server.New(cfg)
		g.Register(srv)
	case proto.RoleMedia:
		cfg.Hooks = server.RoleHooks{Concurrency: 2}
		srv = server.New(cfg)
		media.New(hwInfo, *ffmpegPath, nil).Register(srv)
	case proto.RoleListen:
		cfg.Hooks = server.RoleHooks{Concurrency: 1}
		srv = server.New(cfg)
		listen.New(nil, nil).Register(srv)
	}

	if err := srv.Serve(os.Stdin, protoOut); err != nil {
		logger.Printf("serve error: %v", err)
		os.Exit(1)
	}
}
