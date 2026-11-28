// Package dictation is what system-wide dictation needs of macOS beyond
// MyGo: the app that has the keyboard focus, bringing it back to the
// front, pasting into it with a synthetic ⌘V, the clipboard's change
// count, the release of a global shortcut (MyGo reports presses only),
// the Microphone and Accessibility permissions, and system sounds. On
// darwin it calls AppKit, Carbon, CoreGraphics, HIServices and
// AVFoundation through purego (no cgo); elsewhere Native reports nothing
// and refuses to paste, so the app's tests run on Linux with Fake.
//
// Insert is the delivery algorithm: put the text on the clipboard,
// reactivate the target app, send ⌘V, and put the user's clipboard back
// unless it changed meanwhile.
//
// Imports: nothing internal (mygo/transfer for clipboard contents).
package dictation

import (
	"context"
	"errors"
	"time"

	"github.com/egoist/mygo/transfer"
)

// Target is the app dictation types into: the frontmost app when the
// shortcut was pressed.
type Target struct {
	PID      int
	BundleID string
	Name     string
}

// Valid reports whether the target names an app.
func (t Target) Valid() bool { return t.PID > 0 }

// Permission is the state of a privacy permission.
type Permission int

const (
	PermissionUnknown       Permission = iota // not on macOS, or the query failed
	PermissionNotDetermined                   // never asked
	PermissionDenied
	PermissionRestricted // parental controls / MDM
	PermissionGranted
)

// MainThread runs fn on the app's main thread and waits for it; the app
// sets it (MyGo's Window.Update). Activate makes its AppKit calls through
// it, so Activate must not be called on the main thread. Nil runs them
// where Activate is called.
var MainThread func(fn func())

func onMain(fn func()) {
	if MainThread != nil {
		MainThread(fn)
		return
	}
	fn()
}

// ErrUnsupported: the platform cannot do it (Linux, tests).
var ErrUnsupported = errors.New("dictation: not supported on this system")

// ErrElevatedTarget: on Windows, UIPI blocks synthetic input to a higher-
// privilege (Administrator) foreground window. Insert falls back to the
// clipboard.
var ErrElevatedTarget = errors.New("dictation: target window is elevated (UIPI)")

// System is what dictation needs of the OS. Methods are safe from any
// goroutine unless they say otherwise.
type System interface {
	// Self is this process's pid.
	Self() int
	// Frontmost is the app with the keyboard focus (zero when unknown).
	Frontmost() Target
	// Activate brings t to the front and waits up to wait for it to be
	// frontmost; it reports whether it is.
	Activate(t Target, wait time.Duration) bool
	// Paste sends ⌘V (the key that types "v" with ⌘ in the current
	// keyboard layout) to the frontmost app. It needs Accessibility.
	Paste() error
	// AccessibilityTrusted reports whether AudioSmith may send keys.
	AccessibilityTrusted() bool
	// PromptAccessibility shows the system's prompt, which adds
	// AudioSmith to Privacy & Security → Accessibility.
	PromptAccessibility()
	// Microphone is the microphone permission of the app.
	Microphone() Permission
	// RequestMicrophone asks for it (the system prompt shows once); done
	// runs on another goroutine with the answer.
	RequestMicrophone(done func(granted bool))
	// ClipboardChangeCount counts changes of the general pasteboard; -1
	// when unknown.
	ClipboardChangeCount() int
	// OnHotkeyReleased calls fn on the main thread when the key of a
	// global shortcut registered with mygo.GlobalShortcut goes up. Call it
	// once, on the main thread, after the app is ready.
	OnHotkeyReleased(fn func()) error
	// PlaySound plays a system sound by name ("Tink", "Pop", "Basso").
	PlaySound(name string)
}

// Clipboard is the system clipboard (mygo.Clipboard in the app).
type Clipboard interface {
	// Read copies every representation of the current contents.
	Read() (transfer.Data, error)
	Write(transfer.Data) error
}

// Outcome is how a dictation was delivered.
type Outcome int

const (
	Copied   Outcome = iota // the text is on the clipboard: paste it yourself
	Inserted                // pasted into the target app
)

// Why a dictation was copied rather than inserted (Result.Reason).
const (
	ReasonCopyOnly       = "copy-only"     // the user's setting
	ReasonNoAccess       = "no-access"     // no Accessibility permission
	ReasonNoTarget       = "no-target"     // no app to type into (AudioSmith itself was in front)
	ReasonNotActivated   = "not-activated" // the target app did not come back to the front
	ReasonPasteFailed    = "paste-failed"  // the synthetic ⌘V failed
	ReasonClipboardError = "clipboard"     // the clipboard could not be written
)

// Result says what Insert did.
type Result struct {
	Outcome Outcome
	Reason  string // why it was copied; "" when inserted
	// Restored reports whether the user's clipboard was put back.
	Restored bool
	Err      error
}

// Inserter delivers text into the target app.
type Inserter struct {
	Sys  System
	Clip Clipboard
	// CopyOnly leaves the text on the clipboard without pasting.
	CopyOnly bool
	// ActivateWait bounds the wait for the target to come to the front
	// (default 500 ms); PasteDelay is how long the target gets to read
	// the clipboard before it is restored (default 300 ms).
	ActivateWait, PasteDelay time.Duration
	// Sleep waits (tests); nil sleeps, or stops early when ctx ends.
	Sleep func(ctx context.Context, d time.Duration)
	// Pasting, when set, is told when the paste starts (on: before the
	// clipboard is written; it may wait for another app's paste first)
	// and ends (after the clipboard was restored, or the paste gave up),
	// for the ActNow family's runtime file (spec §11.1 "pasting").
	Pasting func(ctx context.Context, on bool)
}

func (in Inserter) sleep(ctx context.Context, d time.Duration) {
	if in.Sleep != nil {
		in.Sleep(ctx, d)
		return
	}
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// Insert puts text into target: on the clipboard, then, unless CopyOnly
// or without Accessibility, the target is reactivated and sent ⌘V, and
// the clipboard as it was before is restored once the paste had time to
// read it, unless something else changed the clipboard meanwhile. Run it
// on a goroutine: it waits about half a second.
func (in Inserter) Insert(ctx context.Context, target Target, text string) Result {
	copyOnly := func(reason string) Result {
		if err := in.Clip.Write(transfer.TextData(text)); err != nil {
			return Result{Outcome: Copied, Reason: ReasonClipboardError, Err: err}
		}
		return Result{Outcome: Copied, Reason: reason}
	}
	switch {
	case in.CopyOnly:
		return copyOnly(ReasonCopyOnly)
	case !in.Sys.AccessibilityTrusted():
		return copyOnly(ReasonNoAccess)
	}
	if !target.Valid() {
		target = in.Sys.Frontmost()
	}
	if !target.Valid() || target.PID == in.Sys.Self() {
		return copyOnly(ReasonNoTarget)
	}
	if in.Pasting != nil {
		in.Pasting(ctx, true)
		defer in.Pasting(context.WithoutCancel(ctx), false)
	}
	prev, readErr := in.Clip.Read()
	if err := in.Clip.Write(transfer.TextData(text)); err != nil {
		return Result{Outcome: Copied, Reason: ReasonClipboardError, Err: err}
	}
	staged := in.Sys.ClipboardChangeCount()
	wait := in.ActivateWait
	if wait <= 0 {
		wait = 500 * time.Millisecond
	}
	if !in.Sys.Activate(target, wait) {
		return Result{Outcome: Copied, Reason: ReasonNotActivated}
	}
	in.sleep(ctx, 60*time.Millisecond) // the app settles its key window
	if err := in.Sys.Paste(); err != nil {
		return Result{Outcome: Copied, Reason: ReasonPasteFailed, Err: err}
	}
	delay := in.PasteDelay
	if delay <= 0 {
		delay = 300 * time.Millisecond
	}
	in.sleep(context.WithoutCancel(ctx), delay) // the paste must finish before the restore
	res := Result{Outcome: Inserted}
	if readErr == nil && staged >= 0 && in.Sys.ClipboardChangeCount() == staged {
		if err := in.Clip.Write(prev); err == nil {
			res.Restored = true
		}
	}
	return res
}
