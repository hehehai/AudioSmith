package main

// The Transcribe page's open transcript: header and exports, the player
// and the clean-up; split from transcribe_view.go.

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// transcriptDetail is the transcript shown: its title and actions (Copy,
// Export ▾, Reuse as script, ⋯ Delete), the player, the clean-up and the
// segments.
func (a *app) transcriptDetail(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	tr := a.trDetail
	if tr == nil {
		ui.Row(c).Gap(px(t, sp2)).Children(func() {
			ui.Spinner(c)
			caption(c, i18n.T("Reading the transcript…"))
		})
		return
	}
	ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Gap(px(t, 2)).Grow(1).MinWidth(0).Children(func() {
			sectionHeading(c, transcriptTitle(*tr)).Tooltip(transcriptTitle(*tr))
			meta := []string{i18n.DateTime(tr.CreatedAt), langName(tr.Language), clock(tr.Duration), catalog.Builtin().DisplayName(tr.Model)}
			if tr.Mode == settings.TranscribeFast {
				meta = append(meta, i18n.T("Fast"))
			}
			if tr.App != "" {
				meta = append(meta, tr.App)
			}
			truncText(c, strings.Join(meta, " · ")).FontSize(px(t, fsCaption)).TextColor(k.Text2)
		})
		if iconButton(c.Key("tr-copy"), icons.Copy, i18n.T("Copy")).Clicked() {
			mygoClipboard(c, services.PlainText(*tr))
			a.toast(i18n.T("Copied to the clipboard"))
		}
		var exp ui.Element
		if a.trNarrow {
			exp = iconButton(c.Key("tr-export"), icons.Export, i18n.T("Export"))
		} else {
			exp = dropButton(c.Key("tr-export"), icons.Export, i18n.T("Export")).Shrink(0)
		}
		if exp.Clicked() {
			a.trExport = !a.trExport
		}
		ui.Popover(c, exp, &a.trExport, func() {
			ui.Column(c.Key("tr-export-menu")).Padding(px(t, sp1)).Gap(px(t, 2)).Width(px(t, 240)).Children(func() {
				for _, f := range services.ExportFormats {
					b := ui.ButtonBase(c.Key("tr-export-"+f)).Padding(px(t, 6), px(t, sp2)).Radius(px(t, radiusControl)).Label(exportLabel(f))
					if b.Hovered() {
						b.Background(k.Hover)
					}
					if b.Children(func() { ui.Text(c, exportLabel(f)).SingleLine() }).Clicked() {
						a.exportTranscript(f)
					}
				}
			})
		})
		reuse := i18n.T("Reuse as script")
		var rb ui.Element
		if a.trNarrow {
			rb = iconButton(c.Key("tr-reuse"), icons.Soundwave, reuse)
		} else {
			rb = ghostIconButton(c.Key("tr-reuse"), icons.Soundwave, reuse).Label(reuse).Shrink(0)
		}
		if rb.Tooltip(i18n.T("Open the text in the Studio")).Clicked() {
			a.reuseTranscript()
		}
		moreMenu(c.Key("tr-more"), i18n.T("Transcript actions"), func(m *ui.Menu) {
			if m.Item(i18n.T("Delete…")).Chosen() {
				a.trConfirm = tr.ID
			}
		})
	})
	if a.trConfirm == tr.ID {
		notice(c, "tr-delete-ask", noticeWarn, i18n.T("Delete this transcript?"), "", func() {
			if ghostButton(c.Key("tr-delete-no"), i18n.T("Keep")).Clicked() {
				a.trConfirm = ""
			}
			if recordButton(c.Key("tr-delete-yes"), i18n.T("Delete")).Clicked() {
				a.deleteTranscript(tr.ID)
			}
		}, nil)
	}
	if a.trWarn != "" {
		notice(c, "tr-warn", noticeWarn, a.trWarn, "", nil, nil)
	}
	a.transcriptPlayer(c, tr)
	a.transcriptCleanup(c, tr)
	a.transcriptSegments(c, tr)
}

func exportLabel(f string) string {
	switch f {
	case services.ExportTXT:
		return i18n.T("Text (.txt)")
	case services.ExportSRT:
		return i18n.T("Subtitles (.srt)")
	case services.ExportVTT:
		return i18n.T("Web subtitles (.vtt)")
	case services.ExportJSON:
		return i18n.T("JSON with word timings (.json)")
	}
	return f
}

// transcriptPlayer plays the audio kept with the transcript.
func (a *app) transcriptPlayer(c *ui.Context, tr *core.Transcript) {
	t := c.Theme()
	if tr.Audio == "" || tr.Duration <= 0 {
		return
	}
	id := trPlayID(tr.ID)
	pos := max(a.trPosition(), 0)
	card(c.Key("tr-player")).Row().Gap(px(t, sp3)).AlignItems(ui.Center).Padding(px(t, sp2), px(t, sp3)).Children(func() {
		if playButton(c.Key("tr-play"), a.pl.Playing(id), false, 36).Clicked() {
			a.toggleTranscriptPlayback()
		}
		seek := pos
		if seekSlider(c.Key("tr-seek"), &seek, 0, tr.Duration).Grow(1).MinWidth(0).Label(i18n.T("Position")).Changed() {
			a.playTranscriptAt(seek)
		}
		ui.Text(c, clock(pos)+" / "+clock(tr.Duration)).FontSize(px(t, fsMicro)).Font(monoFont).TextColor(tok(t).Text2).SingleLine().Shrink(0)
	})
}

// transcriptCleanup: the clean-up, its original, and the buttons that make it.
func (a *app) transcriptCleanup(c *ui.Context, tr *core.Transcript) {
	t := c.Theme()
	k := tok(t)
	cleaning := a.trClean != nil && a.trClean.id == tr.ID
	ui.Row(c.Key("tr-clean-bar")).Gap(px(t, sp1)).AlignItems(ui.Center).Wrap().Children(func() {
		if ghostIconButton(c.Key("tr-polish"), icons.PenNewRound, i18n.T("Polish")).Label(i18n.T("Polish")).Disabled(cleaning).
			Tooltip(i18n.T("Remove fillers, fix spacing, capitals and punctuation, without a language model")).Clicked() {
			a.polishTranscript()
		}
		avail, who := a.llmAvailable()
		clean := ghostIconButton(c.Key("tr-cleanup"), icons.MagicStick3, i18n.T("Clean up")).Label(i18n.T("Clean up")).Disabled(cleaning || !avail)
		if avail {
			clean.Tooltip(i18n.T("Remove fillers and apply self-corrections with %s", who))
		} else {
			clean.Tooltip(i18n.T("Set up a language model in Settings → Writing help"))
		}
		if clean.Clicked() {
			a.cleanTranscript()
		}
		if cleaning {
			ui.Spinner(c)
			caption(c, i18n.T("Cleaning up…"))
			if ghostButton(c.Key("tr-clean-stop"), i18n.T("Stop")).Clicked() {
				a.trClean.cancel()
			}
		}
	})
	if tr.Clean == "" {
		return
	}
	by := i18n.T("Polished")
	if tr.CleanBy != "polish" && tr.CleanBy != "" {
		by = i18n.T("Cleaned up by %s", tr.CleanBy)
	}
	fillCard(c.Key("tr-clean")).Gap(px(t, sp2)).Children(func() {
		ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, by).FontSize(px(t, fsCaption)).FontWeight(600).TextColor(k.Text2).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
			if !cleaning && ghostButton(c.Key("tr-clean-drop"), i18n.T("Remove the clean-up")).Clicked() {
				a.dropClean()
			}
		})
		ui.Text(c, tr.Clean).Selectable()
		label := i18n.T("Show the original transcript")
		if a.trOrig {
			label = i18n.T("Hide the original transcript")
		}
		ui.Row(c).Children(func() {
			ic := icons.AltArrowDown
			if a.trOrig {
				ic = icons.AltArrowUp
			}
			if ghostIconButton(c.Key("tr-orig"), ic, label).Label(label).Clicked() {
				a.trOrig = !a.trOrig
			}
		})
		if a.trOrig {
			caption(c, tr.Text()).Selectable()
		}
	})
}
