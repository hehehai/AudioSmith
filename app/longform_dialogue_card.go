package main

// The dialogue-model controls Stories (「整场对话」) and Audiobook
// (「对话段」) share: the switch, the model select when more than one
// dialogue model can run here, and the download row.

import (
	"slices"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/services"
)

// dialogueCardText is a page's wording of the dialogue-model controls.
type dialogueCardText struct {
	title     string                          // the switch's row and label
	about     func(e catalog.Entry) string    // the switch's info
	pick      string                          // the model select's row and label
	pickAbout func(a, b catalog.Entry) string // the select's info, of the two models
}

// dialogueModelCard is the dialogue switch (keys <key>-dialogue-row,
// <key>-dialogue) and, when it is on and more than one dialogue model can
// run, the model select (<key>-dialogue-model). It returns the dialogue
// model, whether it is on, and why it cannot run here ("" when it can).
func (a *app) dialogueModelCard(c *ui.Context, d *services.LongformDoc, key string, txt dialogueCardText, locked bool) (catalog.Entry, bool, string) {
	e, choices, blocked := dialogueChoice(a.macMemoryGB(), d)
	on := d.Production.Dialogue && blocked == ""
	formRow(c, key+"-dialogue-row", txt.title, txt.about(e), func() {
		if ui.Switch(c.Key(key+"-dialogue"), &on).Label(txt.title).Disabled(locked || blocked != "").Changed() {
			d.Production.Dialogue = on
			if on {
				d.Production.DialogueModel = e.Repo
			}
		}
	})
	if on && len(choices) > 1 {
		// By their friendly names, as the model picker (thread #84).
		fieldInfo(c, txt.pick, txt.pickAbout(choices[0], choices[1]), func() {
			names := make([]string, len(choices))
			for i, x := range choices {
				names[i] = x.DisplayName()
			}
			cur := e.DisplayName()
			if ui.Select(c.Key(key+"-dialogue-model"), &cur, names).Label(txt.pick).Tooltip(modelTip(e)).Disabled(locked).Changed() {
				if i := slices.Index(names, cur); i >= 0 {
					e = choices[i]
					d.Production.DialogueModel = e.Repo
				}
			}
		})
	}
	return e, on, blocked
}

// dialogueDownloadRow says the dialogue model e is not downloaded, with
// its Download button (key <key>-dialogue-dl) or a spinner; row is the
// row's context (keyed or not).
func (a *app) dialogueDownloadRow(c, row *ui.Context, key string, e catalog.Entry, downloading, locked bool) {
	t := c.Theme()
	k := tok(t)
	ui.Row(row).Gap(px(t, sp2)).AlignItems(ui.Center).Wrap().Children(func() {
		caption(c, i18n.T("%s is not downloaded (%s).", modelRef(e), catalog.HumanSize(e.Size))).TextColor(k.Text2).Shrink(1)
		if downloading {
			ui.Spinner(c)
		} else if secondaryIconButton(c.Key(key+"-dialogue-dl"), icons.DownloadMinimalistic, i18n.T("Download")).Label(i18n.T("Download")).Disabled(locked).Clicked() {
			a.download(e)
		}
	})
}
