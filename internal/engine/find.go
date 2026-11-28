// Package engine runs audiosmith-engine, the Swift helper that does the
// speech work, and talks to it: a Supervisor runs one process per role and
// speaks protocol 2 (package proto, docs/protocol-v2.md) with it, and
// RegisterExecutors runs engine methods as jobs of the jobs.Scheduler.
package engine

import (
	"os"
	"path/filepath"
	"runtime"
)

// engineBinaryName returns the engine executable name for goos.
func engineBinaryName(goos string) string {
	if goos == "windows" {
		return "audiosmith-engine.exe"
	}
	return "audiosmith-engine"
}

// Find looks for audiosmith-engine: $AUDIOSMITH_ENGINE, then the app's
// resources (packaged, mygo dev, or go run from the project). It returns
// an absolute path, or "" when there is none.
func Find(resources string) string {
	if p := os.Getenv("AUDIOSMITH_ENGINE"); p != "" {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
		return p
	}
	bin := engineBinaryName(runtime.GOOS)
	candidates := []string{
		filepath.Join(resources, "engine", bin),
		filepath.Join(resources, runtime.GOOS+"-"+runtime.GOARCH, "engine", bin),
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			// Absolute: the engine runs in its own directory.
			if abs, err := filepath.Abs(p); err == nil {
				return abs
			}
			return p
		}
	}
	return ""
}
