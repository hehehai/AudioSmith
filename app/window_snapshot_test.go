package main

import (
	"context"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/store/historyjson"
)

// TestSnapshotsWindow renders the window in the ActNow family's layout:
// the activity cards at the sidebar's foot (closed, open, a stopped
// engine), the Tasks page, the sidebar's search on Projects, every
// Settings section, ⌘E and the icon rail, light and dark, in English and
// Chinese (window-*-light.png, window-*-light-zh-Hans.png):
//
//	AUDIOSMITH_SNAPSHOTS=/tmp/shots go test -run TestSnapshotsWindow .
func TestSnapshotsWindow(t *testing.T) { runSnapshots(t, snapshotsWindow) }

func snapshotsWindow(t *testing.T, out string) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	savedSys := readSysInfo
	t.Cleanup(func() { readSysInfo = savedSys })
	readSysInfo = func() sysInfo {
		return sysInfo{OS: "macOS 15.4", Chip: "Apple M4", Memory: 16 << 30, Cores: 10, Arch: "arm64"}
	}
	for _, run := range []struct {
		lang string
		dark bool
	}{{"en", false}, {"en", true}, {"zh-Hans", false}, {"zh-Hans", true}} {
		i18n.SetLocale("en")
		a, eng, tt := newTestApp(t)
		if run.lang != "en" {
			a.updateSettings(func(s *settings.Settings) { s.General.Language = run.lang })
		}
		tt.SetDark(run.dark)
		tt.SetScale(2)
		a.loadedModel = kokoro
		suffix := "light"
		if run.dark {
			suffix = "dark"
		}
		if run.lang != "en" {
			suffix += "-" + run.lang
		}
		save := func(name string) {
			tt.Frame()
			tt.Frame()
			f, err := os.Create(filepath.Join(out, "window-"+name+"-"+suffix+".png"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := png.Encode(f, tt.Image()); err != nil {
				t.Fatal(err)
			}
		}
		for i, text := range []string{
			"Hello from AudioSmith. This voice was generated on your Mac with MLX.",
			"你好，这是在 Mac 上用 MLX 生成的语音。",
			"Hello again: the same voice, a little faster, for the trailer cut.",
		} {
			at := time.Date(2026, 10, 13, 21, 50+i, 0, 0, time.Local)
			addTake(t, a, core.Take{ID: historyjson.NewTakeID(at), CreatedAt: at, Text: text, Model: kokoro,
				Voice: []string{"af_heart", "zf_xiaobei", "am_adam"}[i], Speed: 1, SampleRate: 24000, AudioSeconds: 5.1 - float64(i), GenSeconds: 1.2}, 1.2)
		}

		// What goes on: a take being generated, a model downloading, an
		// export waiting.
		a.navigate("/studio")
		a.generate()
		eng.progress(a.job.id, "generating", 0.42, 2.1)
		dl := a.model(entry(t, "chatterbox-turbo-8bit"))
		dl.downloading, dl.progress = true, hub.Progress{Bytes: 480e6, TotalBytes: 1202987769}
		exp, _ := eng.host.sched.Submit(context.Background(), jobs.Spec{Kind: services.KindEncode, Lane: jobs.LaneCPU,
			Title: i18n.T("Export %s", "Harrow Point.m4b"), Owner: services.DubExportOwner + "d1"})
		eng.update(exp, func(j *jobs.Job) { j.State = jobs.StateQueued })
		a.toasts = nil
		save("activity")
		a.noticesOpen = true // the bell's popover (shell_topbar.go)
		save("activity-open")
		a.noticesOpen = false
		a.navigate("/tasks")
		save("tasks")
		eng.cancelled(exp)
		eng.cancelled(a.job.id)
		dl.downloading = false
		a.toasts = nil

		// A stopped engine: a red card on top.
		a.engState, a.engError = engineFailed, "the engine quit (signal: killed)"
		a.navigate("/history")
		save("engine-stopped")
		a.engState, a.engError = engineReady, ""

		// The sidebar's search, on Projects.
		a.projects.query = "hello"
		a.navigate("/projects")
		a.refreshProjects()
		save("search")
		a.projects.query = ""

		// Every Settings section.
		for _, s := range settingsSections() {
			a.navigate("/settings/" + s.id)
			save("settings-" + s.id)
		}
		a.settingsOpen = false
		a.toasts = nil

		// ⌘E, and the icon rail.
		a.navigate("/studio")
		a.switchOpen = true
		save("quickswitch")
		a.switchOpen = false
		a.setRail(true)
		save("rail")
		a.setRail(false)
	}
}
