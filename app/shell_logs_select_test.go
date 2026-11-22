package main

// Adapted from VoxtNew's shell_logs_select_test.go (commits 21fe7ed7,
// ccc48bbe), Copyright 2026 ActNow and the SGLang-Omni Team, Apache
// License 2.0 (see NOTICE).
//
// The logs view's selection (shell_logs_select.go): hover, click to copy,
// Shift and ⌘, dragging, the context menu, the header, ⌘A, ⌘C and Esc,
// as the family's log-selection spec lists them.

import (
	"fmt"
	"image/color"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/logs"
)

// The Tester's window (testutil_test.go's newTestAppIn) and the main
// window's smallest size (shell.go).
const (
	logsTesterWidth, logsTesterHeight = 1100, 720
	logsMinWidth, logsMinHeight       = 820, 520
)

// logsSettle runs the frames after an action (toasts, the selection's
// second build).
func logsSettle(tt *ui.Tester) {
	for range 3 {
		tt.Frame()
	}
}

// logsClick clicks a label and runs the frames after it.
func logsClick(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	logsSettle(tt)
	if err := tt.Click(label); err != nil {
		t.Fatalf("click %q: %v (texts: %q)", label, err, logsTexts(tt))
	}
	logsSettle(tt)
}

// logsTexts are the first texts on screen, for failure messages.
func logsTexts(tt *ui.Tester) []string {
	texts := tt.Texts()
	return texts[:min(len(texts), 40)]
}

// logsShows reports whether the view shows s.
func logsShows(tt *ui.Tester, s string) bool {
	logsSettle(tt)
	return tt.HasText(s) || slices.Contains(tt.Texts(), s)
}

// logsShowsExact reports whether the view shows a text that is s.
func logsShowsExact(tt *ui.Tester, s string) bool {
	logsSettle(tt)
	return slices.Contains(tt.Texts(), s)
}

// logsApp is the app on /logs with lines "line 1" … "line n".
func logsApp(t *testing.T, n int) (*app, *ui.Tester) {
	t.Helper()
	a, _, tt := newTestApp(t)
	logsSettle(tt)
	at := time.Date(2026, 10, 17, 9, 30, 0, 0, time.Local)
	for i := 1; i <= n; i++ {
		a.logs.Append(core.LogEntry{Time: at.Add(time.Duration(i) * time.Second), Source: "app", Message: fmt.Sprintf("line %d", i)})
	}
	a.navigate("/logs")
	a.logsSource = "app" // the app's own start-up lines aside
	a.logsQuery = "line "
	logsSettle(tt)
	return a, tt
}

// lineAt is the middle of the message of line i.
func lineAt(t *testing.T, tt *ui.Tester, i int) (float32, float32) {
	t.Helper()
	r, ok := tt.Find(fmt.Sprintf("line %d", i))
	if !ok {
		t.Fatalf("no line %d: %q", i, logsTexts(tt))
	}
	return r.X + r.W/2, r.Y + r.H/2
}

// rowFill is the colour of line i's row between its source and its
// message, where no text is.
func rowFill(t *testing.T, tt *ui.Tester, i int) color.RGBA {
	t.Helper()
	r, ok := tt.Find(fmt.Sprintf("line %d", i))
	if !ok {
		t.Fatalf("no line %d", i)
	}
	img := tt.Image()
	scale := float32(img.Bounds().Dx()) / float32(logsTesterWidth)
	return img.RGBAAt(int((r.X-4)*scale), int((r.Y+r.H/2)*scale))
}

// selectedLines are the messages of the lines selected, in the log's
// order (the oldest first, as copies are), whatever order they show in.
func selectedLines(a *app) []string {
	var out []string
	for _, e := range listselItems(a) {
		out = append(out, e.Message)
	}
	return out
}

func listselItems(a *app) []core.LogEntry {
	var out []core.LogEntry
	for _, e := range slices.Backward(a.visibleLogs()) {
		if a.logSel.m.IsSelected(e.Seq) {
			out = append(out, e)
		}
	}
	return out
}

func wantSelected(t *testing.T, a *app, want ...string) {
	t.Helper()
	if got := selectedLines(a); !slices.Equal(got, want) {
		t.Fatalf("selected %q, want %q", got, want)
	}
}

func clickLine(t *testing.T, tt *ui.Tester, mods ui.Modifiers, i int) {
	t.Helper()
	x, y := lineAt(t, tt, i)
	tt.ClickAtWith(mods, x, y)
	logsSettle(tt)
}

// formatted is lines i as Copy puts them on the clipboard: in the log's
// order, the oldest first.
func formatted(a *app, i ...int) string {
	var out string
	for _, e := range slices.Backward(a.visibleLogs()) {
		for _, n := range i {
			if e.Message == fmt.Sprintf("line %d", n) {
				out += logs.Format(e)
				if len(i) > 1 {
					out += "\n"
				}
			}
		}
	}
	return out
}

func TestLogsHover(t *testing.T) {
	_, tt := logsApp(t, 5)
	k := lightTokens
	plain := rowFill(t, tt, 2)
	if !logsNear(plain, k.Bg) {
		t.Errorf("plain row %v, want %v", plain, k.Bg)
	}
	x, y := lineAt(t, tt, 2)
	tt.Move(x, y)
	logsSettle(tt)
	if got := rowFill(t, tt, 2); !logsNear(got, k.Hover) {
		t.Errorf("hovered row %v, want %v (plain %v)", got, k.Hover, plain)
	}
	if got := rowFill(t, tt, 3); got != plain {
		t.Errorf("the row below lit too: %v", got)
	}
	x, y = lineAt(t, tt, 4)
	tt.Move(x, y)
	logsSettle(tt)
	if rowFill(t, tt, 2) != plain || !logsNear(rowFill(t, tt, 4), k.Hover) {
		t.Error("the hover did not follow the pointer")
	}
}

func TestLogsClickCopiesLine(t *testing.T) {
	a, tt := logsApp(t, 5)
	clickLine(t, tt, 0, 3)
	if got := tt.Clipboard(); got != formatted(a, 3) {
		t.Errorf("clipboard %q, want %q", got, formatted(a, 3))
	}
	if !logsShows(tt, "Copied 1 line") {
		t.Errorf("no toast: %q", logsTexts(tt))
	}
	// The line becomes the anchor, not selected.
	wantSelected(t, a)
	if !logsShowsExact(tt, "5 lines") {
		t.Error("the header changed after a click")
	}
	clickLine(t, tt, ui.Shift, 5)
	wantSelected(t, a, "line 3", "line 4", "line 5")
	if tt.Clipboard() != formatted(a, 3) {
		t.Error("a Shift-click copied")
	}
}

func TestLogsShiftAndCmd(t *testing.T) {
	a, tt := logsApp(t, 8)
	k := lightTokens
	plain := rowFill(t, tt, 8)
	clickLine(t, tt, 0, 6)
	clickLine(t, tt, ui.Shift, 2) // upwards
	wantSelected(t, a, "line 2", "line 3", "line 4", "line 5", "line 6")
	if !logsShows(tt, "5 lines selected") {
		t.Errorf("header: %q", logsTexts(tt))
	}
	tt.Move(0, 0) // off the rows
	logsSettle(tt)
	if got, want := rowFill(t, tt, 4), k.Selected.Over(k.Bg); got == plain || !logsNear(got, want) {
		t.Errorf("selected row %v, want %v", got, want)
	}
	// ⌘ takes one out and adds another.
	clickLine(t, tt, ui.Cmd, 4)
	clickLine(t, tt, ui.Cmd, 8)
	wantSelected(t, a, "line 2", "line 3", "line 5", "line 6", "line 8")
	// Shift extends from the last ⌘-click, keeping the rest.
	clickLine(t, tt, ui.Shift, 7)
	wantSelected(t, a, "line 2", "line 3", "line 5", "line 6", "line 7", "line 8")
	if tt.Clipboard() != formatted(a, 6) {
		t.Error("a modified click copied")
	}
	// A plain click copies its line and drops the selection.
	clickLine(t, tt, 0, 1)
	wantSelected(t, a)
	if tt.Clipboard() != formatted(a, 1) || !logsShowsExact(tt, "8 lines") {
		t.Errorf("plain click: %q", tt.Clipboard())
	}
}

func TestLogsDrag(t *testing.T) {
	a, tt := logsApp(t, 8)
	x, y := lineAt(t, tt, 2)
	tt.Press(x, y)
	_, y4 := lineAt(t, tt, 4)
	tt.Move(x, y+2) // within the slop: still a click to be
	logsSettle(tt)
	wantSelected(t, a)
	tt.Move(x+10, y4)
	logsSettle(tt)
	wantSelected(t, a, "line 2", "line 3", "line 4")
	if !logsShows(tt, "3 lines selected") {
		t.Errorf("header while dragging: %q", logsTexts(tt))
	}
	tt.Release(x+10, y4)
	logsSettle(tt)
	wantSelected(t, a, "line 2", "line 3", "line 4")
	if tt.Clipboard() != "" {
		t.Errorf("a drag copied %q", tt.Clipboard())
	}
	// Upwards (to the newer lines), past the top: from the line pressed
	// to the newest.
	x, y = lineAt(t, tt, 3)
	tt.Press(x, y)
	_, y6 := lineAt(t, tt, 6)
	if y6 >= y {
		t.Fatalf("line 6 (%v) not above line 3 (%v): the newest are not first", y6, y)
	}
	tt.Move(x, y6)
	logsSettle(tt)
	wantSelected(t, a, "line 3", "line 4", "line 5", "line 6")
	tt.Move(x, 0)
	logsSettle(tt)
	wantSelected(t, a, "line 3", "line 4", "line 5", "line 6", "line 7", "line 8")
	// Esc gives the drag up: the selection it started from comes back.
	tt.Key(0, ui.KeyEscape)
	logsSettle(tt)
	tt.Release(x, 0)
	logsSettle(tt)
	wantSelected(t, a, "line 2", "line 3", "line 4")
	if tt.Clipboard() != "" {
		t.Error("the release after Esc copied")
	}
	// ⌘-drag adds to the selection: the Tester presses without
	// modifiers (Press), so actnowkit's listsel tests it (TestDragAdds).
}

func TestLogsContextMenu(t *testing.T) {
	a, tt := logsApp(t, 5)
	x, y := lineAt(t, tt, 2)
	tt.RightClickAt(x, y)
	if got := tt.Menu(); !slices.Equal(got, []string{"Copy this line", "Copy all lines shown"}) {
		t.Fatalf("menu %q", got)
	}
	if err := tt.ChooseMenuItem("Copy this line"); err != nil {
		t.Fatal(err)
	}
	logsSettle(tt)
	if tt.Clipboard() != formatted(a, 2) || !logsShows(tt, "Copied 1 line") {
		t.Errorf("copy this line: %q", tt.Clipboard())
	}
	clickLine(t, tt, ui.Cmd, 1)
	clickLine(t, tt, ui.Cmd, 4)
	x, y = lineAt(t, tt, 3) // a line not selected: the menu still offers the selection
	tt.RightClickAt(x, y)
	if got := tt.Menu(); !slices.Equal(got, []string{"Copy this line", "Copy 2 selected lines", "Copy all lines shown"}) {
		t.Fatalf("menu %q", got)
	}
	wantSelected(t, a, "line 1", "line 4") // right-click keeps the selection
	if err := tt.ChooseMenuItem("Copy 2 selected lines"); err != nil {
		t.Fatal(err)
	}
	logsSettle(tt)
	if tt.Clipboard() != formatted(a, 1, 4) || !logsShows(tt, "Copied 2 lines") {
		t.Errorf("copy selected: %q", tt.Clipboard())
	}
	wantSelected(t, a) // copying clears
	tt.RightClickAt(x, y)
	if err := tt.ChooseMenuItem("Copy all lines shown"); err != nil {
		t.Fatal(err)
	}
	logsSettle(tt)
	if tt.Clipboard() != formatted(a, 1, 2, 3, 4, 5) || !logsShows(tt, "Copied 5 lines") {
		t.Errorf("copy all: %q", tt.Clipboard())
	}
}

func TestLogsHeader(t *testing.T) {
	a, tt := logsApp(t, 5)
	if !logsShowsExact(tt, "5 lines") || logsShows(tt, "Deselect") {
		t.Fatalf("header without a selection: %q", logsTexts(tt))
	}
	// Copy all lines shown (the icon button).
	logsClick(t, tt, "Copy all lines shown")
	if tt.Clipboard() != formatted(a, 1, 2, 3, 4, 5) {
		t.Errorf("copy all %q", tt.Clipboard())
	}
	clickLine(t, tt, ui.Cmd, 2)
	clickLine(t, tt, ui.Cmd, 3)
	if !logsShows(tt, "2 lines selected") || logsShowsExact(tt, "5 lines") {
		t.Fatalf("header with a selection: %q", logsTexts(tt))
	}
	logsClick(t, tt, "Copy")
	if tt.Clipboard() != formatted(a, 2, 3) || !logsShows(tt, "Copied 2 lines") {
		t.Errorf("header copy %q", tt.Clipboard())
	}
	wantSelected(t, a)
	if !logsShowsExact(tt, "5 lines") {
		t.Error("the header did not come back after copying")
	}
	clickLine(t, tt, ui.Cmd, 5)
	if !logsShows(tt, "1 line selected") {
		t.Errorf("one: %q", logsTexts(tt))
	}
	logsClick(t, tt, "Deselect")
	wantSelected(t, a)
	if tt.Clipboard() != formatted(a, 2, 3) {
		t.Error("Deselect copied")
	}
}

func TestLogsKeys(t *testing.T) {
	a, tt := logsApp(t, 4)
	clickLine(t, tt, 0, 2) // gives the lines the focus
	tt.Key(ui.Cmd, ui.KeyA)
	logsSettle(tt)
	wantSelected(t, a, "line 1", "line 2", "line 3", "line 4")
	tt.Key(0, ui.KeyEscape)
	logsSettle(tt)
	wantSelected(t, a)
	clickLine(t, tt, ui.Shift, 3)
	tt.Key(ui.Cmd, ui.KeyC)
	logsSettle(tt)
	if tt.Clipboard() != formatted(a, 2, 3) || !logsShows(tt, "Copied 2 lines") {
		t.Errorf("⌘C %q", tt.Clipboard())
	}
	wantSelected(t, a)
	// The Edit menu's Select All and Copy (⌘A, ⌘C on macOS).
	tt.Command("selectAll")
	logsSettle(tt)
	wantSelected(t, a, "line 1", "line 2", "line 3", "line 4")
	tt.Command("copy")
	logsSettle(tt)
	if tt.Clipboard() != formatted(a, 1, 2, 3, 4) {
		t.Errorf("copy command %q", tt.Clipboard())
	}
	wantSelected(t, a)
	// The filter keeps its own ⌘A.
	if err := tt.Click("Filter the logs"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	logsSettle(tt)
	wantSelected(t, a)
}

func TestLogsFilterAndNewLines(t *testing.T) {
	a, tt := logsApp(t, 6)
	clickLine(t, tt, 0, 2)
	clickLine(t, tt, ui.Shift, 5)
	wantSelected(t, a, "line 2", "line 3", "line 4", "line 5")
	// New lines keep the selection on its lines.
	a.logs.Append(core.LogEntry{Source: "app", Message: "line 7"})
	logsSettle(tt)
	wantSelected(t, a, "line 2", "line 3", "line 4", "line 5")
	if !logsShows(tt, "4 lines selected") {
		t.Errorf("header: %q", logsTexts(tt))
	}
	// A filter drops the lines it hides.
	a.logsQuery = "line 3"
	logsSettle(tt)
	wantSelected(t, a, "line 3")
	a.logsQuery = "line "
	logsSettle(tt)
	wantSelected(t, a, "line 3")
	if !logsShows(tt, "1 line selected") {
		t.Errorf("header after the filter: %q", logsTexts(tt))
	}
}

func TestLogsChinese(t *testing.T) {
	a, tt := logsApp(t, 3)
	i18n.SetLocale("zh-Hans")
	logsSettle(tt)
	clickLine(t, tt, 0, 1)
	if !logsShows(tt, "已复制 1 行") {
		t.Errorf("toast: %q", logsTexts(tt))
	}
	clickLine(t, tt, ui.Shift, 3)
	if !logsShows(tt, "已选中 3 行") || !logsShows(tt, "取消选择") || !logsShowsExact(tt, "拷贝") { // AudioSmith says 拷贝 for Copy
		t.Errorf("header: %q", logsTexts(tt))
	}
	x, y := lineAt(t, tt, 2)
	tt.RightClickAt(x, y)
	if got := tt.Menu(); !slices.Equal(got, []string{"复制这一行", "复制选中的 3 行", "复制全部显示的行"}) {
		t.Errorf("menu %q", got)
	}
	tt.CloseMenu()
	_ = a
}

// logsNear reports whether two opaque colours differ by at most 3 a channel.
func logsNear(a color.RGBA, c ui.Color) bool {
	d := func(x, y uint8) bool { return x-y <= 3 || y-x <= 3 }
	return d(a.R, c.R) && d(a.G, c.G) && d(a.B, c.B)
}

func TestLogsDragScrolls(t *testing.T) {
	a, tt := logsApp(t, 80)
	x, y := lineAt(t, tt, 80) // the newest, at the top
	h := float32(logsTesterHeight)
	tt.Press(x, y)
	tt.Move(x, y+20)
	tt.Move(x, h-2) // at the window's bottom, under the log's edge
	tt.Advance(2 * time.Second)
	logsSettle(tt)
	st := a.logSel.views[logPage].scroll
	if st.Y <= 0 {
		t.Fatalf("no scroll: %+v", st)
	}
	n := a.logSel.m.Count()
	if n < 20 {
		t.Errorf("the drag selected %d lines while scrolling", n)
	}
	tt.Release(x, h-2)
	logsSettle(tt)
	if got := a.logSel.m.Count(); got != n || tt.Clipboard() != "" {
		t.Errorf("after the release %d lines, clipboard %q", got, tt.Clipboard())
	}
	sel := selectedLines(a)
	if sel[len(sel)-1] != "line 80" {
		t.Errorf("newest selected %q", sel[len(sel)-1])
	}
}

// TestLogsHeaderFits: at the window's smallest, in the panel and in
// Chinese, the selection's count keeps its width (the filter gives way):
// it does not end in an ellipsis.
func TestLogsHeaderFits(t *testing.T) {
	for _, lang := range []string{"en", "zh-Hans", "ja"} {
		a, tt := logsApp(t, 6)
		i18n.SetLocale(lang)
		clickLine(t, tt, ui.Cmd, 2)
		clickLine(t, tt, ui.Cmd, 3)
		selected := i18n.N(2, "%d line selected", "%d lines selected", 2)
		wide, _ := tt.Find(selected)
		a.navigate("/studio")
		a.logsPanel = true
		tt.SetSize(logsMinWidth, logsMinHeight)
		logsSettle(tt)
		if _, ok := tt.Find(i18n.T("Deselect")); !ok {
			t.Fatalf("%s: no Deselect in the panel: %q", lang, logsTexts(tt))
		}
		if r, _ := tt.Find(selected); r.W < wide.W-0.5 {
			t.Errorf("%s: %q squeezed to %.1f (%.1f)", lang, selected, r.W, wide.W)
		}
	}
}
