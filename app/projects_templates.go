package main

// The sample cards (0.5 F5; Projects' top row and Home's Samples tab). A
// card opens its sample (samples.go, samples_copy.go): a copy made into
// the library, or, until the sample is bundled, the built-in fallback.

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// templateCards are the samples as a row of cards that wraps.
func (a *app) templateCards(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	compact := a.toolCompact(c) // a short or narrow window: the titles only
	ui.Row(c.Key("pj-templates")).Wrap().GapX(px(t, sp3)).GapY(px(t, sp3)).AlignItems(ui.Stretch).Children(func() {
		for _, s := range a.sampleCards() {
			b := ui.ButtonBase(c.Key("sample-" + s.key)).Column().Gap(px(t, sp2)).Padding(px(t, sp3)).Radius(px(t, radiusCard)).Background(k.Subtle).
				AlignItems(ui.Stretch).Justify(ui.Start).Grow(1).Basis(px(t, 160)).MinWidth(px(t, 140)).Label(s.title)
			if b.Hovered() {
				b.Background(k.Hover)
			}
			b.Children(func() {
				ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
					col := k.Text2
					if s.bundled != nil && strings.HasPrefix(s.bundled.Cover.Color, "#") {
						col = ui.Hex(s.bundled.Cover.Color)
					}
					ui.Icon(c, s.icon.Linear).Size(px(t, 18), px(t, 18)).TextColor(col)
					ui.Spacer(c)
					if s.soon {
						ui.Text(c, i18n.T("Coming soon")).FontSize(px(t, fsMicro)).TextColor(k.Text3).Padding(px(t, 1), px(t, 6)).Radius(px(t, 8)).Border(1, k.Line).SingleLine()
					}
				})
				ui.Text(c, s.title).FontWeight(600).MaxLines(2).Ellipsis("…")
				if !compact {
					ui.Text(c, s.about).FontSize(px(t, fsCaption)).TextColor(k.Text2).MaxLines(2).Ellipsis("…")
				}
			})
			if b.Clicked() {
				a.openSample(s.key)
			}
		}
	})
}

// sampleIcon is a sample card's cover icon by its manifest's name.
func sampleIcon(name string) (icons.Icon, bool) {
	switch name {
	case sampleStory:
		return storiesIcon, true
	case sampleAudiobook:
		return audiobookIcon, true
	case sampleNarration:
		return studioIcon, true
	case sampleDub:
		return dubIcon, true
	case sampleTranscriptKind:
		return transcribeIcon, true
	}
	return icons.Icon{}, false
}
