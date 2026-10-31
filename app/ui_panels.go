package main

// The kit's side panels: floating cards over the page (#126). A panel
// sits at the right of the page's body, a gap from its edges, rounded,
// with a hairline and a soft shadow; it slides in from the edge as it
// fades in, and slides out before it goes. An inspector (a voice's, a
// take's details) leaves the page's layout alone: the list under it keeps
// its width and stays usable where the panel does not cover it. A tool
// panel (the Studio's settings, a dub step's, a story's inspector) keeps
// a lane beside the page while the page leaves its main column room, so
// that it covers nothing, and floats over the page when it does not.

import (
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// The panels' motion: in, easing out; out, faster, easing in.
const (
	panelInDur  = 240 * time.Millisecond
	panelOutDur = 180 * time.Millisecond
)

// pageWidth is the width of the page beside the sidebar (or the rail).
func (a *app) pageWidth(c *ui.Context) float32 {
	if a.sectionW > 0 { // a Settings section builds: its width
		return a.sectionW
	}
	t := c.Theme()
	w, _ := c.Size()
	side := a.sideW
	if a.railMode() {
		side = px(t, railWidth)
	}
	return w - side
}

// floatState is a floating panel's life: mounted while it shows or slides
// out; closing once its ✕ or Escape sent it out (the host's close runs
// when it is out); item is what it shows.
type floatState struct {
	seen, mounted, closing bool
	item                   any
}

// floatPanel describes a floating panel.
type floatPanel struct {
	key, label string
	w          float32 // its width (DIPs, px'd); narrowed to fit the page
	left       bool    // at the left of the page, sliding from there
	lane       bool    // the page keeps a lane for it (a tool panel that fits)
	fill       bool    // the content fills the panel (it scrolls itself)
	item       any     // what it shows: another item swaps the content
	// close runs once the panel slid out after its ✕ or Escape (nil:
	// only the host's own controls close it).
	close func()
}

// float returns key's floating panel's state.
func (a *app) float(key string) *floatState {
	if a.floats == nil {
		a.floats = map[string]*floatState{}
	}
	st := a.floats[key]
	if st == nil {
		st = &floatState{}
		a.floats[key] = st
	}
	return st
}

// dismissPanel returns what sends key's panel out, for its ✕: it slides
// out, then its close runs.
func (a *app) dismissPanel(key string) func() {
	return func() {
		if st := a.float(key); st.mounted {
			st.closing = true
		}
	}
}

// floating builds p's panel while open, and while it slides out: call it
// last in the page's body (a row filling the page under its header). Out
// of a host that closed it, a panel slides out with build when there is
// one (a tool panel's content outlives its toggle), else goes at once (an
// inspector whose item went away).
func (a *app) floating(c *ui.Context, p floatPanel, open bool, build func()) {
	t := c.Theme()
	k := tok(t)
	st := a.float(p.key)
	first := !st.seen
	st.seen = true
	if open && p.item != st.item {
		st.item, st.closing = p.item, false // another item: it stays, new content
	}
	if !open {
		st.closing = false
		if build == nil || !st.mounted {
			st.mounted = false
			return
		}
	}
	gap := px(t, sp3)
	w := max(min(p.w, a.pageWidth(c)-px(t, 48)-gap), px(t, 200))
	var lane ui.Element
	if p.lane && !p.left {
		lane = ui.Box(c).Shrink(0)
	}
	card := ui.Column(c.Key(p.key+"-float")).Absolute().Top(px(t, sp2)).Bottom(gap).Width(w).
		Radius(px(t, radiusCard)).Background(k.Bg).Border(1, k.Line).Clip().
		Shadow(0, 8, 28, 0, ui.RGBA(0, 0, 0, 0.16)).Label(p.label)
	if p.left {
		card.Left(gap)
	} else {
		card.Right(gap)
	}
	in := open && !st.closing
	if !st.mounted && !first {
		card.Jump("slide", 0) // it comes in; on the page's first build it is there
	}
	st.mounted = true
	target, anim := float32(1), ui.Animation{Duration: panelInDur, Ease: ui.EaseOut}
	if !in {
		target, anim = 0, ui.Animation{Duration: panelOutDur, Ease: ui.EaseIn}
	}
	v := card.AnimateTo("slide", target, anim)
	if lane.Valid() {
		lane.Width(v * (w + gap))
	}
	if !in && v < 0.001 { // out: gone, and the host closes it
		st.mounted = false
		card.Opacity(0)
		if st.closing {
			st.closing = false
			if p.close != nil {
				p.close()
			}
		}
		c.AnimationFrame()
		return
	}
	dx := (1 - v) * (w + gap)
	if p.left {
		dx = -dx
	}
	card.Translate(dx, 0).Opacity(v)
	if in && p.close != nil {
		// Escape, wherever the focus is; built before the panel's content,
		// so that the pickers and popovers it opens take theirs first.
		ui.Overlay(c, func() {
			if ui.Box(c.Key(p.key+"-esc")).Absolute().Width(0).Height(0).OverlayShortcut(0, ui.KeyEscape) {
				a.dismissPanel(p.key)()
			}
		})
	}
	card.Children(func() {
		if p.fill {
			ui.Column(c).Grow(1).MinHeight(0).Children(build)
			return
		}
		ui.Scroll(c.Key(p.key)).Grow(1).MinHeight(0).Children(build)
	})
}

// laneWidth is what a tool panel w wide takes from the page in its lane.
func laneWidth(t *ui.Theme, w float32) float32 { return w + px(t, sp3) }

// panelHeader is a side panel's title row: the title (15, semibold) and
// a close icon button.
func panelHeader(c *ui.Context, title string, close func()) {
	t := c.Theme()
	ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
		sectionHeading(c, title).Grow(1)
		if iconButton(c.Key("panel-close"), icons.Close, i18n.T("Close")).Clicked() {
			close()
		}
	})
}

// panelState is a page's tool panel (a step's settings, an inspector):
// in its lane beside the main column when both fit, else hidden and
// opened by its toggle over the page.
type panelState struct {
	open, set bool // the toggle's choice, once it was used
}

// layout says whether the panel, width wide, fits beside a main column
// of at least minMain (in its lane), and whether it shows.
func (sp *panelState) layout(a *app, c *ui.Context, width, minMain float32) (inline, shown bool) {
	inline = a.pageWidth(c)-width >= minMain
	shown = inline
	if sp.set {
		shown = sp.open
	}
	return inline, shown
}

// toggle is the panel's header button (like the Studio's inspector
// toggle): ic on the pressed face while the panel shows.
func (sp *panelState) toggle(c *ui.Context, ic icons.Icon, shown bool, showTip, hideTip string) {
	tip := showTip
	if shown {
		tip = hideTip
	}
	if panelToggle(c, ic, shown, tip).Clicked() {
		sp.open, sp.set = !shown, true
	}
}

// view builds the panel at the right of the page's body (call it last
// in the body's row): in its lane when inline, else over the page.
func (sp *panelState) view(a *app, c *ui.Context, key, label string, inline, shown bool, width float32, build func()) {
	hide := func() { sp.open, sp.set = false, true }
	a.floating(c, floatPanel{key: key, label: label, w: width, lane: inline, close: hide}, shown, build)
}
