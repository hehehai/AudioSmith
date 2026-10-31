package main

// The Dub page's player bar, timeline lane and footer (status line, the
// step's next action); split from dub_view.go.

import (
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
)

// dubPlayerBar: play, A/B, time, the video.
func (a *app) dubPlayerBar(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	p := a.dub
	if p.Source.HasVideo && a.dubVideo.show {
		a.dubEnsureVideo() // again when the file changed (its preview copy came)
		if a.dubVideo.wv != nil && a.dubVideo.project == p.ID {
			ui.WebView(c, a.dubVideo.wv).Height(px(t, 220)).Radius(px(t, radiusControl))
		} else {
			ui.Column(c.Key("dub-video-off")).Height(px(t, 120)).Center().Radius(px(t, radiusControl)).Background(k.Subtle).Children(func() {
				caption(c, i18n.T("The video preview shows in the app window."))
			})
		}
	}
	a.dubPlayControls(c, true)
}

// dubPlayControls: play, A/B (once there are dubs), the time, and the
// video's switch (when toggle: the editor's; the source's centre shows
// the video always).
func (a *app) dubPlayControls(c *ui.Context, toggle bool) {
	t := c.Theme()
	k := tok(t)
	p := a.dub
	pl := &a.dubPlayer
	ui.Row(c.Key("dub-player")).Gap(px(t, sp2)).AlignItems(ui.Center).Shrink(0).Children(func() {
		if playButton(c.Key("dub-play"), a.dubPlaying(), false, 32).Label(i18n.T("Play or pause")).Tooltip(i18n.T("Play or pause (Space)")).
			Disabled(pl.loading || p.Media.Audio == "").Clicked() {
			a.dubTogglePlay()
		}
		tracks := []string{dubOrig}
		names := []string{i18n.T("Original")}
		for _, l := range p.Targets {
			if len(p.Track(l).Lines) > 0 {
				tracks = append(tracks, l)
				names = append(names, i18n.T("Dub · %s", dubLangName(l)))
			}
		}
		if len(tracks) > 1 {
			i := max(slices.Index(tracks, pl.track), 0)
			if a.dubNarrow {
				cur := names[i]
				if ui.Select(c.Key("dub-ab-select"), &cur, names).Label(i18n.T("Track")).Shrink(1).MinWidth(0).Changed() {
					a.dubSwitchTrack(tracks[slices.Index(names, cur)])
				}
			} else {
				sel := i
				if segmented(c.Key("dub-ab"), &sel, names...).Label(i18n.T("Track")).Shrink(1).MinWidth(0).Changed() || sel != i {
					a.dubSwitchTrack(tracks[sel])
				}
			}
		}
		ui.Text(c, tlClock(a.dubPosition())+" / "+tlClock(p.Source.Duration)).Font(monoFont).FontSize(px(t, fsMicro)).TextColor(k.Text2).SingleLine().Shrink(0)
		ui.Spacer(c)
		if p.Source.HasVideo && toggle {
			show := a.dubVideo.show
			if ui.Toggle(c.Key("dub-video"), &show, i18n.T("Video")).Changed() {
				a.dubVideo.show = show
				if show {
					a.dubEnsureVideo()
				}
			}
		}
	})
}

// dubTimeline is the timeline lane of the open project.
func (a *app) dubTimeline(c *ui.Context) {
	p := a.dub
	m := tlModel{Duration: p.Source.Duration, Peaks: a.dubPeaks, Onsets: p.Media.Onsets, Playhead: a.dubPosition(), Playing: a.dubPlaying()}
	over := map[string]bool{}
	for _, id := range p.Overlaps() {
		over[id] = true
		if i := p.Index(id); i+1 < len(p.Segments) {
			over[p.Segments[i+1].ID] = true
		}
	}
	for i, s := range p.Segments {
		flag := false
		for _, b := range a.dubBadges(s) {
			if b[1] == "warning" || b[1] == "danger" {
				flag = true
			}
		}
		m.Blocks = append(m.Blocks, tlBlock{ID: s.ID, Start: s.Start, End: s.End, Label: strconv.Itoa(i + 1), Overlap: over[s.ID], Muted: s.Volume == 0, Flag: flag})
	}
	if a.dubCur != "" {
		a.dubTL.sel = a.dubCur
	}
	locked := a.dubLocked()
	timelineLane(c, "dub-tl", &a.dubTL, m, tlEvents{
		Seek:    a.dubSeek,
		Select:  func(id string) { a.dubCur = id; a.dubScrollTo(id) },
		Command: a.dubCommand,
		Change: func(id string, start, end float64, clamp bool) {
			if !locked {
				a.dubSetTiming(id, start, end, clamp)
			}
		},
		Nudge: func(id, edge string, delta float64) {
			if !locked {
				a.dubNudge(id, edge, delta)
			}
		},
		Delete: func(id string) {
			if !locked {
				a.dubDelete(id)
			}
		},
		Play: a.dubPlayRange,
	})
	if len(over) > 0 {
		t := c.Theme()
		ui.Row(c.Key("dub-overlap")).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
			iconMark(c, icons.DangerCircle, t.Danger, "")
			caption(c, i18n.T("Some lines overlap the next: both play at once.")).TextColor(t.Danger)
		})
	}
}

// dubScrollTo shows a line in the list.
func (a *app) dubScrollTo(id string) {
	if i := a.dub.Index(id); i >= 0 {
		a.dubList2.ScrollIntoView(i)
	}
}

// --- the footer ------------------------------------------------------------------------------

// dubStatus says what runs.
func (a *app) dubStatus() string {
	j := a.dubRunning()
	if j.ID == "" {
		return ""
	}
	if j.Kind == services.KindDubFetch && j.State == jobs.StateRunning {
		return dubFetchStatus(j.Progress)
	}
	st := dubStageName(dubStageOfKind(j.Kind))
	var p dubJobParams
	dubJSON(j.Params, &p)
	if p.Lang != "" {
		st = dubLangName(p.Lang) + " · " + st
	}
	if j.Progress.Message != "" && strings.Contains(j.Progress.Message, "/") {
		st += " " + j.Progress.Message
	}
	if j.State != jobs.StateRunning {
		st = i18n.T("Waiting: %s", st)
	}
	return st
}

type dubJobParams struct {
	Lang string `json:"lang"`
}

// dubStartLabel names the first run's button.
func (a *app) dubStartLabel() string {
	if a.dub.Settings.Review {
		return i18n.T("Transcribe")
	}
	return i18n.T("Dub it")
}

// dubFooter: the status and progress, Cancel, and the step's action —
// the page's one primary button (Export on the Export step, else the
// pipeline's next step).
func (a *app) dubFooter(c *ui.Context) {
	t := c.Theme()
	p := a.dub
	ui.Row(c.Key("dub-footer")).Height(px(t, 52)).Shrink(0).Gap(px(t, sp2)).AlignItems(ui.Center).Padding(0, px(t, sp6)).Children(func() {
		if a.dubProbing() && !a.dubBusy() {
			// Reading the link's details (#106): a few seconds; Cancel stops it.
			ui.Spinner(c)
			ui.Text(c, i18n.T("Reading the video's details…")).FontSize(px(t, fsCaption)).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
			ui.Spacer(c)
			if secondaryButton(c.Key("dub-cancel"), i18n.T("Cancel")).Clicked() {
				a.dubCancel()
			}
			return
		}
		if a.dubBusy() {
			j := a.dubRunning()
			ui.Spinner(c)
			ui.Text(c, a.dubStatus()).FontSize(px(t, fsCaption)).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
			if f := determinate(j); f > 0 {
				ui.Progress(c.Key("dub-progress"), f).Width(px(t, 160)).Shrink(1).Label(a.dubStatus())
			}
			ui.Spacer(c)
			if secondaryButton(c.Key("dub-cancel"), i18n.T("Cancel")).Clicked() {
				a.dubCancel()
			}
			return
		}
		phase := dubPhaseText(p.Phase())
		if a.dubStep == dubStepExport {
			ex := &a.dubExport
			blocked, ready := a.dubExportBlocked(ex.format), a.dubExportReady()
			caption(c, phase).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
			ui.Spacer(c)
			if ex.job != "" {
				ui.Spinner(c)
			}
			b := primaryIconButton(c.Key("dx-go"), icons.Export, i18n.T("Export…")).Label(i18n.T("Export…")).Disabled(blocked != "" || ready != "" || ex.job != "")
			if why := firstOf(blocked, ready); why != "" {
				b.Tooltip(why)
			}
			if b.Clicked() {
				a.dubDoExport()
			}
			return
		}
		caption(c, phase).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
		ui.Spacer(c)
		switch {
		case len(p.Segments) == 0:
			if primaryButton(c.Key("dub-start"), a.dubStartLabel()).Disabled(p.Source.Path == "" && p.Source.URL == "").Clicked() {
				a.dubStart()
			}
		case !p.AllTranslated():
			if primaryButton(c.Key("dub-translate"), dubTranslateLabel(p)).Clicked() {
				a.dubTranslate(false)
			}
		default:
			n := a.dubStaleCount(a.dubLang)
			label := i18n.N(len(p.Targets), "Generate dub", "Generate %d dubs", len(p.Targets))
			if p.TracksReady() {
				if n == 0 {
					ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
						iconMark(c, icons.CheckCircle, t.Success, "")
						caption(c, i18n.T("Every line is up to date.")).SingleLine()
					})
					return
				}
				label = i18n.N(n, "Regenerate %d changed", "Regenerate %d changed", n)
			}
			if note := a.dubDialogueCount(); note != "" {
				caption(c.Key("dub-dialogue-count"), note).TextColor(tok(t).Text3).SingleLine().Shrink(0)
			}
			if primaryButton(c.Key("dub-generate"), label).Clicked() {
				a.dubGenerate()
			}
		}
	})
}

func dubTranslateLabel(p *dub.Project) string {
	if len(p.Targets) > 1 {
		return i18n.N(len(p.Targets), "Translate", "Translate into %d languages", len(p.Targets))
	}
	return i18n.T("Translate")
}
