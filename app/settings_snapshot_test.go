package main

import (
	"context"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/ffmpeg"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// TestSnapshotsM7 renders M7a's views (the icon rail, Settings → Audio
// tools, Storage, Diagnostics, Network, Usage, Privacy, Feedback,
// Appearance, the first-run wizard) light and dark, in English and Chinese:
//
//	AUDIOSMITH_SNAPSHOTS=/tmp/shots go test -run TestSnapshots .
func TestSnapshotsM7(t *testing.T) { runSnapshots(t, snapshotsM7) }

func snapshotsM7(t *testing.T, out string) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	fake := fakeFFmpeg(t)
	savedSys, savedReach := readSysInfo, reach
	t.Cleanup(func() { readSysInfo, reach = savedSys, savedReach })
	readSysInfo = func() sysInfo {
		return sysInfo{OS: "macOS 15.4", Chip: "Apple M4", Memory: 16 << 30, Cores: 10, Arch: "arm64"}
	}
	reach = func(_ *http.Client, host string) netCheck {
		return netCheck{host: host, ok: true, latency: 120 * time.Millisecond}
	}
	ffmpegFind = func() []string { return []string{"/opt/homebrew/bin/ffmpeg"} }
	t.Cleanup(func() { ffmpegFind = ffmpeg.FindSystem })
	for _, run := range []struct {
		lang string
		dark bool
	}{{"en", false}, {"en", true}, {"zh-Hans", false}, {"zh-Hans", true}} {
		i18n.SetLocale("en")
		a, _, tt := newTestApp(t)
		if run.lang != "en" {
			a.updateSettings(func(s *settings.Settings) { s.General.Language = run.lang })
		}
		tt.SetDark(run.dark)
		tt.SetScale(2)
		suffix := "light"
		if run.dark {
			suffix = "dark"
		}
		if run.lang != "en" {
			suffix += "-" + run.lang
		}
		shot := func(tt *ui.Tester, name string) {
			tt.Frame()
			tt.Frame()
			f, err := os.Create(filepath.Join(out, name+"-"+suffix+".png"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := png.Encode(f, tt.Image()); err != nil {
				t.Fatal(err)
			}
		}
		save := func(name string) { shot(tt, name) }
		tall := ui.NewTester(a.view, 1100, 1600)
		tall.SetDark(run.dark)

		// Audio tools: not set up, then ready.
		a.navigate("/settings/audio-tools")
		save("settings-audio-tools-none")
		tool, _ := ffmpeg.Inspect(context.Background(), fake)
		tool.FFmpeg = "/opt/homebrew/bin/ffmpeg"
		a.updateSettings(func(s *settings.Settings) {
			s.Media.FFmpeg, s.Media.Origin = "/opt/homebrew/bin/ffmpeg", settings.MediaSystem
		})
		a.ffPath, a.ffChecking = "/opt/homebrew/bin/ffmpeg", false
		a.ffTool.Store(tool)
		shot(tall, "settings-audio-tools")

		// Settings pages.
		for i, text := range []string{"Hello from AudioSmith.", "你好。", "Bonjour."} {
			at := time.Date(2026, 10, 10+i, 10, 0, 0, 0, time.Local)
			addTake(t, a, core.Take{ID: core.NewID(at), CreatedAt: at, Text: text, Model: kokoro, Voice: "af_heart",
				Language: []string{"en", "zh", "fr"}[i], SampleRate: 24000, AudioSeconds: 40, GenSeconds: 8, Starred: i == 0}, 0.5)
		}
		a.navigate("/settings/storage")
		shot(tall, "settings-storage")
		a.navigate("/settings/about")
		a.runSelfCheck()
		shot(tall, "settings-about")
		a.navigate("/settings/network")
		a.testNetwork(nil)
		shot(tall, "settings-network")
		a.navigate("/settings/usage")
		shot(tall, "settings-usage")
		a.navigate("/settings/privacy")
		shot(tall, "settings-privacy")
		connectFeedbackShots(t, a) // feedback_test.go
		a.navigate("/settings/feedback")
		shot(tall, "settings-feedback")
		a.navigate("/settings/appearance")
		save("settings-appearance-m7")

		// The first-run wizard, each step.
		a.navigate("/studio")
		a.openOnboarding()
		for i, name := range []string{"system", "models", "privacy", "dictation"} {
			a.obStep = i
			save("onboarding-" + name)
		}
		a.obOpen = false

		// The icon rail.
		a.navigate("/studio")
		a.setRail(true)
		save("rail")
		a.setRail(false)
	}
}
