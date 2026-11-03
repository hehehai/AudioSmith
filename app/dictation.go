package main

// System-wide dictation (M2): a global shortcut (⌘⇧Space by default)
// records from the microphone in the listen worker, then transcribes the
// recording (a transcript.make job, Fast: no word timings), polishes the
// text (and cleans it with the language model within 4 s, when one is set
// up and the setting asks), and types it into the app that was in front
// when the shortcut was pressed: the clipboard and a synthetic ⌘V, the
// user's clipboard restored after. Without Accessibility, or with
// "Copy only", the text stays on the clipboard. Every dictation is kept
// on the Transcribe page.
//
// Toggle mode: press to start, press again to stop. Hold mode: dictate
// while the keys are held (MyGo reports presses; internal/dictation gets
// the release from Carbon); a tap shorter than holdTap keeps listening
// until the next press. Esc cancels while the pill shows.
//
// v1 records whole utterances (capture.start → stt.transcribe); live
// partials need streaming ASR in the listen worker (stt.stream.*, a
// protocol proposal in threads/17.md).
//
// Files: dictation.go (this: the session), dictation_run.go (a
// dictation's run: record, transcribe, deliver), dictation_pill.go (the
// recorder pill, a borderless window), dictation_tray.go (menu bar icon,
// close to tray), dictation_settings.go (Settings → Dictation, the
// shortcut recorder), permissions.go (Settings → Permissions).

import (
	"context"
	"errors"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dictation"
	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// Timings of a dictation.
const (
	holdTap        = 300 * time.Millisecond // a shorter hold keeps listening (tap to lock)
	minSpeech      = 0.3                    // seconds: shorter recordings say "No speech"
	hideInserted   = 700 * time.Millisecond
	hideCopied     = 4 * time.Second
	hideNoSpeech   = 1500 * time.Millisecond
	dictMaxSeconds = 600
)

// dictSleep is the Inserter's wait (nil: real time); tests skip it.
var dictSleep func(ctx context.Context, d time.Duration)

// dictPhase is where a dictation is.
type dictPhase int

const (
	dictIdle         dictPhase = iota
	dictStarting               // the microphone is starting
	dictListening              // recording
	dictTranscribing           // the transcription job (queued, loading the model, running)
	dictCleaning               // polish and clean-up
	dictInserting              // typing into the app
	dictInserted
	dictCopied
	dictNoSpeech
	dictError
)

// live reports whether the dictation still works (not an end state).
func (p dictPhase) live() bool { return p >= dictStarting && p <= dictInserting }

// hotkeys registers global shortcuts (mygo.GlobalShortcut; tests fake it).
type hotkeys interface {
	Register(acc string, fn func()) error
	Unregister(acc string)
}

type mygoHotkeys struct{}

func (mygoHotkeys) Register(acc string, fn func()) error {
	return mygo.GlobalShortcut.Register(acc, fn)
}
func (mygoHotkeys) Unregister(acc string) { mygo.GlobalShortcut.Unregister(acc) }

// mygoClip is the system clipboard through MyGo.
type mygoClip struct{}

func (mygoClip) Read() (transfer.Data, error) { return mygo.Clipboard.Read() }
func (mygoClip) Write(d transfer.Data) error  { return mygo.Clipboard.Write(d) }

// dictationState is dictation's state.
type dictationState struct {
	dictSys  dictation.System
	dictClip dictation.Clipboard
	dictKeys hotkeys // nil until connectDictation (or a test) sets it

	dictShortcut string // the accelerator registered; "" none
	dictRegErr   string // why it could not be registered (translated)
	dictVerified bool   // the shortcut was seen pressed since it was set
	dictEsc      bool   // Escape is registered (the pill shows)

	dict    *dictSession // the current or last dictation; nil before the first
	dictGen int

	dictPill        pillWindow
	dictTray        trayState
	dictSet         dictSettingsForm // dictation_settings.go
	dictSoundsState                  // dictation_sounds.go
	perms           permissionsState // permissions.go

	// dictSleep waits before the pill hides (tests replace it).
	dictAfter func(d time.Duration, fn func())
}

// dictSession is one dictation.
type dictSession struct {
	gen      int
	target   dictation.Target
	phase    dictPhase
	hold     bool // hold mode: the release stops it
	pressed  time.Time
	released bool // released before the recording started
	rec      *engine.Recording
	out      string
	started  time.Time
	level    float64
	aecAuto  string // why the engine turned echo cancellation on (mic_aec.go)
	trID     string
	jobID    string
	stage    string // what the transcription does, translated
	text     string // the text delivered
	note     string // why it was copied, an error…: translated
	need     *catalog.Entry
	cancel   context.CancelFunc // clean-up and insertion
	warming  bool               // clean-up waits for the language model to load
	// The model: release ends the hold on the engine's models (#109, from
	// the start to the end); loading: the transcription waits for the
	// model to load (unloaded after a long pause), said in the pill once
	// it takes longer than dictLoadingAfter (slow).
	release func()
	loading bool
	slow    bool
}

// dictLoadingAfter is how long a transcription may wait for its model
// before the pill says 「正在加载模型…」 (a warm one takes about 0.2 s).
const dictLoadingAfter = 500 * time.Millisecond

// openDictation sets dictation's defaults; connectDictation adds the OS
// side when the app is ready.
func (a *app) openDictation() {
	if a.dictSys == nil {
		a.dictSys = dictation.Native()
	}
	if a.dictClip == nil {
		a.dictClip = mygoClip{}
	}
	if a.dictAfter == nil {
		a.dictAfter = func(d time.Duration, fn func()) {
			if a.win == nil {
				return // tests call dictHide themselves
			}
			time.AfterFunc(d, func() { a.post(fn) })
		}
	}
	a.openDictSounds() // dictation_sounds.go
	a.loadDictSettingsForm()
}

// connectDictation registers the shortcut and its release, and makes the
// menu bar icon; it runs in App.WhenReady.
func (a *app) connectDictation() {
	a.dictKeys = mygoHotkeys{}
	// MyGo's RunOnMain runs fn at once on the main thread, and drops it
	// once the app quit, where waiting on win.Update would hang (#101 L7).
	dictation.MainThread = mygo.RunOnMain
	if err := a.dictSys.OnHotkeyReleased(func() { a.post(a.dictReleased) }); err != nil && !errors.Is(err, dictation.ErrUnsupported) {
		a.logs.Append(core.LogEntry{Level: core.SeverityWarning, Source: "dictation", Message: "hold-to-talk: " + err.Error()})
	}
	a.applyDictShortcut()
	a.openTray()
	a.connectWindowClose()
	a.recheckPermissions()
}

// closeDictation ends a dictation and releases the shortcut; on quit.
func (a *app) closeDictation() {
	if s := a.dict; s != nil && s.phase.live() {
		a.dictCancel()
	}
	if a.dictKeys != nil && a.dictShortcut != "" {
		a.dictKeys.Unregister(a.dictShortcut)
		a.dictShortcut = ""
	}
	a.closeTray()
}

// applyDictShortcut registers the settings' shortcut (or none), the new
// one before the old one goes, so a refused shortcut keeps the working one.
func (a *app) applyDictShortcut() {
	if a.dictKeys == nil {
		return
	}
	d := a.prefs.Dictation
	want := ""
	if d.Enabled {
		if s, err := dictation.ParseShortcut(d.Shortcut); err == nil {
			want = s.String()
		} else {
			a.dictRegErr = shortcutErrorText(err)
		}
	}
	if want == a.dictShortcut {
		return
	}
	if want != "" {
		if err := a.dictKeys.Register(want, a.dictHotkey); err != nil {
			a.dictRegErr = i18n.T("%s could not be registered: another app may use it. Choose another shortcut.", dictation.GlyphsOf(want))
			return
		}
	}
	if a.dictShortcut != "" {
		a.dictKeys.Unregister(a.dictShortcut)
	}
	a.dictShortcut, a.dictRegErr, a.dictVerified = want, "", false
	a.updateTrayMenu()
}

// dictHotkey is the shortcut's callback (main thread): the app in front
// is captured before anything else shows.
func (a *app) dictHotkey() {
	target := a.dictSys.Frontmost()
	a.post(func() { a.dictPressed(target) })
}

// dictModel is the model dictation transcribes with.
func (a *app) dictModel() catalog.Entry {
	return sttEntry(a.prefs.Dictation.Model, a.prefs.Dictation.Language)
}

// dictPressed handles a press of the shortcut (or Start in the tray).
func (a *app) dictPressed(target dictation.Target) {
	a.dictVerified = true
	s := a.dict
	if s != nil {
		switch s.phase {
		case dictStarting, dictListening:
			if !s.hold {
				a.dictStop()
			}
			return
		case dictTranscribing, dictCleaning, dictInserting:
			return // one at a time: this one finishes first
		}
	}
	a.dictStart(target, a.prefs.Dictation.Mode == settings.DictationHold)
}

// dictReleased handles the release of the shortcut's keys (hold mode).
func (a *app) dictReleased() {
	s := a.dict
	if s == nil || !s.hold {
		return
	}
	if time.Since(s.pressed) < holdTap {
		s.hold = false // a tap: listen until the next press
		return
	}
	switch s.phase {
	case dictStarting:
		s.released = true
	case dictListening:
		a.dictStop()
	}
}

// dictToggle starts or stops dictation from the tray or the palette.
func (a *app) dictToggle() {
	if s := a.dict; s != nil && (s.phase == dictStarting || s.phase == dictListening) {
		a.dictStop()
		return
	}
	a.dictPressed(a.dictSys.Frontmost())
}

// dictTranscriptChanged: nothing to do yet (the pill shows the text it
// delivered); kept as the place for live updates of a dictation's entry.
func (a *app) dictTranscriptChanged(core.TranscriptChanged) {}

// dictShow shows the pill and the recording state of the tray, and lets
// Escape cancel.
func (a *app) dictShow() {
	a.showPill()
	a.dictEscape(true)
	a.updateTray()
}

// dictHide hides the pill.
func (a *app) dictHide() {
	a.dictEscape(false)
	a.hidePill()
	a.updateTray()
}

// dictEscape registers Escape (cancel) while a dictation works: the pill
// never takes the keyboard, so Escape has to be a global shortcut then.
func (a *app) dictEscape(on bool) {
	if a.dictKeys == nil || on == a.dictEsc {
		return
	}
	if on {
		if err := a.dictKeys.Register("Escape", func() { a.post(a.dictCancel) }); err == nil {
			a.dictEsc = true
		}
		return
	}
	a.dictKeys.Unregister("Escape")
	a.dictEsc = false
}

// dictRefresh redraws the pill after a change.
func (a *app) dictRefresh() {
	if w := a.dictPill.win; w != nil {
		w.Invalidate()
	}
}

// dictStatus is a dictation's state as the pill says it.
func (s *dictSession) status() string {
	switch s.phase {
	case dictStarting:
		return i18n.T("Starting…")
	case dictListening:
		return i18n.T("Listening…")
	case dictTranscribing:
		if s.loading && s.slow {
			return i18n.T("Loading model…")
		}
		return s.stage
	case dictCleaning:
		if s.warming {
			return i18n.T("Loading model…")
		}
		return i18n.T("Cleaning up…")
	case dictInserting:
		return i18n.T("Typing…")
	case dictInserted:
		return i18n.T("Inserted")
	case dictCopied:
		return i18n.T("Copied to the clipboard")
	case dictNoSpeech:
		return i18n.T("No speech detected")
	case dictError:
		return i18n.T("Dictation failed")
	}
	return ""
}

// shortcutErrorText explains a shortcut the parser refused.
func shortcutErrorText(err error) string {
	switch {
	case errors.Is(err, dictation.ErrNoModifier):
		return i18n.T("Add a modifier: ⌘, ⌃ or ⌥ with another key.")
	case errors.Is(err, dictation.ErrEmpty):
		return i18n.T("No shortcut is set.")
	}
	return i18n.T("This key cannot be a global shortcut.")
}
