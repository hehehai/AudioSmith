package main

// The voice tables of 0.5 F4 (Discover's 全部, Mine; after ElevenLabs'
// voice library), on the row kit (ui_list.go): rows whose body opens the
// voice's detail and whose end holds ▶, 「使用」 and ⋯ (each with a face
// under the pointer, thread #129). A row playing
// its preview is tinted and its ▶ is a pause; the row whose detail is
// open is selected.

import (
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// voiceTableTrail is the room the rows' actions take (▶, 「使用」, ⋯),
// DIPs at 100 %.
const voiceTableTrail = 148

// voiceRow is a row of a voice table (the row kit's, ui_list.go):
// cells (avatar, name, meta values) in a button that opens the voice's
// detail, then actions. It reports a click on the row's body.
func voiceRow(c *ui.Context, key, label string, st rowState, cells, actions func()) bool {
	return voiceRowDel(c, key, label, st, nil, cells, actions)
}

// voiceRowDel is voiceRow whose Delete (on the focused row) runs del
// (which asks first); nil none.
func voiceRowDel(c *ui.Context, key, label string, st rowState, del, cells, actions func()) bool {
	return listRow(c, listRowSpec{key: key, label: label, tip: i18n.T("Open the voice's details"), state: st, trail: voiceTableTrail, del: del}, cells, actions)
}

// voiceNameCell is a row's avatar (portrait or initial) and its name
// over a grey line; the name in the accent while it plays.
func voiceNameCell(c *ui.Context, name, sub string, bmp *ui.Bitmap, playing bool, marks func()) {
	t := c.Theme()
	ui.Avatar(c, name, bmp).Size(px(t, listThumbD), px(t, listThumbD)).Shrink(0)
	listTitleMarks(c, name, sub, playing, marks)
}

// previewToggle is a row's ▶ (the pause glyph, labelled Stop, while it
// plays).
func previewToggle(c *ui.Context, key string, playing bool) ui.Element {
	return previewToggleIf(c, key, playing, true)
}

// previewToggleIf is previewToggle, disabled and saying so when the
// voice has nothing to play (ok false): every voice row has its ▶.
func previewToggleIf(c *ui.Context, key string, playing, ok bool) ui.Element {
	switch {
	case !ok:
		return iconButton(c.Key(key), icons.Play, i18n.T("No preview for this voice")).Disabled(true)
	case playing:
		return iconButton(c.Key(key), icons.Pause, i18n.T("Stop"))
	}
	return iconButton(c.Key(key), icons.Play, i18n.T("Preview"))
}

// useButton is a row's 「使用」: a small text button on a hairline, as
// tall as the row's icon buttons, with their face under the pointer.
func useButton(c *ui.Context, key, tip string) ui.Element {
	t := c.Theme()
	label := i18n.X("voice row", "Use")
	b := ghostBase(c.Key(key)).Height(iconSide(t)).Padding(0, px(t, 10)).Border(1, tok(t).Line).Center().
		Label(label).Tooltip(tip).Shrink(0)
	b.Children(func() { ui.Text(c, label).FontSize(px(t, fsCaption)).SingleLine() })
	return b
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
