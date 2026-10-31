package main

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/gallery"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
)

// designPage is /design.
func (a *app) designPage(c *ui.Context) {
	t := c.Theme()
	d := &a.design
	d.reparse()
	a.syncLinked()
	title := i18n.T("Design")
	if v := a.findVoice(d.linked); v != nil {
		title = i18n.T("Design · %s", v.Name)
	}
	pageHeaderBar(c, pageHead{title: title, parent: i18n.T("New voice"), up: "/voices/new", tools: func(bool) {
		if ghostIconButton(c.Key("design-new"), icons.AddCircle, i18n.T("New design")).Label(i18n.T("New design")).Clicked() {
			script := d.script
			d.reset()
			d.script = script
		}
		if iconButton(c.Key("design-gallery"), icons.Widget, i18n.T("Discover voices")).Tooltip(i18n.T("Start from a voice in Discover voices")).Clicked() {
			a.navigate("/gallery")
		}
	}})
	k := tok(t)
	recipe := func() {
		a.designDescribe(c)
		a.designPresets(c)
		a.designDetails(c) // the seed is inside
		a.designSavedVoices(c)
	}
	main := func() {
		a.designTarget(c)
		a.designScript(c)
		a.designSaveForm(c)
	}
	if a.pageWidth(c) < px(t, 380+480) {
		// Too narrow for two columns: one, the recipe first.
		ui.Scroll(c.Key("design-main")).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).Padding(px(t, sp1), px(t, sp6), px(t, sp6)).Gap(px(t, sp4)).Children(func() {
				recipe()
				ui.Box(c).Height(1).Background(k.Line)
				main()
			})
		})
		return
	}
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Scroll(c.Key("design-recipe")).Width(px(t, 380)).Shrink(0).Children(func() {
			ui.Column(c).Padding(px(t, sp1), px(t, sp5), px(t, sp5), px(t, sp6)).Gap(px(t, sp4)).Children(recipe)
		})
		ui.Box(c).Width(1).Background(k.Line)
		ui.Scroll(c.Key("design-main")).Grow(1).MinWidth(0).Children(func() {
			ui.Column(c).Padding(px(t, sp1), px(t, sp6), px(t, sp6), px(t, sp5)).Gap(px(t, sp4)).MaxWidth(px(t, 720)).Children(main)
		})
	})
}

// designDescribe is the description box and what the parser made of it.
func (a *app) designDescribe(c *ui.Context) {
	t := c.Theme()
	d := &a.design
	loc := i18n.Locale()
	k := tok(t)
	sectionHeading(c, i18n.T("Describe the voice"))
	ui.TextArea(c.Key("design-description"), &d.description).Lines(3, 8).
		Placeholder(i18n.T("e.g. a warm elderly British storyteller, slightly raspy")).Label(i18n.T("Describe the voice"))
	d.reparse()
	res := d.parsed
	switch {
	case strings.TrimSpace(d.description) == "":
		caption(c, i18n.T("Describe the voice, or pick a starting point. Either fills in the details below."))
	case len(res.Matched) == 0:
		caption(c, i18n.T("No voice traits recognised yet: try words like deep, elderly or British."))
	default:
		ui.Row(c.Key("design-matched")).Gap(px(t, sp1)).Wrap().Children(func() {
			for i, m := range res.Matched {
				info, _ := vd.Info(m.Category)
				op, _ := vd.Lookup(m.Category, m.Value)
				ch := tagChip(c.Key("m"+itoa(i)), m.Phrase+" → "+info.Label.In(loc)+": "+op.Label.In(loc))
				if !m.Applied {
					ch.Opacity(0.5).Tooltip(matchNote(m.Note))
				} else {
					ch.TextColor(k.AccentInk).Background(k.AccentSoft)
				}
			}
		})
	}
	if len(res.Unmatched) > 0 {
		caption(c, i18n.T("Couldn't map: %s", res.UnmatchedText()))
	}
	if e, ok := voiceCatalog().ByLabel(d.model); ok {
		if eng, ok := vd.EngineFor(e.Family, e.Repo); ok && eng.Vocabulary == vd.VocabFreeform && d.description != "" {
			caption(c, i18n.T("This model reads your description as written; the details below are extra cues."))
		}
	}
	if d.description != "" || !d.attrs.IsAuto() {
		ui.Row(c).Children(func() {
			if ghostIconButton(c.Key("design-reset"), icons.Restart, i18n.T("Reset to description")).Label(i18n.T("Reset to description")).
				Tooltip(i18n.T("Drop your picks: the description alone decides")).Clicked() {
				d.attrs = d.parsed.Attrs.Canonical()
			}
		})
	}
}

// matchNote explains a phrase the parser did not apply.
func matchNote(note string) string {
	switch note {
	case "negated":
		return i18n.T("Negated")
	case "overridden":
		return i18n.T("Overridden by a later phrase")
	case "default":
		return i18n.T("A weak hint that a clearer phrase replaced")
	}
	return ""
}

// designPresets are the starting points.
func (a *app) designPresets(c *ui.Context) {
	t := c.Theme()
	d := &a.design
	loc := i18n.Locale()
	sectionTitle(c, i18n.T("Starting points"))
	ui.Row(c.Key("design-presets")).Gap(px(t, sp1)).Wrap().Children(func() {
		for _, p := range gallery.Presets() {
			on := d.preset == p.ID && d.attrs.Summary("en") == p.Attrs.Summary("en")
			b := chip(c.Key("preset-"+p.ID), p.Name.In(loc), "", on).Tooltip(p.Description.In(loc))
			if !on && !b.Hovered() {
				b.Background(tok(t).Hover)
			}
			if b.Clicked() {
				if on = !on; on {
					a.applyPreset(p)
				} else {
					d.preset, d.attrs = "", vd.Attributes{}
				}
			}
		}
	})
}

// designDetails are the attribute pickers.
func (a *app) designDetails(c *ui.Context) {
	t := c.Theme()
	d := &a.design
	loc := i18n.Locale()
	summary := d.attrs.Summary(loc)
	collapsible(c.Key("design-details"), i18n.T("Details"), &d.detailsOpen, func() {
		if !d.detailsOpen {
			truncText(c, summary).FontSize(px(t, fsCaption)).TextColor(tok(t).Text2).MaxWidth(px(t, 220))
		}
	}, func() {
		ui.Column(c).Gap(px(t, sp2)).Children(func() {
			caption(c, summary)
			for _, cat := range vd.Categories {
				info, _ := vd.Info(cat)
				if cat == vd.Style {
					ui.Text(c, info.Label.In(loc)).FontSize(px(t, fsCaption))
					ui.Row(c.Key("styles")).Gap(px(t, sp1)).Wrap().Children(func() {
						for _, op := range info.Options {
							on := d.attrs.HasStyle(op.ID)
							if chip(c.Key("style-"+op.ID), op.Label.In(loc), "", on).Clicked() {
								if on = !on; on {
									a.designCleared(d.pick(vd.Style, op.ID))
								} else {
									d.attrs = d.attrs.Without(vd.Style, op.ID)
								}
							}
						}
					})
					continue
				}
				labels := []string{i18n.T("Auto")}
				ids := []string{""}
				for _, op := range info.Options {
					labels, ids = append(labels, op.Label.In(loc)), append(ids, op.ID)
				}
				cur := labels[max(slices.Index(ids, d.attrs.Value(cat)), 0)]
				ui.Row(c.Key("cat-" + string(cat))).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
					ui.Text(c, info.Label.In(loc)).FontSize(px(t, fsCaption)).Width(px(t, 110)).Shrink(0)
					if ui.Select(c.Key("pick-"+string(cat)), &cur, labels).Label(info.Label.In(loc)).Grow(1).Changed() {
						if i := slices.Index(labels, cur); i >= 0 {
							a.designCleared(d.pick(cat, ids[i]))
						}
					}
				})
			}
			a.designSeed(c)
		})
	})
}

func (a *app) designCleared(cs []vd.Cleared) {
	if s := clearedText(cs); s != "" {
		a.toast(s)
	}
}

func (a *app) designSeed(c *ui.Context) {
	t := c.Theme()
	d := &a.design
	ui.Row(c.Key("design-seed")).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
		ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Width(px(t, 110)).Shrink(0).Children(func() {
			ui.Text(c, i18n.T("Seed")).FontSize(px(t, fsCaption))
			infoTip(c.Key("seed-info"), i18n.T("The seed keeps the voice's identity while you change the details."))
		})
		numberInput(c.Key("seed"), &d.seed, 0, float64(vd.MaxSeed), 1, numOpts{Label: i18n.T("Seed"), Grow: true})
		if iconButton(c.Key("new-seed"), icons.Refresh, i18n.T("New seed")).Tooltip(i18n.T("Another voice with the same description")).Clicked() {
			d.seed = float64(vd.NewSeed())
		}
	})
}

// designSavedVoices lists the saved designed voices: a click restores one.
func (a *app) designSavedVoices(c *ui.Context) {
	t := c.Theme()
	d := &a.design
	var designed []*core.Voice
	for i := range a.voices {
		if a.voices[i].Kind == core.VoiceDesigned {
			designed = append(designed, &a.voices[i])
		}
	}
	if len(designed) == 0 {
		return
	}
	collapsible(c.Key("design-saved"), i18n.N(len(designed), "%d saved design", "%d saved designs", len(designed)), &d.savedOpen, nil, func() {
		ui.Column(c).Gap(px(t, sp1)).Children(func() {
			for _, v := range designed {
				ui.Row(c.Key("saved-" + v.ID)).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
					ui.Avatar(c, v.Name, a.portrait(v))
					truncText(c, v.Name).Grow(1)
					label, ic := i18n.T("Preview"), icons.Play
					if a.voicePlaying(v) {
						label, ic = i18n.T("Stop"), icons.Stop
					}
					if iconButton(c.Key("play"), ic, label).Clicked() {
						a.playVoice(v)
					}
					if ghostButton(c.Key("open"), i18n.T("Open")).Clicked() {
						a.openDesignFor(v)
					}
				})
			}
		})
	})
}

// designTarget is the model picker and what it will be sent.
func (a *app) designTarget(c *ui.Context) {
	t := c.Theme()
	d := &a.design
	ms := designModels()
	if len(ms) == 0 {
		banner(c, icons.DangerTriangle, t.Warning, i18n.T("No model here can design voices. Download one that can, such as %s, in Settings → Models & network.", modelRefID(idVoiceDesign)), func() {
			if ui.Button(c.Key("design-models"), i18n.T("Models")).Clicked() {
				a.navigate("/models")
			}
		})
		return
	}
	if !slices.ContainsFunc(ms, func(m catalog.Entry) bool { return m.Label == d.model }) {
		d.model = ms[0].Label
		// The recommended designer first (presets: Qwen3-TTS VoiceDesign).
		for _, r := range voiceCatalog().Recommended(catalog.WorkflowDesign, i18n.Locale()) {
			if slices.ContainsFunc(ms, func(m catalog.Entry) bool { return m.ID == r.ID }) {
				d.model = r.Label
				break
			}
		}
	}
	e, _ := voiceCatalog().ByLabel(d.model)
	ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Wrap().Children(func() {
		ui.Text(c, i18n.T("Model")).FontWeight(600)
		ui.Box(c).Grow(1).Basis(px(t, 200)).MaxWidth(px(t, 360)).Children(func() {
			a.modelPicker(c, "design-model", catalog.WorkflowDesign, e, func(ne catalog.Entry) { d.model = ne.Label }, pickerOpts{Only: ms})
		})
		if !e.Licence.Commercial {
			nc := e.Licence.Name + " · " + i18n.T("non-commercial use only")
			statusMark(c, icons.DangerTriangle, t.Warning, nc).Tooltip(nc).Shrink(1)
		}
	})
	low, err := vd.Lower(designTarget(e), d.recipe(), vd.Options{Lang: designTextLang(d.speechLang())})
	if err != nil {
		return
	}
	// What the model will hear, behind ⓘ; in view only what it can't
	// say (decision D5, thread #42).
	loc := i18n.Locale()
	voice := low.Voice
	if voice == "" {
		voice = i18n.T("(nothing: the model picks a voice)")
	}
	ui.Column(c.Key("design-lowered")).Gap(px(t, sp1)).Children(func() {
		if low.DescriptionDropped && d.description != "" {
			statusMark(c, icons.DangerTriangle, t.Warning, i18n.T("This model only understands the details, not the description as written."))
		}
		if len(low.Dropped) > 0 {
			var parts []string
			for _, x := range low.Dropped {
				if op, ok := vd.Lookup(x.Category, x.ID); ok {
					parts = append(parts, op.Label.In(loc))
				}
			}
			statusMark(c, icons.DangerTriangle, t.Warning, i18n.T("%s can't express: %s", modelRef(e), strings.Join(parts, ", ")))
		}
		ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
			caption(c, i18n.T("What %s will hear", modelRef(e))).TextColor(tok(t).Text3).SingleLine().Ellipsis("…").MinWidth(0)
			infoTip(c.Key("design-sent"), i18n.T("Sent to %s", modelRef(e))+": "+voice)
		})
	})
}

// designScript is the line to preview and the preview.
func (a *app) designScript(c *ui.Context) {
	t := c.Theme()
	d := &a.design
	sectionHeading(c, i18n.T("Script"))
	ui.TextArea(c.Key("design-script"), &d.script).Lines(3, 8).Placeholder(i18n.T("What should this voice say?")).Label(i18n.T("Script"))
	j := a.voiceJob("design", "")
	ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, icons.Global.Linear).Size(px(t, 16), px(t, 16)).TextColor(tok(t).Text2)
		ui.Autocomplete(c.Key("design-language"), &d.language, voiceLanguages()).Placeholder(i18n.T("Auto")).Label(i18n.T("Language")).
			Tooltip(i18n.T("Language")).Width(px(t, 140)).Shrink(1).MinWidth(px(t, 80))
		ui.Spacer(c)
		_, ok := voiceCatalog().ByLabel(d.model)
		gen := ui.Button
		if j == nil || j.take == nil {
			gen = primaryButton // until there is a preview to save
		}
		if gen(c.Key("design-generate"), i18n.T("Generate preview")).
			Disabled(!ok || j.running() || strings.TrimSpace(d.script) == "").Clicked() {
			a.designGenerate()
		}
	})
	switch {
	case j == nil:
	case j.running():
		ui.Progress(c, j.fraction).Label(j.status)
		caption(c, j.status)
	case j.err != "":
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.DangerCircle.Linear).Size(px(t, 14), px(t, 14)).TextColor(t.Danger).Shrink(0)
			ui.Text(c, j.err).FontSize(px(t, fsCaption)).TextColor(t.Danger).Grow(1).MinWidth(0).Selectable()
			if j.needDownload != nil && secondaryIconButton(c.Key("design-download"), icons.DownloadMinimalistic, i18n.T("Download")).Label(i18n.T("Download")).Clicked() {
				a.download(*j.needDownload)
			}
		})
	default:
		a.clipCard(c, j.take, true)
	}
}

// designSaveForm saves the draft as a voice.
func (a *app) designSaveForm(c *ui.Context) {
	t := c.Theme()
	d := &a.design
	ui.Box(c).Height(1).Background(tok(t).Line)
	sectionHeading(c, i18n.T("Save as voice profile"))
	ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
		ui.TextInput(c.Key("design-name"), &d.saveName).Placeholder(i18n.T("Name")).Label(i18n.T("Profile name")).Grow(1).MinWidth(0).MaxWidth(px(t, 320))
		label := i18n.T("Save as voice")
		if v := a.findVoice(d.linked); v != nil && v.Name == strings.TrimSpace(d.saveName) {
			label = i18n.T("Update voice")
		}
		save := ui.Button
		if j := a.voiceJob("design", ""); j != nil && j.take != nil {
			save = primaryButton // the preview is there: saving is next
		}
		if save(c.Key("design-save"), label).Disabled(strings.TrimSpace(d.saveName) == "").Clicked() {
			a.designSave()
		}
	})
}
