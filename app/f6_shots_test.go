package main

// Review images of 0.5 F6 (samples, 「帮我写」, the introductions,
// Developers) at 1100×720 in Chinese, and at 820×520 @125 % and in the
// dark theme. Not compared with anything:
//
//	AUDIOSMITH_SNAPSHOTS=/tmp/f6 go test -run TestSnapshotsF6 .
//
// With AUDIOSMITH_SWEEP set, every page is drawn too at both sizes and
// themes (the check for clipping), as sweep-*.png.

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/llm"
	"github.com/hehehai/AudioSmith/internal/settings"
)

func TestSnapshotsF6(t *testing.T) { runSnapshots(t, snapshotsF6) }

func snapshotsF6(t *testing.T, out string) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	for _, cfg := range []struct {
		name  string
		w, h  int
		scale float64
		theme string
	}{
		{"1100-zh", 1100, 720, 1, "light"},
		{"min125-zh", 820, 520, 1.25, "light"},
		{"1100-zh-dark", 1100, 720, 1, "dark"},
		{"min125-zh-dark", 820, 520, 1.25, "dark"},
	} {
		i18n.SetLocale("en")
		clear(headSearchOpen)
		a, _, tt := newTestApp(t)
		a.updateSettings(func(s *settings.Settings) {
			s.General.Language = "zh-Hans"
			s.Appearance.Scale = cfg.scale
			s.Appearance.Theme = cfg.theme
		})
		e, _ := catalog.Builtin().ByID(catalog.DefaultLLM)
		a.setLocal(e, hub.Local{State: hub.Downloaded, Snapshot: t.TempDir()}, nil)
		useFakeLLM(a, &fakeLLM{local: true, answer: func(llm.Request) (string, error) { return "", nil }})
		tt.SetSize(cfg.w, cfg.h)
		tt.SetScale(2)
		shoot := func(name string, fn func()) {
			fn()
			a.toasts = nil
			tt.Frame()
			tt.Frame()
			tt.Frame()
			f, err := os.Create(filepath.Join(out, fmt.Sprintf("%s-%s.png", name, cfg.name)))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := png.Encode(f, tt.Image()); err != nil {
				t.Fatal(err)
			}
		}
		shoot("f6-01-home-samples", func() {
			a.navigate("/home")
			a.homeUI.tab = 1
		})
		shoot("f6-02-projects-samples", func() { a.navigate("/projects") })
		a.openSample("story") // a copy, then: open it again?
		a.navigate("/projects")
		shoot("f6-03-sample-ask", func() { a.openSample("story") })
		a.sampleAskOpen = false
		a.lfClose(a.lfStories)
		p := a.lfStories
		p.helpWrite, p.entryText = true, "灯塔里两个守塔人的夜晚，风暴快来了"
		shoot("f6-04-stories-help-write", func() { a.navigate("/stories") })
		shoot("f6-05-stories-writing", func() { p.writing = true; p.writeStop = func() {} })
		p.writing = false
		a.noIntros = false
		shoot("f6-06-intro-dub", func() { a.navigate("/dub") })
		a.introOpen = false
		a.noIntros = true
		shoot("f6-07-developers", func() { a.navigate("/developers") })
		shoot("f6-08-developers-curl", func() { a.devSeg = devCurl })
		a.devSeg = 0
		shoot("f6-09-settings-integrations", func() { a.navigate("/settings/integrations") })
		a.settingsOpen = false
		if os.Getenv("AUDIOSMITH_SWEEP") == "" {
			i18n.SetLocale("en")
			continue
		}
		for _, path := range []string{"/home", "/voices/discover", "/voices/mine", "/voices/new", "/projects", "/assets", "/studio", "/stories",
			"/audiobook", "/dub", "/transcripts", "/isolate", "/dictation", "/developers", "/developers/api", "/developers/tools", "/workflows", "/batch", "/calls", "/tasks"} {
			shoot("sweep-"+strings.ReplaceAll(strings.Trim(path, "/"), "/", "-"), func() { a.navigate(path) })
		}
		i18n.SetLocale("en")
	}
}
