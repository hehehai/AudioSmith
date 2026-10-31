package main

// Pieces of the kit the 0.5 structure needs (F1: Home, Voices, New
// voice), in the current tokens: underline tabs, the tool tile, the round
// submit of a composer, the entry card, and ghost rows (an empty list's
// placeholder). Taken from #113's kit (ui_kit_*.go there), without its
// colours.

import (
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// underlineTabs is a row of tabs, of which *selected is the chosen one:
// the chosen tab in the text colour, semibold, over a 2 px ink underline;
// the others in the secondary grey; a hairline runs under the row. A
// click, the arrows, Home and End choose (MyGo's TabsBase); Changed
// reports a new choice. Name it with Label.
func underlineTabs(c *ui.Context, selected *int, labels ...string) ui.Element {
	t := c.Theme()
	k := tok(t)
	tabs := ui.TabsBase(c, selected, len(labels))
	list := tabs.List.Gap(px(t, sp5)).AlignItems(ui.Stretch).Shrink(0).BorderWidth(0, 0, 1, 0).BorderColor(k.Line)
	list.Children(func() {
		for i, label := range labels {
			on := i == *selected
			tab := tabs.Tab(i).Padding(px(t, 7), px(t, 2), px(t, 9)).Label(label)
			col := k.Text2
			if on || tab.Hovered() {
				col = k.Text
			}
			tab.DrawOver(func(p *ui.Painter, r ui.Rect) {
				if on {
					h := px(t, 2)
					p.Fill(ui.Rect{X: r.X, Y: r.Y + r.H - h, W: r.W, H: h}, k.Ink, 0)
				}
			})
			tab.Children(func() {
				tx := ui.Text(c, label).SingleLine().TextColor(col)
				if on {
					tx.FontWeight(600)
				}
			})
		}
	})
	return list
}

// toolTile is a tool to open (Speech, Clone, Dub…): its icon in a 44 px
// outlined square, and one word under it. Act on Clicked.
func toolTile(c *ui.Context, ic icons.Icon, label string) ui.Element {
	t := c.Theme()
	k := tok(t)
	b := ui.ButtonBase(c).Column().Gap(px(t, 6)).AlignItems(ui.Center).Width(px(t, 64)).Label(label).Shrink(0)
	b.Children(func() {
		sq := ui.Box(c).Size(px(t, 44), px(t, 44)).Radius(px(t, radiusCard)).Border(1, k.Line).Background(k.Bg).Center()
		switch {
		case b.Pressed():
			sq.Background(k.Pressed)
		case b.Hovered():
			sq.Background(k.Hover)
		}
		sq.Children(func() {
			ui.Icon(c, ic.Linear).Size(px(t, 20), px(t, 20)).TextColor(k.Text)
		})
		ui.Text(c, label).FontSize(px(t, fsCaption)).TextColor(k.Text2).SingleLine().Ellipsis("…").MaxWidth(px(t, 64))
	})
	return b
}

// submitButton is the round submit button at the right end of a composer
// (an arrow up in a circle, 32 across): grey and disabled until ready,
// then ink. tip names it.
func submitButton(c *ui.Context, tip string, ready bool) ui.Element {
	t := c.Theme()
	k := tok(t)
	d := px(t, 32)
	b := ui.ButtonBase(c).Size(d, d).Radius(d / 2).Center().Label(tip).Tooltip(tip).Disabled(!ready).Shrink(0)
	face, fg := k.G[3], k.Text3
	if ready {
		face, fg = k.Ink, k.OnInk
		switch {
		case b.Pressed():
			face = k.InkPressed
		case b.Hovered():
			face = k.InkHover
		}
	}
	b.Background(face)
	b.Children(func() {
		ui.Icon(c, icons.ArrowUp.Linear).Size(px(t, 16), px(t, 16)).TextColor(fg)
	})
	return b
}

// entryCard is a way in to a tool or a flow (Quick clone, Design a
// voice): a card on the subtle fill, ic in a square, title over body, then
// action (its button) at its foot. Give it a width.
func entryCard(c *ui.Context, key string, ic icons.Icon, title, body string, action func()) ui.Element {
	t := c.Theme()
	k := tok(t)
	return ui.Column(c.Key(key)).Padding(px(t, sp5)).Gap(px(t, sp3)).Radius(px(t, radiusCard)).Background(k.Subtle).Children(func() {
		ui.Box(c).Size(px(t, 40), px(t, 40)).Radius(px(t, radiusControl)).Background(k.Bg).Border(1, k.Line).Center().Children(func() {
			ui.Icon(c, ic.Linear).Size(px(t, 20), px(t, 20)).TextColor(k.Text)
		})
		ui.Column(c).Gap(px(t, 4)).Grow(1).Children(func() {
			ui.Text(c, title).FontSize(px(t, fsSection)).FontWeight(600)
			ui.Text(c, body).FontSize(px(t, fsCaption)).TextColor(k.Text2)
		})
		ui.Row(c).Children(action)
	})
}

// ghostRows is the empty state of a list: rows of grey bars (an avatar,
// a name, a column), each fainter than the one above, then ic, title and
// body ("" none) under them, as a hint of what the list will hold.
func ghostRows(c *ui.Context, rows int, ic icons.Icon, title, body string) ui.Element {
	t := c.Theme()
	k := tok(t)
	bar := k.G[3]
	return ui.Column(c).AlignItems(ui.Stretch).Shrink(0).Children(func() {
		for i := range rows {
			fade := 1 - float32(i)/float32(rows)
			ui.Row(c.Key(i)).Height(px(t, 48)).Gap(px(t, sp3)).AlignItems(ui.Center).Padding(0, px(t, sp2)).
				BorderWidth(0, 0, 1, 0).BorderColor(k.Line).Opacity(fade).Children(func() {
				ui.Box(c).Size(px(t, 28), px(t, 28)).Radius(px(t, radiusControl)).Background(bar).Shrink(0)
				ui.Column(c).Gap(px(t, 6)).Grow(1).Children(func() {
					ui.Box(c).Height(px(t, 8)).Width(px(t, float32(150-20*(i%3)))).Radius(px(t, 4)).Background(bar)
					ui.Box(c).Height(px(t, 6)).Width(px(t, float32(90+25*(i%2)))).Radius(px(t, 3)).Background(bar)
				})
				ui.Box(c).Height(px(t, 8)).Width(px(t, 56)).Radius(px(t, 4)).Background(bar).Shrink(0)
			})
		}
		ui.Column(c).Gap(px(t, 6)).AlignItems(ui.Center).Padding(px(t, sp4), px(t, sp6)).Children(func() {
			ui.Icon(c, ic.Linear).Size(px(t, 22), px(t, 22)).TextColor(k.Text3)
			ui.Text(c, title).FontWeight(600).TextAlign(ui.Center)
			if body != "" {
				ui.Text(c, body).FontSize(px(t, fsCaption)).TextColor(k.Text2).TextAlign(ui.Center).MaxWidth(px(t, 420))
			}
		})
	})
}
