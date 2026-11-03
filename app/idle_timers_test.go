package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/store"
)

// countingTester is a Tester over a's view that counts the builds.
func countingTester(a *app) (*ui.Tester, *int) {
	n := new(int)
	tt := ui.NewTester(func(c *ui.Context) { *n++; a.view(c) }, 1100, 720)
	return tt, n
}

// Code review 11-02 M2 (thread #155): clocks build the view once a
// second, and not at all while the window is hidden (closed to the menu
// bar, minimized, covered); shown again, it builds at once with what
// changed meanwhile.
func TestNoTimedBuildsWhileHidden(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.trJob = &trJob{owner: "x", title: "meeting.m4a", stage: "Transcribing", fraction: 0.2, started: time.Now()}
	a.navigate("/transcripts")
	tt, builds := countingTester(a)
	tt.Advance(time.Second)
	*builds = 0
	tt.Advance(10 * time.Second)
	if *builds < 9 || *builds > 11 {
		t.Fatalf("a transcription's clock: %d builds in 10 s, want one a second", *builds)
	}
	tt.SetOccluded(true)
	*builds = 0
	tt.Advance(10 * time.Second)
	if *builds > 1 {
		t.Fatalf("hidden: %d builds in 10 s", *builds)
	}
	a.trJob.stage = "Aligning words" // an update while hidden
	*builds = 0
	tt.SetOccluded(false)
	if *builds == 0 || !tt.HasText("Aligning words · "+formatDuration(time.Since(a.trJob.started).Seconds())) {
		t.Fatalf("shown again: %d builds, shows %q", *builds, tt.Texts())
	}

	// The Studio's job: its seconds count; the rest of the app does not
	// build for it.
	a.trJob = nil
	a.job = &job{owner: studioOwner + "x", kind: "synthesize", started: time.Now(), fraction: 0.3, status: "Generating"}
	a.navigate("/dub")
	tt.Frame()
	*builds = 0
	tt.Advance(10 * time.Second)
	if *builds != 0 {
		t.Errorf("engine busy on /dub: %d builds in 10 s, want none", *builds)
	}
	a.navigate("/studio")
	tt.Frame()
	*builds = 0
	tt.Advance(10 * time.Second)
	if *builds < 9 || *builds > 11 {
		t.Errorf("engine busy on /studio: %d builds in 10 s, want one a second", *builds)
	}
}

// Rows with a job running build when the job's updates come (as
// Window.Update does), not on a timer, and show its progress.
func TestRunningRowsFollowUpdates(t *testing.T) {
	a, _, _ := newTestApp(t)
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.Local)
	for i := range 3 {
		a.dubList = append(a.dubList, store.DubRecord{ID: fmt.Sprintf("d%d", i), CreatedAt: at, UpdatedAt: at, Title: fmt.Sprintf("video %d.mp4", i), Source: "/x/v.mp4", Languages: []string{"zh"}, Phase: "done", Duration: 60})
	}
	update := func(kind string) {
		a.jobsChanged(jobs.Job{ID: "jd", Owner: services.DubOwner + "d1", Kind: kind, State: jobs.StateRunning, Progress: jobs.Progress{Fraction: 0.5}, Created: at})
	}
	update("dub.translate")
	a.navigate("/dub")
	tt, builds := countingTester(a)
	tt.Advance(time.Second)
	if !tt.HasText("Translating…") {
		t.Fatalf("running row: %q", tt.Texts())
	}
	*builds = 0
	tt.Advance(10 * time.Second)
	if *builds != 0 {
		t.Errorf("running row: %d builds in 10 s without an update", *builds)
	}
	update("dub.synthesize")
	tt.Frame()
	if !tt.HasText("Generating speech…") {
		t.Errorf("after an update: %q", tt.Texts())
	}
}
