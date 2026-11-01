package main

// Mine's meta badges and language filter (thread #129, ui_badge.go): a
// voice's kind sets the type filter (全部/克隆/设计/收藏) to it, its
// language the language filter. That filter shows over the table while
// it filters, a menu of the languages the library's voices speak (the
// header has no room for it beside the type filter), and in the narrow
// header's ⋯ when they speak more than one. Another click on the badge
// of the filter set clears it.

import (
	"slices"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// mineKindOf is the type filter that lists v by its kind, kindAll when
// none does (a portable copy, a built-in speaker not from Discover).
func mineKindOf(v *core.Voice) int {
	switch {
	case fromDiscover(v):
		return kindSaved
	case isPortable(v):
		return kindAll
	case v.Kind == core.VoiceCloned:
		return kindCloned
	case v.Kind == core.VoiceDesigned:
		return kindDesigned
	}
	return kindAll
}

// mineKindBadge is a voice's kind: a badge when a type filter lists it,
// else a plain value.
func (a *app) mineKindBadge(c *ui.Context, v *core.Voice) {
	kind := mineKindOf(v)
	if kind == kindAll {
		metaCell(c, icons.Tag, i18n.T("Kind"), mineKind(v), 92)
		return
	}
	on := a.voiceKind == kind
	if metaBadge(c, badgeSpec{key: "b-kind", ic: icons.Tag, value: mineKind(v), on: on}) {
		a.voiceKind = ifInt(on, kindAll, kind)
	}
}

// mineLangBadge is a voice's language (with its flag) as a badge.
func (a *app) mineLangBadge(c *ui.Context, v *core.Voice) {
	if v.Language == "" {
		return
	}
	on := a.voiceLang == v.Language
	if metaBadge(c, badgeSpec{key: "b-lang", ic: noIcon, value: langName(v.Language), on: on}) {
		a.voiceLang = ifStr(on, "", v.Language)
	}
}

// mineLangs are the languages the library's voices speak, sorted.
func (a *app) mineLangs() []string {
	var out []string
	for _, v := range a.voices {
		if v.Language != "" && !slices.Contains(out, v.Language) {
			out = append(out, v.Language)
		}
	}
	slices.Sort(out)
	return out
}

// mineLangShown reports whether the narrow header's ⋯ offers the
// language filter.
func (a *app) mineLangShown() bool { return a.voiceLang != "" || len(a.mineLangs()) > 1 }

// mineLangFilter is the language filter over the table, while it
// filters: 「语言：🇨🇳 中文 ⌄」, its menu, and Reset.
func (a *app) mineLangFilter(c *ui.Context) {
	if a.voiceLang == "" {
		return
	}
	t := c.Theme()
	ui.Row(c.Key("voices-lang-row")).Gap(px(t, sp1)).AlignItems(ui.Center).Shrink(0).Children(func() {
		label := i18n.T("%s: %s", i18n.T("Language"), langName(a.voiceLang))
		dropText(c.Key("voices-lang"), label, true).Tooltip(label).Menu(a.mineLangMenu)
		if quietButton(c.Key("voices-lang-reset"), icons.Restart, i18n.T("Reset")).Clicked() {
			a.voiceLang = ""
		}
	})
}

// mineLangMenu is the language filter's menu.
func (a *app) mineLangMenu(m *ui.Menu) {
	if m.Item(i18n.T("All languages")).Checked(a.voiceLang == "").Chosen() {
		a.voiceLang = ""
	}
	for _, l := range a.mineLangs() {
		if m.Item(langName(l)).Checked(a.voiceLang == l).Chosen() {
			a.voiceLang = l
		}
	}
}

// ifInt is v when ok, else w.
func ifInt(ok bool, v, w int) int {
	if ok {
		return v
	}
	return w
}
