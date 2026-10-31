package main

// Popovers that fit the window (thread #84). MyGo places a popover below
// its anchor, or above it where that overflows the window less, but never
// shortens it: a list taller than the room on either side ran off the
// window, its last rows out of reach (the model picker with "Show all
// models" open, in the Stories inspector). fitPopover measures the room
// beside the anchor as the window is now (so a resize is followed), picks
// the side, and caps the panel to that room: its content then scrolls
// inside it.

import "github.com/egoist/mygo/ui"

// popoverRoom is where a popover goes beside anchor box r in a window h
// tall: below unless the room there is cramped (under want) and there is
// more above; and the height its panel may take there (its margin from
// the anchor and from the window's edge left out).
func popoverRoom(t *ui.Theme, r ui.Rect, h, want float32) (room float32, above bool) {
	gap := t.Space(1)      // the panel's margin from the anchor
	edge := px(t, sp2) + 4 // air at the window's edge (MyGo keeps 4)
	below := h - (r.Y + r.H) - gap - edge
	over := r.Y - gap - edge
	if below < want && over > below {
		return max(over, 0), true
	}
	return max(below, 0), false
}

// fitPopover is ui.Popover (the theme's look, at least as wide as
// anchor) whose panel never leaves the window: below anchor, or above it
// when below is cramped, at most as tall as the room there. fn builds
// the content: a column whose scroll must be able to shrink
// (MinHeight(0)), so that its last row stays reachable; the panel lays it
// out as a column. want is the height below the anchor that is not
// cramped (0: 300 DIPs at the base size).
func fitPopover(c *ui.Context, anchor ui.Element, open *bool, want float32, fn func()) ui.Element {
	if !*open {
		return ui.Element{}
	}
	t := c.Theme()
	r := anchor.Bounds()
	_, h := c.Size()
	if want <= 0 {
		want = px(t, 300)
	}
	room, above := popoverRoom(t, r, h, want)
	return ui.PopoverBase(c, anchor, open, func(panel ui.Element) {
		panel.MinWidth(r.W).Padding(t.Space(1)).Radius(t.Radius+2).Background(t.Background).Border(1, t.Border).
			Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.18)).MaxHeight(room)
		if above {
			panel.AttachTo(anchor, ui.AnchorTopStart, ui.AnchorBottomStart).Margin(0, 0, t.Space(1), 0)
		} else {
			panel.Margin(t.Space(1), 0, 0, 0)
		}
		fn()
	})
}

// sidePopover is a popover in the kit's look beside anchor, after it (on
// its right), its top at the anchor's top: menus of the sidebar and the
// rail ("··· More").
func sidePopover(c *ui.Context, anchor ui.Element, open *bool, fn func()) {
	t := c.Theme()
	k := tok(t)
	ui.PopoverBase(c, anchor, open, func(panel ui.Element) {
		panel.AttachTo(anchor, ui.AnchorTopEnd, ui.AnchorTopStart).MarginStart(px(t, 6)).Padding(px(t, 4)).
			Radius(px(t, radiusCard)).Background(k.Bg).Border(1, k.Line).Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.18))
		fn()
	})
}
