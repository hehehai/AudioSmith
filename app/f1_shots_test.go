package main

// Review images of 0.5 F1 (Home, the sidebar, the top bar, the bell,
// More, Voices, New voice, Developers, Settings' moved sections), in
// Chinese, at 1100×720 and 820×520 @125 %. Not compared with anything:
//
//	AUDIOSMITH_SNAPSHOTS=/tmp/f1 go test -run TestSnapshotsF1 .

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
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/store/historyjson"
)

func TestSnapshotsF1(t *testing.T) { runSnapshots(t, snapshotsF1) }

func snapshotsF1(t *testing.T, out string) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	for _, cfg := range []struct {
		name  string
		w, h  int
		scale float64
		lang  string
	}{
		{"1100-zh", 1100, 720, 1, "zh-Hans"},
		{"min125-zh", 820, 520, 1.25, "zh-Hans"},
		{"1100-en", 1100, 720, 1, "en"},
	} {
		i18n.SetLocale("en")
		a, eng, tt := newTestApp(t)
		a.updateSettings(func(s *settings.Settings) {
			s.General.Language = cfg.lang
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
			f, err := os.Create(filepath.Join(out, fmt.Sprintf("f1-%s-%s.png", name, cfg.name)))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := png.Encode(f, tt.Image()); err != nil {
				t.Fatal(err)
			}
		}
		shoot("01-home-empty", func() { a.navigate("/home") })
		if cfg.name == "1100-en" {
			shoot("02-home-samples", func() { a.homeUI.tab = 1 })
			a.homeUI.tab = 0
		}
		// Some work, for Recent.
		for i, text := range []string{"欢迎来到春季发布会。", "今天我们发布 AudioSmith 0.5。", "第一章：夜航渡轮"} {
			at := time.Date(2026, 10, 31, 9, 10+i, 0, 0, time.Local)
			addTake(t, a, core.Take{ID: historyjson.NewTakeID(at), CreatedAt: at, Text: text, Model: kokoro, Voice: "zf_xiaobei",
				Speed: 1, SampleRate: 24000, AudioSeconds: 4, GenSeconds: 1}, 1)
		}
		a.trList = append(a.trList, core.Transcript{ID: "tr1", Title: "周会录音.m4a", CreatedAt: time.Date(2026, 10, 31, 9, 30, 0, 0, time.Local), Duration: 1260})
		a.homeUI.readAt = time.Time{}
		shoot("03-home-recent", func() { a.navigate("/home"); a.homeReadRecent() })
		shoot("04-home-typed", func() { a.homeUI.text = "给新款耳机写一段 30 秒的产品旁白，语气轻松。" })
		a.homeUI.text = ""
		shoot("05-more-menu", func() { a.moreOpen = true })
		a.moreOpen = false

		// The bell, with work going on.
		other, _ := eng.host.sched.Submit(context.Background(), jobs.Spec{Kind: "x.batch", Lane: jobs.LaneCPU, Title: i18n.T("Export %s", "夜航渡轮.m4b"), Owner: "project:p1"})
		eng.update(other, func(j *jobs.Job) { j.State = jobs.StateRunning; j.Progress.Fraction = 0.42 })
		a.notify(core.SeverityInfo, i18n.T("Speech generated"), "欢迎来到春季发布会。", "/studio")
		shoot("06-bell", func() { a.noticesOpen = true })
		a.noticesOpen = false
		eng.cancelled(other)

		shoot("07-studio", func() { a.navigate("/studio") })
		shoot("08-voices-discover", func() { a.navigate("/voices/discover") })
		shoot("09-voices-mine", func() { a.navigate("/voices/mine") })
		shoot("10-voices-new", func() { a.navigate("/voices/new") })
		shoot("11-dictation", func() { a.navigate("/dictation") })
		shoot("12-isolate", func() { a.navigate("/isolate") })
		shoot("13-developers", func() { a.navigate("/developers") })
		shoot("14-developers-tools", func() { a.devTab = 2 })
		a.devTab = 0
		shoot("15-settings-engine", func() { a.navigate("/settings/engine") })
		shoot("16-settings-logs", func() { a.navigate("/settings/logs") })
		a.settingsOpen = false
		shoot("17-rail", func() { a.setRail(true); a.navigate("/home") })
		a.setRail(false)
		i18n.SetLocale("en")
	}
}
