package main

import (
	"encoding/json"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

// diskFullText explains a disk_full error (code review 1102 M4): what the
// work needs and what is free when a pre-check refused it, else that the
// disk filled up while it wrote.
func diskFullText(e *jobs.Error) string {
	var d core.DiskFullData
	if e != nil {
		json.Unmarshal(e.Data, &d)
	}
	if d.Need > 0 {
		return i18n.T("Not enough free disk space: this needs about %s and %s is free. Free up some space (Settings → Storage shows what AudioSmith keeps), then try again.",
			humanBytes(d.Need), humanBytes(max(d.Free, 0)))
	}
	return i18n.T("The disk is full. Free up some space (Settings → Storage shows what AudioSmith keeps), then try again.")
}
