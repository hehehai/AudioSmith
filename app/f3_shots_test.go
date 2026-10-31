package main

// Review images of 0.5 F3 (Text to speech: the script on the left, the
// panel 设置 | 历史 on the right), in Chinese and English, at 1100×720 and
// 820×520 @125 %. Not compared with anything:
//
//	AUDIOSMITH_SNAPSHOTS=/tmp/f3 go test -run TestSnapshotsF3 ./app

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/store/historyjson"
)

func TestSnapshotsF3(t *testing.T) { runSnapshots(t, snapshotsF3) }

func snapshotsF3(t *testing.T, out string) {
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
		a, _, tt := newTestApp(t)
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
			f, err := os.Create(filepath.Join(out, fmt.Sprintf("f3-%s-%s.png", name, cfg.name)))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := png.Encode(f, tt.Image()); err != nil {
				t.Fatal(err)
			}
		}
		a.navigate("/studio")
		voice := "af_heart"
		if cfg.lang == "zh-Hans" {
			// A Chinese model and speaker, as a Chinese user has.
			if e, ok := catalog.Builtin().ByID("qwen3-tts-0.6b-customvoice-8bit"); ok {
				a.selectModel(e)
				voice = "Serena"
			}
		}
		a.text, a.voice = "", voice
		shoot("01-empty", func() {})
		if cfg.dark {
			continue
		}
		// Takes, for the latest and 历史.
		for i, text := range []string{"欢迎来到春季发布会。", "今天我们发布 AudioSmith 0.5，带来全新的创作流程。", "第一章：夜航渡轮。雾从江面上漫过来的时候，渡轮已经离岸很远了。"} {
			at := time.Now().Add(time.Duration(i-3) * 7 * time.Minute)
			tk := core.Take{ID: historyjson.NewTakeID(at), CreatedAt: at, Text: text, Model: a.preset().Repo, Voice: voice,
				Speed: 1, SampleRate: 24000, AudioSeconds: 4 + float64(i), GenSeconds: 1}
			addTake(t, a, tk, 1)
			a.lastClipID = tk.ID
		}
		tt.Frame()
		shoot("02-text", func() { a.fillExample(studioExamples()[1]) })
		shoot("03-history", func() { a.showStudioPanel(studioTabHistory) })
		shoot("04-recommend", func() { a.openRecommend() })
		a.voicePickerOpen(studioVoiceKey, false)
		shoot("05-revoice", func() { a.revoice(nil) })
		a.voicePickerOpen(studioVoiceKey, false)
		a.ttsUI.regen = false
		a.composer.panelSet = false
		if cfg.name == "min125-zh" {
			shoot("06-narrow-drawer", func() { a.showStudioPanel(studioTabSettings) })
			a.composer.panelSet = false
		}
		i18n.SetLocale("en")
	}
}
