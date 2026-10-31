package main

// Review images of 0.5 H (each page's own header, the sidebar's search
// and bell) at 1100×720 in Chinese, and some at 820×520 @125 %. Not
// compared with anything:
//
//	AUDIOSMITH_SNAPSHOTS=/tmp/h go test -run TestSnapshotsH .

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

func TestSnapshotsH(t *testing.T) { runSnapshots(t, snapshotsH) }

func snapshotsH(t *testing.T, out string) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	for _, cfg := range []struct {
		name  string
		w, h  int
		scale float64
		all   bool // every page, else three
	}{
		{"1100-zh", 1100, 720, 1, true},
		{"min125-zh", 820, 520, 1.25, false},
	} {
		i18n.SetLocale("en")
		clear(headSearchOpen)
		d := newDubTest(t, 3)
		a, eng, tt := d.a, d.eng, d.tt
		a.updateSettings(func(s *settings.Settings) {
			s.General.Language = "zh-Hans"
			s.Appearance.Scale = cfg.scale
		})
		tt.SetSize(cfg.w, cfg.h)
		tt.SetScale(2)
		shoot := func(name string, fn func()) {
			fn()
			a.toasts = nil
			tt.Frame()
			tt.Frame()
			tt.Frame()
			f, err := os.Create(filepath.Join(out, fmt.Sprintf("h-%s-%s.png", name, cfg.name)))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := png.Encode(f, tt.Image()); err != nil {
				t.Fatal(err)
			}
		}
		// What the pages list: takes, dubs, transcripts, isolations.
		now := time.Now()
		for i, text := range []string{"欢迎收听本期节目，今天我们聊聊城市里的夜晚。", "The last ferry left the harbour at nine.", "请在周五之前提交报告。"} {
			addTake(t, a, core.Take{ID: fmt.Sprintf("T%d", i), CreatedAt: now.Add(-time.Duration(i) * time.Hour), Text: text, Model: kokoro, Voice: "af_heart", AudioSeconds: 3}, 0.3)
		}
		for _, name := range []string{"产品发布会.wav", "Interview.wav", "播客第 12 期.wav"} {
			src := filepath.Join(t.TempDir(), name)
			copyTestFile(t, d.src, src)
			a.dubStartFile(src)
		}
		tr := sampleTranscript("t1", now.Add(-3*time.Hour))
		tr.Title = "周会录音.m4a"
		putTranscript(t, a, tr)
		fakeOnDisk(t, a, isoSeparator())
		fakeOnDisk(t, a, isoDenoiser())
		a.isoEngine = isoFakeEngine(nil)
		eng.executors[services.KindIsolate] = a.isoSvc.Executor()
		src := filepath.Join(t.TempDir(), "采访原声.wav")
		copyTestFile(t, d.src, src)
		a.isoStart(src)

		shoot("03-projects", func() { a.navigate("/projects") })
		shoot("04-assets", func() { a.navigate("/assets") })
		shoot("07-dub-editor", func() { a.openDub(a.dubList[1].ID) })
		if !cfg.all {
			continue
		}
		shoot("01-home", func() { a.navigate("/home") })
		shoot("02-voices", func() { a.navigate("/voices/discover") })
		shoot("02b-voices-mine", func() { a.navigate("/voices/mine") })
		shoot("05-studio", func() { a.navigate("/studio") })
		shoot("06-dub-list", func() { a.navigate("/dub") })
		shoot("08-stt", func() { a.navigate("/transcripts") })
		shoot("09a-isolate-list", func() { a.navigate("/isolate") })
		shoot("09-isolate-result", func() { a.navigate("/isolate/" + a.isoList[0].ID) })
		shoot("10-settings", func() { a.navigate("/settings/general") })
		// The bell's popover, with a job running.
		job, _ := eng.host.sched.Submit(context.Background(), jobs.Spec{Kind: "x.batch", Lane: jobs.LaneCPU, Title: "批量导出", Owner: "project:p1"})
		eng.update(job, func(j *jobs.Job) { j.State = jobs.StateRunning; j.Progress.Fraction = 0.4 })
		shoot("11-bell", func() {
			a.navigate("/assets")
			a.noticesOpen = true
		})
		a.noticesOpen = false
		shoot("12-rail", func() { a.setRail(true) })
		a.setRail(false)
		eng.cancelled(job)
		i18n.SetLocale("en")
	}
}
