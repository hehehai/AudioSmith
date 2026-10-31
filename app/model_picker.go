package main

// The model picker the creation pages share (Studio, Stories, Audiobook,
// Dub, Design): the chosen model by its friendly name with its one-line
// description (thread #45: the family, version and size only in the
// tooltip); a list with a search field, the page's recommended models
// first (the catalogue's presets for the interface's language, thread
// #41), each with up to three tags and a speed and a quality hint, and the
// others behind "Show all models", where the ones that cannot do the
// page's job are greyed out with the reason. Removed models
// (models_removed.go) are never offered.

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/lang"
)

// modelPickerState is the pickers' view state, by picker key.
type modelPickerState struct {
	pickerOpen map[string]bool
	pickerAll  map[string]bool // "Show all models" open (remembered while the app runs)
	pickerFind map[string]string
}

// pickerOpts tunes a picker.
type pickerOpts struct {
	Locked bool
	Custom bool // offer a custom Hugging Face repo (the Studio)
	// Only, when set, is the list (Design: the models that design);
	// otherwise every speech model is offered.
	Only []catalog.Entry
	// Dialogue, when set (Stories), adds the whole-dialogue models
	// (model_picker_dialogue.go).
	Dialogue *pickerDialogue
	// Row draws it as a selector row, with › (Text to speech, 0.5 F3).
	Row bool
}

// whyNotFor says why model e cannot do workflow w ("" = it can).
func whyNotFor(e catalog.Entry, w catalog.Workflow) string {
	switch w {
	case catalog.WorkflowDub:
		if !e.Has(catalog.CapClone) {
			return i18n.T("Can't clone: the dub won't sound like the speakers")
		}
	case catalog.WorkflowDesign:
		if !e.Has(catalog.CapDesign) {
			return i18n.T("Can't design voices from a description")
		}
	case catalog.WorkflowStory, catalog.WorkflowAudiobook:
		if e.NeedsReference {
			return i18n.T("Only clones: every voice must be a cloned one")
		}
	}
	return ""
}

// englishOnly reports whether e speaks only English (Kokoro without its
// hidden Chinese voices, Chatterbox Turbo), for a hint in other languages.
func englishOnly(e catalog.Entry) bool {
	p, _ := catalog.PresetFor(e.ID)
	langs := 0
	for _, l := range e.Languages {
		base := catalog.BaseLanguage(l.Code)
		if slices.Contains(p.HideLanguages, base) {
			continue
		}
		if base != "en" {
			return false
		}
		langs++
	}
	return langs > 0 || len(p.Languages) == 1 && p.Languages[0] == "en"
}

// languageNote warns about a model for the interface's language: one the
// evaluation found unusable in it (Kokoro's Chinese), or one that speaks
// only English; "" when it is fine.
func languageNote(e catalog.Entry) string {
	base := catalog.BaseLanguage(i18n.Locale())
	if base == "en" {
		return ""
	}
	if p, ok := catalog.PresetFor(e.ID); ok && slices.Contains(p.HideLanguages, base) {
		name := base
		if l, ok := lang.Lookup(base); ok {
			name = l.NameIn(i18n.Locale())
		}
		return i18n.T("Not for %s", name)
	}
	if englishOnly(e) {
		return i18n.T("English only")
	}
	return ""
}

// ratingDots is a 1–3 hint as dots: ●●○.
func ratingDots(n int) string {
	n = min(max(n, 0), 3)
	return strings.Repeat("●", n) + strings.Repeat("○", 3-n)
}

// pickerTags are the tags a picker row shows: at most two, leaving out
// Fast and High quality where the speed and quality dots say it.

// modelTip is a model's technical identity, for a tooltip: "Qwen3-TTS
// 0.6B CustomVoice · 8bit · ≈ 2 GB · Apache 2.0".
func modelTip(e catalog.Entry) string {
	if e.ID == "custom" {
		return e.DisplayName()
	}
	return e.TechnicalSummary()
}

// modelPicker is the picker for workflow w; set receives the model chosen
// key must be unique on the page.
func (a *app) modelPicker(c *ui.Context, key string, w catalog.Workflow, cur catalog.Entry, set func(catalog.Entry), o pickerOpts) {
	t := c.Theme()
	k := tok(t)
	if a.pickerOpen == nil {
		a.pickerOpen, a.pickerAll, a.pickerFind = map[string]bool{}, map[string]bool{}, map[string]string{}
	}
	lang := i18n.Locale()
	recs := catalog.Builtin().Recommended(w, lang)
	if o.Only != nil {
		recs = slices.DeleteFunc(recs, func(e catalog.Entry) bool {
			return !slices.ContainsFunc(o.Only, func(x catalog.Entry) bool { return x.ID == e.ID })
		})
	}
	isRec := func(e catalog.Entry) bool {
		return slices.ContainsFunc(recs, func(x catalog.Entry) bool { return x.ID == e.ID })
	}
	label, about, tip, rec := cur.DisplayName(), cur.Description(), modelTip(cur), isRec(cur)
	if dl := o.Dialogue; dl != nil && dl.On {
		// Whole dialogue: the dialogue model, then the story's own model,
		// which reads the narration and the lines it does not say clearly.
		restFor := dl.texts().restFor
		label, about, rec = dl.Cur.DisplayName(), restFor(cur.DisplayName()), false
		tip = modelTip(dl.Cur) + "\n" + restFor(modelTip(cur))
	}
	btn := ui.ButtonBase(c.Key(key)).Padding(px(t, 6), px(t, 10)).Gap(px(t, sp2)).Radius(px(t, radiusControl)).
		Background(t.Surface).Border(1, t.Border).AlignItems(ui.Center).Label(i18n.T("Model")).Tooltip(tip).Disabled(o.Locked)
	if btn.Hovered() && !o.Locked {
		btn.Background(t.SurfaceHover)
	}
	btn.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 1)).Children(func() {
			ui.Row(c).Wrap().GapX(px(t, 6)).GapY(px(t, 2)).AlignItems(ui.Center).Children(func() {
				ui.Text(c, label).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
				if rec {
					ui.Text(c, i18n.T("Recommended")).FontSize(px(t, fsMicro)).TextColor(k.AccentInk).Padding(px(t, 1), px(t, 5)).
						Radius(px(t, 4)).Background(k.AccentSoft).SingleLine().Shrink(0)
				}
			})
			if about != "" {
				ui.Text(c, about).FontSize(px(t, fsMicro)).TextColor(k.Text2).SingleLine().Ellipsis("…").MinWidth(0)
			}
		})
		chev := icons.AltArrowDown
		if o.Row {
			chev = icons.AltArrowRight
		}
		ui.Icon(c, chev.Linear).Size(px(t, 14), px(t, 14)).TextColor(k.Text2).Shrink(0)
	})
	if btn.Clicked() {
		a.pickerOpen[key] = !a.pickerOpen[key]
		a.pickerFind[key] = ""
	}
	open := a.pickerOpen[key]
	fitPopover(c, btn, &open, 0, func() { // popover_fit.go: never past the window
		a.pickerList(c, key, w, cur, recs, set, o)
	})
	a.pickerOpen[key] = a.pickerOpen[key] && open && !o.Locked // closed by a pick, outside, Escape
}

// pickerList is the picker's popover: recommended, then all the others.
func (a *app) pickerList(c *ui.Context, key string, w catalog.Workflow, cur catalog.Entry, recs []catalog.Entry, set func(catalog.Entry), o pickerOpts) {
	t := c.Theme()
	k := tok(t)
	pick := func(e catalog.Entry) {
		a.pickerOpen[key] = false
		if e.ID != cur.ID || e.ID == "custom" {
			set(e)
		}
	}
	others := o.Only
	if others == nil {
		others = catalog.Builtin().ForTask(catalog.TaskTTS)
	}
	others = slices.DeleteFunc(slices.Clone(others), func(e catalog.Entry) bool {
		return slices.ContainsFunc(recs, func(x catalog.Entry) bool { return x.ID == e.ID })
	})
	if o.Only == nil && w == catalog.WorkflowDesign {
		// Design lists only what designs.
		others = slices.DeleteFunc(others, func(e catalog.Entry) bool { return whyNotFor(e, w) != "" })
	}
	// The ones that can do the job first.
	slices.SortStableFunc(others, func(x, y catalog.Entry) int {
		xn, yn := whyNotFor(x, w) != "", whyNotFor(y, w) != ""
		switch {
		case xn == yn:
			return 0
		case yn:
			return -1
		}
		return 1
	})
	_, h := c.Size()
	find := a.pickerFind[key]
	ui.Row(c.Key(key+"-find")).Width(px(t, 340)).Shrink(0).Padding(px(t, 6), px(t, 6), 0).Children(func() {
		searchField(c, key+"-search", &find, i18n.T("Search models"), 0).Label(i18n.T("Search models"))
	})
	a.pickerFind[key] = find
	// The list scrolls under the search field, in what the panel leaves it
	// (the room beside the button, fitPopover).
	ui.Scroll(c.Key(key + "-scroll")).MaxHeight(max(px(t, 220), h*0.7-px(t, 40))).MinHeight(0).Shrink(1).Children(func() {
		ui.Column(c.Key(key + "-list")).Width(px(t, 340)).Gap(px(t, 2)).Padding(px(t, 6)).Children(func() {
			if strings.TrimSpace(find) != "" {
				// Searching: every match in one list, friendly or technical.
				n := 0
				for _, e := range append(slices.Clone(recs), others...) {
					if !e.Matches(find) {
						continue
					}
					n++
					why := whyNotFor(e, w)
					note := ""
					if why == "" {
						note = languageNote(e)
					}
					a.pickerRow(c, e, cur, e.Description(), firstOf(why, note), why == "", pick)
				}
				if o.Dialogue != nil {
					n += a.pickerDialogueRows(c, key, o.Dialogue, find, func() { a.pickerOpen[key] = false })
				}
				if n == 0 {
					ui.Text(c, i18n.T("No model matches")).FontSize(px(t, fsCaption)).TextColor(k.Text3).Padding(px(t, 6), px(t, sp2))
				}
				return
			}
			if len(recs) > 0 {
				head := i18n.T("Recommended")
				if o.Dialogue != nil && o.Dialogue.On {
					head = o.Dialogue.texts().restHead
				}
				ui.Text(c, head).FontSize(px(t, fsMicro)).FontWeight(600).TextColor(k.Text3).Padding(px(t, 4), px(t, sp2), px(t, 2))
				for _, e := range recs {
					a.pickerRow(c, e, cur, e.Description(), "", true, pick)
				}
			}
			if o.Dialogue != nil {
				ui.Divider(c)
				a.pickerDialogueRows(c, key, o.Dialogue, "", func() { a.pickerOpen[key] = false })
			}
			if len(others) == 0 && !o.Custom {
				return
			}
			all := a.pickerAll[key] || len(recs) == 0
			if len(recs) > 0 {
				ui.Divider(c)
				label := i18n.T("Show all models (%d)", len(others))
				tg := ui.ButtonBase(c.Key(key+"-all")).Padding(px(t, 6), px(t, sp2)).Radius(px(t, radiusControl)).Gap(px(t, 6)).AlignItems(ui.Center).Label(label)
				if tg.Hovered() {
					tg.Background(k.Hover)
				}
				if tg.Clicked() {
					a.pickerAll[key] = !all
					all = !all
				}
				tg.Children(func() {
					ic := icons.AltArrowRight
					if all {
						ic = icons.AltArrowDown
					}
					ui.Icon(c, ic.Linear).Size(px(t, 14), px(t, 14)).TextColor(k.Text2)
					ui.Text(c, label).FontSize(px(t, fsCaption)).TextColor(k.Text2)
				})
			}
			if !all {
				return
			}
			for _, e := range others {
				why := whyNotFor(e, w)
				note := ""
				if why == "" {
					note = languageNote(e)
				}
				a.pickerRow(c, e, cur, e.Description(), firstOf(why, note), why == "", pick)
			}
			if o.Custom {
				custom := catalog.Custom("")
				a.pickerRow(c, custom, cur, i18n.T("Any speech model on Hugging Face that the engine can load (type its repo)."), "", true, pick)
			}
		})
	})
}

// pickerRow is one model of the list: its friendly name and up to three
// tags ("non-commercial" first), one line about it (or why it can't be
// used, or a note), the speed and quality hint of a preset, and on the
// right ✓ for the chosen one or the download size of one not
// downloaded. Its tooltip names it technically.
func (a *app) pickerRow(c *ui.Context, e, cur catalog.Entry, about, note string, usable bool, pick func(catalog.Entry)) {
	chosen := e.ID == cur.ID && (e.ID != "custom" || cur.ID == "custom")
	a.pickerRowX(c, e, chosen, about, note, "", usable, pick)
}

// pickerRowX is pickerRow with chosen given, and facts (a dialogue
// model's memory and technical name) as its last line instead of the
// speed and quality hint.
func (a *app) pickerRowX(c *ui.Context, e catalog.Entry, chosen bool, about, note, facts string, usable bool, pick func(catalog.Entry)) {
	t := c.Theme()
	k := tok(t)
	name := e.DisplayName()
	b := ui.ButtonBase(c.Key("pm-"+e.ID)).Padding(px(t, 6), px(t, sp2)).Radius(px(t, radiusControl)).Gap(px(t, sp2)).
		AlignItems(ui.Center).Label(name).Tooltip(modelTip(e)).Disabled(!usable)
	switch {
	case chosen:
		b.Background(k.Pressed)
	case b.Hovered() && usable:
		b.Background(k.Hover)
	}
	if b.Clicked() && usable {
		pick(e)
	}
	b.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 1)).Children(func() {
			col := k.Text
			if !usable {
				col = k.Text3
			}
			ui.Row(c).Wrap().GapX(px(t, 6)).GapY(px(t, 2)).AlignItems(ui.Center).Children(func() {
				ui.Text(c, name).TextColor(col).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
				pickerTags(c, e)
			})
			line, lineCol := about, k.Text2
			if note != "" {
				line = note
				if !usable {
					lineCol = k.Text2
				} else {
					lineCol = k.Text3
				}
			}
			if line != "" {
				ui.Text(c, line).FontSize(px(t, fsMicro)).TextColor(lineCol).MaxLines(2).Ellipsis("…").MinWidth(0)
			}
			if facts != "" {
				ui.Text(c, facts).FontSize(px(t, fsMicro)).TextColor(k.Text3).SingleLine().Ellipsis("…").MinWidth(0)
			} else if p, ok := catalog.PresetFor(e.ID); ok && usable && note == "" {
				ui.Text(c, i18n.T("Speed %s · Quality %s", ratingDots(p.Speed), ratingDots(p.Quality))).FontSize(px(t, fsMicro)).TextColor(k.Text3).SingleLine()
			}
		})
		switch {
		case chosen:
			iconMark(c, icons.CheckCircle, k.AccentInk, i18n.T("Chosen"))
		case e.ID != "custom":
			if ready, known := a.modelOnDisk(e); known && !ready {
				ui.Text(c, catalog.HumanSize(e.Size)).FontSize(px(t, fsMicro)).TextColor(k.Text3).SingleLine().Shrink(0).
					Tooltip(i18n.T("Not downloaded yet"))
			}
		}
	})
}

func pickerTags(c *ui.Context, e catalog.Entry) {
	tags := e.DisplayTags(0)
	if _, ok := catalog.PresetFor(e.ID); ok {
		tags = slices.DeleteFunc(tags, func(tg catalog.Tag) bool { return tg == catalog.TagFast || tg == catalog.TagQuality })
	}
	tagChips(c, tags[:min(len(tags), 2)])
}

// modelTags shows up to n of e's tags as chips (0 = all): Non-commercial
// in the warning colour.
func modelTags(c *ui.Context, e catalog.Entry, n int) { tagChips(c, e.DisplayTags(n)) }

func tagChips(c *ui.Context, tags []catalog.Tag) {
	t := c.Theme()
	for _, tg := range tags {
		ch := tagChip(c, tg.Label())
		if tg == catalog.TagNonCommercial {
			ch.TextColor(t.Warning)
		}
	}
}

// modelRef names model e inside a sentence: its friendly name in quotes
// (“Standard voices” is not downloaded yet).
func modelRef(e catalog.Entry) string { return "“" + e.DisplayName() + "”" }

// modelSelect is a select of models by their friendly names (its tooltip
// names the chosen one technically): it returns the model chosen and
// true when the user chose another. cur may be outside models (then the
// select shows its name too).
func modelSelect(c *ui.Context, key string, cur catalog.Entry, models []catalog.Entry, label string, w float32) (catalog.Entry, bool) {
	names := make([]string, 0, len(models)+1)
	for _, e := range models {
		names = append(names, e.DisplayName())
	}
	sel := cur.DisplayName()
	if cur.ID == "" && len(models) > 0 {
		sel = names[0]
	}
	if ui.Select(c.Key(key), &sel, names).Label(label).Width(w).Tooltip(modelTip(cur)).Changed() {
		if i := slices.Index(names, sel); i >= 0 && models[i].ID != cur.ID {
			return models[i], true
		}
	}
	return cur, false
}

// modelRefID is modelRef of the catalogue model with id ("" when the
// catalogue has none): texts that suggest a model name it by its
// friendly name.
func modelRefID(id string) string {
	if e, ok := catalog.Builtin().ByID(id); ok {
		return modelRef(e)
	}
	return ""
}

// Models texts suggest by name.
const (
	idCustomVoice = "qwen3-tts-0.6b-customvoice-8bit"
	idVoiceDesign = "qwen3-tts-1.7b-voicedesign-4bit"
	idCloneHiFi   = "qwen3-tts-1.7b-base-8bit"
	idOmniVoice   = "omnivoice-bf16"
)
