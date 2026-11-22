package main

// Japanese character voices in the Studio and Settings (thread #171):
// the connector's row of the model picker (or how to get an engine), its
// voices (characters and styles, with their samples and terms), their
// notes, Advanced scales, the terms confirmation, and Settings → Models'
// card of the engines.

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/voicevox"
)

// termsMark says what a character's terms allow, in a few words, and
// whether it limits commercial use.
func termsMark(t voicevox.Terms) (string, bool) {
	switch t.Use {
	case voicevox.UseNonCommercial:
		return i18n.T("Non-commercial only"), true
	case voicevox.UseIndividual:
		return i18n.T("Companies ask first"), true
	case voicevox.UseAsk:
		return i18n.T("Check the terms before use"), true
	}
	if t.Credit != "" {
		return i18n.T("Commercial use OK · credit required"), false
	}
	if strings.HasPrefix(t.Licence, "ACML") {
		return i18n.T("Commercial use OK · credit optional"), false
	}
	return i18n.T("Commercial use OK"), false
}

// termsNote is a Terms' note in the UI's language.
func termsNote(t voicevox.Terms) string {
	switch t.Note {
	case voicevox.NoteCompanies:
		return i18n.T("Companies and organisations must ask the rights holder first.")
	case voicevox.NoteMochiko:
		return i18n.T("Not for voice works, voice material or games; companies ask first.")
	case voicevox.NoteZunko:
		return i18n.T("Tohoku Zunko project: no political or religious use, no misleading content, see the project's terms.")
	case voicevox.NoteUnknown:
		return i18n.T("Terms not in AudioSmith's table: read them in the engine before using its audio.")
	case voicevox.NoteCustom:
		return i18n.T("The model's own licence: read it before using its audio.")
	}
	return ""
}

// characterPickerRows are the connector's part of the Studio's model
// picker: its row while an engine answers, else how to get one. find
// filters it like the other rows.
func (a *app) characterPickerRows(c *ui.Context, cur catalog.Entry, find string, pick func(catalog.Entry)) int {
	e := charactersEntry()
	if e.ID == "" || find != "" && !e.Matches(find) {
		return 0
	}
	a.pokeCharacters()
	t := c.Theme()
	k := tok(t)
	if a.lib != nil && a.lib.vv != nil && a.lib.vv.Online() {
		a.pickerRow(c, e, cur, e.Description(), "", true, pick)
		return 1
	}
	ui.Column(c.Key("pm-voicevox-get")).Gap(px(t, 2)).Padding(px(t, 6), px(t, sp2)).Children(func() {
		ui.Text(c, e.DisplayName()).TextColor(k.Text3).SingleLine().Ellipsis("…").MinWidth(0)
		ui.Text(c, i18n.T("Install and open VOICEVOX or AivisSpeech to use their Japanese characters here.")).
			FontSize(px(t, fsMicro)).TextColor(k.Text2).MaxLines(2).Ellipsis("…").MinWidth(0)
		a.characterGetLinks(c)
	})
	return 1
}

// characterGetLinks are the links to get the engines.
func (a *app) characterGetLinks(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(px(t, sp3)).Children(func() {
		for _, k := range voicevox.Kinds {
			if linkButton(c.Key("get-"+string(k)), i18n.T("Get %s", k.Brand())).Clicked() {
				c.Services().OpenURL(k.Site())
			}
		}
	})
}

// characterVoiceChoices are the Studio's voice rows on the connector:
// every style of every online character, grouped by character, each
// with its sample and what its terms allow.
func (a *app) characterVoiceChoices() (cur string, opts []voiceOption, acts map[string]func()) {
	a.pokeCharacters()
	acts = map[string]func(){}
	cur = a.studioCharacterVoice()
	vv := a.lib.vv
	for _, ch := range a.characterVoices() {
		id := characterVoiceID(ch)
		sub := ""
		if tm, ok := vv.Terms(id); ok && vv.HasInfo(id) || ok && ch.Engine.Flavor == voicevox.VOICEVOX {
			sub, _ = termsMark(tm)
		}
		op := voiceOption{value: id, label: ch.Style.Name, sub: sub, group: ch.Speaker.Name + " · " + ch.Engine.Flavor.Brand()}
		if p := vv.Sample(id); p != "" {
			op.preview = voicePreview{id: "vp:" + id, path: p}
		}
		opts = append(opts, op)
		acts[id] = func() {
			a.voice, a.vvLast = id, id
		}
	}
	if len(opts) == 0 {
		opts = append(opts, voiceOption{value: "none", label: i18n.T("No engine is running"), warn: true})
	}
	return cur, opts, acts
}

// characterLabel names a character voice: "ずんだもん（ノーマル）".
func (a *app) characterLabel(voice string) string {
	if a.lib == nil || a.lib.vv == nil {
		return voice
	}
	if l := a.lib.vv.Label(voice); l != "" {
		return l
	}
	if voice == "" {
		return i18n.T("No engine is running")
	}
	return voice
}

// characterNotes are the lines under the Studio's voice on the
// connector: what the character's terms allow, the credit its takes
// carry, and the terms' link; or that no engine answers.
func (a *app) characterNotes(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	voice := a.studioCharacterVoice()
	if voice == "" || !a.lib.vv.Online() {
		ui.Column(c.Key("vv-offline")).Gap(px(t, 4)).Children(func() {
			caption(c, i18n.T("Neither VOICEVOX nor AivisSpeech is running: open one of them, then try again.")).TextColor(t.Warning)
			a.characterGetLinks(c)
		})
		return
	}
	tm, ok := a.lib.vv.Terms(voice)
	if !ok {
		return
	}
	mark, warn := termsMark(tm)
	ui.Column(c.Key("vv-notes")).Gap(px(t, 4)).Children(func() {
		m := caption(c, mark)
		if warn {
			m.TextColor(t.Warning)
		}
		if note := termsNote(tm); note != "" {
			ui.Text(c, note).FontSize(px(t, fsMicro)).TextColor(k.Text2).MaxLines(3).Ellipsis("…")
		}
		if tm.Credit != "" {
			ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
				ui.Text(c, i18n.T("Credit: %s", tm.Credit)).FontSize(px(t, fsMicro)).TextColor(k.Text2).Grow(1).MinWidth(0).MaxLines(2)
				if linkButton(c.Key("vv-copy-credit"), i18n.T("Copy")).Clicked() {
					c.Services().WriteClipboard(tm.Credit)
					a.toast(i18n.T("Credit copied"))
				}
			})
		}
		if tm.URL != "" {
			if linkButton(c.Key("vv-terms"), i18n.T("Read the terms")).Clicked() {
				c.Services().OpenURL(tm.URL)
			}
		}
	})
}

// characterAdvanced are the connector's Advanced fields: the engine's
// scales (pitch on VOICEVOX only: AivisSpeech's suffers from it; tempo
// dynamics on AivisSpeech only) and the silences around the take.
func (a *app) characterAdvanced(c *ui.Context) {
	ch, _ := a.lib.vv.Lookup(a.studioCharacterVoice())
	aivis := ch.Engine.Flavor == voicevox.Aivis
	s := &a.vvScales
	scale := func(key, label, info string, v *float64, def, lo, hi, step float64) {
		val := *v
		if val == 0 {
			val = def
		}
		fieldInfo(c, label, info, func() {
			if numberInput(c.Key(key), &val, lo, hi, step, numOpts{Label: label}) {
				*v = val
				if val == def {
					*v = 0
				}
			}
		})
	}
	if !aivis {
		scale("vv-pitch", i18n.T("Pitch"), i18n.T("Higher or lower than the character's own voice."), &s.Pitch, 0, -0.15, 0.15, 0.01)
		scale("vv-intonation", i18n.T("Intonation"), i18n.T("How much the voice rises and falls; lower is flatter."), &s.Intonation, 1, 0.1, 2, 0.05)
	} else {
		scale("vv-intonation", i18n.T("Emotion strength"), i18n.T("How strongly the style's emotion comes through."), &s.Intonation, 1, 0.1, 2, 0.05)
		scale("vv-tempo", i18n.T("Tempo dynamics"), i18n.T("How much the pace varies; higher sounds livelier."), &s.TempoDynamics, 1, 0.1, 2, 0.05)
	}
	scale("vv-volume", i18n.T("Volume"), "", &s.Volume, 1, 0.1, 2, 0.05)
	scale("vv-pre", i18n.T("Silence before (seconds)"), "", &s.PreSilence, 0, 0, 5, 0.05)
	scale("vv-post", i18n.T("Silence after (seconds)"), "", &s.PostSilence, 0, 0, 5, 0.05)
}

// characterAskModal confirms a character's terms before its first take.
func (a *app) characterAskModal(c *ui.Context) {
	ask := a.vvAsk
	if ask == nil {
		return
	}
	t := c.Theme()
	k := tok(t)
	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(px(t, 460)).Gap(px(t, sp3)).Children(func() {
			sectionHeading(c, i18n.T("Terms of %s", ask.name))
			fillCard(c).Gap(px(t, sp2)).Children(func() {
				mark, _ := termsMark(ask.terms)
				ui.Text(c, mark).TextColor(t.Warning)
				if note := termsNote(ask.terms); note != "" {
					ui.Text(c, note).FontSize(px(t, fsCaption)).TextColor(k.Text2)
				}
				if ask.terms.Credit != "" {
					ui.Text(c, i18n.T("Credit: %s", ask.terms.Credit)).FontSize(px(t, fsCaption)).TextColor(k.Text2)
				}
				if p := strings.TrimSpace(ask.policy); p != "" {
					ui.Scroll(c.Key("vv-policy")).MaxHeight(px(t, 160)).Children(func() {
						ui.Text(c, p).FontSize(px(t, fsMicro)).TextColor(k.Text2)
					})
				}
				if ask.terms.URL != "" {
					ui.Link(c, i18n.T("Read the terms"), ask.terms.URL)
				}
			})
			ui.Checkbox(c.Key("vv-agree"), &ask.agree, i18n.T("I have read the terms and will use this character's audio only as they allow."))
			ui.Row(c).Gap(px(t, sp2)).Justify(ui.End).Children(func() {
				if ui.Button(c.Key("vv-cancel"), i18n.T("Cancel")).Clicked() {
					open = false
				}
				if primaryButton(c.Key("vv-continue"), i18n.T("Continue")).Disabled(!ask.agree).Clicked() && ask.agree {
					a.confirmCharacter(ask.voice)
					a.vvAsk = nil
					if ask.then != nil {
						ask.then()
					}
				}
			})
		})
	})
	if !open {
		a.vvAsk = nil
	}
}

// charactersSettings is Settings → Models' card of the engines: whether
// each answers (version, characters), its address, and where to get it.
func (a *app) charactersSettings(c *ui.Context) {
	e := charactersEntry()
	if e.ID == "" || a.lib == nil || a.lib.vv == nil {
		return
	}
	a.pokeCharacters()
	t := c.Theme()
	k := tok(t)
	ce := a.prefs.Models.Characters
	card(c.Key("model-voicevox")).Padding(px(t, sp3), px(t, sp4)).Gap(px(t, sp2)).Children(func() {
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, e.DisplayName()).FontWeight(600).Grow(1).MinWidth(0)
			modelTags(c, e, 2)
		})
		caption(c, i18n.T("AudioSmith connects to the VOICEVOX or AivisSpeech app you installed; it bundles none of their files. Each character's terms apply to its audio, and takes keep the credit they need."))
		for _, en := range a.lib.vv.Engines() {
			url := &ce.VoicevoxURL
			if en.Kind == voicevox.Aivis {
				url = &ce.AivisURL
			}
			ui.Row(c.Key("vv-engine-" + string(en.Kind))).Wrap().GapX(px(t, sp3)).GapY(px(t, 4)).AlignItems(ui.Center).Children(func() {
				ui.Text(c, en.Kind.Brand()).Width(px(t, 96)).Shrink(0)
				state, col := i18n.T("Not running"), k.Text3
				if en.Online {
					state, col = i18n.T("Connected · %s · %d characters", en.Version, len(en.Speakers)), t.Success
				}
				ui.Text(c, state).FontSize(px(t, fsCaption)).TextColor(col).Grow(1).MinWidth(px(t, 120))
				addr := *url
				in := ui.TextInput(c.Key("url"), &addr).Placeholder(voicevox.DefaultURL(en.Kind)).Label(i18n.T("%s address", en.Kind.Brand())).Width(px(t, 220))
				if in.Changed() {
					v, kind := strings.TrimSpace(addr), en.Kind
					a.updateSettings(func(s *settings.Settings) {
						if kind == voicevox.Aivis {
							s.Models.Characters.AivisURL = v
						} else {
							s.Models.Characters.VoicevoxURL = v
						}
					})
					a.vvPoked = time.Time{} // probe the new address at once
				}
				if !en.Online && linkButton(c.Key("get"), i18n.T("Get %s", en.Kind.Brand())).Clicked() {
					c.Services().OpenURL(en.Kind.Site())
				}
			})
		}
		if n := len(ce.Confirmed); n > 0 {
			ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
				caption(c, i18n.N(n, "Terms confirmed for %d character", "Terms confirmed for %d characters", n)).Grow(1)
				if linkButton(c.Key("vv-forget"), i18n.T("Ask again")).Clicked() {
					a.updateSettings(func(s *settings.Settings) { s.Models.Characters.Confirmed = nil })
				}
			})
		}
	})
}

// creditLine is a take's credit under it: the attribution its audio
// must carry when published, and Copy.
func (a *app) creditLine(c *ui.Context, credit string) {
	t := c.Theme()
	k := tok(t)
	ui.Row(c.Key("credit")).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
		ui.Text(c, i18n.T("Credit: %s", credit)).FontSize(px(t, fsCaption)).TextColor(k.Text2).Grow(1).MinWidth(0).MaxLines(2).
			Tooltip(i18n.T("Name this credit wherever you publish the audio."))
		if linkButton(c.Key("copy-credit"), i18n.T("Copy")).Clicked() {
			c.Services().WriteClipboard(credit)
			a.toast(i18n.T("Credit copied"))
		}
	})
}
