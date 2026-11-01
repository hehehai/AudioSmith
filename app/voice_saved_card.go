package main

import (
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// savedVoiceCard is the done card of Clone and Design: v's portrait and
// name under "Saved to My voices", extra (nil none: Clone's reference
// grade), then the hand-offs — Listen, Use in Text to speech, Voice
// details. key keys the card; the hand-off row is keyed prefix+"-handoffs".
func (a *app) savedVoiceCard(c *ui.Context, key, prefix string, v *core.Voice, extra func()) {
	t := c.Theme()
	k := tok(t)
	id := v.ID
	ui.Column(c.Key(key)).Gap(px(t, sp3)).Padding(px(t, sp5)).Radius(px(t, radiusCard)).Background(k.Subtle).Children(func() {
		ui.Row(c).Gap(px(t, sp3)).AlignItems(ui.Center).Children(func() {
			ui.Avatar(c, v.Name, a.portrait(v)).Size(px(t, 48), px(t, 48)).Shrink(0)
			ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 2)).Children(func() {
				statusMark(c, icons.CheckCircle, t.Success, i18n.T("Saved to My voices"))
				ui.Text(c, v.Name).FontSize(px(t, fsTitle)).FontWeight(600).SingleLine().Ellipsis("…").MinWidth(0)
			})
		})
		if extra != nil {
			extra()
		}
		handoffRow(c, prefix+"-handoffs", func() {
			label, ic := i18n.T("Listen"), icons.Play
			if a.savedPlaying(v) {
				label, ic = i18n.T("Stop"), icons.Pause
			}
			if secondaryIconButton(c.Key("done-play"), ic, label).Label(label).Clicked() {
				a.previewSaved(v)
			}
			if primaryButton(c.Key("done-tts"), i18n.T("Use in Text to speech")).Clicked() {
				if v := a.findVoice(id); v != nil {
					a.useVoiceInStudio(v)
				}
			}
		}, func() {
			if ghostButton(c.Key("done-detail"), i18n.T("Voice details")+" ›").Clicked() {
				a.openVoiceEditor(id)
			}
		})
	})
}
