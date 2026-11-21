package main

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestSelectListScrolls: a select with many options opens a list that
// stays inside the window and scrolls (MyGo 0.6 and later), rather
// than running off the bottom with options out of reach.
func TestSelectListScrolls(t *testing.T) {
	var opts []string
	for i := range 40 {
		opts = append(opts, fmt.Sprintf("voice %02d", i))
	}
	cur := opts[0]
	view := func(c *ui.Context) {
		ui.Column(c).Padding(20).AlignItems(ui.Start).Children(func() {
			ui.Select(c.Key("s"), &cur, opts).Label("Voice").Width(200)
		})
	}
	const h = 400
	tt := ui.NewTester(view, 400, h)
	tt.Frame()
	if err := tt.Click("Voice"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	last := opts[len(opts)-1]
	if r, ok := tt.Find(last); ok && r.Y+r.H <= h {
		t.Fatalf("all 40 options fit a %d-high window? %q at %v", h, last, r)
	}
	first, ok := tt.Find(opts[0] + "") // the list's first option (or the trigger)
	if !ok {
		t.Fatal("no first option")
	}
	for range 30 {
		tt.Scroll(first.X+20, 200, 0, 400)
		tt.Frame()
	}
	r, ok := tt.Find(last)
	if !ok || r.Y+r.H > h || r.Y < 0 {
		t.Errorf("scrolled to the end, %q is at %v (found %v), not inside the window", last, r, ok)
	}
	if err := tt.Click(last); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if cur != last {
		t.Errorf("chose %q", cur)
	}
}
