package main

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// kitView wraps a view in the kit's theme, as applyTheme does.
func kitView(fn func(c *ui.Context)) func(c *ui.Context) {
	return func(c *ui.Context) {
		th := *c.Theme()
		styleTheme(&th)
		c.SetTheme(&th)
		ui.Column(c).Padding(16).Gap(8).Children(func() { fn(c) })
	}
}

// TestSubmitButton: the round submit button takes no click until ready.
func TestSubmitButton(t *testing.T) {
	ready, clicks := false, 0
	tt := ui.NewTester(kitView(func(c *ui.Context) {
		if submitButton(c.Key("send"), "Send", ready).Clicked() {
			clicks++
		}
	}), 400, 200)
	tt.Frame()
	if err := tt.Click("Send"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if clicks != 0 {
		t.Fatalf("a disabled submit button was clicked %d times", clicks)
	}
	ready = true
	tt.Frame()
	tt.Click("Send")
	tt.Frame()
	if clicks != 1 {
		t.Fatalf("clicks = %d, want 1 once ready", clicks)
	}
}

// TestFilterChip: "+ Language" opens its menu; choosing a value makes it
// the ink "× Language | English" chip, whose × clears it.
func TestFilterChip(t *testing.T) {
	value := ""
	tt := ui.NewTester(kitView(func(c *ui.Context) {
		b, cleared := filterChip(c.Key("f-lang"), "Language", value)
		b.Menu(func(m *ui.Menu) {
			for _, v := range []string{"English", "Chinese"} {
				if m.Item(v).Checked(v == value).Chosen() {
					value = v
				}
			}
		})
		if cleared {
			value = ""
		}
	}), 400, 200)
	tt.Frame()
	if tt.HasText("Clear Language") {
		t.Fatal("an unset filter shows ×")
	}
	if err := tt.Click("Language"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.ChooseMenuItem("English"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if value != "English" {
		t.Fatalf("value = %q after choosing English", value)
	}
	if _, ok := tt.Find("Language: English"); !ok {
		t.Fatalf("no active chip; texts %q", tt.Texts())
	}
	if err := tt.Click("Clear Language"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if value != "" {
		t.Fatalf("value = %q after ×", value)
	}
	if _, ok := tt.Find("Clear Language"); ok {
		t.Fatal("× still shows once cleared")
	}
}

// TestUnderlineTabs: a click and the arrows choose a tab; Changed
// reports it.
func TestUnderlineTabs(t *testing.T) {
	sel, changes := 0, 0
	tt := ui.NewTester(kitView(func(c *ui.Context) {
		if underlineTabs(c.Key("tabs"), &sel, "Settings", "History", "Help").Label("Panel").Changed() {
			changes++
		}
	}), 400, 200)
	tt.Frame()
	if err := tt.Click("History"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if sel != 1 || changes != 1 {
		t.Fatalf("after a click: sel %d, changes %d; want 1, 1", sel, changes)
	}
	tt.Key(0, ui.KeyRight)
	tt.Frame()
	tt.Frame()
	if sel != 2 {
		t.Fatalf("after →: sel %d, want 2", sel)
	}
}

// TestSegmented: the kit's segmented control chooses on a click, as
// ui.Segmented did.
func TestSegmented(t *testing.T) {
	sel := 0
	tt := ui.NewTester(kitView(func(c *ui.Context) {
		segmentedFit(c.Key("seg"), &sel, "System", "Light", "Dark").Label("Theme")
	}), 400, 200)
	tt.Frame()
	tt.Click("Dark")
	tt.Frame()
	if sel != 2 {
		t.Fatalf("sel %d, want 2", sel)
	}
}

// TestIntroDialogOnce: the intro dialog shows until its button closes
// it, is remembered in the settings, and never shows again, nor in a new
// session.
func TestIntroDialogOnce(t *testing.T) {
	a, _, _ := newTestApp(t)
	in := intro{Title: "Introducing dubbing", Icon: icons.VideoFramePlayHorizontal, Points: [3]introPoint{
		{icons.Global, "Any language", ""}, {icons.UserSpeakRounded, "Voices kept", ""}, {icons.Pen, "Edit lines", ""},
	}}
	tt := ui.NewTester(func(c *ui.Context) {
		a.applyTheme(c)
		ui.Column(c).Children(func() { ui.Text(c, "page") })
		a.introDialog(c, "intro:test", in)
	}, 900, 640)
	tt.Frame()
	if !tt.HasText("Introducing dubbing") {
		t.Fatalf("the dialog does not show; texts %q", tt.Texts())
	}
	tt.Frame()
	if !tt.HasText("Introducing dubbing") {
		t.Fatal("the dialog went away by itself")
	}
	if err := tt.Click("Get started"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if tt.HasText("Introducing dubbing") {
		t.Fatal("the dialog still shows after Get started")
	}
	if !a.seen("intro:test") || !slices.Contains(a.settings.Get().Appearance.Seen, "intro:test") {
		t.Fatalf("not remembered: prefs %q, saved %q", a.prefs.Appearance.Seen, a.settings.Get().Appearance.Seen)
	}
	a.intro = introState{} // as in a new session
	tt.Frame()
	tt.Frame()
	if tt.HasText("Introducing dubbing") {
		t.Fatal("the dialog showed again")
	}

	// Escape closes another one, which is remembered too.
	tt2 := ui.NewTester(func(c *ui.Context) {
		a.applyTheme(c)
		a.introDialog(c, "intro:other", intro{Title: "Introducing stories"})
	}, 900, 640)
	tt2.Frame()
	if !tt2.HasText("Introducing stories") {
		t.Fatal("the second dialog does not show")
	}
	tt2.Key(0, ui.KeyEscape)
	tt2.Frame()
	tt2.Frame()
	if tt2.HasText("Introducing stories") || !a.seen("intro:other") {
		t.Fatal("Escape did not close and remember the dialog")
	}
}

// TestSlimSlider: the slim slider keeps ui.Slider's keys and snaps by its
// step.
func TestSlimSlider(t *testing.T) {
	v := 1.0
	tt := ui.NewTester(kitView(func(c *ui.Context) {
		slimStepSlider(c.Key("speed"), &v, 0.5, 2, 0.25).Label("Speed").Width(200)
	}), 400, 200)
	tt.Frame()
	tt.Click("Speed")
	tt.Frame()
	tt.Key(0, ui.KeyRight)
	tt.Frame()
	if v != 1.25 && v != 1.5 { // the click may land on 1.25 first
		t.Fatalf("v = %v after →", v)
	}
	if r := (v - 0.5) / 0.25; r != float64(int(r)) {
		t.Fatalf("v = %v is not on a step", v)
	}
}
