package main

// The menu bar icon: Start/Stop dictation, Open AudioSmith, Transcribe,
// Settings, Quit; its dot (thread #132) is red while AudioSmith generates
// or dictation listens, violet while an agent is connected over MCP and
// nothing generates, and absent otherwise. With Settings → General →
// "Keep running in the menu bar", closing the window hides it, so the
// shortcut keeps working; the Dock icon or the menu brings it back.

import (
	"github.com/hehehai/AudioSmith/assets"

	"github.com/egoist/mygo"
	"github.com/hehehai/AudioSmith/internal/dictation"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

// trayState is the menu bar icon.
type trayState struct {
	tray      *mygo.Tray
	recording bool    // dictation listens
	dot       trayDot // the dot the icon shows
	tip       string  // its tooltip
	locale    string  // the menu's language
	shortcut  string
	live      bool // the menu says Stop
	quitting  bool
}

// Tray icons: AudioSmith's anvil and spark (assets/tray/tray.svg, the
// ActNow flat brand kit's menu bar version) at 36×36 px (18 pt @2x), both
// templates (macOS tints them to the menu bar); under a dot, the anvil
// without the spark, with a gap where MyGo's Tray.SetDot draws the dot in
// its colour, which a template cannot have (assets/tray/tray-active.svg).
// The PNGs come from the kit; the SVGs render the same shapes.
var (
	trayIcon       = assets.TrayIcon
	trayActiveIcon = assets.TrayActiveIcon
)

// trayDot is what the icon's dot says; the more urgent wins.
type trayDot int

const (
	trayDotNone  trayDot = iota
	trayDotAgent         // violet: an agent is connected over MCP
	trayDotBusy          // red: AudioSmith generates, or dictation listens
)

// The dots' colours: macOS's system red, and a blue violet that reads on
// light and dark menu bars alike.
const (
	trayRed    = "#ff3b30"
	trayViolet = "#7b61ff"
)

// trayDotOf is the dot for what goes on.
func trayDotOf(busy, agent bool) trayDot {
	switch {
	case busy:
		return trayDotBusy
	case agent:
		return trayDotAgent
	}
	return trayDotNone
}

// color is the dot's CSS colour, "" for none.
func (d trayDot) color() string {
	switch d {
	case trayDotBusy:
		return trayRed
	case trayDotAgent:
		return trayViolet
	}
	return ""
}

// generatingKind reports whether a running job of kind makes something
// (audio, text, a file): downloads, moving the models and loading one
// into memory do not.
func generatingKind(kind string) bool {
	return kind != hub.KindDownload && kind != kindMoveModels && kind != proto.MethodModelsLoad
}

// generating reports whether AudioSmith makes something now: a job runs
// that generates, or the engine's language model writes. Main thread.
func (a *app) generating() bool {
	for _, j := range a.jobList {
		if j.State == jobs.StateRunning && generatingKind(j.Kind) {
			return true
		}
	}
	return a.llmCalls.Load() > 0
}

// agentConnected reports whether an MCP client is connected.
func (a *app) agentConnected() bool { return a.apiMCP != nil && a.apiMCP.AgentConnected() }

// llmBusy counts a request of the engine's language model in or out, for
// the dot. Any goroutine.
func (a *app) llmBusy(n int32) {
	a.llmCalls.Add(n)
	if a.win != nil {
		a.post(a.updateTray)
	}
}

// openTray makes the menu bar icon.
func (a *app) openTray() {
	t, err := mygo.NewTray(mygo.TrayOptions{Icon: trayIcon, IconIsTemplate: true, ToolTip: "AudioSmith"})
	if err != nil {
		a.toastWarn(i18n.T("The menu bar icon could not be made: %v", err))
		return
	}
	a.dictTray.tray = t
	a.dictTray.dot, a.dictTray.tip = trayDotNone, "AudioSmith"
	a.updateTrayMenu()
	a.updateTray()
}

func (a *app) closeTray() {
	if t := a.dictTray.tray; t != nil {
		t.Destroy()
		a.dictTray.tray = nil
	}
}

// updateTray shows the dot, and the menu that goes with dictation's
// state. It runs when a job, dictation or the MCP clients change, and
// touches the icon only when its dot does. Main thread.
func (a *app) updateTray() {
	tr := &a.dictTray
	rec := a.dict != nil && (a.dict.phase == dictStarting || a.dict.phase == dictListening)
	dot := trayDotOf(rec || a.generating(), a.agentConnected())
	tip := "AudioSmith"
	switch {
	case rec:
		tip += " · " + i18n.T("Dictating")
	case dot == trayDotBusy:
		tip += " · " + i18n.T("Generating")
	case dot == trayDotAgent:
		tip += " · " + i18n.T("Agent connected (MCP)")
	}
	if tr.tray != nil && dot != tr.dot {
		if dot == trayDotNone {
			tr.tray.SetDot("")
			tr.tray.SetIcon(trayIcon, true)
		} else {
			tr.tray.SetIcon(trayActiveIcon, true)
			tr.tray.SetDot(dot.color())
		}
	}
	if tr.tray != nil && tip != tr.tip {
		tr.tray.SetToolTip(tip)
	}
	tr.dot, tr.tip, tr.recording = dot, tip, rec
	if tr.live != rec || tr.locale != i18n.Locale() || tr.shortcut != a.dictShortcut {
		a.updateTrayMenu()
	}
}

// updateTrayMenu builds the tray's menu in the current language.
func (a *app) updateTrayMenu() {
	tr := &a.dictTray
	tr.locale, tr.shortcut = i18n.Locale(), a.dictShortcut
	tr.live = a.dict != nil && (a.dict.phase == dictStarting || a.dict.phase == dictListening)
	if tr.tray == nil {
		return
	}
	act := func(fn func()) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) { a.post(fn) }
	}
	start := &mygo.MenuItem{Label: i18n.T("Start dictation"), Click: func(*mygo.MenuItem, *mygo.Window) {
		// The app in front before the menu took the click.
		target := a.dictSys.Frontmost()
		a.post(func() { a.dictPressed(target) })
	}}
	if tr.live {
		start = &mygo.MenuItem{Label: i18n.T("Stop dictation"), Click: act(a.dictStop)}
	}
	if a.dictShortcut != "" {
		start.Label += "    " + dictation.GlyphsOf(a.dictShortcut)
	}
	tr.tray.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
		start,
		mygo.Separator(),
		{Label: i18n.T("Open AudioSmith"), Click: act(a.showMainWindow)},
		{Label: i18n.T("Transcribe…"), Click: act(func() { a.showMainWindow(); a.navigate("/transcripts") })},
		{Label: i18n.T("Settings…"), Click: act(func() { a.showMainWindow(); a.navigate("/settings/dictation") })},
		mygo.Separator(),
		{Label: i18n.T("Quit AudioSmith"), Click: act(func() { a.dictTray.quitting = true; mygo.App.Quit() })},
	}))
}

// connectWindowClose keeps AudioSmith in the menu bar when its window
// closes (the setting), and quits otherwise: the hidden pill window must
// not keep the app alive.
func (a *app) connectWindowClose() {
	if a.win == nil {
		return
	}
	a.win.OnClose(func(e *mygo.CloseEvent) {
		if a.prefs.General.CloseToTray && !a.dictTray.quitting && a.dictTray.tray != nil {
			e.PreventDefault()
			a.win.Hide()
		}
	})
	a.win.OnClosed(func() { mygo.App.Quit() })
	mygo.App.OnActivate(func(hasVisibleWindows bool) {
		if !a.win.IsVisible() {
			a.win.Show()
		}
	})
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { a.dictTray.quitting = true })
	mygo.App.OnDidBecomeActive(func() { a.post(a.recheckPermissions) })
}
