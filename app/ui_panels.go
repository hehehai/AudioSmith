package main

// The kit's side panels: floating cards over the page (#126). A panel
// sits at the right of the page, flush with the window's edge, from the
// window's top (like the sidebar: #127; the page header's tools move
// left of it, its first row stands level with the header's title) to a
// gap from its bottom; the corner towards the page at the bottom is
// rounded, with a hairline and a small soft shadow. Where the header would not
// fit beside it, it keeps under the header. It slides in from the edge
// as it fades in, and slides out before it goes. An inspector (a voice's, a
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

// The panels' motion: in, easing out (cubic); out, faster, easing in
// (quadratic: a cubic hardly moves in its first third).
const (
	panelInDur  = 240 * time.Millisecond
	panelOutDur = 180 * time.Millisecond
)

func easeInQuad(t float32) float32 { return t * t }

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
	v                      float32 // its slide's value at the last frame
}

// floatPanel describes a floating panel.
type floatPanel struct {
	key, label string
	w          float32 // its width (DIPs, px'd); narrowed to fit the page
	left       bool    // at the left of the page, sliding from there
	lane       bool    // the page keeps a lane for it (a tool panel that fits)
	fill       bool    // the content fills the panel (it scrolls itself)
	// under is the height of the rows between the page's header and the
	// body the panel is built in (the dub's steps).
	under float32
	// lead is room over the content, whose first row is then level with
	// the page header's (panelLead): for content that also shows under
	// the header, in the page (a drawer's).
	lead float32
	item any // what it shows: another item swaps the content
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
	// The card runs from the window's top, like the sidebar, when the
	// page's header still fits beside it (it moves clear of the card);
	// else it stays under the header. The body starts under the header
	// and the rows the page puts under it (under).
	full := a.pageWidth(c)-headerPanelsNext.room-w >= px(t, headerKeepW) // beside the panels built before it
	top := float32(pageTop) + p.under
	r := px(t, radiusCard)
	card := ui.Column(c.Key(p.key+"-float")).Absolute().Bottom(gap).Width(w).
		Background(k.Bg).BorderColor(k.Line).Clip().
		Shadow(0, 2, 10, 0, ui.RGBA(0, 0, 0, 0.08)).Label(p.label)
	switch {
	case full && p.left:
		card.Top(-top).Left(0).Radius(0, 0, r, 0).BorderWidth(0, 1, 1, 0)
	case full:
		card.Top(-top).Right(0).Radius(0, 0, 0, r).BorderWidth(0, 0, 1, 1)
	case p.left:
		card.Top(px(t, sp2)).Left(0).Radius(0, r, r, 0).BorderWidth(1, 1, 1, 0)
	default:
		card.Top(px(t, sp2)).Right(0).Radius(r, 0, 0, r).BorderWidth(1, 0, 1, 1)
	}
	in := open && !st.closing
	wasIn := st.mounted
	if !st.mounted && !first {
		card.Jump("slide", 0) // it comes in; on the page's first build it is there
	}
	st.mounted = true
	target, anim := float32(1), ui.Animation{Duration: panelInDur, Ease: ui.EaseOut}
	if !in {
		target, anim = 0, ui.Animation{Duration: panelOutDur, Ease: easeInQuad}
	}
	now := card.AnimateTo("slide", target, anim)
	// It shows where the last frame's value puts it, as the page's header
	// does (headerPanels).
	v := st.v
	if first || !wasIn {
		v = now
	}
	st.v = now
	if v != now {
		c.AnimationFrame() // and one more frame to catch up
	}
	if lane.Valid() {
		lane.Width(v * w)
	}
	if !in && now < 0.001 { // out: gone, and the host closes it
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
	if full {
		headerAvoid(p.left, now*w, max(target, now)*w) // where the next frame shows it
	}
	dx := (1 - v) * w
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
		band := px(t, sp1) // under the header: a card's top margin
		if full {
			band = panelBand(c, p.left) + p.lead
		}
		if band > 0 {
			ui.Box(c).Height(band).Shrink(0)
		}
		if p.fill {
			ui.Column(c).Grow(1).MinHeight(0).Children(build)
		} else {
			ui.Scroll(c.Key(p.key)).Grow(1).MinHeight(0).Children(build)
		}
		// The band over the content's first row, in the title bar, moves
		// the window like the page's header beside it.
		ui.Box(c.Key(p.key + "-drag")).Absolute().Top(0).Left(0).Right(0).Height(panelDragH(c, p.left)).DragWindow()
	})
}

// headerKeepW is the room (DIPs at 100 %) a page header keeps beside a
// panel that runs from the window's top: its title and narrow tools.
const headerKeepW = 360

// panelBand is the room a panel leaves over its content at its top: a
// left one, under the window controls when they reach past the rail.
func panelBand(c *ui.Context, left bool) float32 {
	if left && headerInset > 0 {
		return titleBarHeight - px(c.Theme(), sp3)
	}
	return 0
}

// panelDragH is the height of the band at a panel's top that moves the
// window: the window's edge, above its content's first row.
func panelDragH(c *ui.Context, left bool) float32 {
	return panelBand(c, left) + 4
}

// panelLead is the room over a panel's first row, rowH DIPs tall at
// 100 %, that puts its middle on the page header's (the header is
// titleBarHeight tall at any scale; the row grows with it).
func panelLead(t *ui.Theme, rowH float32) float32 {
	return max(px(t, 4), titleBarHeight/2-px(t, rowH)/2)
}

// laneWidth is what a tool panel w wide takes from the page in its lane.
func laneWidth(t *ui.Theme, w float32) float32 { return w }

// panelHeader is a side panel's title row: the title (15, semibold) and
// a close icon button.
func panelHeader(c *ui.Context, title string, close func()) {
	t := c.Theme()
	ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).DragWindow().Children(func() {
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
// in the body's row, under rows under the header): in its lane when
// inline, else over the page.
func (sp *panelState) view(a *app, c *ui.Context, key, label string, inline, shown bool, width, under float32, build func()) {
	hide := func() { sp.open, sp.set = false, true }
	a.floating(c, floatPanel{key: key, label: label, w: width, lane: inline, under: under, close: hide}, shown, build)
}
