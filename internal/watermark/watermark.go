// Package watermark is the app side of the invisible watermark (AudioSeal,
// engine methods watermark.embed / watermark.detect): the 16-bit message
// AudioSmith embeds, and the model's preparation (the engine loads
// safetensors converted from the .pth checkpoints facebook/audioseal
// ships).
//
// The message is 16 bits: the high byte is AudioSmith's marker 0xA5, the
// low byte a random install id (1–255, chosen once per install). A detected
// watermark whose high byte is the marker was made by AudioSmith; the low
// byte tells installs apart roughly (256 values: a hint, not an identity).
// With a random 16-bit message, the marker matches by chance 1 time in 256,
// so a detection is reported as AudioSmith's only when the detector also
// finds the watermark (probability ≥ 0.5) and decodes the bits confidently.
package watermark

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hehehai/AudioSmith/internal/torchpt"
)

// Marker is the high byte of AudioSmith's messages.
const Marker = 0xA5

// Message is the 16-bit message of an install.
func Message(install uint8) int { return Marker<<8 | int(install) }

// Decode splits a detected message: whether it is AudioSmith's, and the
// install id.
func Decode(message int) (audioSmith bool, install uint8) {
	return message>>8 == Marker, uint8(message & 0xFF)
}

// Files of the converted model, in the folder the engine gets as modelPath.
var Files = map[string]string{"generator_base.pth": "generator.safetensors", "detector_base.pth": "detector.safetensors"}

// ConvertedDir is where the converted model of a snapshot lives: beside
// the model cache, by repo and revision.
func ConvertedDir(cacheDir, repo, revision string) string {
	return filepath.Join(cacheDir, "audiosmith-converted", strings.ReplaceAll(repo, "/", "--"), revision)
}

// Prepare converts the snapshot's checkpoints into dst unless they are
// there already, and returns dst.
func Prepare(snapshot, dst string) (string, error) {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return "", err
	}
	for src, out := range Files {
		target := filepath.Join(dst, out)
		if fi, err := os.Stat(target); err == nil && fi.Size() > 0 {
			continue
		}
		if err := torchpt.ConvertFile(filepath.Join(snapshot, src), "model", target, map[string]string{"source": src}); err != nil {
			return "", fmt.Errorf("converting %s: %w", src, err)
		}
	}
	dropOtherRevisions(dst)
	return dst, nil
}

// dropOtherRevisions deletes the conversions of the repo's other
// revisions beside dst (ConvertedDir): a model updated to a new revision
// left its old conversion, about 100 MB (code review 1102 L6).
func dropOtherRevisions(dst string) {
	parent := filepath.Dir(dst)
	if filepath.Base(filepath.Dir(parent)) != "audiosmith-converted" {
		return
	}
	ents, _ := os.ReadDir(parent)
	for _, e := range ents {
		if e.IsDir() && e.Name() != filepath.Base(dst) {
			os.RemoveAll(filepath.Join(parent, e.Name()))
		}
	}
}
