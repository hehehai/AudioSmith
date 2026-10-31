package main

// The pieces of a tool page (0.5 F2: Dub, Speech to text, Voice
// isolator; after ElevenLabs' tool pages): the page's column, the
// composer card on top (a drop zone over a row of options and the round
// submit) and a file chip; the list under it is the row kit's
// (ui_list.go). An item's page
// has the page header's ‹ and its list's name (ui_page_header.go). In
// the current tokens: no new colours.

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
		z := ui.Column(c.Key("zone")).Gap(px(t, sp2)).Padding(px(t, sp3), px(t, sp4)).Radius(px(t, radiusControl)).Background(k.Subtle).AlignItems(ui.Center).MinHeight(px(t, 88)).Justify(ui.Center)
		if over {
			z.Background(k.AccentSoft)
		}
		z.Children(zone)
		ui.Row(c.Key("foot")).Gap(px(t, sp1)).AlignItems(ui.Center).Padding(0, px(t, 2), 0, px(t, 4)).Children(foot)
	})
	return box
}

// toolCompact reports whether a tool page is short of room (narrow or
// low): its composer drops the icon and the caption of its drop zone.
func (a *app) toolCompact(c *ui.Context) bool {
	_, h := c.Size()
	return a.toolNarrow(c) || h < px(c.Theme(), 640)
}

// composerPrompt is a drop zone's text: ic, a line and a caption under
// it (the line alone when compact).
func composerPrompt(c *ui.Context, ic icons.Icon, title, sub string, compact bool) {
	t := c.Theme()
	k := tok(t)
	if compact {
		ui.Text(c, title).FontWeight(600).TextAlign(ui.Center)
		return
	}
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
