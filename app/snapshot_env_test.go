package main

// Reproducible snapshots (refactor batch B0): two runs of TestSnapshots* on
// one commit write the same bytes, so that a refactor is checked pixel for
// pixel against the commit before it (scripts/snapshots.sh, then
// go run ./internal/tools/snapcmp BEFORE AFTER). What used to differ from
// run to run is pinned here, for the snapshot tests only:
//
//   - The clock. Each test runs in a testing/synctest bubble: time.Now is
//     snapEpoch when the test starts and moves on only when every goroutine
//     of the test waits for a timer. That pins what the views print (log
//     times, "Oct 16 09:41", elapsed times, "3 min ago") and the phase of
//     spinners (ui.Tester's clock starts at time.Now). time.Local is UTC,
//     so that machines in other zones draw the same.
//   - One core (GOMAXPROCS 1) while a test runs: MyGo's raster helpers are
//     goroutines kept for the life of the program, which a bubble does not
//     allow; on one core the renderer starts none. (It also suits the
//     shared Linux host.)
//   - Data directories: testDir gives <snapRoot>/001, 002, … in the order
//     asked for, instead of t.TempDir's random names, which the settings,
//     engine and onboarding pages show. snapRoot is emptied when a test
//     starts and locked against another snapshot run of the same user.
//   - Free disk space: diskFree reports snapDiskFree.
//   - TCP ports: freePort gives snapPortBase, snapPortBase+1, … (the calls
//     gateway shows its address).
//   - Randomness: crypto/rand is seeded (testing/cryptotest), so the IDs
//     core.NewID makes (jobs and takes in the operations log's lines,
//     #108) are the same in every run.
//
// A new snapshot test calls runSnapshots and makes its directories with
// testDir and its ports with freePort.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"testing/cryptotest"
	"testing/synctest"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/translate"
)

var (
	// snapEpoch is the time at which every snapshot test starts.
	snapEpoch = time.Date(2026, 10, 16, 9, 41, 0, 0, time.UTC)
	// snapDiskFree is the free space of every disk in snapshots.
	snapDiskFree int64 = 412_300_000_000
	// snapPortBase is the first port freePort gives in snapshots.
	snapPortBase = 47210
)

// snap is the state of the snapshot test running, nil outside one.
var snap *snapState

type snapState struct {
	root   string
	bubble bool // runSnapshots, not runSnapshotsPlaying
	dirs   int
	ports  int
}

// runSnapshots runs body, a snapshot test writing its pictures to the
// directory AUDIOSMITH_SNAPSHOTS names (skipped when unset), reproducibly:
// see the top of this file.
func runSnapshots(t *testing.T, body func(t *testing.T, out string)) {
	pinSnapshots(t, true, body)
}

// runSnapshotsPlaying is runSnapshots for a test that plays audio, outside
// a bubble: MyGo's audio output is a goroutine kept for the life of the
// program, which a bubble does not allow. Its clock is the real one, so
// it must not show times other than those it sets; ui.Tester's clock is
// moved to the phase it has in a bubble (newTestAppIn), which pins the
// spinners. It must not run before a bubbled test that plays audio (none
// does): their players would meet the output across the bubble.
func runSnapshotsPlaying(t *testing.T, body func(t *testing.T, out string)) {
	pinSnapshots(t, false, body)
}

func pinSnapshots(t *testing.T, bubble bool, body func(t *testing.T, out string)) {
	out := os.Getenv("AUDIOSMITH_SNAPSHOTS")
	if out == "" {
		t.Skip("set AUDIOSMITH_SNAPSHOTS to a directory")
	}
	old := settingsH
	settingsH = settingsHMax // as tall as in the app (TestMain raises it)
	t.Cleanup(func() { settingsH = old })
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	root := lockSnapRoot(t)
	// Rate calibration is global and learnt by earlier tests (dub renders):
	// every snapshot test starts from the uncalibrated predictor, so its
	// pictures do not depend on which tests ran before it.
	translate.DefaultPredictor.ResetCalibration()
	// The glyph atlases need no reset: every ui.Tester has a text system
	// of its own since MyGo 0.9.1, so a snapshot draws as in a fresh
	// process whatever ran before it.
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	local := time.Local
	time.Local = time.UTC
	defer func() { time.Local = local }()
	savedFree := diskFree
	diskFree = func(string) int64 { return snapDiskFree }
	defer func() { diskFree = savedFree }()
	cryptotest.SetGlobalRandom(t, 109)
	snap = &snapState{root: root, bubble: bubble}
	defer func() { snap = nil }()
	if !bubble {
		body(t, out)
		return
	}
	synctest.Test(t, func(t *testing.T) {
		time.Sleep(time.Until(snapEpoch)) // a bubble's clock starts in 2000
		body(t, out)
	})
}

// pinTester moves the clock of a Tester made in a snapshot test outside a
// bubble on to the phase of snapEpoch, where spinners stand as in one.
func pinTester(tt *ui.Tester) {
	if snap == nil || snap.bubble {
		return
	}
	const turn = 900 // ms: a spinner's turn (MyGo's Spinner)
	want, at := snapEpoch.UnixMilli()%turn, tt.Now().UnixMilli()%turn
	tt.Advance(time.Duration((want-at+turn)%turn) * time.Millisecond)
}

// lockSnapRoot empties and returns the directory under which the snapshot
// test makes its data directories, waiting for another snapshot run of the
// same user to finish with it.
func lockSnapRoot(t *testing.T) string {
	t.Helper()
	user := os.Getenv("USER")
	if user == "" {
		user = fmt.Sprint(os.Getuid())
	}
	root := filepath.Join(os.TempDir(), "audiosmith-snapshots-"+user)
	lock, err := os.OpenFile(root+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	lockSnapFile(t, lock, root)
	empty := func() {
		if err := os.RemoveAll(root); err != nil {
			t.Fatal(err)
		}
	}
	empty()
	t.Cleanup(func() {
		empty()
		lock.Close() // unlocks
	})
	return root
}

// testDir is t.TempDir, but in a snapshot test a directory of a fixed
// name: the next of <snapRoot>/001, 002, ….
func testDir(t *testing.T) string {
	t.Helper()
	if snap == nil {
		return t.TempDir()
	}
	snap.dirs++
	dir := filepath.Join(snap.root, fmt.Sprintf("%03d", snap.dirs))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}
