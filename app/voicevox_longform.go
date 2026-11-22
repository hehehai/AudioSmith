package main

// Stories and Audiobook with Japanese character voices (thread #171):
// the cast pickers offer the characters of the engines that answer, a
// character whose terms limit its use is confirmed before the render,
// and the outputs name the credits the book must carry.

import (
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/voicevox"
)

// characterLfVoices are the characters' styles as longform voices
// ("speaker:<voice id>").
func (a *app) characterLfVoices() []lfVoice {
	a.pokeCharacters()
	var out []lfVoice
	for _, ch := range a.characterVoices() {
		id := characterVoiceID(ch)
		out = append(out, lfVoice{services.SpeakerRef + id, ch.Speaker.Name + "（" + ch.Style.Name + "）"})
	}
	return out
}

// characterLfOption is a character's row of a cast picker: grouped by
// character, with its sample and what its terms allow.
func (a *app) characterLfOption(v lfVoice, id string) voiceOption {
	op := voiceOption{value: v.ref, label: v.label}
	ch, ok := a.lib.vv.Lookup(id)
	if !ok {
		return op
	}
	op.label, op.group = ch.Style.Name, ch.Speaker.Name+" · "+ch.Engine.Flavor.Brand()
	if tm, ok := a.lib.vv.Terms(id); ok {
		op.sub, _ = termsMark(tm)
	}
	if p := a.lib.vv.Sample(id); p != "" {
		op.preview = voicePreview{id: "vp:" + id, path: p}
	}
	return op
}

// unconfirmedCharacter is a character of spec whose terms the user has
// not confirmed yet ("" for none).
func (a *app) unconfirmedCharacter(spec services.LongformSpec) string {
	if !entryFor(spec.Model).IsExternal() {
		return ""
	}
	for _, ch := range spec.Chapters {
		for _, l := range ch.Lines {
			for _, sp := range l.Spans {
				if v := spec.Voices[sp.Voice].Voice; voicevox.IsVoice(v) && !a.characterConfirmed(v) {
					return v
				}
			}
		}
	}
	return ""
}

// renderCredits are the credits a render's audio must carry, under its
// outputs, with Copy: what to name wherever the book is published.
func (a *app) renderCredits(c *ui.Context, credits []string) {
	if len(credits) == 0 {
		return
	}
	t := c.Theme()
	k := tok(t)
	text := services.CreditsText(credits)
	ui.Row(c.Key("render-credits")).Gap(px(t, sp2)).AlignItems(ui.Start).Children(func() {
		ui.Text(c, i18n.T("Credits to name when you publish: %s", joinCredits(credits))).FontSize(px(t, fsCaption)).TextColor(k.Text2).
			Grow(1).MinWidth(0).MaxLines(3).Ellipsis("…")
		if linkButton(c.Key("copy-credits"), i18n.T("Copy")).Clicked() {
			c.Services().WriteClipboard(text)
			a.toast(i18n.T("Credit copied"))
		}
	})
}

// joinCredits lists credits in a line.
func joinCredits(credits []string) string {
	out := ""
	for i, c := range credits {
		if i > 0 {
			out += " · "
		}
		out += c
	}
	return out
}
