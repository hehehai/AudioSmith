package dictation

import (
	"errors"
	"runtime"
	"slices"
	"strings"
)

// Shortcut is a parsed accelerator, as mygo.GlobalShortcut takes it
// ("CmdOrCtrl+Shift+Space").
type Shortcut struct {
	Cmd, Ctrl, Alt, Shift bool
	Key                   string // canonical: "A", "1", "Space", "F5", "/"
}

// Shortcut errors.
var (
	ErrNoModifier = errors.New("dictation: a shortcut needs a modifier (⌘, ⌃, ⌥ or ⇧ with another key)")
	ErrBadKey     = errors.New("dictation: this key cannot be a global shortcut")
	ErrEmpty      = errors.New("dictation: no shortcut")
)

// keys a global shortcut may use (what the Carbon hot key API maps, as
// MyGo's darwin backend does), canonical spelling.
var shortcutKeys = func() []string {
	ks := []string{"Space", "Enter", "Tab", "Backspace", "Delete", "Escape", "Home", "End", "PageUp", "PageDown",
		"Left", "Right", "Up", "Down", "-", "=", "[", "]", ";", "'", ",", ".", "/", "\\", "`"}
	for c := 'A'; c <= 'Z'; c++ {
		ks = append(ks, string(c))
	}
	for c := '0'; c <= '9'; c++ {
		ks = append(ks, string(c))
	}
	for i := 1; i <= 20; i++ {
		ks = append(ks, "F"+itoa(i))
	}
	return ks
}()

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

var keyAliases = map[string]string{
	"space": "Space", "return": "Enter", "enter": "Enter", "tab": "Tab", "backspace": "Backspace",
	"delete": "Delete", "del": "Delete", "esc": "Escape", "escape": "Escape", "home": "Home", "end": "End",
	"pageup": "PageUp", "pagedown": "PageDown", "left": "Left", "right": "Right", "up": "Up", "down": "Down",
	"minus": "-", "equal": "=", "plus": "=", "comma": ",", "period": ".", "slash": "/", "backslash": "\\",
	"semicolon": ";", "quote": "'", "backquote": "`", "bracketleft": "[", "bracketright": "]",
}

// ParseShortcut reads an accelerator. Modifiers: Cmd/Command/Super/Meta,
// CmdOrCtrl/CommandOrControl (⌘ on macOS), Ctrl/Control, Alt/Option,
// Shift. The key comes last. A shortcut needs a modifier, except F-keys.
func ParseShortcut(acc string) (Shortcut, error) {
	var s Shortcut
	acc = strings.TrimSpace(acc)
	if acc == "" {
		return s, ErrEmpty
	}
	parts := strings.Split(acc, "+")
	if strings.HasSuffix(acc, "++") || acc == "+" {
		parts = append(strings.Split(strings.TrimSuffix(acc, "++"), "+"), "+")
	}
	for _, p := range parts[:len(parts)-1] {
		switch strings.ToLower(strings.TrimSpace(p)) {
		case "cmd", "command", "super", "meta", "cmdorctrl", "commandorcontrol":
			s.Cmd = true
		case "ctrl", "control":
			s.Ctrl = true
		case "alt", "option", "opt":
			s.Alt = true
		case "shift":
			s.Shift = true
		default:
			return s, ErrBadKey
		}
	}
	key := strings.TrimSpace(parts[len(parts)-1])
	if key == "+" {
		key, s.Shift = "=", true
	}
	if k, ok := keyAliases[strings.ToLower(key)]; ok {
		key = k
	} else if len(key) == 1 {
		key = strings.ToUpper(key)
	} else if strings.HasPrefix(strings.ToUpper(key), "F") {
		key = "F" + key[1:]
	}
	if !slices.Contains(shortcutKeys, key) {
		return s, ErrBadKey
	}
	s.Key = key
	if !s.Cmd && !s.Ctrl && !s.Alt && !s.fKey() {
		return s, ErrNoModifier
	}
	return s, nil
}

func (s Shortcut) fKey() bool { return len(s.Key) > 1 && s.Key[0] == 'F' }

// String is the accelerator MyGo takes: "CmdOrCtrl+Shift+Space".
func (s Shortcut) String() string {
	var p []string
	if s.Cmd {
		p = append(p, "CmdOrCtrl")
	}
	if s.Ctrl {
		p = append(p, "Ctrl")
	}
	if s.Alt {
		p = append(p, "Alt")
	}
	if s.Shift {
		p = append(p, "Shift")
	}
	return strings.Join(append(p, s.Key), "+")
}

var keyGlyphs = map[string]string{
	"Space": "Space", "Enter": "↩", "Tab": "⇥", "Backspace": "⌫", "Delete": "⌦", "Escape": "⎋",
	"Left": "←", "Right": "→", "Up": "↑", "Down": "↓", "PageUp": "⇞", "PageDown": "⇟", "Home": "↖", "End": "↘",
}

// glyphsOS controls how Shortcut.Glyphs formats accelerators.
// Defaults to runtime.GOOS; tests may override it temporarily.
var glyphsOS = runtime.GOOS

// Glyphs shows the shortcut as platform menus do: "⌃⌥⇧⌘Space" on macOS,
// "Ctrl+Alt+Shift+Space" on Windows.
func (s Shortcut) Glyphs() string {
	if glyphsOS == "windows" {
		var parts []string
		if s.Cmd || s.Ctrl {
			parts = append(parts, "Ctrl")
		}
		if s.Alt {
			parts = append(parts, "Alt")
		}
		if s.Shift {
			parts = append(parts, "Shift")
		}
		parts = append(parts, s.Key)
		return strings.Join(parts, "+")
	}
	var b strings.Builder
	if s.Ctrl {
		b.WriteString("⌃")
	}
	if s.Alt {
		b.WriteString("⌥")
	}
	if s.Shift {
		b.WriteString("⇧")
	}
	if s.Cmd {
		b.WriteString("⌘")
	}
	if g, ok := keyGlyphs[s.Key]; ok {
		b.WriteString(g)
	} else {
		b.WriteString(s.Key)
	}
	return b.String()
}

// GlyphsOf shows an accelerator with glyphs, or the text as given when it
// does not parse.
func GlyphsOf(acc string) string {
	s, err := ParseShortcut(acc)
	if err != nil {
		return acc
	}
	return s.Glyphs()
}

// Conflict is a shortcut something else already uses.
type Conflict struct {
	// Owner: "system" (macOS) or "app" (AudioSmith's own window shortcuts,
	// which a global shortcut would take over).
	Owner string
	// What it does, in English (the UI translates it).
	What string
}

var systemShortcuts = map[string]string{
	"CmdOrCtrl+Space":            "Spotlight",
	"CmdOrCtrl+Alt+Space":        "Finder search",
	"Ctrl+Space":                 "Select the previous input source",
	"Ctrl+Alt+Space":             "Select the next input source",
	"CmdOrCtrl+Ctrl+Space":       "Emoji & Symbols",
	"CmdOrCtrl+Tab":              "Switch apps",
	"CmdOrCtrl+`":                "Switch windows",
	"CmdOrCtrl+Q":                "Quit",
	"CmdOrCtrl+W":                "Close the window",
	"CmdOrCtrl+H":                "Hide the app",
	"CmdOrCtrl+M":                "Minimize",
	"CmdOrCtrl+C":                "Copy",
	"CmdOrCtrl+V":                "Paste",
	"CmdOrCtrl+X":                "Cut",
	"CmdOrCtrl+Z":                "Undo",
	"CmdOrCtrl+A":                "Select all",
	"CmdOrCtrl+Shift+3":          "Screenshot",
	"CmdOrCtrl+Shift+4":          "Screenshot of a part",
	"CmdOrCtrl+Shift+5":          "Screenshot and recording",
	"CmdOrCtrl+Ctrl+Q":           "Lock the screen",
	"CmdOrCtrl+Alt+Escape":       "Force quit",
	"CmdOrCtrl+Alt+D":            "Show or hide the Dock",
	"Ctrl+Up":                    "Mission Control",
	"Ctrl+Down":                  "Application windows",
	"CmdOrCtrl+Ctrl+F":           "Full screen",
	"CmdOrCtrl+Shift+/":          "Help menu",
	"CmdOrCtrl+,":                "Settings",
	"CmdOrCtrl+Shift+Q":          "Log out",
	"CmdOrCtrl+Alt+Ctrl+Escape":  "Force quit",
	"CmdOrCtrl+Shift+Backspace":  "Empty the Trash",
	"CmdOrCtrl+Alt+Shift+Escape": "Force quit the front app",
}

// Conflicts lists what else uses s: macOS shortcuts (systemShortcuts) and
// the app's own (app: accelerators of its window shortcuts, with what
// they do).
func Conflicts(s Shortcut, app map[string]string) []Conflict {
	var out []Conflict
	key := s.String()
	if w, ok := systemShortcuts[key]; ok {
		out = append(out, Conflict{Owner: "system", What: w})
	}
	for acc, w := range app {
		if a, err := ParseShortcut(acc); err == nil && a == s {
			out = append(out, Conflict{Owner: "app", What: w})
		}
	}
	return out
}
