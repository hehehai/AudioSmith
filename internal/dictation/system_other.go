//go:build !darwin && !windows

package dictation

import (
	"os"
	"time"
)

// Native is the system outside macOS: it knows no frontmost app, cannot
// paste, and reports permissions as unknown. Dictation then copies.
func Native() System { return other{} }

type other struct{}

func (other) Self() int                           { return os.Getpid() }
func (other) Frontmost() Target                   { return Target{} }
func (other) Activate(Target, time.Duration) bool { return false }
func (other) Paste() error                        { return ErrUnsupported }
func (other) AccessibilityTrusted() bool          { return false }
func (other) PromptAccessibility()                {}
func (other) Microphone() Permission              { return PermissionUnknown }
func (other) RequestMicrophone(done func(bool))   { go done(false) }
func (other) ClipboardChangeCount() int           { return -1 }
func (other) OnHotkeyReleased(func()) error       { return ErrUnsupported }
func (other) PlaySound(string)                    {}
