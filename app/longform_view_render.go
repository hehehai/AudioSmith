package main

// The longform render bar: status, the chapter tracker and interrupted
// renders; split from longform_view.go.

import (
	"fmt"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/services"
)

// lfRenderPanel is the render bar and the outputs, scrolling when they
// would take more than half the page.
func (a *app) lfRenderPanel(c *ui.Context, p *lfPage) {
	t := c.Theme()
	ui.Scroll(c.Key("lf-panel")).MaxHeightPercent(55).Shrink(0).Children(func() {
		ui.Column(c).Gap(px(t, sp3)).Children(func() {
			a.lfRenderBar(c, p)
			a.lfOutputs(c, p)
		})
	})
}

// lfRenderBar is Generate / Stop, the reason it can't, and the progress.
func (a *app) lfRenderBar(c *ui.Context, p *lfPage) {
	t := c.Theme()
	k := tok(t)
	r := a.lfRenders[p.render]
	running := r != nil && r.rec.State == core.RenderRunning
	blocker := a.lfBlocker(p)
	ui.Column(c.Key("lf-render")).Gap(px(t, sp2)).Children(func() {
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).MinHeight(px(t, 32)).Children(func() {
			label := i18n.T("Generate")
			if p.kind == core.ProjectAudiobook {
				label = i18n.T("Create audiobook")
			}
			switch {
			case running:
				ui.Spinner(c)
				ui.Text(c, a.renderStatus(r)).FontSize(px(t, fsCaption)).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
				if secondaryIconButton(c.Key("lf-stop"), icons.Stop, i18n.T("Stop")).Label(i18n.T("Stop")).Disabled(r.body.Stopping).Clicked() {
					a.stopLf(p)
				}
			default:
				msg := blocker
				if msg == "" {
					msg = a.lfReadyLine(p)
				}
				if blocker != "" {
					iconMark(c, icons.DangerTriangle, t.Warning, "")
				}
				txt := caption(c.Key("lf-blocker"), msg).Grow(1).MinWidth(0)
				if blocker != "" {
					txt.TextColor(t.Warning)
				}
				if r != nil && (r.rec.State == core.RenderFailed || r.rec.State == core.RenderStopped) {
					if secondaryButton(c.Key("lf-resume"), i18n.X("render", "Resume")).Tooltip(i18n.T("Render what is missing: finished chapters are kept")).
						Disabled(a.activeRender() != nil).Clicked() {
						a.resumeLf(p, r.rec.ID)
					}
				}
				// Clear: start over with other text (Undo brings it back).
				if p.kind == core.ProjectStory && len(p.doc.Lines) > 0 {
					if ghostIconButton(c.Key("lf-clear"), icons.TrashBinMinimalistic, i18n.X("story", "Clear")).
						Tooltip(i18n.T("Remove every line to start again; the cast stays")).Clicked() {
						a.clearLfUndo(p)
					}
				}
				if primaryButton(c.Key("lf-generate"), label).Disabled(blocker != "").Clicked() {
					a.startLf(p, "")
				}
			}
		})
		if r == nil {
			return
		}
		switch {
		case running:
			a.chapterTracker(c, r)
		case r.rec.State == core.RenderFailed:
			n := r.body.Failed()
			msg := renderErrorText(&r.body)
			if n > 0 {
				msg = i18n.N(n, "%d chapter failed: Resume retries just that one.", "%d chapters failed: Resume retries just those.", n) + " " + msg
			}
			ui.Row(c).Gap(px(t, 6)).Children(func() {
				iconMark(c, icons.DangerCircle, t.Danger, "")
				caption(c.Key("lf-failed"), msg).TextColor(t.Danger).Selectable().Grow(1).MinWidth(0)
			})
			a.chapterTracker(c, r)
		case r.rec.State == core.RenderStopped:
			caption(c, i18n.T("Stopped: finished chapters are kept; Resume goes on from them.")).TextColor(k.Text2)
		}
	})
	a.interruptedRenders(c, p)
}

// lfReadyLine says what Generate will make.
func (a *app) lfReadyLine(p *lfPage) string {
	st := p.stats
	if st.Chapters == 0 || p.kind == core.ProjectAudiobook { // the Audiobook's stats show above its manuscript
		return ""
	}
	return i18n.N(st.Chapters, "%d chapter", "%d chapters", st.Chapters) + " · " + i18n.T("≈ %s", formatDuration(st.Seconds))
}

// renderStatus is a running render's one line.
func (a *app) renderStatus(r *lfRender) string {
	b := &r.body
	if b.Stopping {
		return i18n.T("Stopping…")
	}
	if b.Assembling {
		if b.Assembled > 0 {
			return i18n.T("Assembling the audiobook… %d%%", int(b.Assembled*100))
		}
		return i18n.T("Assembling the audiobook…")
	}
	cur := 0
	for i, c := range b.Chapters {
		if c.State == services.ChapterRendering {
			cur = i + 1
		}
	}
	if cur == 0 {
		return i18n.T("Waiting for the engine…")
	}
	return i18n.T("Synthesizing chapter %d of %d", cur, len(b.Chapters))
}

// chapterTracker: elapsed, ETA, a bar, and every chapter's state.
func (a *app) chapterTracker(c *ui.Context, r *lfRender) {
	t := c.Theme()
	k := tok(t)
	b := &r.body
	total, done := 0.0, 0.0
	for _, ch := range b.Chapters {
		total += float64(ch.Chars)
		switch ch.State {
		case services.ChapterDone, services.ChapterCached:
			done += float64(ch.Chars)
		case services.ChapterRendering:
			done += float64(ch.Chars) * ch.Fraction
		}
	}
	frac := 0.0
	if total > 0 {
		frac = done / total
	}
	if b.Assembling || r.rec.State == core.RenderDone {
		frac = 1
	}
	now := time.Now()
	if r.rec.State == core.RenderRunning {
		c.After(time.Second) // the clock
	}
	fillCard(c.Key("lf-track")).Padding(px(t, sp3)).Gap(px(t, sp2)).Children(func() {
		ui.Progress(c, frac).Label(i18n.T("Render progress"))
		info := i18n.T("%d of %d chapters", b.Done(), len(b.Chapters))
		if !b.Started.IsZero() && r.rec.State == core.RenderRunning {
			info += " · " + i18n.T("elapsed %s", lfClock(now.Sub(b.Started)))
			if eta, ok := services.ETA(b, now); ok && !b.Assembling {
				info += " · " + i18n.T("~%s left", lfClock(eta))
			}
		}
		caption(c, info).SingleLine().Ellipsis("…").MinWidth(0)
		ui.Scroll(c.Key("lf-chapters")).MaxHeight(px(t, 200)).Children(func() {
			ui.Column(c).Gap(px(t, 2)).Children(func() {
				for i, ch := range b.Chapters {
					title := ch.Title
					if title == "" {
						title = i18n.T("Chapter %d", i+1)
					}
					extra := ""
					switch ch.State {
					case services.ChapterRendering:
						extra = " · " + strconv.Itoa(int(ch.Fraction*100)) + "%"
					case services.ChapterCached:
						extra = " · " + i18n.T("cached")
					case services.ChapterFailed:
						extra = " · " + i18n.T("failed: %s", jobErrorText(ch.Error))
					case services.ChapterStopped:
						extra = " · " + i18n.T("stopped")
					}
					ui.Row(c.Key("ch-" + strconv.Itoa(i))).Gap(px(t, sp2)).AlignItems(ui.Center).MinHeight(px(t, 22)).Children(func() {
						ui.Box(c).Size(px(t, 14), px(t, 14)).Center().Shrink(0).Children(func() {
							switch ch.State {
							case services.ChapterRendering:
								statusDot(c, k.Accent)
							case services.ChapterDone:
								iconMark(c, icons.CheckCircle, t.Success, "")
							case services.ChapterCached:
								iconMark(c, icons.CheckCircle, k.Text2, "")
							case services.ChapterFailed:
								iconMark(c, icons.CloseCircle, t.Danger, "")
							case services.ChapterStopped:
								iconMark(c, icons.Stop, k.Text3, "")
							default:
								statusDot(c, k.G[5])
							}
						})
						truncText(c, title+extra).FontSize(px(t, fsCaption)).Grow(1)
					})
				}
			})
		})
	})
}

// lfClock is "M:SS" or "H:MM:SS".
func lfClock(d time.Duration) string {
	s := int(d.Round(time.Second) / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// interruptedRenders lists this page's renders that stopped or failed
// (other than the one shown), with Resume.
func (a *app) interruptedRenders(c *ui.Context, p *lfPage) {
	t := c.Theme()
	var list []*lfRender
	for _, id := range a.lfRecent {
		r := a.lfRenders[id]
		if r == nil || r.rec.Kind != p.kind || id == p.render {
			continue
		}
		if r.rec.State == core.RenderFailed || r.rec.State == core.RenderStopped {
			list = append(list, r)
		}
		if len(list) == 3 {
			break
		}
	}
	for _, r := range list {
		ui.Row(c.Key("int-"+r.rec.ID)).Gap(px(t, sp2)).AlignItems(ui.Center).Padding(px(t, sp2), px(t, sp2), px(t, sp2), px(t, sp3)).
			Radius(px(t, radiusControl)).Border(1, tok(t).Line).Children(func() {
			iconMark(c, icons.Stop, tok(t).Text3, "")
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				truncText(c, i18n.T("Interrupted: %s", r.rec.Title))
				ui.Text(c, i18n.T("%d of %d chapters are made", r.body.Done(), len(r.body.Chapters))).FontSize(px(t, fsMicro)).TextColor(tok(t).Text2)
			})
			if ghostButton(c.Key("show"), i18n.T("Show")).Clicked() {
				p.render = r.rec.ID
			}
			if secondaryButton(c.Key("resume"), i18n.X("render", "Resume")).Disabled(a.activeRender() != nil).Clicked() {
				a.resumeLf(p, r.rec.ID)
			}
		})
	}
}
