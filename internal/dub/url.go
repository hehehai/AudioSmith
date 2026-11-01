package dub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/hehehai/actnowkit/mirror"
)

// Import from a URL (YouTube and the other sites yt-dlp knows), opt-in:
// the app downloads yt-dlp's standalone macOS build (pinned, checked by
// SHA-256) the first time the user turns URL import on. The rules VS
// applies are kept: http(s) only; no addresses on this machine or the
// local network (a page could otherwise make the app fetch from the
// LAN); no live streams, premieres, playlists or stream manifests.

// URL import error codes (*URLError).
const (
	CodeURLScheme   = "url.scheme"   // not http or https
	CodeURLPrivate  = "url.private"  // this machine or the local network
	CodeURLPlaylist = "url.playlist" // a playlist or channel
	CodeURLLive     = "url.live"     // a live stream or premiere
	CodeURLManifest = "url.manifest" // an HLS/DASH manifest
	CodeURLInvalid  = "url.invalid"
	CodeNoYtDlp     = "url.no_ytdlp" // URL import is off (yt-dlp not installed)
	CodeYtDlp       = "url.ytdlp"    // yt-dlp failed (Detail: its last line)
	CodeChecksum    = "url.checksum" // the download of yt-dlp did not match its checksum
	CodeCookies     = "url.cookies"  // the cookies file is over 1 MB
)

// URLError is a refused or failed URL import.
type URLError struct {
	Code   string
	Detail string
}

func (e *URLError) Error() string {
	if e.Detail != "" {
		return e.Code + ": " + e.Detail
	}
	return e.Code
}

// CheckURL validates u before anything is fetched. lookup resolves host
// names (nil skips the resolution: only literal addresses are checked).
func CheckURL(ctx context.Context, raw string, lookup func(ctx context.Context, host string) ([]netip.Addr, error)) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, &URLError{Code: CodeURLInvalid}
	}
	if u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https" {
		return nil, &URLError{Code: CodeURLScheme}
	}
	if u.Host == "" || u.Scheme == "" {
		return nil, &URLError{Code: CodeURLInvalid}
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".lan") || strings.HasSuffix(host, ".home.arpa") || !strings.Contains(host, ".") && net.ParseIP(host) == nil {
		return nil, &URLError{Code: CodeURLPrivate}
	}
	if a, err := netip.ParseAddr(host); err == nil {
		if private(a) {
			return nil, &URLError{Code: CodeURLPrivate}
		}
	} else if lookup != nil {
		addrs, err := lookup(ctx, host)
		if err != nil {
			return nil, &URLError{Code: CodeURLInvalid, Detail: err.Error()}
		}
		for _, a := range addrs {
			if private(a) {
				return nil, &URLError{Code: CodeURLPrivate}
			}
		}
	}
	path := strings.ToLower(u.Path)
	switch {
	case strings.HasSuffix(path, ".m3u8") || strings.HasSuffix(path, ".mpd") || strings.HasSuffix(path, ".m3u"):
		return nil, &URLError{Code: CodeURLManifest}
	case strings.Contains(path, "/playlist") || (u.Query().Get("list") != "" && u.Query().Get("v") == "" && !strings.Contains(host, "youtu.be")):
		return nil, &URLError{Code: CodeURLPlaylist}
	case strings.HasPrefix(path, "/live/") || strings.HasSuffix(path, "/live") || strings.Contains(path, "/streams"):
		return nil, &URLError{Code: CodeURLLive}
	}
	return u, nil
}

// private reports addresses on this machine or a local network.
func private(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsUnspecified() || a.IsMulticast() || a.IsInterfaceLocalMulticast() ||
		netip.MustParsePrefix("100.64.0.0/10").Contains(a) // carrier-grade NAT
}

// LookupHost is CheckURL's resolver over the system's.
func LookupHost(ctx context.Context, host string) ([]netip.Addr, error) {
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	return ips, err
}

// --- yt-dlp -----------------------------------------------------------------------

// The yt-dlp build URL import uses: the standalone macOS binary (no
// Python), pinned. Update all together. YtDlpPath is the file under a
// source of GitHub releases (github.com, or the ActNow mirror, which
// keeps this pinned file: actnowkit's mirror package); YtDlpURL is
// github.com's.
const (
	YtDlpVersion = "2026.08.19"
	YtDlpSHA256  = "0f192b7ec147ab6288885d6351d9ab67367640029b4377576ef46dd79cf7b202"
	YtDlpPath    = "/yt-dlp/yt-dlp/releases/download/" + YtDlpVersion + "/yt-dlp_macos"
	YtDlpURL     = mirror.GitHub + YtDlpPath
	YtDlpSize    = 37146048
)

// YtDlp runs yt-dlp.
type YtDlp struct {
	// Path is the executable ("" = not installed).
	Path string
	// Proxy is passed as --proxy ("" = none).
	Proxy string
	// FFmpeg is the ffmpeg yt-dlp merges separate video and audio streams
	// with ("" = none: only files that hold both can be downloaded, and
	// YouTube offers few or none of those).
	FFmpeg string
}

// InstallYtDlp downloads yt-dlp into dir (as "yt-dlp") from the fastest
// of src's sources (nil: github.com and the ActNow mirror), checks it
// against the pinned SHA-256 (a source that serves anything else is left
// for the next one) and makes it executable. progress gets bytes done of
// the total. It returns the executable's path and the source it came
// from. When only wrong files came, the error is a *URLError with
// CodeChecksum.
func InstallYtDlp(ctx context.Context, src *mirror.Client, dir string, progress func(done, total int64)) (string, string, error) {
	return installYtDlp(ctx, src, dir, ytdlpPin{YtDlpPath, YtDlpSHA256, YtDlpSize}, progress)
}

// ytdlpPin is the file InstallYtDlp fetches (tests use others).
type ytdlpPin struct {
	path, sha256 string
	size         int64
}

func installYtDlp(ctx context.Context, src *mirror.Client, dir string, pin ytdlpPin, progress func(done, total int64)) (string, string, error) {
	if src == nil {
		src = &mirror.Client{}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	tmp, from, err := src.File(ctx, pin.path, mirror.FileOptions{
		Dir: dir, SHA256: pin.sha256, Size: pin.size, Limit: 4*pin.size + 1<<20, Progress: progress,
	})
	if err != nil {
		if ctx.Err() != nil {
			return "", "", ctx.Err()
		}
		var ce *mirror.ChecksumError
		if errors.As(err, &ce) {
			return "", "", &URLError{Code: CodeChecksum, Detail: ce.Got}
		}
		return "", "", fmt.Errorf("yt-dlp: download: %w", err)
	}
	defer os.Remove(tmp) // after the rename: nothing
	dst := filepath.Join(dir, "yt-dlp")
	if err := os.Chmod(tmp, 0o755); err != nil {
		return "", "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", "", err
	}
	return dst, from, nil
}

// URLInfo is what yt-dlp says about a URL.
type URLInfo struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Duration   float64  `json:"duration"`
	Type       string   `json:"_type"`
	IsLive     bool     `json:"is_live"`
	LiveStatus string   `json:"live_status"`
	Extractor  string   `json:"extractor_key"`
	Language   string   `json:"language"`
	Captions   []string `json:"-"` // languages with captions written by people
	// Meta is what the Dub page shows of it (At not set), with the format
	// Download would take.
	Meta SourceMeta `json:"-"`
}

// Info asks yt-dlp about a URL (no download), with the format Download
// would choose, and refuses playlists and live streams.
func (y YtDlp) Info(ctx context.Context, u string) (URLInfo, error) {
	var info URLInfo
	if y.Path == "" {
		return info, &URLError{Code: CodeNoYtDlp}
	}
	args := append(y.base(), "--dump-single-json", "--no-playlist")
	args = append(args, y.formatArgs(y.FFmpeg)...)
	if y.FFmpeg == "" {
		// Without ffmpeg the download takes one file with both picture
		// and sound; most sites (YouTube) offer none: the streams then
		// say what there is, and that the download needs ffmpeg.
		args[len(args)-1] += "/bv*+ba/bv*/ba"
	}
	out, err := y.run(ctx, append(args, "--", u), nil)
	if err != nil {
		return info, err
	}
	info, err = parseInfo(out)
	if err != nil {
		return info, err
	}
	info.Meta.NeedsFFmpeg = y.FFmpeg == "" && strings.Contains(info.Meta.Format, "+")
	switch {
	case info.Type == "playlist" || info.Type == "multi_video":
		return info, &URLError{Code: CodeURLPlaylist}
	case info.IsLive || info.LiveStatus == "is_live" || info.LiveStatus == "is_upcoming" || info.LiveStatus == "post_live":
		return info, &URLError{Code: CodeURLLive}
	}
	return info, nil
}

// parseInfo reads yt-dlp's --dump-single-json.
func parseInfo(out []byte) (URLInfo, error) {
	var raw rawInfo
	if err := json.Unmarshal(out, &raw); err != nil {
		return URLInfo{}, &URLError{Code: CodeYtDlp, Detail: err.Error()}
	}
	info := raw.URLInfo
	info.Meta = metaOf(raw)
	info.Captions = info.Meta.Captions
	return info, nil
}

// formatArgs choose the format: with ffmpeg (which merges separate video
// and audio streams) up to 1080p H.264 MP4, else the best single file.
func (y YtDlp) formatArgs(ffmpeg string) []string {
	if ffmpeg == "" {
		return []string{"-f", "b[ext=mp4][vcodec^=avc1]/b[ext=mp4]/b"}
	}
	return []string{"--ffmpeg-location", ffmpeg, "--merge-output-format", "mp4", "-f",
		"bv*[height<=1080][ext=mp4][vcodec^=avc1]+ba[ext=m4a]/bv*[height<=1080][ext=mp4]+ba[ext=m4a]/b[height<=1080][ext=mp4]/bv*[height<=1080]+ba/bv*+ba/b"}
}

// DownloadOptions choose what Download fetches.
type DownloadOptions struct {
	// Captions downloads the caption track closest to this language
	// (written by people, not translated automatically); "" = none.
	Captions string
	// Cookies is a cookies.txt export for sites that need a sign-in
	// (≤ 1 MB; used for this download only).
	Cookies string
	// Merge allows separate video and audio streams merged by ffmpeg
	// (FFmpeg names it); without, the best single file (often ≤ 720p).
	FFmpeg string
}

// Download fetches the video into dir and returns the file (and the
// caption file, if any). progress (when set) gets how far it is, as often
// as yt-dlp says (about four times a second), from one goroutine.
func (y YtDlp) Download(ctx context.Context, u, dir string, o DownloadOptions, progress func(DownloadProgress)) (video, captions string, err error) {
	if y.Path == "" {
		return "", "", &URLError{Code: CodeNoYtDlp}
	}
	if o.Cookies != "" {
		if fi, err := os.Stat(o.Cookies); err != nil {
			return "", "", err
		} else if fi.Size() > 1<<20 {
			return "", "", &URLError{Code: CodeCookies}
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	if o.FFmpeg == "" {
		o.FFmpeg = y.FFmpeg
	}
	args := append(y.base(), y.formatArgs(o.FFmpeg)...)
	args = append(args, "--no-playlist", "--no-part", "--restrict-filenames", "-o", filepath.Join(dir, "source.%(ext)s"))
	args = append(args, progressArgs()...)
	args = append(args, "--print", "after_move:"+progFile+"%(filepath)s")
	if o.Captions != "" {
		args = append(args, "--write-subs", "--sub-langs", o.Captions+".*,"+o.Captions, "--sub-format", "vtt/srt/best", "--convert-subs", "srt")
		if o.FFmpeg == "" {
			args = args[:len(args)-2] // converting needs ffmpeg
		}
	}
	if o.Cookies != "" {
		args = append(args, "--cookies", o.Cookies)
	}
	args = append(args, "--", u)
	var pp progressParser
	var last string
	out, err := y.run(ctx, args, func(line string) {
		if p, ok := pp.line(line); ok && progress != nil {
			progress(p)
		}
	})
	if err != nil {
		return "", "", err
	}
	for _, l := range strings.Split(string(out), "\n") {
		if f, ok := strings.CutPrefix(l, progFile); ok && strings.TrimSpace(f) != "" {
			video = strings.TrimSpace(f)
		} else if l = strings.TrimSpace(l); video == "" && l != "" && !strings.HasPrefix(l, "AS") {
			last = l
		}
	}
	if video == "" {
		video = last
	}
	if _, err := os.Stat(video); err != nil {
		return "", "", &URLError{Code: CodeYtDlp, Detail: "no file was written"}
	}
	if o.Captions != "" {
		matches, _ := filepath.Glob(filepath.Join(dir, "source*.srt"))
		vtts, _ := filepath.Glob(filepath.Join(dir, "source*.vtt"))
		matches = append(matches, vtts...)
		if len(matches) > 0 {
			captions = matches[0]
		}
	}
	return video, captions, nil
}

func (y YtDlp) base() []string {
	args := []string{"--ignore-config", "--no-warnings", "--no-color"}
	if y.Proxy != "" {
		args = append(args, "--proxy", y.Proxy)
	}
	return args
}

// errorLine is the start of yt-dlp's error lines: "ERROR: [youtube]
// abc123: " (the site and the video's id).
var errorLine = regexp.MustCompile(`^ERROR:\s*(?:\[[^\]]*\]\s*(?:[^\s:]+:\s+)?)?`)

// run runs yt-dlp, feeding stdout's and stderr's lines to line (when
// set, one line at a time) and returning stdout. Cancelling ctx kills
// yt-dlp with everything it started, and run returns at once even if a
// process outside its group still holds the pipes.
func (y YtDlp) run(ctx context.Context, args []string, line func(string)) ([]byte, error) {
	cmd := exec.CommandContext(ctx, y.Path, args...)
	ownGroup(cmd)
	cmd.WaitDelay = 2 * time.Second
	var (
		mu     sync.Mutex
		out    strings.Builder
		stderr strings.Builder
	)
	emit := func(l string, stdout bool) {
		mu.Lock()
		defer mu.Unlock()
		if stdout {
			out.WriteString(l + "\n")
		} else if stderr.Len() < 64<<10 {
			stderr.WriteString(l[:min(len(l), 64<<10-stderr.Len())] + "\n")
		}
		if line != nil {
			line(l)
		}
	}
	cmd.Stdout = &lineWriter{emit: func(l string) { emit(l, true) }}
	cmd.Stderr = &lineWriter{emit: func(l string) { emit(l, false) }}
	if err := cmd.Start(); err != nil {
		return nil, &URLError{Code: CodeYtDlp, Detail: err.Error()}
	}
	err := cmd.Wait()
	cmd.Stdout.(*lineWriter).flush()
	cmd.Stderr.(*lineWriter).flush()
	if errors.Is(err, exec.ErrWaitDelay) {
		// yt-dlp exited well; something it started still held a pipe
		// after WaitDelay (#101 L4): not a failure.
		err = nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		mu.Lock()
		msg := strings.TrimSpace(stderr.String())
		mu.Unlock()
		if i := strings.LastIndex(msg, "\nERROR:"); i >= 0 {
			msg = msg[i+1:]
		}
		msg = errorLine.ReplaceAllString(msg, "")
		lower := strings.ToLower(msg)
		switch {
		case strings.Contains(lower, "live event") || strings.Contains(lower, "premiere") || strings.Contains(lower, "is live"):
			return nil, &URLError{Code: CodeURLLive, Detail: msg}
		}
		return nil, &URLError{Code: CodeYtDlp, Detail: msg}
	}
	mu.Lock()
	defer mu.Unlock()
	return []byte(out.String()), nil
}

// lineWriter hands what is written to it to emit line by line (without
// the line ends; a carriage return ends a line too). Lines over 64 MB
// (Info's JSON is one line, a few MB at most) are cut.
type lineWriter struct {
	emit func(string)
	buf  []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	for _, b := range p {
		if b == '\n' || b == '\r' {
			if len(w.buf) > 0 {
				w.emit(string(w.buf))
			}
			w.buf = w.buf[:0]
			continue
		}
		if len(w.buf) < 64<<20 {
			w.buf = append(w.buf, b)
		}
	}
	return len(p), nil
}

func (w *lineWriter) flush() {
	if len(w.buf) > 0 {
		w.emit(string(w.buf))
		w.buf = w.buf[:0]
	}
}

// ErrURLRefused reports any refused URL (errors.Is).
var ErrURLRefused = errors.New("dub: URL refused")

// Is lets errors.Is(err, ErrURLRefused) match the refusals.
func (e *URLError) Is(target error) bool {
	if target != ErrURLRefused {
		return false
	}
	switch e.Code {
	case CodeURLScheme, CodeURLPrivate, CodeURLPlaylist, CodeURLLive, CodeURLManifest, CodeURLInvalid:
		return true
	}
	return false
}
