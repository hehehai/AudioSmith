package main

// A line of the Dub editor's list (dub_editor.go): its first row, the
// source and text, notes, the check's flags and options.

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// dubRow is one line; it returns a change to make after the list. A flat
// row (the selected one on the subtle fill): tick, time, the speaker
// chip, badges, then audition and ⋯; the source dimmed, the line.
func (a *app) dubRow(c *ui.Context, i int) (after func()) {
	t := c.Theme()
	k := tok(t)
	p := a.dub
	s := p.Segments[i]
	id := s.ID
	locked := a.dubLocked()
	cur := a.dubCur == id
	row := ui.Column(c.Key("row-"+id)).Gap(px(t, 4)).Padding(px(t, 6), px(t, sp2)).Radius(px(t, radiusCard))
	if cur {
		row.Background(k.Subtle)
	}
	a.dubDialogueBar(c, row, id)
	if row.Clicked() && !cur {
		a.dubCur = id
		a.dubTL.sel = id
	}
	row.Children(func() {
		a.dubRowHead(c, i, &after)
		// The source, dimmed, when a translation shows.
		translated := a.dubLang != "" && strings.TrimSpace(p.TextFor(s, a.dubLang)) != ""
		if translated && !(a.dubEdit == id && a.dubEditL == "") {
			src := ui.Text(c, s.Text).FontSize(px(t, fsCaption)).TextColor(k.Text2)
			if !locked && src.Clicked() {
				after = func() { a.dubBeginEdit(id, "") }
			}
		}
		a.dubLineText(c, s, locked, &after)
		a.dubRowNotes(c, s, locked, &after)
		a.dubDialogueFlag(c, s)
		a.dubRowQC(c, s)
		if a.dubOpen == id {
			a.dubLineOptions(c, s, locked, &after)
		}
	})
	return after
}

// dubRowHead is a line's first row: tick, time, the speaker chip,
// badges, audition and ⋯.
func (a *app) dubRowHead(c *ui.Context, i int, after *func()) {
	t := c.Theme()
	k := tok(t)
	p := a.dub
	s := p.Segments[i]
	id := s.ID
	locked := a.dubLocked()
	overlap := i+1 < len(p.Segments) && s.End > p.Segments[i+1].Start+0.01
	ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).KeepFocus().Children(func() {
		sel := a.dubSel[id]
		if ui.Checkbox(c.Key("sel"), &sel, "").Label(i18n.T("Select line %d", i+1)).Changed() {
			if sel {
				a.dubSel[id] = true
			} else {
				delete(a.dubSel, id)
			}
		}
		tb := ghostBase(c.Key("time")).Padding(px(t, 2), px(t, 6)).Label(i18n.T("Play from %s", tlClock(s.Start))).Tooltip(i18n.T("Play from %s", tlClock(s.Start))).Shrink(0)
		tb.Children(func() {
			ui.Text(c, tlClock(s.Start)).Font(monoFont).FontSize(px(t, fsMicro)).TextColor(k.Text2)
		})
		if tb.Clicked() {
			*after = func() { a.dubPlayRange(id) }
		}
		who := p.SpeakerName(s.Speaker) + " · " + a.dubVoiceLabel(p.VoiceOf(s))
		wb := ghostBase(c.Key("who")).Padding(px(t, 2), px(t, sp2)).Gap(px(t, 4)).Background(k.Hover).Label(who).Tooltip(i18n.T("Line options")).MinWidth(0).Shrink(1)
		if a.dubOpen == id {
			wb.Background(k.Pressed)
		}
		wb.Children(func() {
			ui.Text(c, who).FontSize(px(t, fsCaption)).SingleLine().Ellipsis("…").MinWidth(0)
			ui.Icon(c, icons.AltArrowDown.Linear).Size(px(t, 12), px(t, 12)).TextColor(k.Text3).Shrink(0)
		})
		if wb.Clicked() {
			if a.dubOpen == id {
				a.dubOpen = ""
			} else {
				a.dubOpen = id
			}
		}
		ui.Row(c).Gap(px(t, 4)).AlignItems(ui.Center).Grow(1).MinWidth(0).ClipX().Children(func() {
			if overlap {
				dubBadge(c, i18n.T("Overlaps the next line"), "danger")
			}
			a.dubDialogueChip(c, s)
			for _, b := range a.dubBadges(s) {
				dubBadge(c, b[0], b[1])
			}
		})
		if a.dubLang != "" {
			busy := a.dubAudition.busy && a.dubAudition.seg == id
			if busy {
				ui.Box(c).Size(px(t, 28), px(t, 28)).Center().Shrink(0).Children(func() { ui.Spinner(c).Label(i18n.T("Hear this line dubbed")) })
			} else if iconButton(c.Key("hear"), icons.HeadphonesRound, i18n.T("Hear this line dubbed")).Disabled(strings.TrimSpace(p.TextFor(s, a.dubLang)) == "").Clicked() {
				*after = func() { a.dubAuditionLine(id) }
			}
		}
		moreMenu(c.Key("menu"), i18n.T("Line actions"), func(m *ui.Menu) {
			menuDo(m, i18n.T("Split (⌘D)"), locked, func() { *after = func() { a.dubSplit(id, -1) } })
			menuDo(m, i18n.T("Merge with previous (⇧⌘M)"), locked || i == 0, func() { *after = func() { a.dubMerge(id, false) } })
			menuDo(m, i18n.T("Merge with next (⌘M)"), locked || i+1 >= len(p.Segments), func() { *after = func() { a.dubMerge(id, true) } })
			menuDo(m, i18n.T("Insert a line below"), locked, func() { *after = func() { a.dubInsert(id) } })
			a.dubDialogueMenu(m, s, locked, after)
			m.Separator()
			menuDo(m, i18n.T("Delete"), locked, func() { *after = func() { a.dubDelete(id) } })
		})
	})
}

// dubRowNotes are a line's shorter rewrite and translation error.
func (a *app) dubRowNotes(c *ui.Context, s dub.Segment, locked bool, after *func()) {
	t := c.Theme()
	id := s.ID
	line := s.Lines[dub.LangFor(s, a.dubLang)]
	if a.dubLang != "" && line.Shorter != "" {
		ui.Row(c.Key("shorter")).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
			caption(c, i18n.T("Shorter: %s", line.Shorter)).Grow(1).MinWidth(0)
			if ghostButton(c.Key("use-shorter"), i18n.T("Use shorter rewrite")).Disabled(locked).Shrink(0).Clicked() {
				*after = func() { a.dubUseShorter(id) }
			}
		})
	}
	if a.dubLang != "" && line.Err != "" {
		ui.Row(c).Gap(px(t, 6)).Children(func() {
			iconMark(c, icons.DangerCircle, t.Danger, "")
			caption(c, i18n.T("The translation failed (%s).", line.Err)).TextColor(t.Danger).Grow(1).MinWidth(0)
		})
	}
}

// dubRowQC are what the check heard on a line it flagged.
func (a *app) dubRowQC(c *ui.Context, s dub.Segment) {
	t := c.Theme()
	p := a.dub
	id := s.ID
	if qc := p.Track(a.dubLang).QC; qc != nil && a.dubLang != "" && !p.Track(a.dubLang).QCStale() {
		for _, q := range qc.Lines {
			if q.ID == id && q.Flagged {
				ui.Row(c).Gap(px(t, 6)).Children(func() {
					iconMark(c, icons.DangerTriangle, t.Warning, "")
					caption(c, i18n.T("Heard: “%s”", q.Heard)).TextColor(t.Warning).Grow(1).MinWidth(0)
				})
			}
		}
	}
}
