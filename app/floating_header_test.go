package main

// The page header beside a floating panel (#127): a panel that runs from
// the window's top leaves the header's tools on its left; where the
// header could not stand beside it (820×520 @125 %), it keeps under the
// header. Either way every tool of the header shows in the window.

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/settings"
)

func TestFloatingPanelHeaderClear(t *testing.T) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	d, _ := headerFixture(t)
	a, tt := d.a, d.tt
	a.testMotion = true
	v := addVoice(t, a, core.Voice{Name: "Narrator", Kind: core.VoiceCloned, RefText: "Once upon a time.", Language: "en"}, 6)
	var fitted []ui.Rect
	headerProbe = func(e ui.Element) { fitted = append(fitted, e.Bounds()) }
	t.Cleanup(func() { headerProbe = nil })
	pages := []struct {
		name, panel string
		open        func()
	}{
		{"mine", "Voice details", func() { a.navigate("/voices/mine"); a.openVoiceEditor(v.ID) }},
		{"discover", "Voice details", func() {
			a.edit, a.gallery.detail = nil, ""
			a.navigate("/voices/discover")
			a.openArchetype("c-serena")
		}},
		{"assets", "Take details", func() { a.navigate("/assets"); a.history.selected = "T0" }},
		{"studio", "Text to speech panel", func() { a.navigate("/studio"); a.showStudioPanel(studioTabSettings) }},
		{"stories", "Inspector", func() {
			a.lfCreate(a.lfStories, func() { a.loadSampleStory(a.lfStories) })
			a.lfStories.panel = panelState{open: true, set: true}
		}},
		{"audiobook", "Inspector", func() {
			a.lfCreate(a.lfBook, func() { a.loadSample(a.lfBook) })
			a.lfBook.panel = panelState{open: true, set: true}
		}},
		{"dub-editor", "Step settings", func() { a.openDub(a.dubList[0].ID); a.dubSide = panelState{open: true, set: true} }},
		{"workflows", "Step and run", func() {
			a.navigate("/workflows")
			a.wfNew("translate-narrate")
			a.wfSel = a.wf.InputNode().ID
		}},
	}
	for _, cfg := range []struct {
		name  string
		w, h  int
		scale float64
	}{{"1100", 1100, 720, 1}, {"min125", 820, 520, 1.25}} {
		a.updateSettings(func(s *settings.Settings) { s.Appearance.Scale = cfg.scale })
		tt.SetSize(cfg.w, cfg.h)
		W, H := float32(cfg.w), float32(cfg.h)
		for _, p := range pages {
			clear(headSearchOpen)
			p.open()
			tt.Frame()
			tt.Advance(time.Second)
			tt.Frame()
			fitted = fitted[:0]
			tt.Frame()
			if tt.FramePending() {
				t.Errorf("%s %s: the header and the panel do not settle", cfg.name, p.name)
			}
			r, ok := tt.Find(p.panel)
			if !ok {
				t.Errorf("%s %s: no panel %q", cfg.name, p.name, p.panel)
				continue
			}
			full := r.Y < 0.5
			if r.Y < -0.5 {
				t.Errorf("%s %s: panel %+v above the window", cfg.name, p.name, r)
			}
			if cfg.name == "1100" && !full {
				t.Errorf("%s %s: panel %+v not from the window's top", cfg.name, p.name, r)
			}
			if !full && r.Y < pageTop {
				t.Errorf("%s %s: panel %+v over the header", cfg.name, p.name, r)
			}
			if abs32(W-(r.X+r.W)) > 0.5 || H-(r.Y+r.H) < 11 {
				t.Errorf("%s %s: panel %+v not flush right a gap from the bottom", cfg.name, p.name, r)
			}
			if len(fitted) == 0 {
				t.Errorf("%s %s: no header tools", cfg.name, p.name)
			}
			hr, _ := tt.Find("Page header")
			for _, c := range fitted {
				if c.X < hr.X || c.X+c.W > W {
					t.Errorf("%s %s: a header tool %+v out of the window", cfg.name, p.name, c)
				}
				if full && c.X+c.W > r.X {
					t.Errorf("%s %s: a header tool %+v under the panel at %.1f", cfg.name, p.name, c, r.X)
				}
			}
		}
	}
}

// While a panel slides in and out, the header's tools move with it: the
// last of them (New voice, after ⋯) never goes under it.
func TestFloatingPanelHeaderSlides(t *testing.T) {
	a, tt, v1, _ := mineWithVoices(t)
	th := ui.LightTheme()
	check := func(when string) {
		t.Helper()
		r, ok := tt.Find("Voice details")
		m, okm := findHeaderTool(tt, "More tools")
		if !ok || !okm {
			return // not narrow yet, or gone
		}
		if end := m.X + m.W + px(th, sp1) + headerControlH(th); end > r.X+0.5 {
			t.Errorf("%s: the header's tools end at %.1f, the panel at %.1f", when, end, r.X)
		}
	}
	a.openVoiceEditor(v1.ID)
	tt.Frame()
	for i := range 6 {
		check("in " + time.Duration(i*40*int(time.Millisecond)).String())
		tt.Advance(40 * time.Millisecond)
	}
	tt.Advance(time.Second)
	a.dismissPanel("voice-editor")()
	tt.Frame()
	for i := range 5 {
		check("out " + time.Duration(i*40*int(time.Millisecond)).String())
		tt.Advance(40 * time.Millisecond)
	}
}
