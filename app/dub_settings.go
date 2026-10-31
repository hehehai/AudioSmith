package main

// Settings → Dubbing: importing videos from links (opt-in: downloads
// yt-dlp's standalone macOS build, pinned and checked by SHA-256, into the
// app's data folder), and the defaults of a new dub.

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"slices"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/doc/textenc"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/gallery"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/oplog"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// ytdlpState is the yt-dlp download.
type ytdlpState struct {
	installing bool
	done, size int64
	err        string
	cancel     context.CancelFunc
}

// ytdlpDir is where yt-dlp goes: <data>/tools.
func (a *app) ytdlpDir() string {
	return filepath.Join(filepath.Dir(a.lib.store.Dubs().Dir()), "tools")
}

// installYtDlp downloads yt-dlp and turns URL import on.
func (a *app) installYtDlp() {
	y := &a.ytdlp
	if y.installing {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	*y = ytdlpState{installing: true, size: dub.YtDlpSize, cancel: cancel}
	dir := a.ytdlpDir()
	src := githubClient(a.prefs) // the download source and proxy as of now
	a.background(func() {
		path, from, err := ytdlpInstall(ctx, src, dir, func(done, total int64) {
			a.post(func() { y.done, y.size = done, total })
		})
		if err == nil {
			log.Printf("yt-dlp %s: installed from %s", dub.YtDlpVersion, from)
		} else if ctx.Err() == nil {
			log.Printf("yt-dlp %s: %v", dub.YtDlpVersion, err)
		}
		a.post(func() {
			y.installing, y.cancel = false, nil
			if err != nil {
				if ctx.Err() == nil {
					y.err = dubURLErrorText(err)
				}
				return
			}
			a.updateSettings(func(s *settings.Settings) { s.Dubbing.URLImport, s.Dubbing.YtDlp = true, path })
			a.toast(i18n.T("Importing from links is on."))
		})
	})
}

// ytdlpInstall downloads yt-dlp (tests replace it).
var ytdlpInstall = dub.InstallYtDlp

// removeYtDlp turns URL import off and deletes yt-dlp.
func (a *app) removeYtDlp() {
	path := a.prefs.Dubbing.YtDlp
	a.updateSettings(func(s *settings.Settings) { s.Dubbing.URLImport, s.Dubbing.YtDlp = false, "" })
	if path != "" && filepath.Dir(path) == a.ytdlpDir() {
		a.background(func() { oplog.Err("file.remove", os.Remove(path), "file", filepath.Base(path)) })
	}
}

// dubbingSettings is Settings → Dubbing.
func (a *app) dubbingSettings(c *ui.Context) {
	t := c.Theme()
	d := a.prefs.Dubbing
	settingGroup(c, i18n.T("Import from links"), func() {
		settingNote(c, "ytdlp-intro", i18n.T("Dub a video from a YouTube (or other) link on the Dub page. It needs yt-dlp (about 35 MB, from GitHub or the ActNow mirror, verified). Links to this Mac or the local network, live streams and playlists are refused."))
		y := &a.ytdlp
		switch {
		case y.installing:
			settingBlock(c, "ytdlp-progress", func() {
				ui.Row(c).Gap(px(t, 8)).AlignItems(ui.Center).Children(func() {
					frac := 0.0
					if y.size > 0 {
						frac = float64(y.done) / float64(y.size)
					}
					ui.Progress(c, frac).Grow(1)
					caption(c, i18n.T("%.0f of %.0f MB", float64(y.done)/1e6, float64(y.size)/1e6)).SingleLine()
					if secondaryButton(c.Key("ytdlp-cancel"), i18n.T("Cancel")).Clicked() && y.cancel != nil {
						y.cancel()
					}
				})
			})
		case d.URLImport && d.YtDlp != "":
			settingRowNote(c, "ytdlp-on", i18n.T("Importing from links is on"), i18n.T("yt-dlp %s · %s", dub.YtDlpVersion, shortPath(d.YtDlp)), func() {
				if secondaryButton(c.Key("ytdlp-remove"), i18n.T("Turn off and remove")).Clicked() {
					a.removeYtDlp()
				}
			})
		default:
			settingRow(c, "ytdlp-off", i18n.T("Importing from links is off"), i18n.T("Turning it on downloads yt-dlp %s.", dub.YtDlpVersion), func() {
				if primaryButton(c.Key("ytdlp-install"), i18n.T("Turn on")).Clicked() {
					a.installYtDlp()
				}
			})
		}
		if y.err != "" {
			settingFoot(c, "ytdlp-err", func() {
				ui.Row(c).Gap(px(t, 6)).Children(func() {
					iconMark(c, icons.DangerCircle, t.Danger, "")
					caption(c, y.err).TextColor(t.Danger).Grow(1).MinWidth(0)
				})
			})
		}

	})
	settingGroup(c, i18n.T("New dubs"), func() {
		review := !d.NoReview
		settingRow(c, "dub-review", i18n.T("Review after each step"), i18n.T("Stops after the transcript and after the translation, so they can be checked first."), func() {
			if inkSwitch(c.Key("dub-review"), &review).Label(i18n.T("Review after each step")).Changed() {
				a.updateSettings(func(s *settings.Settings) { s.Dubbing.NoReview = !review })
			}
		})
		models := dubTTSModels()
		names := []string{i18n.T("Automatic")}
		repos := []string{""}
		for _, e := range models {
			names = append(names, e.DisplayName())
			repos = append(repos, e.Repo)
		}
		cur := names[max(slices.Index(repos, d.Model), 0)]
		settingRow(c, "dub-model", i18n.T("Voice model"), i18n.T("Automatic uses a downloaded model that clones voices."), func() {
			if ui.Select(c.Key("dub-model"), &cur, names).Label(i18n.T("Voice model")).Changed() {
				repo := repos[slices.Index(names, cur)]
				a.updateSettings(func(s *settings.Settings) { s.Dubbing.Model = repo })
			}
		})
		// "" is the default (dub.DefaultTarget, switched for a video in that
		// language); a language picked here is used as it is.
		langs := []string{""}
		langNames := []string{i18n.T("%s (default)", dubLangName(dub.DefaultTarget))}
		for _, l := range dubTargetLangs {
			langs = append(langs, l)
			langNames = append(langNames, dubLangName(l))
		}
		target := ""
		if len(d.Targets) > 0 {
			target = d.Targets[0]
			if !slices.Contains(langs, target) {
				langs, langNames = append(langs, target), append(langNames, dubLangName(target))
			}
		}
		tcur := langNames[max(slices.Index(langs, target), 0)]
		settingRow(c, "dub-target", i18n.T("Dub into"), dubAutoTargetHint(), func() {
			if ui.Select(c.Key("dub-target"), &tcur, langNames).Label(i18n.T("Dub into")).Changed() {
				l := langs[slices.Index(langNames, tcur)]
				a.updateSettings(func(s *settings.Settings) {
					s.Dubbing.Targets = nil
					if l != "" {
						s.Dubbing.Targets = []string{l}
					}
				})
			}
		})
	})
}

// --- small helpers of the dub area -------------------------------------------------------

// dubPresets are the design presets the cast offers.
func dubPresets() []gallery.Preset { return gallery.Presets() }

// dubPreset finds a design preset, one no longer offered too.
func dubPreset(id string) (gallery.Preset, bool) { return gallery.PresetByID(id) }

// galleryArchetype finds a gallery voice.
func galleryArchetype(id string) (gallery.Archetype, bool) { return gallery.Default().ByID(id) }

// decodeText decodes a text file of any common encoding.
func decodeText(b []byte) string {
	s, _ := textenc.Decode(b)
	return s
}

// readFileText reads a text file of any common encoding.
func readFileText(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return decodeText(b), nil
}

// shortPath shows a path with ~ for the home folder.
func shortPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, p); err == nil && !filepath.IsAbs(rel) && rel != ".." && len(rel) < len(p) && rel[:min(2, len(rel))] != ".." {
			return "~/" + rel
		}
	}
	return p
}

// dialogOpen asks for one file with ext (blocks: call on a goroutine).
func dialogOpen(a *app, title string, exts ...string) ([]string, error) {
	return mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: title,
		Filters: []mygo.FileFilter{{Name: title, Extensions: exts}}})
}
