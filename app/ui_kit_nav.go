package main

// Pieces of the kit the 0.5 structure needs (F1: Home, Voices, New
// voice), in the current tokens: underline tabs, the tool tile, the round
// submit of a composer, the entry card, and ghost rows (an empty list's
// placeholder). Taken from #113's kit (ui_kit_*.go there), without its
// colours.

import (
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
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
// a name, a column) shaped as the row kit's (ui_list.go: no lines between
// them), each fainter than the one above, then ic, title and
// body ("" none) under them, as a hint of what the list will hold.
func ghostRows(c *ui.Context, rows int, ic icons.Icon, title, body string) ui.Element {
	t := c.Theme()
	k := tok(t)
	bar := k.G[3]
	return ui.Column(c).AlignItems(ui.Stretch).Shrink(0).Children(func() {
		for i := range rows {
			fade := 1 - float32(i)/float32(rows)
			ui.Row(c.Key(i)).Height(px(t, listRowH)).Gap(px(t, sp3)).AlignItems(ui.Center).Padding(0, px(t, sp2)).
				Radius(px(t, radiusControl)).Opacity(fade).Children(func() {
				ui.Box(c).Size(px(t, listThumbD), px(t, listThumbD)).Radius(px(t, radiusControl)).Background(bar).Shrink(0)
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

// selectorRowFace is the inside of a selector row (Text to speech's
// Voice, 0.5 F3): a portrait (or the name's initial) in a circle, the
// name over sub, and › at the end. The caller's button is the row.
func selectorRowFace(c *ui.Context, name, sub string, img *ui.Bitmap, warn bool) {
	t := c.Theme()
	k := tok(t)
	ui.Avatar(c, name, img).Size(px(t, 28), px(t, 28)).Shrink(0)
	ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 1)).Children(func() {
		tx := ui.Text(c, name).SingleLine().Ellipsis("…").MinWidth(0)
		if warn {
			tx.TextColor(t.Warning)
		}
		if sub != "" {
			ui.Text(c, sub).FontSize(px(t, fsMicro)).TextColor(k.Text2).SingleLine().Ellipsis("…").MinWidth(0)
		}
	})
	ui.Icon(c, icons.AltArrowRight.Linear).Size(px(t, 14), px(t, 14)).TextColor(k.Text2).Shrink(0)
}

// stepper is a guided flow's step indicator (New voice's clone and
// design, 0.5 F4): numbered steps joined by hairlines, the current one in
// ink, those done with a check. A done step is a button back to it when
// back is not nil.
func stepper(c *ui.Context, key string, labels []string, cur int, back func(i int)) {
	t := c.Theme()
	k := tok(t)
	ui.Row(c.Key(key)).Gap(px(t, sp2)).AlignItems(ui.Center).Wrap().Role(ui.RoleGroup).Label(i18n.T("Steps")).Shrink(0).Children(func() {
		for i, label := range labels {
			if i > 0 {
				ui.Box(c).Size(px(t, 24), 1).Background(k.Line).Shrink(0)
			}
			done, on := i < cur, i == cur
			face := func(hover bool) {
				d := px(t, 22)
				dot := ui.Box(c).Size(d, d).Radius(d / 2).Center().Shrink(0)
				col := k.Text2
				switch {
				case on:
					dot.Background(k.Ink)
				case done:
					dot.Background(k.Pressed)
					col = k.Text
				default:
					dot.Border(1, k.Line)
				}
				dot.Children(func() {
					switch {
					case done:
						ui.Icon(c, icons.Check.Linear).Size(px(t, 12), px(t, 12)).TextColor(col)
					case on:
						ui.Text(c, itoa(i+1)).FontSize(px(t, fsMicro)).FontWeight(600).TextColor(k.OnInk)
					default:
						ui.Text(c, itoa(i+1)).FontSize(px(t, fsMicro)).TextColor(col)
					}
				})
				tx := ui.Text(c, label).FontSize(px(t, fsCaption)).SingleLine()
				switch {
				case on:
					tx.FontWeight(600)
				case done && hover:
					tx.TextColor(k.Text).Underline()
				default:
					tx.TextColor(k.Text2)
				}
			}
			if done && back != nil {
				b := ui.ButtonBase(c.Key(i)).Gap(px(t, 6)).AlignItems(ui.Center).Label(i18n.T("Back to “%s”", label))
				b.Children(func() { face(b.Hovered()) })
				if b.Clicked() {
					back(i)
				}
				continue
			}
			row := ui.Row(c.Key(i)).Gap(px(t, 6)).AlignItems(ui.Center)
			if on {
				row.Role(ui.RoleGroup).Label(i18n.T("Step %d of %d: %s", i+1, len(labels), label))
			}
			row.Children(func() { face(false) })
		}
	})
}
