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

// templateCards are the samples as cards in cols columns of one width.
func (a *app) templateCards(c *ui.Context, cols int) {
	t := c.Theme()
	k := tok(t)
	compact := a.toolCompact(c) // a short or narrow window: the titles only
	cardGrid(c, "pj-templates", cols, func() {
		for _, s := range a.sampleCards() {
			b := ui.ButtonBase(c.Key("sample-" + s.key)).Column().Gap(px(t, sp2)).Padding(px(t, sp3)).Radius(px(t, radiusCard)).Background(k.Subtle).
				AlignItems(ui.Stretch).Justify(ui.Start).MinWidth(0).Label(s.title)
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

// cardMin is a card's narrowest width in a card grid, DIPs at 100 %.
const cardMin = 160

// cardCols is how many cards of at least cardMin fit avail (a page's
// content width): 1 to 5. Projects' samples and project cards use the
// same count, so their columns line up.
func cardCols(t *ui.Theme, avail float32) int {
	gap := px(t, sp3)
	return max(1, min(5, int((avail+gap)/(px(t, cardMin)+gap))))
}

// cardGrid lays cards out in cols columns of one width: a short last row
// keeps the others' width (user, 11-02: cards must all be as wide).
func cardGrid(c *ui.Context, key string, cols int, body func()) {
	t := c.Theme()
	ui.Grid(c.Key(key)).Columns(max(1, cols)).GapX(px(t, sp3)).GapY(px(t, sp3)).Children(body)
}
