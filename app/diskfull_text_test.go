package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
)

// Code review 1102 M4: a full disk is explained, in the UI's language,
// with what the work needs and what is free.
func TestDiskFullText(t *testing.T) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	pre := fmt.Errorf("dub x: %w", &core.DiskFullError{What: "the dub", Path: "/data", Need: 3.2e9, Free: 1.1e9})
	job := services.DiskFullJob(pre)
	for _, loc := range []string{"en", "zh-Hans"} {
		i18n.SetLocale(loc)
		for _, got := range []string{engineErrorText(pre), jobErrorText(job)} {
			if !strings.Contains(got, "3.2 GB") || !strings.Contains(got, "1.1 GB") || strings.Contains(got, "/data") {
				t.Errorf("%s: %q", loc, got)
			}
			if loc == "zh-Hans" && !strings.Contains(got, "磁盘空间不足") {
				t.Errorf("not translated: %q", got)
			}
		}
		// A write that found the disk full (the engine's words).
		got := jobErrorText(&jobs.Error{Code: proto.CodeInternal, Message: "write /x: no space left on device"})
		if want := diskFullText(nil); got != want || strings.Contains(got, "/x") {
			t.Errorf("%s: write %q", loc, got)
		}
	}
}
