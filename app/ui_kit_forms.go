package main

// The 0.5 kit's form controls (ui_kit.go): the selector row (an avatar or
// an icon, the current value, ›: a voice, a model), the slim slider (a
// 2 px track filled in ink, a 12 px ink knob) and its labelled field with
// end labels, the seek slider (the same, in the accent: what plays), and
// the ink switch.

import (
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// selectorRow is a choice that opens a panel or a menu (Voice, Model): a
// row as wide as its column inside a hairline, an avatar of avatar (when
// not "") or else ic in a grey square, value over sub (a grey line, ""
// for none), and a › at the end. Attach the panel with Popover or Menu,
// or act on Clicked; label names it.
func selectorRow(c *ui.Context, ic icons.Icon, avatar, value, sub, label string) ui.Element {
	t := c.Theme()
	k := tok(t)
	b := ui.ButtonBase(c).AlignSelf(ui.Stretch).MinHeight(px(t, 40)).Padding(px(t, 6), px(t, sp3), px(t, 6), px(t, sp2)).
		Gap(px(t, 10)).Radius(px(t, radiusControl)).Border(1, k.Line).Background(k.Field).Label(label).Tooltip(value).MinWidth(0)
	switch {
	case b.Pressed():
		b.Background(k.Pressed)
	case b.Hovered():
		b.Background(k.Hover)
	}
	b.Children(func() {
		if avatar != "" {
			ui.Avatar(c, avatar, nil).Size(px(t, 26), px(t, 26))
		} else if ic.Linear != nil {
			ui.Box(c).Size(px(t, 26), px(t, 26)).Radius(px(t, 6)).Background(k.G[2]).Center().Shrink(0).Children(func() {
				ui.Icon(c, ic.Linear).Size(px(t, 15), px(t, 15)).TextColor(k.Text)
			})
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 1)).Children(func() {
			ui.Text(c, value).SingleLine().Ellipsis("…").MinWidth(0)
			if sub != "" {
				ui.Text(c, sub).FontSize(px(t, fsCaption)).TextColor(k.Text2).SingleLine().Ellipsis("…").MinWidth(0)
			}
		})
		ui.Icon(c, icons.AltArrowRight.Linear).Size(px(t, 14), px(t, 14)).TextColor(k.Text3)
	})
	return b
}

// slimSlider is a slider setting *value between lo and hi (ui.Slider's
// replacement): a 2 px track, filled in ink up to a 12 px ink knob. The
// arrows, Home and End move it while it has the focus; Changed reports a
// new value. Name it with Label.
func slimSlider(c *ui.Context, value *float64, lo, hi float64) ui.Element {
	return slimTrack(c, value, lo, hi, 0, false)
}

// slimStepSlider is slimSlider whose values are lo and the multiples of
// step from it (ui.StepSlider's replacement, without tick marks):
// dragging snaps to them.
func slimStepSlider(c *ui.Context, value *float64, lo, hi, step float64) ui.Element {
	return slimTrack(c, value, lo, hi, step, false)
}

// seekSlider is slimSlider in the accent, for a position in what plays.
func seekSlider(c *ui.Context, value *float64, lo, hi float64) ui.Element {
	return slimTrack(c, value, lo, hi, 0, true)
}

// slimTrack draws the slim sliders on MyGo's SliderBase.
func slimTrack(c *ui.Context, value *float64, lo, hi, step float64, accent bool) ui.Element {
	t := c.Theme()
	k := tok(t)
	knob := px(t, 12)
	s := ui.SliderBase(c, value, lo, hi).Height(px(t, 20)).MinWidth(px(t, 80)).PaddingX(knob / 2)
	if step > 0 {
		s.Step(step)
	}
	frac := float32(0)
	if hi > lo {
		frac = float32((min(max(*value, lo), hi) - lo) / (hi - lo))
	}
	if c.Direction() == ui.RightToLeft {
		frac = 1 - frac
	}
	fill := k.Ink
	if accent {
		fill = k.Accent
	}
	hover := s.Hovered() || s.Pressed()
	focus := s.FocusVisible()
	track := k.G[4]
	if t.Dark {
		track = k.G[5]
	}
	s.Draw(func(p *ui.Painter, r ui.Rect) {
		h := px(t, 2)
		cy := r.Y + r.H/2
		x := r.X + knob/2 + (r.W-knob)*frac
		p.Fill(ui.Rect{X: r.X, Y: cy - h/2, W: r.W, H: h}, track, h/2)
		if c.Direction() == ui.RightToLeft {
			p.Fill(ui.Rect{X: x, Y: cy - h/2, W: r.X + r.W - x, H: h}, fill, h/2)
		} else {
			p.Fill(ui.Rect{X: r.X, Y: cy - h/2, W: x - r.X, H: h}, fill, h/2)
		}
		d := knob
		if hover {
			d = knob * 1.17
		}
		kr := ui.Rect{X: x - d/2, Y: cy - d/2, W: d, H: d}
		if focus {
			p.Fill(ui.Rect{X: kr.X - 3, Y: kr.Y - 3, W: kr.W + 6, H: kr.H + 6}, k.Accent.Alpha(0.35), (d+6)/2)
		}
		p.Fill(kr, fill, d/2)
	})
	return s
}

// sliderField is a labelled slim slider: title (and value, the current
// setting as text, on the right) over the slider, and loLabel and hiLabel
// ("" for none) at its ends under it, in the meta grey (Slower · Faster).
// It reports whether the slider changed *value; step 0 is continuous.
func sliderField(c *ui.Context, key, title, value string, v *float64, lo, hi, step float64, loLabel, hiLabel string) bool {
	t := c.Theme()
	k := tok(t)
	changed := false
	ui.Column(c.Key(key)).Gap(px(t, 4)).AlignItems(ui.Stretch).Shrink(0).Children(func() {
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).SingleLine().Ellipsis("…").MinWidth(0).Grow(1)
			if value != "" {
				ui.Text(c, value).FontSize(px(t, fsCaption)).TextColor(k.Text2).SingleLine().Shrink(0)
			}
		})
		changed = slimTrack(c.Key("slider"), v, lo, hi, step, false).Label(title).Changed()
		if loLabel != "" || hiLabel != "" {
			ui.Row(c).Gap(px(t, sp2)).Children(func() {
				ui.Text(c, loLabel).FontSize(px(t, fsCaption)).TextColor(k.Text3).SingleLine().Grow(1).MinWidth(0)
				ui.Text(c, hiLabel).FontSize(px(t, fsCaption)).TextColor(k.Text3).SingleLine().Shrink(0)
			})
		}
	})
	return changed
}

// inkSwitch is a switch toggling *on (ui.Switch's replacement): ink while
// on, as the overhaul's monochrome asks.
func inkSwitch(c *ui.Context, on *bool) ui.Element {
	t := c.Theme()
	ink := inkTheme(t)
	if t.Dark {
		// White ink would hide the white knob: a light grey track.
		ink.Accent = tok(t).G[7]
	}
	c.SetTheme(ink)
	s := ui.Switch(c, on)
	c.SetTheme(t)
	return s
}
