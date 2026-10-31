package main

// The stories' 「整场对话」 (whole dialogue) switch, thread #80: a dialogue
// model (catalogue capability "dialogue": SoulX-Podcast, or MOSS-TTSD,
// thread #82) speaks the characters' lines a scene at a time, each line
// hearing the lines before it; narration stays on the story's model
// (services/story_dialogue.go). Shown under Performance; explained and off
// where it cannot run: a Mac with less memory than the model needs, or a
// story in a language it does not speak. Where more than one dialogue
// model can run, a menu (with the switch on) picks one (SoulX-Podcast first: the default);
// models this Mac has too little memory for are not offered.

import (
	"slices"
	"strings"
	"sync"
	"unicode"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/services"
)

// dialogueEntry is the catalogue's first (default) dialogue model.
func dialogueEntry() (catalog.Entry, bool) {
	es := dialogueEntries()
	if len(es) == 0 {
		return catalog.Entry{}, false
	}
	return es[0], true
}

// dialogueEntries are the catalogue's dialogue models, in its order.
func dialogueEntries() []catalog.Entry {
	var out []catalog.Entry
	for _, e := range catalog.Builtin().Entries {
		if e.Has(catalog.CapDialogue) {
			out = append(out, e)
		}
	}
	return out
}

// dialogueModelName is a dialogue model's short name in the API: soulx or
// moss (its catalogue id otherwise).
func dialogueModelName(e catalog.Entry) string {
	switch e.Family {
	case "soulx_podcast":
		return "soulx"
	case services.FamilyMossTTSD:
		return "moss"
	}
	return e.ID
}

// dialogueByName is the dialogue model an API name (soulx, moss, a
// catalogue id or repo) names.
func dialogueByName(name string) (catalog.Entry, bool) {
	for _, e := range dialogueEntries() {
		if strings.EqualFold(name, dialogueModelName(e)) || name == e.ID || name == e.Repo {
			return e, true
		}
	}
	return catalog.Entry{}, false
}

// dialogueChoice is the dialogue model a story uses on a Mac of gb GB
// (0: unknown): its own when it can run here, else the first that can;
// with the models it may pick from, and why none can ("" when one can).
func dialogueChoice(gb int, d *services.LongformDoc) (catalog.Entry, []catalog.Entry, string) {
	all := dialogueEntries()
	if len(all) == 0 {
		return catalog.Entry{}, nil, i18n.T("This version of AudioSmith has no dialogue model.")
	}
	var ok []catalog.Entry
	for _, e := range all {
		if dialogueBlockedOn(gb, d, e) == "" {
			ok = append(ok, e)
		}
	}
	if len(ok) == 0 {
		return all[0], nil, dialogueBlockedOn(gb, d, all[0])
	}
	for _, e := range ok {
		if e.Repo == d.Production.DialogueModel || e.ID == d.Production.DialogueModel {
			return e, ok, ""
		}
	}
	return ok[0], ok, ""
}

// storyLanguage is a story's language: its own, else what its text looks
// like ("zh" with Han, "ja"/"ko" with kana or Hangul, else "en").
func storyLanguage(d *services.LongformDoc) string {
	if d.Language != "" {
		return d.Language
	}
	script := ""
	if d.Kind == core.ProjectAudiobook {
		script = d.Script[:min(len(d.Script), 64<<10)] // a book: how it starts
	}
	// Asked several times a frame (review #102: 75 % of a frame of 2000
	// lines): the answer for the texts last looked at, which keep their
	// memory while kept, so comparing them is quick.
	langMemo.Lock()
	defer langMemo.Unlock()
	m := &langMemo
	if m.ok && m.script == script && len(m.texts) == len(d.Lines) && slices.EqualFunc(d.Lines, m.texts, func(l services.StoryLine, s string) bool { return l.Text == s }) {
		return m.lang
	}
	texts := make([]string, 0, len(d.Lines)+1)
	for _, l := range d.Lines {
		texts = append(texts, l.Text)
	}
	m.texts, m.script, m.ok = slices.Clone(texts), script, true
	if script != "" {
		texts = append(texts, script)
	}
	m.lang = textsLanguage(texts)
	return m.lang
}

// langMemo is storyLanguage's last answer.
var langMemo struct {
	sync.Mutex
	texts  []string
	script string
	lang   string
	ok     bool
}

// textsLanguage is what texts look like: "zh" with Han, "ja"/"ko" with
// kana or Hangul, else "en".
func textsLanguage(texts []string) string {
	han := false
	for _, l := range texts {
		for _, r := range l {
			if r < 0x1100 {
				continue // before Hangul Jamo, the first of the four scripts
			}
			switch {
			case unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r):
				return "ja"
			case unicode.Is(unicode.Hangul, r):
				return "ko"
			case unicode.Is(unicode.Han, r):
				han = true
			}
		}
	}
	if han {
		return "zh"
	}
	return "en"
}

// dialogueBlockedOn says why a story cannot use dialogue model e on a Mac
// of gb GB (0: unknown), "" when it can.
func dialogueBlockedOn(gb int, d *services.LongformDoc, e catalog.Entry) string {
	if gb > 0 && float64(gb) < e.MinMemoryGB {
		return i18n.T("Needs %g GB of memory; this Mac has %d GB.", e.MinMemoryGB, gb)
	}
	lang := strings.ToLower(storyLanguage(d))
	if !slices.ContainsFunc(e.Languages, func(l catalog.Language) bool { return strings.HasPrefix(lang, l.Code) }) {
		if d.Kind == core.ProjectAudiobook {
			return i18n.T("For Chinese and English books.")
		}
		return i18n.T("For Chinese and English stories.")
	}
	return ""
}

// macMemoryGB is this Mac's memory in GB, 0 while it is not known yet (the
// system information loads in the background).
func (a *app) macMemoryGB() int {
	if a.sys == nil {
		a.loadSysInfo()
		return 0
	}
	return a.sys.MemoryGB()
}

// storyDialoguePick is the Stories model picker's whole-dialogue section
// (model_picker_dialogue.go, thread #84), nil for an audiobook or without
// dialogue models. It shares the story's switch and model with the
// Performance section: choosing one there turns it on here, and back.
func (a *app) storyDialoguePick(p *lfPage) *pickerDialogue {
	all := dialogueEntries()
	if len(all) == 0 || p.kind != core.ProjectStory {
		return nil
	}
	d := &p.doc
	gb := a.macMemoryGB()
	e, _, blocked := dialogueChoice(gb, d)
	return &pickerDialogue{
		Models: all,
		Why:    func(x catalog.Entry) string { return dialogueBlockedOn(gb, d, x) },
		On:     d.Production.Dialogue && blocked == "",
		Cur:    e,
		Pick: func(x catalog.Entry) {
			d.Production.Dialogue, d.Production.DialogueModel = true, x.Repo
			p.open["scene"] = true // Performance: the references, the download
		},
		Off: func() { d.Production.Dialogue = false },
	}
}

// storyDialogue is the whole-dialogue row of the Performance section.
func (a *app) storyDialogue(c *ui.Context, p *lfPage, locked bool) {
	if len(dialogueEntries()) == 0 {
		return
	}
	t := c.Theme()
	k := tok(t)
	d := &p.doc
	e, choices, blocked := dialogueChoice(a.macMemoryGB(), d)
	on := d.Production.Dialogue && blocked == ""
	title := i18n.T("Whole dialogue")
	formRow(c, "perf-dialogue-row", title, i18n.T("The characters' lines are spoken by %s a scene at a time, each line hearing the lines before it, so they answer each other; narration stays on the story's model. Each character is cloned from their voice's reference (a cloned voice's clip, a designed voice's reference, else a short sample in that voice). Lines it does not say clearly are made again, then on the story's model. Slower than real time.", modelRef(e)), func() {
		if inkSwitch(c.Key("perf-dialogue"), &on).Label(title).Disabled(locked || blocked != "").Changed() {
			d.Production.Dialogue = on
			if on {
				d.Production.DialogueModel = e.Repo
			}
		}
	})
	if on && len(choices) > 1 {
		// By their friendly names, as the model picker (thread #84).
		fieldInfo(c, i18n.T("Dialogue model"), i18n.T("%s is light (16 GB of memory) and quick; %s is larger (24 GB), up to five voices, and speaks each scene as one recording.", modelRef(choices[0]), modelRef(choices[1])), func() {
			names := make([]string, len(choices))
			for i, x := range choices {
				names[i] = x.DisplayName()
			}
			cur := e.DisplayName()
			if ui.Select(c.Key("perf-dialogue-model"), &cur, names).Label(i18n.T("Dialogue model")).Tooltip(modelTip(e)).Disabled(locked).Changed() {
				if i := slices.Index(names, cur); i >= 0 {
					e = choices[i]
					d.Production.DialogueModel = e.Repo
				}
			}
		})
	}
	if on {
		a.storyDialogueRefs(c, p, locked) // stories_dialogue_refs.go (#83)
	}
	switch m := a.model(e); {
	case blocked != "":
		caption(c.Key("perf-dialogue-why"), blocked).TextColor(k.Text3)
	case on && m.known && m.local.State != hub.Downloaded:
		ui.Row(c.Key("perf-dialogue-need")).Gap(px(t, sp2)).AlignItems(ui.Center).Wrap().Children(func() {
			caption(c, i18n.T("%s is not downloaded (%s).", modelRef(e), catalog.HumanSize(e.Size))).TextColor(k.Text2).Shrink(1)
			if m.downloading {
				ui.Spinner(c)
			} else if secondaryIconButton(c.Key("perf-dialogue-dl"), icons.DownloadMinimalistic, i18n.T("Download")).Label(i18n.T("Download")).Disabled(locked).Clicked() {
				a.download(e)
			}
		})
	}
}

// storyModelSummary is the Setup section's summary: the story's model, or
// with whole dialogue on the dialogue model and the story's.
func (a *app) storyModelSummary(p *lfPage) string {
	name := entryFor(p.doc.Model).DisplayName()
	if dl := a.storyDialoguePick(p); dl != nil && dl.On {
		return dl.Cur.DisplayName() + " + " + name
	}
	return name
}
