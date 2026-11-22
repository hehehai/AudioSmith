package gallery

import (
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
)

// Facet is a filter dimension of the gallery.
type Facet string

const (
	FacetCategory Facet = "category" // the use cases
	FacetGender   Facet = "gender"
	FacetAge      Facet = "age"
	FacetLanguage Facet = "language"
)

// Facets lists the facets in display order (thread #94: pitch, accent and
// style went with the earlier gallery).
var Facets = []Facet{FacetCategory, FacetGender, FacetAge, FacetLanguage}

// Page sizes.
const (
	DefaultLimit = 60
	MaxLimit     = 500
)

// Query filters and pages the gallery. Empty fields don't filter.
type Query struct {
	Text     string // every word must occur in the name, description, persona, tags or facets (en, zh or ja)
	Category string // a use case (or an earlier gallery category, mapped by CategoryID)
	Gender   string
	Age      string
	Language string // BCP-47 primary subtag among the recommended languages
	Featured bool   // featured archetypes only
	// Favorites restricts to favorites (FavoritesOnly) and is otherwise
	// unused; nil means no favorites.
	Favorites     FavoriteSet
	FavoritesOnly bool
	Offset        int
	Limit         int // 0 = DefaultLimit; capped at MaxLimit
}

// FavoriteSet answers whether an id is a favorite (*Favorites implements it).
type FavoriteSet interface{ Has(id string) bool }

// FacetCount is one value of a facet with the number of results it would
// give (counted with every other filter applied).
type FacetCount struct {
	Value string  `json:"value"`
	Label vd.Text `json:"label"`
	Count int     `json:"count"`
}

// Page is one page of results.
type Page struct {
	Items  []Archetype            `json:"items"`
	Total  int                    `json:"total"`  // matches across all pages
	Offset int                    `json:"offset"` // of Items[0]
	Next   int                    `json:"next"`   // offset of the next page, -1 = none
	Facets map[Facet][]FacetCount `json:"facets"` // values with Count > 0, in display order
}

// fold prepares text for matching: NFKC, lower case, accents kept.
func fold(s string) string {
	return strings.Map(unicode.ToLower, norm.NFKC.String(s))
}

func haystack(a Archetype) string {
	var b strings.Builder
	add := func(s string) { b.WriteString(fold(s)); b.WriteByte('\n') }
	add(a.Name.En)
	add(a.Name.Zh)
	add(a.Name.Ja)
	add(a.Description.En)
	add(a.Description.Zh)
	add(a.Description.Ja)
	add(a.Persona)
	add(a.Speaker)
	add(a.Role)
	for _, t := range a.Tags {
		add(t)
	}
	for _, u := range a.useCases() {
		if i := categoryIndex(u); i >= 0 {
			add(categories[i].Name.En)
			add(categories[i].Name.Zh)
			add(categories[i].Name.Ja)
		}
	}
	for _, c := range []vd.Category{vd.Gender, vd.Age} {
		v := a.Gender
		if c == vd.Age {
			v = a.Age
		}
		if op, ok := vd.Lookup(c, v); ok {
			add(op.Label.En)
			add(op.Label.Zh)
			add(op.Label.Ja)
		}
	}
	add(a.Attrs.Summary("en"))
	add(a.Attrs.Summary("zh"))
	add(a.Attrs.Summary("ja"))
	for _, l := range a.Languages {
		add(l)
		add(languageName(l).En)
		add(languageName(l).Zh)
		add(languageName(l).Ja)
	}
	return b.String()
}

func terms(q string) []string {
	return strings.FieldsFunc(fold(q), func(r rune) bool {
		return unicode.IsSpace(r) || r == ',' || r == '，' || r == '、' || r == ';'
	})
}

type matcher struct {
	q     Query
	terms []string
}

// useCases are a's use cases: its own (curated), or its category's.
func (a *Archetype) useCases() []string {
	if len(a.Uses) > 0 {
		return a.Uses
	}
	if c := CategoryID(a.Category); c != "" {
		return []string{c}
	}
	return nil
}

// match reports whether item i passes every filter except skip.
func (m *matcher) match(g *Gallery, i int, skip Facet) bool {
	a := &g.items[i]
	q := &m.q
	if q.Featured && !a.Featured {
		return false
	}
	if q.FavoritesOnly && (q.Favorites == nil || !q.Favorites.Has(a.ID)) {
		return false
	}
	if skip != FacetCategory && q.Category != "" && !slices.Contains(a.useCases(), CategoryID(q.Category)) {
		return false
	}
	if skip != FacetGender && q.Gender != "" && a.Gender != q.Gender {
		return false
	}
	if skip != FacetAge && q.Age != "" && a.Age != q.Age {
		return false
	}
	if skip != FacetLanguage && q.Language != "" && !slices.Contains(a.Languages, q.Language) {
		return false
	}
	for _, t := range m.terms {
		if !strings.Contains(g.hay[i], t) {
			return false
		}
	}
	return true
}

// Search filters, counts facets and returns one page. Results keep the
// gallery's order.
func (g *Gallery) Search(q Query) Page {
	m := &matcher{q: q, terms: terms(q.Text)}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	limit = min(limit, MaxLimit)
	offset := max(q.Offset, 0)

	var hits []int
	for i := range g.items {
		if m.match(g, i, "") {
			hits = append(hits, i)
		}
	}
	p := Page{Total: len(hits), Offset: offset, Next: -1, Items: []Archetype{}}
	if offset < len(hits) {
		end := min(offset+limit, len(hits))
		for _, i := range hits[offset:end] {
			p.Items = append(p.Items, g.items[i])
		}
		if end < len(hits) {
			p.Next = end
		}
	}
	p.Facets = g.facetCounts(m)
	return p
}

// facetCounts counts, for each facet, the results each of its values would
// give with all other filters kept (so picking a value never empties the
// list unexpectedly).
func (g *Gallery) facetCounts(m *matcher) map[Facet][]FacetCount {
	out := map[Facet][]FacetCount{}
	for _, f := range Facets {
		counts := map[string]int{}
		for i := range g.items {
			if !m.match(g, i, f) {
				continue
			}
			a := &g.items[i]
			switch f {
			case FacetCategory:
				for _, u := range a.useCases() {
					counts[u]++
				}
			case FacetGender:
				counts[a.Gender]++
			case FacetAge:
				counts[a.Age]++
			case FacetLanguage:
				for _, l := range a.Languages {
					counts[l]++
				}
			}
		}
		delete(counts, "")
		var list []FacetCount
		for _, v := range facetValues(f) {
			if n := counts[v.Value]; n > 0 {
				v.Count = n
				list = append(list, v)
			}
		}
		out[f] = list
	}
	return out
}

// facetValues lists every value of a facet in display order, with labels.
func facetValues(f Facet) []FacetCount {
	var out []FacetCount
	fromCat := func(c vd.Category) {
		for _, op := range vd.ValuesOf(c) {
			out = append(out, FacetCount{Value: op.ID, Label: op.Label})
		}
	}
	switch f {
	case FacetCategory:
		for _, c := range categories {
			out = append(out, FacetCount{Value: c.ID, Label: c.Name})
		}
	case FacetGender:
		fromCat(vd.Gender)
	case FacetAge:
		fromCat(vd.Age)
	case FacetLanguage:
		for _, l := range Languages {
			out = append(out, FacetCount{Value: l, Label: languageName(l)})
		}
	}
	return out
}

// FacetValues lists every value of a facet in display order (Count = 0),
// for building the filter UI before any search.
func FacetValues(f Facet) []FacetCount { return facetValues(f) }

var languageNames = map[string]vd.Text{
	"en": {En: "English", Zh: "英语"}, "zh": {En: "Chinese", Zh: "中文"}, "es": {En: "Spanish", Zh: "西班牙语"},
	"fr": {En: "French", Zh: "法语"}, "de": {En: "German", Zh: "德语"}, "it": {En: "Italian", Zh: "意大利语"},
	"pt": {En: "Portuguese", Zh: "葡萄牙语"}, "ru": {En: "Russian", Zh: "俄语"}, "hi": {En: "Hindi", Zh: "印地语"},
	"ja": {En: "Japanese", Zh: "日语"}, "ko": {En: "Korean", Zh: "韩语"},
}

func languageName(code string) vd.Text {
	if t, ok := languageNames[code]; ok {
		return t
	}
	return vd.Text{En: code, Zh: code}
}
