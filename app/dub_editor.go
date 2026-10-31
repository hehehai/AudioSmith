package main

// The Dub page's transcript editor: a toolbar (language, select all,
// undo/redo, retry failed, paste, glossary, more), the bulk bar, and the
// lines in a virtualised list — time, speaker and voice, the source
// dimmed, the line in the language shown (click to edit; ⌘↩ or leaving
// it keeps it, Esc drops it), badges (Error, Tight, Won't fit, Overflows,
// Trimmed, Verify, Changed), audition, and each line's options.

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/translate"
)

// dubToolbar is the editor's toolbar: the count, the language shown,
// select all, undo / redo, Retry failed, Verify, and ⋯ (paste, the
// glossary, and the editing tools).
func (a *app) dubToolbar(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	p := a.dub
	locked := a.dubLocked()
	ui.Row(c.Key("dub-toolbar")).Gap(px(t, sp1)).AlignItems(ui.Center).Shrink(0).KeepFocus().Children(func() {
		all := len(p.Segments) > 0 && len(a.dubSelected()) == len(p.Segments)
		if ui.Checkbox(c.Key("dub-all"), &all, "").Label(i18n.T("All")).Tooltip(i18n.T("Select all lines")).Changed() {
			a.dubSel = map[string]bool{}
			if all {
				for _, s := range p.Segments {
					a.dubSel[s.ID] = true
				}
			}
		}
		ui.Text(c, i18n.N(len(p.Segments), "%d line", "%d lines", len(p.Segments))).FontSize(px(t, fsCaption)).TextColor(k.Text2).SingleLine().Shrink(0).Margin(0, px(t, sp1))
		langs := p.Languages()
		if len(langs) > 0 {
			names := []string{i18n.T("Source")}
			ids := []string{""}
			for _, l := range langs {
				done, total := p.Translated(l)
				names = append(names, fmt.Sprintf("%s %d/%d", dubLangName(l), done, total))
				ids = append(ids, l)
			}
			cur := names[max(slices.Index(ids, a.dubLang), 0)]
			if ui.Select(c.Key("dub-showlang"), &cur, names).Label(i18n.T("Language shown")).Tooltip(i18n.T("Language shown")).Width(px(t, 180)).Shrink(1).MinWidth(px(t, 120)).Changed() {
				a.dubCommitEdit()
				a.dubLang = ids[slices.Index(names, cur)]
				a.dubPaste.parsed = ""
			}
		}
		if iconButton(c.Key("dub-undo"), icons.UndoLeft, i18n.T("Undo")).Tooltip(i18n.T("Undo (⌘Z)")).Disabled(locked || !a.dubHist.CanUndo()).Clicked() {
			a.dubUndo()
		}
		if iconButton(c.Key("dub-redo"), icons.UndoRight, i18n.T("Redo")).Tooltip(i18n.T("Redo (⇧⌘Z)")).Disabled(locked || !a.dubHist.CanRedo()).Clicked() {
			a.dubRedo()
		}
		ui.Spacer(c)
		if n := a.dubFailedCount(a.dubLang); n > 0 && a.dubLang != "" {
			if labelOrIcon(c.Key("dub-retry"), icons.Restart, i18n.N(n, "Retry %d failed", "Retry %d failed", n), a.dubNarrow).
				Disabled(a.dubBusy()).Shrink(0).Clicked() {
				a.dubTranslate(false)
			}
		}
		if a.dubLang != "" && p.Track(a.dubLang).File != "" {
			label := i18n.T("Verify")
			if qc := p.Track(a.dubLang).QC; qc != nil && !p.Track(a.dubLang).QCStale() {
				label = i18n.N(qc.Flagged, "Verify · %d to re-listen", "Verify · %d to re-listen", qc.Flagged)
			}
			var b ui.Element
			if a.dubNarrow {
				b = iconButton(c.Key("dub-verify"), icons.Pulse2, label)
			} else {
				b = ghostIconButton(c.Key("dub-verify"), icons.Pulse2, label).Label(label).Shrink(0)
			}
			if b.Tooltip(i18n.T("Listen to the dub again with speech to text, and flag lines that drift")).Disabled(a.dubBusy()).Clicked() {
				a.dubVerify()
			}
		}
		moreMenu(c.Key("dub-more"), i18n.T("More editing tools"), func(m *ui.Menu) {
			if a.dubLang != "" {
				menuDo(m, i18n.T("Paste translation"), locked, func() { a.dubPaste.open = !a.dubPaste.open })
			}
			menuDo(m, i18n.T("Glossary (%d)", len(p.Glossary)), false, func() {
				a.dubGloss.open = !a.dubGloss.open
				a.dubGloss.editing = -1
			})
			m.Separator()
			menuDo(m, i18n.T("Clean up fragments"), locked, a.dubCleanup)
			menuDo(m, i18n.T("Mirror source delivery"), locked || p.Media.Audio == "", a.dubMirror)
			menuDo(m, i18n.T("Import subtitles as the transcript…"), locked || a.dubBusy(), a.dubImportSubtitles)
			menuDo(m, i18n.T("Transcribe again…"), a.dubBusy(), func() { a.dubConfirm, a.dubConfirmOpen = "transcribe", true })
			menuDo(m, i18n.T("Translate everything again"), locked || a.dubBusy(), func() { a.dubTranslate(true) })
			m.Separator()
			menuDo(m, i18n.T("Restore the source (drop translations)"), locked, a.dubRestore)
			if a.dubLang != "" {
				menuDo(m, i18n.T("Skip failed lines"), locked || a.dubFailedCount(a.dubLang) == 0, a.dubSkipFailed)
			}
		})
	})
	if a.dubNotice != "" {
		notice(c, "dub-notice", noticeInfo, a.dubNotice, "", nil, func() { a.dubNotice = "" })
	}
	if n := len(a.dubSelected()); n > 0 {
		a.dubBulkBar(c, n)
	}
}

// dubBulkBar acts on the lines ticked.
func (a *app) dubBulkBar(c *ui.Context, n int) {
	t := c.Theme()
	k := tok(t)
	locked := a.dubLocked()
	ui.Row(c.Key("dub-bulk")).Gap(px(t, sp1)).AlignItems(ui.Center).Padding(px(t, 4), px(t, 4), px(t, 4), px(t, sp3)).Radius(px(t, radiusControl)).
		Background(k.Subtle).Shrink(0).Children(func() {
		ui.Text(c, i18n.N(n, "%d selected", "%d selected", n)).FontWeight(600).SingleLine().Shrink(0).Margin(0, px(t, sp2), 0, 0)
		dropButton(c.Key("bulk-voice"), icons.UserSpeakRounded, i18n.T("Set voice…")).Menu(func(m *ui.Menu) {
			for _, v := range a.dubVoiceChoices() {
				ref := v.ref
				label := v.label
				if v.group != "" {
					label = v.group + " · " + v.label
				}
				menuDo(m, label, locked, func() { a.dubBulkVoice(ref) })
			}
		})
		dropButton(c.Key("bulk-lang"), icons.Global, i18n.T("Set language…")).Menu(func(m *ui.Menu) {
			menuDo(m, i18n.T("The track's (default)"), locked, func() { a.dubBulkLang("") })
			if src := a.dub.SourceLanguage(); src != "" {
				menuDo(m, i18n.T("Keep the source language (%s)", dubLangName(src)), locked, func() { a.dubBulkLang(src) })
			}
			for _, l := range a.dub.Targets {
				menuDo(m, dubLangName(l), locked, func() { a.dubBulkLang(l) })
			}
		})
		if iconButton(c.Key("bulk-delete"), icons.TrashBinMinimalistic, i18n.T("Delete")).Disabled(locked).Clicked() {
			a.dubBulkDelete()
		}
		ui.Spacer(c)
		if iconButton(c.Key("bulk-clear"), icons.Close, i18n.T("Clear selection")).Clicked() {
			a.dubSel = map[string]bool{}
		}
	})
}

// dubLines is the list of lines.
func (a *app) dubLines(c *ui.Context) {
	t := c.Theme()
	p := a.dub
	a.dubList2.Key = func(i int) any {
		if a.dub != nil && i < len(a.dub.Segments) {
			return a.dub.Segments[i].ID
		}
		return i
	}
	var after func()
	a.dubDlgNow = a.dubDialogue()
	// Basis(0): the list takes the room left in the workspace (220 at
	// least), not the height of all its lines, so it scrolls itself and
	// builds only the lines in view; the workspace's scroll is for short
	// windows only (review #102: 2000 lines took 190 ms a frame). It runs
	// into the workspace's bottom padding (the negative margin), where the
	// next line showed cut off when the workspace scrolled the lines.
	ui.List(c.Key("dub-lines"), &a.dubList2, len(p.Segments)).Basis(0).Grow(1).MinHeight(px(t, 220)).Margin(0, 0, -px(t, sp2), 0).Gap(px(t, 2)).ItemKey(a.dubList2.Key).Rows(func(r ui.ListRow) {
		if r.Index >= len(a.dub.Segments) {
			return
		}
		if f := a.dubRow(c, r.Index); f != nil {
			after = f
		}
	})
	if after != nil {
		after()
	}
}

// dubBadges are a line's badges in the language shown: label and colour.
func (a *app) dubBadges(s dub.Segment) [][2]string {
	p := a.dub
	l := a.dubLang
	var out [][2]string
	if l == "" {
		return nil
	}
	line := s.Lines[dub.LangFor(s, l)]
	switch {
	case line.Skipped:
		out = append(out, [2]string{i18n.T("Skipped"), "muted"})
	case line.Err != "":
		out = append(out, [2]string{i18n.T("Error"), "danger"})
	}
	if s.Lang != "" {
		out = append(out, [2]string{dubLangName(s.Lang), "muted"})
	}
	t := p.Track(l)
	r, rendered := t.Lines[s.ID]
	switch {
	case rendered && r.Err != "":
		out = append(out, [2]string{i18n.T("Failed"), "danger"})
	case rendered && r.Verdict == dub.VerdictTrimmed:
		out = append(out, [2]string{i18n.T("Trimmed %.1fs", r.Over), "danger"})
	case rendered && (r.Verdict == dub.VerdictOverflow || r.Verdict == dub.VerdictVideo):
		out = append(out, [2]string{i18n.T("Overflows +%.1fs", r.Over), "warning"})
	case rendered && r.Verdict == dub.VerdictTight:
		out = append(out, [2]string{i18n.T("Tight fit"), "warning"})
	case !rendered && line.Fit != nil && line.Fit.Verdict == translate.FitOverflow:
		out = append(out, [2]string{i18n.T("Won't fit +%.1fs", line.Fit.Over), "warning"})
	case !rendered && line.Fit != nil && line.Fit.Verdict == translate.FitTight:
		out = append(out, [2]string{i18n.T("Tight fit"), "warning"})
	}
	if rendered && strings.TrimSpace(p.TextFor(s, l)) != "" && (r.Text != p.TextFor(s, l) || r.Start != s.Start) {
		out = append(out, [2]string{i18n.T("Changed"), "accent"})
	}
	if rendered && r.Flag != "" && r.Dialogue == 0 {
		// The synthesize stage's check (thread #90); a conversation's
		// line has the badge beside its chip.
		out = append(out, [2]string{i18n.T("Listen"), "warning"})
	}
	if qc := t.QC; qc != nil && !t.QCStale() {
		for _, q := range qc.Lines {
			if q.ID == s.ID && q.Flagged {
				out = append(out, [2]string{i18n.T("Verify"), "warning"})
			}
		}
	}
	return out
}

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
	overlap := i+1 < len(p.Segments) && s.End > p.Segments[i+1].Start+0.01
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
				after = func() { a.dubPlayRange(id) }
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
					after = func() { a.dubAuditionLine(id) }
				}
			}
			moreMenu(c.Key("menu"), i18n.T("Line actions"), func(m *ui.Menu) {
				menuDo(m, i18n.T("Split (⌘D)"), locked, func() { after = func() { a.dubSplit(id, -1) } })
				menuDo(m, i18n.T("Merge with previous (⇧⌘M)"), locked || i == 0, func() { after = func() { a.dubMerge(id, false) } })
				menuDo(m, i18n.T("Merge with next (⌘M)"), locked || i+1 >= len(p.Segments), func() { after = func() { a.dubMerge(id, true) } })
				menuDo(m, i18n.T("Insert a line below"), locked, func() { after = func() { a.dubInsert(id) } })
				a.dubDialogueMenu(m, s, locked, &after)
				m.Separator()
				menuDo(m, i18n.T("Delete"), locked, func() { after = func() { a.dubDelete(id) } })
			})
		})
		// The source, dimmed, when a translation shows.
		translated := a.dubLang != "" && strings.TrimSpace(p.TextFor(s, a.dubLang)) != ""
		if translated && !(a.dubEdit == id && a.dubEditL == "") {
			src := ui.Text(c, s.Text).FontSize(px(t, fsCaption)).TextColor(k.Text2)
			if !locked && src.Clicked() {
				after = func() { a.dubBeginEdit(id, "") }
			}
		}
		a.dubLineText(c, s, locked, &after)
		line := s.Lines[dub.LangFor(s, a.dubLang)]
		if a.dubLang != "" && line.Shorter != "" {
			ui.Row(c.Key("shorter")).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
				caption(c, i18n.T("Shorter: %s", line.Shorter)).Grow(1).MinWidth(0)
				if ghostButton(c.Key("use-shorter"), i18n.T("Use shorter rewrite")).Disabled(locked).Shrink(0).Clicked() {
					after = func() { a.dubUseShorter(id) }
				}
			})
		}
		if a.dubLang != "" && line.Err != "" {
			ui.Row(c).Gap(px(t, 6)).Children(func() {
				iconMark(c, icons.DangerCircle, t.Danger, "")
				caption(c, i18n.T("The translation failed (%s).", line.Err)).TextColor(t.Danger).Grow(1).MinWidth(0)
			})
		}
		a.dubDialogueFlag(c, s)
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
		if a.dubOpen == id {
			a.dubLineOptions(c, s, locked, &after)
		}
	})
	return after
}

// dubBadge is a line's badge: a small grey chip, its text coloured by
// what it says (danger, warning, accent; muted is grey).
func dubBadge(c *ui.Context, text, tone string) {
	t := c.Theme()
	k := tok(t)
	b := tagChip(c, text)
	switch tone {
	case "danger":
		b.TextColor(k.Danger)
	case "warning":
		b.TextColor(k.Warning)
	case "accent":
		b.TextColor(k.AccentInk)
	}
}

// dubLineText is the line's text in the language shown, or its editor.
func (a *app) dubLineText(c *ui.Context, s dub.Segment, locked bool, after *func()) {
	t := c.Theme()
	id := s.ID
	if a.dubEdit == id {
		// Done or not, the keyboard goes back to the editor (its undo).
		commit := func() { a.dubCommitEdit(); a.dubEdFocus.Focus() }
		cancel := func() { a.dubCancelEdit(); a.dubEdFocus.Focus() }
		box := ui.TextArea(c.Key("edit"), &a.dubBuf).Lines(1, 6).AutoFocus().Label(i18n.T("Line text"))
		if box.Shortcut(ui.Cmd, ui.KeyEnter) {
			*after = commit
		}
		if box.Shortcut(0, ui.KeyEscape) {
			*after = cancel
		}
		if box.Shortcut(ui.Cmd, ui.KeyD) {
			*after = func() { a.dubSplit(id, -1) }
		}
		ui.Row(c).Gap(px(t, 6)).Children(func() {
			if secondaryButton(c.Key("edit-ok"), i18n.T("Done")).Tooltip("⌘↩").Clicked() {
				*after = commit
			}
			if ghostButton(c.Key("edit-cancel"), i18n.T("Cancel")).Tooltip("Esc").Clicked() {
				*after = cancel
			}
		})
		return
	}
	text := s.Text
	missing := false
	if a.dubLang != "" {
		if tr := a.dub.TextFor(s, a.dubLang); strings.TrimSpace(tr) != "" {
			text = tr
		} else {
			missing = true // the source shows until it is translated
		}
	}
	var label ui.Element
	if strings.TrimSpace(text) == "" {
		label = ui.Text(c, i18n.T("(no text: click to write it)")).TextColor(tok(t).Text3)
	} else {
		label = ui.Text(c, text)
	}
	label.Grow(1).Cursor(ui.CursorText)
	if missing && strings.TrimSpace(text) != "" {
		ui.Text(c, i18n.T("Not translated yet: click to write the translation")).FontSize(px(t, fsMicro)).TextColor(tok(t).Text3)
	}
	if !locked && label.Clicked() {
		l := a.dubLang
		*after = func() { a.dubBeginEdit(id, l) }
	}
}

// dubLineOptions are a line's voice, volume, speed, direction, timing,
// language and speaker.
func (a *app) dubLineOptions(c *ui.Context, s dub.Segment, locked bool, after *func()) {
	t := c.Theme()
	p := a.dub
	id := s.ID
	ui.Column(c.Key("opts")).Gap(px(t, sp2)).Padding(px(t, sp3)).Radius(px(t, radiusControl)).Background(tok(t).Bg).Children(func() {
		ui.Row(c).Gap(px(t, 8)).AlignItems(ui.Center).Wrap().Children(func() {
			choices := a.dubVoiceChoices()
			choices[0] = dubVoiceChoice{"", i18n.T("The speaker's (%s)", a.dubVoiceLabel(p.Cast[s.Speaker])), ""}
			cur := s.Voice
			if !slices.ContainsFunc(choices, func(ch dubVoiceChoice) bool { return ch.ref == cur }) {
				cur = ""
			}
			ui.Field(c, i18n.T("Voice"), func() {
				if ref, ok := a.voicePicker(c, "dub-line-voice-"+id, cur, a.dubVoiceOptions(choices), voicePickerOpts{label: i18n.T("Voice"), locked: locked}); ok {
					a.dubSetLine(id, "voice", func(s *dub.Segment) { s.Voice = ref })
				}
			})
			speakers := p.SpeakerLabels()
			names := make([]string, len(speakers))
			for i, sp := range speakers {
				names[i] = p.SpeakerName(sp)
			}
			if len(names) > 0 {
				who := p.SpeakerName(s.Speaker)
				ui.Field(c, i18n.T("Speaker"), func() {
					if ui.Select(c.Key("speaker"), &who, names).Label(i18n.T("Speaker")).Disabled(locked).Changed() {
						sp := speakers[slices.Index(names, who)]
						a.dubSetLine(id, "speaker", func(s *dub.Segment) { s.Speaker, s.Parts = sp, nil })
					}
				})
			}
		})
		ui.Row(c).Gap(px(t, 8)).AlignItems(ui.Center).Wrap().Children(func() {
			vol := s.Volume
			ui.Field(c, i18n.T("Volume"), func() {
				if slimStepSlider(c.Key("volume"), &vol, 0, 2, 0.1).Label(i18n.T("Volume")).Width(px(t, 140)).Disabled(locked).Changed() {
					a.dubSetLine(id, "volume", func(s *dub.Segment) { s.Volume = dubRound(vol) })
				}
			})
			caption(c, strconv.Itoa(int(vol*100+0.5))+"%")
			speed := s.Speed
			if speed == 0 {
				speed = 1
			}
			ui.Field(c, i18n.T("Speed"), func() {
				if slimStepSlider(c.Key("speed"), &speed, 0.5, 2, 0.05).Label(i18n.T("Speed")).Width(px(t, 140)).Disabled(locked).Changed() {
					v := dubRound(speed)
					if v == 1 {
						v = 0
					}
					a.dubSetLine(id, "speed", func(s *dub.Segment) { s.Speed = v })
				}
			})
			caption(c, fmt.Sprintf("%.2f×", speed))
		})
		dir := s.Direction
		ui.Field(c, i18n.T("Direction"), func() {
			in := ui.TextInput(c.Key("direction"), &dir).Placeholder(i18n.T("e.g. whispered, excited, slow")).Label(i18n.T("Direction")).Disabled(locked)
			if in.Changed() {
				d := strings.TrimSpace(dir)
				a.dubSetLine(id, "direction", func(s *dub.Segment) { s.Direction = d })
			}
		})
		if !dubCaps(p.Settings.Model).Instruct && s.Direction != "" {
			caption(c, i18n.T("This voice model does not take directions; Prosody Mirror's notes still guide the translation.")).TextColor(tok(t).Text3)
		}
		ui.Row(c).Gap(px(t, 8)).AlignItems(ui.End).Wrap().Children(func() {
			start, end := s.Start, s.End
			ui.Field(c, i18n.T("Start"), func() {
				if numberInput(c.Key("start"), &start, 0, max(p.Source.Duration, s.End), 0.01, numOpts{Label: i18n.T("Start"), Width: px(t, 150), Disabled: locked}) {
					*after = func() { a.dubSetTiming(id, start, s.End, true) }
				}
			})
			ui.Field(c, i18n.T("End"), func() {
				if numberInput(c.Key("end"), &end, 0, max(p.Source.Duration, s.End+10), 0.01, numOpts{Label: i18n.T("End"), Width: px(t, 150), Disabled: locked}) {
					*after = func() { a.dubSetTiming(id, s.Start, end, true) }
				}
			})
			lockT := s.Locked
			if ui.Checkbox(c.Key("lock"), &lockT, i18n.T("Lock timing")).Disabled(locked).Changed() {
				a.dubSetLine(id, "lock", func(s *dub.Segment) { s.Locked = lockT })
			}
		})
	})
}

// dubCommand performs the Edit menu's undo or redo in the editor, and
// reports whether it took the command: the editor's element or its
// timeline has the focus (dubWorkspace), no line's text is being edited.
func (a *app) dubCommand(name string) bool {
	if a.dub == nil || len(a.dub.Segments) == 0 || a.dubEdit != "" {
		return false
	}
	switch name {
	case "undo":
		a.dubUndo()
	case "redo":
		a.dubRedo()
	default:
		return false
	}
	return true
}

// dubShortcuts are the editor's keys. ⌘Z and ⇧⌘Z are here too for a
// window without the Edit menu (tests): where the menu has Undo and
// Redo, it takes the keys first and sends commands (dubCommand), so one
// of the two runs, never both.
func (a *app) dubShortcuts(c *ui.Context) {
	p := a.dub
	if p == nil || len(p.Segments) == 0 {
		return
	}
	if a.dubEdit == "" {
		if c.Shortcut(ui.Cmd, ui.KeyZ) {
			a.dubUndo()
		}
		if c.Shortcut(ui.Cmd|ui.Shift, ui.KeyZ) {
			a.dubRedo()
		}
	}
	if a.dubCur == "" {
		return
	}
	if c.Shortcut(ui.Cmd, ui.KeyD) && a.dubEdit == "" {
		a.dubSplit(a.dubCur, -1)
	}
	if c.Shortcut(ui.Cmd, ui.KeyM) {
		a.dubMerge(a.dubCur, true)
	}
	if c.Shortcut(ui.Cmd|ui.Shift, ui.KeyM) {
		a.dubMerge(a.dubCur, false)
	}
}

// menuDo adds an item that runs fn when chosen.
func menuDo(m *ui.Menu, label string, disabled bool, fn func()) {
	if m.Item(label).Disabled(disabled).Chosen() {
		fn()
	}
}
