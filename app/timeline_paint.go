package main

// Painting the timeline lane — its ruler, waveform, blocks and playhead
// with its handle — and its overview strip (timeline.go).

import (
	"fmt"
	"math"
	"strconv"

	"github.com/egoist/mygo/ui"
)

// tlRulerSteps are the ruler's label steps in seconds; it takes the
// first that leaves tlRulerGap DIPs between two labels.
var tlRulerSteps = []float64{0.1, 0.2, 0.5, 1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 1200}

const tlRulerGap = 56

// tlRulerStep is the ruler's step at pxPerSec, in a lane at unit (a DIP).
func tlRulerStep(pxPerSec float64, unit float32) float64 {
	for _, st := range tlRulerSteps {
		if st*pxPerSec >= float64(tlRulerGap*max(unit, 1)) {
			return st
		}
	}
	return tlRulerSteps[len(tlRulerSteps)-1]
}

// tlRulerLabel is a ruler's label: 0:05, 1:30, or 0:01.5 for steps
// under a second.
func tlRulerLabel(sec, step float64) string {
	if step < 1 {
		return tlClock(sec)
	}
	m := int(sec+0.5) / 60
	return strconv.Itoa(m) + ":" + fmt.Sprintf("%02d", int(sec+0.5)-m*60)
}

// timelinePaint paints the lane in r: the time ruler along its top, then
// the waveform, onsets, the blocks and the playhead with its handle.
func timelinePaint(p *ui.Painter, r ui.Rect, k *tokens, s *timelineState, m tlModel) {
	d := m.Duration
	s.width = r.W
	s.zoom = min(s.zoom, s.maxZoom(d))
	s.clampOffset(d)
	span := s.span(d)
	u := max(s.unit, 0.5)
	accent, danger, muted := k.Accent, k.Danger, k.Text3
	border, text := k.Line, k.Text
	x := func(sec float64) float32 { return r.X + float32((sec-s.offset)/span)*r.W }
	ruler := min(s.ruler, r.H/3)
	body := ui.Rect{X: r.X, Y: r.Y + ruler, W: r.W, H: r.H - ruler}
	// The ruler: ticks at its step (halves between), labelled, and a
	// faint grid line under each labelled tick.
	pps := float64(r.W) / span
	step := tlRulerStep(pps, u)
	knob, hasKnob := s.playheadX(m)
	knob += r.X
	if ruler > 0 {
		p.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W, H: ruler}, text.Alpha(0.035), 0)
		p.Line(r.X, body.Y, r.X+r.W, body.Y, 1, border)
		for i := int(math.Floor(s.offset / step * 2)); float64(i)*step/2 <= s.offset+span; i++ {
			at := float64(i) * step / 2
			if at < s.offset {
				continue
			}
			tx := x(at)
			if i%2 != 0 {
				p.Line(tx, body.Y-3*u, tx, body.Y, 1, muted.Alpha(0.6))
				continue
			}
			p.Line(tx, body.Y-6*u, tx, body.Y, 1, muted)
			if tx > r.X+1 {
				p.Line(tx, body.Y, tx, r.Y+r.H, 1, border.Alpha(0.5))
			}
			// A label the playhead's handle would cover is left out.
			if covered := hasKnob && knob+6*u > tx && knob-6*u < tx+3*u+24*u; tx+3*u < r.X+r.W-20*u && !covered {
				p.Text(tx+3*u, r.Y+2*u, tlRulerLabel(at, step), 9*u, text.Alpha(0.55))
			}
		}
	}
	// The waveform: a bar every 2 DIPs from the pyramid's level for the
	// zoom, kept while the view stays.
	if m.Peaks != nil {
		n := max(int(r.W/2), 1)
		bars := s.bars.get(m.Peaks, s.offset, s.offset+span, n)
		mid := body.Y + body.H/2
		w := r.W / float32(n)
		for i, v := range bars {
			h := v * (body.H - 8*u) / 2
			if h < 0.5 {
				continue
			}
			p.Fill(ui.Rect{X: r.X + float32(i)*w, Y: mid - h, W: max(w-0.5, 0.5), H: 2 * h}, text.Alpha(0.28), 0)
		}
	}
	// Onsets.
	for _, o := range m.Onsets {
		if o < s.offset || o > s.offset+span {
			continue
		}
		ox := x(o)
		p.Line(ox, r.Y+r.H-5*u, ox, r.Y+r.H-1, 1, muted)
	}
	// Blocks, then their labels, the ones that have room.
	var lx, lw, lroom []float32
	var lbl []string
	for _, b := range m.Blocks {
		start, end := b.Start, b.End
		if s.drag != nil && s.drag.id == b.ID && s.drag.mode != tlSeek {
			start, end = s.drag.curS, s.drag.curE
		}
		if end < s.offset || start > s.offset+span {
			continue
		}
		// Zoomed in, a block cut by the lane's edge is drawn up to it.
		bx, bw := x(start), max(x(end)-x(start), 1)
		if bx < r.X {
			bw, bx = max(bw-(r.X-bx), 1), r.X
		}
		bw = max(min(bw, r.X+r.W-bx), 1)
		// Lines are grey blocks; the chosen one is in the accent,
		// overlaps in the danger colour.
		col, fill := text, float32(0.08)
		if b.Muted {
			fill = 0.04
		}
		if b.ID == s.sel {
			col, fill = accent, 0.18
		}
		if b.Overlap {
			col, fill = danger, 0.16
		}
		rect := ui.Rect{X: bx, Y: body.Y + 5*u, W: bw, H: body.H - 12*u}
		p.Fill(rect, col.Alpha(fill), 3)
		if b.ID == s.sel {
			p.Stroke(rect, col, 3, 2)
			if s.edit {
				p.Line(bx, rect.Y-3*u, bx, rect.Y+rect.H+3*u, 2, col)
			}
		} else if b.Flag {
			p.Stroke(rect, k.Warning, 3, 1)
		} else {
			p.Stroke(rect, text.Alpha(0.14), 3, 1)
		}
		if b.Label != "" {
			lx, lw, lroom, lbl = append(lx, bx+3), append(lw, float32(len(b.Label)*tlLabelCharPx)*u), append(lroom, bw), append(lbl, b.Label)
		}
	}
	for i, ok := range tlLabels(lx, lw, lroom) {
		if ok {
			p.Text(lx[i], body.Y+6*u, lbl[i], 10*u, text.Alpha(0.8))
		}
	}
	// The playhead: its line, and its handle in the ruler (a tab with a
	// point, which a drag takes along).
	if px, ok := s.playheadX(m); ok {
		px += r.X
		p.Line(px, body.Y, px, r.Y+r.H, 2, accent)
		if ruler > 0 {
			hw, top, bot := 5*u, r.Y+1, body.Y
			var path ui.Path
			path.MoveTo(px-hw, top).LineTo(px+hw, top).LineTo(px+hw, bot-hw).LineTo(px, bot).LineTo(px-hw, bot-hw).Close()
			p.FillPath(&path, accent)
		} else {
			p.Line(px, r.Y, px, body.Y, 2, accent)
		}
	}
}

// overviewPaint paints the overview strip: the whole source's waveform,
// the lines, the window the lane shows and the playhead.
func overviewPaint(p *ui.Painter, r ui.Rect, k *tokens, s *timelineState, m tlModel) {
	d := m.Duration
	s.ovW = r.W
	x := func(sec float64) float32 { return r.X + float32(sec/d)*r.W }
	if m.Peaks != nil {
		n := max(int(r.W/2), 1)
		mid, w := r.Y+r.H/2, r.W/float32(n)
		for i, v := range s.ovBars.get(m.Peaks, 0, d, n) {
			if h := v * (r.H - 4) / 2; h >= 0.5 {
				p.Fill(ui.Rect{X: r.X + float32(i)*w, Y: mid - h, W: max(w-0.5, 0.5), H: 2 * h}, k.Text.Alpha(0.18), 0)
			}
		}
	}
	for _, b := range m.Blocks {
		col := k.Text.Alpha(0.35)
		if b.ID == s.sel {
			col = k.Accent
		}
		p.Fill(ui.Rect{X: x(b.Start), Y: r.Y + r.H - 4, W: max(x(b.End)-x(b.Start), 1), H: 2}, col, 0)
	}
	win := ui.Rect{X: x(s.offset), Y: r.Y + 1, W: max(x(s.offset+s.span(d))-x(s.offset), 4), H: r.H - 2}
	p.Fill(win, k.Accent.Alpha(0.16), 3)
	p.Stroke(win, k.Accent, 3, 1.5)
	if m.Playhead >= 0 {
		px := x(m.Playhead)
		p.Line(px, r.Y, px, r.Y+r.H, 1.5, k.Accent)
	}
}
