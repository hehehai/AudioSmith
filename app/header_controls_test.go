package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// headerPage is a page whose header tools a test checks: how to open it
// and the labels of its tools ("A|B": either; the first found).
type headerPage struct {
	name  string
	open  func(a *app)
	tools []string
}

// headerFixture is an app with something on every page: takes, dubs, a
// transcript, an isolation, a story, an audiobook, a workflow.
func headerFixture(t *testing.T) (*dubTest, []headerPage) {
	d := newDubTest(t, 3)
	a, eng := d.a, d.eng
	now := time.Now()
	for i, text := range []string{"欢迎收听本期节目。", "The last ferry left the harbour at nine."} {
		addTake(t, a, core.Take{ID: fmt.Sprintf("T%d", i), CreatedAt: now.Add(-time.Duration(i) * time.Hour), Text: text, Model: kokoro, Voice: "af_heart", AudioSeconds: 3}, 0.3)
	}
	for _, name := range []string{"产品发布会.wav", "Interview.wav"} {
		src := filepath.Join(t.TempDir(), name)
		copyTestFile(t, d.src, src)
		a.dubStartFile(src)
	}
	tr := putTranscript(t, a, sampleTranscript("t1", now.Add(-3*time.Hour)))
	fakeOnDisk(t, a, isoSeparator())
	fakeOnDisk(t, a, isoDenoiser())
	a.isoEngine = isoFakeEngine(nil)
	eng.executors[services.KindIsolate] = a.isoSvc.Executor()
	src := filepath.Join(t.TempDir(), "采访原声.wav")
	copyTestFile(t, d.src, src)
	a.isoStart(src)
	d.tt.Frame()
	inspector := "Show the inspector|Hide the inspector"
	pages := []headerPage{
		{"home", func(a *app) { a.navigate("/home") }, nil},
		{"studio", func(a *app) { a.navigate("/studio") }, []string{"Show settings and history|Hide settings and history"}},
		{"voices-discover", func(a *app) { a.navigate("/voices/discover") }, []string{"Search Discover voices", "Favorites"}},
		{"voices-mine", func(a *app) { a.navigate("/voices/mine") }, []string{"Search voices", "Kind", "Import…"}},
		{"voices-new", func(a *app) { a.navigate("/voices/new") }, []string{"My voices ›"}},
		{"clone", func(a *app) { a.startClone("") }, []string{"Cancel"}},
		{"design", func(a *app) { a.startDesign() }, []string{"New design", "Discover voices"}},
		{"projects", func(a *app) { a.navigate("/projects") }, []string{"Search projects", "Type", "List (⌥1)", "Cards (⌥2)", "New"}},
		{"assets", func(a *app) { a.navigate("/assets") }, []string{"Search assets", "Type", "Source", "Open folder"}},
		{"dub-list", func(a *app) { a.navigate("/dub") }, []string{"Search dubs", "Status", "Languages"}},
		{"dub-editor", func(a *app) { a.openDub(a.dubList[0].ID) }, []string{"Show the step settings|Hide the step settings", "More actions"}},
		{"transcripts", func(a *app) { a.navigate("/transcripts") }, []string{"Search transcripts", "Export all"}},
		{"transcript", func(a *app) { a.navigate("/transcripts/" + tr.ID) }, nil},
		{"isolate", func(a *app) { a.navigate("/isolate") }, []string{"Search isolations"}},
		{"isolate-result", func(a *app) { a.navigate("/isolate/" + a.isoList[0].ID) }, []string{"Isolation actions"}},
		{"dictation", func(a *app) { a.navigate("/dictation") }, []string{"Settings…"}},
		{"stories", func(a *app) {
			a.lfCreate(a.lfStories, func() { a.loadSampleStory(a.lfStories) })
		}, []string{"Import…", "Add", inspector, "More actions"}},
		{"stories-list", func(a *app) { a.lfClose(a.lfStories); a.navigate("/stories") }, []string{"Search stories"}},
		{"audiobook", func(a *app) {
			a.lfCreate(a.lfBook, func() { a.loadSample(a.lfBook) })
		}, []string{"Import…", inspector, "More actions"}},
		{"workflows", func(a *app) { a.navigate("/workflows"); a.wfNew("translate-narrate") }, []string{"Show workflows and steps|Hide workflows and steps", "Run workflow"}},
		{"batch", func(a *app) { a.navigate("/batch") }, []string{"Dub page"}},
		{"calls", func(a *app) { a.navigate("/calls") }, []string{"Incoming calls…", "Set up Twilio"}},
		{"calls-setup", func(a *app) { a.navigate("/calls/setup") }, []string{"Turn on calls", "Open Calls"}},
		{"developers", func(a *app) { a.navigate("/developers") }, []string{"API reference", "Developer sections"}},
		{"api-reference", func(a *app) { a.navigate("/api-reference") }, []string{"Open in the browser", "Settings…"}},
		{"tasks", func(a *app) { a.navigate("/tasks") }, nil},
	}
	return d, pages
}

// findHeaderTool finds the tool labelled l ("A|B": either) in the header.
func findHeaderTool(tt *ui.Tester, l string) (ui.Rect, bool) {
	for _, s := range strings.Split(l, "|") {
		if r, ok := tt.Find(s); ok && r.Y < float32(titleBarHeight) {
			return r, true
		}
	}
	return ui.Rect{}, false
}

// Every control in a page header — search, filters, segmented controls,
// icon buttons, labelled buttons, the primary button — stands as tall as
// the search field (headerControlH) and on the same middle (user, 10-31).
func TestHeaderControlsOneHeight(t *testing.T) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	d, pages := headerFixture(t)
	a, tt := d.a, d.tt
	// Every control the kit fitted in a header, with its box.
	var fitted []ui.Rect
	headerProbe = func(e ui.Element) { fitted = append(fitted, e.Bounds()) }
	t.Cleanup(func() { headerProbe = nil })
	for _, cfg := range []struct {
		name  string
		w, h  int
		scale float64
	}{{"1300", 1300, 760, 1}, {"min125", 820, 520, 1.25}} {
		a.updateSettings(func(s *settings.Settings) { s.Appearance.Scale = cfg.scale })
		tt.SetSize(cfg.w, cfg.h)
		a.navigate("/assets")
		tt.Frame()
		tt.Frame()
		sr, _ := tt.Find("Search assets")
		want := sr.H
		t.Logf("%s: search %.2f", cfg.name, want)
		for _, p := range pages {
			clear(headSearchOpen)
			p.open(a)
			tt.Frame()
			tt.Frame()
			fitted = fitted[:0]
			tt.Frame()
			hr, _ := tt.Find("Page header")
			mid := hr.Y + hr.H/2
			for _, l := range p.tools {
				r, ok := findHeaderTool(tt, l)
				if !ok {
					if cfg.name == "1300" {
						t.Errorf("%s %s: no %q", cfg.name, p.name, l)
					}
					continue
				}
				if d := r.H - want; d > 1 || d < -1 {
					t.Errorf("%s %s: %q is %.1f tall, want %.1f", cfg.name, p.name, l, r.H, want)
				}
				if d := r.Y + r.H/2 - mid; d > 1 || d < -1 {
					t.Errorf("%s %s: %q's middle at %.1f, the header's %.1f", cfg.name, p.name, l, r.Y+r.H/2, mid)
				}
			}
			// And whatever else the header holds (the Voices tabs, New voice).
			for _, r := range fitted {
				if d := r.H - want; d > 1 || d < -1 || r.Y+r.H/2-mid > 1 || mid-r.Y-r.H/2 > 1 {
					t.Errorf("%s %s: a control at %+v, want %.1f tall on %.1f", cfg.name, p.name, r, want, mid)
				}
			}
			if len(p.tools) > 0 && len(fitted) == 0 {
				t.Errorf("%s %s: no control fitted to the header", cfg.name, p.name)
			}
		}
	}
}
