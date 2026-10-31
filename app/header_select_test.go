package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// A header's filter menus stand as tall as its search field and line up
// with it (user, 10-31).
func TestHeaderSelectsMatchSearch(t *testing.T) {
	a, _, _ := newTestApp(t)
	tt := ui.NewTester(a.view, 1300, 800)
	for _, c := range []struct{ path, search, sel string }{
		{"/assets", "Search assets", "Type"},
		{"/projects", "Search projects", "Type"},
	} {
		a.navigate(c.path)
		tt.Frame()
		tt.Frame()
		s, ok1 := tt.Find(c.search)
		m, ok2 := tt.Find(c.sel)
		if !ok1 || !ok2 {
			t.Fatalf("%s: %v %v %q", c.path, ok1, ok2, tt.Texts())
		}
		if d := s.H - m.H; d > 1 || d < -1 {
			t.Errorf("%s: search %.0f tall, select %.0f", c.path, s.H, m.H)
		}
		if d := s.Y - m.Y; d > 1 || d < -1 {
			t.Errorf("%s: search at %.0f, select at %.0f", c.path, s.Y, m.Y)
		}
	}
}
