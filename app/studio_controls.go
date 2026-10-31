package main

// The composer's controls: the "+ Insert" menu of markup (Alt+/) and the
// language picker. What they offer follows the model: its catalogue
// capabilities and its render traits (services.TraitsFor). The voice and
// generation settings are in the inspector (studio_inspector.go).

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/lang"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/text/markup"
)

// composerState is the composer's view state.
type composerState struct {
	insertOpen bool
	langOpen   bool
	langQuery  string
	langList   ui.ListState
	// undoToast, when set, is shown as a toast with Undo (an import).
	undoToast string
	// panel: the inspector shown or hidden by its toggle; panelAuto
	// while the toggle was not used (shown when the window is wide
	// enough).
	panel, panelSet bool
}

// insertItem is an entry of the insert menu.
type insertItem struct {
	key, label  string
	open, close string // text before and after the selection
	hint        string // what it inserts, as shown; open when empty
	nonVerbal   bool
}

func pauseItems() []insertItem {
	return []insertItem{
		{key: "pause", label: i18n.T("Short pause"), open: "[pause]"},
		{key: "pause-500", label: i18n.T("Half a second"), open: "[pause 500ms]"},
		{key: "pause-1s", label: i18n.T("One second"), open: "[pause 1s]"},
		{key: "pause-2s", label: i18n.T("Two seconds"), open: "[pause 2s]"},
	}
}

func deliveryItems() []insertItem {
	return []insertItem{
		{key: "slow", label: i18n.T("Slower"), open: "[slow]", close: "[/slow]"},
		{key: "fast", label: i18n.T("Faster"), open: "[fast]", close: "[/fast]"},
		{key: "spell", label: i18n.T("Spell out"), open: "[spell]", close: "[/spell]"},
	}
}

// nonVerbalLabel names a non-verbal tag.
func nonVerbalLabel(tag string) string {
	switch tag {
	case "laughter":
		return i18n.T("Laughter")
	case "sigh":
		return i18n.T("Sigh")
	case "breath":
		return i18n.T("Breath")
	case "confirmation-en":
		return i18n.T("Agreement (“mm-hm”)")
	case "dissatisfaction-hnn":
		return i18n.T("Displeasure (“hnn”)")
	}
	if rest, ok := strings.CutPrefix(tag, "question-"); ok {
		return i18n.T("Question (“%s?”)", rest)
	}
	if rest, ok := strings.CutPrefix(tag, "surprise-"); ok {
		return i18n.T("Surprise (“%s!”)", rest)
	}
	return tag
}

// insertMenu is the Insert button and its menu of markup.
func (a *app) insertMenu(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	tr := a.traits()
	var btn ui.Element
	if a.studioNarrow(c) {
		btn = iconButton(c.Key("insert"), icons.AddCircle, i18n.T("Insert"))
	} else {
		btn = dropButton(c.Key("insert"), icons.AddCircle, i18n.T("Insert"))
	}
	btn.Tooltip(i18n.T("Insert a pause, a delivery change or an expression (⌥/)"))
	if btn.Clicked() {
		a.composer.insertOpen = !a.composer.insertOpen
	}
	ui.Popover(c, btn, &a.composer.insertOpen, func() {
		ui.Column(c.Key("insert-menu")).Width(px(t, 260)).Gap(px(t, 2)).Padding(px(t, 4)).Children(func() {
			section := func(title string) {
				ui.Text(c, title).FontSize(px(t, fsMicro)).FontWeight(600).TextColor(k.Text3).Padding(px(t, sp2), px(t, sp2), px(t, 2))
			}
			item := func(it insertItem, disabled bool) {
				row := ui.ButtonBase(c.Key("ins-"+it.key)).Padding(px(t, 5), px(t, sp2)).Radius(px(t, radiusControl)).Label(it.label).Disabled(disabled)
				if row.Hovered() && !disabled {
					row.Background(k.Hover)
				}
				if row.Clicked() && !disabled {
					a.composer.insertOpen = false
					if it.close != "" {
						a.wrapSelection(it.open, it.close)
					} else {
						a.insertScript(it.open)
					}
				}
				row.Children(func() {
					color := k.Text
					if disabled {
						color = k.Text3
					}
					ui.Text(c, it.label).TextColor(color).Grow(1).SingleLine().Ellipsis("…").MinWidth(0)
					hint := it.hint
					if hint == "" {
						hint = it.open
					}
					ui.Text(c, hint).Font(monoFont).FontSize(px(t, fsMicro)).TextColor(k.Text3).SingleLine()
				})
			}
			section(i18n.T("Pauses"))
			for _, it := range pauseItems() {
				item(it, false)
			}
			section(i18n.T("Delivery"))
			for _, it := range deliveryItems() {
				item(it, (it.key == "slow" || it.key == "fast") && !tr.Speed)
			}
			item(insertItem{key: "say", label: i18n.T("Pronounce as…"), open: "[[", close: "|]]", hint: "[[word|say]]"}, false)
			section(i18n.T("Expressions"))
			if !tr.NonVerbal {
				ui.Text(c, i18n.T("%s cannot voice expressions; they are left out.", a.preset().Label)).
					FontSize(px(t, fsMicro)).TextColor(k.Text3).Padding(0, px(t, sp2), px(t, 4))
			}
			ui.Scroll(c).MaxHeight(px(t, 220)).Children(func() {
				ui.Column(c).Children(func() {
					for _, tag := range markup.DefaultNonVerbal {
						item(insertItem{key: tag, label: nonVerbalLabel(tag), open: "[" + tag + "]", nonVerbal: true}, !tr.NonVerbal)
					}
				})
			})
		})
	})
}

// languageSupport lists the language codes the model speaks, and maps a
// code to the value its language parameter takes. all is true when every
// language is offered (a custom model).
func languageSupport(p catalog.Entry) (codes []string, value func(code string) string, all bool) {
	value = func(code string) string { return code }
	switch {
	case p.ID == "custom":
		return nil, value, true
	case len(p.Languages) == 0 && p.Family == "omnivoice":
		return lang.OmniVoiceCodes(), value, false
	}
	byCode := map[string]string{}
	for _, l := range p.Languages {
		codes = append(codes, l.Code)
		byCode[l.Code] = l.Value
	}
	value = func(code string) string {
		if v := byCode[code]; v != "" {
			return v
		}
		return code
	}
	return codes, value, false
}

// languageLabel shows the form's language: flag and name, or Auto.
func (a *app) languageLabel() string {
	if a.language == "" {
		return i18n.T("Auto-detect")
	}
	p := a.preset()
	code := a.language
	for _, l := range p.Languages {
		if l.Value == a.language {
			code = l.Code
		}
	}
	if l, ok := lang.Lookup(code); ok {
		name := l.NameIn(i18n.Locale())
		if f := l.Flag(); f != "" {
			name = f + " " + name
		}
		return name
	}
	return a.language
}

// languageChosen sets the language and remembers it among the recent ones.
func (a *app) languageChosen(code, value string) {
	a.language = value
	r := a.recentLangs[:]
	r = slices.DeleteFunc(slices.Clone(r), func(s string) bool { return s == code || s == "" })
	r = append([]string{code}, r...)
	for i := range a.recentLangs {
		a.recentLangs[i] = ""
		if i < len(r) {
			a.recentLangs[i] = r[i]
		}
	}
	a.composer.langOpen = false
}

// languagePicker is the language button and its searchable list.
func (a *app) languagePicker(c *ui.Context) {
	t := c.Theme()
	p := a.preset()
	if !p.Has(catalog.CapLanguage) && p.ID != "custom" {
		return
	}
	btn := dropButton(c.Key("language"), icons.Global, a.languageLabel()).Tooltip(i18n.T("Language of the script"))
	if btn.Clicked() {
		a.composer.langOpen = !a.composer.langOpen
	}
	ui.Popover(c, btn, &a.composer.langOpen, func() {
		codes, valueOf, all := languageSupport(p)
		var langs []lang.Language
		if q := strings.TrimSpace(a.composer.langQuery); q != "" {
			langs = lang.Search(q)
		} else {
			langs = lang.All()
		}
		opts := lang.Annotate(langs, codes)
		if all {
			for i := range opts {
				opts[i].Supported, opts[i].ModelCode = true, opts[i].Code
			}
		}
		// What the model speaks first.
		slices.SortStableFunc(opts, func(x, y lang.Option) int {
			switch {
			case x.Supported == y.Supported:
				return 0
			case x.Supported:
				return -1
			}
			return 1
		})
		ui.Column(c.Key("lang-picker")).Width(px(t, 320)).Gap(px(t, 6)).Padding(px(t, 6)).Children(func() {
			ui.TextInput(c.Key("lang-search"), &a.composer.langQuery).Placeholder(i18n.T("Search 650 languages")).Label(i18n.T("Search languages"))
			ui.Row(c).Gap(px(t, 6)).Wrap().Children(func() {
				if secondaryButton(c.Key("lang-auto"), i18n.T("Auto-detect")).Clicked() {
					a.language = ""
					a.composer.langOpen = false
				}
				for _, code := range a.recentLangs {
					if code == "" {
						continue
					}
					l, ok := lang.Lookup(code)
					if !ok {
						continue
					}
					mc, supported := lang.Match(codes, code)
					if all {
						mc, supported = code, true
					}
					if !supported {
						continue
					}
					if secondaryButton(c.Key("lang-recent-"+code), strings.TrimSpace(l.Flag()+" "+l.NameIn(i18n.Locale()))).Clicked() {
						a.languageChosen(code, valueOf(mc))
					}
				}
			})
			ui.List(c.Key("lang-list"), &a.composer.langList, len(opts)).Height(px(t, 300)).ItemKey(func(i int) any { return opts[i].Code }).Rows(func(r ui.ListRow) {
				o := opts[r.Index]
				row := ui.ButtonBase(c.Key("lang-"+o.Code)).Padding(px(t, 4), px(t, 8)).Radius(5).Disabled(!o.Supported).
					Label(o.NameIn(i18n.Locale()))
				if o.Supported && row.Hovered() {
					row.Background(t.SurfaceHover)
				}
				if o.Supported && row.Clicked() {
					a.languageChosen(o.Code, valueOf(o.ModelCode))
				}
				row.Children(func() {
					muted := t.TextMuted
					text := t.Text
					if !o.Supported {
						text = muted
					}
					ui.Text(c, o.Flag()).Width(px(t, 22))
					ui.Column(c).Grow(1).MinWidth(0).Children(func() {
						name := o.NameIn(i18n.Locale())
						if o.Native != "" && o.Native != name {
							name += " · " + o.Native
						}
						ui.Text(c, name).TextColor(text).SingleLine().Ellipsis("…")
						if !o.Supported {
							ui.Text(c, i18n.T("Not supported by %s", modelRef(p))).FontSize(px(t, 11)).TextColor(muted).SingleLine().Ellipsis("…")
						}
					})
					ui.Text(c, o.Code).FontSize(px(t, 11)).TextColor(muted)
				})
			})
		})
	})
}

// tierSteps are the performance tiers' steps, in the Segmented's order.
var tierSteps = []float64{services.StepsFast, services.StepsBalanced, services.StepsQuality}
