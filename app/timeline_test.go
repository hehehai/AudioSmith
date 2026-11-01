package main

import (
	"fmt"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// tlLong is a 6.5-minute source in a 1000-DIP lane, as the user's.
func tlLong() (*timelineState, tlModel) {
	return &timelineState{zoom: 1, width: 1000}, tlModel{Duration: 390, Playhead: -1}
}

// tlTimeAt is the time the lane shows at x.
func tlTimeAt(s *timelineState, d float64, x float32) float64 {
	return s.offset + float64(x/s.width)*s.span(d)
}

func tlNear(a, b float64) bool { return math.Abs(a-b) < 1e-4 }

// TestTimelineZoomKeepsThePointersTime: zooming in and out by pinch or
// wheel keeps the time under the pointer where it is.
func TestTimelineZoomKeepsThePointersTime(t *testing.T) {
	s, m := tlLong()
	d := m.Duration
	for _, x := range []float32{120, 500, 873} {
		s.fit()
		before := tlTimeAt(s, d, x)
		for _, f := range []float64{1.3, 2, 1.7, 0.8, 3, 0.6} {
			s.zoomAt(f, x, d)
			if got := tlTimeAt(s, d, x); !tlNear(got, before) {
				t.Fatalf("x %v: zoom %.2f shows %.6f at the pointer, want %.6f", x, s.zoom, got, before)
			}
		}
	}
	// Through the event routing: ⌘+wheel, ⌥+wheel and a pinch.
	for _, e := range []ui.InputEvent{
		{Kind: ui.InputScroll, X: 300, DY: -40, Mods: ui.Cmd},
		{Kind: ui.InputScroll, X: 300, DY: -40, Mods: ui.Alt},
		{Kind: ui.InputMagnify, X: 300, Magnify: 0.25},
		{Kind: ui.InputMagnify, X: 300, Magnify: -0.1},
	} {
		before, z := tlTimeAt(s, d, 300), s.zoom
		if !timelineInput(s, m, tlEvents{}, e) {
			t.Fatalf("%+v not taken", e)
		}
		if got := tlTimeAt(s, d, 300); !tlNear(got, before) || s.zoom == z {
			t.Errorf("%+v: zoom %v → %v, time at the pointer %.4f → %.4f", e, z, s.zoom, before, got)
		}
	}
}

// TestTimelineZoomClamps: fit is the widest, tlMaxPxPerSec the closest
// (at least tlMinMaxZoom for a short source).
func TestTimelineZoomClamps(t *testing.T) {
	s, m := tlLong()
	s.setZoom(1000, 200, m.Duration)
	if want := 390.0 * tlMaxPxPerSec / 1000; s.zoom != want || !tlNear(float64(s.width)/s.span(m.Duration), tlMaxPxPerSec) {
		t.Errorf("closest zoom %v (%v px/s), want %v", s.zoom, float64(s.width)/s.span(m.Duration), want)
	}
	s.zoomAt(0.01, 400, m.Duration)
	if s.zoom != 1 || s.offset != 0 {
		t.Errorf("widest: zoom %v offset %v", s.zoom, s.offset)
	}
	short := &timelineState{zoom: 1, width: 1000}
	short.setZoom(100, 5, 10)
	if short.zoom != tlMinMaxZoom {
		t.Errorf("a short source zooms to %v", short.zoom)
	}
	if got := (&timelineState{}).maxZoom(390); got != tlNoWidthZoom {
		t.Errorf("before layout %v", got)
	}
	for z, want := range map[float64]string{1: "1×", 1.04: "1×", 2.5: "2.5×", 9.96: "10×", 38.6: "39×"} {
		if got := tlZoomLabel(z); got != want {
			t.Errorf("label %v = %q, want %q", z, got, want)
		}
	}
}

// TestTimelinePanBounds: panning stops at the source's ends, and does
// nothing at fit.
func TestTimelinePanBounds(t *testing.T) {
	s, m := tlLong()
	d := m.Duration
	pan := func(e ui.InputEvent) bool { return timelineInput(s, m, tlEvents{}, e) }
	if pan(ui.InputEvent{Kind: ui.InputScroll, DX: 50}) || s.offset != 0 {
		t.Errorf("fit: took a sideways scroll, offset %v", s.offset)
	}
	s.setZoom(8, 0, d)
	if !pan(ui.InputEvent{Kind: ui.InputScroll, DX: 1e6}) || !tlNear(s.offset, d-s.span(d)) {
		t.Errorf("past the end: offset %v, want %v", s.offset, d-s.span(d))
	}
	if !pan(ui.InputEvent{Kind: ui.InputScroll, DX: -1e6}) || s.offset != 0 {
		t.Errorf("before the start: offset %v", s.offset)
	}
	// ⇧ turns a mouse wheel sideways; 100 DIPs is 100/1000 of the view.
	if !pan(ui.InputEvent{Kind: ui.InputScroll, DY: 100, Mods: ui.Shift}) || !tlNear(s.offset, s.span(d)/10) {
		t.Errorf("shift+wheel: offset %v", s.offset)
	}
	// A vertical scroll is the page's.
	off := s.offset
	if pan(ui.InputEvent{Kind: ui.InputScroll, DY: 100}) || pan(ui.InputEvent{Kind: ui.InputScroll, DX: 10, DY: 30}) || s.offset != off {
		t.Errorf("vertical scroll taken: offset %v → %v", off, s.offset)
	}
	// The middle button drags the view; a plain press on the empty lane
	// does not seek (thread #134: it pans), a press on the ruler does.
	var sought []float64
	ev := tlEvents{Seek: func(at float64) { sought = append(sought, at) }}
	s.offset = 100
	timelineInput(s, m, ev, ui.InputEvent{Kind: ui.InputPointerDown, Button: 2, X: 500})
	timelineInput(s, m, ev, ui.InputEvent{Kind: ui.InputPointerMove, Button: 2, X: 400})
	timelineInput(s, m, ev, ui.InputEvent{Kind: ui.InputPointerUp, Button: 2, X: 400})
	if want := 100 + s.span(d)/10; !tlNear(s.offset, want) || len(sought) != 0 {
		t.Errorf("middle drag: offset %v want %v, seeks %v", s.offset, want, sought)
	}
	s.ruler = 16
	timelineInput(s, m, ev, ui.InputEvent{Kind: ui.InputPointerDown, X: 500, Y: 40})
	timelineInput(s, m, ev, ui.InputEvent{Kind: ui.InputPointerUp, X: 500, Y: 40})
	if len(sought) != 0 {
		t.Errorf("a press on the lane seeks: %v", sought)
	}
	timelineInput(s, m, ev, ui.InputEvent{Kind: ui.InputPointerDown, X: 500, Y: 5})
	timelineInput(s, m, ev, ui.InputEvent{Kind: ui.InputPointerUp, X: 500, Y: 5})
	if len(sought) != 1 || !tlNear(sought[0], tlTimeAt(s, d, 500)) {
		t.Errorf("a press on the ruler does not seek there: %v", sought)
	}
	// The overview: drag the window, press beside it to centre it there.
	s.ovW = 390 // 1 s a DIP
	s.offset = 100
	span := s.span(d)
	overviewInput(s, m, ui.InputEvent{Kind: ui.InputPointerDown, X: 110})
	overviewInput(s, m, ui.InputEvent{Kind: ui.InputPointerMove, X: 150})
	overviewInput(s, m, ui.InputEvent{Kind: ui.InputPointerUp, X: 150})
	if !tlNear(s.offset, 140) {
		t.Errorf("overview drag: offset %v", s.offset)
	}
	overviewInput(s, m, ui.InputEvent{Kind: ui.InputPointerDown, X: 380})
	overviewInput(s, m, ui.InputEvent{Kind: ui.InputPointerUp, X: 380})
	if !tlNear(s.offset, d-span) {
		t.Errorf("overview press at the end: offset %v want %v", s.offset, d-span)
	}
}

// TestTimelineKeysZoom: ⌘= and ⌘− zoom around the playhead, ⌘0 fits.
func TestTimelineKeysZoom(t *testing.T) {
	s, m := tlLong()
	m.Playhead = 100
	key := func(mods ui.Modifiers, k ui.Key) {
		timelineInput(s, m, tlEvents{}, ui.InputEvent{Kind: ui.InputKeyDown, Key: k, Mods: mods})
	}
	key(ui.Cmd, ui.KeyEqual)
	key(ui.Cmd, ui.KeyEqual)
	at := (m.Playhead - s.offset) / s.span(m.Duration)
	if s.zoom != 4 || !tlNear(at, 100.0/390) {
		t.Errorf("⌘= ⌘=: zoom %v, playhead at %.4f of the view", s.zoom, at)
	}
	key(ui.Cmd, ui.KeyMinus)
	if s.zoom != 2 {
		t.Errorf("⌘−: zoom %v", s.zoom)
	}
	key(ui.Cmd, ui.Key0)
	if s.zoom != 1 || s.offset != 0 {
		t.Errorf("⌘0: zoom %v offset %v", s.zoom, s.offset)
	}
}

// TestTimelineFollowsThePlayhead: while playing zoomed, the view turns a
// page as the playhead nears its right edge, but not for tlFollowPause
// after the user panned.
func TestTimelineFollowsThePlayhead(t *testing.T) {
	s, m := tlLong()
	d := m.Duration
	s.setZoom(10, 0, d) // 39 s a view
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	m.Playing, m.Playhead = true, 20
	s.follow(m, now)
	if s.offset != 0 {
		t.Fatalf("turned early: %v", s.offset)
	}
	m.Playhead = 38.5
	s.follow(m, now)
	if !tlNear(s.offset, 38.5-0.03*39) {
		t.Errorf("page turn: offset %v", s.offset)
	}
	timelineInput(s, m, tlEvents{}, ui.InputEvent{Kind: ui.InputScroll, DX: -2000, Time: now})
	m.Playhead = 200
	s.follow(m, now.Add(time.Second))
	if s.offset != 0 {
		t.Errorf("followed right after a pan: %v", s.offset)
	}
	s.follow(m, now.Add(tlFollowPause+time.Second))
	if !tlNear(s.offset, 200-0.03*39) {
		t.Errorf("did not follow later: %v", s.offset)
	}
	m.Playing, m.Playhead = false, 390
	s.follow(m, now.Add(time.Hour))
	if tlNear(s.offset, d-s.span(d)) {
		t.Error("followed while paused")
	}
}

// TestTimelineLabelsCollapse: dense labels skip the ones that would
// overlap, wide ones all show.
func TestTimelineLabelsCollapse(t *testing.T) {
	// 65 lines in 1000 DIPs, 15 DIPs apart, numbers 12 DIPs wide.
	var x, w, room []float32
	for i := range 65 {
		x = append(x, float32(i)*15+3)
		w = append(w, float32(len(fmt.Sprint(i+1))*tlLabelCharPx))
		room = append(room, 18)
	}
	show := tlLabels(x, w, room)
	n, last := 0, float32(-1e9)
	for i, ok := range show {
		if !ok {
			continue
		}
		if x[i] < last+tlLabelGap {
			t.Fatalf("label %d overlaps the one before", i+1)
		}
		n, last = n+1, x[i]+w[i]
	}
	if n == 0 || n > 40 { // 1–9 all (6 DIPs), then every other
		t.Errorf("%d of 65 labels shown", n)
	}
	// Wide blocks: every label; too narrow a block: none.
	if got := tlLabels([]float32{0, 100, 200}, []float32{12, 12, 18}, []float32{90, 90, 90}); !slices.Equal(got, []bool{true, true, true}) {
		t.Errorf("wide %v", got)
	}
	if got := tlLabels([]float32{0}, []float32{6}, []float32{17}); got[0] {
		t.Error("label in a block narrower than 18 DIPs")
	}
}

// TestTimelineGestures: in the Dub editor, ⌘+wheel and a pinch over the
// lane zoom it (MyGo patch 0012), a vertical wheel leaves it, and the
// overview strip shows once zoomed.
func TestTimelineGestures(t *testing.T) {
	d := newDubTest(t, 6)
	a, tt := d.a, d.tt
	a.dubSourceFile(d.src)
	a.dubStart()
	tt.Frame()
	tt.Frame()
	r, ok := tt.Find("Timeline")
	if !ok || a.dub == nil || a.dub.Source.Duration <= 0 {
		t.Fatalf("no timeline: %q", tt.Texts())
	}
	if _, ok := tt.Find("Scroll the timeline"); ok {
		t.Error("overview at fit")
	}
	y := r.Y + r.H/2
	tt.Scroll(r.X+r.W/2, y, 0, 40)
	if a.dubTL.zoom != 1 {
		t.Errorf("a vertical wheel zoomed: %v", a.dubTL.zoom)
	}
	tt.ScrollWith(ui.Cmd, r.X+r.W/4, y, 0, -40)
	z := a.dubTL.zoom
	if z <= 1 {
		t.Fatalf("⌘+wheel did not zoom: %v", z)
	}
	tt.Magnify(r.X+r.W/4, y, 0.5)
	if a.dubTL.zoom <= z {
		t.Errorf("pinch: zoom %v after %v", a.dubTL.zoom, z)
	}
	tt.Frame()
	if _, ok := tt.Find("Scroll the timeline"); !ok {
		t.Error("no overview when zoomed")
	}
	if !tt.HasText(tlZoomLabel(a.dubTL.zoom)) {
		t.Errorf("readout %q not shown", tlZoomLabel(a.dubTL.zoom))
	}
}
