package main

// How far a URL download is, in words, for the Dub page (its centre and
// the header status's tooltip), the activity card and the Tasks page: "Downloading the
// video… 37% · 48 / 130 MB · 1 min left".

import (
	"fmt"
	"math"
	"strings"

	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
)

// dubFetchStatus is a running download's status line.
func dubFetchStatus(pr jobs.Progress) string {
	switch pr.Stage {
	case services.FetchMerging:
		return i18n.T("Joining the video and audio…")
	case services.FetchFinishing:
		return i18n.T("Finishing the download…")
	}
	st := i18n.T("Downloading the video…")
	if d := dubFetchDetail(pr); d != "" {
		st += " " + d
	}
	return st
}

// dubFetchDetail is "37% · 48 / 130 MB · 1 min left", as much as is
// known ("" before anything is).
func dubFetchDetail(pr jobs.Progress) string {
	if pr.Stage != services.FetchDownloading && pr.Stage != "" {
		return ""
	}
	var parts []string
	if pr.Fraction >= 0 {
		parts = append(parts, percent(pr.Fraction))
	}
	switch {
	case pr.TotalBytes > 0:
		parts = append(parts, sizeOf(pr.Bytes, pr.TotalBytes))
	case pr.Bytes > 0:
		parts = append(parts, humanBytes(pr.Bytes))
	}
	if pr.ETA > 0 {
		parts = append(parts, timeLeft(pr.ETA))
	}
	return joinDot(parts)
}

// determinate is a job's fraction when it is known and under way, else -1:
// bars show only then.
func determinate(j jobs.Job) float64 {
	if j.State != jobs.StateRunning || j.Progress.Fraction <= 0 || j.Progress.Fraction > 1 {
		return -1
	}
	return j.Progress.Fraction
}

// percent is "37%".
func percent(f float64) string {
	return fmt.Sprintf("%d%%", int(100*math.Min(math.Max(f, 0), 1)))
}

// sizeOf is "48 / 130 MB" (or "0.4 / 1.3 GB").
func sizeOf(done, total int64) string {
	if total >= 1e9 {
		return fmt.Sprintf("%.1f / %.1f GB", float64(done)/1e9, float64(total)/1e9)
	}
	return fmt.Sprintf("%.0f / %.0f MB", float64(done)/1e6, math.Max(float64(total)/1e6, 1))
}

// timeLeft is "20 s left", "3 min left", "1 h 5 min left": coarse (to
// 5 s under a minute, else to the nearest minute), as yt-dlp's speed varies.
func timeLeft(s float64) string {
	n := int(math.Ceil(s))
	switch {
	case n < 60:
		return i18n.T("%d s left", max(5, (n+4)/5*5))
	case n < 3600:
		return i18n.T("%d min left", (n+30)/60)
	}
	m := (n + 30) / 60
	return i18n.T("%d h %d min left", m/60, m%60)
}

func joinDot(parts []string) string { return strings.Join(parts, " · ") }
