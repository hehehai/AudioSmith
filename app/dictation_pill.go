package main

// The recorder pill: a borderless, transparent, always-on-top window at
// the bottom of the display under the pointer, shown without taking the
// keyboard (orderFrontRegardless), so the app being dictated into keeps
// its focus. It says what dictation does, with the input level and the
// time while listening, Stop, and × (cancel, or close); Escape cancels
// (a global shortcut while it shows). It hides by itself after a
// dictation is typed (0.7 s) or copied (4 s).

import (
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// Size of the pill, in DIPs; it sits pillLift above the bottom of the
// display's work area.
const (
	pillWidth  = 400
	pillHeight = 68
	pillLift   = 96
	// pillPad is the clear margin round the pill inside its window, for
	// the pill's own shadow: the window has none (macOS would draw its
	// rectangle's shadow and outline, with corners unlike the pill's).
	pillPad = 16
	// The window: the pill and its margin.
	pillWinWidth  = pillWidth + 2*pillPad
	pillWinHeight = pillHeight + 2*pillPad
)

// pillWindow is the pill's window, made at its first use.
type pillWindow struct {
	win     *mygo.Window
	visible bool
}

// showPill places the pill on the display under the pointer and shows it.
func (a *app) showPill() {
	a.dictPill.visible = true
	if a.win == nil {
		return // tests: the state is enough
	}
	if a.dictPill.win == nil {
		a.dictPill.win = mygo.NewWindow(mygo.WindowOptions{
			Title: "AudioSmith Dictation", Width: pillWinWidth, Height: pillWinHeight, UseContentSize: true,
			Frameless: true, Transparent: true, DisableShadow: true, AlwaysOnTop: true, Hidden: true, SkipTaskbar: true,
			DisableResize: true, DisableMinimize: true, DisableMaximize: true, DisableFullScreen: true,
			Content: ui.View(a.pillView),
		})
		a.dictPill.win.SetVisibleOnAllWorkspaces(true)
	}
	w := a.dictPill.win
	d := mygo.Screen.DisplayNearestPoint(mygo.Screen.CursorScreenPoint())
	wa := d.WorkArea
	w.SetPosition(wa.X+(wa.Width-pillWinWidth)/2, wa.Y+wa.Height-pillHeight-pillLift-pillPad)
	w.ShowInactive()
	w.Invalidate()
}

// hidePill hides it.
func (a *app) hidePill() {
	a.dictPill.visible = false
	if w := a.dictPill.win; w != nil {
		w.Hide()
	}
}

// pillView is the pill's content.
func (a *app) pillView(c *ui.Context) {
	th := *ui.DarkTheme()
	styleTheme(&th) // the design's dark tokens (ui_tokens.go)
	c.SetTheme(&th)
	// The root draws the theme's background unless told not to: in this
	// transparent window only the pill may show (MyGo docs/ui/windows.md).
	// Last, once the view is built: every SetTheme sets the root's
	// background again, and the kit's ink and record buttons (Stop) swap
	// the theme while they build.
	defer c.Root().Background(ui.Transparent)
	if a.win == nil {
		c.DisableAnimations()
	}
	s := a.dict
	if s == nil {
		return
	}
	if s.phase.live() {
		c.After(100 * time.Millisecond) // the level, the time
	}
	// The pill draws its own shadow in the window's clear margin. No
	// tooltips here: they could not leave this small window, and were cut.
	ui.Column(c).Fill().Padding(pillPad).Children(func() { a.pill(c, s) })
}

// pill is the pill itself, inside its window's margin.
func (a *app) pill(c *ui.Context, s *dictSession) {
	t := c.Theme()
	ui.Row(c.Key("pill")).Fill().Radius(pillHeight/2).Background(ui.RGBA(22, 22, 26, 0.94)).Border(1, ui.RGBA(255, 255, 255, 0.12)).
		Shadow(0, 6, 16, 0, ui.RGBA(0, 0, 0, 0.32)).
		Padding(0, 14, 0, 18).Gap(12).AlignItems(ui.Center).DragWindow().Children(func() {
		a.pillIndicator(c, s)
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Text(c, s.status()).FontSize(14).Bold().TextColor(ui.RGB(245, 245, 247)).SingleLine().Ellipsis("…")
			sub, color := a.pillDetail(s), ui.RGBA(235, 235, 245, 0.6)
			if s.phase == dictError {
				color = t.Danger
			}
			if s.phase == dictListening {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, clock(time.Since(s.started).Seconds())).FontSize(12).Font("monospace").TextColor(color)
					ui.Meter(c, float64(levelFraction(s.level)), 0, 1, nil).Width(120).Label(i18n.T("Input level"))
				})
			} else if sub != "" {
				ui.Text(c, sub).FontSize(11).TextColor(color).MaxLines(2).Ellipsis("…")
			}
		})
		if s.phase == dictListening || s.phase == dictStarting {
			if recordButton(c.Key("pill-stop"), i18n.T("Stop")).Clicked() {
				a.dictStop()
			}
		}
		if s.need != nil && s.phase == dictError {
			if secondaryButton(c.Key("pill-models"), i18n.T("Open Models")).Clicked() {
				a.dictHide()
				a.showMainWindow()
				a.navigate("/models")
			}
		}
		closeLabel := i18n.T("Close")
		if s.phase.live() {
			closeLabel = i18n.T("Cancel (Esc)")
		}
		if ui.ButtonBase(c.Key("pill-close")).Size(28, 28).Center().Radius(14).Label(closeLabel).Children(func() {
			ui.Icon(c, icons.Close.Linear).Size(16, 16).TextColor(ui.RGBA(235, 235, 245, 0.7))
		}).Clicked() {
			if s.phase.live() {
				a.dictCancel()
			} else {
				a.dictHide()
			}
		}
	})
}

var copyIcon = icons.Copy

// pillIndicator: a red dot while listening, a spinner while working, a
// mark at the end.
func (a *app) pillIndicator(c *ui.Context, s *dictSession) {
	t := c.Theme()
	switch s.phase {
	case dictListening:
		ui.Box(c).Width(12).Height(12).Radius(6).Background(tok(t).Record)
	case dictStarting, dictTranscribing, dictCleaning, dictInserting:
		ui.Spinner(c)
	case dictInserted:
		ui.Icon(c, icons.CheckCircle.Bold).Size(18, 18).TextColor(t.Success)
	case dictCopied:
		ui.Icon(c, copyIcon.Linear).TextColor(ui.RGB(245, 245, 247))
	case dictError:
		ui.Icon(c, icons.DangerCircle.Bold).Size(18, 18).TextColor(t.Danger)
	default:
		ui.Icon(c, micIcon.Linear).TextColor(ui.RGB(245, 245, 247))
	}
}

// pillDetail is the pill's second line.
func (a *app) pillDetail(s *dictSession) string {
	switch s.phase {
	case dictStarting:
		if s.target.Name != "" {
			return i18n.T("For %s", s.target.Name)
		}
	case dictTranscribing, dictCleaning, dictInserting:
		if s.target.Name != "" {
			return i18n.T("For %s", s.target.Name)
		}
	case dictInserted:
		return s.text
	case dictCopied:
		if s.note != "" {
			return s.note
		}
		return s.text
	case dictError, dictNoSpeech:
		return s.note
	}
	return ""
}

// showMainWindow brings the main window back (it may be hidden in the
// menu bar).
func (a *app) showMainWindow() {
	if a.win != nil {
		a.win.Show()
	}
}
