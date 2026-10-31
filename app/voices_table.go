package main

// The voice tables of 0.5 F4 (Discover's 全部, Mine; after ElevenLabs'
// voice library): a header of column titles, rows whose body opens the
// voice's detail and whose end holds ▶, 「使用」 and ⋯. A row playing
// its preview is tinted and its ▶ is a stop; the row whose detail is open
// is selected. In the current tokens.

import (
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// voiceCol is a column of a voice table: its title and width (DIPs at
// 100 %; 0 grows); wide columns hide in a narrow table.
type voiceCol struct {
	title string
	w     float32
	wide  bool
}

// voiceTableTrail is the room the rows' actions take (▶, 「使用」, ⋯),
// DIPs at 100 %.
const voiceTableTrail = 148

// voiceTableHead is a voice table's header: the columns' titles over a
// hairline.
func voiceTableHead(c *ui.Context, key string, narrow bool, cols ...voiceCol) {
	t := c.Theme()
	k := tok(t)
	ui.Row(c.Key(key)).Height(px(t, 30)).Gap(px(t, sp3)).AlignItems(ui.Center).Padding(0, px(t, sp2)).
		BorderWidth(0, 0, 1, 0).BorderColor(k.Line).Shrink(0).Children(func() {
		for i, col := range cols {
			if col.wide && narrow {
				continue
			}
			tx := ui.Text(c.Key(i), col.title).FontSize(px(t, fsCaption)).TextColor(k.Text3).SingleLine().Ellipsis("…")
			if col.w == 0 {
				tx.Grow(1).MinWidth(0).Margin(0, 0, 0, px(t, 32+sp3))
			} else {
				tx.Width(px(t, col.w)).Shrink(0)
			}
		}
		ui.Box(c).Width(px(t, voiceTableTrail)).Shrink(0)
	})
}

// voiceRowState is how a voice row shows: selected (its detail is open),
// playing (its preview plays).
type voiceRowState struct {
	selected, playing bool
}

// voiceRow is a row of a voice table: cells (avatar, name, columns) in a
// button that opens the voice's detail, then actions. It reports a click
// on the row's body.
func voiceRow(c *ui.Context, key, label string, st voiceRowState, cells, actions func()) bool {
	t := c.Theme()
	k := tok(t)
	clicked := false
	row := ui.Row(c.Key(key)).MinHeight(px(t, 52)).Gap(px(t, sp1)).AlignItems(ui.Center).Padding(0, px(t, sp1), 0, 0).
		Radius(px(t, radiusControl)).BorderWidth(0, 0, 1, 0).BorderColor(k.Line).Shrink(0)
	switch {
	case st.selected:
		row.Background(k.Selected)
	case st.playing:
		row.Background(k.AccentSoft)
	case row.Hovered():
		row.Background(k.Hover)
	}
	row.Children(func() {
		b := ui.ButtonBase(c.Key("open")).Grow(1).MinWidth(0).Gap(px(t, sp3)).AlignItems(ui.Center).Padding(px(t, 6), px(t, sp2)).
			Label(label).Tooltip(i18n.T("Open the voice's details"))
		b.Children(cells)
		clicked = b.Clicked()
		ui.Row(c.Key("acts")).Width(px(t, voiceTableTrail)).Shrink(0).Gap(px(t, 2)).AlignItems(ui.Center).Justify(ui.End).Children(actions)
	})
	return clicked
}

// voiceNameCell is a row's avatar (portrait or initial) and its name
// over a grey line; the name in the accent while it plays.
func voiceNameCell(c *ui.Context, name, sub string, bmp *ui.Bitmap, playing bool, marks func()) {
	t := c.Theme()
	k := tok(t)
	ui.Avatar(c, name, bmp).Size(px(t, 32), px(t, 32)).Shrink(0)
	ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 2)).Children(func() {
		ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).MinWidth(0).Children(func() {
			tx := ui.Text(c, name).FontWeight(600).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
			if playing {
				tx.TextColor(k.AccentInk)
			}
			if marks != nil {
				marks()
			}
			if playing {
				ui.Text(c, i18n.T("Playing")).FontSize(px(t, fsMicro)).TextColor(k.AccentInk).SingleLine().Shrink(0)
			}
		})
		if sub != "" {
			ui.Text(c, sub).FontSize(px(t, fsCaption)).TextColor(k.Text2).SingleLine().Ellipsis("…").MinWidth(0)
		}
	})
}

// voiceCell is a row's column: s in w DIPs, secondary grey.
func voiceCell(c *ui.Context, s string, w float32) {
	t := c.Theme()
	ui.Text(c, orDash(s)).FontSize(px(t, fsCaption)).TextColor(tok(t).Text2).SingleLine().Ellipsis("…").Width(px(t, w)).Shrink(0).Tooltip(s)
}

// previewToggle is a row's ▶ (the pause glyph, labelled Stop, while it
// plays).
func previewToggle(c *ui.Context, key string, playing bool) ui.Element {
	if playing {
		return iconButton(c.Key(key), icons.Pause, i18n.T("Stop"))
	}
	return iconButton(c.Key(key), icons.Play, i18n.T("Preview"))
}

// useButton is a row's 「使用」: a small outlined button.
func useButton(c *ui.Context, key, tip string) ui.Element {
	t := c.Theme()
	return ghostButton(c.Key(key), i18n.X("voice row", "Use")).FontSize(px(t, fsCaption)).Tooltip(tip).Shrink(0)
}

// sectionRow is a section's title in a voice page (「推荐」, 「全部」),
// with an optional count after it.
func sectionRow(c *ui.Context, key, title, count string) {
	t := c.Theme()
	k := tok(t)
	ui.Row(c.Key(key)).Gap(px(t, sp2)).AlignItems(ui.Center).Padding(px(t, sp2), 0, 0).Shrink(0).Children(func() {
		ui.Text(c, title).FontSize(px(t, fsSection)).FontWeight(600).Role(ui.RoleHeading)
		if count != "" {
			ui.Text(c, count).FontSize(px(t, fsCaption)).TextColor(k.Text3)
		}
	})
}
