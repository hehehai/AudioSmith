package main

// The 0.5 kit's chips (ui_kit.go): the category chip (36 high, an icon and
// a label inside a hairline: a use case, a kind), the filter chip (small;
// "+ name" until set, then ink "× name | value"), the example chip (a
// sample that fills the text in when clicked), and chip, the plain
// toggle chip of a few choices (presets, styles, speakers).

import (
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// categoryChip is a category to browse by (Narration, Characters…): 36
// high, its icon and label inside a hairline, radius 8; on (the category
// chosen) puts it on the chosen grey with an ink border. Its Checked
// follows on.
func categoryChip(c *ui.Context, ic icons.Icon, label string, on bool) ui.Element {
	t := c.Theme()
	k := tok(t)
	b := ui.ButtonBase(c).Height(px(t, 34)).Padding(0, px(t, sp3)).Gap(px(t, sp2)).Radius(px(t, radiusControl)).
		Border(1, k.Line).Background(k.Field).Label(label).Checked(on).Shrink(0)
	switch {
	case on:
		b.Background(k.Pressed).Border(1, k.Ink)
	case b.Pressed():
		b.Background(k.Pressed)
	case b.Hovered():
		b.Background(k.Hover)
	}
	b.Children(func() {
		if ic.Linear != nil {
			ui.Icon(c, ic.Linear).Size(px(t, 16), px(t, 16)).TextColor(k.Text)
		}
		ui.Text(c, label).SingleLine()
	})
	return b
}

// filterChip is a filter in a row of them (Language, Created by, Status):
// a small chip "+ name" inside a hairline while value is "", and once set
// an ink chip "× name | value", whose × clears it. chip is the part that
// opens the filter's menu (attach it with Menu); cleared reports a click
// on × in this build pass.
//
//	b, cleared := filterChip(c.Key("f-lang"), i18n.T("Language"), cur)
//	b.Menu(func(m *ui.Menu) { … })
//	if cleared { cur = "" }
func filterChip(c *ui.Context, name, value string) (chip ui.Element, cleared bool) {
	t := c.Theme()
	k := tok(t)
	h := px(t, 24)
	if value == "" {
		chip = ui.ButtonBase(c).Height(h).Padding(0, px(t, sp2), 0, px(t, 6)).Gap(px(t, 3)).Radius(px(t, radiusControl)).
			Border(1, k.Line).Background(k.Field).Label(name).Shrink(0)
		col := k.Text2
		switch {
		case chip.Pressed():
			chip.Background(k.Pressed)
		case chip.Hovered():
			chip.Background(k.Hover)
			col = k.Text
		}
		chip.Children(func() {
			ui.Icon(c, icons.Add.Linear).Size(px(t, 12), px(t, 12)).TextColor(col)
			ui.Text(c, name).FontSize(px(t, fsCaption)).TextColor(col).SingleLine()
		})
		return chip, false
	}
	face := k.Ink
	ui.Row(c).Height(h).Radius(px(t, radiusControl)).Background(face).AlignItems(ui.Stretch).Shrink(0).MinWidth(0).Children(func() {
		x := ui.ButtonBase(c.Key("clear")).Width(px(t, 22)).Center().Radius(px(t, radiusControl)).
			Label(i18n.T("Clear %s", name)).Tooltip(i18n.T("Clear %s", name))
		if x.Hovered() {
			x.Background(k.InkHover)
		}
		x.Children(func() {
			ui.Icon(c, icons.Close.Linear).Size(px(t, 12), px(t, 12)).TextColor(k.OnInk)
		})
		cleared = x.Clicked()
		chip = ui.ButtonBase(c.Key("open")).Padding(0, px(t, sp2), 0, px(t, 2)).Gap(px(t, 6)).Radius(px(t, radiusControl)).
			Label(name + ": " + value).Checked(true).MinWidth(0).Shrink(1)
		if chip.Hovered() {
			chip.Background(k.InkHover)
		}
		chip.Children(func() {
			ui.Text(c, name).FontSize(px(t, fsCaption)).TextColor(k.OnInk.Alpha(0.72)).SingleLine().Shrink(0)
			ui.Box(c).Width(1).Height(px(t, 12)).Background(k.OnInk.Alpha(0.3)).Shrink(0)
			ui.Text(c, value).FontSize(px(t, fsCaption)).FontWeight(500).TextColor(k.OnInk).SingleLine().Ellipsis("…").MinWidth(0)
		})
	})
	return chip, cleared
}

// exampleChip is a sample the user can start from (Narrate a story, Tell a
// joke): an optional icon (icons.Icon{} for none) and a label inside a
// hairline; a click should fill the sample in.
//
//	if exampleChip(c.Key("ex-story"), icons.NotebookMinimalistic, i18n.T("Narrate a story")).Clicked() { a.text = sample }
func exampleChip(c *ui.Context, ic icons.Icon, label string) ui.Element {
	t := c.Theme()
	k := tok(t)
	b := ui.ButtonBase(c).Height(px(t, 30)).Padding(0, px(t, 10)).Gap(px(t, 6)).Radius(px(t, radiusControl)).
		Border(1, k.Line).Background(k.Field).Label(label).Shrink(0)
	switch {
	case b.Pressed():
		b.Background(k.Pressed)
	case b.Hovered():
		b.Background(k.Hover)
	}
	b.Children(func() {
		if ic.Linear != nil {
			ui.Icon(c, ic.Linear).Size(px(t, 14), px(t, 14)).TextColor(k.Text2)
		}
		ui.Text(c, label).FontSize(px(t, fsCaption)).SingleLine()
	})
	return b
}

// chip is a toggle chip among a few (a preset, a style, a speaker): its
// label inside a hairline, in ink once on; count ("" for none) follows
// the label in a lighter shade.
func chip(c *ui.Context, label, count string, on bool) ui.Element {
	t := c.Theme()
	k := tok(t)
	name := label
	if count != "" {
		name += " " + count
	}
	b := ui.ButtonBase(c).Padding(px(t, 3), px(t, 10)).Radius(px(t, radiusControl)).Gap(px(t, 6)).AlignItems(ui.Center).
		Border(1, k.Line).Background(k.Field).Label(name).Checked(on).Shrink(0)
	col, sub := k.Text2, k.Text3
	switch {
	case on:
		b.Background(k.Ink).Border(1, k.Ink)
		col, sub = k.OnInk, k.OnInk.Alpha(0.6)
		if b.Hovered() {
			b.Background(k.InkHover)
		}
	case b.Pressed():
		b.Background(k.Pressed)
	case b.Hovered():
		b.Background(k.Hover)
		col = k.Text
	}
	b.Children(func() {
		ui.Text(c, label).FontSize(px(t, fsCaption)).TextColor(col).SingleLine()
		if count != "" {
			ui.Text(c, count).FontSize(px(t, fsMicro)).TextColor(sub).SingleLine()
		}
	})
	return b
}
