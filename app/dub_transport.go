package main

// The Dub editor's transport row (thread #134, user 11-01: the footer
// went; its step action and the timeline's zoom came up here): play,
// Original | Dub, the time; then back to the start, −5 s / +5 s, the
// timeline's zoom, the video's switch, and the step's action — the
// page's one primary button. As the workspace narrows, the skips and the
// zoom go into a ⋯ menu (dubTransportLevel). Before the transcript there is no row of the
// editor's: the step's action is then the header's primary
// (dubHeadPrimary).

import (
	"slices"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// dubSkip is the transport's skip back and forward, seconds.
const dubSkip = 5

// The transport row's levels, by the workspace's width: everything in
// it; A/B a menu, the skips and Fit in ⋯, the video an icon and the clock
// the time alone (with a side panel at 1100); and the zoom in ⋯ too.
const (
	tpFull = iota
	tpMid
	tpNarrow
)

// dubTransportLevel is the transport row's level now.
func (a *app) dubTransportLevel(t *ui.Theme) int {
	switch {
	case a.dubNarrow:
		return tpNarrow
	case a.dubWorkW < px(t, 840):
		return tpMid
	}
	return tpFull
}

// dubPlayControls: play, A/B (once there are dubs), the time; with tl
// (the editor's timeline, nil before the transcript: the source's centre
// shows the video always) the timeline's tools, the video's switch and
// the step's action.
func (a *app) dubPlayControls(c *ui.Context, tl *tlModel) {
	t := c.Theme()
	k := tok(t)
	p := a.dub
	pl := &a.dubPlayer
	level := a.dubTransportLevel(t)
	if tl == nil && !a.dubNarrow {
		level = tpFull
	}
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
			if level != tpFull {
				cur := names[i]
				if ui.Select(c.Key("dub-ab-select"), &cur, names).Label(i18n.T("Track")).Shrink(1).MinWidth(px(t, 72)).Changed() {
					a.dubSwitchTrack(tracks[slices.Index(names, cur)])
				}
			} else {
				sel := i
				if ui.Segmented(c.Key("dub-ab"), &sel, names...).Label(i18n.T("Track")).Shrink(0).Changed() || sel != i {
					a.dubSwitchTrack(tracks[sel])
				}
			}
		}
		clock := tlClock(a.dubPosition()) + " / " + tlClock(p.Source.Duration)
		shown := clock
		if level != tpFull {
			shown = tlClock(a.dubPosition())
		}
		ui.Text(c.Key("dub-clock"), shown).Font(monoFont).FontSize(px(t, fsMicro)).TextColor(k.Text2).SingleLine().Shrink(0).Tooltip(clock)
		ui.Spacer(c)
		if tl == nil {
			return
		}
		if tl.Duration > 0 {
			a.dubTransportTools(c, *tl, level)
		}
		if p.Source.HasVideo {
			show := a.dubVideo.show
			var b ui.Element
			if level == tpFull {
				b = ui.Toggle(c.Key("dub-video"), &show, i18n.T("Video")).Shrink(0)
			} else if b = iconToggle(c.Key("dub-video"), dubIcon, show, i18n.T("Video")); b.Clicked() {
				show = !show
			}
			if b.Changed() || show != a.dubVideo.show {
				a.dubVideo.show = show
				if show {
					a.dubEnsureVideo()
				}
			}
		}
		a.dubPrimary(c)
	})
}

// dubTransportTools: back to the start, −5 s / +5 s, a hairline, and the
// timeline's zoom, as level leaves room; the rest in a ⋯ menu.
func (a *app) dubTransportTools(c *ui.Context, m tlModel, level int) {
	t := c.Theme()
	k := tok(t)
	s := &a.dubTL
	d := m.Duration
	seek := func(sec float64) {
		sec = min(max(sec, 0), d)
		a.dubSeek(sec)
		s.showTime(sec, d)
	}
	ui.Row(c.Key("dub-transport")).Gap(px(t, 2)).AlignItems(ui.Center).Shrink(0).KeepFocus().Children(func() {
		if iconButton(c.Key("tp-start"), icons.SkipPrevious, i18n.T("Back to the start")).Tooltip(i18n.T("Back to the start (Home)")).Clicked() {
			seek(0)
		}
		if level == tpFull {
			if skipButton(c, "tp-back", "−5s", i18n.T("Back %d s", dubSkip), i18n.T("Back %d s (J)", dubSkip)).Clicked() {
				seek(a.dubPosition() - dubSkip)
			}
			if skipButton(c, "tp-fwd", "+5s", i18n.T("Forward %d s", dubSkip), i18n.T("Forward %d s (L)", dubSkip)).Clicked() {
				seek(a.dubPosition() + dubSkip)
			}
			ui.Box(c).Size(1, px(t, 16)).Background(k.Line).Margin(0, px(t, 4)).Shrink(0)
			timelineZoomTools(c, s, m, true)
			return
		}
		if level == tpMid {
			timelineZoomTools(c, s, m, false)
		}
		moreMenu(c.Key("tp-more"), i18n.T("Timeline tools"), func(mn *ui.Menu) {
			if mn.Item(i18n.T("Back %d s (J)", dubSkip)).Chosen() {
				seek(a.dubPosition() - dubSkip)
			}
			if mn.Item(i18n.T("Forward %d s (L)", dubSkip)).Chosen() {
				seek(a.dubPosition() + dubSkip)
			}
			mn.Separator()
			timelineZoomMenu(mn, s, m, level == tpNarrow)
		})
	})
}

// skipButton is a small ghost button with a short text ("−5s").
func skipButton(c *ui.Context, key, text, label, tip string) ui.Element {
	t := c.Theme()
	b := ghostBase(c.Key(key)).Height(px(t, 28)).Padding(0, px(t, 5)).Center().Label(label).Tooltip(tip).Shrink(0)
	b.Children(func() {
		col := tok(t).Text2
		if b.Hovered() {
			col = tok(t).Text
		}
		ui.Text(c, text).Font(monoFont).FontSize(px(t, fsMicro)).TextColor(col).SingleLine()
	})
	return b
}

// dubTransportKeys are the transport's keys while the editor has the
// keyboard and no text field does: Space plays or pauses, Home goes
// back to the start, J and L skip 5 s back and forward (as video
// editors' J/K/L).
func (a *app) dubTransportKeys(e ui.InputEvent) bool {
	p := a.dub
	if e.Kind != ui.InputKeyDown || e.Mods&(ui.Cmd|ui.Ctrl|ui.Alt) != 0 || p == nil || p.Source.Duration <= 0 {
		return false
	}
	d := p.Source.Duration
	at := -1.0
	switch e.Key {
	case ui.KeySpace:
		if p.Media.Audio != "" && !a.dubPlayer.loading {
			a.dubTogglePlay()
		}
		return true
	case ui.KeyHome:
		at = 0
	case ui.KeyJ:
		at = a.dubPosition() - dubSkip
	case ui.KeyL:
		at = a.dubPosition() + dubSkip
	default:
		return false
	}
	at = min(max(at, 0), d)
	a.dubSeek(at)
	a.dubTL.showTime(at, d)
	return true
}

// dubPrimary is the step's action, the page's one primary button:
// Export on the Export step, else the pipeline's next step (Transcribe
// or Dub it, Translate, Generate or Regenerate the changed lines); none
// once every line is up to date (the header's status says so). While a
// step runs it stays, disabled, so the row keeps its place.
func (a *app) dubPrimary(c *ui.Context) {
	p := a.dub
	busy := a.dubBusy() || a.dubProbing()
	var b ui.Element
	if a.dubStep == dubStepExport && len(p.Segments) > 0 {
		ex := &a.dubExport
		blocked, ready := a.dubExportBlocked(ex.format), a.dubExportReady()
		b = primaryIconButton(c.Key("dx-go"), icons.Export, i18n.T("Export")).Label(i18n.T("Export")).Disabled(blocked != "" || ready != "" || ex.job != "")
		if why := firstOf(blocked, ready); why != "" {
			b.Tooltip(why)
		}
		if b.Shrink(0).Clicked() {
			a.dubDoExport()
		}
		return
	}
	switch {
	case len(p.Segments) == 0:
		b = primaryButton(c.Key("dub-start"), a.dubStartLabel()).Disabled(busy || p.Source.Path == "" && p.Source.URL == "")
		if b.Clicked() {
			a.dubStart()
		}
	case !p.AllTranslated():
		b = primaryButton(c.Key("dub-translate"), dubTranslateLabel(p)).Disabled(busy)
		if b.Clicked() {
			a.dubTranslate(false)
		}
	default:
		n := a.dubStaleCount(a.dubLang)
		label := i18n.N(len(p.Targets), "Generate dub", "Generate %d dubs", len(p.Targets))
		if p.TracksReady() {
			if n == 0 && !busy {
				return
			}
			label = i18n.N(n, "Regenerate %d changed", "Regenerate %d changed", n)
		}
		b = primaryButton(c.Key("dub-generate"), label).Disabled(busy)
		if note := a.dubDialogueCount(); note != "" {
			b.Tooltip(note) // the lines a conversation records together
		}
		if b.Clicked() {
			a.dubGenerate()
		}
	}
	b.Shrink(0)
}

// dubHeadPrimary puts the step's action in the header while the editor
// has no transport row (no transcript yet): the page's one primary.
func (a *app) dubHeadPrimary(c *ui.Context) func(bool) {
	if a.dub == nil || len(a.dub.Segments) > 0 {
		return nil
	}
	return func(bool) { a.dubPrimary(c) }
}
