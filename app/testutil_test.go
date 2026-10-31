package main

// Test helpers shared by the view tests: a test app over a temporary
// library (SQLite store + services) and fake engines (enginefake_test.go),
// and a WAV writer. Thread A owns this file; B and C add their fakes in files of
// their own.

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// inline runs background work at once, as the app does in tests.
func inline(fn func()) { fn() }

// openTestLibrary opens a library with the real job scheduler in a
// temporary directory (dir, when given), closed when the test ends.
func openTestLibrary(t *testing.T, dir ...string) *library {
	t.Helper()
	d := testDir(t)
	if len(dir) > 0 {
		d = dir[0]
	}
	return openTestLibraryWith(t, d, libraryOptions{Async: inline})
}

func openTestLibraryWith(t *testing.T, dir string, o libraryOptions) *library {
	t.Helper()
	lib, err := openLibrary(dir, filepath.Join(dir, "cache"), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lib.close)
	return lib
}

// newTestApp is the app over a new library with fake engines: a
// scheduler that records submissions and a supervisor that records calls
// (enginefake_test.go); the test plays the engine.
func newTestApp(t *testing.T) (*app, *fakeEngine, *ui.Tester) {
	t.Helper()
	return newTestAppIn(t, testDir(t))
}

// newTestAppIn is newTestApp over the library in dir, which may hold one
// from an earlier "run".
func newTestAppIn(t *testing.T, dir string) (*app, *fakeEngine, *ui.Tester) {
	t.Helper()
	eng := newFakeEngine()
	lib := openTestLibraryWith(t, dir, libraryOptions{Sched: eng.host.sched, Async: inline})
	useLocalModels(t, lib)
	a := newApp(eng.host, lib)
	// The app opens at Home (0.5 F1); the tests written before it start
	// in the Studio, as the app did (TestHomeStart checks Home).
	a.router.Replace("/studio")
	eng.a = a
	a.connectEngine(eng.host)
	a.autoplay = false
	a.noIntros = true // TestIntroDialogs turns them on
	a.startEngine()
	eng.ready()
	t.Cleanup(a.pl.Stop)
	tt := ui.NewTester(a.view, 1100, 720)
	pinTester(tt)
	return a, eng, tt
}

// useLocalModels points the library's model folder at a temporary one in
// which every catalogue entry (and its auxiliary repos) is "downloaded":
// a snapshot at its pinned commit with a config and a weights file.
func useLocalModels(t *testing.T, lib *library) string {
	t.Helper()
	dir := testDir(t)
	put := func(repo, rev string) {
		snap := filepath.Join(hub.RepoDir(dir, repo), "snapshots", rev)
		for name, data := range map[string]string{"config.json": "{}", "model.safetensors": "weights"} {
			if err := os.MkdirAll(snap, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(snap, name), []byte(data), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, e := range catalog.Builtin().Entries {
		put(e.Repo, e.Revision)
		for _, x := range e.Aux {
			put(x.Repo, x.Revision)
		}
	}
	if err := lib.settings.Update(context.Background(), func(s *settings.Settings) { s.Models.CacheDir = dir }); err != nil {
		t.Fatal(err)
	}
	lib.resetHub()
	return dir
}

// addTake stores a take with a test WAV, as a finished generation would;
// the snapshot follows from the TakeCreated event.
func addTake(t *testing.T, a *app, take core.Take, seconds float64) {
	t.Helper()
	take.File = take.ID + ".wav"
	writeTestWAV(t, a.takeStore.AudioPath(take), 24000, seconds)
	if _, err := a.lib.svc.Takes.Add(context.Background(), take); err != nil {
		t.Fatal(err)
	}
}

// writeTestWAV writes a mono 16-bit sine, as the engine would write a take.
func writeTestWAV(t *testing.T, path string, rate int, seconds float64) {
	t.Helper()
	n := int(float64(rate) * seconds)
	data := make([]byte, 44+2*n)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(36+2*n))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], uint32(rate))
	binary.LittleEndian.PutUint32(data[28:], uint32(rate*2))
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(2*n))
	for i := range n {
		// A tone that swells, so the waveform has a shape.
		v := math.Sin(2*math.Pi*220*float64(i)/float64(rate)) * float64(i) / float64(n)
		binary.LittleEndian.PutUint16(data[44+2*i:], uint16(int16(v*20000)))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// tallSettings makes the window and Settings tall enough for a whole
// section to show without scrolling (tests that click far down one).
func tallSettings(tt *ui.Tester) { tt.SetSize(1100, 3000) }

// openModels opens Settings → Models & network over the logs page,
// whose texts are none of the models' (the Studio's model picker is).
func openModels(a *app) {
	a.navigate("/assets") // not the Studio, whose model picker names models too
	a.navigate("/models")
}
