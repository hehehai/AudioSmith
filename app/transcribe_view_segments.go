package main

// The Transcribe page's segments (editable sentences, words that seek)
// and the speakers bar; split from transcribe_view.go.

import (
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// transcriptSegments lists the sentences: time (click to play from
// there), speaker, text (click to edit), and the timed words of the
// sentence playing (click one to play from it).
func (a *app) transcriptSegments(c *ui.Context, tr *core.Transcript) {
	t := c.Theme()
	k := tok(t)
	pos := a.trPosition()
	labels := transcriptSpeakers(*tr)
	ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Margin(px(t, sp2), 0, 0, 0).Children(func() {
		sectionHeading(c, i18n.N(len(tr.Segments), "%d segment", "%d segments", len(tr.Segments))).Grow(1)
		if len(labels) == 0 && len(tr.Segments) > 0 {
			ui.Text(c, i18n.T("No speaker labels")).FontSize(px(t, fsMicro)).TextColor(k.Text3).SingleLine().
				Tooltip(i18n.T("Turn on Speakers in the toolbar before transcribing to tell who says what."))
		}
	})
	if len(labels) > 0 {
		a.trSpeakersBar(c, tr, labels)
	}
	if len(tr.Segments) == 0 {
		caption(c, i18n.T("No speech was found."))
		return
	}
	shown := max(a.trShown, trPage)
	// The sentence playing shows, however far it is.
	for i, s := range tr.Segments {
		if pos >= s.Start && pos < s.End && i >= shown {
			shown = (i/trPage + 1) * trPage
		}
	}
	ui.Column(c).Gap(px(t, 2)).Children(func() {
		for i, s := range tr.Segments {
			if i >= shown {
				rest := len(tr.Segments) - shown
				if ghostButton(c.Key("tr-more"), i18n.N(min(rest, trPage), "Show %d more segment", "Show %d more segments", min(rest, trPage))).Clicked() {
					a.trShown = shown + trPage
				}
				break
			}
			playing := pos >= 0 && pos >= s.Start && pos < s.End
			row := ui.Row(c.Key(fmt.Sprintf("tr-seg-%d", i))).Gap(px(t, sp2)).AlignItems(ui.Start).Padding(px(t, 4), px(t, 4)).Radius(px(t, radiusControl))
			if playing {
				row.Background(k.AccentSoft)
			}
			row.Children(func() {
				tb := ui.ButtonBase(c.Key("time")).Padding(px(t, 3), px(t, 6)).Radius(px(t, 6)).Label(clock(s.Start)).Tooltip(i18n.T("Play from here")).Shrink(0)
				if tb.Hovered() {
					tb.Background(k.Hover)
				}
				if tb.Clicked() {
					a.playTranscriptAt(s.Start)
				}
				tb.Children(func() { ui.Text(c, clock(s.Start)).Font(monoFont).FontSize(px(t, fsMicro)).TextColor(k.AccentInk) })
				if len(labels) > 0 {
					// One column for the speakers, so the text lines up.
					ui.Row(c).Width(px(t, 72)).Shrink(0).AlignItems(ui.Start).Children(func() {
						if s.Speaker != "" {
							name := speakerLabel(*tr, s.Speaker)
							tagChip(c, name).Margin(px(t, 2), 0, 0, 0).MaxWidth(px(t, 72)).Ellipsis("…").Tooltip(name)
						}
					})
				}
				ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 6)).Children(func() {
					if a.trEdit == i {
						area := ui.TextArea(c.Key("edit"), &a.trEditBuf).Lines(1, 6).Label(i18n.T("Segment text")).AutoFocus()
						if area.Shortcut(0, ui.KeyEscape) {
							a.trEdit = -1
						}
						if area.Shortcut(ui.Cmd, ui.KeyEnter) {
							a.saveSegmentEdit()
						}
						ui.Row(c).Gap(px(t, 6)).Children(func() {
							if secondaryButton(c.Key("edit-save"), i18n.T("Save")).Tooltip("⌘↩").Clicked() {
								a.saveSegmentEdit()
							}
							if ghostButton(c.Key("edit-cancel"), i18n.T("Cancel")).Tooltip("Esc").Clicked() {
								a.trEdit = -1
							}
						})
						return
					}
					txt := ui.ButtonBase(c.Key("text")).Label(s.Text).Tooltip(i18n.T("Click to edit")).Padding(px(t, 2), px(t, 4)).Radius(px(t, 6)).Gap(px(t, sp2)).Cursor(ui.CursorText)
					if txt.Hovered() && !playing {
						txt.Background(k.Hover)
					}
					if txt.Clicked() {
						a.editSegment(i)
					}
					txt.Children(func() {
						ui.Text(c, s.Text).Grow(1).MinWidth(0)
						if s.Edited {
							tagChip(c, i18n.T("edited"))
						}
					})
					if playing && len(s.Words) > 0 && !s.Edited {
						ui.Row(c.Key("words")).Gap(px(t, 4)).Wrap().Children(func() {
							for wi, w := range s.Words {
								b := ui.ButtonBase(c.Key(wi)).Padding(px(t, 1), px(t, 4)).Radius(px(t, 4)).Label(w.Text)
								if pos >= w.Start && pos < w.End {
									b.Background(t.Selection)
								} else if b.Hovered() {
									b.Background(k.Hover)
								}
								if b.Clicked() {
									a.playTranscriptAt(w.Start)
								}
								b.Children(func() { ui.Text(c, w.Text).FontSize(px(t, fsCaption)) })
							}
						})
					}
				})
			})
		}
	})
	if a.pl.Playing(trPlayID(tr.ID)) {
		c.After(150 * time.Millisecond)
	}
}

// transcriptSpeakers are a transcript's speaker labels in order of
// appearance.
func transcriptSpeakers(t core.Transcript) []string {
	var out []string
	for _, s := range t.Segments {
		if s.Speaker != "" && !slices.Contains(out, s.Speaker) {
			out = append(out, s.Speaker)
		}
	}
	return out
}

// trSpeakersBar lists the transcript's speakers with their lines; a
// click renames one (the name shows on its segments and in exports).
func (a *app) trSpeakersBar(c *ui.Context, tr *core.Transcript, labels []string) {
	t := c.Theme()
	k := tok(t)
	ui.Row(c.Key("tr-speakers-bar")).Gap(px(t, sp1)).AlignItems(ui.Center).Wrap().Children(func() {
		ui.Icon(c, icons.UsersGroupRounded.Linear).Size(px(t, 14), px(t, 14)).TextColor(k.Text3).Margin(0, px(t, 2), 0, 0)
		for _, l := range labels {
			n := 0
			for _, s := range tr.Segments {
				if s.Speaker == l {
					n++
				}
			}
			name := speakerLabel(*tr, l)
			rename := i18n.T("Rename %s", name)
			b := chip(c.Key("tr-spk-"+l), name, strconv.Itoa(n), a.trRename == l && a.trRenOpen).Label(rename).Tooltip(rename)
			if b.Clicked() {
				a.trRename, a.trRenBuf, a.trRenOpen = l, tr.Speakers[l], true
			}
			open := a.trRename == l && a.trRenOpen
			ui.Popover(c, b, &open, func() {
				ui.Column(c.Key("tr-spk-rename")).Width(px(t, 240)).Gap(px(t, sp2)).Padding(px(t, sp2)).Children(func() {
					ui.Text(c, i18n.T("Name of %s", l)).FontSize(px(t, fsCaption)).FontWeight(600)
					in := ui.TextInput(c.Key("tr-spk-name"), &a.trRenBuf).Placeholder(l).Label(i18n.T("Name of %s", l)).AutoFocus()
					if in.Shortcut(0, ui.KeyEnter) {
						a.renameSpeaker(l, a.trRenBuf)
					}
					ui.Row(c).Gap(px(t, 6)).Justify(ui.End).Children(func() {
						if ghostButton(c.Key("tr-spk-cancel"), i18n.T("Cancel")).Clicked() {
							a.trRename, a.trRenOpen = "", false
						}
						if secondaryButton(c.Key("tr-spk-save"), i18n.T("Save")).Clicked() {
							a.renameSpeaker(l, a.trRenBuf)
						}
					})
				})
			})
			if a.trRename == l && a.trRenOpen && !open {
				a.trRenOpen = false // dismissed
			}
		}
	})
}

// speakerLabel is a speaker's name, or its label.
func speakerLabel(t core.Transcript, label string) string {
	if n := t.Speakers[label]; n != "" {
		return n
	}
	return label
}
