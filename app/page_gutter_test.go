package main

// The page gutter (#130): on every page with a header, the body's content
// lines up with the header's: its leftmost box (a section label, a card,
// a row's band, a field) on the title's left edge and its rightmost on
// the header's last control's right edge (or the same gutter before a
// tool panel in its lane).

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// gutterCfg is a window the gutter is checked (and pictured) in.
type gutterCfg struct {
	name  string
	w, h  int
	scale float64
	dark  bool
	rail  bool // the icon rail, beside the window controls
	en    bool // in English
}

var (
	gutterWide = gutterCfg{"1100-zh", 1100, 720, 1, false, false, false}
	gutterMin  = gutterCfg{"min125-zh", 820, 520, 1.25, false, false, false}
	gutterRail = gutterCfg{"rail-1100-zh", 1100, 720, 1, false, true, false}
	gutterBig  = gutterCfg{"1440-zh", 1440, 900, 1, false, false, false} // no cap leaves a strip on the right
	gutterEn   = gutterCfg{"1100-en", 1100, 720, 1, false, false, true}  // longer words in the headers
)

// gutterPage is a page to check: its name and what opens it.
type gutterPage struct {
	name string
	open func(d *dubTest)
}

// gutterFree are the pages whose right edge and top are not checked, and
// why.
var gutterFree = map[string]string{
	"batch": "nothing queued: the queue is a centred empty state",
}

// gutterCentred are pages whose body is a centred column of their own
// width, not gutter to gutter: their left and right edges are not checked.
var gutterCentred = map[string]string{
	"home": "a centred 720-DIP column (user, 11-01: not full width)",
}

// gutterPages are the pages with a header, each with something in it.
var gutterPages = []gutterPage{
	{"home", func(d *dubTest) { d.a.navigate("/home") }},
	{"discover", func(d *dubTest) { d.a.navigate("/voices/discover") }},
	{"mine", func(d *dubTest) { d.a.navigate("/voices/mine") }},
	{"new-voice", func(d *dubTest) { d.a.navigate("/voices/new") }},
	{"clone", func(d *dubTest) { d.a.navigate("/voices/new/clone") }},
	{"design", func(d *dubTest) { d.a.navigate("/voices/new/design") }},
	{"projects", func(d *dubTest) { d.a.projects.cards = 0; d.a.navigate("/projects") }},
	{"projects-cards", func(d *dubTest) { d.a.navigate("/projects"); d.a.projects.cards = 1 }},
	{"assets", func(d *dubTest) { d.a.projects.cards = 0; d.a.navigate("/assets") }},
	{"dub", func(d *dubTest) { d.a.navigate("/dub") }},
	{"dub-editor", func(d *dubTest) { d.a.navigate("/dub"); d.a.openDub(d.a.dubList[0].ID) }},
	{"transcripts", func(d *dubTest) { d.a.navigate("/transcripts") }},
	{"transcript", func(d *dubTest) { d.a.navigate("/transcripts/t1") }},
	{"isolate", func(d *dubTest) { d.a.navigate("/isolate") }},
	{"isolate-result", func(d *dubTest) {
		d.a.navigate("/isolate")
		d.a.navigate("/isolate/" + d.a.isoList[0].ID)
	}},
	{"studio", func(d *dubTest) { d.a.navigate("/studio") }},
	{"stories", func(d *dubTest) { d.a.navigate("/stories") }},
	{"story-editor", func(d *dubTest) { d.a.navigate("/stories"); d.a.openSample("story") }},
	// A new story: the 「粘贴或导入文本」 card first, the inspector beside it.
	{"story-new", func(d *dubTest) { d.a.navigate("/stories"); d.a.lfCreate(d.a.lfStories, nil) }},
	{"book-editor", func(d *dubTest) { d.a.navigate("/audiobook"); d.a.loadSample(d.a.lfBook) }},
	{"audiobook", func(d *dubTest) { d.a.navigate("/audiobook") }},
	{"dictation", func(d *dubTest) { d.a.navigate("/dictation") }},
	{"workflows", func(d *dubTest) {
		d.a.navigate("/workflows")
		if d.a.wf == nil {
			d.a.wfNew("narration-normalize")
		}
	}},
	{"batch", func(d *dubTest) { d.a.navigate("/batch") }},
	{"calls", func(d *dubTest) { d.a.navigate("/calls") }},
	{"developers", func(d *dubTest) { d.a.navigate("/developers") }},
	{"api", func(d *dubTest) { d.a.navigate("/developers/api") }},
	{"tasks", func(d *dubTest) { d.a.navigate("/tasks") }},
	{"twilio", func(d *dubTest) { d.a.navigate("/calls/setup") }},
}

// gutterSeed is an app in cfg's window, in Chinese, with voices, takes,
// stories, a book, dubs, a transcript and an isolated file.
func gutterSeed(t *testing.T, cfg gutterCfg) *dubTest {
	t.Helper()
	i18n.SetLocale("en")
	clear(headSearchOpen)
	d := newDubTest(t, 3)
	a, eng, tt := d.a, d.eng, d.tt
	a.updateSettings(func(s *settings.Settings) {
		if !cfg.en {
			s.General.Language = "zh-Hans"
		}
		s.Appearance.Scale = cfg.scale
		if cfg.dark {
			s.Appearance.Theme = "dark"
		}
		s.Appearance.Rail = cfg.rail
	})
	tt.SetSize(cfg.w, cfg.h)
	if cfg.rail {
		tt.SetTitleBar(ui.TitleBar{Height: 52, Left: 78})
	}
	addVoice(t, a, core.Voice{Name: "旁白", Kind: core.VoiceCloned, RefText: "从前有座山。", Language: "zh"}, 6)
	addVoice(t, a, core.Voice{Name: "外婆", Kind: core.VoiceDesigned, Design: "一位慈祥的老奶奶", Language: "zh", Seed: 7}, 0)
	now := time.Now()
	for i, text := range []string{"欢迎收听本期节目，今天我们聊聊城市里的夜晚。", "最后一班渡轮在十一点离港。", "请在周五之前提交报告。"} {
		addTake(t, a, core.Take{ID: fmt.Sprintf("T%d", i), CreatedAt: now.Add(-time.Duration(i) * time.Hour), Text: text, Model: kokoro, Voice: "af_heart", AudioSeconds: 3 + float64(i)}, 0.3)
	}
	ctx := context.Background()
	for _, pr := range []struct {
		kind  core.ProjectKind
		title string
	}{{core.ProjectStory, "夜航渡轮"}, {core.ProjectStory, "两个人的晚餐"}, {core.ProjectAudiobook, "城南旧事 · 第一章"}} {
		doc := services.NewLongformDoc(pr.kind, kokoro)
		doc.Title = pr.title
		doc.Lines = append(doc.Lines, services.StoryLine{ID: "l0", Text: "台词"})
		if pr.kind == core.ProjectAudiobook {
			doc.Script = "第一章\n\n雾从江面上漫过来的时候，渡轮已经离岸很远了。"
		}
		a.lib.svc.Library.SaveDoc(ctx, core.Project{Kind: pr.kind, Title: pr.title}, doc)
	}
	for _, name := range []string{"产品发布会.wav", "Interview.wav"} {
		src := filepath.Join(t.TempDir(), name)
		copyTestFile(t, d.src, src)
		a.dubStartFile(src)
	}
	tr := sampleTranscript("t1", now.Add(-3*time.Hour))
	tr.Title = "周会录音.m4a"
	putTranscript(t, a, tr)
	fakeOnDisk(t, a, isoSeparator())
	fakeOnDisk(t, a, isoDenoiser())
	a.isoEngine = isoFakeEngine(nil)
	eng.executors[services.KindIsolate] = a.isoSvc.Executor()
	src := filepath.Join(t.TempDir(), "采访原声.wav")
	copyTestFile(t, d.src, src)
	a.isoStart(src)
	a.projects.dirty = true
	tt.Frame()
	tt.Advance(10 * time.Second) // the isolation's toast goes
	tt.Frame()
	return d
}

// gutterGeom is a page's measure: the header's content edges (its
// title's left, its last tool's right) and the body's (its leftmost,
// rightmost and topmost box: a row's band, a card, a labelled control, a
// text), with what made them; panel is a tool panel's box in its lane
// (empty: none); unit is a DIP at the app's scale.
type gutterGeom struct {
	head, panel               ui.Rect
	headL, headR              float32
	bodyL, bodyR, bodyT       float32
	besideT                   float32 // the first content's top beside the panel
	leftWho, rightWho, topWho string
	unit                      float32
	over                      bool    // the panel floats over the page
	toolsX                    float32 // the header's tools' left edge
	aboveR                    float32 // the right edge of what is above a panel in its lane
	titleR                    float32 // the right edge of the title and the tabs after it
}

// gutterPanels are the tool panels that keep a lane.
func gutterPanels() []string {
	return []string{i18n.T("Text to speech panel"), i18n.T("Step settings"), i18n.T("Inspector"), i18n.T("Step and run")}
}

// wantR is where the body's right edge belongs: the header's last
// control's right edge, or laneGap before a panel in its lane. (A panel
// over the page leaves it its gutter; what moves out of its way, as
// Workflows' toolbar, only must not pass the gutter: rightFree.)
func (g gutterGeom) wantR() float32 {
	if g.panel.W > 0 && !g.over {
		return g.panel.X - sp4*g.unit
	}
	return g.headR
}

// wantT is where the body's first content belongs: bodyTop under the
// header (and the panel's top).
func (g gutterGeom) wantT() float32 { return g.head.Y + g.head.H + sp2*g.unit }

// measureGutter builds the page a few frames and measures it.
func measureGutter(tt *ui.Tester, a *app) gutterGeom {
	g := gutterGeom{unit: float32(a.prefs.Appearance.Scale)}
	if g.unit == 0 {
		g.unit = 1
	}
	var boxes []struct {
		kind string
		r    ui.Rect
	}
	gutterProbe = func(kind string, r ui.Rect) {
		switch kind {
		case "head":
			g.head = r
		case "head-l":
			g.headL = r.X
		case "head-r":
			g.headR, g.toolsX = r.X+r.W, r.X
		case "head-t", "head-in":
			if r.W > 0 {
				g.titleR = max(g.titleR, r.X+r.W)
			}
		default:
			boxes = append(boxes, struct {
				kind string
				r    ui.Rect
			}{kind, r})
		}
	}
	defer func() { gutterProbe = nil }()
	a.toasts = nil
	tt.Frame()
	tt.Frame()
	tt.Advance(time.Second)
	boxes = boxes[:0]
	tt.Frame()
	for _, label := range gutterPanels() {
		if r, ok := tt.Find(label); ok && r.W > 0 && r.X > g.head.X {
			g.panel = r
		}
	}
	// A panel with a ✕ at its top floats over the page (no lane): what is
	// under it is not weighed, and the page keeps its gutter.
	if x, ok := tt.Find(i18n.T("Close")); ok && g.panel.W > 0 && x.X >= g.panel.X && x.Y < g.panel.Y+sp6*g.unit {
		g.over = true
	}
	canvas, _ := tt.Find(i18n.T("Workflow canvas"))
	g.bodyL, g.bodyR, g.bodyT, g.besideT = 1e9, -1e9, 1e9, 1e9
	take := func(who string, r ui.Rect) {
		if gutterTrace != nil {
			gutterTrace(who, r)
		}
		if r.W <= 0 || r.H <= 0 || r.Y < g.head.Y+g.head.H-0.5 {
			return
		}
		if r.X < g.head.X-0.5 || r.X+r.W > g.head.X+g.head.W+0.5 {
			return // the sidebar, or past the page
		}
		if g.panel.W > 0 && r.X >= g.panel.X-0.5 {
			return // in the floating panel
		}
		if canvas.W > 0 && r.X >= canvas.X && r.Y >= canvas.Y && r.X+r.W <= canvas.X+canvas.W+0.5 {
			return // on the workflow canvas, which runs to the page's edges
		}
		if r.X < g.bodyL {
			g.bodyL, g.leftWho = r.X, who
		}
		if g.panel.W > 0 && !g.over && r.Y+r.H <= g.panel.Y {
			// Above the panel (Dub's steps): within the header's edge.
			g.aboveR = max(g.aboveR, r.X+r.W)
		} else if r.X+r.W > g.bodyR {
			g.bodyR, g.rightWho = r.X+r.W, who
		}
		if r.Y < g.bodyT {
			g.bodyT, g.topWho = r.Y, who
		}
		// The first content beside a panel: in the row it floats in.
		if g.panel.W > 0 && r.Y >= g.panel.Y-sp2*g.unit-1 && r.Y < g.besideT {
			g.besideT = r.Y
		}
	}
	for _, b := range boxes {
		take(b.kind, b.r)
	}
	seen := map[string]bool{}
	for _, s := range tt.Texts() {
		if seen[s] {
			continue
		}
		seen[s] = true
		if r, ok := tt.Find(s); ok {
			take(fmt.Sprintf("%q", s), r)
		}
	}
	return g
}

// gutterTrace, when set, sees every box measureGutter weighs.
var gutterTrace func(who string, r ui.Rect)

// TestPageGutter: each page's body starts on the header title's left
// edge and ends on its last control's right edge, ±1 DIP, in a 1100-wide
// window, in the smallest at 125 % and beside the icon rail. Home's
// header has no title: its body keeps the same gutters.
// GUTTER_TRACE=<page> logs the boxes weighed on that page.
func TestPageGutter(t *testing.T) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	for _, cfg := range []gutterCfg{gutterWide, gutterMin, gutterRail, gutterBig, gutterEn} {
		d := gutterSeed(t, cfg)
		for _, p := range gutterPages {
			_, free := gutterFree[p.name]
			p.open(d)
			if p.name == os.Getenv("GUTTER_TRACE") {
				gutterTrace = func(who string, r ui.Rect) { t.Logf("   %s %.1f %.1f %.1f %.1f", who, r.X, r.Y, r.W, r.H) }
			}
			g := measureGutter(d.tt, d.a)
			gutterTrace = nil
			if g.head.W == 0 {
				t.Errorf("%s %s: no page header", cfg.name, p.name)
				continue
			}
			if g.aboveR > g.headR+1 {
				t.Errorf("%s %s: above the panel, the body ends at %.1f, past the header's %.1f", cfg.name, p.name, g.aboveR, g.headR)
			}
			if g.titleR > g.toolsX+0.5 {
				t.Errorf("%s %s: the header's title side reaches %.1f into its tools", cfg.name, p.name, g.titleR-g.toolsX)
			}
			_, centred := gutterCentred[p.name]
			if d := g.bodyL - g.headL; (d < -1 || d > 1) && !centred {
				t.Errorf("%s %s: the body starts at %.1f (%s), the header's title at %.1f (%+.1f)", cfg.name, p.name, g.bodyL, g.leftWho, g.headL, d)
			}
			if d := g.bodyR - g.wantR(); ((d < -1 && !free && !g.over) || d > 1) && !centred {
				t.Errorf("%s %s: the body ends at %.1f (%s), want %.1f (%+.1f; panel %v)", cfg.name, p.name, g.bodyR, g.rightWho, g.wantR(), d, g.panel)
			}
			if d := g.bodyT - g.wantT(); (d < -1 || d > 1) && !free && p.name != "home" {
				t.Errorf("%s %s: the body's first content's top at %.1f (%s), want %.1f (%+.1f)", cfg.name, p.name, g.bodyT, g.topWho, g.wantT(), d)
			}
			if d := g.panel.Y - g.besideT; g.panel.W > 0 && !g.over && (d < -1 || d > 1) {
				t.Errorf("%s %s: the panel's top at %.1f, the content's beside it at %.1f (%+.1f)", cfg.name, p.name, g.panel.Y, g.besideT, d)
			}
		}
	}
}

// Home's column stays 720 DIPs wide and centred in a wide window.
func TestHomeCentredColumn(t *testing.T) {
	a, _, _ := newTestApp(t)
	tt := ui.NewTester(a.view, 1600, 900)
	a.navigate("/home")
	tt.Frame()
	tt.Frame()
	r, ok := tt.Find("What would you like to create?")
	if !ok {
		t.Fatalf("home: %q", tt.Texts())
	}
	if r.W > 721 {
		t.Errorf("Home's column is %.0f wide, want 720 at most", r.W)
	}
}
