package main

// The Voice isolator's pages: /isolate (the composer, a note when its
// models are missing, the list) and /isolate/{id} (the stems: play,
// save; hand-offs to Speech to text and Dub).

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
)

// isolatePage is /isolate.
func (a *app) isolatePage(c *ui.Context) {
	pageHeaderBar(c, pageHead{title: pageTitle("/isolate"), count: listCount(len(a.isoList), a.isoLoaded)})
	a.toolPage(c, "iso-page", func() {
		a.isoNeedNotice(c)
		a.isoComposer(c)
		if a.isoErr != "" {
			notice(c, "iso-err", noticeError, a.isoErr, "", nil, func() { a.isoErr = "" })
		}
		a.isoListView(c)
	})
	a.isoConfirmDialog(c)
}

// isoNeedNotice: 「需要下载 X（size）」 with Download, while a model the
// isolator uses is missing.
func (a *app) isoNeedNotice(c *ui.Context) {
	missing, blocked := a.isoMissing()
	if len(missing) == 0 {
		return
	}
	var names []string
	var size int64
	downloading := false
	for _, e := range missing {
		names = append(names, modelRef(e)+" ("+humanBytes(e.Size)+")")
		size += e.Size
		downloading = downloading || a.model(e).downloading
	}
	text := i18n.T("Needs %s: download it to isolate voices.", strings.Join(names, ", "))
	if !blocked {
		text = i18n.T("Needs %s to denoise the voices; without it the voices are only separated.", strings.Join(names, ", "))
	}
	kind := noticeWarn
	if !blocked {
		kind = noticeInfo
	}
	notice(c, "iso-need", kind, text, "", func() {
		label := i18n.T("Download (%s)", humanBytes(size))
		if downloading {
			label = i18n.T("Downloading…")
		}
		if secondaryIconButton(c.Key("iso-download"), icons.DownloadMinimalistic, label).Label(label).Disabled(downloading).Clicked() {
			for _, e := range missing {
				a.download(e)
			}
		}
	}, nil)
}

// isoComposer: the file (drop, choose) and the submit.
func (a *app) isoComposer(c *ui.Context) {
	_, blocked := a.isoMissing()
	toolComposer(c, "iso-composer", a.isoDropped, func() {
		if a.isoPend != "" {
			fileChip(c, "iso-pend", icons.FileText, a.isoPend, func() { a.isoPend = "" })
			return
		}
		composerPrompt(c, isolateIcon, i18n.T("Drop audio or video here"), i18n.T("Get the voices alone, the music and effects, and the voices denoised."), a.toolCompact(c))
		if ghostIconButton(c.Key("iso-choose"), icons.UploadMinimalistic, i18n.T("Upload")).Label(i18n.T("Choose a file")).Clicked() {
			a.isoChoose()
		}
	}, func() {
		caption(c, i18n.T("On this Mac, with the models dubbing uses.")).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
		ui.Spacer(c)
		if submitButton(c.Key("iso-go"), tip(i18n.T("Isolate voices"), "↩"), a.isoPend != "" && !blocked).Clicked() {
			a.isoSubmit()
		}
	})
}

// isoListView: the search and the isolations; ghost rows when none.
func (a *app) isoListView(c *ui.Context) {
	t := c.Theme()
	if len(a.isoList) == 0 {
		ghostRows(c.Key("iso-ghost"), 4, isolateIcon, i18n.T("No isolations yet"), i18n.T("Drop a song, a video or a noisy recording above: its stems show here."))
		return
	}
	narrow := a.toolNarrow(c)
	ui.Row(c.Key("iso-filters")).Gap(px(t, sp2)).AlignItems(ui.Center).Shrink(0).Children(func() {
		searchField(c, "iso-search", &a.isoQuery, i18n.T("Search isolations"), 0)
	})
	q := strings.ToLower(strings.TrimSpace(a.isoQuery))
	ui.Column(c.Key("iso-table")).Shrink(0).Children(func() {
		listHead(c, narrow, listColumn{title: i18n.T("Name")}, listColumn{title: i18n.T("Length"), w: 64, wide: true},
			listColumn{title: i18n.T("Created"), w: 110, wide: true}, listColumn{title: i18n.T("Status"), w: 130})
		n := 0
		for _, x := range a.isoList {
			if q != "" && !strings.Contains(strings.ToLower(x.Title), q) {
				continue
			}
			n++
			a.isoRow(c, x, narrow)
		}
		if n == 0 {
			ui.Column(c).Padding(px(t, sp5)).AlignItems(ui.Center).Children(func() { caption(c, i18n.T("Nothing matches.")) })
		}
	})
}

// isoStatus is how far x is: its text, whether it runs, the bar's
// fraction (-1 none) and whether it failed.
func (a *app) isoStatus(x services.Isolation) (text string, running bool, frac float64, failed bool) {
	j := a.isoJob(x.ID)
	switch {
	case j.ID != "" && j.State == jobs.StateRunning:
		return isoStageText(j.Progress.Stage), true, max(determinate(j), 0), false
	case j.ID != "", x.State == services.IsolateQueued, x.State == services.IsolateRunning:
		return i18n.T("Waiting"), true, -1, false
	case x.State == services.IsolateDone:
		return i18n.T("Done"), false, -1, false
	case x.ErrorCode == jobs.CodeCancelled:
		return i18n.T("Stopped"), false, -1, false
	}
	return i18n.T("Failed"), false, -1, true
}

// isoStageText names an isolation's stage.
func isoStageText(stage string) string {
	switch stage {
	case "decoding":
		return i18n.T("Reading the file…")
	case "separating":
		return i18n.T("Separating voices and music…")
	case "denoising":
		return i18n.T("Removing noise…")
	}
	return i18n.T("Working…")
}

// isoRow is an isolation: its title and file, length, when, status, ⋯.
func (a *app) isoRow(c *ui.Context, x services.Isolation, narrow bool) {
	k := tok(c.Theme())
	id := x.ID
	status, running, frac, failed := a.isoStatus(x)
	if running {
		c.After(500 * time.Millisecond)
	}
	ic := icons.Soundwave
	if x.Video {
		ic = dubIcon
	}
	open := listRow(c, "iso-row-"+id, x.Title, func() {
		listThumb(c, ic, nil)
		listTitle(c, x.Title, shortPath(x.Input))
		if !narrow {
			d := "—"
			if x.Duration > 0 {
				d = clock(x.Duration)
			}
			listCell(c, d, 64)
			listCell(c, ago(x.CreatedAt), 110)
		}
		col := k.Text2
		if failed {
			col = k.Danger
		}
		listStatus(c, status, running, frac, col, 130)
	}, func() {
		moreMenu(c.Key("iso-row-more-"+id), i18n.T("Isolation actions"), func(m *ui.Menu) {
			if m.Item(i18n.T("Open")).Chosen() {
				a.navigate("/isolate/" + id)
			}
			done := x.State == services.IsolateDone
			if m.Item(i18n.T("Send to Speech to text")).Disabled(!done).Chosen() {
				a.isoToTranscribe(x)
			}
			if m.Item(i18n.T("Send to Dub")).Chosen() {
				a.isoToDub(x)
			}
			m.Separator()
			if m.Item(i18n.T("Delete…")).Chosen() {
				a.isoConfirm, a.isoConfirmOpen = id, true
			}
		})
	})
	if open {
		a.navigate("/isolate/" + id)
	}
}

// isoConfirmDialog asks before an isolation is deleted.
func (a *app) isoConfirmDialog(c *ui.Context) {
	if !a.isoConfirmOpen {
		return
	}
	switch ui.AlertDialog(c.Key("iso-confirm"), &a.isoConfirmOpen, i18n.T("Delete this isolation?"),
		i18n.T("Its stems are deleted. The file you dropped is kept."), i18n.T("Delete"), i18n.T("Cancel")) {
	case 0:
		a.isoConfirmOpen = false
		a.isoDelete(a.isoConfirm, true)
	case 1:
		a.isoConfirmOpen = false
	}
}

// isolationPage is /isolate/{id}: its stems with play and save, or how
// far it is, and the hand-offs.
func (a *app) isolationPage(c *ui.Context, id string) {
	t := c.Theme()
	k := tok(t)
	x, ok := a.isoByID(id)
	title := x.Title
	if !ok {
		title = a.isoTitleOf(id)
	}
	pageHeaderBar(c, pageHead{title: title, parent: pageTitle("/isolate"), up: "/isolate", tools: func(bool) {
		if !ok {
			return
		}
		moreMenu(c.Key("iso-more"), i18n.T("Isolation actions"), func(m *ui.Menu) {
			if m.Item(i18n.T("Delete…")).Chosen() {
				a.isoConfirm, a.isoConfirmOpen = id, true
			}
		})
	}})
	a.isoConfirmDialog(c)
	if !ok {
		if a.isoLoaded {
			emptyState(c, isolateIcon, i18n.T("This isolation is gone."), "", nil)
		}
		return
	}
	ui.Scroll(c.Key("iso-detail")).Grow(1).Children(func() {
		ui.Column(c).Padding(px(t, sp1), px(t, sp6), px(t, sp6)).AlignItems(ui.Center).Children(func() {
			ui.Column(c).FillWidth().MaxWidth(px(t, 760)).Gap(px(t, sp3)).Children(func() {
				ui.Column(c).Gap(px(t, 2)).Children(func() {
					meta := []string{shortPath(x.Input)}
					if x.Duration > 0 {
						meta = append(meta, tlClock(x.Duration))
					}
					meta = append(meta, i18n.DateTime(x.CreatedAt))
					truncText(c, strings.Join(meta, " · ")).FontSize(px(t, fsCaption)).TextColor(k.Text2)
				})
				a.isoDetailBody(c, x)
			})
		})
	}).ClipX()
}

// isoDetailBody: the run, its failure, or the stems and the hand-offs.
func (a *app) isoDetailBody(c *ui.Context, x services.Isolation) {
	t := c.Theme()
	status, running, frac, failed := a.isoStatus(x)
	switch {
	case running:
		c.After(500 * time.Millisecond)
		fillCard(c.Key("iso-run")).Gap(px(t, sp2)).Children(func() {
			ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c)
				ui.Text(c, status).FontWeight(600).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
			})
			if frac >= 0 {
				ui.Progress(c, frac).Label(status)
			}
		})
		return
	case x.State != services.IsolateDone:
		text := orDash(x.Error)
		if !failed {
			text = i18n.T("Stopped before it finished.")
		}
		notice(c, "iso-failed", noticeError, text, "", func() {
			if ui.Button(c.Key("iso-retry"), i18n.T("Retry")).Clicked() {
				a.isoRetry(x)
				a.navigate("/isolate")
			}
		}, nil)
		return
	}
	for _, s := range isoStems(x) {
		a.isoStemRow(c, x, s)
	}
	for _, w := range x.Warnings {
		if w == services.WarnNoDenoise {
			caption(c, i18n.T("The voices were not denoised: %s is not downloaded.", modelRef(isoDenoiser())))
		}
	}
	ui.Row(c.Key("iso-handoffs")).Gap(px(t, sp2)).Wrap().Margin(px(t, sp2), 0, 0).Children(func() {
		if ghostIconButton(c.Key("iso-to-stt"), transcribeIcon, i18n.T("Send to Speech to text")).Label(i18n.T("Send to Speech to text")).Clicked() {
			a.isoToTranscribe(x)
		}
		if ghostIconButton(c.Key("iso-to-dub"), dubIcon, i18n.T("Send to Dub")).Label(i18n.T("Send to Dub")).Clicked() {
			a.isoToDub(x)
		}
	})
}

// isoStemRow is a stem: play, its name and what it is, Save.
func (a *app) isoStemRow(c *ui.Context, x services.Isolation, s isoStem) {
	t := c.Theme()
	k := tok(t)
	pid := "iso:" + x.ID + ":" + s.key
	card(c.Key("stem-"+s.key)).Row().Gap(px(t, sp3)).AlignItems(ui.Center).Padding(px(t, sp2), px(t, sp3)).Children(func() {
		if a.pl.Playing(pid) {
			c.After(200 * time.Millisecond)
		}
		if playButton(c.Key("play"), a.pl.Playing(pid), false, 36).Label(i18n.T("Play %s", s.name)).Clicked() {
			if err := a.pl.Toggle(pid, a.isoSvc.Path(x.ID, s.file)); err != nil {
				a.toastWarn(i18n.T("Can't play: %v", err))
			}
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 2)).Children(func() {
			ui.Text(c, s.name).FontWeight(600).SingleLine().Ellipsis("…")
			ui.Text(c, s.about).FontSize(px(t, fsCaption)).TextColor(k.Text2).SingleLine().Ellipsis("…")
		})
		save := i18n.T("Download")
		if ghostIconButton(c.Key("save"), icons.DownloadMinimalistic, save).Label(i18n.T("Download %s", s.name)).Tooltip(i18n.T("Save a copy (WAV)")).Clicked() {
			a.isoSaveStem(x, s)
		}
	})
}
