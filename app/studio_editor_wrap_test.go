package main

import (
	"fmt"
	"image"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
)

// Long paragraphs for the script editor, as the user's report (thread
// #98: on the Mac, "也", "多米", "西", "吃" at the ends of rows were cut in
// half at the editor's right edge: soft wraps summed the widths of
// characters shaped alone, and Core Text shapes a Chinese comma alone half
// as wide as between ideographs; MyGo now wraps rows by their shaped
// width).
const (
	scriptZhPara = "他也说过，这个城市里有很多米粉店，西边那家最好吃，每天早上都排着长队，大家吃完了才去上班，也有人专门从很远的地方赶过来吃一碗。"
	scriptZhMix  = "M4B（含章节）导出 3 个文件，AI 模型 Qwen3-ASR 识别 25 分钟的音频，约 0.2 秒；Kokoro、MOSS 和 SoulX 也可以用，吃的东西越来越多，也越来越好。"
	scriptEnPara = "Welcome back to the show. Today we talk about three things, and it takes twenty-five minutes; AVAWAY, Typography, kerning and LAVA."
	scriptEnMix  = "Second, a word you may say wrong, and a long line for the next story, which wraps more than once in the editor at every width."
)

// longScript is the script of a run: as much as the editor shows without
// scrolling (its scroll bar would draw in the gutter).
func longScript(run b1Run) string {
	para, mix := scriptZhPara, scriptZhMix
	if run.lang == "en" {
		para, mix = scriptEnPara, scriptEnMix
	}
	if run.w < 1000 {
		return para + "\n" + mix
	}
	return para + para + "\n\n" + mix
}

// scriptWrapRuns are the window sizes of the report and of the reviews:
// the default window, about 1300 and 1500 points wide, and the smallest at
// 125 %.
var scriptWrapRuns = []b1Run{
	{"1100", 1100, 720, 1, "zh-Hans", true}, {"1300", 1300, 900, 1, "zh-Hans", true},
	{"1500", 1500, 1000, 1, "zh-Hans", false}, {"min125", 820, 520, 1.25, "zh-Hans", true},
	{"1100", 1100, 720, 1, "en", false}, {"1300", 1300, 900, 1, "en", true},
	{"1500", 1500, 1000, 1, "en", false}, {"min125", 820, 520, 1.25, "en", false},
}

// TestScriptEditorWrapsInside: the script's rows end inside the editor,
// before the gutter its scroll bar takes at the right: no glyph is drawn
// there. Meaningful on macOS (Core Text), where the rows overflowed; with
// Pango they always fitted.
func TestScriptEditorWrapsInside(t *testing.T) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	for _, run := range scriptWrapRuns {
		i18n.SetLocale("en")
		a, _, tt := newTestApp(t)
		(&b1Shooter{t: t, run: run}).setup(a, tt)
		a.text = longScript(run)
		name := fmt.Sprintf("%s %s", run.lang, run.suffix())
		// The editor measures its rows within a time budget a frame, so a
		// slow machine (CI) may need a few frames before every row in view
		// is painted: wait until the left half shows text.
		var (
			img *image.RGBA
			box ui.Rect
			k   float32
		)
		for i := 0; i < 30; i++ {
			tt.Frame()
			b, ok := tt.Find(i18n.T("Script"))
			if !ok {
				t.Fatalf("%s: no script editor", name)
			}
			box = b
			img = tt.Image()
			k = float32(img.Bounds().Dx()) / float32(run.w)
			if i >= 1 && areaInk(img, ui.Rect{X: box.X + 16, Y: box.Y + 16, W: box.W/2 - 16, H: box.H - 32}, k) {
				break
			}
		}
		if ink := gutterInk(img, box, k); ink != "" {
			t.Errorf("%s: text drawn in the editor's right gutter (%s)", name, ink)
		}
		if !areaInk(img, ui.Rect{X: box.X + 16, Y: box.Y + 16, W: box.W/2 - 16, H: box.H - 32}, k) {
			t.Errorf("%s: no text in the editor's left half: the test sees nothing", name)
		}
	}
}

// gutterInk tells where pixels unlike the editor's background show in the
// rightmost scrollGutter DIPs of box (the gutter of the editor's scroll
// bar, which rows end before), k image pixels a DIP; "" when none do.
func gutterInk(img *image.RGBA, box ui.Rect, k float32) string {
	const scrollGutter = 12 // MyGo's editor: 10 for the bar, 4 apart, a pixel or two of antialiasing
	x0, x1 := int((box.X+box.W-scrollGutter)*k), int((box.X+box.W)*k)-1
	y0, y1 := int(box.Y*k), int((box.Y+box.H)*k)
	bg := img.RGBAAt(x1-1, y1-2)
	n, first := 0, image.Point{}
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			c := img.RGBAAt(x, y)
			if absDiff(c.R, bg.R)+absDiff(c.G, bg.G)+absDiff(c.B, bg.B) > 60 {
				if n == 0 {
					first = image.Pt(x, y)
				}
				n++
			}
		}
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d pixels, first at %v, the gutter is x %d–%d", n, first, x0, x1)
}

// areaInk tells whether pixels unlike the background (taken at the
// area's bottom-right corner) show anywhere in r, k image pixels a DIP.
func areaInk(img *image.RGBA, r ui.Rect, k float32) bool {
	x0, x1 := int(r.X*k), int((r.X+r.W)*k)
	y0, y1 := int(r.Y*k), int((r.Y+r.H)*k)
	bg := img.RGBAAt(x1-1, y1-1)
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			c := img.RGBAAt(x, y)
			if absDiff(c.R, bg.R)+absDiff(c.G, bg.G)+absDiff(c.B, bg.B) > 60 {
				return true
			}
		}
	}
	return false
}

func absDiff(a, b uint8) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

// TestSnapshotsStudioLongText renders the Studio with long paragraphs in
// the script editor, where rows wrap at the editor's right edge (thread
// #98), Chinese dark and English light at about 1300 points, and Chinese
// at the smallest window at 125 %:
//
//	AUDIOSMITH_SNAPSHOTS=/tmp/long go test -run TestSnapshotsStudioLongText .
func TestSnapshotsStudioLongText(t *testing.T) { runSnapshots(t, snapshotsStudioLongText) }

func snapshotsStudioLongText(t *testing.T, out string) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	for _, run := range []b1Run{{"1300", 1300, 900, 1, "zh-Hans", true}, {"1300", 1300, 900, 1, "en", false}, {"min125", 820, 520, 1.25, "zh-Hans", true}} {
		i18n.SetLocale("en")
		a, _, tt := newTestApp(t)
		s := &b1Shooter{t: t, out: out, run: run}
		s.setup(a, tt)
		a.text = longScript(run)
		s.save(tt, "studio-long-text")
	}
}
