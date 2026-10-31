package main

import (
	"math"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

func (a *app) studioPage(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	inline, shown := a.studioInspectorShown(c)
	pageHeader(c, i18n.T("Studio"), func() {
		tip := i18n.T("Show the inspector")
		if shown {
			tip = i18n.T("Hide the inspector")
		}
		if panelToggle(c.Key("inspector-toggle"), icons.PanelRight, shown, tip).Clicked() {
			a.composer.panel, a.composer.panelSet = !shown, true
		}
	})
	if c.Shortcut(ui.Alt, ui.KeySlash) { // wherever the focus is, the script editor's too
		a.composer.insertOpen = !a.composer.insertOpen
	}
	if msg := a.composer.undoToast; msg != "" {
		a.composer.undoToast = ""
		c.ToastAction(msg, i18n.T("Undo"), a.undoScript)
	}
	_, winH := c.Size()
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Scroll(c.Key("composer-scroll")).Grow(1).MinWidth(0).Children(func() {
			// As tall as the page at least, so the script fills it; the
			// page scrolls when the window is too short for all of it.
			ui.Column(c).MinHeight(winH-titleBarHeight-2).Padding(px(t, sp1), px(t, sp6), px(t, sp5)).Gap(px(t, sp3)).Children(func() {
				a.composerToolbar(c)
				a.scriptView(c)
				a.statsLine(c)
				a.generateBar(c)
				if a.job != nil {
					a.jobStatus(c)
				} else if a.jobError != "" {
					a.jobProblem(c)
				}
				if cl := a.lastClip(); cl != nil {
					a.outputPanel(c, cl, true)
				}
			})
		}).ClipX()
		if shown && inline {
			ui.Box(c).Width(1).Background(k.Line)
			ui.Scroll(c.Key("inspector")).Width(px(t, inspectorWidth)).Shrink(0).Children(func() {
				ui.Column(c).Padding(px(t, sp2), px(t, sp5), px(t, sp5)).Gap(px(t, sp4)).Children(func() {
					a.inspector(c)
				})
			})
		}
	})
	if shown && !inline {
		// Too narrow for both: the inspector slides over the composer.
		ui.Overlay(c, func() {
			_, h := c.Size()
			panel := ui.Scroll(c.Key("inspector-drawer")).Absolute().Right(0).Top(titleBarHeight).Width(px(t, inspectorWidth)).
				Height(h-titleBarHeight-1).Background(k.Bg).BorderWidth(0, 0, 0, 1).BorderColor(k.Line).
				Shadow(-8, 0, 24, 0, ui.RGBA(0, 0, 0, 0.12)).Label(i18n.T("Inspector"))
			panel.Children(func() {
				ui.Column(c).Padding(px(t, sp4), px(t, sp5), px(t, sp5)).Gap(px(t, sp4)).Children(func() {
					a.inspector(c)
				})
			})
			if panel.PressedOutside() || panel.OverlayShortcut(0, ui.KeyEscape) {
				a.composer.panel, a.composer.panelSet = false, true
			}
		})
	}
	// After the inputs above applied this frame's edits.
	a.autosaveStudio()
}

// studioInspectorShown says whether the Studio's inspector shows, and
// whether beside the composer (inline) or over it: beside when the page
// leaves the composer room, which the toggle can change.
func (a *app) studioInspectorShown(c *ui.Context) (inline, shown bool) {
	t := c.Theme()
	w, _ := c.Size()
	side := a.sideW
	if a.railMode() {
		side = px(t, railWidth)
	}
	inline = w-side-px(t, inspectorWidth) >= px(t, 480)
	shown = inline
	if a.composer.panelSet {
		shown = a.composer.panel
	}
	return inline, shown
}

// studioNarrow is true when the composer is narrow: its bars show icons
// without their labels.
func (a *app) studioNarrow(c *ui.Context) bool {
	t := c.Theme()
	w, _ := c.Size()
	side := a.sideW
	if a.railMode() {
		side = px(t, railWidth)
	}
	inline, shown := a.studioInspectorShown(c)
	if shown && inline {
		w -= px(t, inspectorWidth)
	}
	return w-side < px(t, 500)
}

// composerToolbar: insert menu, import, and the script's language.
func (a *app) composerToolbar(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
		a.insertMenu(c)
		imp := i18n.T("Import script…")
		var b ui.Element
		if a.studioNarrow(c) {
			b = iconButton(c.Key("import"), icons.Import, imp)
		} else {
			b = ghostIconButton(c.Key("import"), icons.Import, imp).Label(imp)
		}
		if b.Tooltip(i18n.T("TXT, Markdown, DOCX, EPUB, SRT or VTT, inserted at the caret")).Clicked() {
			a.importScript()
		}
		ui.Spacer(c)
		a.languagePicker(c)
	})
}

// formatDuration is "45 s", "3 min 20 s", "1 h 05 min".
func formatDuration(seconds float64) string {
	s := int(math.Round(seconds))
	switch {
	case s < 60:
		return i18n.T("%d s", s)
	case s < 3600:
		if s%60 == 0 {
			return i18n.T("%d min", s/60)
		}
		return i18n.T("%d min %d s", s/60, s%60)
	}
	return i18n.T("%d h %02d min", s/3600, (s%3600)/60)
}

// typicalRTF is about how many seconds of audio a model makes per second
// on an M-series Mac, for the slow-generation warning.
func typicalRTF(family string) float64 {
	switch family {
	case "kokoro":
		return 20
	case "omnivoice":
		return 3
	}
	return 2
}

// statsLine: characters, estimated length, chunks, and the readiness or
// slow-generation line.
func (a *app) statsLine(c *ui.Context) {
	t := c.Theme()
	st := a.scriptStats()
	speed := 1.0
	if a.preset().Has(catalog.CapSpeed) && a.speed > 0 {
		speed = a.speed
	}
	seconds := st.seconds / speed
	if a.preset().Has(catalog.CapDuration) && a.duration > 0 {
		seconds = a.duration
	}
	ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
		parts := i18n.T("%d characters", st.chars)
		if st.chunks > 0 {
			parts += " · " + i18n.T("≈ %s of audio", formatDuration(seconds))
		}
		if st.chunks > 1 {
			parts += " · " + i18n.N(st.chunks, "%d chunk", "%d chunks", st.chunks)
		}
		ui.Text(c, parts).FontSize(px(t, fsCaption)).TextColor(tok(t).Text2).SingleLine().Ellipsis("…").MinWidth(0)
		if n := len(st.diags); n > 0 {
			a.markupNotes(c, st)
		}
	})
	if msg, warn := a.readiness(st); msg != "" {
		color := tok(t).Text2
		if warn {
			color = t.Warning
		}
		ui.Row(c.Key("readiness")).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, msg).FontSize(px(t, fsCaption)).TextColor(color).Grow(1).MinWidth(0)
			e := a.preset()
			if m := a.model(e); e.ID != "custom" && m.known && m.local.State != hub.Downloaded && !m.downloading && a.needDownload == nil {
				if secondaryIconButton(c.Key("ready-download"), icons.DownloadMinimalistic, i18n.T("Download")).Label(i18n.T("Download")).Tooltip(e.Repo).Clicked() {
					a.download(e)
				}
			}
		})
		return
	}
	if rtf := typicalRTF(a.preset().Family); seconds/rtf > 90 {
		ui.Text(c.Key("slow"), i18n.T("A long render: about %s of audio may take %s or more. It plays from the first chunk while the rest renders.",
			formatDuration(seconds), formatDuration(seconds/rtf))).FontSize(px(t, fsCaption)).TextColor(tok(t).Text2)
	}
}

// readiness says what keeps Generate from working, or "" when nothing
// does; warn is true for a problem (not just the engine starting).
func (a *app) readiness(st scriptStats) (msg string, warn bool) {
	p := a.preset()
	switch {
	case a.engState == engineStarting:
		return i18n.T("The engine is starting…"), false
	case a.engState == engineFailed:
		return i18n.T("The engine is not running. Restart it on the Engine page."), true
	case p.Repo == "":
		return i18n.T("Type the Hugging Face repo of the model to use."), true
	case st.chars == 0:
		return i18n.T("Type, paste or import a script."), false
	case st.chunks == 0:
		return i18n.T("The script has nothing to say: only markup."), true
	}
	if m := a.model(p); p.ID != "custom" && m.known && m.local.State != hub.Downloaded && a.needDownload == nil {
		return i18n.T("%s is not downloaded yet.", modelRef(p)), true
	}
	if a.studioMissingReference() {
		return studioNeedsReferenceText(p), true
	}
	if a.studioDefaultVoice() {
		return i18n.T("No reference clip: the model speaks in its default voice."), false
	}
	return "", false
}

// studioNeedsReferenceText says what the Studio needs before model e
// (which only clones voices) can generate.
func studioNeedsReferenceText(e catalog.Entry) string {
	return i18n.T("%s only clones voices: under Voice in the inspector, pick a saved voice or a clip to clone.", modelRef(e))
}

func (a *app) generateBar(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
		a.studioSummary(c)
		ui.Spacer(c)
		if a.job != nil {
			if secondaryButton(c.Key("cancel"), i18n.T("Cancel")).Clicked() {
				a.cancel()
			}
		}
		label := i18n.T("Generate")
		if a.job != nil && a.job.kind == "synthesize" {
			label = i18n.T("Generating…")
		}
		tip := i18n.T("Generate speech (⌘↩)")
		if a.studioMissingReference() {
			tip = studioNeedsReferenceText(a.preset())
		}
		if primaryButton(c.Key("generate"), label).
			Disabled(!a.canGenerate()).
			Tooltip(tip).
			Clicked() {
			a.generate()
		}
	})
}

// jobProblem shows why the last job failed or did not start, with a
// Download button when the model is missing.
func (a *app) jobProblem(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, icons.DangerCircle.Linear).Size(px(t, 14), px(t, 14)).TextColor(t.Danger).Shrink(0)
		ui.Text(c, a.jobError).TextColor(t.Danger).FontSize(px(t, fsCaption)).Selectable().Grow(1).MinWidth(0)
		e := a.needDownload
		if e == nil {
			return
		}
		m := a.model(*e)
		switch {
		case m.downloading:
			text := i18n.T("Downloading %s", modelRef(*e))
			if m.progress.TotalBytes > 0 {
				text = i18n.T("Downloading %s · %d%%", modelRef(*e), int(100*m.progress.Bytes/m.progress.TotalBytes))
			}
			caption(c, text)
			if secondaryButton(c.Key("dl-cancel"), i18n.T("Cancel")).Clicked() {
				a.cancelDownload(*e)
			}
		default:
			if secondaryIconButton(c.Key("dl-model"), icons.DownloadMinimalistic, i18n.T("Download")).Label(i18n.T("Download")).Tooltip(e.Repo).Clicked() {
				a.download(*e)
			}
		}
	})
}

func (a *app) jobStatus(c *ui.Context) {
	t := c.Theme()
	j := a.job
	ui.Column(c).Gap(px(t, 6)).Children(func() {
		ui.Progress(c, j.fraction).Label(j.status)
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			status := j.status
			if j.kind == "synthesize" && a.renderTake == j.takeID && a.renderChunks > 1 && a.renderChunk > 0 && !j.cancelling {
				status += " · " + i18n.T("chunk %d of %d", a.renderChunk, a.renderChunks)
				if a.renderCached > 0 {
					status += " · " + i18n.N(a.renderCached, "%d from the cache", "%d from the cache", a.renderCached)
				}
			}
			ui.Text(c, status).FontSize(px(t, fsCaption)).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
			info := i18n.T("%.0f s", time.Since(j.started).Seconds())
			if j.audioSeconds > 0 {
				info = i18n.T("%.1f s of audio · ", j.audioSeconds) + info
			}
			caption(c, info).SingleLine()
			if a.previewAvailable() {
				label, ic := i18n.T("Play preview"), icons.Play
				if a.pl.Playing(a.previewID) {
					label, ic = i18n.T("Pause preview"), icons.Pause
				}
				if iconButton(c.Key("preview"), ic, label).Tooltip(i18n.T("Listen to what is rendered so far")).Clicked() {
					switch {
					case a.pl.Playing(a.previewID):
						a.pl.Stop()
					default:
						a.playPreview()
					}
				}
			}
		})
	})
}

// markupNotes is what the markup checks found, as a short warning whose
// tooltip lists the notes; a click shows them as a toast.
func (a *app) markupNotes(c *ui.Context, st scriptStats) {
	t := c.Theme()
	var msgs []string
	for _, d := range st.diags {
		msgs = append(msgs, diagText(d))
	}
	n := len(msgs)
	label := i18n.N(n, "%d note about the markup", "%d notes about the markup", n)
	b := ghostBase(c.Key("markup-notes")).Padding(px(t, 2), px(t, 6)).Gap(px(t, sp1)).Label(label).Tooltip(strings.Join(msgs, "\n")).MinWidth(0).Shrink(1)
	b.Children(func() {
		ui.Icon(c, icons.DangerTriangle.Linear).Size(px(t, 13), px(t, 13)).TextColor(t.Warning)
		ui.Text(c, label).FontSize(px(t, fsCaption)).TextColor(t.Warning).SingleLine().Ellipsis("…").MinWidth(0)
	})
	if b.Clicked() {
		a.toast(label + ": " + strings.Join(msgs, " · "))
	}
}
