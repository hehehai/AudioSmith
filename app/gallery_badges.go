package main

// Discover's meta badges (thread #129, ui_badge.go): a voice's language,
// gender and age, and first use, in 「全部」's rows and on 「推荐」's
// cards. A click sets the language menu, the gender and age menus, or
// the use chip to the voice's value (another click on the one set
// clears it).

import (
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/gallery"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// setFacet sets facet f to v ("" clears it).
func (g *galleryState) setFacet(f gallery.Facet, v string) {
	if g.filters == nil {
		g.filters = map[gallery.Facet]string{}
	}
	if v == "" {
		delete(g.filters, f)
	} else {
		g.filters[f] = v
	}
	g.limit = galleryPageSize
}

// archUseTab is the use chip (g.tab) of a voice's first use, 0 none.
func archUseTab(arch gallery.Archetype) int {
	if len(arch.Uses) == 0 {
		return 0
	}
	for i, cat := range gallery.Categories() {
		if cat.ID == arch.Uses[0] {
			return i + 1
		}
	}
	return 0
}

// archLangBadge is a voice's language as a badge.
func (a *app) archLangBadge(c *ui.Context, arch gallery.Archetype, small bool) {
	g := &a.gallery
	if len(arch.Languages) == 0 {
		return
	}
	code := arch.Languages[0]
	on := g.filters[gallery.FacetLanguage] == code
	if metaBadge(c, badgeSpec{key: "b-lang", ic: noIcon, value: archLanguage(arch), on: on, small: small}) {
		g.setFacet(gallery.FacetLanguage, ifStr(on, "", code))
	}
}

// archGenderBadge is a voice's gender and age as one badge: it sets both
// menus (the age left free when the voice has none).
func (a *app) archGenderBadge(c *ui.Context, arch gallery.Archetype) {
	g := &a.gallery
	on := arch.Gender != "" && g.filters[gallery.FacetGender] == arch.Gender && g.filters[gallery.FacetAge] == arch.Age
	if metaBadge(c, badgeSpec{key: "b-gender", ic: icons.UserRounded, value: archGenderAge(arch, i18n.Locale()), on: on}) {
		g.setFacet(gallery.FacetGender, ifStr(on, "", arch.Gender))
		g.setFacet(gallery.FacetAge, ifStr(on, "", arch.Age))
	}
}

// archUseBadge is a voice's first use as a badge, and "+n" for the rest
// (more, in the table: its tooltip names them all).
func (a *app) archUseBadge(c *ui.Context, arch gallery.Archetype, small, more bool) {
	g := &a.gallery
	loc := i18n.Locale()
	tab := archUseTab(arch)
	on := tab != 0 && g.tab == tab
	if metaBadge(c, badgeSpec{key: "b-use", ic: icons.Tag, value: archUse(arch, loc), on: on, small: small}) && tab != 0 {
		g.tab, g.limit = tab, galleryPageSize
		if on {
			g.tab = 0
		}
	}
	if more {
		badgeMore(c, len(archUseNames(arch, loc))-1, i18n.T("%s: %s", i18n.T("Use cases"), archUses(arch, loc)))
	}
}
