package main

// The dub's 「多人对话配音」 (multi-speaker dialogue dub, thread #89, plan
// §2.7): the conversations of a track in Chinese or English are recorded
// by a dialogue model (SoulX-Podcast), each line hearing the one before
// it (services/dub_dialogue.go). Here:
//
//   - the Cast step's switch, with why it is greyed (memory, per-line
//     voice match, no Chinese or English track), the conversations it
//     found, the tracks it leaves line by line and the download;
//   - under a speaker's card, which clip their conversations clone;
//   - the Generate step's note and the model picker's section (the same
//     setting, as Stories' #84);
//   - in the line list, each conversation's 「对话 N」 chip and coloured
//     bar, a line made line by line instead (and flagged when that one is
//     not heard clearly either), and the ⋯ menu's 整段重录 and
//     这一句不用多人对话;
//   - the footer's count, and the audition of a line already recorded.
//
// The switch is on for new projects (dub.DialogueAuto) and off for older
// ones; only one dialogue model speaks dubs yet, so 高级 has no menu for it.

import (
	"context"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/dialogue"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/services"
)

// dubDlg is the open dub's dialogue model as the page shows it.
type dubDlg struct {
	e       catalog.Entry
	shown   bool     // the switch shows: two speakers or more, a dialogue model
	blocked string   // why it is greyed ("" = it can be on)
	on      bool     // on, and it can be
	skipped []string // target languages it does not speak (line by line)
	need    bool     // not downloaded
	lang    string   // the track the stretches are of
	sts     []dub.Stretch
	byID    map[string]dub.Stretch
}

// dubDialogue is the open dub's dialogue model now.
func (a *app) dubDialogue() dubDlg {
	p := a.dub
	var d dubDlg
	es := dubDialogueEntries()
	if p == nil || len(es) == 0 || len(p.SpeakerLabels()) < 2 {
		return d
	}
	d.shown, d.e = true, es[0]
	for _, e := range es {
		if e.Repo == p.Settings.DialogueModel {
			d.e = e
		}
	}
	langs := entryLangs(d.e)
	for _, l := range p.Targets {
		if !dub.DialogueSpeaks(langs, l) {
			d.skipped = append(d.skipped, l)
		} else if d.lang == "" {
			d.lang = l
		}
	}
	if a.dubLang != "" && dub.DialogueSpeaks(langs, a.dubLang) {
		d.lang = a.dubLang
	}
	d.blocked = dubDialogueBlocked(a.macMemoryGB(), p, d.e, d.lang, d.skipped)
	d.on = d.blocked == "" && p.Settings.DialogueOn()
	if m := a.model(d.e); m.known && m.local.State != hub.Downloaded {
		d.need = true
	}
	if d.on {
		d.sts = p.DialoguePreview(d.lang, dialogue.LimitsFor(d.e.Family))
		d.byID = map[string]dub.Stretch{}
		for _, st := range d.sts {
			for _, id := range st.IDs {
				d.byID[id] = st
			}
		}
	}
	return d
}

// entryLangs are a model's languages, as base codes.
func entryLangs(e catalog.Entry) []string {
	var out []string
	for _, l := range e.Languages {
		out = append(out, catalog.BaseLanguage(l.Code))
	}
	return out
}

// dubDialogueBlocked says why dialogue model e cannot speak p's
// conversations on a Mac of gb GB ("" when it can): lang is the first
// track it speaks ("" none), skipped the tracks it does not.
func dubDialogueBlocked(gb int, p *dub.Project, e catalog.Entry, lang string, skipped []string) string {
	switch {
	case gb > 0 && float64(gb) < e.MinMemoryGB:
		return i18n.T("Needs %g GB of memory; this Mac has %d GB.", e.MinMemoryGB, gb)
	case p.Settings.VoiceMatch == dub.MatchLine:
		return i18n.T("Per-line voice match clones each line from its own clip: it cannot go with multi-speaker dialogue.")
	case lang == "":
		return dubDialogueSkipped(skipped)
	}
	return ""
}

// dubDialogueSkipped says which tracks are made line by line.
func dubDialogueSkipped(langs []string) string {
	names := make([]string, len(langs))
	for i, l := range langs {
		names[i] = dubLangPlain(l)
	}
	return i18n.T("%s: line by line; the multi-speaker models speak only Chinese and English.", strings.Join(names, i18n.T(", ")))
}

// dubDialogueRow is the Cast step's switch.
func (a *app) dubDialogueRow(c *ui.Context, locked bool) {
	d := a.dubDialogue()
	if !d.shown {
		return
	}
	t := c.Theme()
	k := tok(t)
	p := a.dub
	title := i18n.T("Multi-speaker dialogue")
	info := i18n.T("%s records the people of a conversation together: each line hears the one before it, so answers sound like answers. Each line is then fitted to its own time, as any other. Each speaker is cloned from about 12 s of their own lines. Slower than line by line; lines it does not say clearly are made again, then line by line.", modelRef(d.e))
	fillCard(c.Key("cast-dialogue")).Gap(px(t, 4)).Padding(px(t, sp3)).Children(func() {
		on := d.on
		formRow(c, "cast-dialogue-row", title, info, func() {
			if inkSwitch(c.Key("cast-dialogue"), &on).Label(title).Disabled(locked || d.blocked != "").Changed() {
				v := dub.DialogueOff
				if on {
					v = dub.DialogueAuto
				}
				a.dubSettings(func(s *dub.Settings) { s.Dialogue = v })
			}
		})
		caption(c, i18n.T("The people of a conversation are recorded together, each line hearing the one before it.")).TextColor(k.Text2)
		if d.blocked != "" {
			caption(c.Key("cast-dialogue-why"), d.blocked).TextColor(k.Text3)
			return
		}
		if !d.on {
			return
		}
		lines := 0
		for _, st := range d.sts {
			lines += len(st.IDs)
		}
		if len(d.sts) == 0 {
			caption(c.Key("cast-dialogue-none"), i18n.T("No conversation found: one person speaks at a time.")).TextColor(k.Text3)
		} else {
			caption(c.Key("cast-dialogue-found"), i18n.N(len(d.sts), "%d conversation · %d lines · %s", "%d conversations · %d lines · %s", len(d.sts), lines, d.e.DisplayName())).TextColor(k.Text3)
		}
		if len(d.skipped) > 0 {
			caption(c.Key("cast-dialogue-skip"), dubDialogueSkipped(d.skipped)).TextColor(k.Text3)
		}
		if (p.Settings.Timing == dub.TimingLipSync || p.Settings.Timing == "") && dubCaps(p.Settings.Model).Duration {
			caption(c.Key("cast-dialogue-lipsync"), i18n.T("Conversation lines can only be sped up or cut, not spoken to length: turn this off for strict lip sync.")).TextColor(k.Text3)
		}
		if d.need {
			m := a.model(d.e)
			ui.Row(c.Key("cast-dialogue-need")).Gap(px(t, sp2)).AlignItems(ui.Center).Wrap().Children(func() {
				caption(c, i18n.T("%s is not downloaded (%s): until it is, every line is made line by line.", modelRef(d.e), catalog.HumanSize(d.e.Size))).TextColor(k.Text2).Shrink(1)
				if m.downloading {
					ui.Spinner(c)
				} else if secondaryIconButton(c.Key("cast-dialogue-dl"), icons.DownloadMinimalistic, i18n.T("Download")).Label(i18n.T("Download")).Disabled(locked).Clicked() {
					a.download(d.e)
				}
			})
		}
	})
}

// dubDialogueRefNote says under speaker sp's card which clip their
// conversations clone, when it is in another language than the track.
func (a *app) dubDialogueRefNote(c *ui.Context, d dubDlg, sp string) {
	p := a.dub
	src := p.SourceLanguage()
	if !d.on || len(d.sts) == 0 || src == "" || dub.DialogueSpeaks([]string{catalog.BaseLanguage(src)}, d.lang) {
		return
	}
	if r := p.Cast[sp]; r != "" && r != dub.VoiceAuto {
		return
	}
	if _, ok := p.References[sp]; !ok {
		return
	}
	t := c.Theme()
	ui.Row(c.Key("ref-dialogue")).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
		caption(c, i18n.T("In conversations: their %s voice speaks %s", dubLangPlain(src), dubLangPlain(d.lang))).TextColor(tok(t).Text3).Grow(1).MinWidth(0)
		infoTip(c, i18n.T("The dialogue model clones them from about 12 s of their own lines and what they say there, and speaks the track's language."))
	})
}

// dubDialoguePick is the Generate step's picker section, nil where the
// switch does not show.
func (a *app) dubDialoguePick(d dubDlg) *pickerDialogue {
	if !d.shown {
		return nil
	}
	p := a.dub
	gb := a.macMemoryGB()
	return &pickerDialogue{
		Models: dubDialogueEntries(),
		Why: func(e catalog.Entry) string {
			return dubDialogueBlocked(gb, p, e, d.lang, d.skipped)
		},
		On:  d.on,
		Cur: d.e,
		Pick: func(e catalog.Entry) {
			repo := e.Repo
			a.dubSettings(func(s *dub.Settings) { s.Dialogue, s.DialogueModel = dub.DialogueAuto, repo })
		},
		Off:      func() { a.dubSettings(func(s *dub.Settings) { s.Dialogue = dub.DialogueOff }) },
		Head:     i18n.T("Conversations (multi-speaker models)"),
		Tip:      i18n.T("A dialogue model records the people of a conversation together, each line hearing the one before it, so answers sound like answers. Other lines, and lines it does not say clearly, stay on the dub's model."),
		OffLabel: i18n.T("Turn multi-speaker dialogue off"),
		OffNote:  i18n.T("Every line on the dub's model"),
		RestHead: i18n.T("Other lines and fallback · Recommended"),
		RestFor:  func(m string) string { return i18n.T("Other lines and fallback: %s", m) },
	}
}

// dubDialogueGenerateNote is the Generate step's line under the model.
func (a *app) dubDialogueGenerateNote(c *ui.Context, d dubDlg) {
	if !d.on {
		return
	}
	caption(c.Key("gen-dialogue"), i18n.T("Multi-speaker dialogue is on: %s records the conversations; other lines, and lines it does not say clearly, are made with this model.", modelRef(d.e))).TextColor(tok(c.Theme()).Text3)
}

// dubStretchColor is conversation n's colour (its lines' bar and chip).
func dubStretchColor(n int) ui.Color {
	cs := []string{"#2a9d8f", "#7c6fd6", "#d08c2f", "#cf5f7a"}
	return ui.Hex(cs[(max(n, 1)-1)%len(cs)])
}

// dubDialogueShown reports whether the line list shows d's stretches:
// with the source shown, or their track.
func (a *app) dubDialogueShown(d dubDlg) bool {
	return d.on && (a.dubLang == "" || a.dubLang == d.lang)
}

// dubDialogueBar marks a row of a conversation with its colour.
func (a *app) dubDialogueBar(c *ui.Context, row ui.Element, id string) {
	d := a.dubDlgNow
	if st, ok := d.byID[id]; ok && a.dubDialogueShown(d) {
		row.BorderStartWidth(px(c.Theme(), 3)).BorderColor(dubStretchColor(st.N))
	}
}

// dubDialogueChip is a row's 「对话 N」 chip: made line by line instead
// when it was (why, as its tooltip); flagged when that line failed the
// check too.
func (a *app) dubDialogueChip(c *ui.Context, s dub.Segment) {
	d := a.dubDlgNow
	st, ok := d.byID[s.ID]
	if !ok || !a.dubDialogueShown(d) {
		return
	}
	r, done := a.dub.Track(d.lang).Lines[s.ID]
	label := i18n.T("Conversation %d", st.N)
	tip := i18n.T("Recorded with the lines around it by %s.", d.e.DisplayName())
	if done && r.Via == dub.ViaFallback && r.Dialogue == st.N {
		label = i18n.T("Conversation %d · line by line", st.N)
		tip = dubFallbackText(r.FallbackReason, entryFor(a.dub.Settings.Model))
	}
	tagChip(c, label).TextColor(dubStretchColor(st.N)).Tooltip(tip)
	if done && r.Flag != "" && r.Dialogue == st.N {
		dubBadge(c, i18n.T("Listen"), "warning")
	}
}

// dubFallbackText says why a conversation line was made line by line.
func dubFallbackText(reason string, e catalog.Entry) string {
	switch reason {
	case services.DialogueHeard:
		return i18n.T("The dialogue model did not say this line clearly, twice: %s made it alone.", e.DisplayName())
	case services.DialogueLength:
		return i18n.T("The dialogue model made this line far too short or long, twice: %s made it alone.", e.DisplayName())
	case services.DialogueUnavailable:
		return i18n.T("The dialogue model was not there: %s made it alone.", e.DisplayName())
	}
	return i18n.T("The dialogue model could not record this conversation: %s made it alone.", e.DisplayName())
}

// dubDialogueFlag is a flagged line's warning under it: a conversation's
// line made line by line, or a line cloned from a clip in another
// language (thread #90), that failed its check.
func (a *app) dubDialogueFlag(c *ui.Context, s dub.Segment) {
	d, lang := a.dubDlgNow, a.dubLang
	if lang == "" && a.dubDialogueShown(d) {
		lang = d.lang
	}
	if a.dub == nil || lang == "" {
		return
	}
	r, ok := a.dub.Track(lang).Lines[s.ID]
	if !ok || r.Flag == "" || r.File == "" {
		return
	}
	var msg string
	switch {
	case r.Via == dub.ViaFallback:
		if !a.dubDialogueShown(d) {
			return
		}
		switch r.Flag {
		case dub.FlagHeard:
			msg = i18n.T("Made line by line, and still not heard clearly (“%s”): listen to it.", r.Heard)
		case dub.FlagLanguage:
			msg = i18n.T("Made line by line, and still heard in another language (“%s”): listen to it.", r.Heard)
		default:
			msg = i18n.T("Made line by line, and still far too short or long for its text: listen to it.")
		}
	case r.Flag == dub.FlagLanguage:
		msg = i18n.T("Heard in another language, even made again (“%s”): listen to it, or cast another voice.", r.Heard)
	default:
		msg = i18n.T("Not heard clearly, even made again (“%s”): listen to it.", r.Heard)
	}
	t := c.Theme()
	ui.Row(c.Key("dlg-flag")).Gap(px(t, 6)).Children(func() {
		iconMark(c, icons.DangerTriangle, t.Warning, "")
		caption(c, msg).TextColor(t.Warning).Grow(1).MinWidth(0)
	})
}

// dubDialogueMenu adds a line's conversation actions to its ⋯ menu.
func (a *app) dubDialogueMenu(m *ui.Menu, s dub.Segment, locked bool, after *func()) {
	d := a.dubDlgNow
	if !a.dubDialogueShown(d) {
		return
	}
	st, in := d.byID[s.ID]
	if !in && !s.NoDialogue {
		return
	}
	m.Separator()
	id := s.ID
	if in {
		_, made := a.dub.Track(d.lang).Lines[id]
		menuDo(m, i18n.T("Record the whole conversation again"), locked || a.dubBusy() || !made, func() {
			*after = func() { a.dubRetake(d.lang, st.IDs[0]) }
		})
	}
	if s.NoDialogue {
		menuDo(m, i18n.T("Back into multi-speaker dialogue"), locked, func() {
			a.dubSetLine(id, "dialogue", func(s *dub.Segment) { s.NoDialogue = false })
		})
		return
	}
	menuDo(m, i18n.T("Not in multi-speaker dialogue"), locked, func() {
		a.dubSetLine(id, "dialogue", func(s *dub.Segment) { s.NoDialogue = true })
	})
}

// dubRetake records conversation first (its first line) of track l
// again with another seed (整段重录), then the dub.
func (a *app) dubRetake(l, first string) {
	p := a.dub
	if p == nil || a.dubBusy() {
		return
	}
	inc := func(q *dub.Project) {
		t := q.Track(l)
		t.Lang = l
		if t.DialogueTakes == nil {
			t.DialogueTakes = map[string]int{}
		}
		t.DialogueTakes[first]++
		q.PutTrack(t)
	}
	inc(p)
	svc, id := a.dubSvc, p.ID
	a.async(func() {
		_, err := svc.Update(context.Background(), id, func(q *dub.Project) error { inc(q); return nil })
		a.post(func() {
			if err != nil {
				a.toast(i18n.T("Could not save the dub: %v", err))
				return
			}
			a.dubRun(services.RunRequest{Through: dub.StageMix, Langs: []string{l}})
		})
	})
}

// dubDialogueCount is the footer's note: how many of the lines are
// recorded in conversations ("" none).
func (a *app) dubDialogueCount() string {
	d := a.dubDialogue()
	n := 0
	for _, st := range d.sts {
		n += len(st.IDs)
	}
	if n == 0 {
		return ""
	}
	return i18n.N(n, "%d line in conversations", "%d lines in conversations", n)
}

// dubDialogueHeard is the file to play for an audition of line id: a
// conversation line already recorded and fitted, as it is ("" none), and
// whether the line is in a conversation (an audition of it on the dub's
// model is not what generating records).
func (a *app) dubDialogueHeard(id, l string) (string, bool) {
	d := a.dubDialogue()
	if _, in := d.byID[id]; !in || l != d.lang {
		return "", false
	}
	s, ok := a.dub.Segment(id)
	r, done := a.dub.Track(l).Lines[id]
	if !ok || !done || r.Via != dub.ViaDialogue || r.Err != "" || r.File == "" || r.Text != a.dub.TextFor(*s, l) || r.Start != s.Start {
		return "", true
	}
	path := a.dubSvc.Path(a.dub.ID, r.File)
	if !fileExists(path) {
		return "", true
	}
	return path, true
}
