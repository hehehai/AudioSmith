package main

// The Transcribe page's history list, the status of a run, and the start
// state: sources (file, drop, record) and the recorder; split from
// transcribe_view.go.

import (
	"slices"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dictation"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// transcriptHistory is the list of transcripts (the left panel); inline
// says it is beside the page (else a drawer, which closes on a choice).
func (a *app) transcriptHistory(c *ui.Context, inline bool) {
	t := c.Theme()
	k := tok(t)
	list := a.shownTranscripts()
	ui.Column(c).Padding(px(t, sp3), px(t, sp3), px(t, sp2)).Gap(px(t, sp2)).Shrink(0).Children(func() {
		ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, i18n.T("History")).FontWeight(600).Grow(1).MinWidth(0).SingleLine()
			ui.Text(c, i18n.N(len(a.trList), "%d transcript", "%d transcripts", len(a.trList))).FontSize(px(t, fsMicro)).TextColor(k.Text3).SingleLine()
			if len(a.trList) > 0 && iconButton(c.Key("tr-export-all"), icons.Export, i18n.T("Export all")).Tooltip(i18n.T("Every transcript in one text file")).Clicked() {
				a.exportAllTranscripts()
			}
		})
		if len(a.trList) > 0 {
			ui.Row(c).Children(func() {
				if searchField(c, "tr-search", &a.trQuery, i18n.T("Search transcripts"), 0).Changed() {
					a.searchTranscripts()
				}
			})
		}
	})
	if len(list) == 0 {
		ui.Column(c).Grow(1).Center().Padding(px(t, sp4)).Gap(px(t, sp1)).Children(func() {
			if len(a.trList) == 0 {
				ui.Text(c, i18n.T("No transcripts yet")).FontWeight(600).TextAlign(ui.Center)
				caption(c, i18n.T("Transcribe a file or a recording, or dictate anywhere with %s.", dictation.GlyphsOf(a.prefs.Dictation.Shortcut))).TextAlign(ui.Center)
			} else {
				ui.Text(c, i18n.T("No matching transcripts")).FontWeight(600).TextAlign(ui.Center)
				caption(c, i18n.T("Try a different search.")).TextAlign(ui.Center)
			}
		})
		return
	}
	ui.List(c.Key("tr-list"), &a.trListUI, len(list)).Grow(1).MinHeight(0).Gap(px(t, 2)).ItemKey(func(i int) any { return list[i].ID }).Rows(func(r ui.ListRow) {
		tr := list[r.Index]
		row := ui.ButtonBase(c.Key("tr-item-"+tr.ID)).Padding(px(t, sp2), px(t, sp2)).Margin(0, px(t, sp2)).Radius(px(t, radiusControl)).
			Label(transcriptTitle(tr)).Gap(px(t, sp2)).AlignItems(ui.Start)
		switch {
		case a.trSel == tr.ID:
			row.Background(k.Pressed)
		case row.Hovered():
			row.Background(k.Hover)
		}
		if row.Clicked() {
			a.selectTranscript(tr.ID)
			if !inline {
				a.trSide.open, a.trSide.set = false, true
			}
		}
		row.Children(func() {
			ic := icons.FileText
			if tr.Source == core.SourceDictation {
				ic = micIcon
			}
			ui.Icon(c, ic.Linear).Size(px(t, 14), px(t, 14)).TextColor(k.Text2).Margin(px(t, 2), 0, 0, 0).Shrink(0)
			ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 2)).Children(func() {
				ui.Text(c, transcriptTitle(tr)).FontSize(px(t, fsCaption)).FontWeight(600).SingleLine().Ellipsis("…").MinWidth(0)
				ui.Text(c, orDash(tr.Preferred())).FontSize(px(t, fsCaption)).TextColor(k.Text2).MaxLines(2).Ellipsis("…")
				meta := ago(tr.CreatedAt)
				if tr.Language != "" {
					meta += " · " + langName(tr.Language)
				}
				meta += " · " + clock(tr.Duration)
				ui.Text(c, meta).FontSize(px(t, fsMicro)).TextColor(k.Text3).SingleLine().Ellipsis("…")
			})
		})
	})
}

// transcriptStatus shows the transcription running, or why the last one
// failed.
func (a *app) transcriptStatus(c *ui.Context) {
	t := c.Theme()
	if j := a.trJob; j != nil {
		c.After(500 * time.Millisecond)
		fillCard(c.Key("tr-job")).Gap(px(t, sp2)).Padding(px(t, sp3), px(t, sp4)).Children(func() {
			ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c)
				ui.Text(c, i18n.T("Transcribing %s", j.title)).FontWeight(600).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
				caption(c, formatDuration(time.Since(j.started).Seconds())).SingleLine()
				if secondaryButton(c.Key("tr-cancel"), i18n.T("Cancel")).Disabled(j.cancelling).Clicked() {
					a.cancelTranscription()
				}
			})
			ui.Progress(c, j.fraction).Label(j.stage)
			caption(c, j.stage)
		})
	}
	if a.trErr != "" {
		card(c.Key("tr-error")).Gap(px(t, sp2)).Padding(px(t, sp3), px(t, sp4)).Children(func() {
			ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
				iconMark(c, icons.DangerCircle, t.Danger, "")
				ui.Text(c, i18n.T("Transcription failed")).FontWeight(600).TextColor(t.Danger).Grow(1).MinWidth(0)
				if iconButton(c.Key("tr-dismiss"), icons.Close, i18n.T("Dismiss")).Clicked() {
					a.trErr, a.trRetry, a.trNeed = "", nil, nil
				}
			})
			caption(c, a.trErr).TextColor(tok(t).Text).Selectable()
			ui.Row(c).Gap(px(t, sp2)).Wrap().Children(func() {
				if e := a.trNeed; e != nil {
					m := a.model(*e)
					label := i18n.T("Download %s", modelRef(*e))
					if m.downloading {
						label = i18n.T("Downloading…")
					}
					if secondaryIconButton(c.Key("tr-download"), icons.DownloadMinimalistic, label).Label(label).Disabled(m.downloading).Clicked() {
						a.download(*e)
					}
				}
				if r := a.trRetry; r != nil {
					if ghostIconButton(c.Key("tr-retry"), icons.Restart, i18n.T("Retry")).Label(i18n.T("Retry")).Clicked() {
						a.queueTranscription(*r, true)
					}
				}
			})
		})
	}
}

// transcribeSources: a file, or the microphone.
func (a *app) transcribeSources(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	e := a.trModel(a.trMode == 0)
	if ready, known := a.modelOnDisk(e); known && !ready {
		notice(c, "tr-need-model", noticeWarn, i18n.T("Transcribing needs %s, which is not downloaded yet.", modelRef(e)), "", func() {
			m := a.model(e)
			label := i18n.T("Download")
			if m.downloading {
				label = i18n.T("Downloading…")
			}
			if secondaryIconButton(c.Key("tr-need-download"), icons.DownloadMinimalistic, label).Label(label).Disabled(m.downloading).Clicked() {
				a.download(e)
			}
		}, nil)
	}
	ui.Column(c).Gap(px(t, 2)).Children(func() {
		ui.Text(c, i18n.T("Turn speech into text: a meeting, an interview, a voice memo or a video. Everything stays on this Mac.")).TextColor(k.Text2)
		ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
			iconMark(c, icons.Box, k.Text3, "")
			ui.Text(c, a.trModelLine()).FontSize(px(t, fsCaption)).TextColor(k.Text3).SingleLine().Ellipsis("…").MinWidth(0)
		})
	})
	busy := a.trJob != nil
	ui.Row(c).Gap(px(t, sp3)).AlignItems(ui.Stretch).Wrap().Children(func() {
		zone := ui.ButtonBase(c.Key("tr-upload")).Grow(1).Basis(px(t, 240)).MinHeight(px(t, 180)).Padding(px(t, sp4)).
			Radius(px(t, radiusCard)).Border(1, k.G[5]).BorderStyle(ui.BorderDashed).Label(i18n.T("Choose a file")).Disabled(busy)
		if files := zone.DroppedFiles(); files != nil {
			a.transcribeDropped(files)
		}
		switch {
		case zone.FileDragOver():
			zone.Border(2, k.Accent).Background(k.AccentSoft)
		case zone.Hovered():
			zone.Background(k.Subtle)
		}
		if zone.Clicked() {
			a.chooseTranscribeFile()
		}
		zone.Children(func() {
			ui.Column(c).Grow(1).Center().Gap(px(t, sp2)).Children(func() {
				ui.Icon(c, icons.Import.Linear).Size(px(t, 28), px(t, 28)).TextColor(k.Text3)
				ui.Text(c, i18n.T("Drop audio or video here, or click to choose")).FontWeight(600).TextAlign(ui.Center)
				caption(c, i18n.T("WAV, MP3, M4A, FLAC, OGG, AAC — or a video")).TextAlign(ui.Center)
			})
		})
		card(c.Key("tr-record")).Grow(1).Basis(px(t, 240)).MinHeight(px(t, 180)).Gap(px(t, sp2)).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			a.trRecorder(c, busy)
		})
	})
	caption(c, i18n.T("Dictate into any app with %s: the text is typed where your cursor is. Set it up in Settings → Dictation.",
		dictation.GlyphsOf(a.prefs.Dictation.Shortcut))).TextColor(k.Text3)
}

// trRecorder records for a transcript: Record / Stop, the time and level,
// and the microphone (mono, 16 kHz: what speech models take).
func (a *app) trRecorder(c *ui.Context, busy bool) {
	t := c.Theme()
	k := tok(t)
	m := &a.trMic
	a.trRecEnded()
	switch m.phase {
	case "recording":
		c.After(100 * time.Millisecond)
		ui.Text(c, clock(time.Since(m.started).Seconds())).FontSize(px(t, fsTitle)).FontWeight(600).Font(monoFont)
		ui.Meter(c, float64(levelFraction(m.level)), 0, 1, nil).Width(px(t, 180)).Label(i18n.T("Input level"))
		if inputDetected(m.level) {
			ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
				statusDot(c, k.Success)
				caption(c, i18n.T("Input detected"))
			})
		} else {
			caption(c, i18n.T("No input detected")).TextColor(k.Text3)
		}
		if recordButton(c.Key("tr-rec-stop"), i18n.T("Stop recording")).Clicked() {
			a.trRecStop()
		}
		return
	case "starting", "stopping":
		ui.Spinner(c)
		label := i18n.T("Starting…")
		if m.phase == "stopping" {
			label = i18n.T("Finishing…")
		}
		caption(c, label)
		return
	}
	ui.Icon(c, micIcon.Linear).Size(px(t, 28), px(t, 28)).TextColor(k.Text3)
	caption(c, i18n.T("Record a meeting, a lecture or a voice memo.")).TextAlign(ui.Center)
	labels, ids := []string{i18n.T("System default")}, []string{""}
	for _, d := range a.clone.devices {
		labels, ids = append(labels, d.Name), append(ids, d.ID)
	}
	if !slices.Contains(labels, a.trDevice) {
		a.trDevice = labels[max(slices.Index(ids, a.prefs.Transcribe.Device), 0)]
	}
	ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Wrap().Justify(ui.Center).Children(func() {
		if ui.Select(c.Key("tr-rec-device"), &a.trDevice, labels).Label(i18n.T("Input device")).Tooltip(i18n.T("Input device")).Width(px(t, 180)).Shrink(1).MinWidth(px(t, 100)).Changed() {
			id := ids[max(slices.Index(labels, a.trDevice), 0)]
			a.updateSettings(func(s *settings.Settings) { s.Transcribe.Device = id })
		}
		if recordButton(c.Key("tr-rec-start"), i18n.T("Record")).Disabled(busy).Clicked() {
			a.trRecStart()
		}
	})
	if m.err != "" {
		caption(c, m.err).TextColor(t.Danger).TextAlign(ui.Center)
	}
}
