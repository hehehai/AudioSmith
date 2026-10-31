package main

// The 0.5 kit's cards and rows (ui_kit.go): the composer card (a drop
// zone or a text area over a row of parameter chips, a meta slot and the
// round submit), the drop zone, the tool tile (a 44 px outlined square, an
// icon, one word), the image card, the feature card, the dismissible
// promo card, the suggestion card, and the table row (hairlines, a hover
// tint, trailing inline actions).

import (
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// composerCard is a tool's "do one thing" entry (transcribe this, dub
// that): a card inside a hairline, radius 12, with top (a dropZone or a
// text area) above a row of chips (the parameters: model, language…),
// meta (a grey line: length, duration; nil for none) and submit (a
// submitButton) at the right end.
func composerCard(c *ui.Context, key string, top, chips, meta, submit func()) ui.Element {
	t := c.Theme()
	k := tok(t)
	return ui.Column(c.Key(key)).Radius(px(t, radiusCard)).Border(1, k.Line).Background(k.Bg).Clip().Shrink(0).Children(func() {
		ui.Column(c).Padding(px(t, sp2)).AlignItems(ui.Stretch).Children(top)
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Padding(px(t, 4), px(t, sp3), px(t, sp3)).Children(func() {
			ui.Row(c).Gap(px(t, 6)).Wrap().AlignItems(ui.Center).Grow(1).MinWidth(0).Children(func() {
				if chips != nil {
					chips()
				}
			})
			if meta != nil {
				ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Shrink(0).Children(meta)
			}
			if submit != nil {
				submit()
			}
		})
	})
}

// dropZone is the top of a composer that takes files: a grey area with
// ic, title (Click to upload, or drag and drop) and sub (formats; "" for
// none), and actions (buttons: Upload, Record; nil for none) under them.
// It returns the area: give it OnDrop.
func dropZone(c *ui.Context, ic icons.Icon, title, sub string, actions func()) ui.Element {
	t := c.Theme()
	k := tok(t)
	z := ui.Column(c).MinHeight(px(t, 132)).Center().Gap(px(t, 6)).Padding(px(t, sp4)).Radius(px(t, radiusControl)).Background(k.Subtle)
	if t.Dark {
		z.Background(k.G[1])
	}
	z.Children(func() {
		ui.Icon(c, ic.Linear).Size(px(t, 22), px(t, 22)).TextColor(k.Text2)
		ui.Text(c, title).TextAlign(ui.Center)
		if sub != "" {
			ui.Text(c, sub).FontSize(px(t, fsCaption)).TextColor(k.Text3).TextAlign(ui.Center)
		}
		if actions != nil {
			ui.Row(c).Gap(px(t, sp2)).Wrap().Justify(ui.Center).Margin(px(t, 6), 0, 0).Children(actions)
		}
	})
	return z
}

// toolTile is a tool to open (Speech, Clone, Dub…): its icon in a 44 px
// outlined square, and one word under it. Act on Clicked.
func toolTile(c *ui.Context, ic icons.Icon, label string) ui.Element {
	t := c.Theme()
	k := tok(t)
	b := ui.ButtonBase(c).Column().Gap(px(t, 6)).AlignItems(ui.Center).Width(px(t, 64)).Label(label).Shrink(0)
	b.Children(func() {
		sq := ui.Box(c).Size(px(t, 44), px(t, 44)).Radius(px(t, radiusCard)).Border(1, k.Line).Background(k.Field).Center()
		switch {
		case b.Pressed():
			sq.Background(k.Pressed)
		case b.Hovered():
			sq.Background(k.Hover)
		}
		lift(c, sq)
		sq.Children(func() {
			ui.Icon(c, ic.Linear).Size(px(t, 20), px(t, 20)).TextColor(k.Text)
		})
		ui.Text(c, label).FontSize(px(t, fsCaption)).TextColor(k.Text2).SingleLine().Ellipsis("…").MaxWidth(px(t, 64))
	})
	return b
}

// imageCard is a template or a sample to open: img (or, when nil, a fill
// of tint) under a gradient, title and sub in white at its bottom left;
// radius 12. Give it a width (it is 4:3); act on Clicked.
func imageCard(c *ui.Context, img *ui.Bitmap, tint ui.Color, title, sub string) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c).Column().AspectRatio(4.0 / 3).Radius(px(t, radiusCard)).Clip().Label(title).Background(tint).
		Justify(ui.End).AlignItems(ui.Stretch)
	b.Children(func() {
		if img != nil {
			ui.Image(c, img).Fit(ui.Cover).Absolute().Left(0).Top(0).Right(0).Bottom(0)
		}
		shade := ui.RGBA(0, 0, 0, 0.55)
		if b.Hovered() {
			shade = ui.RGBA(0, 0, 0, 0.7)
		}
		ui.Column(c).Padding(px(t, sp6), px(t, sp3), px(t, sp3)).Gap(px(t, 1)).Gradient(shade.Alpha(0), shade, 180).Children(func() {
			ui.Text(c, title).FontWeight(500).TextColor(ui.Hex("#ffffff")).SingleLine().Ellipsis("…").MinWidth(0)
			if sub != "" {
				ui.Text(c, sub).FontSize(px(t, fsCaption)).TextColor(ui.RGBA(255, 255, 255, 0.8)).SingleLine().Ellipsis("…").MinWidth(0)
			}
		})
	})
	return b
}

// featureCard is a large card on the light grey fill, radius 20: art (an
// illustration or a live control; nil for none) above title and body, and
// action (one primaryButtonWide or a secondary one) at its foot.
func featureCard(c *ui.Context, title, body string, art, action func()) ui.Element {
	t := c.Theme()
	k := tok(t)
	return ui.Column(c).Padding(px(t, sp5)).Gap(px(t, sp3)).Radius(px(t, radiusFeature)).Background(k.Subtle).AlignItems(ui.Stretch).
		MinWidth(0).Children(func() {
		if art != nil {
			ui.Column(c).MinHeight(px(t, 96)).Center().Children(art)
		}
		ui.Column(c).Gap(px(t, 4)).Grow(1).Children(func() {
			ui.Text(c, title).FontSize(px(t, fsSection)).FontWeight(600)
			if body != "" {
				ui.Text(c, body).FontSize(px(t, fsCaption)).TextColor(k.Text2)
			}
		})
		if action != nil {
			action()
		}
	})
}

// promoCard is a card that points to something (a new tool, a tip worth
// trying): ic in a grey square, title over body, action (a secondary
// button; nil for none) on the right, and × when onClose is set. Callers
// remember the dismissal (app.markSeen).
func promoCard(c *ui.Context, ic icons.Icon, title, body string, action, onClose func()) ui.Element {
	t := c.Theme()
	k := tok(t)
	return ui.Row(c).Gap(px(t, sp3)).AlignItems(ui.Center).Padding(px(t, sp3), px(t, sp2), px(t, sp3), px(t, sp3)).
		Radius(px(t, radiusCard)).Border(1, k.Line).Background(k.Bg).Shrink(0).Children(func() {
		ui.Box(c).Size(px(t, 36), px(t, 36)).Radius(px(t, radiusControl)).Background(k.G[2]).Center().Shrink(0).Children(func() {
			ui.Icon(c, ic.Linear).Size(px(t, 18), px(t, 18)).TextColor(k.Text)
		})
		ui.Column(c).Gap(px(t, 2)).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, title).FontWeight(500).SingleLine().Ellipsis("…").MinWidth(0)
			if body != "" {
				ui.Text(c, body).FontSize(px(t, fsCaption)).TextColor(k.Text2).MinWidth(0)
			}
		})
		if action != nil {
			action()
		}
		if onClose != nil && iconButton(c.Key("promo-close"), icons.Close, i18n.T("Dismiss")).Clicked() {
			onClose()
		}
	})
}

// suggestionCard is a suggestion to start from: title over two lines of
// body in grey, inside a hairline. Act on Clicked.
func suggestionCard(c *ui.Context, title, body string) ui.Element {
	t := c.Theme()
	k := tok(t)
	b := ui.ButtonBase(c).Column().Gap(px(t, 4)).AlignItems(ui.Stretch).Padding(px(t, sp3), px(t, sp4)).Radius(px(t, radiusCard)).
		Border(1, k.Line).Background(k.Bg).Label(title).MinWidth(0)
	if b.Hovered() {
		b.Background(k.Hover)
	}
	b.Children(func() {
		ui.Text(c, title).FontWeight(500).SingleLine().Ellipsis("…").MinWidth(0)
		ui.Text(c, body).FontSize(px(t, fsCaption)).TextColor(k.Text2).MaxLines(2).Ellipsis("…").MinWidth(0)
	})
	return b
}

// tableRow is a row of a list as a table: cells (avatar or icon, name,
// columns), then actions (inline icon buttons: play, download, ⋯) at its
// end; a hairline under it, the hover grey under the pointer, the chosen
// grey when selected. label names it; act on Clicked (open the row).
func tableRow(c *ui.Context, label string, selected bool, cells, actions func()) ui.Element {
	t := c.Theme()
	k := tok(t)
	b := ui.ButtonBase(c).MinHeight(px(t, 52)).Padding(px(t, 6), px(t, sp2), px(t, 6), px(t, sp3)).Gap(px(t, sp3)).
		AlignItems(ui.Center).Justify(ui.Start).BorderWidth(0, 0, 1, 0).BorderColor(k.Line).Label(label).Checked(selected).MinWidth(0)
	switch {
	case selected:
		b.Background(k.Pressed)
	case b.Hovered():
		b.Background(k.Hover)
	}
	b.Children(func() {
		ui.Row(c).Gap(px(t, sp3)).AlignItems(ui.Center).Grow(1).MinWidth(0).Children(cells)
		if actions != nil {
			ui.Row(c).Gap(px(t, 2)).AlignItems(ui.Center).Shrink(0).Children(actions)
		}
	})
	return b
}
