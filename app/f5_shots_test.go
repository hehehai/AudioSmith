package main

// Review images of 0.5 F5 (Projects, Assets, ⌘K, the Stories and
// Audiobook pages' way in) at 1100×720 in Chinese, a few at 820×520
// @125 % and in the dark theme. Not compared with anything:
//
//	AUDIOSMITH_SNAPSHOTS=/tmp/f5 go test -run TestSnapshotsF5 .

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
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
)

func TestSnapshotsF5(t *testing.T) { runSnapshots(t, snapshotsF5) }

func snapshotsF5(t *testing.T, out string) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	for _, cfg := range []struct {
		name  string
		w, h  int
		scale float64
		theme string
		all   bool
	}{
		{"1100-zh", 1100, 720, 1, "light", true},
		{"min125-zh", 820, 520, 1.25, "light", false},
		{"1100-zh-dark", 1100, 720, 1, "dark", false},
	} {
		i18n.SetLocale("en")
		clear(headSearchOpen)
		d := newDubTest(t, 3)
		a, eng, tt := d.a, d.eng, d.tt
		a.updateSettings(func(s *settings.Settings) {
			s.General.Language = "zh-Hans"
			s.Appearance.Scale = cfg.scale
			s.Appearance.Theme = cfg.theme
		})
		tt.SetSize(cfg.w, cfg.h)
		tt.SetScale(2)
		shoot := func(name string, fn func()) {
			fn()
			a.toasts = nil
			tt.Frame()
			tt.Frame()
			tt.Frame()
			f, err := os.Create(filepath.Join(out, fmt.Sprintf("f5-%s-%s.png", name, cfg.name)))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := png.Encode(f, tt.Image()); err != nil {
				t.Fatal(err)
			}
		}
		if cfg.all {
			shoot("01-projects-empty", func() { a.navigate("/projects") })
			shoot("06-stories-entry-empty", func() { a.navigate("/stories") })
		}
		// What the pages list: takes, stories and books, dubs, a
		// transcript, an isolation.
		now := time.Now()
		for i, text := range []string{"欢迎收听本期节目，今天我们聊聊城市里的夜晚。", "最后一班渡轮在十一点离港。", "请在周五之前提交报告。"} {
			addTake(t, a, core.Take{ID: fmt.Sprintf("T%d", i), CreatedAt: now.Add(-time.Duration(i) * time.Hour), Text: text, Model: kokoro, Voice: "af_heart", AudioSeconds: 3 + float64(i)}, 0.3)
		}
		ctx := context.Background()
		for _, pr := range []struct {
			kind  core.ProjectKind
			title string
			lines int
		}{{core.ProjectStory, "夜航渡轮", 9}, {core.ProjectStory, "两个人的晚餐", 14}, {core.ProjectAudiobook, "城南旧事 · 第一章", 0}} {
			doc := services.NewLongformDoc(pr.kind, kokoro)
			doc.Title = pr.title
			for i := range pr.lines {
				doc.Lines = append(doc.Lines, services.StoryLine{ID: fmt.Sprint("l", i), Text: "台词"})
			}
			if pr.kind == core.ProjectAudiobook {
				doc.Script = "第一章\n\n" + "雾从江面上漫过来的时候，渡轮已经离岸很远了。"
			}
			a.lib.svc.Library.SaveDoc(ctx, core.Project{Kind: pr.kind, Title: pr.title}, doc)
		}
		for _, name := range []string{"产品发布会.wav", "Interview.wav"} {
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
		a.projects.dirty = true

		shoot("02-projects-list", func() { a.navigate("/projects") })
		shoot("06-stories-entry", func() { a.navigate("/stories") })
		if !cfg.all {
			shoot("04-assets", func() { a.navigate("/assets") })
			continue
		}
		shoot("03-projects-grid", func() { a.projects.cards = 1 })
		a.projects.cards = 0
		shoot("04-assets", func() { a.navigate("/assets") })
		shoot("05a-cmdk-empty", func() {
			a.navigate("/home")
			a.openPalette()
		})
		shoot("05b-cmdk-typed", func() { a.paletteQuery = "渡轮" })
		a.paletteOpen = false
		shoot("07-audiobook-entry", func() { a.navigate("/audiobook") })
		shoot("08-story-editor", func() {
			a.navigate("/stories")
			a.useTemplate("story")
		})
		i18n.SetLocale("en")
	}
}
