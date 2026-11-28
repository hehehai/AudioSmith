package main

import "testing"

func TestShortcutLabel(t *testing.T) {
	orig := shortcutOS
	t.Cleanup(func() { shortcutOS = orig })

	cases := []struct {
		in  string
		win string
	}{
		{"⌘F", "Ctrl+F"},
		{"⌘K", "Ctrl+K"},
		{"⌘,", "Ctrl+,"},
		{"⌘1 … ⌘4", "Ctrl+1 … Ctrl+4"},
		{"⌘[  ⌘]", "Ctrl+[  Ctrl+]"},
		{"⇧⌘L", "Ctrl+Shift+L"},
		{"⌘↩", "Ctrl+Enter"},
		{"⌥1", "Alt+1"},
		{"⌥/", "Alt+/"},
		{"Undo (⌘Z)", "Undo (Ctrl+Z)"},
		{"撤销（⌘Z）", "撤销（Ctrl+Z）"},
		{"取り消す（⇧⌘Z）", "取り消す（Ctrl+Shift+Z）"},
		{"Zoom out (⌘−)", "Zoom out (Ctrl+−)"},
		{"Zoom in (⌘=)", "Zoom in (Ctrl+=)"},
		{"Actual size (⌘0)", "Actual size (Ctrl+0)"},
		{"⌘+scroll or pinch to zoom · ⇧+scroll or swipe sideways to pan · double-click to fit (⌘0)",
			"Ctrl+scroll or pinch to zoom · Shift+scroll or swipe sideways to pan · double-click to fit (Ctrl+0)"},
		{"⌘＋スクロールまたはピンチでズーム · ⇧＋スクロールまたは横スワイプでパン",
			"Ctrl＋スクロールまたはピンチでズーム · Shift＋スクロールまたは横スワイプでパン"},
		{"⌘-scroll zooms", "Ctrl-scroll zooms"},
		{"Precise editing: ←/→ nudge · ⇧ end · ⌥ move · S snap · Esc",
			"Precise editing: ←/→ nudge · Shift end · Alt move · S snap · Esc"},
		{"↩ send · ⇧↩ new line", "Enter send · Shift+Enter new line"},
		{"Press the keys together, e.g. ⌥Space or ⌃⇧D. Esc cancels.",
			"Press the keys together, e.g. Alt+Space or Ctrl+Shift+D. Esc cancels."},
		{"Changing it only in the app (⌘,): tell the user",
			"Changing it only in the app (Ctrl+,): tell the user"},
		{"plain text", "plain text"},
	}

	// macOS: 100% unchanged.
	shortcutOS = "darwin"
	if got := modKey(); got != "⌘" {
		t.Errorf("darwin modKey() = %q, want ⌘", got)
	}
	for _, tc := range cases {
		if got := shortcutLabel(tc.in); got != tc.in {
			t.Errorf("darwin shortcutLabel(%q) = %q, want unchanged", tc.in, got)
		}
	}

	// Windows: translated to Ctrl/Alt/Shift/Enter.
	shortcutOS = "windows"
	if got := modKey(); got != "Ctrl" {
		t.Errorf("windows modKey() = %q, want Ctrl", got)
	}
	for _, tc := range cases {
		if got := shortcutLabel(tc.in); got != tc.win {
			t.Errorf("windows shortcutLabel(%q) = %q, want %q", tc.in, got, tc.win)
		}
	}
	if got := tip("Settings", "⌘,"); got != "Settings (Ctrl+,)" {
		t.Errorf("windows tip(Settings, ⌘,) = %q", got)
	}
}
