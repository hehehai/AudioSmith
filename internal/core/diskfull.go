package core

// Running out of disk space (code review 1102 M4): the error a free-space
// pre-check returns, and what recognises a full disk in the errors of
// writes (ENOSPC from Go, the engine's messages).

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"syscall"
)

// CodeDiskFull is the error code of a job or request refused or failed
// for want of free disk space; its data are DiskFullData.
const CodeDiskFull = "disk_full"

// ErrDiskFull is wrapped by the errors of a lack of free disk space
// (DiskFullError, a write's ENOSPC as reported by NoSpace).
var ErrDiskFull = errors.New("not enough free disk space")

// DiskFullError is a pre-check's refusal: work that would write about
// Need bytes under Path, with Free bytes free there (Need counts the
// space it keeps free).
type DiskFullError struct {
	What string // what was refused ("dub", "render"…), may be ""
	Path string
	Need int64
	Free int64
}

func (e *DiskFullError) Error() string {
	what := e.What
	if what == "" {
		what = "this"
	}
	return fmt.Sprintf("Not enough free disk space: %s needs about %s and %s is free on the disk of %s.",
		what, SizeText(e.Need), SizeText(e.Free), e.Path)
}

// Is makes errors.Is(err, ErrDiskFull) hold.
func (e *DiskFullError) Is(target error) bool { return target == ErrDiskFull }

// DiskFullData are a disk_full error's data: bytes needed and free (0
// when not known, as for a write that failed).
type DiskFullData struct {
	Need int64  `json:"need,omitempty"`
	Free int64  `json:"free,omitempty"`
	Path string `json:"path,omitempty"`
}

// noSpaceText are what the errors of a full disk say: Go's ENOSPC text,
// the engine's (Foundation's NSFileWriteOutOfSpaceError, in English and
// Chinese) and ErrDiskFull's.
var noSpaceText = []string{
	"no space left on device", "isn’t enough space", "isn't enough space", "not enough free disk space",
	"没有足够的空间", "磁盘空间不足",
}

// NoSpace reports whether err says the disk is full: it wraps
// ErrDiskFull or ENOSPC, or its text says so (an engine error).
func NoSpace(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrDiskFull) || errors.Is(err, syscall.ENOSPC) {
		return true
	}
	return NoSpaceText(err.Error())
}

// NoSpaceText reports whether an error message says the disk is full.
func NoSpaceText(msg string) bool {
	m := strings.ToLower(msg)
	for _, s := range noSpaceText {
		if strings.Contains(m, s) {
			return true
		}
	}
	return false
}

// DiskFullDataOf are the data of err's DiskFullError (zero for a write
// that failed).
func DiskFullDataOf(err error) DiskFullData {
	var d *DiskFullError
	if errors.As(err, &d) {
		return DiskFullData{Need: d.Need, Free: d.Free, Path: d.Path}
	}
	return DiskFullData{}
}

// SizeText is a byte count for a message: "3.2 GB", "640 MB".
func SizeText(n int64) string {
	switch {
	case n >= 1e9:
		return strconv.FormatFloat(float64(n)/1e9, 'f', 1, 64) + " GB"
	case n >= 1e6:
		return strconv.FormatInt((n+5e5)/1e6, 10) + " MB"
	case n >= 1e3:
		return strconv.FormatInt((n+500)/1e3, 10) + " KB"
	}
	return strconv.FormatInt(max(n, 0), 10) + " B"
}
