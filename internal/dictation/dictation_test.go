package dictation

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/egoist/mygo/transfer"
)

var ctx = context.Background()

func noSleep(context.Context, time.Duration) {}

func inserter(f *Fake) Inserter { return Inserter{Sys: f, Clip: f, Sleep: noSleep} }

func TestInsertPastesAndRestores(t *testing.T) {
	f := NewFake()
	html := transfer.NewItem(transfer.Bytes(transfer.HTML, []byte("<b>mine</b>")), transfer.Bytes(transfer.Text, []byte("mine")))
	f.Write(transfer.New(html))
	target := Target{PID: 7, Name: "Slack"}
	res := inserter(f).Insert(ctx, target, "Hello there.")
	if res.Outcome != Inserted || !res.Restored || res.Reason != "" {
		t.Fatalf("%+v", res)
	}
	if len(f.Activated) != 1 || f.Activated[0].PID != 7 || len(f.Pastes) != 1 || f.Pastes[0] != "Hello there." {
		t.Fatalf("activated %v pastes %v", f.Activated, f.Pastes)
	}
	// Every representation came back.
	if b, err := f.Clip.Read(transfer.HTML); err != nil || string(b) != "<b>mine</b>" || f.ClipText() != "mine" {
		t.Fatalf("clipboard %q %v", b, err)
	}
}

func TestInsertKeepsWhatWasCopiedMeanwhile(t *testing.T) {
	f := NewFake()
	f.Write(transfer.TextData("old"))
	f.OnPaste = func() { f.Write(transfer.TextData("copied meanwhile")) }
	res := inserter(f).Insert(ctx, Target{PID: 7}, "dictated")
	if res.Outcome != Inserted || res.Restored || f.ClipText() != "copied meanwhile" {
		t.Fatalf("%+v %q", res, f.ClipText())
	}
}

func TestInsertCopies(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(*Fake, *Inserter)
		target Target
		reason string
	}{
		{"copy only", func(f *Fake, in *Inserter) { in.CopyOnly = true }, Target{PID: 7}, ReasonCopyOnly},
		{"no access", func(f *Fake, in *Inserter) { f.Trusted = false }, Target{PID: 7}, ReasonNoAccess},
		{"self", func(f *Fake, in *Inserter) { f.Front = Target{PID: 1} }, Target{}, ReasonNoTarget},
		{"not activated", func(f *Fake, in *Inserter) { f.ActivateOK = false }, Target{PID: 7}, ReasonNotActivated},
		{"paste failed", func(f *Fake, in *Inserter) { f.PasteErr = errors.New("no") }, Target{PID: 7}, ReasonPasteFailed},
	}
	for _, c := range cases {
		f := NewFake()
		f.Write(transfer.TextData("old"))
		in := inserter(f)
		c.setup(f, &in)
		res := in.Insert(ctx, c.target, "dictated")
		if res.Outcome != Copied || res.Reason != c.reason || f.ClipText() != "dictated" {
			t.Errorf("%s: %+v clipboard %q", c.name, res, f.ClipText())
		}
	}
	// Without a remembered target, the frontmost app gets it.
	f := NewFake()
	if res := inserter(f).Insert(ctx, Target{}, "x"); res.Outcome != Inserted || f.Activated[0].Name != "Notes" {
		t.Errorf("frontmost: %+v %v", res, f.Activated)
	}
}

func TestParseShortcut(t *testing.T) {
	for in, want := range map[string]string{
		"CmdOrCtrl+Shift+Space": "CmdOrCtrl+Shift+Space",
		"cmd+shift+space":       "CmdOrCtrl+Shift+Space",
		"Alt+d":                 "Alt+D",
		"Ctrl+Option+Return":    "Ctrl+Alt+Enter",
		"F5":                    "F5",
		"Command+Plus":          "CmdOrCtrl+=",
		"CmdOrCtrl++":           "CmdOrCtrl+Shift+=",
		"Cmd+/":                 "CmdOrCtrl+/",
		"Shift+F12":             "Shift+F12",
	} {
		s, err := ParseShortcut(in)
		if err != nil || s.String() != want {
			t.Errorf("%q: %q %v, want %q", in, s.String(), err, want)
		}
	}
	for in, want := range map[string]error{"": ErrEmpty, "Space": ErrNoModifier, "Shift+A": ErrNoModifier, "Cmd+Hyper+A": ErrBadKey, "Cmd+Pause": ErrBadKey} {
		if _, err := ParseShortcut(in); !errors.Is(err, want) {
			t.Errorf("%q: %v, want %v", in, err, want)
		}
	}
	wantSpace, wantLeft := "⇧⌘Space", "⌃⌥←"
	if runtime.GOOS == "windows" {
		wantSpace, wantLeft = "Ctrl+Shift+Space", "Ctrl+Alt+Left"
	}
	if g := GlyphsOf("CmdOrCtrl+Shift+Space"); g != wantSpace {
		t.Errorf("glyphs %q, want %q", g, wantSpace)
	}
	if g := GlyphsOf("Ctrl+Alt+Left"); g != wantLeft {
		t.Errorf("glyphs %q, want %q", g, wantLeft)
	}
}

func TestConflicts(t *testing.T) {
	s, _ := ParseShortcut("Cmd+Space")
	if c := Conflicts(s, nil); len(c) != 1 || c[0].Owner != "system" || c[0].What != "Spotlight" {
		t.Errorf("%+v", c)
	}
	s, _ = ParseShortcut("Cmd+K")
	if c := Conflicts(s, map[string]string{"CmdOrCtrl+K": "Command palette"}); len(c) != 1 || c[0].Owner != "app" {
		t.Errorf("%+v", c)
	}
	s, _ = ParseShortcut("CmdOrCtrl+Shift+Space")
	if c := Conflicts(s, map[string]string{"CmdOrCtrl+K": "Command palette"}); len(c) != 0 {
		t.Errorf("default conflicts: %+v", c)
	}
}

func TestNativeOffMac(t *testing.T) {
	n := Native()
	if n.Self() <= 0 {
		t.Error("self")
	}
	// Linux: everything says no; the app falls back to copying.
	if n.Frontmost().Valid() && n.Paste() == nil {
		t.Skip("running on macOS")
	}
	done := make(chan bool, 1)
	n.RequestMicrophone(func(ok bool) { done <- ok })
	if <-done {
		t.Error("microphone granted off macOS")
	}
}

// The family hears when the paste starts and ends: around the clipboard
// write, ⌘V and the restore; nothing when the text is only copied.
func TestInsertTellsPasting(t *testing.T) {
	f := NewFake()
	f.Write(transfer.TextData("old"))
	var seen []string
	in := inserter(f)
	in.Pasting = func(_ context.Context, on bool) {
		seen = append(seen, map[bool]string{true: "on:" + f.ClipText(), false: "off:" + f.ClipText()}[on])
	}
	f.OnPaste = func() { seen = append(seen, "paste") }
	if res := in.Insert(ctx, Target{PID: 7}, "dictated"); res.Outcome != Inserted || !res.Restored {
		t.Fatalf("%+v", res)
	}
	if want := []string{"on:old", "paste", "off:old"}; len(seen) != 3 || seen[0] != want[0] || seen[1] != want[1] || seen[2] != want[2] {
		t.Errorf("pasting %v, want %v", seen, want)
	}
	seen = nil
	f.ActivateOK = false // gives up after writing the clipboard
	in.Insert(ctx, Target{PID: 7}, "dictated")
	if len(seen) != 2 || seen[1][:4] != "off:" {
		t.Errorf("a paste given up: %v", seen)
	}
	seen = nil
	in.CopyOnly = true
	in.Insert(ctx, Target{PID: 7}, "dictated")
	if len(seen) != 0 {
		t.Errorf("copy only: %v", seen)
	}
}

func TestWindowsGlyphsAndElevatedFallback(t *testing.T) {
	orig := glyphsOS
	t.Cleanup(func() { glyphsOS = orig })

	glyphsOS = "darwin"
	if got := GlyphsOf("Alt+Space"); got != "⌥Space" {
		t.Errorf("darwin GlyphsOf(Alt+Space) = %q, want ⌥Space", got)
	}
	if got := GlyphsOf("Ctrl+Shift+D"); got != "⌃⇧D" {
		t.Errorf("darwin GlyphsOf(Ctrl+Shift+D) = %q, want ⌃⇧D", got)
	}

	glyphsOS = "windows"
	if got := GlyphsOf("Alt+Space"); got != "Alt+Space" {
		t.Errorf("windows GlyphsOf(Alt+Space) = %q, want Alt+Space", got)
	}
	if got := GlyphsOf("Ctrl+Shift+D"); got != "Ctrl+Shift+D" {
		t.Errorf("windows GlyphsOf(Ctrl+Shift+D) = %q, want Ctrl+Shift+D", got)
	}
	if got := GlyphsOf("CmdOrCtrl+Shift+Space"); got != "Ctrl+Shift+Space" {
		t.Errorf("windows GlyphsOf(CmdOrCtrl+Shift+Space) = %q, want Ctrl+Shift+Space", got)
	}

	// Elevated target (UIPI): falls back to clipboard with ErrElevatedTarget.
	f := NewFake()
	f.PasteErr = ErrElevatedTarget
	res := inserter(f).Insert(context.Background(), Target{PID: 7}, "elevated text")
	if res.Outcome != Copied || res.Reason != ReasonPasteFailed || !errors.Is(res.Err, ErrElevatedTarget) {
		t.Fatalf("unexpected elevated result: %+v", res)
	}
	if f.ClipText() != "elevated text" {
		t.Errorf("clipboard = %q, want dictated text preserved", f.ClipText())
	}
}
