package ffmpeg

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// run runs ffmpeg with args (inputs, filters, codecs: not the output),
// writing out through a temporary file beside it that is renamed when
// ffmpeg succeeds (a failure or a cancel removes it). duration (seconds,
// 0 = unknown) turns ffmpeg's progress into fractions for progress.
func (t *Tool) run(ctx context.Context, args []string, out string, duration float64, progress func(float64)) error {
	return t.runIn(ctx, "", args, out, duration, progress)
}

// runIn is run in the working directory dir ("" = the app's).
func (t *Tool) runIn(ctx context.Context, dir string, args []string, out string, duration float64, progress func(float64)) error {
	if t == nil || t.FFmpeg == "" {
		return ErrNotFound
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	tmp, err := partFile(out)
	if err != nil {
		return err
	}
	full := append([]string{"-hide_banner", "-nostdin", "-y", "-loglevel", "error", "-nostats", "-progress", "pipe:1"}, args...)
	full = append(full, tmp)
	cmd := command(ctx, t.FFmpeg, full...)
	cmd.Dir = dir
	var errb tailBuffer
	cmd.Stderr = &errb
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		os.Remove(tmp) // code review 1103 L2: no empty .part left behind
		return err
	}
	if err := cmd.Start(); err != nil {
		os.Remove(tmp)
		return &RunError{Err: err}
	}
	readProgress(stdout, duration, progress) // until ffmpeg closes it
	err = cmd.Wait()
	if err != nil {
		os.Remove(tmp)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &RunError{Err: err, Tail: lastLine(errb.String())}
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, out); err != nil {
		os.Remove(tmp)
		return err
	}
	if progress != nil {
		progress(1)
	}
	return nil
}

// partFile creates the empty temporary file ffmpeg writes out to: beside
// it, hidden, with out's extension (ffmpeg picks the format by it) and a
// random part, created exclusively (code review 11-02, N2: a predictable
// name in a shared folder could be a symlink planted there, which ffmpeg
// -y would follow). ffmpeg overwrites this file of ours.
func partFile(out string) (string, error) {
	ext := filepath.Ext(out)
	f, err := os.CreateTemp(filepath.Dir(out), "."+strings.TrimSuffix(filepath.Base(out), ext)+".*.part"+ext)
	if err != nil {
		return "", err
	}
	f.Close()
	return f.Name(), nil
}

// readProgress follows `-progress pipe:1`: key=value lines, a block per
// update, out_time_us the position written.
func readProgress(r io.Reader, duration float64, progress func(float64)) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok || progress == nil || duration <= 0 {
			continue
		}
		switch k {
		case "out_time_us", "out_time_ms": // both are microseconds
			us, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err == nil && us > 0 {
				progress(min(float64(us)/1e6/duration, 0.999))
			}
		}
	}
	io.Copy(io.Discard, r)
}

// tailBuffer keeps the last 8 KB written to it.
type tailBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Write(p)
	if n := b.buf.Len(); n > 8<<10 {
		rest := b.buf.Bytes()[n-(8<<10):]
		b.buf = *bytes.NewBuffer(append([]byte(nil), rest...))
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// secs formats seconds for ffmpeg's -ss / -t.
func secs(s float64) string { return strconv.FormatFloat(s, 'f', 3, 64) }
