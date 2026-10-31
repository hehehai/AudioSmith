package main

// Review images of 0.5 F2 (Dub, Speech to text, Voice isolator: empty,
// with items, an item's page), at 1100×720 and 820×520 @125 %, in
// Chinese (and English at 1100). Not compared with anything:
//
//	AUDIOSMITH_SNAPSHOTS=/tmp/f2 go test -run TestSnapshotsF2 .

import (
	"context"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
)

func TestSnapshotsF2(t *testing.T) { runSnapshots(t, snapshotsF2) }

func snapshotsF2(t *testing.T, out string) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	for _, cfg := range []struct {
		name  string
		w, h  int
		scale float64
		lang  string
		dark  bool
	}{
		{"1100-zh", 1100, 720, 1, "zh-Hans", false},
		{"min125-zh", 820, 520, 1.25, "zh-Hans", false},
		{"1100-en", 1100, 720, 1, "en", false},
		{"1100-zh-dark", 1100, 720, 1, "zh-Hans", true},
	} {
		i18n.SetLocale("en")
		d := newDubTest(t, 3)
		a, eng, tt := d.a, d.eng, d.tt
		a.updateSettings(func(s *settings.Settings) {
			s.General.Language = cfg.lang
			s.Appearance.Scale = cfg.scale
			if cfg.dark {
				s.Appearance.Theme = "dark"
			}
		})
		tt.SetSize(cfg.w, cfg.h)
		tt.SetScale(2)
		shoot := func(name string, fn func()) {
			fn()
			a.toasts = nil
			tt.Frame()
			tt.Frame()
			tt.Frame()
			f, err := os.Create(filepath.Join(out, fmt.Sprintf("f2-%s-%s.png", name, cfg.name)))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := png.Encode(f, tt.Image()); err != nil {
				t.Fatal(err)
			}
		}

		// Dub.
		shoot("01-dub-empty", func() { a.navigate("/dub") })
		for _, name := range []string{"产品发布会.mp4", "Interview.wav", "播客第 12 期.m4a"} {
			src := filepath.Join(t.TempDir(), name)
			copyTestFile(t, d.src, src)
			a.dubStartFile(src)
		}
		// One more, running: its download at 37 %.
		a.dubURL = "https://www.youtube.com/watch?v=aaaaaaaaaaa"
		id := a.dubList[0].ID
		job, _ := eng.host.sched.Submit(context.Background(), jobs.Spec{Kind: services.KindDubTranscribe, Lane: jobs.LaneGPU, Owner: services.DubOwner + id})
		eng.update(job, func(j *jobs.Job) { j.State = jobs.StateRunning; j.Progress.Fraction = 0.37 })
		a.dubURL = ""
		shoot("02-dub-list", func() { a.navigate("/dub") })
		shoot("03-dub-composer-file", func() { a.dubPend = "/Users/me/Movies/发布会彩排.mov" })
		a.dubPend = ""
		eng.cancelled(job)
		shoot("04-dub-editor", func() { a.openDub(a.dubList[1].ID) })

		// Speech to text.
		shoot("05-stt-empty", func() { a.navigate("/transcripts") })
		now := time.Now()
		tr := sampleTranscript("t1", now.Add(-3*time.Hour))
		tr.Title = "周会录音.m4a"
		putTranscript(t, a, tr)
		putTranscript(t, a, core.Transcript{ID: "t2", CreatedAt: now.Add(-time.Hour), UpdatedAt: now, Source: core.SourceDictation, App: "Notes", Mode: "fast", Language: "zh",
			Duration: 2, Segments: []core.TranscriptSegment{{Start: 0, End: 2, Text: "记得周五之前把发布说明发给大家。"}}})
		a.trJob = &trJob{owner: "transcript:x", title: "客户访谈.mp3", stage: i18n.T("Transcribing…"), fraction: 0.4, started: now.Add(-20 * time.Second)}
		shoot("06-stt-list", func() { a.navigate("/transcripts") })
		a.trJob = nil
		shoot("07-stt-detail", func() { a.selectTranscript("t1") })

		// Voice isolator: the models missing, then three results.
		shoot("08-isolate-empty", func() { a.navigate("/isolate") })
		fakeOnDisk(t, a, isoSeparator())
		fakeOnDisk(t, a, isoDenoiser())
		a.isoEngine = isoFakeEngine(nil)
		eng.executors[services.KindIsolate] = a.isoSvc.Executor()
		for _, name := range []string{"现场版.mp3", "采访原声.mov", "嘈杂的会议.wav"} {
			src := filepath.Join(t.TempDir(), name)
			copyTestFile(t, d.src, src)
			a.isoStart(src)
		}
		shoot("09-isolate-list", func() { a.navigate("/isolate") })
		shoot("10-isolate-result", func() { a.navigate("/isolate/" + a.isoList[0].ID) })
		i18n.SetLocale("en")
	}
}

// copyTestFile copies a test file.
func copyTestFile(t *testing.T, src, dst string) {
	t.Helper()
	if err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}
}
