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

// designDescribe is the description box and what the parser made of it.
func (a *app) designDescribe(c *ui.Context) {
	t := c.Theme()
	d := &a.design
	loc := i18n.Locale()
	k := tok(t)
	sectionHeading(c, i18n.T("Describe the voice"))
	ui.TextArea(c.Key("design-description"), &d.description).Lines(2, 6).
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
		if eng, ok := vd.EngineFor(e.Family, e.Repo); ok && eng.Vocabulary == vd.VocabFreeform && d.description != "" && d.detailsOpen {
			caption(c, i18n.T("This model reads your description as written; the details below are extra cues."))
		}
	}
	if d.preset == "" && d.attrs.Summary("en") != d.parsed.Attrs.Canonical().Summary("en") { // picks beyond the description
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
	caption(c, i18n.T("Or start from a preset:"))
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
					if d.description == p.Description.In(loc) {
						d.description = ""
					}
				}
			}
		}
	})
}

// designDetails are the attribute pickers and the seed.
func (a *app) designDetails(c *ui.Context) {
	t := c.Theme()
	d := &a.design
	loc := i18n.Locale()
	summary := d.attrs.Summary(loc)
	{
		ui.Column(c.Key("design-details")).Gap(px(t, sp2)).Children(func() {
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
	}
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
						label, ic = i18n.T("Stop"), icons.Pause
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

// designModelDefault picks the draft's model when it has none it can
// use: the recommended designer first (presets: Qwen3-TTS VoiceDesign).
// It reports whether any model can design.
func (a *app) designModelDefault() bool {
	d := &a.design
	ms := designModels()
	if len(ms) == 0 {
		return false
	}
	if !slices.ContainsFunc(ms, func(m catalog.Entry) bool { return m.Label == d.model }) {
		d.model = ms[0].Label
		for _, r := range voiceCatalog().Recommended(catalog.WorkflowDesign, i18n.Locale()) {
			if slices.ContainsFunc(ms, func(m catalog.Entry) bool { return m.ID == r.ID }) {
				d.model = r.Label
				break
			}
		}
	}
	return true
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
	a.designModelDefault()
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
