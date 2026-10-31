package main

// Review images of the floating panels (#126): each page with its panel
// open, Chinese at 1100×720, and Mine and Studio dark and at 820×520
// @125 %. Not compared with anything:
//
//	AUDIOSMITH_SNAPSHOTS=/tmp/c126 go test -run TestSnapshotsFloating ./app

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/settings"
)

func TestSnapshotsFloating(t *testing.T) { runSnapshots(t, snapshotsFloating) }

// floatCfg is a picture's window: size, scale, appearance.
type floatCfg struct {
	name  string
	w, h  int
	scale float64
	dark  bool
}

var floatLight = floatCfg{"1100-zh", 1100, 720, 1, false}

// floatSetup sizes a's window as cfg says, in Chinese, and returns what
// saves a picture of it.
func floatSetup(t *testing.T, a *app, tt *ui.Tester, out string, cfg floatCfg) func(name string) {
	a.updateSettings(func(s *settings.Settings) {
		s.General.Language = "zh-Hans"
		s.Appearance.Scale = cfg.scale
		if cfg.dark {
			s.Appearance.Theme = "dark"
		}
	})
	tt.SetSize(cfg.w, cfg.h)
	tt.SetScale(2)
	return func(name string) {
		a.toasts = nil
		tt.Frame()
		tt.Frame()
		tt.Advance(time.Second)
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
}

func snapshotsFloating(t *testing.T, out string) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	for _, cfg := range []floatCfg{floatLight, {"1100-zh-dark", 1100, 720, 1, true}, {"min125-zh", 820, 520, 1.25, false}} {
		i18n.SetLocale("en")
		a, _, tt := newTestApp(t)
		shoot := floatSetup(t, a, tt, out, cfg)
		v := addVoice(t, a, core.Voice{Name: "旁白", Kind: core.VoiceCloned, RefText: "从前有座山。", Language: "zh"}, 6)
		addVoice(t, a, core.Voice{Name: "外婆", Kind: core.VoiceDesigned, Design: "一位慈祥的老奶奶", Language: "zh", Seed: 7}, 0)
		addVoice(t, a, core.Voice{Name: "主播", Kind: core.VoiceDesigned, Design: "沉稳的新闻主播", Language: "zh", Seed: 3}, 0)
		a.navigate("/voices/mine")
		tt.Frame()
		a.openVoiceEditor(v.ID)
		shoot("01-mine")
		a.edit = nil
		a.navigate("/studio")
		a.showStudioPanel(studioTabSettings) // narrow: over the script
		shoot("04-studio")
		if cfg.name != floatLight.name {
			continue
		}
		a.navigate("/voices/discover")
		a.openArchetype("c-serena")
		shoot("02-discover")
		for i, text := range []string{"欢迎来到春季发布会。", "今天我们发布 AudioSmith 0.5。", "第一章：夜航渡轮。"} {
			at := time.Now().Add(time.Duration(i-3) * 7 * time.Minute)
			addTake(t, a, core.Take{ID: fmt.Sprintf("T%d", i), CreatedAt: at, Text: text, Model: kokoro, Voice: "af_heart", SampleRate: 24000, AudioSeconds: 4}, 1)
		}
		a.navigate("/assets")
		a.history.selected = "T1"
		shoot("03-assets")
		a.navigate("/stories")
		a.loadSampleStory(a.lfStories)
		shoot("06-stories")
		a.navigate("/audiobook")
		a.loadSample(a.lfBook)
		shoot("07-audiobook")
	}
	// Workflows and Dub need their own fakes.
	i18n.SetLocale("en")
	w := newWfTest(t)
	shoot := floatSetup(t, w.a, w.tt, out, floatLight)
	w.a.navigate("/workflows")
	w.a.wfNew("narration-normalize")
	w.a.wfSel = w.a.wf.InputNode().ID
	shoot("05-workflows")
	i18n.SetLocale("en")
	d := newDubTest(t, 3)
	shoot = floatSetup(t, d.a, d.tt, out, floatLight)
	src := filepath.Join(t.TempDir(), "发布会.wav")
	copyTestFile(t, d.src, src)
	d.a.dubStartFile(src)
	d.tt.Frame()
	d.a.openDub(d.a.dubList[0].ID)
	shoot("08-dub")
}

// TestSnapshotsFloatingSlide saves frames of Mine's panel sliding in and
// out, 60 ms apart (the contact sheet of #126).
func TestSnapshotsFloatingSlide(t *testing.T) { runSnapshots(t, snapshotsFloatingSlide) }

func snapshotsFloatingSlide(t *testing.T, out string) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	i18n.SetLocale("en")
	a, _, tt := newTestApp(t)
	a.testMotion = true
	floatSetup(t, a, tt, out, floatLight)
	v := addVoice(t, a, core.Voice{Name: "旁白", Kind: core.VoiceCloned, RefText: "从前有座山。", Language: "zh"}, 6)
	addVoice(t, a, core.Voice{Name: "外婆", Kind: core.VoiceDesigned, Design: "一位慈祥的老奶奶", Language: "zh", Seed: 7}, 0)
	a.navigate("/voices/mine")
	tt.Frame()
	tt.Advance(time.Second)
	save := func(name string) {
		f, err := os.Create(filepath.Join(out, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
	a.openVoiceEditor(v.ID)
	tt.Frame()
	for i := range 5 {
		save(fmt.Sprintf("slide-in-%03dms", i*60))
		tt.Advance(60 * time.Millisecond)
	}
	tt.Advance(time.Second)
	a.dismissPanel("voice-editor")()
	tt.Frame()
	for i := range 4 {
		save(fmt.Sprintf("slide-out-%03dms", i*60))
		tt.Advance(60 * time.Millisecond)
	}
}
