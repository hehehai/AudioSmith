package main

import (
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"
)

// shortcutOS controls how shortcut glyphs are formatted in the UI.
// Defaults to runtime.GOOS; tests may override it temporarily.
var shortcutOS = runtime.GOOS

// isWindowsShortcuts reports whether UI shortcut labels should use
// Windows conventions (Ctrl, Alt, Shift, Enter) instead of macOS glyphs
// (⌘, ⌥, ⇧, ⌃, ↩).
func isWindowsShortcuts() bool { return shortcutOS == "windows" }

// modKey returns the primary modifier label ("Ctrl" on Windows, "⌘" on macOS).
func modKey() string {
	if isWindowsShortcuts() {
		return "Ctrl"
	}
	return "⌘"
}

// shortcutLabel translates macOS shortcut glyph notation in s (such as
// "⌘K", "⇧⌘L", "⌥1", "Undo (⌘Z)", "⌘+scroll", "↩ send · ⇧↩ new line")
// to Windows notation ("Ctrl+K", "Ctrl+Shift+L", "Alt+1", "Undo (Ctrl+Z)",
// "Ctrl+scroll", "Enter send · Shift+Enter new line") when running on
// Windows. On macOS and other non-Windows platforms it returns s
// unchanged.
func shortcutLabel(s string) string {
	if !isWindowsShortcuts() || s == "" {
		return s
	}
	if !strings.ContainsAny(s, "⌘⌥⇧⌃↩") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	i := 0
	for i < len(s) {
		switch {
		case strings.HasPrefix(s[i:], "⇧⌘"):
			b.WriteString("Ctrl+Shift")
			i += len("⇧⌘")
			if nextIsKey(s[i:]) {
				b.WriteByte('+')
			}
		case strings.HasPrefix(s[i:], "⌃⇧"):
			b.WriteString("Ctrl+Shift")
			i += len("⌃⇧")
			if nextIsKey(s[i:]) {
				b.WriteByte('+')
			}
		case strings.HasPrefix(s[i:], "⌘"):
			b.WriteString("Ctrl")
			i += len("⌘")
			if nextIsKey(s[i:]) {
				b.WriteByte('+')
			}
		case strings.HasPrefix(s[i:], "⌃"):
			b.WriteString("Ctrl")
			i += len("⌃")
			if nextIsKey(s[i:]) {
				b.WriteByte('+')
			}
		case strings.HasPrefix(s[i:], "⌥"):
			b.WriteString("Alt")
			i += len("⌥")
			if nextIsKey(s[i:]) {
				b.WriteByte('+')
			}
		case strings.HasPrefix(s[i:], "⇧"):
			b.WriteString("Shift")
			i += len("⇧")
			if nextIsKey(s[i:]) {
				b.WriteByte('+')
			}
		case strings.HasPrefix(s[i:], "↩"):
			b.WriteString("Enter")
			i += len("↩")
		default:
			r, sz := utf8.DecodeRuneInString(s[i:])
			b.WriteRune(r)
			i += sz
		}
	}
	return b.String()
}

// nextIsKey reports whether the start of rest is a key symbol that
// should be joined to a preceding modifier with "+" on Windows (as
// opposed to whitespace, prose punctuation, or an existing "+" / "-").
func nextIsKey(rest string) bool {
	if rest == "" {
		return false
	}
	r, sz := utf8.DecodeRuneInString(rest)
	switch r {
	case ' ', '\t', '\n', '+', '-', '＋', '、', ')', '）', '·', '。', '.':
		return false
	case ',':
		if sz < len(rest) && rest[sz] == ' ' {
			return false
		}
		return true
	}
	return !unicode.IsSpace(r)
}
