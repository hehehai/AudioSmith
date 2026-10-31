package main

// The Dub page's Translate step panel with its glossary and paste
// panels; split from dub_view.go.

import (
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/translate"
)

// dubTranslatePanel: target languages, the translator and its options,
// the glossary and pasting.
func (a *app) dubTranslatePanel(c *ui.Context) {
	t := c.Theme()
	p := a.dub
	locked := a.dubBusy()
	dubPanelTitle(c, i18n.T("Translate"))
	// The primary target.
	pick := func(key, label, cur string, set func(string)) {
		names := make([]string, 0, len(dubTargetLangs)+1)
		ids := slices.Clone(dubTargetLangs)
		if cur != "" && !slices.Contains(ids, cur) {
			ids = append(ids, cur)
		}
		for _, l := range ids {
			names = append(names, dubLangName(l))
		}
		v := names[max(slices.Index(ids, cur), 0)]
		ui.Field(c, label, func() {
			if ui.Select(c.Key(key), &v, names).Label(label).Disabled(locked).Changed() {
				set(ids[slices.Index(names, v)])
			}
		})
	}
	pick("tl-primary", i18n.T("Dub into"), p.Primary(), func(l string) {
		a.dubSet(func(p *dub.Project) {
			if len(p.Targets) == 0 {
				p.Targets = []string{l}
			} else {
				p.Targets[0] = l
				p.Targets = slices.Compact(p.Targets)
			}
			p.ChooseTargets(p.Targets)
		})
		a.dubLang = l
	})
	if note := dubRetargetNote(p); note != "" {
		caption(c, note)
	}
	// Dialects.
	base, _, _ := strings.Cut(p.Primary(), "-")
	if ds, ok := dubDialects[base]; ok {
		opts := append([]string{base}, ds...)
		labels := make([]string, len(opts))
		for i, d := range opts {
			labels[i] = i18n.T("Default")
			if d != base {
				labels[i] = d
			}
		}
		cur := labels[max(slices.Index(opts, p.Primary()), 0)]
		ui.Field(c, i18n.T("Dialect"), func() {
			if ui.Select(c.Key("tl-dialect"), &cur, labels).Label(i18n.T("Dialect")).Disabled(locked).Changed() {
				dd := opts[slices.Index(labels, cur)]
				a.dubSet(func(p *dub.Project) { p.Targets[0] = dd; p.ChooseTargets(p.Targets) })
				a.dubLang = dd
			}
		})
	}
	ok, name := a.dubTranslatorReady(p.Settings)
	if !ok {
		fillCard(c.Key("tl-needs")).Gap(px(t, sp2)).Padding(px(t, sp3)).Children(func() {
			ui.Row(c).Gap(px(t, 6)).Children(func() {
				iconMark(c, icons.DangerTriangle, t.Warning, "")
				caption(c, i18n.T("Translating needs a translation engine: a local model (Ollama, LM Studio) or a provider in Settings → Writing help.")).TextColor(tok(t).Text).Grow(1).MinWidth(0)
			})
			if secondaryButton(c.Key("tl-setup"), i18n.T("Set up writing help")).Clicked() {
				a.navigate("/settings/writing")
			}
		})
	}
	a.dubAdvanced(c, dubStepTranslate, locked, func() {
		// More languages.
		ui.Text(c, i18n.T("More languages"))
		ui.Row(c.Key("tl-more")).Gap(px(t, 4)).Wrap().AlignItems(ui.Center).Children(func() {
			for _, l := range p.Targets[min(1, len(p.Targets)):] {
				ll := l
				ui.Row(c.Key("chip-"+l)).Gap(px(t, 2)).AlignItems(ui.Center).Padding(px(t, 2), px(t, 2), px(t, 2), px(t, sp2)).Radius(px(t, radiusControl)).Background(tok(t).Hover).Children(func() {
					ui.Text(c, dubLangName(ll)).FontSize(px(t, fsCaption)).SingleLine()
					if iconButton(c.Key("x"), icons.Close, i18n.T("Remove %s", dubLangName(ll))).Size(px(t, 22), px(t, 22)).Disabled(locked).Clicked() {
						a.dubSet(func(p *dub.Project) {
							p.ChooseTargets(slices.DeleteFunc(p.Targets, func(x string) bool { return x == ll }))
						})
					}
				})
			}
			dropButton(c.Key("tl-add"), icons.AddCircle, i18n.T("Add")).Shrink(0).Menu(func(m *ui.Menu) {
				for _, l := range dubTargetLangs {
					if slices.Contains(p.Targets, l) {
						continue
					}
					if m.Item(dubLangName(l)).Disabled(locked).Chosen() {
						ll := l
						a.dubSet(func(p *dub.Project) { p.ChooseTargets(append(p.Targets, ll)) })
					}
				}
			})
		})
		// The translator.
		engines := []string{"llm", "deepl", "google", "microsoft"}
		names := []string{i18n.T("Language model"), "DeepL", "Google Cloud Translation", "Microsoft Translator"}
		cur := names[max(slices.Index(engines, p.Settings.Translator), 0)]
		ui.Field(c, i18n.T("Translator"), func() {
			if ui.Select(c.Key("tl-engine"), &cur, names).Label(i18n.T("Translator")).Disabled(locked).Changed() {
				e := engines[slices.Index(names, cur)]
				a.dubSettings(func(s *dub.Settings) { s.Translator = e })
			}
		})
		if ok && name != "" {
			caption(c, i18n.T("Uses %s", name))
		}
		if p.Settings.Translator != "llm" && p.Settings.Translator != "" {
			a.mtKeyRow(c, p.Settings.Translator) // family_keys_view.go
			caption(c, i18n.T("The service's API key is kept in the Keychain as “mt.%s”.", p.Settings.Translator)).TextColor(tok(t).Text3)
		}
		if p.Settings.Translator == "llm" || p.Settings.Translator == "" {
			quals := []translate.Quality{translate.Fast, translate.Autofit, translate.Cinematic}
			qi := max(slices.Index(quals, p.Settings.Quality), 0)
			fieldInfo(c, i18n.T("Quality"), i18n.T("Fast is one literal pass; Autofit rewrites lines that would not fit their time; Cinematic drafts, critiques and adapts for natural phrasing."), func() {
				if segmentedFit(c.Key("tl-quality"), &qi, i18n.T("Fast"), i18n.T("Autofit"), i18n.T("Cinematic")).Label(i18n.T("Quality")).Disabled(locked).Changed() {
					q := quals[qi]
					a.dubSettings(func(s *dub.Settings) { s.Quality = q })
				}
			})
			auto := p.Settings.AutoGlossary
			if dubSwitch(c, "tl-autogloss", i18n.T("Auto glossary"), "", &auto, locked) {
				a.dubSettings(func(s *dub.Settings) { s.AutoGlossary = auto })
			}
		}
		shorter := p.Settings.SuggestShorter
		if dubSwitch(c, "tl-shorter", i18n.T("Suggest shorter lines"), "", &shorter, locked) {
			a.dubSettings(func(s *dub.Settings) { s.SuggestShorter = shorter })
		}
		forms := []translate.Formality{translate.FormalityAuto, translate.FormalityFormal, translate.FormalityInformal}
		fnames := []string{i18n.T("Automatic"), i18n.T("Formal"), i18n.T("Informal")}
		fcur := fnames[max(slices.Index(forms, p.Settings.Formality), 0)]
		ui.Field(c, i18n.T("Register"), func() {
			if ui.Select(c.Key("tl-formality"), &fcur, fnames).Label(i18n.T("Register")).Disabled(locked).Changed() {
				f := forms[slices.Index(fnames, fcur)]
				a.dubSettings(func(s *dub.Settings) { s.Formality = f })
			}
		})
		style := p.Settings.Style
		ui.Field(c, i18n.T("Style notes"), func() {
			if ui.TextArea(c.Key("tl-style"), &style).Lines(2, 6).Placeholder(i18n.T("Tone, audience, how freely to adapt…")).Label(i18n.T("Style notes")).Disabled(locked).Changed() {
				v := []rune(style)
				v = v[:min(len(v), translate.MaxStyle)]
				a.dubSettings(func(s *dub.Settings) { s.Style = string(v) })
			}
		})
	})
}

// --- glossary and paste panels ------------------------------------------------------------

// dubGlossaryPanel is the glossary.
func (a *app) dubGlossaryPanel(c *ui.Context) {
	t := c.Theme()
	p := a.dub
	g := &a.dubGloss
	w, h := c.Size()
	ui.Column(c.Key("dub-gloss")).Gap(px(t, sp2)).Width(min(px(t, 620), w-px(t, 64))).MaxHeight(min(px(t, 520), h-px(t, 64))).Children(func() {
		ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
			sectionHeading(c, i18n.N(len(p.Glossary), "Glossary · %d term", "Glossary · %d terms", len(p.Glossary))).Grow(1)
			if ghostIconButton(c.Key("g-auto"), icons.MagicStick3, i18n.T("Auto")).Label(i18n.T("Auto")).Tooltip(i18n.T("Ask the language model for names and recurring terms")).
				Disabled(g.extracting || len(p.Segments) == 0 || a.llmRouter == nil).Clicked() {
				a.dubExtractTerms()
			}
			if iconButton(c.Key("g-close"), icons.Close, i18n.T("Close")).Clicked() {
				g.open = false
			}
		})
		if len(p.Glossary) == 0 {
			caption(c, i18n.T("No terms yet. Add one below, or Auto."))
		}
		for i, term := range p.Glossary {
			ii := i
			ui.Row(c.Key("term-" + strconv.Itoa(i))).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
				if g.editing == ii {
					ui.TextInput(c.Key("es"), &g.eSrc).Label(i18n.T("Source term")).Grow(1)
					ui.TextInput(c.Key("et"), &g.eTgt).Label(i18n.T("Translation")).Grow(1)
					ui.TextInput(c.Key("en"), &g.eNot).Label(i18n.T("Note")).Grow(1)
					if secondaryButton(c.Key("save"), i18n.T("Save")).Clicked() {
						a.dubSaveTerm(ii)
					}
					if ghostButton(c.Key("cancel"), i18n.T("Cancel")).Clicked() {
						g.editing = -1
					}
					return
				}
				line := term.Source + " → " + term.Target
				if term.Note != "" {
					line += " · " + term.Note
				}
				truncText(c, line).Grow(1)
				if term.Auto {
					tagChip(c, i18n.T("auto"))
				}
				if iconButton(c.Key("edit"), icons.Pen, i18n.T("Edit")).Clicked() {
					g.editing, g.eSrc, g.eTgt, g.eNot = ii, term.Source, term.Target, term.Note
				}
				if iconButton(c.Key("del"), icons.TrashBinMinimalistic, i18n.T("Delete %s", term.Source)).Clicked() {
					a.dubDeleteTerm(ii)
				}
			})
		}
		ui.Row(c.Key("g-add")).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c.Key("src"), &g.src).Placeholder(i18n.T("Source")).Label(i18n.T("Source term")).Grow(1).MinWidth(0)
			ui.TextInput(c.Key("tgt"), &g.tgt).Placeholder(i18n.T("Translation")).Label(i18n.T("Translation")).Grow(1).MinWidth(0)
			ui.TextInput(c.Key("note"), &g.note).Placeholder(i18n.T("Note (optional)")).Label(i18n.T("Note")).Grow(1).MinWidth(0)
			if primaryButton(c.Key("add"), i18n.T("Add term")).Disabled(strings.TrimSpace(g.src) == "" || strings.TrimSpace(g.tgt) == "").Clicked() {
				a.dubAddTerm()
			}
		})
		caption(c, i18n.T("Terms go into every translation; yours win over auto ones.")).TextColor(tok(t).Text3)
	})
}

// dubPastePanel pastes a translation.
func (a *app) dubPastePanel(c *ui.Context) {
	t := c.Theme()
	ps := &a.dubPaste
	w, _ := c.Size()
	ui.Column(c.Key("dub-paste")).Gap(px(t, sp2)).Width(min(px(t, 620), w-px(t, 64))).Children(func() {
		ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
			sectionHeading(c, i18n.T("Paste translation · %s", dubLangName(a.dubLang))).Grow(1)
			if ghostIconButton(c.Key("p-file"), icons.Import, i18n.T("Load .srt / .vtt…")).Label(i18n.T("Load .srt / .vtt…")).Clicked() {
				a.dubPasteFile()
			}
			if iconButton(c.Key("p-close"), icons.Close, i18n.T("Close")).Clicked() {
				ps.open = false
			}
		})
		box := ui.TextArea(c.Key("p-text"), &ps.text).Lines(3, 10).Label(i18n.T("Translation to paste")).
			Placeholder(i18n.T("Paste subtitles (SRT/VTT), numbered lines (1. …), or one line per segment."))
		if files := box.DroppedFiles(); len(files) > 0 {
			path := files[0]
			a.background(func() {
				data, err := readFileText(path)
				a.post(func() {
					if err != nil {
						ps.err = err.Error()
						return
					}
					ps.text = data
				})
			})
		}
		a.dubPastePreview()
		if strings.TrimSpace(ps.text) == "" {
			return
		}
		pv := ps.preview
		mode := map[dub.PasteMode]string{dub.PasteTimed: i18n.T("Timestamped"), dub.PasteNumbered: i18n.T("Numbered lines"), dub.PastePlain: i18n.T("Plain lines")}[pv.Mode]
		ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Wrap().Children(func() {
			tagChip(c, mode)
			ui.Text(c, i18n.T("%d of %d segments matched", pv.Matched, len(pv.Rows))).FontSize(px(t, fsCaption))
			if pv.Unused > 0 {
				iconMark(c, icons.DangerTriangle, t.Warning, "")
				caption(c, i18n.N(pv.Unused, "%d pasted line went unused", "%d pasted lines went unused", pv.Unused)).TextColor(t.Warning)
			}
		})
		shown := 0
		for _, r := range pv.Rows {
			if shown >= 8 {
				break
			}
			if r.After == "" {
				continue
			}
			shown++
			caption(c.Key("pv-"+r.ID), firstOf(r.Before, "—")+"  →  "+r.After).MaxLines(2).Ellipsis("…")
		}
		if pv.Matched > shown {
			caption(c, i18n.N(pv.Matched-shown, "+ %d more", "+ %d more", pv.Matched-shown)).TextColor(tok(t).Text3)
		}
		if ps.err != "" {
			caption(c, ps.err).TextColor(t.Danger)
		}
		ui.Row(c).Justify(ui.End).Children(func() {
			if primaryButton(c.Key("p-apply"), i18n.N(pv.Matched, "Apply to %d segment", "Apply to %d segments", pv.Matched)).Disabled(pv.Matched == 0 || a.dubLocked()).Clicked() {
				a.dubApplyPaste()
			}
		})
	})
}
