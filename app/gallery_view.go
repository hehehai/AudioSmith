package main

// 音色 · 发现 (/voices/discover, 0.5 F4; after ElevenLabs' Explore): the
// header (title, ‹ ›, count, search, ☆, 「+ 新建音色」), the Voices tabs,
// then the filters — a language menu, gender and age, and the use cases
// as chips — and the voices: 「推荐」, a three-column grid of the
// strongest voices for the interface's language (while nothing filters),
// and 「全部」, a table (name and description, language, gender and age,
// use, ▶, 「使用」, ⋯). A click on a row opens the voice's detail beside
// the table, or over it when the window is narrow (voice_detail.go).

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

// galleryFacets are the facets offered as menus (the use cases are
// chips).
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

// recommendedVoices are 「推荐」's voices for an interface language: the
// curated voices that #91/#94's listening rated best, two per use case
// where there are, in that language's voices first.
var recommendedVoices = map[string][]string{
	"zh": {"serena", "headline-desk", "paper-lantern", "warm-uncle", "grandma-osmanthus", "dylan"},
	"en": {"af_heart", "late-night-radio", "history-vault", "interviewer", "velvet-chapters", "trailer-thunder"},
}

// recommended are 「推荐」's voices for the interface's language.
func recommended() []gallery.Archetype {
	ids := recommendedVoices["en"]
	if strings.HasPrefix(i18n.Locale(), "zh") {
		ids = recommendedVoices["zh"]
	}
	var out []gallery.Archetype
	for _, id := range ids {
		if arch, ok := gallery.Default().ByID(gallery.CuratedPrefix + id); ok {
			out = append(out, arch)
		} else if arch, ok := gallery.Default().ByID(id); ok {
			out = append(out, arch)
		}
	}
	return out
}

// galleryFiltered reports whether anything narrows the voices (then
// 「推荐」 steps aside).
func (g *galleryState) filtered() bool {
	return strings.TrimSpace(g.text) != "" || g.tab != 0 || len(g.filters) > 0 || g.favOnly
}

// galleryPage is /voices/discover.
func (a *app) galleryPage(c *ui.Context) {
	t := c.Theme()
	g := &a.gallery
	page := a.galleryResults()
	pageHeaderBar(c, pageHead{
		title: i18n.T("Discover voices"),
		// The count sits at the right of the filters' row (user, 10-31).
		search: &headSearch{key: "gallery-search", value: &g.text, placeholder: i18n.T("Search: narrator, 东北, grandma…"),
			label:   i18n.T("Search Discover voices"),
			changed: func() { g.limit = galleryPageSize }},
		tools: func(bool) {
			if iconToggle(c.Key("gallery-favs"), icons.Star, g.favOnly, i18n.T("Favorites")).Tooltip(i18n.T("Show only your favorites")).Clicked() {
				g.favOnly = !g.favOnly
				g.limit = galleryPageSize
			}
		},
		primary: a.newVoiceButton(c),
	})
	arch, open := gallery.Default().ByID(g.detail)
	ui.Row(c.Key("gallery-body")).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		avail := a.pageWidth(c)
		narrow := avail < px(t, 700)
		ui.Scroll(c.Key("gallery-scroll")).Grow(1).MinWidth(0).Children(func() {
			ui.Column(c).Padding(px(t, sp2), px(t, sp6), px(t, sp6)).Gap(px(t, sp3)).Children(func() {
				a.galleryFilters(c, page)
				if len(page.Items) == 0 {
					ghostRows(c, 3, icons.Widget, i18n.T("No voices match these filters."), "")
					return
				}
				if !g.filtered() {
					a.galleryRecommended(c, narrow, avail-2*px(t, sp6))
					sectionRow(c, "all-title", i18n.T("All voices"), itoa(page.Total))
				}
				a.galleryTable(c, page.Items, narrow)
			})
		})
		var build func()
		if open {
			build = func() { a.archetypeDetail(c, arch) }
		}
		a.floating(c, floatPanel{key: "voice-detail", label: i18n.T("Voice details"), w: px(t, voiceDetailW), item: g.detail,
			close: func() { g.detail = "" }}, open, build)
	})
}

// ifThen is v when ok, else w.
func ifThen(ok bool, v, w float32) float32 {
	if ok {
		return v
	}
	return w
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

// galleryFilters are the language, gender and age menus, a hairline, and
// the use cases as chips (All first); Reset while anything filters.
func (a *app) galleryFilters(c *ui.Context, page gallery.Page) {
	t := c.Theme()
	k := tok(t)
	g := &a.gallery
	loc := i18n.Locale()
	counts := galleryCounts(page)
	ui.Row(c.Key("gallery-facets")).Gap(px(t, sp1)).Wrap().AlignItems(ui.Center).Children(func() {
		for _, f := range galleryFacets {
			a.facetMenu(c, f, page.Facets[f])
		}
		ui.Box(c).Size(1, px(t, 18)).Background(k.Line).Margin(0, px(t, sp1)).Shrink(0)
		if chip(c.Key("use-all"), i18n.T("All uses"), "", g.tab == 0).Clicked() {
			g.tab, g.limit = 0, galleryPageSize
		}
		for i, cat := range gallery.Categories() {
			on := g.tab == i+1
			n := ""
			if counts[cat.ID] > 0 || on {
				n = itoa(counts[cat.ID])
			}
			if chip(c.Key("use-"+cat.ID), cat.Name.In(loc), n, on).Tooltip(cat.Description.In(loc)).Clicked() {
				if on {
					g.tab = 0
				} else {
					g.tab = i + 1
				}
				g.limit = galleryPageSize
			}
		}
		if g.filtered() {
			if quietButton(c.Key("gallery-reset"), icons.Restart, i18n.T("Reset")).Clicked() {
				g.text, g.tab, g.favOnly, g.limit = "", 0, false, galleryPageSize
				clear(g.filters)
			}
		}
		ui.Box(c).Grow(1).MinWidth(px(t, sp2))
		n := i18n.N(page.Total, "%d voice", "%d voices", page.Total)
		ui.Text(c.Key("gallery-count"), n).FontSize(px(t, fsCaption)).TextColor(k.Text3).SingleLine().Shrink(0)
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

// galleryRecommended is 「推荐」: compact rows in three columns (two, then
// one, as the page narrows: then only three): avatar, name, a line of
// description, the language and the first use. A click opens the
// detail; ▶ previews.
func (a *app) galleryRecommended(c *ui.Context, narrow bool, avail float32) {
	t := c.Theme()
	sectionRow(c, "rec-title", i18n.T("Recommended"), "")
	recs := recommended()
	if narrow {
		recs = recs[:min(len(recs), 3)] // one column: the first three
	}
	// Fixed columns: every card has the same width, a short last row
	// included (user, 10-31: cards must not widen when fewer are left).
	cols := recColumns(avail, px(t, recCardMin), px(t, sp2))
	if narrow {
		cols = 1
	}
	ui.Column(c.Key("rec-grid")).Gap(px(t, sp2)).Children(func() {
		for r := 0; r < len(recs); r += cols {
			row := recs[r:min(r+cols, len(recs))]
			ui.Row(c.Key("rec-row-" + itoa(r))).Gap(px(t, sp2)).Children(func() {
				for _, arch := range row {
					a.recCard(c, arch)
				}
				for range cols - len(row) {
					ui.Box(c).Grow(1).Basis(0).MinWidth(0) // keeps the columns' widths
				}
			})
		}
	})
}

// recCardMin is a recommended card's narrowest width, DIPs at 100 %.
const recCardMin = 240

// recColumns is how many recommended cards fit a row avail wide: cards
// at least min wide, gap apart; 1 to 4.
func recColumns(avail, least, gap float32) int {
	return max(1, min(4, int((avail+gap)/(least+gap))))
}

// recCard is one recommended voice: avatar, name, a line of description,
// the language and the first use; a click opens the detail, ▶ previews.
func (a *app) recCard(c *ui.Context, arch gallery.Archetype) {
	t := c.Theme()
	k := tok(t)
	g := &a.gallery
	loc := i18n.Locale()
	p := archetypePreview(arch)
	playing := p.ok() && a.pl.Playing(p.id)
	cell := ui.Row(c.Key("rec-" + arch.ID)).Gap(px(t, sp2)).AlignItems(ui.Center).Padding(px(t, sp2)).Radius(px(t, radiusCard)).
		Grow(1).Basis(0).MinWidth(0)
	switch {
	case g.detail == arch.ID:
		cell.Background(k.Selected)
	case playing:
		cell.Background(k.AccentSoft)
	case cell.Hovered():
		cell.Background(k.Hover)
	}
	cell.Children(func() {
		name := arch.Name.In(loc)
		b := ui.ButtonBase(c.Key("open")).Grow(1).MinWidth(0).Gap(px(t, sp2)).AlignItems(ui.Center).Label(name).Tooltip(i18n.T("Open the voice's details"))
		b.Children(func() {
			ui.Avatar(c, name, nil).Size(px(t, 40), px(t, 40)).Shrink(0)
			ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 1)).Children(func() {
				ui.Text(c, name).FontWeight(600).SingleLine().Ellipsis("…").MinWidth(0)
				ui.Text(c, arch.Description.In(loc)).FontSize(px(t, fsCaption)).TextColor(k.Text2).SingleLine().Ellipsis("…").MinWidth(0)
				ui.Text(c, archLanguage(arch)+" · "+archUse(arch, loc)).FontSize(px(t, fsMicro)).TextColor(k.Text3).SingleLine().Ellipsis("…").MinWidth(0)
			})
		})
		if b.Clicked() {
			a.openArchetype(arch.ID)
		}
		if p.ok() && previewToggle(c, "play", playing).Clicked() {
			a.previewArchetype(arch)
		}
	})
}

// galleryTable is 「全部」: the voices as a table.
func (a *app) galleryTable(c *ui.Context, items []gallery.Archetype, narrow bool) {
	g := &a.gallery
	loc := i18n.Locale()
	ui.Column(c.Key("gallery-table")).Children(func() {
		// No column titles: the rows say what they are (user, 10-31).
		for _, arch := range items {
			p := archetypePreview(arch)
			playing := p.ok() && a.pl.Playing(p.id)
			name := arch.Name.In(loc)
			st := rowState{selected: g.detail == arch.ID, playing: playing}
			if voiceRow(c, "arch-"+arch.ID, name, st, func() {
				voiceNameCell(c, name, arch.Description.In(loc), nil, playing, func() {
					if g.favs.Has(arch.ID) {
						iconMark(c, icons.Star, tok(c.Theme()).Text3, i18n.T("Favorite"))
					}
				})
				metaCell(c, noIcon, i18n.T("Language"), archLanguage(arch), 100) // its flag leads
				if !narrow {
					metaCell(c, icons.UserRounded, i18n.T("Gender and age"), archGenderAge(arch, loc), 120)
					more := len(archUseNames(arch, loc)) - 1
					metaCellExtra(c, icons.Tag, "", archUse(arch, loc), 136, func() { countPill(c, more) }).Tooltip(i18n.T("%s: %s", i18n.T("Use cases"), archUses(arch, loc)))
				}
			}, func() {
				if p.ok() && previewToggle(c, "play", playing).Clicked() {
					a.previewArchetype(arch)
				}
				if useButton(c, "use", i18n.T("Use it in Text to speech")).Disabled(g.using).Clicked() {
					a.useArchetype(arch, true)
				}
				a.archetypeMenu(c, arch, "")
			}) {
				a.openArchetype(arch.ID)
			}
		}
	})
}

// archetypeMenu is a Discover voice's ⋯: save it, Stories, Audiobook,
// Design without saving, favorite; tip names it ("" More actions).
func (a *app) archetypeMenu(c *ui.Context, arch gallery.Archetype, tip string) {
	g := &a.gallery
	moreMenu(c.Key("more"), tip, func(m *ui.Menu) {
		saved := a.savedFrom(arch.ID) != nil
		if m.Item(i18n.T("Save to My voices")).Disabled(saved || g.using).Chosen() {
			a.saveArchetype(arch)
		}
		a.longformVoiceItems(m, func(then func(*core.Voice)) { a.useArchetypeThen(arch, then) }) // M3 (thread #18)
		if arch.Designed() && m.Item(i18n.T("Open in Design without saving")).Chosen() {
			a.designFromArchetype(arch)
		}
		m.Separator()
		fav := i18n.T("Add to favorites")
		if g.favs.Has(arch.ID) {
			fav = i18n.T("Remove from favorites")
		}
		if m.Item(fav).Chosen() {
			a.toggleFavorite(arch.ID)
		}
	})
}

// openArchetype opens a Discover voice's detail (or closes it, a second
// click).
func (a *app) openArchetype(id string) {
	g := &a.gallery
	if g.detail == id {
		a.dismissPanel("voice-detail")()
		return
	}
	g.detail = id
	g.try = voiceTry{}
}

// savedFrom is the saved voice made from Discover voice id, or nil.
func (a *app) savedFrom(id string) *core.Voice {
	origin := galleryOrigin(id)
	for i := range a.voices {
		if a.voices[i].Origin == origin {
			return &a.voices[i]
		}
	}
	return nil
}

// saveArchetype is 「保存到我的音色」: saves it (once) and says so.
func (a *app) saveArchetype(arch gallery.Archetype) {
	a.useArchetypeThen(arch, func(*core.Voice) {})
}

// archLanguage names a Discover voice's language.
func archLanguage(arch gallery.Archetype) string {
	if len(arch.Languages) == 0 {
		return ""
	}
	return dubLangName(arch.Languages[0])
}

// archGenderAge is a Discover voice's gender and age.
func archGenderAge(arch gallery.Archetype, loc string) string {
	var out []string
	if op, ok := vd.Lookup(vd.Gender, arch.Gender); ok {
		out = append(out, op.Label.In(loc))
	}
	if op, ok := vd.Lookup(vd.Age, arch.Age); ok {
		out = append(out, op.Label.In(loc))
	}
	return strings.Join(out, " · ")
}

// archUse names a Discover voice's first use case; archUses all of them.
func archUse(arch gallery.Archetype, loc string) string {
	if u := archUseNames(arch, loc); len(u) > 0 {
		return u[0]
	}
	return ""
}

func archUses(arch gallery.Archetype, loc string) string {
	return strings.Join(archUseNames(arch, loc), i18n.T(", "))
}

func archUseNames(arch gallery.Archetype, loc string) []string {
	var out []string
	for _, u := range arch.Uses {
		for _, cat := range gallery.Categories() {
			if cat.ID == u {
				out = append(out, cat.Name.In(loc))
			}
		}
	}
	return out
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
	a.navigate("/voices/new/design")
}

// archetypeTags are a gallery voice's tags: its language, gender and age,
// use cases, and a built-in speaker's model.
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
	out = append(out, archUseNames(arch, locale)...)
	if e, ok := catalog.Builtin().ByID(arch.Model); ok && arch.Kind == gallery.KindBuiltin {
		out = append(out, e.DisplayName())
	}
	return out
}
