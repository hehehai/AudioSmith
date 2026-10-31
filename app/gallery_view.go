package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/gallery"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
)

// galleryFacets are the facets offered as filters (use cases are a
// select beside the search).
var galleryFacets = []gallery.Facet{gallery.FacetLanguage, gallery.FacetGender, gallery.FacetAge}

// facetTitle names a facet.
func facetTitle(f gallery.Facet) string {
	switch f {
	case gallery.FacetGender:
		return i18n.T("Gender")
	case gallery.FacetAge:
		return i18n.T("Age")
	case gallery.FacetLanguage:
		return i18n.T("Language")
	}
	return string(f)
}

// galleryPage is /gallery.
func (a *app) galleryPage(c *ui.Context) {
	t := c.Theme()
	g := &a.gallery
	page := a.galleryResults()
	narrow := a.pageWidth(c) < px(t, 720)
	pageHeaderBar(c, pageHead{
		title: i18n.T("Discover voices"),
		count: i18n.N(page.Total, "%d voice", "%d voices", page.Total),
		search: &headSearch{key: "gallery-search", value: &g.text, placeholder: i18n.T("Search: narrator, 东北, grandma…"),
			label:   i18n.T("Search Discover voices"),
			changed: func() { g.limit = galleryPageSize }},
		tools: func(hn bool) {
			if !hn {
				a.galleryCategorySelect(c, page, narrow)
			}
			if iconToggle(c.Key("gallery-favs"), icons.Star, g.favOnly, i18n.T("Favorites")).Tooltip(i18n.T("Show only your favorites")).Clicked() {
				g.favOnly = !g.favOnly
				g.limit = galleryPageSize
			}
		},
		more:    func(m *ui.Menu) { a.galleryCategoryMenu(m, page) },
		primary: a.newVoiceButton(c),
	})
	ui.Column(c).Padding(px(t, sp1), px(t, sp6), px(t, sp3)).Gap(px(t, sp2)).Children(func() {
		ui.Row(c.Key("gallery-facets")).Gap(px(t, sp1)).Wrap().AlignItems(ui.Center).Children(func() {
			for _, f := range galleryFacets {
				a.facetMenu(c, f, page.Facets[f])
			}
			if len(g.filters) > 0 || g.text != "" || g.tab != 0 || g.favOnly {
				if quietButton(c.Key("gallery-reset"), icons.Restart, i18n.T("Reset")).Clicked() {
					g.text, g.tab, g.favOnly, g.limit = "", 0, false, galleryPageSize
					clear(g.filters)
				}
			}
		})
	})
	if len(page.Items) == 0 {
		emptyState(c, icons.Widget, i18n.T("No voices match these filters."), "", nil)
		return
	}
	items := page.Items
	ui.GridView(c.Key("gallery-grid"), &g.grid, len(items), px(t, 260), g.cardHeight(t), func(i int) {
		a.archetypeCard(c, items[i])
	}).Grow(1).Padding(0, px(t, sp6), px(t, sp4))
}

// galleryCounts are the voices of each use case (a voice may have
// several).
func galleryCounts(page gallery.Page) map[string]int {
	counts := map[string]int{}
	for _, fc := range page.Facets[gallery.FacetCategory] {
		counts[fc.Value] = fc.Count
	}
	return counts
}

// galleryCategorySelect is the use cases, with their counts, as a select
// beside the search field.
func (a *app) galleryCategorySelect(c *ui.Context, page gallery.Page, narrow bool) {
	t := c.Theme()
	g := &a.gallery
	loc := i18n.Locale()
	counts := galleryCounts(page)
	cats := gallery.Categories()
	options := make([]string, 0, len(cats)+1)
	options = append(options, i18n.T("All uses"))
	for _, cat := range cats {
		options = append(options, cat.Name.In(loc)+"  "+itoa(counts[cat.ID]))
	}
	if g.tab < 0 || g.tab >= len(options) {
		g.tab = 0
	}
	cur := options[g.tab]
	w := px(t, 220)
	if narrow {
		w = px(t, 170)
	}
	if ui.Select(c.Key("gallery-category"), &cur, options).Label(i18n.T("Use")).Tooltip(i18n.T("Use")).Width(w).Shrink(1).MinWidth(px(t, 120)).Changed() {
		for i, o := range options {
			if o == cur {
				g.tab, g.limit = i, galleryPageSize
			}
		}
	}
}

// galleryCategoryMenu is the use cases as a submenu of the header's ⋯
// (a narrow window).
func (a *app) galleryCategoryMenu(m *ui.Menu, page gallery.Page) {
	g := &a.gallery
	loc := i18n.Locale()
	counts := galleryCounts(page)
	m.Submenu(i18n.T("Use"), func(m *ui.Menu) {
		if m.Item(i18n.T("All uses")).Checked(g.tab <= 0).Chosen() {
			g.tab, g.limit = 0, galleryPageSize
		}
		for i, cat := range gallery.Categories() {
			if m.Item(cat.Name.In(loc) + "  " + itoa(counts[cat.ID])).Checked(g.tab == i+1).Chosen() {
				g.tab, g.limit = i+1, galleryPageSize
			}
		}
	})
}

// facetMenu is one facet as a menu button of its values with counts.
func (a *app) facetMenu(c *ui.Context, f gallery.Facet, values []gallery.FacetCount) {
	g := &a.gallery
	loc := i18n.Locale()
	cur := g.filters[f]
	label := facetTitle(f)
	if cur != "" {
		for _, v := range gallery.FacetValues(f) {
			if v.Value == cur {
				label += ": " + v.Label.In(loc)
			}
		}
	}
	dropText(c.Key("facet-"+string(f)), label, cur != "").Label(facetTitle(f)).Tooltip(label).Menu(func(m *ui.Menu) {
		if m.Item(i18n.T("Any")).Checked(cur == "").Chosen() {
			delete(g.filters, f)
			g.limit = galleryPageSize
		}
		for _, v := range values {
			if m.Item(v.Label.In(loc) + "  " + itoa(v.Count)).Checked(cur == v.Value).Chosen() {
				g.filters[f] = v.Value
				g.limit = galleryPageSize
			}
		}
	})
}

// archetypeCard is one gallery voice: a flat card with its avatar, name,
// traits, star, description, Preview, Use voice and ⋯.
func (a *app) archetypeCard(c *ui.Context, arch gallery.Archetype) {
	t := c.Theme()
	k := tok(t)
	g := &a.gallery
	loc := i18n.Locale()
	cd := card(c.Key("arch-" + arch.ID)).Grow(1).Gap(px(t, sp2)).Padding(px(t, sp3))
	cd.Children(func() {
		name := arch.Name.In(loc)
		desc := arch.Description.In(loc)
		head := ui.Row(c.Key("head")).Gap(px(t, sp2)).AlignItems(ui.Start)
		head.Children(func() {
			ui.Avatar(c, name, nil)
			ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 2)).Children(func() {
				ui.Text(c, name).FontWeight(600).SingleLine().Ellipsis("…").MinWidth(0).Tooltip(name)
				ui.Text(c, desc).FontSize(px(t, fsCaption)).TextColor(k.Text2).SingleLine().Ellipsis("…").MinWidth(0).Tooltip(desc)
			})
			fav := g.favs.Has(arch.ID)
			tip := i18n.T("Add to favorites")
			if fav {
				tip = i18n.T("Remove from favorites")
			}
			if iconToggle(c.Key("fav"), icons.Star, fav, tip).Clicked() {
				a.toggleFavorite(arch.ID)
			}
		})
		// The voice's details as tags: its language, gender and age, its
		// use cases, and a built-in speaker's model; they wrap.
		// At most two rows (the card has a fixed height): the rest are
		// cut, and the tooltip lists them all.
		tags := archetypeTags(arch, loc)
		// Two rows' height comes from a tag as last laid out (its font and
		// size vary: Chinese runs taller than English).
		meta := ui.Row(c.Key("meta")).Gap(px(t, 4)).Wrap().AlignContent(ui.Start).Shrink(0).Tooltip(strings.Join(tags, " · "))
		if g.tagH > 0 {
			meta.Height(2*g.tagH + px(t, 4)).ClipY() // footers line up across cards
		}
		meta.Children(func() {
			for i, l := range tags {
				chip := tagChip(c, l)
				if i == 0 {
					g.measure(c, &g.tagH, chip.Bounds().H)
				}
			}
		})
		foot := ui.Row(c.Key("foot")).Gap(px(t, sp1)).AlignItems(ui.Center)
		// The card's content runs from its top to the footer's bottom, as
		// laid out (fonts differ between Mac and Linux).
		defer func() {
			top, fb := cd.Bounds(), foot.Bounds()
			if top.H > 0 && fb.H > 0 {
				g.measure(c, &g.contentH, fb.Y+fb.H-top.Y)
			}
		}()
		foot.Children(func() {
			if p := archetypePreview(arch); p.ok() {
				label, ic := i18n.T("Preview"), icons.Play
				if a.pl.Playing(p.id) {
					label, ic = i18n.T("Stop"), icons.Stop
				}
				if ghostIconButtonSized(c.Key("preview"), ic, label, t.FontSize*0.9).Label(label).Clicked() {
					a.previewArchetype(arch)
				}
			}
			ui.Spacer(c)
			tip := i18n.T("Save it to your voices and open it in Design")
			if !arch.Designed() {
				tip = i18n.T("Save it to your voices and open it in the Studio")
			}
			if ui.Button(c.Key("use"), i18n.T("Use voice")).Disabled(g.using).Tooltip(tip).Clicked() {
				a.useArchetype(arch, false)
			}
			moreMenu(c.Key("more"), "", func(m *ui.Menu) {
				if arch.Designed() && m.Item(i18n.T("Use in Studio")).Chosen() {
					a.useArchetype(arch, true)
				}
				a.longformVoiceItems(m, func(then func(*core.Voice)) { a.useArchetypeThen(arch, then) }) // M3 (thread #18)
				if arch.Designed() && m.Item(i18n.T("Open in Design without saving")).Chosen() {
					a.designFromArchetype(arch)
				}
			})
		})
	})
}

// designFromArchetype opens Design on a gallery voice's recipe, unsaved:
// a curated voice's persona as its description.
func (a *app) designFromArchetype(arch gallery.Archetype) {
	d := &a.design
	script := d.script
	d.reset()
	r := arch.Recipe()
	d.description, d.attrs, d.seed = r.Description, r.Attrs.Canonical(), float64(r.Seed)
	d.script, d.language = arch.PreviewLine()
	if d.script == "" {
		d.script = script
	}
	d.saveName = arch.Name.In(i18n.Locale())
	d.parsed, d.parsedFor = vd.Result{}, ""
	a.navigate("/design")
}

// archetypeTags are a gallery voice's card tags: its language, gender and
// age, use cases, and a built-in speaker's model.
func archetypeTags(arch gallery.Archetype, locale string) []string {
	var out []string
	if len(arch.Languages) > 0 {
		out = append(out, dubLangName(arch.Languages[0]))
	}
	for _, f := range []struct {
		cat vd.Category
		v   string
	}{{vd.Gender, arch.Gender}, {vd.Age, arch.Age}} {
		if op, ok := vd.Lookup(f.cat, f.v); ok {
			out = append(out, op.Label.In(locale))
		}
	}
	for _, u := range arch.Uses {
		for _, c := range gallery.Categories() {
			if c.ID == u {
				out = append(out, c.Name.In(locale))
			}
		}
	}
	if e, ok := catalog.Builtin().ByID(arch.Model); ok && arch.Kind == gallery.KindBuiltin {
		out = append(out, e.DisplayName())
	}
	return out
}

// measure keeps a card size as last laid out (a tag's height, the
// content's), and lays the grid out again when it changed: the cards'
// fixed height is made of them, so there is no slack whatever the fonts.
func (g *galleryState) measure(c *ui.Context, dst *float32, v float32) {
	if v > 0 && v != *dst {
		*dst = v
		c.Invalidate()
	}
}

// cardHeight is a Discover voices card's height: its content (top padding
// to the footer's bottom) and the bottom padding and border; a guess until
// measured.
func (g *galleryState) cardHeight(t *ui.Theme) float32 {
	if g.contentH <= 0 {
		return px(t, 158)
	}
	return g.contentH + px(t, sp3) + 1
}
