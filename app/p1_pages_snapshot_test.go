package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/store/historyjson"
	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
)

// p1Config is one look of the UI overhaul's P1 review (the tokens and
// the kit, plan/ui-overhaul-plan.md §4).
type p1Config struct {
	name  string
	w, h  int
	scale float64
	dark  bool
	lang  string
}

var p1Configs = []p1Config{
	{"1100-zh", 1100, 720, 1, false, "zh-Hans"},
	{"1100-dark-zh", 1100, 720, 1, true, "zh-Hans"},
	{"min125-zh", 820, 520, 1.25, false, "zh-Hans"},
}

// TestSnapshotsP1Pages renders the six pages of the P1 review (Studio,
// Discover voices, My voices, the Dub editor, Transcribe, Settings) in
// each p1Config, for before/after pictures of the new tokens and kit:
//
//	AUDIOSMITH_SNAPSHOTS=/tmp/shots go test -run TestSnapshotsP1Pages ./app
func TestSnapshotsP1Pages(t *testing.T) { runSnapshots(t, snapshotsP1Pages) }

func snapshotsP1Pages(t *testing.T, out string) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	useMemKeys(t)
	stubMachine(t, 16, true)
	for _, cfg := range p1Configs {
		setup := func(a *app, tt *ui.Tester) {
			i18n.SetLocale("en")
			a.updateSettings(func(s *settings.Settings) {
				s.General.Language = cfg.lang
				s.Appearance.Scale = cfg.scale
			})
			tt.SetSize(cfg.w, cfg.h)
			tt.SetDark(cfg.dark)
			tt.SetScale(2)
		}
		shoot := func(a *app, tt *ui.Tester, page string, fn func()) {
			fn()
			a.toasts = nil
			tt.Frame()
			tt.Frame()
			tt.Frame()
			f, err := os.Create(filepath.Join(out, "p1-"+page+"-"+cfg.name+".png"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := png.Encode(f, tt.Image()); err != nil {
				t.Fatal(err)
			}
		}

		// Studio, Discover voices, My voices.
		a, _, tt := newTestApp(t)
		acceptLicences(t, a)
		setup(a, tt)
		a.loadedModel = kokoro
		for i, text := range []string{
			"Hello from AudioSmith. This voice was generated on your Mac with MLX.",
			"你好，这是在 Mac 上用 MLX 生成的语音。",
			"The same voice, a little faster, for the trailer cut.",
		} {
			at := time.Date(2026, 10, 13, 21, 50+i, 0, 0, time.Local)
			c := core.Take{ID: historyjson.NewTakeID(at), CreatedAt: at, Text: text, Model: kokoro,
				Voice: []string{"af_heart", "zf_xiaobei", "am_adam"}[i], Speed: 1, SampleRate: 24000,
				AudioSeconds: 5.1 - float64(i), GenSeconds: 1.2}
			addTake(t, a, c, 1.2)
			a.lastClipID = c.ID
		}
		shoot(a, tt, "studio", func() { a.navigate("/studio") })
		shoot(a, tt, "gallery", func() { a.navigate("/gallery") })
		addVoice(t, a, core.Voice{Name: "Narrator (me)", Kind: core.VoiceCloned, RefText: "The house at the end of the lane had been empty for years.",
			Style: "calm and slow", Language: "en", Tags: []string{"narration"}}, 9)
		addVoice(t, a, core.Voice{Name: "老奶奶讲故事", Kind: core.VoiceDesigned, Language: "zh", Design: "慈祥的老奶奶，语速慢，四川话",
			DesignAttrs: designAttrs(vd.Attributes{Gender: "female", Age: "elderly", Pace: "slow", Dialect: "sichuan"}), Seed: 42}, 0)
		addVoice(t, a, core.Voice{Name: "Podcast Host", Kind: core.VoiceCloned, Language: "en"}, 12)
		tt.Frame()
		shoot(a, tt, "voices", func() { a.edit = nil; a.navigate("/voices") })
		shoot(a, tt, "settings", func() { a.navigate("/settings/general") })
		a.settingsOpen = false

		// The Dub editor, at the translation step.
		i18n.SetLocale("en")
		d := newDubTest(t, 6)
		setup(d.a, d.tt)
		da := d.a
		da.navigate("/dub")
		da.dubSourceFile(d.src)
		da.dubSet(func(p *dub.Project) { p.Targets = []string{"es", "fr"} })
		da.dubStart()
		da.dubTranslate(false)
		da.dubLang = "es"
		shoot(da, d.tt, "dub", func() { da.dubStep = dubStepTranslate })

		// Transcribe, a transcript open.
		i18n.SetLocale("en")
		m := newM2Test(t)
		setup(m.a, m.tt)
		now := time.Now()
		long := sampleTranscript("t1", now.Add(-26*time.Hour))
		long.Duration = 74
		long.Segments = append(long.Segments,
			core.TranscriptSegment{Start: 4.1, End: 9.8, Text: "You are a bold one. I have been looking forward to this.", Edited: true},
			core.TranscriptSegment{Start: 10.5, End: 14, Text: "So uncivilized."})
		putTranscript(t, m.a, long)
		shoot(m.a, m.tt, "transcribe", func() {
			m.a.navigate("/transcripts")
			m.a.selectTranscript("t1")
		})
		i18n.SetLocale("en")
	}
}
