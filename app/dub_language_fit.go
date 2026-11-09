package main

// The dub's voice model and its target languages (thread #165): a note
// under the model when it doesn't speak some of them, offering a model
// that clones in all of them (VoxCPM2's 30 languages, say Vietnamese or
// Thai, which Qwen3-TTS lacks; recommended there, thread #167: the dub's
// model picker marks it too). Offered only: the dub never switches
// models by itself, and no default changes.

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/i18n"
)

// dubUnspoken are the targets model e does not list among its languages
// (none for models that list no languages).
func dubUnspoken(e catalog.Entry, targets []string) []string {
	var out []string
	for _, l := range targets {
		if !e.Knows(l) {
			out = append(out, l)
		}
	}
	return out
}

// dubModelSpeaking is the model to offer for targets: one offered in
// pickers that clones and speaks them all, commercial licences first,
// then catalogue order; false when none does.
func dubModelSpeaking(targets []string, except string) (catalog.Entry, bool) {
	// The models presets recommend for other languages first (VoxCPM2,
	// Apache, before OmniVoice, thread #167).
	for _, e := range catalog.Builtin().ForUse(catalog.UseOtherLanguages, catalog.WorkflowDub) {
		if e.Repo != except && e.Has(catalog.CapClone) && e.Licence.Commercial && len(dubUnspoken(e, targets)) == 0 {
			return e, true
		}
	}
	var fallback *catalog.Entry
	for _, e := range catalog.Builtin().ForTask(catalog.TaskTTS) {
		if e.Hidden || e.Repo == except || !e.Has(catalog.CapClone) || len(e.Languages) == 0 && e.Family != "omnivoice" || len(dubUnspoken(e, targets)) > 0 {
			continue
		}
		if e.Licence.Commercial {
			return e, true
		}
		if fallback == nil {
			fallback = &e
		}
	}
	if fallback != nil {
		return *fallback, true
	}
	return catalog.Entry{}, false
}

// dubLanguageNote is the note under the voice model of a dub into
// targets with model cur.
func (a *app) dubLanguageNote(c *ui.Context, cur catalog.Entry, targets []string, locked bool) {
	missing := dubUnspoken(cur, targets)
	if len(missing) == 0 {
		return
	}
	names := make([]string, len(missing))
	for i, l := range missing {
		names[i] = dubLangName(l)
	}
	list := strings.Join(names, i18n.T(", "))
	alt, ok := dubModelSpeaking(missing, cur.Repo)
	if !ok {
		caption(c.Key("gen-lang-note"), i18n.T("%s doesn't speak %s: those tracks may sound wrong.", modelRef(cur), list)).TextColor(tok(c.Theme()).Warning)
		return
	}
	t := c.Theme()
	ui.Column(c.Key("gen-lang-note")).Gap(px(t, 2)).Children(func() {
		caption(c, i18n.T("%s doesn't speak %s; %s does and is recommended for it.", modelRef(cur), list, modelRef(alt))).TextColor(tok(t).Warning)
		if !locked && linkButton(c.Key("gen-lang-use"), i18n.T("Use %s", alt.DisplayName())).AlignSelf(ui.Start).Clicked() {
			repo := alt.Repo
			a.dubSettings(func(s *dub.Settings) { s.Model = repo })
		}
	})
}
