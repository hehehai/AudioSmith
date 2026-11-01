package main

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
)

// dubEditorOpen is a dub with its transcript in the editor.
func dubEditorOpen(t *testing.T) *dubTest {
	t.Helper()
	d := newDubTest(t, 6)
	d.a.dubSourceFile(d.src)
	d.a.dubStart()
	d.tt.Frame()
	d.tt.Frame()
	if d.a.dub == nil || len(d.a.dub.Segments) == 0 || d.a.dub.Source.Duration <= 0 {
		t.Fatalf("no transcript: %q", d.tt.Texts())
	}
	return d
}

func find(t *testing.T, tt *ui.Tester, label string) ui.Rect {
	t.Helper()
	r, ok := tt.Find(label)
	if !ok {
		t.Fatalf("%q not found: %q", label, tt.Texts())
	}
	return r
}

// TestDubTransportRow (thread #134): no footer; the step's action is the
// last control of the transport row, after the timeline's tools; the
// status is in the header, left of the step settings' toggle; the lane is
// at most tlLaneH tall.
func TestDubTransportRow(t *testing.T) {
	d := dubEditorOpen(t)
	tt := d.tt
	play := find(t, tt, "Play or pause")
	prim := find(t, tt, "Translate")
	zoom := find(t, tt, "Zoom in")
	start := find(t, tt, "Back to the start")
	lane := find(t, tt, "Timeline")
	mid := func(r ui.Rect) float32 { return r.Y + r.H/2 }
	for name, r := range map[string]ui.Rect{"primary": prim, "zoom in": zoom, "back to the start": start} {
		if math.Abs(float64(mid(r)-mid(play))) > 1 {
			t.Errorf("%s at y %.1f, the play button at %.1f: not one row", name, mid(r), mid(play))
		}
	}
	if !(play.X < start.X && start.X < zoom.X && zoom.X < prim.X) {
		t.Errorf("order: play %.0f, start %.0f, zoom %.0f, primary %.0f", play.X, start.X, zoom.X, prim.X)
	}
	if prim.Y+prim.H > lane.Y {
		t.Errorf("the primary (%.0f) is not above the timeline (%.0f)", prim.Y+prim.H, lane.Y)
	}
	if lane.H > tlLaneH+0.5 {
		t.Errorf("timeline %.1f tall, want ≤ %d", lane.H, tlLaneH)
	}
	// Beside the step panel, the skips and Fit are in ⋯; a wider window
	// has them in the row.
	_, fit := tt.Find("Fit all")
	_, more := tt.Find("Timeline tools")
	if fit || !more {
		t.Errorf("beside the panel: Fit all %v, ⋯ %v", fit, more)
	}
	tt.SetSize(1500, 800)
	tt.Frame()
	tt.Frame()
	for _, l := range []string{"Fit all", "Back 5 s", "Forward 5 s", "Zoom out"} {
		if r, ok := tt.Find(l); !ok || math.Abs(float64(mid(r)-mid(find(t, tt, "Play or pause")))) > 1 {
			t.Errorf("wide: %q not in the row (%v)", l, ok)
		}
	}
	if _, ok := tt.Find("Timeline tools"); ok {
		t.Error("wide: a ⋯ in the transport row")
	}
	// The status: the phase in the header, before the panel's toggle.
	st := find(t, tt, "Transcribed")
	toggle, ok := tt.Find("Hide the step settings")
	if !ok {
		toggle = find(t, tt, "Show the step settings")
	}
	if st.Y+st.H > titleBarHeight+1 || st.X+st.W > toggle.X+0.5 || math.Abs(float64(mid(st)-mid(toggle))) > 1 {
		t.Errorf("status %+v, toggle %+v: not in the header left of the toggle", st, toggle)
	}
	// Generate: the status counts, the primary stays (disabled) in place.
	d.a.dubTranslate(false)
	tt.Frame()
	if _, ok := tt.Find("Generate dub"); !ok {
		t.Errorf("no Generate dub: %q", tt.Texts())
	}
}

// TestDubStatusText: the header's short status and its tooltip.
func TestDubStatusText(t *testing.T) {
	for msg, want := range map[string]string{"3/6": "3/6", "line 12/40": "12/40", "(2/5)": "2/5", "a/b/c/d/e/f/g": "", "": "", "done": ""} {
		if got := dubProgressCount(msg); got != want {
			t.Errorf("%q → %q, want %q", msg, got, want)
		}
	}
	d := dubEditorOpen(t)
	d.a.dubTranslate(false)
	d.a.dubGenerate()
	d.tt.Frame()
	v := d.a.dubStatusNow()
	if v.tone != "ok" || v.short != i18n.T("Done") || v.full != "Every line is up to date." {
		t.Errorf("done: %+v", v)
	}
	if _, ok := d.tt.Find("Every line is up to date."); !ok {
		t.Errorf("the status's label is not the full sentence: %q", d.tt.Texts())
	}
	if _, ok := d.tt.Find("Regenerate 0 changed"); ok {
		t.Error("a primary when every line is up to date")
	}
}

// TestDubTimelinePlayhead (thread #134): the ruler seeks; the playhead's
// handle drags it from where it is; a drag on the empty lane does not
// seek; the cursor says which.
func TestDubTimelinePlayhead(t *testing.T) {
	d := dubEditorOpen(t)
	a, tt := d.a, d.tt
	r := find(t, tt, "Timeline")
	dur := a.dub.Source.Duration
	at := func(x float32) float64 { return float64((x-r.X)/r.W) * dur }
	near := func(got, want float64) bool { return math.Abs(got-want) < dur*3/float64(r.W) }
	rulerY := r.Y + 5
	// The ruler: a click seeks there.
	tt.ClickAt(r.X+r.W/2, rulerY)
	tt.Frame()
	if got := a.dubPosition(); !near(got, at(r.X+r.W/2)) {
		t.Fatalf("ruler click: at %.2f, want %.2f", got, at(r.X+r.W/2))
	}
	// The handle: a press beside its middle does not jump; a drag moves it.
	ph := r.X + r.W/2
	before := a.dubPosition()
	tt.Move(ph+3, rulerY)
	tt.Frame()
	if c := tt.Cursor(); c != ui.CursorResizeEW {
		t.Errorf("cursor over the handle %v", c)
	}
	tt.Press(ph+3, rulerY)
	tt.Frame()
	if got := a.dubPosition(); math.Abs(got-before) > 1e-6 {
		t.Errorf("pressing the handle jumped %.3f → %.3f", before, got)
	}
	tt.Move(ph+103, rulerY+30) // the drag goes on below the ruler
	tt.Release(ph+103, rulerY+30)
	tt.Frame()
	if got, want := a.dubPosition(), before+at(r.X+100); !near(got, want) {
		t.Errorf("handle drag: at %.2f, want %.2f", got, want)
	}
	// The empty lane: room between blocks (the third line starts later),
	// away from the playhead.
	s3 := a.dub.Segments[2]
	a.dubSetTiming(s3.ID, s3.Start+(s3.End-s3.Start)/2, s3.End, true)
	tt.Frame()
	m := a.dubTLModel()
	s := &a.dubTL
	phX, _ := s.playheadX(m)
	ex := float32(-1)
	for x := float32(4); x < r.W-4; x += 2 {
		if i, _ := s.blockAt(m, x); i < 0 && math.Abs(float64(x-phX)) > 12 {
			if j, _ := s.blockAt(m, x+30); j < 0 {
				ex = r.X + x
				break
			}
		}
	}
	if ex < 0 {
		t.Fatalf("no empty room in the lane: %+v (%.0f wide, %.2f s)", m.Blocks, r.W, dur)
	}
	bodyY := r.Y + r.H - 10
	tt.Move(ex, rulerY)
	tt.Frame()
	if c := tt.Cursor(); c != ui.CursorPointer {
		t.Errorf("cursor over the ruler %v", c)
	}
	before = a.dubPosition()
	tt.Press(ex, bodyY)
	tt.Move(ex+30, bodyY)
	tt.Release(ex+30, bodyY)
	tt.Frame()
	if got := a.dubPosition(); math.Abs(got-before) > 1e-6 {
		t.Errorf("a drag on the empty lane seeked %.3f → %.3f", before, got)
	}
	tt.ClickAt(ex, bodyY)
	tt.Frame()
	if got := a.dubPosition(); math.Abs(got-before) > 1e-6 {
		t.Errorf("a click on the empty lane seeked %.3f → %.3f", before, got)
	}
	// The transport: back to the start, and the keys.
	if err := tt.Click("Back to the start"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.dubPosition() != 0 {
		t.Errorf("back to the start: %.2f", a.dubPosition())
	}
	a.dubTransportKeys(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyL})
	if got := a.dubPosition(); !near(got, min(dubSkip, dur)) {
		t.Errorf("L: %.2f", got)
	}
	a.dubTransportKeys(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyJ})
	if a.dubPosition() != 0 {
		t.Errorf("J: %.2f", a.dubPosition())
	}
}

// TestDubTransportNarrow: at 820×520 and 125 % the transport row fits the
// workspace, its skips and zoom in a ⋯ menu, the primary last.
func TestDubTransportNarrow(t *testing.T) {
	t.Cleanup(func() { i18n.SetLocale("en"); gutterProbe = nil })
	for _, cfg := range []gutterCfg{gutterMin, {"min125-en", 820, 520, 1.25, false, false, true}} {
		d := gutterSeed(t, cfg)
		a, tt := d.a, d.tt
		a.navigate("/dub")
		a.openDub(a.dubList[0].ID)
		tt.Frame()
		a.dubTranslate(false)
		tt.Frame()
		tt.Frame()
		play := find(t, tt, i18n.T("Play or pause"))
		prim := find(t, tt, i18n.N(1, "Generate dub", "Generate %d dubs", 1))
		more := find(t, tt, i18n.T("Timeline tools"))
		lane := find(t, tt, i18n.T("Timeline"))
		if _, ok := tt.Find(i18n.T("Zoom in")); ok {
			t.Errorf("%s: zoom buttons in a narrow row", cfg.name)
		}
		if !(play.X < more.X && more.X+more.W <= prim.X+0.5) {
			t.Errorf("%s: play %.0f, ⋯ %.0f–%.0f, primary %.0f", cfg.name, play.X, more.X, more.X+more.W, prim.X)
		}
		if prim.X+prim.W > lane.X+lane.W+0.5 || prim.W < 40 {
			t.Errorf("%s: primary %.0f–%.0f past the lane's edge %.0f", cfg.name, prim.X, prim.X+prim.W, lane.X+lane.W)
		}
		// A step runs: the header still fits (the step's icon has the
		// ring, ⋯ has Cancel).
		a.dubJobs["fake"] = jobs.Job{ID: "fake", Kind: services.KindDubSynthesize, State: jobs.StateRunning, Progress: jobs.Progress{Fraction: 0.5, Message: "3/6"}}
		var tools ui.Rect
		gutterProbe = func(kind string, r ui.Rect) {
			if kind == "head-r" {
				tools = r
			}
		}
		tt.Frame()
		tt.Frame()
		gutterProbe = nil
		last := find(t, tt, i18n.T("Step %d: %s", 6, i18n.T("Export")))
		if last.X+last.W > tools.X+0.5 {
			t.Errorf("%s running: the steps end at %.1f, past the tools at %.1f", cfg.name, last.X+last.W, tools.X)
		}
		if _, ok := tt.Find(i18n.T("Cancel")); ok {
			t.Errorf("%s: a Cancel button in the narrow header", cfg.name)
		}
		delete(a.dubJobs, "fake")
	}
}
