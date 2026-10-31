package main

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/lang"
)

// shownVoices are the library's voices that match the page's search and
// kind filter, by name. The search is in memory: names, styles,
// transcripts, descriptions and tags.
func (a *app) shownVoices() []core.Voice {
	q := strings.ToLower(strings.TrimSpace(a.voiceQuery))
	var out []core.Voice
	for _, v := range a.voices {
		switch a.voiceKind {
		case 1:
			if v.Kind != core.VoiceCloned {
				continue
			}
		case 2:
			if v.Kind != core.VoiceDesigned {
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

// voicesPage is /voices: the saved voices, and the profile editor of the
// one chosen (beside the list, or a drawer over it when the window is
// narrow).
func (a *app) voicesPage(c *ui.Context) {
	t := c.Theme()
	pageHeaderBar(c, pageHead{
		title:  i18n.T("My voices"),
		count:  i18n.N(len(a.voices), "%d voice", "%d voices", len(a.voices)),
		search: &headSearch{key: "voices-search", value: &a.voiceQuery, placeholder: i18n.T("Search voices")},
		tools: func(hn bool) {
			if !hn {
				ui.Segmented(c.Key("voices-kind"), &a.voiceKind, i18n.T("All"), i18n.T("Cloned"), i18n.T("Designed")).Label(i18n.T("Kind")).Shrink(0)
			}
			if iconButton(c.Key("voices-import"), icons.Import, i18n.T("Import…")).Tooltip(i18n.T("Import a .asvoice or .ovsvoice file")).Clicked() {
				a.chooseVoiceFile()
			}
		},
		more: func(m *ui.Menu) {
			m.Submenu(i18n.T("Kind"), func(m *ui.Menu) {
				for i, l := range []string{i18n.T("All"), i18n.T("Cloned"), i18n.T("Designed")} {
					if m.Item(l).Checked(a.voiceKind == i).Chosen() {
						a.voiceKind = i
					}
				}
			})
		},
		primary: a.newVoiceButton(c),
	})
	w := px(t, 360)
	inline := a.panelFits(c, w)
	body := ui.Row(c.Key("voices-body")).Grow(1).MinHeight(0).AlignItems(ui.Stretch)
	if files := body.DroppedFiles(); files != nil {
		a.voicesDropped(files)
	}
	body.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Box(c).Height(px(t, sp2)).Shrink(0)
			a.voicesGrid(c)
		})
		if a.edit != nil {
			sidePanel(c, "voice-editor", w, inline, i18n.T("Edit voice profile"), func() { a.edit = nil }, func() {
				a.voiceEditor(c)
			})
		}
	})
	a.voiceDeleteAlert(c)
	a.personaDialogs(c)
}

func (a *app) voicesGrid(c *ui.Context) {
	t := c.Theme()
	vs := a.shownVoices()
	if len(vs) == 0 {
		switch {
		case !a.voicesLoaded:
			ui.Column(c).Grow(1).Center().Children(func() { caption(c, i18n.T("Loading…")) })
		case len(a.voices) > 0:
			emptyState(c, icons.Magnifer, i18n.T("No matches"), "", nil)
		default:
			emptyState(c, icons.UserSpeakRounded, i18n.T("No saved voices yet"),
				i18n.T("Upload or record a clip and save it as a voice, design one from a description, or pick one in the gallery."), func() {
					if ui.Button(c.Key("empty-gallery"), i18n.T("Discover voices")).Clicked() {
						a.navigate("/gallery")
					}
					if ui.Button(c.Key("empty-design"), i18n.T("Design a voice")).Clicked() {
						a.navigate("/design")
					}
				})
		}
		return
	}
	ui.GridView(c.Key("voices-grid"), &a.voiceGrid, len(vs), px(t, 240), px(t, 68), func(i int) {
		v := vs[i]
		a.voiceCard(c, &v)
	}).Grow(1).Padding(0, px(t, sp6), px(t, sp4))
}

// voiceCard is one saved voice: a click opens its editor; Preview and
// the ⋯ menu show while the pointer is on it (or it is chosen, or plays).
func (a *app) voiceCard(c *ui.Context, v *core.Voice) {
	t := c.Theme()
	k := tok(t)
	selected := a.edit != nil && a.edit.id == v.ID
	card := ui.Row(c.Key("voice-"+v.ID)).Grow(1).Gap(px(t, sp2)).AlignItems(ui.Center).Padding(px(t, sp2), px(t, sp2), px(t, sp2), px(t, sp3)).
		Radius(px(t, radiusCard)).Border(1, k.Line)
	hovered := card.Hovered()
	// The ⋯ menu stays built while it has the focus (a menu button takes
	// it as it opens): the native menu takes the pointer, so the card is
	// no longer hovered in the frame that reports the item chosen, and a
	// menu not built then loses the choice (#108: Delete… did nothing).
	actions := hovered || selected || card.FocusWithin()
	switch {
	case selected:
		card.Background(k.Pressed).Border(1, k.Pressed)
	case hovered:
		card.Background(k.Subtle).Border(1, k.G[4])
	}
	card.Children(func() {
		open := ui.ButtonBase(c.Key("open")).Grow(1).MinWidth(0).Label(v.Name)
		if open.Clicked() {
			a.openVoiceEditor(v.ID)
		}
		open.Children(func() {
			ui.Row(c).Gap(px(t, sp3)).AlignItems(ui.Center).Grow(1).MinWidth(0).Children(func() {
				ui.Avatar(c, v.Name, a.portrait(v))
				ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 2)).Children(func() {
					ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
						ui.Text(c, v.Name).FontWeight(600).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0).Tooltip(v.Name)
						voiceMarks(c, v)
					})
					truncText(c, voiceMeta(v)).FontSize(px(t, fsCaption)).TextColor(k.Text2)
				})
			})
		})
		playing := a.voicePlaying(v)
		if j := a.voiceJob("sample", v.ID); j.running() {
			ui.Spinner(c).Label(i18n.T("Rendering the sample…"))
		} else if a.refMaking(v.ID) != nil {
			ui.Spinner(c).Label(i18n.T("Making a reference…")).Tooltip(i18n.T("Making a reference…"))
		} else if hovered || playing || selected {
			label, ic := i18n.T("Preview"), icons.Play
			if playing {
				label, ic = i18n.T("Stop"), icons.Stop
			}
			if iconButton(c.Key("play"), ic, label).Clicked() {
				a.playVoice(v)
			}
		}
		if actions {
			id := v.ID
			moreMenu(c.Key("voice-more"), "", func(m *ui.Menu) {
				if m.Item(i18n.T("Use in Studio")).Chosen() {
					if v := a.findVoice(id); v != nil {
						a.useVoiceInStudio(v)
					}
				}
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
	})
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
// marks (the editor).
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

// voiceMeta is a card's second line: kind, language, date.
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

// openVoiceEditor opens the voice library with v's editor.
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
		a.voiceQuery, a.voiceKind = "", 0
	}
	a.navigate("/voices")
}
