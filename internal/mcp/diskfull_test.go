package mcp

import (
	"strings"
	"testing"

	"github.com/hehehai/AudioSmith/internal/core"
)

// Code review 1102 M4: agents are told the disk is full, with the sizes.
func TestToolErrorDiskFull(t *testing.T) {
	err := toolError(&core.DiskFullError{What: "the render", Path: "/data", Need: 3e9, Free: 1e9})
	if msg := err.Error(); !strings.HasPrefix(msg, "disk_full: ") || !strings.Contains(msg, "3.0 GB") || !strings.Contains(msg, "1.0 GB") {
		t.Errorf("tool error %q", msg)
	}
}
