package main

// The timeline lane (M4): a source's waveform (a peaks pyramid), speech
// onsets as ticks, a playhead, and blocks — a dub's lines — to select,
// move and resize by dragging, with snapping to onsets, to the edges of
// neighbouring blocks and to the playhead (Alt turns it off), and to edit
// with the keyboard: ←/→ select, Enter enters precise mode, where ←/→
// nudge the start by 0.01 s (⌘: 0.1 s), ⇧ the end, ⌥ the whole block;
// S snaps the start to the nearest onset (⇧S the end), Delete deletes it.
// Overlapping blocks are red.
//
// Zoom (thread #107), as Logic, Final Cut and Audacity have it: a pinch,
// ⌘ or ⌥ with the wheel, ⌘= and ⌘−, anchored at the pointer, from fit
// (1×) to tlMaxPxPerSec; ⌘0 or a double click on the readout fits. Pan: a
// sideways swipe (with the system's inertia), ⇧ with the wheel, a drag
// with the middle button or on the empty lane, or the overview strip, which
// shows the window over the whole source. A plain vertical wheel scrolls
// the page. Playing while zoomed, the view turns a page as the playhead
// reaches its edge, unless the user panned in the last tlFollowPause.
//
// The playhead (thread #134, user 11-01): a press on the time ruler along
// the lane's top seeks there and drags on as a scrub; the playhead's
// handle in the ruler (or its line, off a block's edge) drags it from
// where it is; a press on a block selects it without moving the
// playhead (a double click plays it); the empty lane never seeks
// (timelinePress).
//
// It is one element painted by Draw and driven by HandleInput: the
// pointer and keys reach it before the next frame, and a drag changes
// only the lane's state until the pointer lets go, which commits one
// change (one undo step).

import (
	"math"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// Timeline limits.
const (
	// tlMaxPxPerSec is the closest zoom: 2-DIP waveform bars are then
	// 20 ms, the peaks' second level (dub.PeaksPerSecond is 100).
	tlMaxPxPerSec = 100
	tlMinMaxZoom  = 4  // a short source still zooms this far
	tlNoWidthZoom = 16 // the limit before the lane is laid out
	tlSnapPx      = 8  // snapping distance
	tlEdgePx      = 5  // grab zone of a block's edge
	tlMinGrabPx   = 16 // narrower blocks only select
	tlResizePx    = 24 // narrower blocks only move
	tlLabelGap    = 14 // DIPs between two block labels: more than a number's width
	tlLabelCharPx = 6  // a digit of a label at 10 DIPs
	tlFollowPause = 4 * time.Second
)

// timelineState is a lane's view state.
type timelineState struct {
	zoom   float64 // 1 (fit) … maxZoom
	offset float64 // seconds at the left edge
	sel    string  // the selected block
	edit   bool    // precise editing (Enter)
	drag   *tlDrag
	pan    *tlPan    // a middle-button drag, or the overview's
	panned time.Time // when the user last panned or zoomed
	width  float32   // the lane's width as last drawn
	ruler  float32   // the time ruler's height at the lane's top (tlRulerH)
	unit   float32   // a DIP at the theme's scale
	hoverX float32   // the pointer over the lane, for its cursor
	hoverY float32
	hover  bool
	ovW    float32 // the overview's
	focus  ui.Handle
	bars   tlBars // the waveform as last drawn
	ovBars tlBars // the overview's
}

// tlDrag is a drag in progress.
type tlDrag struct {
	id         string
	mode       int // tlMove, tlStart, tlEnd, tlSeek
	x0         float32
	grab       float64   // tlSeek: the playhead's time less the pointer's
	seeked     time.Time // tlSeek: when the drag last seeked
	start, end float64   // as the drag started
	curS, curE float64   // where it is now
	moved      bool
}

const (
	tlMove = iota
	tlStart
	tlEnd
	tlSeek // the playhead, by its handle or the ruler
)

// tlPan is a pan in progress: the pointer's x and the offset as it
// began; grab is the seconds between the overview window's left edge and
// the pointer.
type tlPan struct {
	x0   float32
	off0 float64
	grab float64
}

// tlBlock is a block of the lane.
type tlBlock struct {
	ID         string
	Start, End float64
	Label      string // index number
	Overlap    bool
	Muted      bool
	Flag       bool // a problem (won't fit, verify)
}

// tlModel is what a lane shows.
type tlModel struct {
	Duration float64
	Blocks   []tlBlock // in time order
	Peaks    *dub.Peaks
	Onsets   []float64
	Playhead float64 // seconds; < 0 = none
	Playing  bool    // the view follows the playhead
}

// tlEvents are what a lane asks of its owner.
type tlEvents struct {
	Seek   func(t float64)
	Select func(id string)
	// Change moves a block (clamp: keep it between its neighbours).
	Change func(id string, start, end float64, clamp bool)
	Nudge  func(id, edge string, delta float64)
	Delete func(id string)
	Play   func(id string)
	// Command performs an Edit menu command (undo, redo) that came to
	// the focused lane, and reports whether it did.
	Command func(name string) bool
	// Key takes a key the lane leaves (the owner's transport keys); nil none.
	Key func(e ui.InputEvent) bool
}

// maxZoom is the closest zoom: tlMaxPxPerSec, at least tlMinMaxZoom.
func (s *timelineState) maxZoom(d float64) float64 {
	if s.width <= 0 || d <= 0 {
		return tlNoWidthZoom
	}
	return max(d*tlMaxPxPerSec/float64(s.width), tlMinMaxZoom)
}

// span is the seconds the lane shows.
func (s *timelineState) span(d float64) float64 {
	return d / min(max(s.zoom, 1), s.maxZoom(d))
}

// clampOffset keeps the view inside the source.
func (s *timelineState) clampOffset(d float64) {
	s.offset = min(max(s.offset, 0), max(d-s.span(d), 0))
}

// setZoom zooms to z around the time at, which stays where it shows.
func (s *timelineState) setZoom(z, at, d float64) {
	if d <= 0 || math.IsNaN(z) {
		return
	}
	old := s.span(d)
	frac := 0.5
	if old > 0 {
		frac = (at - s.offset) / old
	}
	s.zoom = min(max(z, 1), s.maxZoom(d))
	s.offset = at - frac*s.span(d)
	s.clampOffset(d)
}

// zoomAt zooms by factor around the lane's x, as a pinch or ⌘+wheel.
func (s *timelineState) zoomAt(factor float64, x float32, d float64) {
	if s.width <= 0 {
		return
	}
	s.setZoom(s.zoom*factor, s.offset+float64(x/s.width)*s.span(d), d)
}

// zoomKey zooms by factor around the playhead when it shows, else the
// middle, as the buttons and ⌘=/⌘− do.
func (s *timelineState) zoomKey(factor float64, m tlModel) {
	span := s.span(m.Duration)
	at := s.offset + span/2
	if m.Playhead >= s.offset && m.Playhead <= s.offset+span {
		at = m.Playhead
	}
	s.setZoom(s.zoom*factor, at, m.Duration)
}

// fit shows the whole source.
func (s *timelineState) fit() { s.zoom, s.offset = 1, 0 }

// panBy moves the view by dx DIPs of the lane.
func (s *timelineState) panBy(dx float32, d float64) {
	if s.width <= 0 {
		return
	}
	s.offset += float64(dx/s.width) * s.span(d)
	s.clampOffset(d)
}

// follow turns the page as the playhead reaches the view's edge while it
// plays, unless the user panned or zoomed in the last tlFollowPause.
func (s *timelineState) follow(m tlModel, now time.Time) {
	d := m.Duration
	if !m.Playing || s.zoom <= 1 || s.drag != nil || s.pan != nil || m.Playhead < 0 || now.Sub(s.panned) < tlFollowPause {
		return
	}
	span := s.span(d)
	if m.Playhead < s.offset || m.Playhead > s.offset+span*0.97 {
		s.offset = m.Playhead - span*0.03
		s.clampOffset(d)
	}
}

// tlZoomLabel is the readout: 1×, 2.5×, 12×.
func tlZoomLabel(z float64) string {
	if z < 9.95 {
		return strconv.FormatFloat(math.Round(z*10)/10, 'f', -1, 64) + "×"
	}
	return strconv.Itoa(int(math.Round(z))) + "×"
}

// tlLabels picks the block labels to draw, left to right: a label at x
// (its left edge) w DIPs wide in a block room DIPs wide shows when it fits
// its block (at least 18 DIPs) and starts tlLabelGap past the last one
// shown, so dense lines
// at a low zoom keep every block's outline but every other number or
// fewer.
func tlLabels(x, w, room []float32) []bool {
	show := make([]bool, len(x))
	end := float32(math.Inf(-1))
	for i := range x {
		if w[i] <= 0 || room[i] < max(w[i]+6, 18) || x[i] < end+tlLabelGap {
			continue
		}
		show[i], end = true, x[i]+w[i]
	}
	return show
}

// tlBars keeps the waveform's bars for one view, so frames that show the
// same reuse them.
type tlBars struct {
	peaks    *dub.Peaks
	from, to float64
	v        []float32
}

func (b *tlBars) get(p *dub.Peaks, from, to float64, n int) []float32 {
	if b.peaks != p || b.from != from || b.to != to || len(b.v) != n {
		b.peaks, b.from, b.to, b.v = p, from, to, p.Bars(from, to, n)
	}
	return b.v
}

// tlLaneH is the lane's height and tlRulerH its time ruler's, DIPs at
// 100 % (user, 11-01: lower than the 86 + tools row it was).
const (
	tlLaneH  = 76
	tlRulerH = 16
)

// timelineLane draws a lane and handles its input. Its zoom controls
// are the owner's to place (timelineZoomTools: the Dub editor's
// transport row).
func timelineLane(c *ui.Context, key string, s *timelineState, m tlModel, ev tlEvents) {
	t := c.Theme()
	d := m.Duration
	if d <= 0 {
		return
	}
	s.zoom = min(max(s.zoom, 1), s.maxZoom(d))
	s.clampOffset(d)
	s.follow(m, c.Now())
	s.unit, s.ruler = px(t, 1), px(t, tlRulerH)
	k := tok(t)
	if s.edit {
		ui.Row(c.Key(key + "-edit")).Shrink(0).Children(func() {
			tagChip(c, i18n.T("Precise editing: ←/→ nudge · ⇧ end · ⌥ move · S snap · Esc")).Shrink(1).MinWidth(0).Ellipsis("…")
		})
	}
	if s.zoom > 1 {
		timelineOverview(c, key+"-pan", s, m)
	}
	lane := ui.Box(c.Key(key)).Height(px(t, tlLaneH)).Shrink(0).Radius(px(t, radiusControl)).Background(k.Subtle).Border(1, k.Line).Focusable().Bind(&s.focus).
		Label(i18n.T("Timeline")).Cursor(s.cursor(m))
	lane.Draw(func(p *ui.Painter, r ui.Rect) { timelinePaint(p, r, k, s, m) })
	lane.HandleInput(func(e ui.InputEvent) bool {
		return timelineInput(s, m, ev, e)
	})
}

// timelineZoomTools are the lane's zoom controls: zoom out, the zoom (its
// tooltip says the range shown and the gestures; a double click fits),
// zoom in, fit (with fit; else ⋯ has it) — the order of the Workflows
// canvas.
func timelineZoomTools(c *ui.Context, s *timelineState, m tlModel, fit bool) {
	t := c.Theme()
	k := tok(t)
	d := m.Duration
	if iconButton(c.Key("zoom-out"), icons.MagnifierZoomOut, i18n.T("Zoom out")).Disabled(s.zoom <= 1).Clicked() {
		s.zoomKey(0.5, m)
	}
	level := ui.Box(c.Key("zoom-level")).Shrink(0).Padding(0, px(t, 2)).Label(i18n.T("Zoom")).
		Tooltip(tlRange(s, d) + "\n" + i18n.T("⌘+scroll or pinch to zoom · ⇧+scroll or swipe sideways to pan · double-click to fit (⌘0)"))
	level.Children(func() {
		ui.Text(c, tlZoomLabel(s.zoom)).Font(monoFont).FontSize(px(t, fsMicro)).TextColor(k.Text2).SingleLine()
	})
	if level.DoubleClicked() {
		s.fit()
	}
	if iconButton(c.Key("zoom-in"), icons.MagnifierZoomIn, i18n.T("Zoom in")).Disabled(s.zoom >= s.maxZoom(d)).Clicked() {
		s.zoomKey(2, m)
	}
	if fit && iconButton(c.Key("zoom-fit"), icons.FullScreenSquare, i18n.T("Fit all")).Disabled(s.zoom <= 1).Clicked() {
		s.fit()
	}
}

// timelineZoomMenu is timelineZoomTools in a menu: zoom in and out with
// zoom, and fit.
func timelineZoomMenu(mn *ui.Menu, s *timelineState, m tlModel, zoom bool) {
	if zoom && mn.Item(i18n.T("Zoom in")).Disabled(s.zoom >= s.maxZoom(m.Duration)).Chosen() {
		s.zoomKey(2, m)
	}
	if zoom && mn.Item(i18n.T("Zoom out")).Disabled(s.zoom <= 1).Chosen() {
		s.zoomKey(0.5, m)
	}
	if mn.Item(i18n.T("Fit all")).Disabled(s.zoom <= 1).Chosen() {
		s.fit()
	}
}

// tlRange is the time the lane shows: 0:12.0 – 0:24.6 / 1:02.3.
func tlRange(s *timelineState, d float64) string {
	return tlClock(s.offset) + " – " + tlClock(min(s.offset+s.span(d), d)) + " / " + tlClock(d)
}

// showTime scrolls a zoomed view so the time sec shows, a tenth in from
// its edge (the transport's skips).
func (s *timelineState) showTime(sec, d float64) {
	span := s.span(d)
	if sec < s.offset || sec > s.offset+span {
		s.offset = sec - span*0.1
		s.clampOffset(d)
	}
}

// timelineOverview is the strip that shows the view over the whole source
// while zoomed: drag its window, or press beside it to jump there.
func timelineOverview(c *ui.Context, key string, s *timelineState, m tlModel) {
	t := c.Theme()
	k := tok(t)
	strip := ui.Box(c.Key(key)).Height(px(t, 18)).Shrink(0).Radius(px(t, 4)).Background(k.Subtle).Border(1, k.Line).
		Focusable().Label(i18n.T("Scroll the timeline")).Cursor(ui.CursorGrab)
	strip.Draw(func(p *ui.Painter, r ui.Rect) { overviewPaint(p, r, k, s, m) })
	strip.HandleInput(func(e ui.InputEvent) bool { return overviewInput(s, m, e) })
}
