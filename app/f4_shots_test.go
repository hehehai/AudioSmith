package main

// Review images of 0.5 F4 (Voices: Discover, Mine, the detail, New
// voice, Quick clone's steps, Design), in Chinese at 1100×720, a few in
// English, dark, and at 820×520 @125 %. Not compared with anything:
//
//	AUDIOSMITH_SNAPSHOTS=/tmp/f4 go test -run TestSnapshotsF4 ./app

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/gallery"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/settings"
	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
)

func TestSnapshotsF4(t *testing.T) { runSnapshots(t, snapshotsF4) }

func snapshotsF4(t *testing.T, out string) {
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
		a, eng, tt := newTestApp(t)
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
		pinTester(tt)
		shoot := func(name string, fn func()) {
			fn()
			a.toasts = nil
			tt.Frame()
			tt.Frame()
			tt.Frame()
			f, err := os.Create(filepath.Join(out, fmt.Sprintf("f4-%s-%s.png", name, cfg.name)))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := png.Encode(f, tt.Image()); err != nil {
				t.Fatal(err)
			}
		}
		g := &a.gallery
		shoot("01-discover", func() { a.navigate("/voices/discover") })
		if cfg.dark {
			continue
		}
		shoot("02-discover-detail", func() {
			if cfg.lang == "zh-Hans" {
				a.openArchetype("c-headline-desk")
			} else {
				a.openArchetype("c-late-night-radio")
			}
		})
		g.detail = ""
		shoot("03-discover-filtered", func() {
			g.tab = 1 // narration
			g.filters["language"] = "zh"
		})
		g.tab = 0
		clear(g.filters)
		shoot("04-mine-empty", func() { a.navigate("/voices/mine") })
		me := addVoice(t, a, core.Voice{Name: i18n.T("My narrator voice"), Kind: core.VoiceCloned, Language: "zh", RefText: "大家好，欢迎收听今天的节目。",
			Style: "沉稳、温暖"}, 12)
		addVoice(t, a, core.Voice{Name: "老奶奶讲故事", Kind: core.VoiceDesigned, Language: "zh", Design: "慈祥的老奶奶，语速慢，四川话",
			DesignAttrs: designAttrs(vd.Attributes{Gender: "female", Age: "elderly", Pace: "slow", Dialect: "sichuan"}), Seed: 42}, 0)
		addVoice(t, a, core.Voice{Name: "Trailer Voice", Kind: core.VoiceDesigned, Language: "en", Design: "A deep, big trailer voice.",
			DesignAttrs: designAttrs(vd.Attributes{Gender: "male", Age: "middle_aged", Pitch: "very_low"}), Seed: 7}, 0)
		addVoice(t, a, core.Voice{Name: "Heart", Kind: core.VoiceBuiltin, Model: kokoro, Speaker: "af_heart", Language: "en", Origin: "gallery:c-af_heart"}, 0)
		shoot("05-mine", func() { a.mine.usesFor = "" })
		shoot("06-mine-detail", func() { a.openVoiceEditor(me.ID) })
		a.edit = nil
		shoot("07-new-voice", func() { a.navigate("/voices/new") })

		// Quick clone: 检查, 命名保存, 完成.
		a.startClone("")
		clip := filepath.Join(testDir(t), "interview.wav")
		writeSpeech(clip, 24, []span{{0.4, 3}, {4, 11}, {11.6, 19}, {20, 23}})
		a.cloneLoad(clip, "interview.wav", false)
		if r := eng.lastOf(t, proto.MethodVADDetect); r.ID != "" {
			eng.fail(r.ID, proto.CodeModelNotDownloaded, "no VAD")
		}
		answerTranscribe(t, eng, "大家好，今天想和你聊聊我最近读的一本书，它讲的是一个小镇上的故事。")
		shoot("08-clone-check", func() {})
		shoot("09-clone-name", func() {
			a.clone.step = cloneName
			a.clone.name = "我的旁白"
		})
		shoot("10-clone-done", func() { a.cloneSave() })

		// Design: a preset, candidates, one picked.
		a.startDesign()
		for _, p := range gallery.Presets() {
			if p.ID == "storyteller" {
				a.applyPreset(p)
			}
		}
		shoot("11-design", func() {})
		n := len(eng.sent)
		a.designGenerateCandidates()
		for _, c := range a.design.cands { // no sound in a snapshot: the first doesn't play
			a.voiceJobs[c.takeID].voiceID = "shot"
		}
		for _, r := range eng.sent[n:] {
			if r.Cmd == "synthesize" {
				writeTestWAV(t, r.Output.Path, 24000, 2.4)
				eng.succeed(r.ID, proto.SynthesizeResult{Output: r.Output.Path, SampleRate: 24000, AudioSeconds: 2.4, Seconds: 1, Seed: r.Seed})
			}
		}
		shoot("12-design-candidates", func() { a.pickCandidate(1) })
		i18n.SetLocale("en")
	}
}
