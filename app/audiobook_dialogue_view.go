package main

// The Audiobook inspector's 「对话段」 section (audiobook_dialogue.go),
// shown while the manuscript has passages: whether the dialogue model
// speaks them, each passage with its preview, the dialogue model when
// more than one can run here, the speakers without a voice ([自动分配]
// [去选]), and what happens without the model (not downloaded, too
// little memory): each line read in its own voice.

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/services"
)

// bookDialogueCard is the section's body.
func (a *app) bookDialogueCard(c *ui.Context, p *lfPage, locked bool) {
	t := c.Theme()
	k := tok(t)
	d := &p.doc
	e, choices, blocked := dialogueChoice(a.macMemoryGB(), d)
	on := d.Production.Dialogue && blocked == ""
	title := i18n.T("Dialogue model")
	formRow(c, "ab-dialogue-row", title, i18n.T("%s speaks each passage's lines together, each line hearing the lines before it, so they answer each other; the rest of the book stays on its model. Voices in a passage are cloned by the dialogue model and sound a little different from the same voice outside it. Slower than real time.", modelRef(e)), func() {
		if inkSwitch(c.Key("ab-dialogue"), &on).Label(title).Disabled(locked || blocked != "").Changed() {
			d.Production.Dialogue = on
			if on {
				d.Production.DialogueModel = e.Repo
			}
		}
	})
	if on && len(choices) > 1 {
		fieldInfo(c, i18n.T("Which dialogue model"), i18n.T("%s is light (16 GB of memory) and quick; %s is larger (24 GB), up to five voices, and speaks each passage as one recording.", modelRef(choices[0]), modelRef(choices[1])), func() {
			names := make([]string, len(choices))
			for i, x := range choices {
				names[i] = x.DisplayName()
			}
			cur := e.DisplayName()
			if ui.Select(c.Key("ab-dialogue-model"), &cur, names).Label(i18n.T("Which dialogue model")).Tooltip(modelTip(e)).Disabled(locked).Changed() {
				if i := slices.Index(names, cur); i >= 0 {
					e = choices[i]
					d.Production.DialogueModel = e.Repo
				}
			}
		})
	}
	a.bookDialogueFallback(c, p, e, on, blocked, locked)
	a.bookUncast(c, p, locked)
	_, ready := a.bookDialogueModel(p)
	ui.Column(c.Key("ab-blocks")).Gap(px(t, 2)).Children(func() {
		for _, b := range p.blocks {
			what := blockWhat(b)
			chTitle := ""
			if b.Chapter < len(p.spec.Chapters) {
				chTitle = p.spec.Chapters[b.Chapter].Title
			}
			if chTitle == "" {
				chTitle = i18n.T("Chapter %d", b.Chapter+1)
			}
			who := strings.Join(b.Speakers, i18n.X("list separator", ", "))
			if b.Unknown > 0 {
				who = strings.TrimPrefix(who+i18n.X("list separator", ", ")+"？", i18n.X("list separator", ", "))
			}
			ui.Row(c.Key(what)).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Children(func() {
					truncText(c, chTitle+" · "+who)
					ui.Text(c, blockSummary(b, e.DisplayName(), ready)+blockWhy(b)).FontSize(px(t, fsMicro)).TextColor(k.Text3).SingleLine().Ellipsis("…").MinWidth(0)
				})
				a.auditionButton(c, p, what, i18n.T("Preview the dialogue: %s", chTitle))
			})
		}
	})
}

// blockWhy is why a passage is read line by line ("" when it is not).
func blockWhy(b services.DialogueBlockInfo) string {
	switch {
	case !b.Language:
		return " · " + i18n.T("the dialogue models speak Chinese and English")
	case len(b.Speakers) == 1 && b.Unknown == 0:
		return " · " + i18n.T("one speaker")
	}
	return ""
}

// bookDialogueFallback says what happens without the dialogue model, and
// offers its download.
func (a *app) bookDialogueFallback(c *ui.Context, p *lfPage, e catalog.Entry, on bool, blocked string, locked bool) {
	t := c.Theme()
	k := tok(t)
	lineByLine := i18n.T("Without it, each line is read in its own voice.")
	switch m := a.model(e); {
	case blocked != "":
		caption(c.Key("ab-dialogue-why"), blocked+" "+lineByLine).TextColor(k.Text3)
	case on && m.known && m.local.State != hub.Downloaded:
		ui.Column(c.Key("ab-dialogue-need")).Gap(px(t, 4)).Children(func() {
			ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Wrap().Children(func() {
				caption(c, i18n.T("%s is not downloaded (%s).", modelRef(e), catalog.HumanSize(e.Size))).TextColor(k.Text2).Shrink(1)
				if m.downloading {
					ui.Spinner(c)
				} else if secondaryIconButton(c.Key("ab-dialogue-dl"), icons.DownloadMinimalistic, i18n.T("Download")).Label(i18n.T("Download")).Disabled(locked).Clicked() {
					a.download(e)
				}
			})
			caption(c, i18n.T("You can generate without it: each line is read in its own voice.")).TextColor(k.Text3)
		})
	}
}

// bookUncast warns of a passage's speakers without a voice: 自动分配
// gives each one of its own, 去选 opens the cast.
func (a *app) bookUncast(c *ui.Context, p *lfPage, locked bool) {
	names := uncastNames(p.blocks)
	if len(names) == 0 {
		return
	}
	t := c.Theme()
	ui.Column(c.Key("ab-uncast")).Gap(px(t, 6)).Children(func() {
		ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Start).Children(func() {
			iconMark(c, icons.DangerTriangle, t.Warning, "")
			caption(c, i18n.N(len(names), "%[2]s has no voice yet: given one of their own when the book is generated.",
				"%[2]s have no voices yet: given ones of their own when the book is generated.", len(names), strings.Join(names, i18n.X("list separator", ", ")))).Grow(1).MinWidth(0)
		})
		ui.Row(c).Gap(px(t, sp2)).Padding(0, 0, 0, px(t, 22)).Children(func() {
			if secondaryButton(c.Key("ab-uncast-auto"), i18n.T("Assign voices")).Disabled(locked).Clicked() {
				a.autoCastBook(p)
			}
			if ghostButton(c.Key("ab-uncast-pick"), i18n.T("Choose…")).Disabled(locked).Clicked() {
				p.open["cast"] = true
			}
		})
	})
}
