package main

// Text to speech's right panel (0.5 F3, after ElevenLabs): two underline
// tabs, 设置 | 历史. 设置 is the voice and the model as selector rows,
// the speed, the language and autoplay as rows, and 「高级」 at the foot
// (studio_inspector.go); 历史 is this page's takes (studio_history.go).
// Beside the script when the window has room, else a drawer over it that
// the toolbar's toggle opens.

import (
	"strconv"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// studioPanelWidth is the right panel's width at 100 %.
const studioPanelWidth = 340

// studioPanelState is the panel's view state.
type studioPanelState struct {
	tab int // 0 设置, 1 历史
	// recommend: the voice picker was opened by 「✦ 推荐」; regen: by
	// 「换个音色」, so the voice picked makes the take again.
	recommend, regen bool
	// use is the use case of the example the script was filled with
	// (studio_examples.go), until the script is emptied.
	use string
	// scriptFill is the script scroll's height last frame (studio_view.go).
	scriptFill float32
}

const (
	studioTabSettings = iota
	studioTabHistory
)

// studioPanel is the panel: the tabs, then the chosen tab's content in a
// scroll. drawer adds a close button (the panel over the script).
func (a *app) studioPanel(c *ui.Context, drawer bool) {
	t := c.Theme()
	ui.Row(c).Padding(px(t, sp3), px(t, sp5), 0).Gap(px(t, sp2)).AlignItems(ui.Center).Shrink(0).Children(func() {
		// A segmented control, not underlined tabs (user, 10-31).
		ui.Segmented(c.Key("studio-tabs"), &a.ttsUI.tab, i18n.T("Settings"), i18n.T("History")).Label(i18n.T("Text to speech panel")).Shrink(0)
		ui.Box(c).Grow(1)
		if drawer {
			if iconButton(c.Key("panel-close"), icons.Close, i18n.T("Close")).Clicked() {
				a.composer.panel, a.composer.panelSet = false, true
			}
		}
	})
	ui.Scroll(c.Key("studio-panel-" + []string{"settings", "history"}[a.ttsUI.tab])).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Padding(px(t, sp4), px(t, sp5), px(t, sp5)).Gap(px(t, sp4)).Children(func() {
			if a.ttsUI.tab == studioTabHistory {
				a.studioHistory(c)
				return
			}
			a.inspector(c)
		})
	})
}

// showStudioPanel shows the panel at tab: beside the script, or as the
// drawer when the window is narrow.
func (a *app) showStudioPanel(tab int) {
	a.ttsUI.tab = tab
	a.composer.panel, a.composer.panelSet = true, true
}

// openRecommend opens the voice picker with voices for the script first.
func (a *app) openRecommend() {
	a.showStudioPanel(studioTabSettings)
	a.ttsUI.recommend = true
	a.voicePickerOpen(studioVoiceKey, true)
}

// revoice is 「换个音色再生成」: take t's whole recipe back into the form
// (none: the form as it is), then the voice picker, where the voice
// picked makes the take again.
func (a *app) revoice(t *core.Take) {
	if t != nil {
		a.reuse(t)
	}
	a.showStudioPanel(studioTabSettings)
	a.ttsUI.regen, a.ttsUI.recommend = true, true
	a.voicePickerOpen(studioVoiceKey, true)
}

// voiceHeader is the Voice section's title row: 音色, and 「✦ 推荐」 on the
// right.
func (a *app) voiceHeader(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
		ui.Text(c, i18n.T("Voice")).FontWeight(600).Grow(1)
		label := i18n.T("Recommend")
		b := ghostBase(c.Key("recommend")).Padding(px(t, 2), px(t, 6)).Gap(px(t, 4)).AlignItems(ui.Center).Label(label).
			Tooltip(i18n.T("Voices for the script's language and use"))
		b.Children(func() {
			col := k.Text2
			if b.Hovered() {
				col = k.Text
			}
			ui.Icon(c, icons.Stars.Linear).Size(px(t, 13), px(t, 13)).TextColor(col)
			ui.Text(c, label).FontSize(px(t, fsCaption)).TextColor(col).SingleLine()
		})
		if b.Clicked() {
			a.openRecommend()
		}
	})
}

// speedField is the speed: its name and value, the slider, and its ends.
func (a *app) speedField(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	ui.Column(c.Key("speed-field")).Gap(px(t, 4)).Children(func() {
		ui.Row(c).Children(func() {
			ui.Text(c, i18n.T("Speed")).FontWeight(600).Grow(1)
			ui.Text(c, strconv.FormatFloat(a.speed, 'f', 2, 64)+"×").FontSize(px(t, fsCaption)).TextColor(k.Text2)
		})
		snapped(ui.Slider(c.Key("speed"), &a.speed, 0.5, 2).Label(i18n.T("Speed")), &a.speed, 0.05)
		ui.Row(c).Children(func() {
			ui.Text(c, i18n.X("speed", "Slower")).FontSize(px(t, fsMicro)).TextColor(k.Text3).Grow(1)
			ui.Text(c, i18n.X("speed", "Faster")).FontSize(px(t, fsMicro)).TextColor(k.Text3)
		})
	})
}

// settingsRows are the rows under the speed: the script's language (when
// the model takes one) and autoplay.
func (a *app) settingsRows(c *ui.Context, p catalog.Entry) {
	if p.Has(catalog.CapLanguage) || p.ID == "custom" {
		formRow(c, "language-row", i18n.T("Language"), i18n.T("The language of the script; Auto-detect reads it from the text."), func() {
			a.languagePicker(c)
		})
	}
	formRow(c, "autoplay-row", i18n.T("Play when ready"), i18n.T("Play each take when it is ready"), func() {
		on := a.autoplay
		if ui.Switch(c.Key("autoplay"), &on).Label(i18n.T("Play when ready")).Changed() {
			a.autoplay = on
			a.updateSettings(func(s *settings.Settings) { s.Studio.Autoplay = on })
		}
	})
}
