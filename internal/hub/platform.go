package hub

import (
	"errors"
	"fmt"
	"runtime"

	"github.com/hehehai/AudioSmith/internal/catalog"
)

// ErrUnsupportedPlatform is returned when a catalogue entry is not
// available on the target operating system (for example, an MLX-only
// checkpoint on Windows).
var ErrUnsupportedPlatform = errors.New("model is not available on this platform")

func effectiveOS(goos string) string {
	if goos == "" {
		return runtime.GOOS
	}
	return goos
}

func (c *Client) targetOS() string {
	return effectiveOS(c.opts.GOOS)
}

// ResolveEntry resolves e's download and cache source for goos.
// On macOS ("darwin") and Linux, e is returned unchanged.
// On Windows ("windows"), entries with !AvailableOn("windows") return
// ErrUnsupportedPlatform so MLX weights are never downloaded; available
// entries substitute their Windows repo, revision, files and size and
// clear macOS MLX auxiliary repos.
func ResolveEntry(e catalog.Entry, goos string) (catalog.Entry, error) {
	goos = effectiveOS(goos)
	if goos != "windows" {
		return e, nil
	}
	if !e.AvailableOn(goos) {
		return catalog.Entry{}, fmt.Errorf("%s: %w", e.Repo, ErrUnsupportedPlatform)
	}
	re := e
	re.Repo = e.EffectiveRepo(goos)
	re.Revision = e.EffectiveRevision(goos)
	re.Files = e.EffectiveFiles(goos)
	re.Size = e.EffectiveSize(goos)
	re.Aux = nil
	if re.Repo == "" {
		return catalog.Entry{}, fmt.Errorf("%s: %w", e.Repo, ErrUnsupportedPlatform)
	}
	return re, nil
}
