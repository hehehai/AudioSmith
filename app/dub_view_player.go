package main

// The Dub page's player bar and timeline lane; split from dub_view.go.

import (
	"strconv"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// dubPlayerBar: the video when shown, then the transport row
// (dub_transport.go).
func (a *app) dubPlayerBar(c *ui.Context, tl *tlModel) {
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
	a.dubPlayControls(c, tl)
}

// dubTLModel is what the open project's timeline shows.
func (a *app) dubTLModel() tlModel {
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
	return m
}

// dubTimeline is the timeline lane of the open project.
func (a *app) dubTimeline(c *ui.Context, m tlModel) {
	over := 0
	for _, b := range m.Blocks {
		if b.Overlap {
			over++
		}
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
		Key:  a.dubTransportKeys,
	})
	if over > 0 {
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
