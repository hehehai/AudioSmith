package settings

// M7a sections (thread #22): the first-run setup, the media tools
// (ffmpeg), and the appearance fields the sidebar uses.

import (
	"encoding/json"
	"slices"
	"strings"
)

// Setup: the first-run wizard.
type Setup struct {
	// Done: the wizard ran to its end (or was skipped). A fresh install
	// shows it; an older document without this section is an install that
	// predates the wizard, which counts as done (migrateM7).
	Done bool `json:"done"`
	// Pack is the model pack chosen ("fast", "balanced", "quality",
	// "max"; "" = none), FirstSound whether the welcome line played.
	Pack       string `json:"pack,omitempty"`
	FirstSound bool   `json:"firstSound,omitempty"`
	// Starters maps the bundled starter voices imported (their file
	// names) to the voices they became: each is imported once, so one
	// the user deleted stays deleted.
	Starters map[string]string `json:"starters,omitempty"`
	// Intros are the features whose 「介绍」 dialog showed (0.5 F6: the
	// first visit to Dub, Speech to text…); Settings → General clears it.
	Intros []string `json:"intros,omitempty"`
	// Samples maps a sample opened (its id) to the copy it made, as
	// "<kind>:<id>" (app/samples.go): opening it again asks.
	Samples map[string]string `json:"samples,omitempty"`
}

// Media: the optional ffmpeg build that adds formats AVFoundation lacks
// (MP3, Opus and FLAC output, WebM/MKV input, burnt-in subtitles).
type Media struct {
	// FFmpeg is the ffmpeg executable to use; "" = none (AVFoundation
	// only). ffprobe is looked for beside it.
	FFmpeg string `json:"ffmpeg,omitempty"`
	// Origin says where it came from: MediaDownloaded, MediaSystem or
	// MediaCustom.
	Origin string `json:"origin,omitempty"`
}

// Media origins.
const (
	MediaDownloaded = "download" // the pinned build, downloaded into <data>/tools/ffmpeg
	MediaSystem     = "system"   // Homebrew's or another on the PATH
	MediaCustom     = "custom"   // a file the user chose
)

// Sidebar width bounds, DIPs at 100 %.
const (
	MinSidebarWidth = 170
	MaxSidebarWidth = 320
)

func normalizeM7(s *Settings) {
	ap := &s.Appearance
	if ap.SidebarWidth != 0 {
		ap.SidebarWidth = min(max(ap.SidebarWidth, MinSidebarWidth), MaxSidebarWidth)
	}
	var closed []string
	for _, c := range ap.ClosedSections {
		if c = strings.TrimSpace(c); c != "" && !slices.Contains(closed, c) {
			closed = append(closed, c)
		}
	}
	ap.ClosedSections = closed
	s.Media.FFmpeg = strings.TrimSpace(s.Media.FFmpeg)
	if s.Media.FFmpeg == "" {
		s.Media.Origin = ""
	}
	switch s.Setup.Pack {
	case "", "fast", "balanced", "quality", "max":
	default:
		s.Setup.Pack = ""
	}
}

func cloneM7(s *Settings) {
	s.Appearance.ClosedSections = slices.Clone(s.Appearance.ClosedSections)
}

// migrateM7 marks the setup done in documents saved before the wizard
// existed: their user has set AudioSmith up already.
func migrateM7(data []byte, s *Settings) {
	var top map[string]json.RawMessage
	if json.Unmarshal(data, &top) != nil {
		return
	}
	if _, ok := top["setup"]; !ok {
		s.Setup.Done = true
	}
}
