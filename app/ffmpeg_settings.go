package main

// Settings → Audio tools (M7a): the optional ffmpeg that adds formats
// AVFoundation lacks. Where it comes from (the pinned download, a copy
// already on the Mac, a chosen file), its version, licence and what it
// can do; use another, remove the download. ffmpeg_engine.go puts it
// behind the engine's media methods.

import (
	"context"
	"log"
	"path/filepath"
	"sync/atomic"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/ffmpeg"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/oplog"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// mediaToolsState is the ffmpeg in use and the Audio tools page's state.
type mediaToolsState struct {
	ffTool     atomic.Pointer[ffmpeg.Tool] // nil: none; read on any goroutine
	ffPath     string                      // the path ffTool (or ffErr) is about
	ffErr      string                      // why the chosen ffmpeg does not run
	ffSystem   []string                    // copies found on this Mac
	ffScanned  bool
	ffInstall  ffInstallState
	ffChecking bool
}

type ffInstallState struct {
	running    bool
	done, size int64
	err        string
	cancel     context.CancelFunc
}

// ffmpegDownloadOffered shows "Download ffmpeg" (the pinned build). Off
// until the build to offer is decided (no reputable LGPL-only static
// arm64 build exists; the pinned one is GPL): the page offers an ffmpeg
// already on the Mac (Homebrew) or a chosen file. Tests turn it on.
var ffmpegDownloadOffered = false

// Test seams: the download and the search for copies on this Mac.
var (
	ffmpegInstall = ffmpeg.Install
	ffmpegFind    = ffmpeg.FindSystem
)

// mediaTool is the ffmpeg in use, nil when none (any goroutine).
func (a *app) mediaTool() *ffmpeg.Tool { return a.ffTool.Load() }

// ffmpegDir is where the download goes: <data>/tools/ffmpeg.
func (a *app) ffmpegDir() string { return filepath.Join(a.lib.store.Dir(), "tools", "ffmpeg") }

// loadMediaTool inspects the ffmpeg the settings name (at start, and
// when they change).
func (a *app) loadMediaTool() {
	path := a.prefs.Media.FFmpeg
	if path == a.ffPath && (a.mediaTool() != nil || a.ffErr != "") {
		return
	}
	a.ffPath, a.ffErr = path, ""
	if path == "" {
		a.ffTool.Store(nil)
		return
	}
	a.ffChecking = true
	a.background(func() {
		tool, err := ffmpeg.Inspect(context.Background(), path)
		a.post(func() {
			if a.ffPath != path {
				return // changed meanwhile
			}
			a.ffChecking = false
			if err != nil {
				a.ffTool.Store(nil)
				a.ffErr = err.Error()
				return
			}
			a.ffTool.Store(tool)
		})
	})
}

// useFFmpeg makes path the ffmpeg in use.
func (a *app) useFFmpeg(path, origin string) {
	a.ffErr = ""
	a.updateSettings(func(s *settings.Settings) { s.Media.FFmpeg, s.Media.Origin = path, origin })
	a.loadMediaTool()
}

// scanFFmpeg looks for copies of ffmpeg on this Mac.
func (a *app) scanFFmpeg() {
	a.background(func() {
		found := ffmpegFind()
		a.post(func() { a.ffSystem, a.ffScanned = found, true })
	})
}

// installFFmpeg downloads the pinned build and uses it.
func (a *app) installFFmpeg() {
	st := &a.ffInstall
	if st.running {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	*st = ffInstallState{running: true, size: ffmpeg.Pinned.Size(), cancel: cancel}
	dir := a.ffmpegDir()
	src := githubClient(a.prefs) // the download source and proxy as of now
	a.background(func() {
		path, err := ffmpegInstall(ctx, src, dir, ffmpeg.Pinned, func(done, total int64) {
			a.post(func() { st.done, st.size = done, total })
		})
		if err != nil && ctx.Err() == nil {
			log.Printf("ffmpeg: download: %v", err) // each file's source: "mirror: … from …"
		}
		a.post(func() {
			st.running, st.cancel = false, nil
			if err != nil {
				if ctx.Err() == nil {
					st.err = i18n.T("The download failed: %v", err)
				}
				return
			}
			a.useFFmpeg(path, settings.MediaDownloaded)
			a.toast(i18n.T("ffmpeg is ready"))
		})
	})
}

// removeFFmpeg stops using ffmpeg; a downloaded copy is deleted.
func (a *app) removeFFmpeg() {
	origin, dir := a.prefs.Media.Origin, a.ffmpegDir()
	a.useFFmpeg("", "")
	if origin == settings.MediaDownloaded {
		a.background(func() { oplog.Err("ffmpeg.remove", ffmpeg.Remove(dir)) })
	}
}

// chooseFFmpeg asks for an ffmpeg executable.
func (a *app) chooseFFmpeg() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: i18n.T("Choose ffmpeg")})
		if err != nil || len(paths) == 0 {
			return
		}
		a.post(func() { a.useFFmpeg(paths[0], settings.MediaCustom) })
	}()
}

// ffOriginText says where the ffmpeg in use came from.
func ffOriginText(origin string) string {
	switch origin {
	case settings.MediaDownloaded:
		return i18n.T("Downloaded by AudioSmith")
	case settings.MediaSystem:
		return i18n.T("Installed on this Mac")
	case settings.MediaCustom:
		return i18n.T("Chosen file")
	}
	return ""
}

// ffCapabilities lists what an ffmpeg adds, each with whether this one can.
func ffCapabilities(tool *ffmpeg.Tool) []struct {
	what string
	ok   bool
} {
	return []struct {
		what string
		ok   bool
	}{
		{i18n.T("MP3 export"), tool.CanEncode(ffmpeg.MP3)},
		{i18n.T("Opus export"), tool.CanEncode(ffmpeg.Opus)},
		{i18n.T("FLAC export"), tool.CanEncode(ffmpeg.FLAC)},
		{i18n.T("WebM, MKV, VP9 and AV1 files"), tool != nil && tool.FFprobe != ""},
		{i18n.T("Subtitles burnt into videos"), tool.CanBurn()},
	}
}

// audioToolsSettings is Settings → Audio tools.
func (a *app) audioToolsSettings(c *ui.Context) {
	t := c.Theme()
	if !a.ffScanned {
		a.ffScanned = true
		a.scanFFmpeg()
	}
	caption(c, i18n.T("AudioSmith uses macOS for audio and video. ffmpeg, optional and free, adds MP3, Opus and FLAC export, WebM and MKV, and burnt-in subtitles; it runs as a separate program."))

	m := a.prefs.Media
	settingGroup(c, i18n.T("ffmpeg"), func() {
		tool := a.mediaTool()
		switch {
		case m.FFmpeg == "":
			settingRowNote(c, "ff-none", i18n.T("Not set up"), i18n.T("Exports are WAV, M4A and M4B; videos must be MP4 or MOV."), func() {})
		case a.ffChecking:
			settingRowNote(c, "ff-checking", i18n.T("Checking ffmpeg…"), shortPath(m.FFmpeg), func() { ui.Spinner(c) })
		case tool == nil:
			settingRowNote(c, "ff-broken", i18n.T("ffmpeg does not run"), orDash(a.ffErr), func() {
				if secondaryButton(c.Key("ff-retry"), i18n.T("Try again")).Clicked() {
					a.ffPath = ""
					a.loadMediaTool()
				}
			})
		default:
			desc := i18n.T("Version %s · %s licence · %s", tool.Version, tool.Licence, ffOriginText(m.Origin)) + "\n" + shortPath(tool.FFmpeg)
			if tool.FFprobe == "" {
				desc += "\n" + i18n.T("ffprobe is missing beside it: WebM and MKV files cannot be read.")
			}
			settingRowNote(c, "ff-ready", i18n.T("ffmpeg is ready"), desc, func() {
				label := i18n.T("Stop using it")
				if m.Origin == settings.MediaDownloaded {
					label = i18n.T("Remove")
				}
				if secondaryButton(c.Key("ff-remove"), label).Clicked() {
					a.removeFFmpeg()
				}
			})
			settingFoot(c, "ff-caps", func() {
				for _, cp := range ffCapabilities(tool) {
					mark, col := icons.CheckCircle, t.Success
					if !cp.ok {
						mark, col = icons.CloseCircle, t.TextMuted
					}
					ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, mark.Linear).Size(px(t, 14), px(t, 14)).TextColor(col).Shrink(0)
						ui.Text(c, cp.what).FontSize(px(t, 12))
					})
				}
			})
		}

		if ffmpegDownloadOffered || a.ffInstall.running || m.Origin == settings.MediaDownloaded {
			a.ffmpegDownloadSection(c)
		}

	})
	settingGroup(c, i18n.T("On this Mac"), func() {
		if a.ffScanned && len(a.ffSystem) == 0 {
			settingNote(c, "ff-sys-none", i18n.T("No ffmpeg found in Homebrew's or MacPorts' folders. With Homebrew: brew install ffmpeg"))
		}
		for _, p := range a.ffSystem {
			settingRow(c, "ff-sys-"+p, shortPath(p), "", func() {
				inUse := m.FFmpeg == p
				if secondaryButton(c.Key("ff-use"), i18n.T("Use this one")).Disabled(inUse).Clicked() {
					a.useFFmpeg(p, settings.MediaSystem)
				}
			})
		}
		settingBlock(c, "ff-sys-actions", func() {
			ui.Row(c).Gap(px(t, 8)).Children(func() {
				if secondaryButton(c.Key("ff-rescan"), i18n.T("Look again")).Clicked() {
					a.scanFFmpeg()
				}
				if a.win != nil && secondaryButton(c.Key("ff-choose"), i18n.T("Choose a file…")).Clicked() {
					a.chooseFFmpeg()
				}
			})
		})
	})
}

// ffmpegDownloadSection offers the pinned build (see ffmpegDownloadOffered).
func (a *app) ffmpegDownloadSection(c *ui.Context) {
	settingGroup(c, i18n.T("Get ffmpeg"), func() { a.ffmpegDownloadRows(c) })
}

// ffmpegDownloadRows are the pinned build's download and its state.
func (a *app) ffmpegDownloadRows(c *ui.Context) {
	t := c.Theme()
	m, tool := a.prefs.Media, a.mediaTool()
	b := ffmpeg.Pinned
	st := &a.ffInstall
	desc := i18n.T("ffmpeg %s for Apple silicon from %s on GitHub or the ActNow mirror (%s), checked by SHA-256 before use.", b.Version, b.Name, humanBytes(b.Size())) + " " +
		i18n.T("Its licence is %s: AudioSmith does not include it, and runs it as a separate program.", b.Licence)
	switch {
	case st.running:
		settingBlock(c, "ff-progress", func() {
			ui.Row(c).Gap(px(t, 8)).AlignItems(ui.Center).Children(func() {
				frac := 0.0
				if st.size > 0 {
					frac = float64(st.done) / float64(st.size)
				}
				ui.Progress(c, frac).Grow(1)
				ui.Text(c, i18n.T("%.0f of %.0f MB", float64(st.done)/1e6, float64(st.size)/1e6)).FontSize(px(t, 12)).TextColor(t.TextMuted)
				if secondaryButton(c.Key("ff-cancel"), i18n.T("Cancel")).Clicked() && st.cancel != nil {
					st.cancel()
				}
			})
		})
	default:
		settingRowNote(c, "ff-download", i18n.T("Download ffmpeg"), desc, func() {
			disabled := m.Origin == settings.MediaDownloaded && tool != nil
			if secondaryButton(c.Key("ff-install"), i18n.T("Download")).Disabled(disabled).Clicked() {
				a.installFFmpeg()
			}
		})
	}
	if st.err != "" {
		settingFoot(c, "ff-err", func() { ui.Text(c, st.err).FontSize(px(t, 12)).TextColor(t.Danger) })
	}

}

// ffmpegSettingsChanged follows the settings (shell_settings.go).
func (a *app) ffmpegSettingsChanged(old, new settings.Settings) {
	if old.Media != new.Media {
		a.loadMediaTool()
	}
}

// ffmpegHas reports whether the ffmpeg in use writes format.
func (a *app) ffmpegHas(format string) bool { return a.mediaTool().CanEncode(format) }
