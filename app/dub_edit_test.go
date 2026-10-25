package main

import (
	"testing"

	"github.com/hehehai/AudioSmith/internal/dub"
)

// TestDubEditUntranslatedOpensOnSource: a line with no translation yet
// shows its source; clicking it opens the editor on that same text (it
// used to open empty), and Done without a change leaves it untranslated.
func TestDubEditUntranslatedOpensOnSource(t *testing.T) {
	p := &dub.Project{
		Segments: []dub.Segment{{ID: "s1", Text: "It is October 20th, 2026."}},
	}
	a := &app{dub: p}
	a.dubLang = "zh-Hans"
	a.dubBeginEdit("s1", "zh-Hans")
	if a.dubBuf != "It is October 20th, 2026." {
		t.Fatalf("the editor opened on %q, not the line's text", a.dubBuf)
	}
	a.dubCommitEdit()
	if got := p.TextFor(p.Segments[0], "zh-Hans"); got != "" {
		t.Fatalf("Done without a change made a translation: %q", got)
	}
}
