package main

// The 0.5 kit's buttons (ui_kit.go; the ranks are in ui_buttons.go): the
// white secondary button with its hairline and faint lift, the text
// button, the square outlined icon button, the wide primary of a card or
// a dialog, and the round submit button of a composer.

import (
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// secondaryButton is a secondary action (Upload, Try now, Cancel): a white
// face inside a 1 px hairline, with a faint shadow, as tall as
// primaryButton. It is ui.Button in the kit's theme (styleTheme makes its
// face white), plus the lift.
//
//	if secondaryButton(c.Key("upload"), i18n.T("Upload")).Clicked() { … }
func secondaryButton(c *ui.Context, label string) ui.Element {
	return lift(c, ui.Button(c, label))
}

// lift puts the faint shadow of a white control under b (none in the
// dark, where controls are a raised grey).
func lift(c *ui.Context, b ui.Element) ui.Element {
	if k := tok(c.Theme()); k.Lift.A > 0 {
		b.Shadow(0, 1, 2, 0, k.Lift)
	}
	return b
}

// textButton is a quiet action written as text (Learn more, View all,
// Continue without switching): no face and no border, the secondary grey
// turning to the text colour under the pointer; chevron adds a "›" after
// it. It stands as tall as the other buttons.
func textButton(c *ui.Context, label string, chevron bool) ui.Element {
	t := c.Theme()
	k := tok(t)
	b := ui.ButtonBase(c).Padding(t.Space(1.5), t.Space(1)).Gap(px(t, 2)).Radius(px(t, radiusControl)).Border(1, ui.Color{}).
		Label(label).Shrink(0)
	col := k.Text2
	if b.Hovered() || b.Pressed() {
		col = k.Text
	}
	b.Children(func() {
		ui.Text(c, label).SingleLine().TextColor(col)
		if chevron {
			ui.Icon(c, icons.AltArrowRight.Linear).Size(px(t, 12), px(t, 12)).TextColor(col)
		}
	})
	return b
}

// outlineIconButton is a square button showing an icon inside a hairline
// (Filters, sort, a view switch), as tall as a text field; tip names it.
// on shows it chosen (the pressed grey), for toggles.
func outlineIconButton(c *ui.Context, ic icons.Icon, tip string, on bool) ui.Element {
	t := c.Theme()
	k := tok(t)
	side := t.Rem(1.15) + t.Space(3) + 2 // a text field's height: its line, its padding, its border
	b := ui.ButtonBase(c).Size(side, side).Center().Radius(px(t, radiusControl)).Border(1, k.Line).Background(k.Field).
		Label(tip).Tooltip(tip).Checked(on).Shrink(0)
	switch {
	case on || b.Pressed():
		b.Background(k.Pressed)
	case b.Hovered():
		b.Background(k.Hover)
	}
	lift(c, b)
	b.Children(func() {
		col := k.Text2
		if on || b.Hovered() {
			col = k.Text
		}
		ui.Icon(c, ic.Linear).Size(px(t, 16), px(t, 16)).TextColor(col)
	})
	return b
}

// primaryButtonWide is the primary action at the foot of a card or a
// dialog (Get started, Create): ink, 40 high, as wide as its column, an
// icon before its label when ic is set (icons.Icon{} for none).
func primaryButtonWide(c *ui.Context, ic icons.Icon, label string) ui.Element {
	t := c.Theme()
	c.SetTheme(inkTheme(t))
	b := ui.PrimaryButton(c, "").Border(1, ui.Color{}).Height(px(t, 40)).AlignSelf(ui.Stretch).Label(label)
	b.Children(func() {
		if ic.Linear != nil {
			ui.Icon(c, ic.Linear).Size(labelIcon(t), labelIcon(t))
		}
		ui.Text(c, label).SingleLine().FontWeight(500)
	})
	c.SetTheme(t)
	return b
}

// submitButton is the round submit button at the right end of a composer
// (an arrow up in a circle, 32 across): grey and disabled until ready,
// then ink. tip names it.
//
//	if submitButton(c.Key("send"), i18n.T("Generate"), text != "").Clicked() { … }
func submitButton(c *ui.Context, tip string, ready bool) ui.Element {
	t := c.Theme()
	k := tok(t)
	d := px(t, 32)
	b := ui.ButtonBase(c).Size(d, d).Radius(d / 2).Center().Label(tip).Tooltip(tip).Disabled(!ready).Shrink(0)
	face, fg := k.G[6], k.Bg // dimmed by Disabled: a light grey
	if t.Dark {
		face, fg = k.G[6], k.G[9]
	}
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
