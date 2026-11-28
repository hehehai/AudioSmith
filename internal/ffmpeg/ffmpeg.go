// Package ffmpeg runs an optional ffmpeg (and ffprobe) for what
// AVFoundation, which the engine uses, cannot do: MP3, Opus and FLAC
// output, WebM/MKV/VP9/AV1 input, burnt-in subtitles. AudioSmith works
// without it: callers check Tool's capabilities and say what is missing.
//
// Where it comes from (M7a decision: AVFoundation first, ffmpeg on
// demand): a pinned static build the user chooses to download (Pinned,
// checked by SHA-256, into the app's data folder), an ffmpeg already on
// the Mac (Homebrew, MacPorts, the PATH: FindSystem), or a file the user
// picks. It runs as a separate process; nothing links to it.
//
// Imports: nothing internal (leaf).
package ffmpeg

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// Tool is an ffmpeg executable and what it can do.
type Tool struct {
	FFmpeg  string // the executable
	FFprobe string // ffprobe beside it, "" when there is none
	Version string // "8.1.2", "n8.1.2", "6.1.1-3ubuntu5"…
	// Licence is how it was built: "LGPL", "GPL" or "nonfree" (not
	// redistributable), from its configuration.
	Licence string
	Config  string // the configure line

	encoders map[string]bool
	filters  map[string]bool
}

// Errors.
var (
	// ErrNotFound: no ffmpeg is set up.
	ErrNotFound = errors.New("ffmpeg is not set up")
	// ErrNoProbe: the ffmpeg in use has no ffprobe beside it.
	ErrNoProbe = errors.New("ffprobe is missing beside ffmpeg")
)

// NoEncoderError: the ffmpeg in use cannot write a format.
type NoEncoderError struct{ Format string }

func (e *NoEncoderError) Error() string {
	return fmt.Sprintf("this ffmpeg cannot encode %s", e.Format)
}

// RunError is a failed ffmpeg run: its exit and the last lines it wrote.
type RunError struct {
	Err  error
	Tail string
}

func (e *RunError) Error() string {
	if e.Tail != "" {
		return "ffmpeg: " + e.Tail
	}
	return "ffmpeg: " + e.Err.Error()
}

func (e *RunError) Unwrap() error { return e.Err }

// command makes the commands Tool runs (tests replace it).
var command = exec.CommandContext

// Inspect asks the ffmpeg at path for its version, licence, encoders and
// filters (a second or so), and looks for ffprobe beside it.
func Inspect(ctx context.Context, path string) (*Tool, error) {
	if path == "" {
		return nil, ErrNotFound
	}
	if fi, err := os.Stat(path); err != nil {
		return nil, err
	} else if fi.IsDir() {
		return nil, fmt.Errorf("%s is a folder", path)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := output(ctx, path, "-hide_banner", "-version")
	if err != nil {
		return nil, fmt.Errorf("%s does not run: %w", path, err)
	}
	t := &Tool{FFmpeg: path}
	t.Version, t.Config = parseVersion(out)
	if t.Version == "" {
		return nil, fmt.Errorf("%s is not ffmpeg", path)
	}
	t.Licence = licenceOf(t.Config)
	if out, err := output(ctx, path, "-hide_banner", "-encoders"); err == nil {
		t.encoders = parseList(out)
	}
	if out, err := output(ctx, path, "-hide_banner", "-filters"); err == nil {
		t.filters = parseList(out)
	}
	probe := filepath.Join(filepath.Dir(path), strings.Replace(filepath.Base(path), "ffmpeg", "ffprobe", 1))
	if probe != path {
		if fi, err := os.Stat(probe); err == nil && !fi.IsDir() {
			t.FFprobe = probe
		}
	}
	return t, nil
}

func output(ctx context.Context, path string, args ...string) (string, error) {
	cmd := command(ctx, path, args...)
	hideWindow(cmd)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", &RunError{Err: err, Tail: lastLine(errb.String())}
	}
	return out.String(), nil
}

var versionRE = regexp.MustCompile(`(?m)^ffmpeg version (\S+)`)

// parseVersion reads `ffmpeg -version`: the version and the configure line.
func parseVersion(s string) (version, config string) {
	if m := versionRE.FindStringSubmatch(s); m != nil {
		version = m[1]
	}
	for line := range strings.Lines(s) {
		if c, ok := strings.CutPrefix(strings.TrimSpace(line), "configuration:"); ok {
			config = strings.TrimSpace(c)
		}
	}
	return version, config
}

func licenceOf(config string) string {
	switch {
	case strings.Contains(config, "--enable-nonfree"):
		return "nonfree"
	case strings.Contains(config, "--enable-gpl"):
		return "GPL"
	}
	return "LGPL"
}

// parseList reads `ffmpeg -encoders` or `-filters`: the names, which
// follow a column of capability flags.
func parseList(s string) map[string]bool {
	m := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(s))
	started := false
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if !started && len(f) > 0 && strings.HasPrefix(f[0], "---") {
			// -encoders lists flags, a "------" rule, then entries.
			started = true
			continue
		}
		if len(f) < 2 {
			continue
		}
		if !started {
			// -filters has no rule: entries start after the legend.
			if len(f) >= 3 && strings.Contains(f[2], "->") {
				started = true
			} else {
				continue
			}
		}
		m[f[1]] = true
	}
	return m
}

// HasEncoder reports an encoder ("libmp3lame", "h264_videotoolbox").
func (t *Tool) HasEncoder(name string) bool { return t != nil && t.encoders[name] }

// HasFilter reports a filter ("subtitles", "overlay").
func (t *Tool) HasFilter(name string) bool { return t != nil && t.filters[name] }

// Audio output formats ffmpeg adds.
const (
	MP3  = "mp3"
	Opus = "opus" // Ogg Opus
	FLAC = "flac"
	OGG  = "ogg" // Ogg Vorbis
	M4A  = "m4a" // AAC in MP4 (the engine makes these too)
	WAV  = "wav"
)

// audioCodec is the encoder and its options for format, "" when this
// ffmpeg has none.
func (t *Tool) audioCodec(format string, bitrate int) (codec string, args []string) {
	kbps := func(def int) string {
		if bitrate <= 0 {
			bitrate = def * 1000
		}
		return fmt.Sprintf("%dk", bitrate/1000)
	}
	switch format {
	case MP3:
		for _, c := range []string{"libmp3lame", "libshine"} {
			if t.HasEncoder(c) {
				return c, []string{"-b:a", kbps(192)}
			}
		}
	case Opus:
		if t.HasEncoder("libopus") {
			return "libopus", []string{"-b:a", kbps(96)}
		}
		if t.HasEncoder("opus") {
			return "opus", []string{"-b:a", kbps(96), "-strict", "-2"}
		}
	case OGG:
		if t.HasEncoder("libvorbis") {
			return "libvorbis", []string{"-b:a", kbps(160)}
		}
	case FLAC:
		if t.HasEncoder("flac") {
			return "flac", []string{"-compression_level", "8"}
		}
	case M4A:
		for _, c := range []string{"aac_at", "aac"} {
			if t.HasEncoder(c) {
				return c, []string{"-b:a", kbps(192)}
			}
		}
	case WAV:
		return "pcm_s16le", nil
	}
	return "", nil
}

// CanEncode reports whether this ffmpeg writes an audio format.
func (t *Tool) CanEncode(format string) bool {
	c, _ := t.audioCodec(format, 0)
	return c != ""
}

// videoCodec is the H.264 encoder to use: the Mac's hardware encoder,
// else x264, else MPEG-4 part 2 (every build has it).
func (t *Tool) videoCodec() []string {
	switch {
	case t.HasEncoder("h264_videotoolbox"):
		return []string{"-c:v", "h264_videotoolbox", "-q:v", "65", "-allow_sw", "1"}
	case t.HasEncoder("libx264"):
		return []string{"-c:v", "libx264", "-crf", "20", "-preset", "veryfast"}
	}
	return []string{"-c:v", "mpeg4", "-q:v", "3"}
}

// CanBurn reports whether this ffmpeg can burn subtitles in: with libass
// (the subtitles filter), or the captions AudioSmith draws (overlay).
func (t *Tool) CanBurn() bool { return t.HasFilter("subtitles") || t.HasFilter("overlay") }

// SystemPaths are where ffmpeg is looked for on a Mac: Homebrew (Apple
// silicon, then Intel), MacPorts. Apps started from the Finder do not
// get the shell's PATH, so these are checked first.
var SystemPaths = []string{"/opt/homebrew/bin/ffmpeg", "/usr/local/bin/ffmpeg", "/opt/local/bin/ffmpeg"}

// findOS controls platform discovery in FindSystem (tests may override).
var findOS = runtime.GOOS

// FindSystem lists the ffmpeg executables on this machine outside the app.
func FindSystem() []string {
	var out []string
	add := func(p string) {
		if p == "" {
			return
		}
		if r, err := filepath.EvalSymlinks(p); err == nil {
			for _, o := range out {
				if r2, _ := filepath.EvalSymlinks(o); r2 == r {
					return
				}
			}
		}
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() {
			return
		}
		if findOS != "windows" && fi.Mode()&0o111 == 0 {
			return
		}
		out = append(out, p)
	}
	if findOS == "windows" {
		for _, p := range windowsCandidates("ffmpeg.exe") {
			add(p)
		}
		if p, err := exec.LookPath("ffmpeg.exe"); err == nil {
			add(p)
		}
		if p, err := exec.LookPath("ffmpeg"); err == nil {
			add(p)
		}
		return out
	}
	for _, p := range SystemPaths {
		add(p)
	}
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		add(p)
	}
	return out
}

// windowsCandidates returns standard Windows locations for an external
// tool executable (app resources dir, WinGet, Scoop, Chocolatey, Program
// Files).
func windowsCandidates(exe string) []string {
	var out []string
	if self, err := os.Executable(); err == nil {
		dir := filepath.Dir(self)
		out = append(out,
			filepath.Join(dir, exe),
			filepath.Join(dir, "resources", exe),
			filepath.Join(dir, "bin", exe),
		)
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		out = append(out, filepath.Join(local, "Microsoft", "WinGet", "Links", exe))
		if matches, err := filepath.Glob(filepath.Join(local, "Microsoft", "WinGet", "Packages", "*", "*", "bin", exe)); err == nil {
			out = append(out, matches...)
		}
	}
	if scoop := os.Getenv("SCOOP"); scoop != "" {
		out = append(out, filepath.Join(scoop, "shims", exe))
	}
	if profile := os.Getenv("USERPROFILE"); profile != "" {
		out = append(out, filepath.Join(profile, "scoop", "shims", exe))
	}
	out = append(out, filepath.Join(`C:\ProgramData\chocolatey\bin`, exe))
	if pf := os.Getenv("ProgramFiles"); pf != "" {
		out = append(out, filepath.Join(pf, "ffmpeg", "bin", exe))
	}
	out = append(out, filepath.Join(`C:\ffmpeg\bin`, exe))
	return out
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			if len(l) > 300 {
				l = l[:300] + "…"
			}
			return l
		}
	}
	return ""
}
