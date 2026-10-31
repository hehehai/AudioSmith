package main

// The pieces of a tool page (0.5 F2: Dub, Speech to text, Voice
// isolator; after ElevenLabs' tool pages): the page's column, the
// composer card on top (a drop zone over a row of options and the round
// submit), the list under it (search, filter chips, a header and rows
// that open the item, with their ⋯ menu), a file chip, and the back row
// of an item's page. In the current tokens: no new colours.

import (
	"path/filepath"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// toolColumnW is the widest a tool page's column grows, DIPs at 100 %.
const toolColumnW = 960

// toolPage is a tool page's scrolling column: centred, at most
// toolColumnW wide, body in it.
func (a *app) toolPage(c *ui.Context, key string, body func()) {
	t := c.Theme()
	ui.Scroll(c.Key(key)).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Padding(px(t, sp5), px(t, sp6), px(t, sp8)).AlignItems(ui.Center).Children(func() {
			ui.Column(c).FillWidth().MaxWidth(px(t, toolColumnW)).Gap(px(t, sp4)).Children(body)
		})
	}).ClipX()
}

// toolNarrow reports whether a tool page's list drops its secondary
// columns (the window is narrow).
func (a *app) toolNarrow(c *ui.Context) bool {
	return a.pageWidth(c) < px(c.Theme(), 700)
}

// toolComposer is a tool's composer: a card whose top is a drop zone
// (zone builds it; files dropped on the card go to drop) and whose foot
// is a row of options ending in the submit (foot builds it).
func toolComposer(c *ui.Context, key string, drop func([]string), zone, foot func()) ui.Element {
	t := c.Theme()
	k := tok(t)
	box := ui.Column(c.Key(key)).Gap(px(t, sp2)).Padding(px(t, sp2), px(t, sp2), px(t, sp2)).Radius(px(t, radiusCard)).Border(1, k.Line).Background(k.Bg).
		Shadow(0, 2, 10, 0, ui.RGBA(0, 0, 0, 0.05)).Shrink(0)
	if files := box.DroppedFiles(); len(files) > 0 && drop != nil {
		drop(files)
	}
	over := box.FileDragOver()
	if over {
		box.Border(1, k.Accent)
	}
	box.Children(func() {
		z := ui.Column(c.Key("zone")).Gap(px(t, sp2)).Padding(px(t, sp4), px(t, sp4)).Radius(px(t, radiusControl)).Background(k.Subtle).AlignItems(ui.Center).MinHeight(px(t, 120)).Justify(ui.Center)
		if over {
			z.Background(k.AccentSoft)
		}
		z.Children(zone)
		ui.Row(c.Key("foot")).Gap(px(t, sp1)).AlignItems(ui.Center).Padding(0, px(t, 2), 0, px(t, 4)).Children(foot)
	})
	return box
}

// composerPrompt is a drop zone's text: ic, a line and a caption under it.
func composerPrompt(c *ui.Context, ic icons.Icon, title, sub string) {
	t := c.Theme()
	k := tok(t)
	ui.Icon(c, ic.Linear).Size(px(t, 24), px(t, 24)).TextColor(k.Text3)
	ui.Text(c, title).FontWeight(600).TextAlign(ui.Center)
	if sub != "" {
		ui.Text(c, sub).FontSize(px(t, fsCaption)).TextColor(k.Text2).TextAlign(ui.Center)
	}
}

// fileChip is the file a composer holds: its icon and name, and × (it
// calls remove).
func fileChip(c *ui.Context, key string, ic icons.Icon, path string, remove func()) {
	t := c.Theme()
	k := tok(t)
	ui.Row(c.Key(key)).Gap(px(t, sp2)).AlignItems(ui.Center).Padding(px(t, 4), px(t, 4), px(t, 4), px(t, sp2)).Radius(px(t, radiusControl)).
		Background(k.Bg).Border(1, k.Line).MaxWidth(px(t, 520)).Children(func() {
		ui.Icon(c, ic.Linear).Size(px(t, 16), px(t, 16)).TextColor(k.Text2).Shrink(0)
		ui.Text(c, filepath.Base(path)).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1).Tooltip(path)
		if iconButton(c.Key(key+"-x"), icons.Close, i18n.T("Remove the file")).Clicked() {
			remove()
		}
	})
}

// listColumn is a column of a tool's list: its title and width (DIPs at
// 100 %; 0 grows); wide columns hide when the list is narrow.
type listColumn struct {
	title string
	w     float32
	wide  bool
}

// listHead is the header of a tool's list: the columns' titles over a
// hairline, room for the rows' icon and ⋯.
func listHead(c *ui.Context, narrow bool, cols ...listColumn) {
	t := c.Theme()
	k := tok(t)
	ui.Row(c.Key("list-head")).Height(px(t, 32)).Gap(px(t, sp3)).AlignItems(ui.Center).Padding(0, px(t, sp2)).
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
		ui.Box(c).Width(px(t, 28)).Shrink(0)
	})
}

// listRow is a row of a tool's list: cells (the icon, the title and the
// columns, as listCell lays them out) in a button that opens the item,
// then actions (its ⋯ menu). It reports a click on the row.
func listRow(c *ui.Context, key, label string, cells, actions func()) bool {
	t := c.Theme()
	k := tok(t)
	clicked := false
	row := ui.Row(c.Key(key)).MinHeight(px(t, 52)).Gap(px(t, sp1)).AlignItems(ui.Center).Padding(0, px(t, sp1), 0, 0).
		BorderWidth(0, 0, 1, 0).BorderColor(k.Line).Shrink(0)
	if row.Hovered() {
		row.Background(k.Hover)
	}
	row.Children(func() {
		b := ui.ButtonBase(c.Key("open")).Grow(1).MinWidth(0).Gap(px(t, sp3)).AlignItems(ui.Center).Padding(px(t, 6), px(t, sp2)).Label(label)
		b.Children(cells)
		clicked = b.Clicked()
		if actions != nil {
			actions()
		}
	})
	return clicked
}

// listThumb is a row's leading square: bmp when there is one, else ic.
func listThumb(c *ui.Context, ic icons.Icon, bmp *ui.Bitmap) {
	t := c.Theme()
	k := tok(t)
	d := px(t, 32)
	ui.Box(c).Size(d, d).Radius(px(t, radiusControl)).Background(k.Subtle).Center().Clip().Shrink(0).Children(func() {
		if bmp != nil {
			ui.Image(c, bmp).Size(d, d).Fit(ui.Cover)
			return
		}
		ui.Icon(c, ic.Linear).Size(px(t, 16), px(t, 16)).TextColor(k.Text2)
	})
}

// listTitle is a row's title over a grey line (sub, "" none).
func listTitle(c *ui.Context, title, sub string) {
	t := c.Theme()
	k := tok(t)
	ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 2)).Children(func() {
		ui.Text(c, orDash(title)).SingleLine().Ellipsis("…").MinWidth(0)
		if sub != "" {
			ui.Text(c, sub).FontSize(px(t, fsCaption)).TextColor(k.Text3).SingleLine().Ellipsis("…").MinWidth(0)
		}
	})
}

// listCell is a row's column: s in w DIPs, secondary grey.
func listCell(c *ui.Context, s string, w float32) {
	t := c.Theme()
	ui.Text(c, s).FontSize(px(t, fsCaption)).TextColor(tok(t).Text2).SingleLine().Ellipsis("…").Width(px(t, w)).Shrink(0)
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

// backRow is the row over an item's page: "← name" back to its list,
// then trailing (the item's actions) on the right.
func backRow(c *ui.Context, key, name string, back, trailing func()) {
	t := c.Theme()
	k := tok(t)
	ui.Row(c.Key(key)).Height(px(t, 44)).Shrink(0).AlignItems(ui.Center).Padding(0, px(t, sp4), 0, px(t, sp4)).Gap(px(t, sp2)).Children(func() {
		b := ghostBase(c.Key("back")).Gap(px(t, 4)).AlignItems(ui.Center).Padding(px(t, 4), px(t, sp2), px(t, 4), px(t, 6)).Label(i18n.T("Back to %s", name)).Shrink(0)
		b.Children(func() {
			ui.Icon(c, icons.AltArrowLeft.Linear).Size(px(t, 14), px(t, 14)).TextColor(k.Text2)
			ui.Text(c, name).TextColor(k.Text2).SingleLine()
		})
		if b.Clicked() {
			back()
		}
		ui.Box(c).Grow(1).MinWidth(0)
		if trailing != nil {
			trailing()
		}
	})
}
