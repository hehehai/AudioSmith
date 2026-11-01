package main

// The timeline lane's input (timeline.go): the pointer, the wheel, the
// pinch and the keys.

import (
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/dub"
)

// tlWheelZoom is how much a DIP of ⌘/⌥ + scroll zooms: a mouse wheel's
// notch (40 DIPs) zooms by e^0.2 ≈ 1.22×, as the Workflows canvas's
// e^(0.01·DIP) at half the rate (a timeline zooms over a wider range).
const tlWheelZoom = 0.005

// timelineInput handles the lane's pointer and keys.
func timelineInput(s *timelineState, m tlModel, ev tlEvents, e ui.InputEvent) bool {
	if e.Kind == ui.InputCommand {
		return ev.Command != nil && ev.Command(e.Text)
	}
	d := m.Duration
	if s.width <= 0 || d <= 0 {
		return false
	}
	span := s.span(d)
	toSec := func(x float32) float64 { return s.offset + float64(x/s.width)*span }
	pxPerSec := float64(s.width) / span
	switch e.Kind {
	case ui.InputPointerDown:
		if e.Button == 2 {
			// The middle button pans, anywhere.
			s.pan = &tlPan{x0: e.X, off0: s.offset}
			return true
		}
		if e.Button != 0 {
			return false
		}
		return timelinePress(s, m, ev, e, toSec)
	case ui.InputPointerMove:
		if g := s.pan; g != nil {
			s.offset = g.off0 - float64(e.X-g.x0)/pxPerSec
			s.clampOffset(d)
			s.panned = e.Time
			return true
		}
		if s.drag == nil && e.Button < 0 {
			// Hovering: the cursor says what a press does there.
			before := s.cursor(m)
			s.hoverX, s.hoverY, s.hover = e.X, e.Y, true
			return s.cursor(m) != before
		}
		if g := s.drag; g != nil && g.mode == tlSeek {
			g.curS = min(max(toSec(e.X)+g.grab, 0), d)
			g.moved = true
			if ev.Seek != nil && e.Time.Sub(g.seeked) >= tlScrubEvery {
				ev.Seek(g.curS)
				g.seeked = e.Time
			}
			return true
		}
		return timelineDragMove(s, m, e, pxPerSec)
	case ui.InputPointerUp:
		if s.pan != nil {
			s.pan = nil
			return true
		}
		g := s.drag
		s.drag = nil
		if g != nil && g.mode == tlSeek {
			if g.moved && ev.Seek != nil {
				ev.Seek(g.curS)
			}
			return true
		}
		if g == nil || !g.moved || ev.Change == nil {
			return g != nil
		}
		// Alt allows overlap; otherwise the block stays between its neighbours.
		ev.Change(g.id, g.curS, g.curE, e.Mods&ui.Alt == 0)
		return true
	case ui.InputMagnify:
		s.zoomAt(min(max(1+float64(e.Magnify), 0.5), 2), e.X, d)
		s.panned = e.Time
		return true
	case ui.InputScroll:
		return timelineScroll(s, d, e)
	case ui.InputKeyDown:
		return timelineKey(s, m, ev, e) || ev.Key != nil && ev.Key(e)
	}
	return false
}

// tlScrubEvery is how often dragging the playhead seeks the player (the
// lane draws the playhead under the pointer every frame).
const tlScrubEvery = 40 * time.Millisecond

// Grab zones of the playhead, DIPs either side of it: its handle in the
// ruler, its line in the lane under it.
const (
	tlKnobPx = 7
	tlLinePx = 4
)

// timelinePress takes a press of the main button (thread #134, user
// 11-01: a press anywhere used to seek, so the playhead could not be
// dragged deliberately). The ruler at the top seeks there and drags on
// as a scrub; the playhead's handle (or its line, off a block's edge)
// drags it from where it is, without a jump; a block is selected and
// moved or resized as before (a double click plays it), the playhead
// staying put; the empty lane pans while zoomed and otherwise does
// nothing.
func timelinePress(s *timelineState, m tlModel, ev tlEvents, e ui.InputEvent, toSec func(float32) float64) bool {
	d := m.Duration
	s.hover = false
	inRuler := e.Y < s.ruler
	ph, onPH := s.playheadX(m)
	near := float32(tlLinePx)
	if inRuler {
		near = tlKnobPx
	}
	onPH = onPH && abs32(e.X-ph) <= near*max(s.unit, 1)
	i, mode := s.blockAt(m, e.X)
	if inRuler || onPH && (i < 0 || mode == tlMove || mode < 0) {
		at, grab := min(max(toSec(e.X), 0), d), 0.0
		if onPH {
			at = m.Playhead
			grab = m.Playhead - toSec(e.X)
		}
		s.drag = &tlDrag{mode: tlSeek, curS: at, grab: grab, x0: e.X, seeked: e.Time}
		if !onPH && ev.Seek != nil {
			ev.Seek(at)
		}
		return true
	}
	if i >= 0 {
		b := m.Blocks[i]
		s.sel = b.ID
		if ev.Select != nil {
			ev.Select(b.ID)
		}
		if e.Clicks >= 2 && ev.Play != nil {
			ev.Play(b.ID)
			return true
		}
		if mode >= 0 {
			s.drag = &tlDrag{id: b.ID, mode: mode, x0: e.X, start: b.Start, end: b.End, curS: b.Start, curE: b.End}
		}
		return true
	}
	if s.zoom > 1 {
		s.pan = &tlPan{x0: e.X, off0: s.offset} // a drag on the empty lane pans
	}
	return true
}

// blockAt is the block under the lane's x (-1 none; the last drawn, on
// top, first) and what a drag of it does there: tlStart or tlEnd at a
// wide enough block's edge, tlMove inside it, -1 when it only selects
// (narrow, or just beside it).
func (s *timelineState) blockAt(m tlModel, x float32) (int, int) {
	d := m.Duration
	if s.width <= 0 || d <= 0 {
		return -1, -1
	}
	pxPerSec := float64(s.width) / s.span(d)
	for i := len(m.Blocks) - 1; i >= 0; i-- {
		b := m.Blocks[i]
		bw := (b.End - b.Start) * pxPerSec
		x0 := float32((b.Start - s.offset) * pxPerSec)
		x1 := float32((b.End - s.offset) * pxPerSec)
		if x < x0-tlEdgePx || x > x1+tlEdgePx {
			continue
		}
		switch {
		case bw >= tlResizePx && math.Abs(float64(x-x0)) <= tlEdgePx:
			return i, tlStart
		case bw >= tlResizePx && math.Abs(float64(x-x1)) <= tlEdgePx:
			return i, tlEnd
		case bw < tlMinGrabPx || x < x0 || x > x1:
			return i, -1
		}
		return i, tlMove
	}
	return -1, -1
}

// playheadX is where the lane draws the playhead (where a drag of it
// is, while it is dragged), and whether it is in view.
func (s *timelineState) playheadX(m tlModel) (float32, bool) {
	at := m.Playhead
	if g := s.drag; g != nil && g.mode == tlSeek {
		at = g.curS
	}
	span := s.span(m.Duration)
	if at < 0 || at < s.offset || at > s.offset+span || s.width <= 0 {
		return 0, false
	}
	return float32((at-s.offset)/span) * s.width, true
}

// cursor is the lane's cursor where the pointer hovers: a resize arrow
// on the playhead's handle and line and on a block's edges, a hand on the
// ruler, an open hand on the empty lane while zoomed (it pans).
func (s *timelineState) cursor(m tlModel) ui.Cursor {
	switch {
	case s.pan != nil:
		return ui.CursorGrabbing
	case s.drag != nil && s.drag.mode == tlSeek, s.drag != nil && s.drag.mode != tlMove:
		return ui.CursorResizeEW
	case s.drag != nil || !s.hover:
		return ui.CursorDefault
	}
	ph, onPH := s.playheadX(m)
	inRuler := s.hoverY < s.ruler
	near := float32(tlLinePx)
	if inRuler {
		near = tlKnobPx
	}
	onPH = onPH && abs32(s.hoverX-ph) <= near*max(s.unit, 1)
	i, mode := s.blockAt(m, s.hoverX)
	switch {
	case inRuler && onPH:
		return ui.CursorResizeEW
	case inRuler:
		return ui.CursorPointer
	case mode == tlStart || mode == tlEnd:
		return ui.CursorResizeEW
	case onPH:
		return ui.CursorResizeEW
	case i < 0 && s.zoom > 1:
		return ui.CursorGrab
	}
	return ui.CursorDefault
}

// timelineScroll takes the wheel and two-finger swipes over the lane:
// ⌘ or ⌥ zooms around the pointer, ⇧ turns a mouse wheel sideways, and a
// mostly sideways scroll pans; a vertical one is the page's.
func timelineScroll(s *timelineState, d float64, e ui.InputEvent) bool {
	dx, dy := e.DX, e.DY
	switch {
	case e.Mods&(ui.Cmd|ui.Alt) != 0:
		v := dy
		if v == 0 {
			v = dx
		}
		s.zoomAt(math.Exp(-float64(v)*tlWheelZoom), e.X, d)
		s.panned = e.Time
		return true
	case e.Mods&ui.Shift != 0 && dx == 0:
		dx = dy
	case math.Abs(float64(dx)) <= math.Abs(float64(dy)):
		return false
	}
	if s.zoom <= 1 {
		return false
	}
	s.panBy(dx, d)
	s.panned = e.Time
	return true
}

// timelineDragMove follows a block's drag.
func timelineDragMove(s *timelineState, m tlModel, e ui.InputEvent, pxPerSec float64) bool {
	d := m.Duration
	g := s.drag
	if g == nil {
		return false
	}
	dt := float64(e.X-g.x0) / pxPerSec
	if math.Abs(float64(e.X-g.x0)) > 2 {
		g.moved = true
	}
	snap := e.Mods&ui.Alt == 0
	switch g.mode {
	case tlMove:
		ns := min(max(g.start+dt, 0), d-(g.end-g.start))
		if snap {
			if t, ok := tlSnap(m, g.id, ns, pxPerSec); ok {
				ns = t
			} else if t, ok := tlSnap(m, g.id, ns+(g.end-g.start), pxPerSec); ok {
				ns = t - (g.end - g.start)
			}
		}
		g.curS, g.curE = ns, ns+(g.end-g.start)
	case tlStart:
		ns := min(max(g.start+dt, 0), g.end-dub.MinSegment)
		if snap {
			if t, ok := tlSnap(m, g.id, ns, pxPerSec); ok && t < g.end-dub.MinSegment {
				ns = t
			}
		}
		g.curS = ns
	case tlEnd:
		ne := max(min(g.end+dt, d), g.start+dub.MinSegment)
		if snap {
			if t, ok := tlSnap(m, g.id, ne, pxPerSec); ok && t > g.start+dub.MinSegment {
				ne = t
			}
		}
		g.curE = ne
	}
	return true
}

// overviewInput drags the overview strip's window, jumps it to a press
// beside it, and pans with sideways scrolls and the arrow keys.
func overviewInput(s *timelineState, m tlModel, e ui.InputEvent) bool {
	d := m.Duration
	if s.ovW <= 0 || d <= 0 {
		return false
	}
	at := float64(e.X/s.ovW) * d
	span := s.span(d)
	switch e.Kind {
	case ui.InputPointerDown:
		if e.Button != 0 {
			return false
		}
		grab := at - s.offset
		if grab < 0 || grab > span {
			grab = span / 2 // beside the window: centre it there
		}
		s.pan = &tlPan{grab: grab}
		fallthrough
	case ui.InputPointerMove:
		if s.pan == nil {
			return false
		}
		s.offset = at - s.pan.grab
		s.clampOffset(d)
		s.panned = e.Time
		return true
	case ui.InputPointerUp:
		taken := s.pan != nil
		s.pan = nil
		return taken
	case ui.InputScroll:
		dx := e.DX
		if e.Mods&ui.Shift != 0 && dx == 0 {
			dx = e.DY
		}
		if dx == 0 || math.Abs(float64(dx)) < math.Abs(float64(e.DY)) && e.Mods&ui.Shift == 0 {
			return false
		}
		s.panBy(dx, d)
		s.panned = e.Time
		return true
	case ui.InputKeyDown:
		switch e.Key {
		case ui.KeyLeft, ui.KeyRight:
			step := span / 10
			if e.Key == ui.KeyLeft {
				step = -step
			}
			s.offset += step
			s.clampOffset(d)
			s.panned = e.Time
			return true
		}
	}
	return false
}

// timelineKey handles the lane's keys.
func timelineKey(s *timelineState, m tlModel, ev tlEvents, e ui.InputEvent) bool {
	i := -1
	for k, b := range m.Blocks {
		if b.ID == s.sel {
			i = k
		}
	}
	selectAt := func(k int) {
		if k >= 0 && k < len(m.Blocks) {
			s.sel = m.Blocks[k].ID
			s.reveal(m, s.sel)
			if ev.Select != nil {
				ev.Select(s.sel)
			}
		}
	}
	switch e.Key {
	case ui.KeyLeft, ui.KeyRight:
		dir := 1.0
		if e.Key == ui.KeyLeft {
			dir = -1
		}
		if !s.edit || i < 0 {
			if i < 0 {
				selectAt(0)
			} else {
				selectAt(i + int(dir))
			}
			return true
		}
		step := 0.01
		if e.Mods&ui.Cmd != 0 {
			step = 0.1
		}
		edge := "start"
		switch {
		case e.Mods&ui.Alt != 0:
			edge = "move"
		case e.Mods&ui.Shift != 0:
			edge = "end"
		}
		if ev.Nudge != nil {
			ev.Nudge(s.sel, edge, dir*step)
		}
		return true
	case ui.KeyEnter:
		if i >= 0 {
			s.edit = !s.edit
		}
		return true
	case ui.KeyEscape:
		if s.edit {
			s.edit = false
			return true
		}
	case ui.KeyS:
		if i < 0 || ev.Change == nil || len(m.Onsets) == 0 {
			return false
		}
		b := m.Blocks[i]
		if e.Mods&ui.Shift != 0 {
			if t, ok := tlNearest(m.Onsets, b.End); ok && t > b.Start+dub.MinSegment {
				ev.Change(b.ID, b.Start, t, true)
			}
		} else if t, ok := tlNearest(m.Onsets, b.Start); ok && t < b.End-dub.MinSegment {
			ev.Change(b.ID, t, b.End, true)
		}
		return true
	case ui.KeyDelete, ui.KeyBackspace:
		if i >= 0 && ev.Delete != nil {
			ev.Delete(s.sel)
			if i+1 < len(m.Blocks) {
				s.sel = m.Blocks[i+1].ID
			} else if i > 0 {
				s.sel = m.Blocks[i-1].ID
			}
			return true
		}
	case ui.KeyEqual: // = and ⌘=
		s.zoomKey(2, m)
		return true
	case ui.KeyMinus: // − and ⌘−
		s.zoomKey(0.5, m)
		return true
	case ui.Key0:
		if e.Mods&ui.Cmd != 0 {
			s.fit()
			return true
		}
	}
	return false
}

// reveal scrolls so the block id is in view.
func (s *timelineState) reveal(m tlModel, id string) {
	for _, b := range m.Blocks {
		if b.ID == id {
			span := s.span(m.Duration)
			if b.Start < s.offset || b.End > s.offset+span {
				s.offset = b.Start - span*0.1
				s.clampOffset(m.Duration)
			}
		}
	}
}

// tlSnap finds what t snaps to (onsets, other blocks' edges, the
// playhead) within tlSnapPx.
func tlSnap(m tlModel, id string, t, pxPerSec float64) (float64, bool) {
	best, bestD := 0.0, float64(tlSnapPx)/pxPerSec
	found := false
	try := func(c float64) {
		if dd := math.Abs(c - t); dd <= bestD {
			best, bestD, found = c, dd, true
		}
	}
	for _, o := range m.Onsets {
		try(o)
	}
	for _, b := range m.Blocks {
		if b.ID != id {
			try(b.Start)
			try(b.End)
		}
	}
	if m.Playhead >= 0 {
		try(m.Playhead)
	}
	return best, found
}

// tlNearest is the onset nearest t (within 1 s).
func tlNearest(onsets []float64, t float64) (float64, bool) {
	best, bestD := 0.0, 1.0
	found := false
	for _, o := range onsets {
		if d := math.Abs(o - t); d < bestD {
			best, bestD, found = o, d, true
		}
	}
	return best, found
}

// tlClock is m:ss.s.
func tlClock(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	m := int(sec) / 60
	return fmt.Sprintf("%d:%04.1f", m, sec-float64(m*60))
}
