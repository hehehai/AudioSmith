package main

// The Dub page (/dub, 0.5 F2): a composer on top (drop a video or audio
// file, or paste a link; the language to dub into; the round 「配音」)
// that makes a dub and starts it at once (the source's card, download
// and transcript run in the background), and the list of dubs under it
// (search, status and language filters, live progress); a row opens the
// dub's editor at /dub/{id} (dub_view.go).

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/store"
)

// dubListState is the list page's state (dubState embeds it).
type dubListState struct {
	dubPend    string            // the composer's file ("" none)
	dubTarget  string            // the language chosen in the composer ("" the default)
	dubQuery   string            // the list's search
	dubFilter  int               // 0 all, 1 in progress, 2 done, 3 failed
	dubLangF   string            // the language filter ("" all)
	dubCovers  map[string]string // each dub's cover, as its project says
	dubOpening string            // the dub /dub/{id} is reading
	dubWant    int               // the step a dub opens at (-1: the first not done)
}

// dubListPage is /dub: the composer and the list.
func (a *app) dubListPage(c *ui.Context) {
	a.toolPage(c, "dub-list-page", func() {
		a.dubComposer(c)
		if a.dubErr != "" {
			a.dubErrorNotice(c)
		}
		a.dubListView(c)
	})
	a.dubConfirmDialog(c)
}

// dubComposer: the drop zone (the file, or a link), the language and 「配音」.
func (a *app) dubComposer(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	ready := a.dubPend != "" || strings.TrimSpace(a.dubURL) != ""
	toolComposer(c, "dub-composer", a.dubComposerDrop, func() {
		if a.dubPend != "" {
			fileChip(c, "dub-pend", dubIcon, a.dubPend, func() { a.dubPend = "" })
		} else {
			composerPrompt(c, icons.UploadMinimalistic, i18n.T("Drop a video or audio file here"), "MP4 · MOV · M4V · MKV · WEBM · MP3 · WAV · M4A · FLAC", a.toolCompact(c))
			if ghostIconButton(c.Key("dub-choose"), icons.UploadMinimalistic, i18n.T("Choose file…")).Label(i18n.T("Choose file…")).Clicked() {
				a.chooseDubSource()
			}
		}
		if a.dubPend != "" {
			return // a file goes first: no link with it
		}
		ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).FillWidth().MaxWidth(px(t, 520)).Children(func() {
			ui.Icon(c, icons.Link.Linear).Size(px(t, 16), px(t, 16)).TextColor(k.Text3).Shrink(0)
			in := ui.TextInput(c.Key("dub-url"), &a.dubURL).Placeholder(i18n.T("…or paste a YouTube or video link")).Label(i18n.T("Video link")).Grow(1).MinWidth(0)
			if in.Submitted() && ready {
				a.dubSubmit()
			}
		})
		if strings.TrimSpace(a.dubURL) != "" {
			ui.Column(c).FillWidth().MaxWidth(px(t, 520)).Children(func() { a.dubLinkOptions(c) })
		}
	}, func() {
		a.dubTargetChip(c)
		ui.Spacer(c)
		if submitButton(c.Key("dub-go"), tip(i18n.X("action", "Dub"), "↩"), ready).Clicked() {
			a.dubSubmit()
		}
	})
}

// dubComposerDrop holds the first video or audio file dropped.
func (a *app) dubComposerDrop(paths []string) {
	for _, p := range paths {
		if dub.IsMedia(p, dubExts) {
			a.dubPend = p
			return
		}
	}
	a.toast(i18n.T("Drop a video or audio file."))
}

// dubLinkOptions: the captions and the sign-in cookies of a link.
func (a *app) dubLinkOptions(c *ui.Context) {
	t := c.Theme()
	collapsible(c.Key("dub-url-more"), i18n.T("Link options"), &a.dubURLMore, nil, func() {
		ui.Checkbox(c.Key("dub-captions"), &a.dubCaptions, i18n.T("Use the video's captions when it has them (written by people)"))
		ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
			label := i18n.T("Sign-in cookies (cookies.txt)…")
			if a.dubCookies != "" {
				label = shortPath(a.dubCookies)
			}
			if ui.Button(c.Key("dub-cookies"), label).Shrink(1).MinWidth(0).Clicked() {
				a.dubChooseCookies()
			}
			if a.dubCookies != "" && iconButton(c.Key("dub-cookies-x"), icons.Close, i18n.T("Remove the cookies")).Clicked() {
				a.dubCookies = ""
			}
			infoTip(c.Key("dub-cookies-tip"), i18n.T("Cookies are used for this download only and never saved."))
		})
	})
}

// dubTargetChip is the language a new dub is made in: Settings →
// Dubbing's (Chinese, English for a Chinese video) until one is chosen.
func (a *app) dubTargetChip(c *ui.Context) {
	t := c.Theme()
	label := i18n.T("Into: %s", dubLangPlain(a.dubDefaultTarget()))
	if a.dubTarget == "" && len(a.prefs.Dubbing.Targets) == 0 {
		label = i18n.T("Into: %s (auto)", dubLangPlain(dub.DefaultTarget))
	}
	dropButton(c.Key("dub-target"), icons.Global, label).MaxWidth(px(t, 260)).Tooltip(dubAutoTargetHint()).Menu(func(m *ui.Menu) {
		if m.Item(i18n.T("Default (%s)", dubLangPlain(firstOf(firstTarget(a.prefs.Dubbing.Targets), dub.DefaultTarget)))).Checked(a.dubTarget == "").Chosen() {
			a.dubTarget = ""
		}
		m.Separator()
		for _, l := range dubTargetLangs {
			if m.Item(dubLangName(l)).Checked(a.dubTarget == l).Chosen() {
				a.dubTarget = l
			}
		}
	})
}

// firstTarget is the first of targets ("" none).
func firstTarget(targets []string) string {
	if len(targets) == 0 {
		return ""
	}
	return targets[0]
}

// dubDefaultTarget is the language the composer dubs into.
func (a *app) dubDefaultTarget() string {
	return firstOf(a.dubTarget, firstTarget(a.prefs.Dubbing.Targets), dub.DefaultTarget)
}

// dubSubmit makes a dub of the composer's file, else its link, and
// starts it.
func (a *app) dubSubmit() {
	if a.dubPend != "" {
		path := a.dubPend
		a.dubPend = ""
		a.dubStartFile(path)
		return
	}
	u := strings.TrimSpace(a.dubURL)
	if u == "" {
		return
	}
	if _, err := dub.CheckURL(context.Background(), u, nil); err != nil {
		a.dubErr, a.dubErrCode = dubURLErrorText(err), ""
		return
	}
	if !a.prefs.Dubbing.URLImport || a.prefs.Dubbing.YtDlp == "" {
		a.dubErr, a.dubErrCode = i18n.T("Importing from a URL is off. Turn it on in Settings → Dubbing (it downloads yt-dlp, about 35 MB)."), dub.CodeNoYtDlp
		return
	}
	a.dubErr, a.dubURL = "", ""
	a.dubCreate(dub.Source{Kind: dub.SourceURL, URL: u, Name: u, WantCaptions: a.dubCaptions})
}

// dubStartFile makes a dub of a file and starts it (the composer, Home,
// a transcript's or an isolation's 「配音」).
func (a *app) dubStartFile(path string) {
	src := dub.Source{Kind: dub.SourceFile, Path: path, Name: filepath.Base(path)}
	if fi, err := os.Stat(path); err == nil {
		src.Size = fi.Size()
	}
	a.dubCreate(src)
}

// dubCreate makes a dub of src in the composer's language, puts it at the
// top of the list, makes it the open dub (its jobs are followed) without
// leaving the list, reads what its source is and starts its run.
func (a *app) dubCreate(src dub.Source) {
	p := newDubProject(src, a.prefs.Dubbing, a.dubDefaultModel())
	if a.dubTarget != "" {
		p.ChooseTargets([]string{a.dubTarget})
	}
	a.dubErr, a.dubErrCode, a.dubNeed = "", "", nil
	svc := a.dubSvc
	a.async(func() {
		created, err := svc.Create(context.Background(), p)
		a.post(func() {
			if err != nil {
				a.dubErr = i18n.T("Could not make the dub: %v", err)
				return
			}
			a.dubPutRecord(created)
			a.dubShow(created, nil)
			a.dubProbe(created.ID)
			a.dubStart() // dub_run.go: to the transcript (review on), else the whole dub
		})
	})
}

// dubShown are the list's rows: the Dub page's dubs, searched and filtered.
func (a *app) dubShown() []store.DubRecord {
	q := strings.ToLower(strings.TrimSpace(a.dubQuery))
	var out []store.DubRecord
	for _, r := range a.dubList {
		if q != "" && !strings.Contains(strings.ToLower(r.Title+" "+r.Source), q) {
			continue
		}
		if a.dubLangF != "" && !slices.Contains(r.Languages, a.dubLangF) {
			continue
		}
		running := a.dubRowJob(r.ID).ID != ""
		switch ph := dub.Phase(r.Phase); a.dubFilter {
		case 1:
			if !running && ph != dub.PhasePreparing && ph != dub.PhaseTranslating && ph != dub.PhaseGenerating {
				continue
			}
		case 2:
			if running || ph != dub.PhaseDone {
				continue
			}
		case 3:
			if running || ph != dub.PhaseFailed {
				continue
			}
		}
		out = append(out, r)
	}
	return out
}

// dubRowJob is the job of dub id that runs (or waits) now, zero if none.
func (a *app) dubRowJob(id string) jobs.Job {
	if a.dub != nil && a.dub.ID == id {
		return a.dubRunning()
	}
	var best jobs.Job
	for _, j := range a.jobList {
		if j.Owner != services.DubOwner+id || j.State.Terminal() || j.State == jobs.StateInterrupted {
			continue
		}
		if j.State == jobs.StateRunning {
			return j
		}
		if best.ID == "" || j.Created.Before(best.Created) {
			best = j
		}
	}
	return best
}

// dubListView: the search and filters, then the dubs (ghost rows when none).
func (a *app) dubListView(c *ui.Context) {
	t := c.Theme()
	if len(a.dubList) == 0 {
		ghostRows(c.Key("dub-ghost"), 4, dubIcon, i18n.T("No dubs yet"), i18n.T("Drop a video above or paste its link: the dub starts at once and shows here."))
		return
	}
	narrow := a.toolNarrow(c)
	ui.Column(c.Key("dub-filters")).Gap(px(t, sp2)).Shrink(0).Children(func() {
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			searchField(c, "dub-search", &a.dubQuery, i18n.T("Search dubs"), 0)
		})
		ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Wrap().Children(func() {
			for i, name := range []string{i18n.T("All"), i18n.T("In progress"), i18n.T("Done"), i18n.T("Failed")} {
				if chip(c.Key("dub-f-"+itoa(i)), name, "", a.dubFilter == i).Clicked() {
					a.dubFilter = i
				}
			}
			ui.Box(c).Width(1).Height(px(t, 16)).Background(tok(t).Line).Margin(0, px(t, sp1))
			label := i18n.T("All languages")
			if a.dubLangF != "" {
				label = dubLangName(a.dubLangF)
			}
			dropText(c.Key("dub-lang-f"), label, a.dubLangF != "").Menu(func(m *ui.Menu) {
				if m.Item(i18n.T("All languages")).Checked(a.dubLangF == "").Chosen() {
					a.dubLangF = ""
				}
				for _, l := range a.dubListLangs() {
					if m.Item(dubLangName(l)).Checked(a.dubLangF == l).Chosen() {
						a.dubLangF = l
					}
				}
			})
		})
	})
	rows := a.dubShown()
	ui.Column(c.Key("dub-table")).Shrink(0).Children(func() {
		listHead(c, narrow, listColumn{title: i18n.T("Name")}, listColumn{title: i18n.T("Languages"), w: 130, wide: true},
			listColumn{title: i18n.T("Length"), w: 64, wide: true}, listColumn{title: i18n.T("Status"), w: 150})
		if len(rows) == 0 {
			ui.Column(c).Padding(px(t, sp5)).AlignItems(ui.Center).Children(func() {
				caption(c, i18n.T("No dubs match."))
			})
		}
		for _, r := range rows {
			a.dubListRow(c, r, narrow)
		}
	})
}

// dubListLangs are the target languages of the list's dubs.
func (a *app) dubListLangs() []string {
	var out []string
	for _, r := range a.dubList {
		for _, l := range r.Languages {
			if !slices.Contains(out, l) {
				out = append(out, l)
			}
		}
	}
	slices.Sort(out)
	return out
}

// dubListRow is a dub: its cover, title and source, languages, length,
// status (live while it runs) and ⋯ (Open, Export, Delete).
func (a *app) dubListRow(c *ui.Context, r store.DubRecord, narrow bool) {
	t := c.Theme()
	k := tok(t)
	id := r.ID
	title := firstOf(r.Title, i18n.T("Untitled dub"))
	j := a.dubRowJob(id)
	if j.ID != "" {
		c.After(500 * time.Millisecond) // the progress moves
	}
	open := listRow(c, "dub-row-"+id, title, func() {
		ic := dubIcon
		if ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(r.Source), ".")); slices.Contains(homeAudioExts, ext) {
			ic = icons.Soundwave // sound alone
		}
		listThumb(c, ic, a.coverBitmap(a.dubCovers[id]))
		sub := ago(r.UpdatedAt)
		if src := dubSourceLabel(r.Source); src != "" {
			sub = src + " · " + sub
		}
		listTitle(c, title, sub)
		if !narrow {
			var langs []string
			for _, l := range r.Languages {
				langs = append(langs, dubLangName(l))
			}
			listCell(c, strings.Join(langs, ", "), 130)
			d := "—"
			if r.Duration > 0 {
				d = clock(r.Duration)
			}
			listCell(c, d, 64)
		}
		status, col, frac := dubPhaseText(dub.Phase(r.Phase)), k.Text2, -1.0
		if j.ID != "" {
			status = dubStageName(dubStageOfKind(j.Kind))
			if j.State != jobs.StateRunning {
				status = i18n.T("Waiting: %s", status)
			}
			frac = max(determinate(j), 0)
		} else if dub.Phase(r.Phase) == dub.PhaseFailed {
			col = k.Danger
		}
		listStatus(c, status, j.ID != "", frac, col, 150)
	}, func() {
		moreMenu(c.Key("dub-row-more-"+id), i18n.T("Dub actions"), func(m *ui.Menu) {
			if m.Item(i18n.T("Open")).Chosen() {
				a.openDub(id)
			}
			if m.Item(i18n.T("Export…")).Disabled(dub.Phase(r.Phase) != dub.PhaseDone).Chosen() {
				a.dubWant = dubStepExport
				a.openDub(id)
			}
			m.Separator()
			if m.Item(i18n.T("Delete…")).Chosen() {
				a.dubConfirm, a.dubConfirmOpen = "delete:"+id, true
			}
		})
	})
	if open {
		a.openDub(id)
	}
}

// dubSourceLabel is a source for a row: a link's host, a file's name.
func dubSourceLabel(src string) string {
	if src == "" {
		return ""
	}
	if l := strings.ToLower(src); strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") {
		host := src[strings.Index(src, "//")+2:]
		host, _, _ = strings.Cut(host, "/")
		return strings.TrimPrefix(host, "www.")
	}
	return filepath.Base(src)
}

// dubErrorNotice is the last error, with its fix (a download, the
// writing help, Settings → Dubbing).
func (a *app) dubErrorNotice(c *ui.Context) {
	notice(c, "b-error", noticeError, a.dubErr, "", func() {
		if a.dubNeed != nil {
			e := *a.dubNeed
			if secondaryIconButton(c.Key("b-download"), icons.DownloadMinimalistic, i18n.T("Download")).Label(i18n.T("Download")).Clicked() {
				a.download(e)
				a.dubErr, a.dubNeed = "", nil
			}
		}
		switch a.dubErrCode {
		case services.DubCodeNoTranslator:
			if ui.Button(c.Key("b-llm"), i18n.T("Set up writing help")).Clicked() {
				a.navigate("/settings/writing")
			}
		case dub.CodeNoYtDlp:
			if ui.Button(c.Key("b-ytdlp"), i18n.T("Open Settings → Dubbing")).Clicked() {
				a.navigate("/settings/dubbing")
			}
		}
	}, func() { a.dubErr, a.dubErrCode, a.dubNeed = "", "", nil })
}
