package main

// Regression tests of the perf review of 11-02 (C1, H2, H3, M1): the long
// lists build only the rows in view, and a frame of each page stays far
// under what it cost before, with a library of hundreds of items. The
// thresholds leave wide margins (the Linux host is shared, its load ~20);
// the counts of rows built are exact.

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/store"
)

var perfLines = []string{"他也说过，这个城市里有很多米粉店，西边那家最好吃。", "每天早上都排着长队，大家吃完了才去上班。",
	"Hello there, this is a test line for the review. ", "西门外面的那条街上现在全是卖早点的。"}

// perfText is a script of about chars characters.
func perfText(chars int) string {
	var b strings.Builder
	for i := 0; b.Len() < chars*2; i++ {
		b.WriteString(perfLines[i%len(perfLines)])
	}
	return b.String()
}

// perfLibrary fills a's lists in memory as a big library would: takes
// (100–3 000 characters), stories and books with summaries, transcripts
// of an hour (600 segments), dubs, isolations and saved voices.
type perfLibrary struct{ takes, projects, transcripts, dubs, isolations, voices int }

func (p perfLibrary) fill(a *app) {
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.Local)
	for i := range p.takes {
		a.takes = append([]core.Take{{ID: fmt.Sprintf("t%05d", i), CreatedAt: at.Add(time.Duration(i) * time.Minute), Text: perfText(100 + (i%10)*300),
			Model: kokoro, Voice: "af_heart", Speed: 1, SampleRate: 24000, AudioSeconds: 3, File: "x.wav"}}, a.takes...)
	}
	for i := range p.projects {
		cat := store.LibStory
		if i%2 == 1 {
			cat = store.LibAudiobook
		}
		a.projects.lf = append(a.projects.lf, store.LibraryItem{Source: "project", ID: fmt.Sprintf("p%04d", i), Category: cat,
			Title: fmt.Sprintf("Project %d", i), Detail: `{"chapters":12,"lines":340,"seconds":3600}`, Updated: at.Add(time.Duration(i) * time.Minute)})
	}
	a.projects.loaded, a.projects.dirty = true, false
	for i := range p.transcripts {
		ts := at.Add(time.Duration(i) * time.Hour)
		tr := core.Transcript{ID: fmt.Sprintf("tr%04d", i), CreatedAt: ts, UpdatedAt: ts, Title: fmt.Sprintf("meeting-%d.m4a", i), Duration: 3600}
		for j := range 600 {
			tr.Segments = append(tr.Segments, core.TranscriptSegment{Start: float64(j) * 6, End: float64(j)*6 + 5, Text: perfLines[j%len(perfLines)]})
		}
		a.trList = append(a.trList, tr)
	}
	for i := range p.dubs {
		ts := at.Add(time.Duration(i) * time.Hour)
		a.dubList = append(a.dubList, store.DubRecord{ID: fmt.Sprintf("d%04d", i), CreatedAt: ts, UpdatedAt: ts, Title: fmt.Sprintf("video %d.mp4", i),
			Source: "/x/v.mp4", Languages: []string{"zh", "en"}, Phase: "done", Duration: 600})
	}
	for i := range p.isolations {
		ts := at.Add(time.Duration(i) * time.Hour)
		a.isoList = append(a.isoList, services.Isolation{ID: fmt.Sprintf("i%04d", i), CreatedAt: ts, UpdatedAt: ts, Title: fmt.Sprintf("iso %d", i),
			Input: "/x/a.wav", State: "done", Duration: 300})
	}
	for i := range p.voices {
		a.voices = append(a.voices, core.Voice{ID: fmt.Sprintf("v%04d", i), Name: fmt.Sprintf("Voice %03d", i), Kind: core.VoiceCloned, CreatedAt: at, UpdatedAt: at, Language: "zh"})
	}
	if p.voices > 0 {
		a.voicesLoaded = true
	}
}

// perfFrame is the best of three batches of frames: ms a frame, and the
// bytes allocated a frame.
func perfFrame(tt *ui.Tester) (ms float64, bytes uint64) {
	for range 3 {
		tt.Frame()
	}
	ms = 1e9
	for range 3 {
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		t0 := time.Now()
		const n = 6
		for range n {
			tt.Frame()
		}
		el := float64(time.Since(t0).Microseconds()) / 1000 / n
		runtime.ReadMemStats(&m1)
		if el < ms {
			ms, bytes = el, (m1.TotalAlloc-m0.TotalAlloc)/n
		}
	}
	return ms, bytes
}

// countTexts is how many texts shown start with prefix.
func countTexts(tt *ui.Tester, prefix string) int {
	n := 0
	for _, s := range tt.Texts() {
		if strings.HasPrefix(s, prefix) {
			n++
		}
	}
	return n
}

// perfApp is a new app (each page alone: the router keeps pages seen)
// with lib, at route.
func perfApp(t *testing.T, lib perfLibrary, route string) (*app, *ui.Tester) {
	t.Helper()
	i18n.SetLocale("en")
	a, _, tt := newTestApp(t)
	tt.SetScale(2)
	lib.fill(a)
	a.navigate(route)
	tt.Frame()
	return a, tt
}

func TestPerfTranscriptsPage(t *testing.T) {
	if testing.Short() {
		t.Skip("perf")
	}
	// 20 transcripts of an hour: 241 ms and 423 MB a frame before (C1).
	_, tt := perfApp(t, perfLibrary{transcripts: 20}, "/transcripts")
	if ms, b := perfFrame(tt); ms > 15 || b > 8<<20 {
		t.Errorf("20 hour-long transcripts: %.2f ms and %d KB a frame, want < 15 ms and 8 MB (241 ms, 423 MB before)", ms, b>>10)
	}
	// 200: the rows in view only.
	_, tt = perfApp(t, perfLibrary{transcripts: 200}, "/transcripts")
	if n := countTexts(tt, "meeting-"); n == 0 || n > 100 {
		t.Errorf("200 transcripts: %d rows built, want the ones in view (1–100)", n)
	}
	if ms, _ := perfFrame(tt); ms > 15 {
		t.Errorf("200 hour-long transcripts: %.2f ms a frame, want < 15 ms (3.3 s before)", ms)
	}
}

func TestPerfProjectsPage(t *testing.T) {
	if testing.Short() {
		t.Skip("perf")
	}
	lib := perfLibrary{projects: 500, transcripts: 20, dubs: 300, isolations: 100}
	a, tt := perfApp(t, lib, "/projects")
	if n := countTexts(tt, "Project "); n == 0 || n > 100 {
		t.Errorf("list: %d project rows built, want the ones in view (1–100)", n)
	}
	if ms, _ := perfFrame(tt); ms > 20 {
		t.Errorf("list of ~920 items: %.2f ms a frame, want < 20 ms (28–43 ms before)", ms)
	}
	a, tt = perfApp(t, lib, "/projects")
	a.projects.cards = 1
	tt.Frame()
	if n := countTexts(tt, "Project "); n == 0 || n > 150 {
		t.Errorf("cards: %d project cards built, want the rows in view (1–150)", n)
	}
	if ms, _ := perfFrame(tt); ms > 25 {
		t.Errorf("cards of ~920 items: %.2f ms a frame, want < 25 ms (269 ms before)", ms)
	}
	a.projects.query = "zzz" // no hit: every transcript's text searched, once
	tt.Frame()
	if ms, _ := perfFrame(tt); ms > 15 {
		t.Errorf("a search without hits: %.2f ms a frame, want < 15 ms", ms)
	}
}

func TestPerfAssetsPage(t *testing.T) {
	if testing.Short() {
		t.Skip("perf")
	}
	// 2 000 takes (1 450 characters on average): 17–23 ms and 25 MB a
	// frame before (H3).
	_, tt := perfApp(t, perfLibrary{takes: 2000, transcripts: 20, dubs: 300, isolations: 100}, "/assets")
	if ms, b := perfFrame(tt); ms > 12 || b > 6<<20 {
		t.Errorf("2 000 takes: %.2f ms and %d KB a frame, want < 12 ms and 6 MB (17–23 ms, 25 MB before)", ms, b>>10)
	}
}

func TestPerfPaletteTyped(t *testing.T) {
	if testing.Short() {
		t.Skip("perf")
	}
	// A query nothing has: every take's script was lowered each frame
	// (M1: 21 ms and 6.6 MB a frame for 2 000 takes).
	a, tt := perfApp(t, perfLibrary{takes: 2000, projects: 100, transcripts: 20}, "/home")
	a.paletteOpen, a.paletteQuery = true, "qqq"
	tt.Frame()
	if ms, b := perfFrame(tt); ms > 10 || b > 2<<20 {
		t.Errorf("⌘K, no hit: %.2f ms and %d KB a frame, want < 10 ms and 2 MB (21 ms, 6.6 MB before)", ms, b>>10)
	}
}

func TestPerfToolLists(t *testing.T) {
	if testing.Short() {
		t.Skip("perf")
	}
	for _, c := range []struct {
		route, prefix string
		lib           perfLibrary
		before        string
	}{
		{"/dub", "video ", perfLibrary{dubs: 300}, "20–32 ms"},
		{"/isolate", "iso ", perfLibrary{isolations: 300}, "~3 ms"},
		{"/voices/mine", "Voice ", perfLibrary{voices: 300}, "6.9 ms"},
	} {
		_, tt := perfApp(t, c.lib, c.route)
		if n := countTexts(tt, c.prefix); n == 0 || n > 100 {
			t.Errorf("%s: %d rows built of 300, want the ones in view (1–100)", c.route, n)
		}
		if ms, _ := perfFrame(tt); ms > 10 {
			t.Errorf("%s with 300 items: %.2f ms a frame, want < 10 ms (%s before)", c.route, ms, c.before)
		}
	}
}
