package main

// The row kit every list of the app uses (thread #124; after Voices ·
// Discover's 「全部」 and ElevenLabs' voice library): no column titles and
// no lines between rows; a row is a rounded band (radiusControl) tinted
// under the pointer (Hover), while pressed (Pressed), when it is the
// open one (Selected) or while it plays (AccentSoft). Its body is a button
// that opens the item — the leading picture, the title (semibold) over a
// grey line, then grey meta values, each after a small icon — and its end
// holds the actions (▶, the primary action, ⋯). Delete or Backspace on a
// focused row asks to delete it where the list deletes. The meta values
// hide when the list is narrow; the actions stay. In the current tokens.

import (
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// Row sizes, DIPs at 100 %: a row's least height, a dense row's (the
// narrow side panels), and the leading picture's side.
const (
	listRowH      = 52
	listRowDenseH = 44
	listThumbD    = 32
)

// rowState is how a row shows: selected (its detail is open), playing
// (its audio plays).
type rowState struct {
	selected, playing bool
}

// listRowSpec describes a row of the kit.
type listRowSpec struct {
	key   string   // its key among the list's rows
	label string   // the body's accessible name
	tip   string   // the body's tooltip ("" none)
	state rowState // selected, playing
	trail float32  // the actions' width, DIPs (0: as wide as they are); one value per list keeps the meta values in columns
	dense bool     // a side panel's row: listRowDenseH high
	lead  func()   // before the body, outside it (a ▶ that is not the row's click); nil none
	del   func()   // Delete / Backspace on the focused row (asks first); nil none
}

// listRow is a row: lead, then cells (listThumb or an avatar, listTitle,
// metaCell…) in a button that opens the item, then actions. It reports
// a click on the body.
func listRow(c *ui.Context, s listRowSpec, cells, actions func()) bool {
	t := c.Theme()
	k := tok(t)
	h := float32(listRowH)
	if s.dense {
		h = listRowDenseH
	}
	clicked := false
	row := ui.Row(c.Key(s.key)).MinHeight(px(t, h)).Gap(px(t, sp1)).AlignItems(ui.Center).Padding(0, px(t, sp1), 0, 0).
		Radius(px(t, radiusControl)).Shrink(0)
	if s.lead != nil {
		row.Padding(0, px(t, sp1), 0, px(t, sp2))
	}
	var body ui.Element
	row.Children(func() {
		if s.lead != nil {
			s.lead()
		}
		body = ui.ButtonBase(c.Key("open")).Grow(1).MinWidth(0).Gap(px(t, sp3)).AlignItems(ui.Center).Padding(px(t, 6), px(t, sp2)).
			Radius(px(t, radiusControl)).Label(s.label)
		if s.tip != "" {
			body.Tooltip(s.tip)
		}
		body.Children(cells)
		clicked = body.Clicked()
		if s.del != nil && (body.Shortcut(0, ui.KeyDelete) || body.Shortcut(0, ui.KeyBackspace)) {
			s.del()
		}
		if actions == nil {
			return
		}
		acts := ui.Row(c.Key("acts")).Shrink(0).Gap(px(t, 2)).AlignItems(ui.Center).Justify(ui.End)
		if s.trail > 0 {
			acts.Width(px(t, s.trail))
		}
		acts.Children(actions)
	})
	switch {
	case s.state.selected && row.Hovered():
		row.Background(k.SelectedHover)
	case s.state.selected:
		row.Background(k.Selected)
	case s.state.playing:
		row.Background(k.AccentSoft)
	case body.Pressed():
		row.Background(k.Pressed)
	case row.Hovered():
		row.Background(k.Hover)
	}
	return clicked
}

// listThumb is a row's leading square: bmp when there is one, else ic.
func listThumb(c *ui.Context, ic icons.Icon, bmp *ui.Bitmap) {
	t := c.Theme()
	k := tok(t)
	d := px(t, listThumbD)
	ui.Box(c).Size(d, d).Radius(px(t, radiusControl)).Background(k.Subtle).Center().Clip().Shrink(0).Children(func() {
		if bmp != nil {
			ui.Image(c, bmp).Size(d, d).Fit(ui.Cover)
			return
		}
		ui.Icon(c, ic.Linear).Size(px(t, 16), px(t, 16)).TextColor(k.Text2)
	})
}

// listTitle is a row's title (semibold) over a grey line (sub, "" none).
func listTitle(c *ui.Context, title, sub string) {
	listTitleMarks(c, title, sub, false, nil)
}

// listTitleMarks is listTitle with marks after the title (icons whose
// tooltips say what they mean; nil none) and, while playing, the title
// in the accent and 「播放中」 after it.
func listTitleMarks(c *ui.Context, title, sub string, playing bool, marks func()) {
	t := c.Theme()
	k := tok(t)
	ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 2)).Children(func() {
		ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).MinWidth(0).Children(func() {
			tx := ui.Text(c, orDash(title)).FontWeight(600).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
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
			ui.Text(c, sub).FontSize(px(t, fsCaption)).TextColor(k.Text2).SingleLine().Ellipsis("…").MinWidth(0).Tooltip(sub)
		}
	})
}

// noIcon is a meta value without an icon (a language: its flag leads).
var noIcon icons.Icon

// metaCell is a row's meta value: a small grey ic (noIcon none) and s,
// w DIPs wide, cut with …; its tooltip names it (label, as the column
// title did: "语言：中文"; "" s alone). An empty value leaves the room
// blank (no icon, no dash).
func metaCell(c *ui.Context, ic icons.Icon, label, s string, w float32) ui.Element {
	return metaCellExtra(c, ic, label, s, w, nil)
}

// metaCellExtra is metaCell with extra after the text (a countPill).
func metaCellExtra(c *ui.Context, ic icons.Icon, label, s string, w float32, extra func()) ui.Element {
	t := c.Theme()
	k := tok(t)
	cell := ui.Row(c).Width(px(t, w)).Shrink(0).Gap(px(t, 5)).AlignItems(ui.Center).MinWidth(0)
	if s == "" {
		return cell
	}
	tip := s
	if label != "" {
		tip = i18n.T("%s: %s", label, s)
	}
	return cell.Tooltip(tip).Children(func() {
		if ic.Linear != nil {
			ui.Icon(c, ic.Linear).Size(px(t, 13), px(t, 13)).TextColor(k.Text3).Shrink(0)
		}
		ui.Text(c, s).FontSize(px(t, fsCaption)).TextColor(k.Text2).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
		if extra != nil {
			extra()
		}
	})
}

// countPill is a muted "+n": how many more values a meta cell has.
func countPill(c *ui.Context, n int) {
	if n <= 0 {
		return
	}
	t := c.Theme()
	k := tok(t)
	ui.Box(c).Padding(px(t, 1), px(t, 5)).Radius(px(t, 999)).Background(k.G[2]).Shrink(0).Children(func() {
		ui.Text(c, "+"+itoa(n)).FontSize(px(t, fsMicro)).TextColor(k.Text2).SingleLine()
	})
}

// listStatus is a row's status column: a spinner and a bar while it runs
// (frac < 0: no bar), else s in col.
func listStatus(c *ui.Context, s string, running bool, frac float64, col ui.Color, w float32) {
	t := c.Theme()
	ui.Column(c).Width(px(t, w)).Shrink(0).Gap(px(t, 4)).Children(func() {
		ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).MinWidth(0).Children(func() {
			if running {
				ui.Spinner(c).Size(px(t, 12), px(t, 12))
			}
			ui.Text(c, s).FontSize(px(t, fsCaption)).TextColor(col).SingleLine().Ellipsis("…").MinWidth(0).Tooltip(s)
		})
		if running && frac >= 0 {
			ui.Progress(c, frac).Height(px(t, 3)).Label(s)
		}
	})
}

// listEditRow is a row being edited in place (a rename): the leading
// picture and the editor (a field and its buttons) in a row of the
// list's height on the subtle fill.
func listEditRow(c *ui.Context, key string, lead, edit func()) {
	t := c.Theme()
	ui.Row(c.Key(key)).MinHeight(px(t, listRowH)).Gap(px(t, sp2)).AlignItems(ui.Center).Padding(0, px(t, sp2)).
		Radius(px(t, radiusControl)).Background(tok(t).Subtle).Shrink(0).Children(func() {
		lead()
		edit()
	})
}

// listNoMatch is a list's line when its search or filters let nothing
// through.
func listNoMatch(c *ui.Context, s string) {
	t := c.Theme()
	ui.Column(c).Padding(px(t, sp5)).AlignItems(ui.Center).Children(func() { caption(c, s) })
}
