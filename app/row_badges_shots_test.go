package main

// Review images of thread #129 (row buttons with a hover face, clickable
// meta badges): Discover with the pointer on a row's ▶ and on a badge,
// Mine, Projects and Assets at 1100×720 in Chinese, Discover in the dark
// and at 820×520 @125 %. Not compared with anything:
//
//	AUDIOSMITH_SNAPSHOTS=/tmp/rb go test -run TestSnapshotsRowBadges ./app

import (
	"context"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
)

func TestSnapshotsRowBadges(t *testing.T) { runSnapshots(t, snapshotsRowBadges) }

// rbShooter writes tt's picture as rb-<name>-<cfg>.png after fn.
func rbShooter(t *testing.T, a *app, tt *ui.Tester, out, cfg string) func(string, func()) {
	return func(name string, fn func()) {
		fn()
		a.toasts = nil
		tt.Frame()
		tt.Frame()
		tt.Frame()
		f, err := os.Create(filepath.Join(out, fmt.Sprintf("rb-%s-%s.png", name, cfg)))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
}

// rbHover moves the pointer to the middle of what Find finds by s, or
// off the window when nothing.
func rbHover(tt *ui.Tester, s string) {
	if r, ok := tt.Find(s); ok {
		tt.Move(r.X+r.W/2, r.Y+r.H/2)
		return
	}
	tt.Move(1, 1)
}

func snapshotsRowBadges(t *testing.T, out string) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	for _, cfg := range []struct {
		name  string
		w, h  int
		scale float64
		dark  bool
		all   bool
		lang  string
	}{
		{"1100-zh", 1100, 720, 1, false, true, "zh-Hans"},
		{"1100-zh-dark", 1100, 720, 1, true, false, "zh-Hans"},
		{"min125-zh", 820, 520, 1.25, false, false, "zh-Hans"},
		{"1100-en", 1100, 720, 1, false, false, "en"},
	} {
		i18n.SetLocale("en")
		clear(headSearchOpen)
		d := newDubTest(t, 3)
		a, eng, tt := d.a, d.eng, d.tt
		acceptLicences(t, a)
		a.updateSettings(func(s *settings.Settings) {
			s.General.Language = cfg.lang
			s.Appearance.Scale = cfg.scale
			if cfg.dark {
				s.Appearance.Theme = "dark"
			}
		})
		tt.SetSize(cfg.w, cfg.h)
		tt.SetScale(2)
		shoot := rbShooter(t, a, tt, out, cfg.name)
		loc := i18n.Locale()

		// Discover: the pointer on the first table row's ▶ (left of its
		// 「使用」), then on its gender·age badge.
		a.navigate("/voices/discover")
		tt.Frame()
		shoot("01-discover-play-hover", func() {
			if r, ok := tt.Find(i18n.X("voice row", "Use")); ok {
				tt.Move(r.X-float32(16*cfg.scale), r.Y+r.H/2)
			}
		})
		if len(a.gallery.page.Items) > 0 {
			first := a.gallery.page.Items[0]
			shoot("02-discover-badge-hover", func() {
				rbHover(tt, i18n.T("Filter by “%s”", archGenderAge(first, loc)))
			})
		}
		if cfg.scale > 1 { // the table under 「推荐」
			shoot("02b-discover-table", func() {
				tt.Move(float32(cfg.w)*0.6, float32(cfg.h)*0.6)
				tt.Scroll(float32(cfg.w)*0.6, float32(cfg.h)*0.6, 0, float32(cfg.h))
			})
		}
		tt.Move(1, 1)

		// Mine.
		addVoice(t, a, core.Voice{Name: i18n.T("My narrator voice"), Kind: core.VoiceCloned, Language: "zh", RefText: "大家好，欢迎收听今天的节目。",
			Style: "沉稳、温暖"}, 12)
		addVoice(t, a, core.Voice{Name: "老奶奶讲故事", Kind: core.VoiceDesigned, Language: "zh", Design: "慈祥的老奶奶，语速慢，四川话",
			DesignAttrs: designAttrs(vd.Attributes{Gender: "female", Age: "elderly", Pace: "slow", Dialect: "sichuan"}), Seed: 42}, 0)
		addVoice(t, a, core.Voice{Name: "Trailer Voice", Kind: core.VoiceDesigned, Language: "en", Design: "A deep, big trailer voice.",
			DesignAttrs: designAttrs(vd.Attributes{Gender: "male", Age: "middle_aged", Pitch: "very_low"}), Seed: 7}, 0)
		addVoice(t, a, core.Voice{Name: "Heart", Kind: core.VoiceBuiltin, Model: kokoro, Speaker: "af_heart", Language: "en", Origin: "gallery:c-af_heart"}, 0)
		a.navigate("/voices/mine")
		tt.Frame()
		shoot("03-mine-use-hover", func() { rbHover(tt, i18n.X("voice row", "Use")) })
		shoot("04-mine-badge-hover", func() { rbHover(tt, i18n.T("Filter by “%s”", i18n.T("Cloned"))) })
		shoot("04b-mine-lang-filtered", func() {
			tt.Move(1, 1)
			a.voiceLang = "zh" // as a click on a 中文 badge sets it
		})
		a.voiceLang = ""
		if !cfg.all {
			i18n.SetLocale("en")
			continue
		}

		// Projects and Assets.
		now := time.Now()
		for i, text := range []string{"欢迎收听本期节目，今天我们聊聊城市里的夜晚。", "最后一班渡轮在十一点离港。"} {
			addTake(t, a, core.Take{ID: fmt.Sprintf("T%d", i), CreatedAt: now.Add(-time.Duration(i) * time.Hour), Text: text, Model: kokoro, Voice: "af_heart", AudioSeconds: 3 + float64(i)}, 0.3)
		}
		for _, pr := range []struct {
			kind  core.ProjectKind
			title string
		}{{core.ProjectStory, "夜航渡轮"}, {core.ProjectAudiobook, "城南旧事 · 第一章"}} {
			doc := services.NewLongformDoc(pr.kind, kokoro)
			doc.Title = pr.title
			doc.Lines = append(doc.Lines, services.StoryLine{ID: "l0", Text: "台词"})
			a.lib.svc.Library.SaveDoc(context.Background(), core.Project{Kind: pr.kind, Title: pr.title}, doc)
		}
		src := filepath.Join(t.TempDir(), "产品发布会.wav")
		copyTestFile(t, d.src, src)
		a.dubStartFile(src)
		_ = eng
		a.projects.dirty = true
		a.navigate("/projects")
		tt.Frame()
		shoot("05-projects-more-hover", func() { rbHover(tt, i18n.T("Project actions")) })
		shoot("06-projects-badge-hover", func() { rbHover(tt, i18n.T("Filter by “%s”", i18n.T("Story"))) })
		tt.Move(1, 1)
		a.navigate("/assets")
		tt.Frame()
		shoot("07-assets-more-hover", func() { rbHover(tt, i18n.T("Asset actions")) })
		shoot("08-assets-badge-hover", func() { rbHover(tt, i18n.T("Filter by “%s”", pageTitle("/studio"))) })
		tt.Move(1, 1)
		i18n.SetLocale("en")
	}
}
