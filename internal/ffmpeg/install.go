package ffmpeg

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/hehehai/actnowkit/mirror"
)

// Asset is one downloadable file of a build.
type Asset struct {
	// Path is the file under a source of GitHub releases
	// ("/owner/repo/releases/download/tag/asset"): github.com or the
	// ActNow mirror (actnowkit's mirror package).
	Path   string
	SHA256 string
	Size   int64
}

// URL is the asset on github.com.
func (a Asset) URL() string { return mirror.GitHub + a.Path }

// Build is a downloadable static ffmpeg build.
type Build struct {
	Name    string // who builds it
	Home    string // its page
	Version string // ffmpeg's version
	// Licence of the binaries (shown before the download).
	Licence string
	FFmpeg  Asset
	FFprobe Asset
}

// Pinned is the build "Download ffmpeg" fetches: static macOS arm64
// binaries that need only system frameworks, built from source in
// public CI by Google's shaka-project (GitHub release n8.1.2-1, digests
// from the release's assets, checked against the binaries).
//
// No reputable LGPL-only static arm64 build exists (checked 2026-10-14:
// BtbN has no macOS builds; evermeet, OSXExperts, Martin Riedl, Tyrrrz,
// eugeneware and shaka all enable GPL parts such as x264). This one is
// GPL-3.0: AudioSmith does not ship or link it — it is downloaded when
// the user asks, from the publisher's GitHub release or from the ActNow
// mirror of it (whether the mirror carries ffmpeg is the user's call;
// without it, the download comes from GitHub), and AudioSmith runs it as
// a separate program. Either way the file must match the digest below.
// Update all fields together, after checking the new digests.
var Pinned = Build{
	Name:    "shaka-project/static-ffmpeg-binaries",
	Home:    "https://github.com/shaka-project/static-ffmpeg-binaries/releases/tag/n8.1.2-1",
	Version: "n8.1.2",
	Licence: "GPL-3.0",
	FFmpeg: Asset{
		Path:   "/shaka-project/static-ffmpeg-binaries/releases/download/n8.1.2-1/ffmpeg-osx-arm64",
		SHA256: "e7b9fcd97f95f333512d6e8b8ac24d9dbc08f189f36047695499bd7b57214b22",
		Size:   34074040,
	},
	FFprobe: Asset{
		Path:   "/shaka-project/static-ffmpeg-binaries/releases/download/n8.1.2-1/ffprobe-osx-arm64",
		SHA256: "ded4c698b8ff38d0bc1fd30fcc5e768dc46f58bc15a8dfd61f98615ba49cde5c",
		Size:   33882408,
	},
}

// Size is the download's size in bytes.
func (b Build) Size() int64 { return b.FFmpeg.Size + b.FFprobe.Size }

// ChecksumError: a download did not match its pinned digest.
type ChecksumError struct{ URL, Got string }

func (e *ChecksumError) Error() string {
	return fmt.Sprintf("%s: checksum mismatch (got %s)", e.URL, e.Got)
}

// exeName appends ".exe" on Windows.
func exeName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

// Install downloads b into dir as "ffmpeg" and "ffprobe" (with ".exe" on
// Windows) from the fastest of src's sources (nil: github.com and the
// ActNow mirror), checks both against their digests (a source that
// serves anything else is left for the next one), makes them executable
// and returns ffmpeg's path. progress gets the bytes done of the total.
// Files are written under temporary names and renamed only once checked;
// a failure leaves what was there before. When only wrong files came,
// the error is a *ChecksumError.
func Install(ctx context.Context, src *mirror.Client, dir string, b Build, progress func(done, total int64)) (string, error) {
	if src == nil {
		src = &mirror.Client{}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	total := b.Size()
	var base int64
	var tmps []string
	defer func() {
		for _, t := range tmps {
			os.Remove(t)
		}
	}()
	for _, a := range []Asset{b.FFmpeg, b.FFprobe} {
		tmp, _, err := src.File(ctx, a.Path, mirror.FileOptions{
			Dir: dir, SHA256: a.SHA256, Size: a.Size, Limit: 4*a.Size + 1<<20,
			Progress: func(done, _ int64) {
				if progress != nil {
					progress(base+done, total)
				}
			},
		})
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			var ce *mirror.ChecksumError
			if errors.As(err, &ce) {
				return "", &ChecksumError{URL: ce.URL, Got: ce.Got}
			}
			return "", err
		}
		tmps = append(tmps, tmp)
		base += a.Size
	}
	names := []string{exeName("ffmpeg"), exeName("ffprobe")}
	for i, tmp := range tmps {
		if err := os.Chmod(tmp, 0o755); err != nil {
			return "", err
		}
		prepare(tmp)
		if err := os.Rename(tmp, filepath.Join(dir, names[i])); err != nil {
			return "", err
		}
	}
	tmps = nil
	return filepath.Join(dir, names[0]), nil
}

// prepare lets macOS run a downloaded binary: no quarantine flag (Go's
// downloads set none, but a copy from a browser may), and an ad-hoc
// signature when it has none (Apple silicon refuses unsigned code).
var prepare = func(path string) {
	if runtime.GOOS != "darwin" {
		return
	}
	exec.Command("/usr/bin/xattr", "-d", "com.apple.quarantine", path).Run()
	if exec.Command("/usr/bin/codesign", "-v", path).Run() != nil {
		exec.Command("/usr/bin/codesign", "--force", "-s", "-", path).Run()
	}
}

// Verify checks installed files against b's digests.
func Verify(dir string, b Build) error {
	for name, a := range map[string]Asset{exeName("ffmpeg"): b.FFmpeg, exeName("ffprobe"): b.FFprobe} {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return err
		}
		if got := hex.EncodeToString(h.Sum(nil)); got != a.SHA256 {
			return &ChecksumError{URL: a.URL(), Got: got}
		}
	}
	return nil
}

// Remove deletes a build Install put in dir.
func Remove(dir string) error {
	for _, n := range []string{exeName("ffmpeg"), exeName("ffprobe")} {
		if err := os.Remove(filepath.Join(dir, n)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	os.Remove(dir) // when empty
	return nil
}
