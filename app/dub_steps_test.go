package main

import (
	"image"
	"strconv"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
)

// dubStepLabel is step i's name for VoiceOver (in English).
func dubStepLabel(i int) string {
	return "Step " + strconv.Itoa(i+1) + ": " + []string{"Source", "Transcribe", "Translate", "Cast", "Generate", "Export"}[i]
}

// colourful reports whether img has, inside r (DIPs at scale), a pixel
// that is not a grey: an icon in the accent (or red).
func colourful(img *image.RGBA, r ui.Rect, scale float32) bool {
	for y := int((r.Y + 4) * scale); y < int((r.Y+r.H-4)*scale); y++ {
		for x := int((r.X + 4) * scale); x < int((r.X+r.W-4)*scale); x++ {
			c := img.RGBAAt(x, y)
			hi, lo := max(c.R, c.G, c.B), min(c.R, c.G, c.B)
			if int(hi)-int(lo) > 40 {
				return true
			}
		}
	}
	return false
}

// TestDubStepsInHeader: the Dub editor's steps are icon buttons in its
// header (no row under it); done ones are in the accent, the rest grey;
// a click shows a step; the tooltip says the step and its state.
func TestDubStepsInHeader(t *testing.T) {
	d := newDubTest(t, 3)
	a, tt := d.a, d.tt
	a.dubSourceFile(d.src)
	a.dubStart() // review mode: stops after the transcript
	tt.Frame()
	tt.Frame()
	if a.dubStep != dubStepTranscribe {
		t.Fatalf("step %d", a.dubStep)
	}
	img := tt.Image()
	var prev ui.Rect
	for i := range 6 {
		r, ok := tt.Find(dubStepLabel(i))
		if !ok {
			t.Fatalf("no %q: %q", dubStepLabel(i), tt.Texts())
		}
		if r.Y < 0 || r.Y+r.H > titleBarHeight {
			t.Errorf("%s at y %.1f–%.1f: not in the header (%v)", dubStepLabel(i), r.Y, r.Y+r.H, titleBarHeight)
		}
		if i > 0 && r.X <= prev.X+prev.W-0.5 {
			t.Errorf("%s at x %.1f overlaps the step before (%.1f)", dubStepLabel(i), r.X, prev.X+prev.W)
		}
		prev = r
		if r.W > 32 {
			t.Errorf("%s is %.1f wide: an icon only, no name", dubStepLabel(i), r.W)
		}
		done, _, _ := a.dubStepState(i)
		if got := colourful(img, r, 1); got != done {
			t.Errorf("%s: done %v, accent icon %v", dubStepLabel(i), done, got)
		}
	}
	// A click shows the step.
	if err := tt.Click(dubStepLabel(dubStepCast)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.dubStep != dubStepCast {
		t.Errorf("after clicking Cast: step %d", a.dubStep)
	}
	// The tooltip: its name and state.
	r, _ := tt.Find(dubStepLabel(dubStepTranscribe))
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	tt.Advance(2 * time.Second)
	tt.Frame()
	if !tt.HasText("Transcribe · Done") {
		t.Errorf("no tooltip 'Transcribe · Done': %q", tt.Texts())
	}
	if got := a.dubStepTip(dubStepCast, "Cast"); got != "Cast · Current step" && got != "Cast · Current step · Done" {
		t.Errorf("Cast's tip %q", got)
	}
	if got := a.dubStepTip(dubStepExport, "Export"); got != "Export · Not done" {
		t.Errorf("Export's tip %q", got)
	}
}

// TestDubStepsNarrowFit: in the smallest window at 125 %, Chinese and
// English, the six steps fit in the header between the dub's title (which
// gives way) and the tools.
func TestDubStepsNarrowFit(t *testing.T) {
	t.Cleanup(func() { i18n.SetLocale("en"); gutterProbe = nil })
	for _, cfg := range []gutterCfg{gutterMin, {"min125-en", 820, 520, 1.25, false, false, true}, gutterWide} {
		d := gutterSeed(t, cfg)
		d.a.navigate("/dub")
		d.a.openDub(d.a.dubList[0].ID)
		var title, tools ui.Rect
		gutterProbe = func(kind string, r ui.Rect) {
			switch kind {
			case "head-t":
				title = r
			case "head-r":
				tools = r
			}
		}
		d.tt.Frame()
		d.tt.Frame()
		gutterProbe = nil
		first, ok1 := d.tt.Find(i18n.T("Step %d: %s", 1, i18n.T("Source")))
		last, ok2 := d.tt.Find(i18n.T("Step %d: %s", 6, i18n.T("Export")))
		if !ok1 || !ok2 {
			t.Fatalf("%s: steps missing: %q", cfg.name, d.tt.Texts())
		}
		if title.W < 20 || title.X+title.W > first.X+0.5 {
			t.Errorf("%s: title %.1f+%.1f runs into the steps at %.1f", cfg.name, title.X, title.W, first.X)
		}
		if last.X+last.W > tools.X+0.5 {
			t.Errorf("%s: the steps end at %.1f, past the tools at %.1f", cfg.name, last.X+last.W, tools.X)
		}
		t.Logf("%s: title %.1f–%.1f, steps %.1f–%.1f, tools %.1f–%.1f", cfg.name, title.X, title.X+title.W, first.X, last.X+last.W, tools.X, tools.X+tools.W)
	}
}
