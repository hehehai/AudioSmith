package main

// The samples to start from (0.5 F5; Projects' top row and Home's
// Samples tab): a story for two voices and an audiobook chapter make a
// project of the sample script and open it; the product narration opens
// Text to speech with its script; the dub and the meeting transcript
// come with F6 (their cards say 即将推出).

import (
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// projTemplate is a sample card.
type projTemplate struct {
	key, title, about string
	icon              icons.Icon
	soon              bool // its sample comes later
}

// projTemplates are the samples, in order.
func projTemplates() []projTemplate {
	return []projTemplate{
		{"story", i18n.T("A story for two voices"), i18n.T("A short scene, cast and performed"), storiesIcon, false},
		{"chapter", i18n.T("An audiobook chapter"), i18n.T("A chapter narrated with its headings"), audiobookIcon, false},
		{"narration", i18n.T("A product narration"), i18n.T("Thirty seconds of voice-over"), studioIcon, false},
		{"dub", i18n.T("A dubbed clip"), i18n.T("A short video dubbed into Chinese"), dubIcon, true},
		{"meeting", i18n.T("A meeting transcript"), i18n.T("Who said what, with timings"), transcribeIcon, true},
	}
}

// useTemplate starts from a sample: makes its project and opens it.
func (a *app) useTemplate(key string) {
	switch key {
	case "story":
		p := a.lfStories
		a.lfCreate(p, func() { a.loadSampleStory(p) })
	case "chapter":
		p := a.lfBook
		a.lfCreate(p, func() { a.loadSample(p) })
	case "narration":
		for _, ex := range studioExamples() {
			if ex.key == "product" {
				a.navigate("/studio")
				a.fillExample(ex)
			}
		}
	default:
		a.toast(i18n.T("The sample projects come with the next update."))
	}
}

// templateCards are the samples as a row of cards that wraps.
func (a *app) templateCards(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	ui.Row(c.Key("pj-templates")).Wrap().GapX(px(t, sp3)).GapY(px(t, sp3)).Children(func() {
		for _, s := range projTemplates() {
			b := ui.ButtonBase(c.Key("sample-"+s.key)).Column().Gap(px(t, sp2)).Padding(px(t, sp3)).Radius(px(t, radiusCard)).Background(k.Subtle).
				Grow(1).Basis(px(t, 160)).MinWidth(px(t, 140)).Label(s.title)
			if b.Hovered() {
				b.Background(k.Hover)
			}
			b.Children(func() {
				ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, s.icon.Linear).Size(px(t, 18), px(t, 18)).TextColor(k.Text2)
					ui.Spacer(c)
					if s.soon {
						ui.Text(c, i18n.T("Coming soon")).FontSize(px(t, fsMicro)).TextColor(k.Text3).Padding(px(t, 1), px(t, 6)).Radius(px(t, 8)).Border(1, k.Line).SingleLine()
					}
				})
				ui.Text(c, s.title).FontWeight(600).MaxLines(2).Ellipsis("…")
				ui.Text(c, s.about).FontSize(px(t, fsCaption)).TextColor(k.Text2).MaxLines(2).Ellipsis("…")
			})
			if b.Clicked() {
				a.useTemplate(s.key)
			}
		}
	})
}
