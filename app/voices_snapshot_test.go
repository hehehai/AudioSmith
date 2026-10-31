package main

import (
	"context"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/settings"
	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
)

// TestSnapshotsVoices renders the voice pages (library + editor, a voice
// from a clip, Design, Gallery) light and dark, in English and Chinese, as
// TestSnapshots does the others:
//
//	AUDIOSMITH_SNAPSHOTS=/tmp/shots go test -run TestSnapshots .
func TestSnapshotsVoices(t *testing.T) { runSnapshots(t, snapshotsVoices) }

func snapshotsVoices(t *testing.T, out string) {
	t.Cleanup(func() { i18n.SetLocale("en") })

	for _, run := range []struct {
		lang string
		dark bool
	}{{"en", false}, {"en", true}, {"zh-Hans", false}, {"zh-Hans", true}} {
		i18n.SetLocale("en")
		a, eng := newVoiceTestApp(t)
		acceptLicences(t, a)
		if run.lang != "en" {
			a.updateSettings(func(s *settings.Settings) { s.General.Language = run.lang })
		}
		tt := ui.NewTester(a.view, 1100, 720)
		tt.SetDark(run.dark)
		tt.SetScale(2)
		suffix := "light"
		if run.dark {
			suffix = "dark"
		}
		if run.lang != "en" {
			suffix += "-" + run.lang
		}
		save := func(name string) {
			tt.Frame()
			tt.Frame()
			f, err := os.Create(filepath.Join(out, name+"-"+suffix+".png"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := png.Encode(f, tt.Image()); err != nil {
				t.Fatal(err)
			}
		}

		a.navigate("/voices")
		save("voices-empty")

		me := addVoice(t, a, core.Voice{Name: "Narrator (me)", Kind: core.VoiceCloned, RefText: "The house at the end of the lane had been empty for years.",
			Style: "calm and slow", Language: "en", Tags: []string{"narration"}}, 9)
		a.setVoicePortrait(me.ID, testPNG(t, 320, 240))
		rec := filepath.Join(testDir(t), "consent.wav")
		writeSpeech(rec, 3, []span{{0, 3}})
		a.lib.svc.Voices.SetConsent(context.Background(), me.ID, core.ConsentRecording{Audio: rec, Statement: consentStatement(), Locale: "en"})
		addVoice(t, a, core.Voice{Name: "老奶奶讲故事", Kind: core.VoiceDesigned, Language: "zh", Design: "慈祥的老奶奶，语速慢，四川话",
			DesignAttrs: designAttrs(vd.Attributes{Gender: "female", Age: "elderly", Pace: "slow", Dialect: "sichuan"}), Seed: 42}, 0)
		addVoice(t, a, core.Voice{Name: "Trailer Voice", Kind: core.VoiceDesigned, Language: "en",
			DesignAttrs: designAttrs(vd.Attributes{Gender: "male", Age: "middle_aged", Pitch: "very_low", Styles: []string{"dramatic"}}), Seed: 7}, 0)
		addVoice(t, a, core.Voice{Name: "Podcast Host", Kind: core.VoiceCloned, Language: "en"}, 12)
		tt.Frame()
		save("voices")
		a.openVoiceEditor(me.ID)
		save("voice-editor")
		a.edit.consentOpen = true
		a.edit.exportOpen = true
		tt.SetSize(1100, 1700)
		save("voice-editor-tall")
		tt.SetSize(1100, 720)
		a.edit = nil

		a.startClone("")
		save("clone-empty")
		clip := filepath.Join(testDir(t), "interview.wav")
		writeSpeech(clip, 32, []span{{0.4, 3}, {4, 11}, {11.6, 19}, {20, 27}, {28, 31}})
		a.cloneLoad(clip, "interview.wav", false)
		if r := eng.lastOf(t, proto.MethodVADDetect); r.ID != "" {
			eng.fail(r.ID, proto.CodeModelNotDownloaded, "no VAD")
		}
		answerTranscribe(t, eng, "So the first thing I tell every new narrator is: slow down, and let the sentence breathe.")
		a.clone.name = "Interview voice"
		save("clone")
		tt.SetSize(1100, 1300)
		save("clone-tall")
		tt.SetSize(1100, 720)

		a.navigate("/design")
		a.design.description = "a warm elderly British storyteller, slightly raspy, sparkly"
		a.design.seed = 1234567
		save("design")
		tt.SetSize(1100, 1500)
		save("design-tall")
		tt.SetSize(1100, 720)

		a.navigate("/gallery")
		save("gallery")
	}
}
