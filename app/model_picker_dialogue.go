package main

// The Stories model picker's 「整场对话」 section (thread #84). The dialogue
// models (SoulX-Podcast, MOSS-TTSD: catalogue capability "dialogue",
// hidden from the speech pickers because they speak whole scenes, not one
// line) were reachable only from the Performance section's Whole dialogue
// switch, collapsed by default and named after the feature: people looked
// for them in the model picker and did not find them. The section lists
// them there, with what they need; choosing one turns whole dialogue on
// with it, and the story's own model stays as the narration and fallback
// model (the button says so). Macs with too little memory, and stories in
// other languages, see them greyed out with the reason.

import (
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// pickerDialogue is what a picker needs for the whole-dialogue section.
type pickerDialogue struct {
	Models []catalog.Entry            // the catalogue's dialogue models, the default first
	Why    func(catalog.Entry) string // why one cannot be used here ("" = it can)
	On     bool                       // whole dialogue is on (and can run)
	Cur    catalog.Entry              // the one it uses, or would use
	Pick   func(catalog.Entry)        // turn it on with this one
	Off    func()                     // turn it off: every line on the story's model
	// The section's words where they are not a story's (the dub, thread
	// #89; "": the story's): its heading and ⓘ, the off row's note, the
	// heading of the speech models and what the button says of the
	// picker's own model.
	Head, Tip, OffLabel, OffNote, RestHead string
	RestFor                                func(model string) string
}

// pickerDialogueTexts are a picker's dialogue section's words.
type pickerDialogueTexts struct {
	head, tip, offLabel, offNote, restHead string
	restFor                                func(string) string
}

// texts are the section's words, the story's where dl leaves them.
func (dl *pickerDialogue) texts() pickerDialogueTexts {
	if dl.Head != "" {
		return pickerDialogueTexts{dl.Head, dl.Tip, dl.OffLabel, dl.OffNote, dl.RestHead, dl.RestFor}
	}
	return pickerDialogueTexts{
		head:     i18n.T("Whole dialogue (multi-speaker models)"),
		tip:      i18n.T("A dialogue model speaks the characters' lines a scene at a time, each line hearing the lines before it, so they answer each other. Narration, and lines it does not say clearly, stay on the story's model."),
		offLabel: i18n.T("Turn whole dialogue off"), offNote: i18n.T("Every line on the story's model"),
		restHead: i18n.T("Narration and fallback · Recommended"),
		restFor:  func(m string) string { return i18n.T("Narration and fallback: %s", m) },
	}
}

// pickerDialogueRows builds the section, or with find only the models
// that match it; it returns how many models it showed.
func (a *app) pickerDialogueRows(c *ui.Context, key string, dl *pickerDialogue, find string, done func()) int {
	t := c.Theme()
	k := tok(t)
	var shown []catalog.Entry
	for _, e := range dl.Models {
		if e.Matches(find) {
			shown = append(shown, e)
		}
	}
	if len(shown) == 0 {
		return 0
	}
	tx := dl.texts()
	ui.Row(c.Key(key+"-dialogue-head")).Gap(px(t, 6)).AlignItems(ui.Center).Padding(px(t, 4), px(t, sp2), px(t, 2)).Children(func() {
		ui.Text(c, tx.head).FontSize(px(t, fsMicro)).FontWeight(600).TextColor(k.Text3).Shrink(1).MinWidth(0)
		infoTip(c.Key(key+"-dialogue-tip"), tx.tip)
	})
	var cur catalog.Entry
	if dl.On {
		cur = dl.Cur
	}
	for _, e := range shown {
		why := dl.Why(e)
		if !a.modelAvailable(e) {
			why = a.modelFitNote(e)
		}
		facts := i18n.T("Needs %g GB of memory", e.MinMemoryGB) + " · " + e.Label
		if why != "" {
			facts = e.Label // the reason says what it needs
		}
		a.pickerRowX(c, e, cur.ID != "" && e.ID == cur.ID, e.Description(), why, facts, why == "", func(e catalog.Entry) {
			done()
			if !dl.On || e.ID != dl.Cur.ID {
				dl.Pick(e)
			}
		})
	}
	if dl.On && find == "" {
		label := tx.offLabel
		b := ui.ButtonBase(c.Key(key+"-dialogue-off")).Padding(px(t, 6), px(t, sp2)).Radius(px(t, radiusControl)).Gap(px(t, sp2)).AlignItems(ui.Center).Label(label)
		if b.Hovered() {
			b.Background(k.Hover)
		}
		if b.Clicked() {
			done()
			dl.Off()
		}
		b.Children(func() {
			ui.Icon(c, icons.CloseCircle.Linear).Size(px(t, 14), px(t, 14)).TextColor(k.Text2).Shrink(0)
			ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 1)).Children(func() {
				ui.Text(c, label).FontSize(px(t, fsCaption)).TextColor(k.Text2).SingleLine().Ellipsis("…").MinWidth(0)
				ui.Text(c, tx.offNote).FontSize(px(t, fsMicro)).TextColor(k.Text3).SingleLine().Ellipsis("…").MinWidth(0)
			})
		})
	}
	return len(shown)
}
