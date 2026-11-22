package gallery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"testing"

	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
)

// TestDefault: the curated set (thread #94) is small and complete: about
// 35 voices, Chinese and English first, every use case, gender and the
// main ages present, each designed voice with its persona, a tested seed,
// a preview line and its reference's text.
func TestDefault(t *testing.T) {
	g := Default()
	if n := g.Len(); n < 40 || n > 50 {
		t.Errorf("Len = %d, want about 45", n)
	}
	langs, uses, kinds := map[string]int{}, map[string]int{}, map[string]int{}
	namesEn, namesZh := map[string]string{}, map[string]string{}
	for _, a := range g.All() {
		if !a.Curated() || a.Legacy || !a.Featured {
			t.Errorf("%s: not curated", a.ID)
		}
		kinds[a.Kind]++
		langs[a.Languages[0]]++
		for _, u := range a.Uses {
			uses[u]++
		}
		if prev, dup := namesEn[a.Name.En]; dup {
			t.Errorf("duplicate English name %q: %s and %s", a.Name.En, prev, a.ID)
		}
		if prev, dup := namesZh[a.Name.Zh]; dup {
			t.Errorf("duplicate Chinese name %q: %s and %s", a.Name.Zh, prev, a.ID)
		}
		namesEn[a.Name.En], namesZh[a.Name.Zh] = a.ID, a.ID
		if a.Kind != KindDesigned {
			continue
		}
		// The persona is what the model gets, as written: no slow pace
		// (it ran past the model's length limit, #91) and no fillers.
		low, err := vd.Lower(vd.TargetQwen3Design, a.Recipe(), vd.Options{Lang: vd.TextLang(a.Persona)})
		if err != nil || low.Voice != a.Persona {
			t.Errorf("%s: lowered to %q", a.ID, low.Voice)
		}
		for _, w := range []string{"慢", "slow", "unhurried", "嗯", "呃", "um,", "uh,"} {
			if strings.Contains(strings.ToLower(a.Persona), w) {
				t.Errorf("%s: persona has %q", a.ID, w)
			}
		}
		// What the persona says agrees with the facets (the Design page
		// shows those attributes; a child gets the children's reference
		// texts when its reference is made again).
		if p := vd.Parse(a.Persona).Attrs; p.Gender != "" && p.Gender != a.Gender || p.Age != "" && p.Age != a.Age {
			t.Errorf("%s: the persona says %s/%s, the facets %s/%s", a.ID, p.Gender, p.Age, a.Gender, a.Age)
		}
		if (a.Age == "child") != (a.Attrs.Register() == vd.RegisterChild) {
			t.Errorf("%s: register %s for age %s", a.ID, a.Attrs.Register(), a.Age)
		}
		if line, lang := a.PreviewLine(); line == "" || lang != a.Languages[0] {
			t.Errorf("%s: preview line %q in %s", a.ID, line, lang)
		}
		if a.Seed == vd.SeedFrom(a.ID) {
			t.Errorf("%s: seed derived from the id, not tested", a.ID)
		}
	}
	if langs["zh"] < 10 || langs["en"] < 10 || len(langs) < 6 {
		t.Errorf("languages %v", langs)
	}
	for _, c := range Categories() {
		if uses[c.ID] < 4 {
			t.Errorf("use case %s has %d voices", c.ID, uses[c.ID])
		}
	}
	if kinds[KindDesigned] < 20 || kinds[KindBuiltin] < 5 {
		t.Errorf("kinds %v", kinds)
	}
	if len(Categories()) != 4 {
		t.Errorf("%d use cases", len(Categories()))
	}
}

// TestLegacy: the earlier gallery (1,100-odd voices) is not listed, but
// its ids resolve, hidden ones too, with their recipes as before.
func TestLegacy(t *testing.T) {
	items, err := BuildLegacy()
	if err != nil {
		t.Fatal(err)
	}
	if n := len(items); n < 1000 || n > 1300 {
		t.Errorf("legacy %d", n)
	}
	var ids []string
	for _, a := range items {
		if !a.Legacy || a.Curated() || a.Gender == "" || a.Age == "" {
			t.Fatalf("%s: %+v", a.ID, a)
		}
		ids = append(ids, a.ID)
	}
	g := Default()
	for _, id := range []string{"f-grandma-osmanthus", "f-monsoon-stories", "f-elder-sage", "g-quest-giver-gravelly-zh-mm", ids[len(ids)-1]} {
		a, ok := g.ByID(id)
		if !ok || !a.Legacy || a.Recipe().Description != "" || a.Recipe().Seed != vd.SeedFrom(id) {
			t.Errorf("%s: %v %+v", id, ok, a.Recipe())
		}
		if g.Search(Query{Text: id}).Total != 0 {
			t.Errorf("%s is listed", id)
		}
	}
	// The earlier ids must not change: saved voices, dub casts and agents
	// name them.
	sum := sha256.Sum256([]byte(strings.Join(ids, "\n")))
	if got := hex.EncodeToString(sum[:8]); got != goldenLegacyIDs {
		t.Errorf("legacy ids changed: hash %s, want %s (%d ids)", got, goldenLegacyIDs, len(ids))
	}
	if _, ok := g.ByID("nope"); ok {
		t.Error("unknown id resolves")
	}
}

const goldenLegacyIDs = "651283ca2f7bd2c7" // the 1,184 ids as the gallery had them before #93 hid one

func TestNewValidates(t *testing.T) {
	var good Archetype
	for _, a := range Default().All() {
		if a.Kind == KindDesigned {
			good = a
			break
		}
	}
	builtin := Default().All()[0]
	bad := func(base Archetype, f func(a *Archetype)) Archetype { a := base; f(&a); return a }
	for name, a := range map[string]Archetype{
		"no prefix":    bad(good, func(a *Archetype) { a.ID = "grandma" }),
		"use":          bad(good, func(a *Archetype) { a.Uses = []string{"cooking"} }),
		"no use":       bad(good, func(a *Archetype) { a.Uses = nil }),
		"name zh":      bad(good, func(a *Archetype) { a.Name.Zh = "" }),
		"gender":       bad(good, func(a *Archetype) { a.Gender = "robot" }),
		"language":     bad(good, func(a *Archetype) { a.Languages = []string{"xx"} }),
		"no persona":   bad(good, func(a *Archetype) { a.Persona = "" }),
		"no seed":      bad(good, func(a *Archetype) { a.Seed = 0 }),
		"no ref":       bad(good, func(a *Archetype) { a.RefText = "" }),
		"poor ref":     bad(good, func(a *Archetype) { a.RefGrade = "poor" }),
		"no line":      bad(good, func(a *Archetype) { a.Line = "" }),
		"kind":         bad(good, func(a *Archetype) { a.Kind = "cloned" }),
		"no speaker":   bad(builtin, func(a *Archetype) { a.Speaker = "" }),
		"speaker+desc": bad(builtin, func(a *Archetype) { a.Persona = "x" }),
	} {
		if _, err := New([]Archetype{a}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := New([]Archetype{good, good}); err == nil {
		t.Error("duplicate id accepted")
	}
}

func TestSearch(t *testing.T) {
	g := Default()
	all := g.Search(Query{})
	if all.Total != g.Len() || len(all.Items) != g.Len() || all.Next != -1 {
		t.Errorf("all: total %d items %d next %d", all.Total, len(all.Items), all.Next)
	}
	if len(Facets) != 4 || len(all.Facets) != 4 {
		t.Errorf("facets %v", Facets)
	}
	p := g.Search(Query{Category: "narration", Gender: "female", Language: "en"})
	if p.Total == 0 {
		t.Fatal("no English female narrators")
	}
	for _, a := range p.Items {
		if !slices.Contains(a.Uses, "narration") || a.Gender != "female" || a.Languages[0] != "en" {
			t.Fatalf("filter leak: %+v", a)
		}
	}
	// disjunctive facets: the gender facet still counts men
	var male int
	for _, c := range p.Facets[FacetGender] {
		if c.Value == "male" {
			male = c.Count
		}
	}
	if male == 0 || g.Search(Query{Category: "narration", Gender: "male", Language: "en"}).Total != male {
		t.Errorf("gender facet %d", male)
	}
	// An earlier category maps to its use case.
	if a, b := g.Search(Query{Category: "news"}).Total, g.Search(Query{Category: "announcing"}).Total; a != b || a == 0 {
		t.Errorf("news %d, announcing %d", a, b)
	}
	if CategoryID("cooking") != "" || CategoryID("games") != "characters" {
		t.Error("CategoryID")
	}
	// paging
	p1 := g.Search(Query{Limit: 10})
	p2 := g.Search(Query{Limit: 10, Offset: p1.Next})
	if p1.Next != 10 || len(p2.Items) != 10 || p1.Items[9].ID == p2.Items[0].ID {
		t.Errorf("paging: next %d", p1.Next)
	}
	// text search, English and Chinese, across names, personas, use
	// cases, facets and speakers
	for q, want := range map[string]string{
		"四川":          "c-chengdu-teahouse",
		"桂花":          "c-grandma-osmanthus",
		"grandmother": "c-wise-mentor",
		"af_heart":    "c-af_heart",
		"老年 女":        "c-grandma-osmanthus",
		"播客":          "c-kitchen-table",
		"russian":     "c-taiga-tales",
	} {
		r := g.Search(Query{Text: q})
		if !slices.ContainsFunc(r.Items, func(a Archetype) bool { return a.ID == want }) {
			t.Errorf("%q: no %s in %d results", q, want, r.Total)
		}
	}
	if r := g.Search(Query{Text: "zzzz-nothing"}); r.Total != 0 {
		t.Error("nonsense found something")
	}
	if r := g.Search(Query{FavoritesOnly: true}); r.Total != 0 {
		t.Error("favorites without a set")
	}
	if len(FacetValues(FacetLanguage)) != len(Languages) {
		t.Error("language facet values")
	}
}

func TestFavorites(t *testing.T) {
	ctx := context.Background()
	store := &MemoryFavorites{}
	f, err := LoadFavorites(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	g := Default()
	id := g.All()[3].ID
	if on, err := f.Toggle(ctx, id); err != nil || !on || !f.Has(id) {
		t.Fatalf("toggle on: %v %v", on, err)
	}
	r := g.Search(Query{FavoritesOnly: true, Favorites: f})
	if r.Total != 1 || r.Items[0].ID != id {
		t.Errorf("favorites search: %+v", r.Total)
	}
	f2, _ := LoadFavorites(ctx, store)
	if !slices.Equal(f2.List(), []string{id}) {
		t.Errorf("reload: %v", f2.List())
	}
	store.Err = errors.New("disk full")
	if err := f.Set(ctx, "x", true); err == nil || f.Has("x") {
		t.Error("store error not reported or set changed")
	}
	if _, err := LoadFavorites(ctx, store); err == nil {
		t.Error("load error not reported")
	}
	store.Err = nil
	if on, _ := f.Toggle(ctx, id); on || f.Has(id) {
		t.Error("toggle off")
	}
	var nilFav *Favorites
	if nilFav.Has(id) {
		t.Error("nil favorites")
	}
}

// TestPresets: eight starting points (thread #94: 旁白, 聊天, 新闻播报,
// 商务演示, 讲故事, 长者, 反派 and 轻声, which replaced 耳语 and 冥想引导),
// none slow; the ones no longer offered still resolve.
func TestPresets(t *testing.T) {
	ps := Presets()
	var ids []string
	demos := 0
	for _, p := range ps {
		ids = append(ids, p.ID)
		if p.Demo {
			demos++
		}
		if p.Hidden || p.Name.En == "" || p.Name.Zh == "" || p.Description.Zh == "" || p.StyleHint.En == "" || p.StyleHint.Zh == "" ||
			p.Script.En == "" || p.Script.Zh == "" || p.Icon == "" || (p.Language != "en" && p.Language != "zh") {
			t.Errorf("%s: incomplete", p.ID)
		}
		if err := p.Attrs.Validate(); err != nil || p.Attrs.IsAuto() {
			t.Errorf("%s: %v", p.ID, err)
		}
		if p.Attrs.Pace == "slow" || p.Attrs.Pace == "very_slow" || strings.Contains(p.StyleHint.En, "slow") || strings.Contains(p.StyleHint.Zh, "慢") {
			t.Errorf("%s: slow", p.ID)
		}
		if r := p.Recipe(42); r.Seed != 42 || r.Validate() != nil {
			t.Errorf("%s: recipe", p.ID)
		}
	}
	if want := []string{"narrator", "conversational", "newsreader", "presenter", "storyteller", "elder", "villain", "soft"}; !slices.Equal(ids, want) {
		t.Errorf("presets %v, want %v", ids, want)
	}
	if demos < 4 {
		t.Errorf("%d demos", demos)
	}
	for _, id := range []string{"narrator", "whisper", "kid", "upbeat", "northeast-buddy", "calm-guide"} {
		if p, ok := PresetByID(id); !ok || p.Attrs.IsAuto() {
			t.Errorf("PresetByID(%s)", id)
		}
	}
}

func BenchmarkSearch(b *testing.B) {
	g := Default()
	for b.Loop() {
		g.Search(Query{Text: "warm", Gender: "female"})
	}
}
