package main

// A take's output panel (the Studio's latest take, the History's details):
// waveform with click-to-seek and a handle to drag the file out, file
// details, the render's warnings, Check audio (findings that seek when
// clicked), Export… (the Export audio dialog, export_dialog.go), Show in
// Finder.

import (
	"context"
	"fmt"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// takeBits is the bit depth of a take's file.
func takeBits(t *core.Take) int {
	if t.Render != nil && t.Render.Bits != 0 {
		return t.Render.Bits
	}
	return 16
}

// bitsLabel names a bit depth.
func bitsLabel(bits int) string {
	if bits == 32 {
		return i18n.T("32-bit float")
	}
	return i18n.T("%d-bit", bits)
}

// humanBytes formats a file size: "840 KB", "12.4 MB".
func humanBytes(n int64) string {
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%.1f GB", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.1f MB", float64(n)/1e6)
	case n >= 1e3:
		return fmt.Sprintf("%.0f KB", float64(n)/1e3)
	}
	return fmt.Sprintf("%d B", n)
}

// fileDetails is "1.2 MB · 24 kHz · 16-bit · mono".
func fileDetails(t *core.Take) string {
	var parts []string
	if t.Render != nil && t.Render.Bytes > 0 {
		parts = append(parts, humanBytes(t.Render.Bytes))
	}
	if t.SampleRate > 0 {
		parts = append(parts, i18n.T("%s kHz", strings.TrimSuffix(fmt.Sprintf("%.1f", float64(t.SampleRate)/1000), ".0")))
	}
	parts = append(parts, bitsLabel(takeBits(t)))
	ch := 1
	if t.Render != nil && t.Render.Channels > 0 {
		ch = t.Render.Channels
	}
	if ch == 1 {
		parts = append(parts, i18n.T("mono"))
	} else {
		parts = append(parts, i18n.T("stereo"))
	}
	return strings.Join(parts, " · ")
}

// renderedIn is "5.1 s · rendered in 1.2 s".
func renderedIn(t *core.Take) string {
	s := i18n.T("%.1f s", t.AudioSeconds)
	if t.GenSeconds > 0 {
		s += " · " + i18n.T("rendered in %.1f s", t.GenSeconds)
	}
	return s
}

// seekTake plays a take from seconds in.
func (a *app) seekTake(t *core.Take, seconds float64) {
	if a.pl.Current() != t.ID || a.pl.Streaming(t.ID) {
		a.pl.Stop()
		a.play(t)
	}
	if t.AudioSeconds > 0 {
		a.pl.Seek(t.ID, float32(min(max(seconds/t.AudioSeconds, 0), 1)))
	}
}

// checkTake runs Check audio over a take in the background; the take
// comes back through TakeUpdated.
func (a *app) checkTake(t *core.Take) {
	id := t.ID
	a.checkOpen[id] = true
	if a.checking[id] {
		return
	}
	a.checking[id] = true
	svc := a.lib.svc.Render
	a.background(func() {
		_, err := svc.Check(context.Background(), id)
		a.post(func() {
			delete(a.checking, id)
			if err != nil {
				a.toastWarn(i18n.T("Could not check the audio: %v", err))
			}
		})
	})
}

// dragHandle lets the take's file be dragged to Finder or another app.
func (a *app) dragHandle(c *ui.Context, t *core.Take) {
	th := c.Theme()
	path := a.takeStore.AudioPath(*t)
	h := ui.ButtonBase(c.Key("drag-out")).Size(px(th, 24), px(th, 28)).Center().Radius(px(th, 6)).Label(i18n.T("Drag the file")).
		Tooltip(i18n.T("Drag to Finder or another app")).Cursor(ui.CursorGrab)
	h.DragDataFrom(func() transfer.Data {
		d, _ := transfer.FileData(path)
		return d
	})
	if h.Hovered() {
		h.Background(tok(th).Hover)
	}
	h.Children(func() { ui.Text(c, "⠿").TextColor(tok(th).Text3) })
}

// noticeList shows notices; those with a place in the audio seek there.
func (a *app) noticeList(c *ui.Context, t *core.Take, key string, notices []core.TakeNotice) {
	th := c.Theme()
	ui.Column(c.Key(key)).Gap(px(th, 2)).Children(func() {
		for i, n := range notices {
			color := th.Warning
			if n.Severity == "error" {
				color = th.Danger
			} else if n.Severity == "info" {
				color = th.TextMuted
			}
			row := ui.ButtonBase(c.Key(fmt.Sprintf("%s-%d", key, i))).Padding(px(th, 3), px(th, 6)).Radius(px(th, 6)).Label(noticeText(n))
			seek := n.Start > 0 || n.End > 0
			if seek {
				if row.Hovered() {
					row.Background(tok(th).Hover)
				}
				if row.Clicked() {
					a.seekTake(t, n.Start)
				}
				row.Tooltip(i18n.T("Play from here"))
			}
			row.Children(func() {
				ui.Box(c).Size(px(th, 6), px(th, 6)).Radius(px(th, 3)).Background(color).Shrink(0)
				if seek {
					ui.Text(c, noticeTime(n.Start)).Font(monoFont).FontSize(px(th, fsMicro)).TextColor(tok(th).Text3).Width(px(th, 52)).Shrink(0)
				}
				ui.Text(c, noticeText(n)).FontSize(px(th, fsCaption)).Grow(1).MinWidth(0)
			})
		}
	})
}

// checkResults shows what Check audio found.
func (a *app) checkResults(c *ui.Context, t *core.Take) {
	th := c.Theme()
	switch {
	case a.checking[t.ID]:
		caption(c, i18n.T("Checking…"))
	case t.Render == nil || !t.Render.QCDone:
	case len(t.Render.Issues) == 0:
		ui.Row(c).Gap(px(th, 6)).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.CheckCircle.Linear).Size(px(th, 14), px(th, 14)).TextColor(th.Success).Shrink(0)
			ui.Text(c, i18n.T("No problems found: no clipping, silences or sudden jumps in loudness.")).FontSize(px(th, fsCaption)).TextColor(th.Success)
		})
	default:
		ui.Text(c, i18n.N(len(t.Render.Issues), "Check audio found %d thing to look at:", "Check audio found %d things to look at:", len(t.Render.Issues))).
			FontSize(px(th, fsCaption)).FontWeight(600)
		a.noticeList(c, t, "qc", t.Render.Issues)
	}
}

// outputPanel is a take's panel: the Studio's latest take, or the
// History's selected one (detailed: the recipe follows). A flat card:
// play, the waveform, what the take is, and its actions as icons.
func (a *app) outputPanel(c *ui.Context, t *core.Take, latest bool) {
	a.takePanel(c, t, latest, false)
}

// takePanel is outputPanel; studio adds Text to speech's hand-offs (0.5
// F3): 「换个音色」 beside the actions, and the rest in ⋯ (takeHandoffs).
func (a *app) takePanel(c *ui.Context, t *core.Take, latest, studio bool) {
	th := c.Theme()
	k := tok(th)
	card(c.Key("out-"+t.ID)).Padding(px(th, sp3), px(th, sp4)).Gap(px(th, sp2)).Children(func() {
		ui.Row(c).Gap(px(th, sp3)).AlignItems(ui.Center).Children(func() {
			playing := a.pl.Playing(t.ID)
			label, ic := i18n.T("Play"), icons.Play
			if playing {
				label, ic = i18n.T("Pause"), icons.Pause
			}
			play := ui.ButtonBase(c.Key("play")).Size(px(th, 36), px(th, 36)).Radius(px(th, 18)).Center().Label(label).Tooltip(label)
			switch {
			case play.Pressed():
				play.Background(k.InkPressed)
			case play.Hovered():
				play.Background(k.InkHover)
			default:
				play.Background(k.Ink)
			}
			play.Children(func() {
				ui.Icon(c, ic.Bold).Size(px(th, 16), px(th, 16)).TextColor(k.OnInk)
			})
			if play.Clicked() {
				a.play(t)
			}
			a.waveform(c, t)
			a.dragHandle(c, t)
		})
		ui.Row(c).Gap(px(th, sp2)).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(px(th, 2)).Children(func() {
				title := t.Text
				if latest {
					title = i18n.T("Latest take")
				}
				ui.Text(c, title).FontWeight(600).SingleLine().Ellipsis("…").MinWidth(0)
				meta := renderedIn(t) + " · " + fileDetails(t)
				switch {
				case studio:
					meta = a.takeVoiceName(t) + " · " + i18n.T("%.1f s", t.AudioSeconds) + " · " + ago(t.CreatedAt)
				case latest:
					meta = clipMeta(t) + " · " + fileDetails(t)
				}
				m := truncText(c, meta).FontSize(px(th, fsCaption)).TextColor(k.Text2)
				if studio {
					m.Tooltip(clipMeta(t) + "\n" + fileDetails(t))
				}
			})
			if studio {
				if labelOrIcon(c.Key("revoice"), icons.UserSpeakRounded, i18n.T("Another voice"), a.studioNarrow(c)).
					Tooltip(i18n.T("Regenerate with another voice")).Clicked() {
					a.revoice(t)
				}
			}
			a.starButton(c, t)
			checked := t.Render != nil && t.Render.QCDone
			check := iconToggle(c.Key("check"), icons.Pulse2, a.checkOpen[t.ID] && checked, i18n.T("Check audio")).
				Tooltip(i18n.T("Look for clipping, silences and jumps in loudness"))
			if check.Clicked() {
				if checked {
					a.checkOpen[t.ID] = !a.checkOpen[t.ID]
				} else {
					a.checkTake(t)
				}
			}
			if iconButton(c.Key("export"), icons.Export, i18n.T("Export audio…")).Tooltip(i18n.T("Export the audio: format, quality, loudness")).Clicked() {
				a.openExport(t) // export_dialog.go
			}
			path := a.takeStore.AudioPath(*t)
			moreMenu(c.Key("take-more"), "", func(m *ui.Menu) {
				if studio {
					a.takeHandoffs(m, t, false)
					return
				}
				if m.Item(i18n.T("Show in Finder")).Chosen() {
					mygo.Shell.ShowItemInFolder(path)
				}
			})
		})
		if t.Render != nil && len(t.Render.Warnings) > 0 {
			a.noticeList(c, t, "warn", t.Render.Warnings)
		}
		if a.checkOpen[t.ID] {
			a.checkResults(c, t)
		}
	})
}

// starButton stars or unstars a take; starred takes are never pruned.
func (a *app) starButton(c *ui.Context, t *core.Take) {
	tip := i18n.T("Star: keep this take")
	if t.Starred {
		tip = i18n.T("Unstar")
	}
	if iconToggle(c.Key("star"), icons.Star, t.Starred, tip).Clicked() {
		a.setStarred(t, !t.Starred)
	}
}

// setStarred stars a take in the background (TakeUpdated follows).
func (a *app) setStarred(t *core.Take, on bool) {
	id := t.ID
	t.Starred = on // at once in the snapshot; the event confirms it
	svc := a.lib.svc.Takes
	a.background(func() {
		if err := svc.SetStarred(context.Background(), id, on); err != nil {
			a.post(func() { a.toastWarn(i18n.T("Could not star the take: %v", err)) })
		}
	})
}

// modelName names a take's model.
func modelName(repo string) string { return catalog.Builtin().DisplayName(repo) }
