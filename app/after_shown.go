package main

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// afterShown asks for another build after d, as c.After, unless nothing of
// the window shows (closed to the menu bar, minimized, covered: Context.
// WindowHidden). AudioSmith's clocks and progress (engine busy, a
// transcription running, playback, the recorder) then stop building the
// page; asking WindowHidden makes MyGo build the view once as the window
// hides and again as it shows, so what changed meanwhile shows at once
// (code review 11-02 M2, thread #155; TestNoTimedBuildsWhileHidden).
func afterShown(c *ui.Context, d time.Duration) {
	if !c.WindowHidden() {
		c.After(d)
	}
}
