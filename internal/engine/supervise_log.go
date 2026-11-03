package engine

// The engines' stderr logs (engine-<role>.log): rotated by size while
// they are written, not only when a worker starts (code review 1102 L3:
// the gpu worker often lives as long as the app, and its log had no
// bound within a session). One previous file is kept (.1), as before;
// diagnostic bundles take both.

import (
	"os"
	"path/filepath"
)

// engineLogMax is the size past which an engine log is rotated (a
// variable for tests).
var engineLogMax int64 = 10 << 20

// rotatingLog appends lines to a file, moving it to path.1 (replacing
// the one there) once it passes max bytes. Not goroutine-safe: one
// worker's stderr reader writes it.
type rotatingLog struct {
	path string
	max  int64
	f    *os.File
	size int64
}

// openLog opens the stderr log at path for appending, rotated past max
// bytes (0 = engineLogMax); nil when it cannot be opened.
func openLog(path string, max int64) *rotatingLog {
	if max <= 0 {
		max = engineLogMax
	}
	l := &rotatingLog{path: path, max: max}
	os.MkdirAll(filepath.Dir(path), 0o755)
	if st, err := os.Stat(path); err == nil && st.Size() > max {
		os.Rename(path, path+".1")
	}
	if !l.open() {
		return nil
	}
	return l
}

func (l *rotatingLog) open() bool {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		l.f = nil
		return false
	}
	l.f, l.size = f, 0
	if st, err := f.Stat(); err == nil {
		l.size = st.Size()
	}
	return true
}

// line appends s and a newline, rotating first when it would pass max.
// A line is never split, so a file may pass max by one line.
func (l *rotatingLog) line(s string) {
	if l.f == nil {
		return
	}
	n := int64(len(s) + 1)
	if l.size > 0 && l.size+n > l.max {
		l.f.Close()
		os.Rename(l.path, l.path+".1")
		if !l.open() {
			return
		}
	}
	w, _ := l.f.WriteString(s + "\n")
	l.size += int64(w)
}

// Close closes the file.
func (l *rotatingLog) Close() error {
	if l.f == nil {
		return nil
	}
	return l.f.Close()
}
