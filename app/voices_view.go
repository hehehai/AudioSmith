package main

// 音色 · 我的 (/voices/mine, 0.5 F4; after ElevenLabs' My Voices): the
// saved voices as a table — name, kind, language, created, how often
// used, ▶, 「使用」, ⋯ (rename, duplicate, export .asvoice, delete with
// Undo) — filtered by type (全部/克隆/设计/收藏: saved from Discover) and
// the header's search. A click on a row opens its detail
// (voice_editor.go) beside the table, or over it when narrow. Ghost rows
// and 「新建音色」 when there is none.

import (
	"context"
	"os"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/lang"
)

// The type filter's values (voicesState.voiceKind).
const (
	kindAll = iota
	kindCloned
	kindDesigned
	kindSaved // saved from Discover
)

// voiceKinds are the type filter's labels, in order.
func voiceKinds() []string {
	return []string{i18n.T("All"), i18n.T("Cloned"), i18n.T("Designed"), i18n.X("voice type", "Saved")}
}

// fromDiscover reports whether v was saved from Discover.
func fromDiscover(v *core.Voice) bool { return strings.HasPrefix(v.Origin, "gallery:") }

// shownVoices are the library's voices that match the page's search and
// type filter, by name. The search is in memory: names, styles,
// transcripts, descriptions and tags.
func (a *app) shownVoices() []core.Voice {
	q := strings.ToLower(strings.TrimSpace(a.voiceQuery))
	var out []core.Voice
	for _, v := range a.voices {
		switch a.voiceKind {
		case kindCloned:
			if v.Kind != core.VoiceCloned {
				continue
			}
		case kindDesigned:
			if v.Kind != core.VoiceDesigned || fromDiscover(&v) {
				continue
			}
		case kindSaved:
			if !fromDiscover(&v) {
				continue
			}
		}
		if q != "" && !voiceMatches(&v, q) {
			continue
		}
		out = append(out, v)
	}
	return out
}

func voiceMatches(v *core.Voice, q string) bool {
	for _, s := range append([]string{v.Name, v.Style, v.RefText, v.Design, v.Language}, v.Tags...) {
		if strings.Contains(strings.ToLower(s), q) {
			return true
		}
	}
	return false
}

// voicesPage is /voices/mine.
func (a *app) voicesPage(c *ui.Context) {
	t := c.Theme()
	pageHeaderBar(c, pageHead{
		title:  i18n.T("My voices"),
		search: &headSearch{key: "voices-search", value: &a.voiceQuery, placeholder: i18n.T("Search voices")},
		tools: func(hn bool) {
			if !hn {
				ui.Segmented(c.Key("voices-kind"), &a.voiceKind, voiceKinds()...).Label(i18n.T("Kind")).Shrink(0)
			}
			if iconButton(c.Key("voices-import"), icons.Import, i18n.T("Import…")).Tooltip(i18n.T("Import a .asvoice or .ovsvoice file")).Clicked() {
				a.chooseVoiceFile()
			}
		},
		more: func(m *ui.Menu) {
			m.Submenu(i18n.T("Kind"), func(m *ui.Menu) {
				for i, l := range voiceKinds() {
					if m.Item(l).Checked(a.voiceKind == i).Chosen() {
						a.voiceKind = i
					}
				}
			})
		},
		primary: a.newVoiceButton(c),
	})
	a.loadVoiceUses()
	w := px(t, voiceDetailW)
	inline := a.panelFits(c, w)
	body := ui.Row(c.Key("voices-body")).Grow(1).MinHeight(0).AlignItems(ui.Stretch)
	if files := body.DroppedFiles(); files != nil {
		a.voicesDropped(files)
	}
	body.Children(func() {
		narrow := a.pageWidth(c)-ifThen(a.edit != nil && inline, w, 0) < px(t, 720)
		ui.Scroll(c.Key("voices-scroll")).Grow(1).MinWidth(0).Children(func() {
			ui.Column(c).Padding(px(t, sp2), px(t, sp6), px(t, sp6)).Gap(px(t, sp2)).Children(func() {
				a.voicesTable(c, narrow)
			})
		})
		if a.edit != nil {
			sidePanel(c, "voice-editor", w, inline, i18n.T("Voice details"), func() { a.edit = nil }, func() {
				a.voiceEditor(c)
			})
		}
	})
	a.voiceDeleteAlert(c)
	a.personaDialogs(c)
}

// voiceDateW is the Created column's width: a whole Chinese date
// ("2026年10月31日") fits after its icon (the Mac check, #121: 92 cut it
// to "2026年10月3…").
const voiceDateW = 126

// voicesTable is the saved voices' table, or its empty states.
func (a *app) voicesTable(c *ui.Context, narrow bool) {
	t := c.Theme()
	vs := a.shownVoices()
	if len(vs) == 0 {
		switch {
		case !a.voicesLoaded:
			ui.Column(c).Grow(1).Center().Padding(px(t, sp6)).Children(func() { caption(c, i18n.T("Loading…")) })
		case len(a.voices) > 0:
			ghostRows(c, 3, icons.Magnifer, i18n.T("No matches"), "")
		default:
			ghostRows(c, 4, icons.UserSpeakRounded, i18n.T("No saved voices yet"),
				i18n.T("Clone a voice from a recording, design one from a description, or save one from Discover."))
			ui.Row(c.Key("voices-empty-acts")).Gap(px(t, sp2)).Justify(ui.Center).Children(func() {
				if ui.Button(c.Key("empty-new"), i18n.T("New voice")).Clicked() {
					a.navigate("/voices/new")
				}
				if ghostButton(c.Key("empty-gallery"), i18n.T("Discover voices")+" ›").Clicked() {
					a.navigate("/voices/discover")
				}
			})
		}
		return
	}
	for i := range vs {
		v := &vs[i]
		a.voiceTableRow(c, v, narrow)
	}
}

// voiceTableRow is one saved voice.
func (a *app) voiceTableRow(c *ui.Context, v *core.Voice, narrow bool) {
	e := a.edit
	if a.mine.renaming == v.ID {
		a.renameRow(c, v)
		return
	}
	playing := a.savedPlaying(v)
	st := rowState{selected: e != nil && e.id == v.ID, playing: playing}
	id := v.ID
	if voiceRow(c, "voice-"+v.ID, v.Name, st, func() {
		voiceNameCell(c, v.Name, voiceLine(v), a.portrait(v), playing, func() { voiceMarks(c, v) })
		metaCell(c, icons.Tag, i18n.T("Kind"), mineKind(v), 92)
		if !narrow {
			lang := ""
			if v.Language != "" {
				lang = langName(v.Language) // with its flag, as Discover's
			}
			metaCell(c, noIcon, i18n.T("Language"), lang, 100)
			metaCell(c, icons.CalendarMinimalistic, i18n.T("Created"), i18n.Date(v.CreatedAt), voiceDateW)
			uses := ""
			if n := a.mine.uses[v.ID]; n > 0 {
				uses = i18n.N(n, "%d time", "%d times", n)
			}
			metaCell(c, icons.Repeat, i18n.T("Times used"), uses, 80)
		}
	}, func() {
		switch {
		case a.voiceJob("sample", v.ID).running():
			ui.Spinner(c).Label(i18n.T("Rendering the sample…"))
		case a.refMaking(v.ID) != nil:
			ui.Spinner(c).Label(i18n.T("Making a reference…")).Tooltip(i18n.T("Making a reference…"))
		default:
			if previewToggle(c, "play", playing).Clicked() {
				a.previewSaved(v)
			}
		}
		if useButton(c, "use", i18n.T("Use it in Text to speech")).Clicked() {
			if v := a.findVoice(id); v != nil {
				a.useVoiceInStudio(v)
			}
		}
		a.voiceRowMenu(c, v)
	}) {
		a.toggleVoiceDetail(id)
	}
}

// voiceRowMenu is a saved voice's ⋯: rename, duplicate, export, Stories
// and Audiobook, references, delete.
func (a *app) voiceRowMenu(c *ui.Context, v *core.Voice) {
	id := v.ID
	moreMenu(c.Key("voice-more"), "", func(m *ui.Menu) {
		if m.Item(i18n.T("Rename")).Chosen() {
			a.mine.renaming, a.mine.renameText = id, v.Name
		}
		if m.Item(i18n.T("Duplicate")).Chosen() {
			a.duplicateVoice(id)
		}
		if m.Item(i18n.T("Export voice file…")).Chosen() {
			a.exportVoice(id, v.Kind == core.VoiceCloned, "")
		}
		m.Separator()
		a.longformVoiceItems(m, func(then func(*core.Voice)) {
			if v := a.findVoice(id); v != nil {
				then(v)
			}
		})
		if canMakeReference(v) && v.RefAudio == "" && m.Item(i18n.T("Make reference")).Disabled(a.refMaking(id) != nil).Chosen() {
			if v := a.findVoice(id); v != nil {
				a.makeReference(v, nil)
			}
		}
		if canMakePortable(v) && m.Item(i18n.T("Make a portable copy (approximate)")).Chosen() {
			if v := a.findVoice(id); v != nil {
				a.makePortable(v)
			}
		}
		m.Separator()
		if m.Item(i18n.T("Delete…")).Chosen() {
			a.askDeleteVoice(id)
		}
	})
}

// renameRow is a row being renamed: the name's field, Save, Cancel.
func (a *app) renameRow(c *ui.Context, v *core.Voice) {
	t := c.Theme()
	s := &a.mine
	listEditRow(c, "rename-"+v.ID, func() {
		ui.Avatar(c, v.Name, a.portrait(v)).Size(px(t, listThumbD), px(t, listThumbD)).Shrink(0)
	}, func() {
		in := ui.TextInput(c.Key("rename"), &s.renameText).Label(i18n.T("Profile name")).Grow(1).MinWidth(0).AutoFocus()
		ok := strings.TrimSpace(s.renameText) != ""
		if (in.Submitted() || ui.Button(c.Key("rename-ok"), i18n.T("Save name")).Disabled(!ok).Clicked()) && ok {
			a.renameVoice(v.ID, s.renameText)
			s.renaming = ""
		}
		if ghostButton(c.Key("rename-cancel"), i18n.T("Cancel")).Clicked() || in.Shortcut(0, ui.KeyEscape) {
			s.renaming = ""
		}
	})
}

// toggleVoiceDetail opens a saved voice's detail, or closes it (a second
// click).
func (a *app) toggleVoiceDetail(id string) {
	if a.edit != nil && a.edit.id == id {
		a.edit = nil
		return
	}
	a.openVoiceEditor(id)
}

// mineState is My voices' view state: the row being renamed, how often
// each voice was used (by id) and what that was read for.
type mineState struct {
	renaming, renameText string
	uses                 map[string]int
	usesFor              string
}

// loadVoiceUses reads how often each voice was used, when the voices
// changed since the last read (or the page was opened again).
func (a *app) loadVoiceUses() {
	ids := make([]string, len(a.voices))
	for i, v := range a.voices {
		ids[i] = v.ID + "@" + v.UpdatedAt.String()
	}
	key := strings.Join(ids, ",")
	if key == a.mine.usesFor {
		return
	}
	a.mine.usesFor = key
	svc := a.lib.svc.Voices
	list := slices.Clone(a.voices)
	a.background(func() {
		uses := map[string]int{}
		for _, v := range list {
			if u, err := svc.Usage(context.Background(), v.ID); err == nil {
				uses[v.ID] = u.Takes + u.Segments
			}
		}
		a.post(func() {
			if a.mine.usesFor == key {
				a.mine.uses = uses
			}
		})
	})
}

// renameVoice gives a saved voice another name.
func (a *app) renameVoice(id, name string) {
	v := a.findVoice(id)
	name = strings.TrimSpace(name)
	if v == nil || name == "" || name == v.Name {
		return
	}
	cur := *v
	cur.Name = name
	if a.edit != nil && a.edit.id == id {
		a.edit.name = name
	}
	a.voiceAction(func(ctx context.Context, svc core.VoiceService) (core.Voice, error) {
		return svc.Save(ctx, cur)
	}, i18n.T("Renamed"))
}

// duplicateVoice saves a copy of a voice, with its reference, sample
// and portrait (not its lock, ownership or cached prompts), as “Name
// (copy)”.
func (a *app) duplicateVoice(id string) {
	v := a.findVoice(id)
	if v == nil {
		return
	}
	cp := *v
	cp.ID, cp.Name, cp.Origin = "", i18n.T("%s (copy)", v.Name), ""
	cp.Locked, cp.LockTake, cp.Consent, cp.Prompts = false, "", core.VoiceConsent{}, nil
	cp.RefAudio, cp.Sample, cp.Portrait, cp.RefOriginal = "", "", "", ""
	var files core.VoiceFiles
	if v.RefAudio != "" {
		files.Reference = a.voicePath(v, v.RefAudio)
	}
	if v.RefOriginal != "" {
		files.Original = a.voicePath(v, v.RefOriginal)
	}
	if v.Sample != "" {
		files.Sample = a.voicePath(v, v.Sample)
	}
	portrait := ""
	if v.Portrait != "" {
		portrait = a.voicePath(v, v.Portrait)
	}
	svc := a.lib.svc.Voices
	a.background(func() {
		if portrait != "" {
			files.Portrait, _ = os.ReadFile(portrait)
		}
		saved, err := svc.SaveFiles(context.Background(), cp, files)
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not duplicate the voice: %v", err))
				return
			}
			a.voiceChanged(core.VoiceChanged{Voice: saved})
			a.toast(i18n.T("Saved “%s” to your voices", saved.Name))
		})
	})
}

// mineKind is a saved voice's kind, short: 克隆, 设计, 内置, 收藏 (saved
// from Discover).
func mineKind(v *core.Voice) string {
	switch {
	case fromDiscover(v):
		return i18n.X("voice type", "Saved")
	case isPortable(v):
		return i18n.T("Portable")
	case v.Kind == core.VoiceCloned:
		return i18n.T("Cloned")
	case v.Kind == core.VoiceDesigned:
		return i18n.T("Designed")
	}
	return i18n.T("Built-in")
}

// voiceLine is a row's grey line: the voice's style, description or
// transcript.
func voiceLine(v *core.Voice) string {
	return strings.Join(strings.Fields(firstOf(v.Style, v.Design, v.RefText, strings.Join(v.Tags, ", "))), " ")
}

// voiceMarks are a voice's Verified, Consent included and Locked marks:
// icons whose tooltips name them.
func voiceMarks(c *ui.Context, v *core.Voice) {
	t := c.Theme()
	k := tok(t)
	if v.Verified() {
		iconMark(c, icons.VerifiedCheck, t.Success, i18n.T("Verified"))
	} else if v.Consent.State == core.ConsentIncluded {
		iconMark(c, icons.ShieldCheck, k.Text3, i18n.T("Consent included")+": "+i18n.T("The imported file says the speaker consented. It is not verified here."))
	}
	if v.Locked {
		iconMark(c, icons.LockKeyhole, k.Text2, i18n.T("Locked")+": "+i18n.T("The voice's identity is locked to a take and a seed"))
	}
}

// voiceBadges are a voice's Verified and Locked badges, as short status
// marks (the detail).
func voiceBadges(c *ui.Context, v *core.Voice) {
	t := c.Theme()
	k := tok(t)
	if v.Verified() {
		statusMark(c, icons.VerifiedCheck, t.Success, i18n.T("Verified"))
	} else if v.Consent.State == core.ConsentIncluded {
		statusMark(c, icons.ShieldCheck, k.Text2, i18n.T("Consent included")).Tooltip(i18n.T("The imported file says the speaker consented. It is not verified here."))
	}
	if v.Locked {
		statusMark(c, icons.LockKeyhole, k.Text2, i18n.T("Locked")).Tooltip(i18n.T("The voice's identity is locked to a take and a seed"))
	}
}

// voiceMeta is a voice's kind, language and date.
func voiceMeta(v *core.Voice) string {
	parts := []string{voiceKindLabel(v)}
	if v.Language != "" {
		parts = append(parts, languageName(v.Language))
	}
	parts = append(parts, i18n.Date(v.CreatedAt))
	return strings.Join(parts, " · ")
}

// languageName names a BCP-47 code in the UI's language when the table
// knows it.
func languageName(code string) string {
	if l, ok := lang.Lookup(code); ok {
		return l.NameIn(i18n.Locale())
	}
	return code
}

// voiceLanguages are the language codes a voice may say it speaks.
func voiceLanguages() []string {
	return []string{"en", "zh", "ja", "ko", "es", "fr", "de", "it", "pt", "ru", "hi", "ar", "nl", "pl", "tr", "vi", "id", "th", "yue"}
}

// voicesDropped imports dropped voice files; a dropped clip starts a new
// voice from it.
func (a *app) voicesDropped(paths []string) {
	for _, p := range paths {
		if isVoiceFile(p) {
			a.importVoice(p)
		}
	}
	for _, p := range paths {
		if !isVoiceFile(p) && !isPortraitPath(p) {
			a.startClone("")
			a.cloneLoad(p, baseName(p), false)
			return
		}
	}
}

// openVoiceEditor opens My voices with v's detail.
func (a *app) openVoiceEditor(id string) {
	v := a.findVoice(id)
	if v == nil {
		return
	}
	if a.edit == nil || a.edit.id != id {
		a.edit = newVoiceEdit(*v)
		a.loadUsage(id)
	}
	if i := slices.IndexFunc(a.shownVoices(), func(x core.Voice) bool { return x.ID == id }); i < 0 {
		a.voiceQuery, a.voiceKind = "", kindAll
	}
	a.navigate("/voices/mine")
}
