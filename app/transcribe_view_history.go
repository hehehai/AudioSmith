package main

// The Transcribe page's composer (a file, the recorder; the language,
// speakers and quality; the submit), the run and its error, and the list
// of transcripts (search, rows with ⋯); split from transcribe_view.go.

import (
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dictation"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// trNeedNotice says the mode's model is not downloaded, with Download.
func (a *app) trNeedNotice(c *ui.Context) {
	e := a.trModel(a.trMode == 0)
	if ready, known := a.modelOnDisk(e); !known || ready {
		return
	}
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

// trComposer: the file (drop, Upload) or the recorder, then the
// language, speakers, quality and the submit.
func (a *app) trComposer(c *ui.Context) {
	t := c.Theme()
	busy := a.trJob != nil
	rec := a.trMic.phase != ""
	toolComposer(c, "tr-composer", a.trComposerDrop, func() {
		switch {
		case rec:
			a.trRecorder(c, busy)
		case a.trPend != "":
			fileChip(c, "tr-pend", icons.FileText, a.trPend, func() { a.trPend = "" })
		default:
			composerPrompt(c, transcribeIcon, i18n.T("Drop audio or video here"), i18n.T("WAV, MP3, M4A, FLAC, OGG, AAC — or a video"), a.toolCompact(c))
			ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Wrap().Justify(ui.Center).Children(func() {
				if ghostIconButton(c.Key("tr-upload"), icons.UploadMinimalistic, i18n.T("Upload")).Label(i18n.T("Choose a file")).Clicked() {
					a.chooseTranscribeFile()
				}
				a.trRecordButtons(c, busy)
			})
			if a.toolCompact(c) {
				return
			}
			ui.Row(c.Key("tr-model-line")).Gap(px(t, 6)).AlignItems(ui.Center).MinWidth(0).Children(func() {
				iconMark(c, icons.Box, tok(t).Text3, "")
				ui.Text(c, a.trModelLine()).FontSize(px(t, fsCaption)).TextColor(tok(t).Text3).SingleLine().Ellipsis("…").MinWidth(0)
			})
		}
		if a.trMic.err != "" && !rec {
			caption(c, a.trMic.err).TextColor(t.Danger).TextAlign(ui.Center)
		}
	}, func() {
		a.trLanguageButton(c, a.trModel(a.trMode == 0))
		a.trSpeakersToggle(c, busy)
		ui.Spacer(c)
		a.trModeControl(c, busy)
		ready := a.trPend != "" && !busy
		if submitButton(c.Key("tr-go"), tip(i18n.T("Transcribe"), "↩"), ready).Clicked() {
			path := a.trPend
			a.trPend = ""
			a.transcribeFile(path)
		}
	})
	if a.toolCompact(c) {
		return
	}
	caption(c, i18n.T("Dictate into any app with %s: the text is typed where your cursor is. Set it up in Settings → Dictation.",
		dictation.GlyphsOf(a.prefs.Dictation.Shortcut))).TextColor(tok(t).Text3)
}

// trComposerDrop holds the first audio or video file dropped.
func (a *app) trComposerDrop(paths []string) {
	for _, p := range paths {
		if trTakes(p) {
			a.trPend = p
			return
		}
	}
	a.toast(i18n.T("Drop an audio or video file."))
}

// trRecordButtons: Record, and the microphone's menu (its devices).
func (a *app) trRecordButtons(c *ui.Context, busy bool) {
	labels, ids := []string{i18n.T("System default")}, []string{""}
	for _, d := range a.clone.devices {
		labels, ids = append(labels, d.Name), append(ids, d.ID)
	}
	if ghostIconButton(c.Key("tr-rec-start"), micIcon, i18n.T("Record")).Label(i18n.T("Record")).Disabled(busy).Clicked() {
		a.trRecStart()
	}
	cur := max(slices.Index(ids, a.prefs.Transcribe.Device), 0)
	dropText(c.Key("tr-rec-device"), labels[cur], false).Tooltip(i18n.T("Input device")).Label(i18n.T("Input device")).MaxWidth(px(c.Theme(), 200)).Menu(func(m *ui.Menu) {
		for i, l := range labels {
			if m.Item(l).Checked(i == cur).Chosen() {
				id := ids[i]
				a.updateSettings(func(s *settings.Settings) { s.Transcribe.Device = id })
			}
		}
	})
}

// transcriptStatus shows why the last transcription failed, with its fix.
func (a *app) transcriptStatus(c *ui.Context) {
	t := c.Theme()
	if a.trErr == "" {
		return
	}
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

// trListView: the search and the transcripts (dictations too), the one
// being made on top; ghost rows when there are none.
func (a *app) trListView(c *ui.Context) {
	t := c.Theme()
	list := a.shownTranscripts()
	if len(a.trList) == 0 && a.trJob == nil {
		ghostRows(c.Key("tr-ghost"), 4, transcribeIcon, i18n.T("No transcripts yet"),
			i18n.T("Transcribe a file or a recording, or dictate anywhere with %s.", dictation.GlyphsOf(a.prefs.Dictation.Shortcut)))
		return
	}
	narrow := a.toolNarrow(c)
	ui.Column(c.Key("tr-table")).Shrink(0).Children(func() {
		listHead(c, narrow, listColumn{title: i18n.T("Title")}, listColumn{title: i18n.T("Language"), w: 110, wide: true},
			listColumn{title: i18n.T("Length"), w: 64, wide: true}, listColumn{title: i18n.T("Created"), w: 110})
		if a.trJob != nil {
			a.trJobRow(c, narrow)
		}
		if len(list) == 0 && a.trJob == nil {
			ui.Column(c).Padding(px(t, sp5)).AlignItems(ui.Center).Children(func() {
				caption(c, i18n.T("No matching transcripts"))
			})
		}
		for _, tr := range list {
			a.trListRow(c, tr, narrow)
		}
	})
}

// trJobRow is the transcription running: its title, stage and bar, and
// Cancel.
func (a *app) trJobRow(c *ui.Context, narrow bool) {
	t := c.Theme()
	k := tok(t)
	j := a.trJob
	c.After(500 * time.Millisecond)
	listRow(c, "tr-job", i18n.T("Transcribing %s", j.title), func() {
		listThumb(c, transcribeIcon, nil)
		listTitle(c, j.title, j.stage+" · "+formatDuration(time.Since(j.started).Seconds()))
		if !narrow {
			listCell(c, langName(a.prefs.Transcribe.Language), 110)
			listCell(c, "", 64)
		}
		listStatus(c, i18n.T("Transcribing"), true, max(j.fraction, 0), k.Text2, 110)
	}, func() {
		if iconButton(c.Key("tr-cancel"), icons.CloseCircle, i18n.T("Cancel")).Disabled(j.cancelling).Clicked() {
			a.cancelTranscription()
		}
	})
}

// trListRow is a transcript: title and first words, language, length,
// when, and ⋯ (Open, Export, Dub, Read in Text to speech, Delete).
func (a *app) trListRow(c *ui.Context, tr core.Transcript, narrow bool) {
	id := tr.ID
	ic := icons.FileText
	if tr.Source == core.SourceDictation {
		ic = micIcon
	}
	title := transcriptTitle(tr)
	open := listRow(c, "tr-item-"+id, title, func() {
		listThumb(c, ic, nil)
		listTitle(c, title, strings.TrimSpace(tr.Preferred()))
		if !narrow {
			listCell(c, langName(tr.Language), 110)
			listCell(c, clock(tr.Duration), 64)
		}
		listCell(c, ago(tr.CreatedAt), 110)
	}, func() {
		moreMenu(c.Key("tr-row-more-"+id), i18n.T("Transcript actions"), func(m *ui.Menu) {
			if m.Item(i18n.T("Open")).Chosen() {
				a.selectTranscript(id)
			}
			if m.Item(i18n.T("Export…")).Chosen() {
				a.selectTranscript(id)
				a.trExport = true
			}
			m.Separator()
			if m.Item(i18n.X("action", "Dub")).Disabled(a.trMediaOf(tr) == "").Chosen() {
				a.trToDub(tr)
			}
			if m.Item(i18n.T("Read it in Text to speech")).Chosen() {
				a.trToStudio(id)
			}
			m.Separator()
			if m.Item(i18n.T("Delete…")).Chosen() {
				a.selectTranscript(id)
				a.trConfirm = id
			}
		})
	})
	if open {
		a.selectTranscript(id)
	}
}

// trMediaOf is the media a transcript was made of: the file picked if it
// is still there, else the audio kept with it ("" none).
func (a *app) trMediaOf(tr core.Transcript) string {
	if tr.SourcePath != "" && fileExists(tr.SourcePath) {
		return tr.SourcePath
	}
	if tr.Audio != "" && a.trSvc != nil {
		if p := a.trSvc.AudioPath(tr); fileExists(p) {
			return p
		}
	}
	return ""
}

// trToDub makes a dub of a transcript's media, started at once, and
// shows it in the Dub list.
func (a *app) trToDub(tr core.Transcript) {
	path := a.trMediaOf(tr)
	if path == "" {
		a.toast(i18n.T("The file this transcript was made of is gone."))
		return
	}
	a.navigate("/dub")
	a.dubStartFile(path) // dub_list.go
}

// trToStudio reads transcript id in Text to speech (its text becomes the
// script).
func (a *app) trToStudio(id string) {
	if a.trDetail != nil && a.trDetail.ID == id {
		a.reuseTranscript()
		return
	}
	for _, tr := range a.trList {
		if tr.ID == id {
			full := tr
			a.trDetail = &full
			a.reuseTranscript()
			a.trDetail = nil
			if a.trSel == id {
				a.trSel = ""
			}
			return
		}
	}
}

// trTakes reports whether the page takes a file (by its extension).
func trTakes(path string) bool {
	return slices.Contains(transcribeExts, strings.ToLower(strings.TrimPrefix(filepath.Ext(path), ".")))
}

// trRecorder is the recorder while it records: the time and level, and
// Stop (mono, 16 kHz: what speech models take); a spinner while it starts
// or finishes.
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
	case "starting", "stopping":
		ui.Spinner(c)
		label := i18n.T("Starting…")
		if m.phase == "stopping" {
			label = i18n.T("Finishing…")
		}
		caption(c, label)
	}
}
