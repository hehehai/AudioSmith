package main

// The features' 「介绍」 dialogs (0.5 F6, after ElevenLabs' "Introducing
// …"): the first visit to Dub, Speech to text, Voice isolator, New voice,
// Stories and Audiobook shows what the page does in three points, and
// 「开始」. Each shows once (settings' Setup.Intros); Settings → General's
// 「重新显示功能介绍」 shows them again.

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// introState is the dialog's state.
type introState struct {
	introID   string // the feature shown
	introOpen bool
	// noIntros keeps the dialogs away (tests that are not about them).
	noIntros bool
}

// intro is a feature's introduction.
type intro struct {
	id, path string // path: the page (and those under it) that shows it
	icon     icons.Icon
	title    string
	points   [3]string
}

// intros are the features with an introduction.
func intros() []intro {
	return []intro{
		{"dub", "/dub", dubIcon, i18n.T("Video dubbing"), [3]string{
			i18n.T("Drop a video or paste a link, pick the language and press Dub."),
			i18n.T("Each speaker is transcribed, translated and voiced in a clone of their own voice."),
			i18n.T("Open a dub to check its lines, edit the subtitles and export."),
		}},
		{"transcripts", "/transcripts", transcribeIcon, i18n.T("Speech to text"), [3]string{
			i18n.T("Drop a recording, choose a file, paste a link or record."),
			i18n.T("Turn on speakers to see who said what, with timings."),
			i18n.T("Export subtitles, or send the text to Dub or Text to speech."),
		}},
		{"isolate", "/isolate", isolateIcon, i18n.T("Voice isolator"), [3]string{
			i18n.T("Drop audio or video: the voice is separated from music and noise."),
			i18n.T("Listen to the voice, the background and the cleaned voice, and save each."),
			i18n.T("Send the voice on to Speech to text or Dub."),
		}},
		{"voice-new", "/voices/new", cloneIcon, i18n.T("New voice"), [3]string{
			i18n.T("Quick clone: record or drop a short, clear sample of a voice you may use."),
			i18n.T("Design: describe a voice in words and pick one of the candidates."),
			i18n.T("Saved voices are in Voices, ready for Text to speech, Stories and Dub."),
		}},
		{"stories", "/stories", storiesIcon, i18n.T("Stories"), [3]string{
			i18n.T("Describe a story in a sentence, or paste a script."),
			i18n.T("Each character gets a voice, and the narrator one of their own."),
			i18n.T("Direct the lines, then render the whole scene and export it."),
		}},
		{"audiobook", "/audiobook", audiobookIcon, i18n.T("Audiobook"), [3]string{
			i18n.T("Paste a manuscript, or import TXT, Markdown, EPUB or DOCX."),
			i18n.T("Headings become chapters; choose the narrator's voice."),
			i18n.T("Render chapter by chapter, then export the book."),
		}},
	}
}

// introOf is the introduction of path's feature; false for none.
func introOf(path string) (intro, bool) {
	for _, in := range intros() {
		if path == in.path || strings.HasPrefix(path, in.path+"/") {
			return in, true
		}
	}
	return intro{}, false
}

// introByID is an introduction by its id.
func introByID(id string) (intro, bool) {
	for _, in := range intros() {
		if in.id == id {
			return in, true
		}
	}
	return intro{}, false
}

// maybeIntro opens the introduction of the page shown, the first time;
// it counts as seen from then on.
func (a *app) maybeIntro() {
	if a.introOpen || a.noIntros || a.settingsOpen || a.obOpen || a.paletteOpen || a.licenceOpen {
		return
	}
	in, ok := introOf(a.router.Path())
	if !ok || slices.Contains(a.prefs.Setup.Intros, in.id) {
		return
	}
	a.introID, a.introOpen = in.id, true
	a.updateSettings(func(s *settings.Settings) {
		if !slices.Contains(s.Setup.Intros, in.id) {
			s.Setup.Intros = append(slices.Clone(s.Setup.Intros), in.id)
		}
	})
}

// skipIntroOf counts the introduction of path's feature as seen (a sample
// opened there: the user chose to try it).
func (a *app) skipIntroOf(path string) {
	in, ok := introOf(path)
	if !ok || slices.Contains(a.prefs.Setup.Intros, in.id) {
		return
	}
	a.updateSettings(func(s *settings.Settings) {
		if !slices.Contains(s.Setup.Intros, in.id) {
			s.Setup.Intros = append(slices.Clone(s.Setup.Intros), in.id)
		}
	})
}

// resetIntros shows every introduction again (Settings → General).
func (a *app) resetIntros() {
	a.updateSettings(func(s *settings.Settings) { s.Setup.Intros = nil })
	a.toast(i18n.T("The feature introductions show again on your next visit."))
}

// introDialog is the dialog: an illustration, the title, three points
// and 「开始」.
func (a *app) introDialog(c *ui.Context) {
	a.maybeIntro()
	if !a.introOpen {
		return
	}
	in, ok := introByID(a.introID)
	if !ok {
		a.introOpen = false
		return
	}
	t := c.Theme()
	k := tok(t)
	_, h := c.Size()
	dialogFrame(c, "intro", &a.introOpen, in.title, i18n.T("What's here"), 440, func() {
		ui.Box(c.Key("intro-art")).Height(max(px(t, 64), min(px(t, 96), h*0.14))).Radius(px(t, radiusCard)).Background(k.Subtle).Center().Shrink(0).Children(func() {
			ui.Box(c).Size(px(t, 52), px(t, 52)).Radius(px(t, radiusControl)).Background(k.Bg).Border(1, k.Line).Center().Children(func() {
				ui.Icon(c, in.icon.Linear).Size(px(t, 26), px(t, 26)).TextColor(k.Text)
			})
		})
		ui.Column(c).Gap(px(t, sp3)).Padding(px(t, sp3), 0, px(t, sp2)).Children(func() {
			for i, pt := range in.points {
				ui.Row(c.Key(fmt.Sprint("intro-pt-", i))).Gap(px(t, sp2)).AlignItems(ui.Start).Children(func() {
					ui.Icon(c, icons.CheckCircle.Linear).Size(px(t, 16), px(t, 16)).TextColor(k.Text2).Shrink(0).Margin(px(t, 2), 0, 0, 0)
					ui.Text(c, pt).Wrap().Grow(1).MinWidth(0)
				})
			}
		})
	}, func() {
		ui.Spacer(c)
		if primaryButton(c.Key("intro-start"), i18n.T("Get started")).Clicked() {
			a.introOpen = false
		}
	})
}
