package main

// The menu bar icon: Start/Stop dictation, Open AudioSmith, Transcribe,
// Settings, Quit; a dot marks it while dictation listens. With Settings → General →
// "Keep running in the menu bar", closing the window hides it, so the
// shortcut keeps working; the Dock icon or the menu brings it back.

import (
	"github.com/hehehai/AudioSmith/assets"

	"github.com/egoist/mygo"
	"github.com/hehehai/AudioSmith/internal/dictation"
	"github.com/hehehai/AudioSmith/internal/i18n"
)

// trayState is the menu bar icon.
type trayState struct {
	tray      *mygo.Tray
	recording bool   // the icon shows recording
	locale    string // the menu's language
	shortcut  string
	live      bool // the menu says Stop
	quitting  bool
}

// Tray icons: AudioSmith's anvil and spark (assets/tray/tray.svg, the
// ActNow flat brand kit's menu bar version) at 36×36 px (18 pt @2x), both
// templates (macOS tints them to the menu bar); while dictation listens
// a larger dot takes the spark's place (assets/tray/tray-active.svg).
// The PNGs come from the kit; the SVGs render the same shapes.
var (
	trayIcon       = assets.TrayIcon
	trayActiveIcon = assets.TrayActiveIcon
)

// openTray makes the menu bar icon.
func (a *app) openTray() {
	t, err := mygo.NewTray(mygo.TrayOptions{Icon: trayIcon, IconIsTemplate: true, ToolTip: "AudioSmith"})
	if err != nil {
		a.toastWarn(i18n.T("The menu bar icon could not be made: %v", err))
		return
	}
	a.dictTray.tray = t
	a.updateTrayMenu()
}

func (a *app) closeTray() {
	if t := a.dictTray.tray; t != nil {
		t.Destroy()
		a.dictTray.tray = nil
	}
}

// updateTray shows the recording state, and the menu that goes with it.
func (a *app) updateTray() {
	tr := &a.dictTray
	rec := a.dict != nil && (a.dict.phase == dictStarting || a.dict.phase == dictListening)
	if tr.tray != nil && rec != tr.recording {
		if rec {
			tr.tray.SetIcon(trayActiveIcon, true)
		} else {
			tr.tray.SetIcon(trayIcon, true)
		}
	}
	tr.recording = rec
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
		{Label: i18n.T("Transcribe"), Click: act(func() { a.showMainWindow(); a.navigate("/transcripts") })},
		{Label: i18n.T("Settings"), Click: act(func() { a.showMainWindow(); a.navigate("/settings/dictation") })},
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
