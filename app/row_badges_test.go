package main

// Behaviour tests of thread #129: a click on a row's meta badge filters
// the list by its value (another click clears it) and never opens the
// row; a row's ▶, 「使用」 and ⋯ wear a face under the pointer that shows
// on the row's own tint; every Discover card and row and every Mine row
// has a ▶; the narrow window keeps the actions.

import (
	"context"
	"image/color"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/gallery"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
)

// clickBadge clicks the badge labelled for value (on: the clearing one).
func clickBadge(t *testing.T, tt *ui.Tester, value string, on bool) {
	t.Helper()
	if err := tt.Click(badgeTip(value, on)); err != nil {
		t.Fatalf("%v: %q", err, tt.Texts())
	}
	tt.Frame()
	tt.Frame()
}

// Discover: the language, gender·age and use badges of 「全部」's rows
// and of 「推荐」's cards set the filters; the detail stays shut.
func TestDiscoverBadgesFilter(t *testing.T) {
	a, _ := newVoiceTestApp(t)
	tt := ui.NewTester(a.view, 1300, 1800)
	g := &a.gallery
	a.navigate("/voices/discover")
	tt.Frame()
	// A recommended card's language (the first badge of the page).
	rec := recommended()[0]
	clickBadge(t, tt, archLanguage(rec), false)
	if g.filters[gallery.FacetLanguage] != rec.Languages[0] || g.detail != "" {
		t.Fatalf("card language: %v, detail %q", g.filters, g.detail)
	}
	if tt.HasText("Recommended") {
		t.Error("「推荐」 stays while a filter is set")
	}
	for _, it := range g.page.Items {
		if it.Languages[0] != rec.Languages[0] {
			t.Fatalf("%s speaks %v", it.ID, it.Languages)
		}
	}
	// Again (now in the table's first row): clears it.
	clickBadge(t, tt, archLanguage(rec), true)
	if len(g.filters) != 0 || g.detail != "" {
		t.Fatalf("cleared: %v %q", g.filters, g.detail)
	}

	// The table: gender and age (one badge, both menus).
	first := g.page.Items[0]
	clickBadge(t, tt, archGenderAge(first, i18n.Locale()), false)
	if g.filters[gallery.FacetGender] != first.Gender || g.filters[gallery.FacetAge] != first.Age || g.detail != "" {
		t.Fatalf("gender: %v %q", g.filters, g.detail)
	}
	for _, it := range g.page.Items {
		if it.Gender != first.Gender || it.Age != first.Age {
			t.Fatalf("%s is %s %s", it.ID, it.Gender, it.Age)
		}
	}
	if !tt.HasText(i18n.T("Gender") + ": ") { // the menu says what it filters by
		t.Errorf("gender menu: %q", tt.Texts())
	}
	clear(g.filters)
	g.text = first.Name.In(i18n.Locale()) // the table alone
	tt.Frame()
	tt.Frame()
	// The use: the chip.
	use := archUse(first, i18n.Locale())
	clickBadge(t, tt, use, false)
	if g.tab != archUseTab(first) || g.tab == 0 || g.detail != "" {
		t.Fatalf("use: tab %d, detail %q", g.tab, g.detail)
	}
	clickBadge(t, tt, use, true)
	if g.tab != 0 {
		t.Errorf("use cleared: %d", g.tab)
	}
	// The row's body still opens the detail.
	tt.Click(first.Name.In(i18n.Locale()))
	tt.Frame()
	if g.detail != first.ID {
		t.Errorf("row: detail %q", g.detail)
	}
}

// Mine: the kind badge sets the type filter, the language badge the
// language filter (shown over the table while it filters); the editor
// stays shut.
func TestMineBadgesFilter(t *testing.T) {
	a, _ := newVoiceTestApp(t)
	tt := ui.NewTester(a.view, 1300, 1800)
	a.navigate("/voices/mine")
	addVoice(t, a, core.Voice{Name: "Narrator", Kind: core.VoiceCloned, RefText: "Once upon a time.", Language: "en"}, 6)
	addVoice(t, a, core.Voice{Name: "Grandma", Kind: core.VoiceDesigned, Language: "zh", DesignAttrs: designAttrs(vd.Attributes{Gender: "female", Age: "elderly"}), Seed: 7}, 0)
	addVoice(t, a, core.Voice{Name: "Heart", Kind: core.VoiceBuiltin, Model: kokoro, Speaker: "af_heart", Language: "en", Origin: "gallery:c-af_heart"}, 0)
	tt.Frame()
	tt.Frame()
	clickBadge(t, tt, i18n.T("Designed"), false)
	if a.voiceKind != kindDesigned || a.edit != nil {
		t.Fatalf("kind %d, editor %v", a.voiceKind, a.edit != nil)
	}
	clickBadge(t, tt, i18n.T("Designed"), true)
	if a.voiceKind != kindAll {
		t.Fatalf("kind cleared: %d", a.voiceKind)
	}
	clickBadge(t, tt, langName("en"), false)
	if a.voiceLang != "en" || a.edit != nil {
		t.Fatalf("language %q, editor %v", a.voiceLang, a.edit != nil)
	}
	var names []string
	for _, v := range a.shownVoices() {
		names = append(names, v.Name)
	}
	if !slices.Equal(names, []string{"Heart", "Narrator"}) || tt.HasText("Grandma") {
		t.Errorf("filtered: %v", names)
	}
	if !tt.HasText(i18n.T("%s: %s", i18n.T("Language"), langName("en"))) {
		t.Errorf("no language filter shows: %q", tt.Texts())
	}
	// Reset clears it.
	if err := tt.Click(i18n.T("Reset")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.voiceLang != "" || !tt.HasText("Grandma") {
		t.Errorf("reset: %q", a.voiceLang)
	}
}

// Projects: the type badge sets the type filter; the project stays shut.
// Assets: the source badge sets the source filter.
func TestProjectsAssetsBadgesFilter(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.SetSize(1300, 1400)
	for _, pr := range []struct {
		kind  core.ProjectKind
		title string
	}{{core.ProjectStory, "Night Ferry"}, {core.ProjectAudiobook, "Book One"}} {
		doc := services.NewLongformDoc(pr.kind, kokoro)
		doc.Title = pr.title
		doc.Lines = append(doc.Lines, services.StoryLine{ID: "l0", Text: "Line"})
		a.lib.svc.Library.SaveDoc(context.Background(), core.Project{Kind: pr.kind, Title: pr.title}, doc)
	}
	addTake(t, a, core.Take{ID: "T1", Text: "Hello there.", Model: kokoro, Voice: "af_heart", AudioSeconds: 1}, 0.3)
	a.projects.dirty = true
	a.navigate("/projects")
	tt.Frame()
	tt.Frame()
	clickBadge(t, tt, i18n.T("Audiobook"), false)
	if projFilters[a.projects.filter] != projBook || a.router.Path() != "/projects" {
		t.Fatalf("type %d, at %s", a.projects.filter, a.router.Path())
	}
	if tt.HasText("Night Ferry") || !tt.HasText("Book One") {
		t.Errorf("filtered: %q", tt.Texts())
	}
	clickBadge(t, tt, i18n.T("Audiobook"), true)
	if a.projects.filter != 0 {
		t.Errorf("cleared: %d", a.projects.filter)
	}

	a.navigate("/assets")
	tt.Frame()
	tt.Frame()
	clickBadge(t, tt, pageTitle("/studio"), false)
	if assetSources[a.history.source] != "/studio" || a.history.selected != "" {
		t.Fatalf("source %d, selected %q", a.history.source, a.history.selected)
	}
}

// faceShows hovers the element labelled label (the first) and reports
// whether its face differs from the row's tint around it: a pixel inside
// its left edge against one just above it.
func faceShows(t *testing.T, tt *ui.Tester, w int, label string) bool {
	t.Helper()
	r, ok := tt.Find(label)
	if !ok {
		t.Fatalf("no %q: %q", label, tt.Texts())
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	img := tt.Image()
	s := float32(img.Bounds().Dx()) / float32(w)
	y := int((r.Y + r.H/2) * s)
	in := img.At(int((r.X+4)*s), y)
	out := img.At(int((r.X+4)*s), int((r.Y-3)*s))
	d := luma(in) - luma(out)
	return d > 4 || d < -4 // darker in the light theme, lighter in the dark
}

func luma(c color.Color) int {
	r, g, b, _ := c.RGBA()
	return int((299*r + 587*g + 114*b) / 1000 >> 8)
}

// A row's ▶, 「使用」 and ⋯ wear a face on the row's tint under the
// pointer (Discover, Mine, Projects), in both themes.
func TestRowButtonsHoverFace(t *testing.T) {
	for _, dark := range []bool{false, true} {
		a, _ := newVoiceTestApp(t)
		tt := ui.NewTester(a.view, 1300, 1800)
		tt.SetDark(dark)
		g := &a.gallery
		a.navigate("/voices/discover")
		g.text = "Sarah" // the table alone
		tt.Frame()
		for _, l := range []string{i18n.T("Preview"), i18n.X("voice row", "Use"), i18n.T("More actions")} {
			if !faceShows(t, tt, 1300, l) {
				t.Errorf("dark %v: Discover's %q has no face on the row", dark, l)
			}
		}
		addVoice(t, a, core.Voice{Name: "Narrator", Kind: core.VoiceCloned, RefText: "Once upon a time.", Language: "en"}, 6)
		a.navigate("/voices/mine")
		tt.Frame()
		tt.Frame()
		for _, l := range []string{i18n.T("Preview"), i18n.X("voice row", "Use")} {
			if !faceShows(t, tt, 1300, l) {
				t.Errorf("dark %v: Mine's %q has no face on the row", dark, l)
			}
		}
		// A badge shows its face too.
		if !faceShows(t, tt, 1300, badgeTip(i18n.T("Cloned"), false)) {
			t.Errorf("dark %v: the kind badge has no face", dark)
		}
	}
}

// Every Discover card and row and every Mine row has its ▶; at
// 820×520 @125 % the rows keep ▶, 「使用」 and ⋯ and the language badge.
func TestEveryVoiceRowPreviews(t *testing.T) {
	a, _ := newVoiceTestApp(t)
	tt := ui.NewTester(a.view, 1300, 16000)
	g := &a.gallery
	a.navigate("/voices/discover")
	g.limit = gallery.MaxLimit
	tt.Frame()
	tt.Frame()
	count := func() int {
		n := 0
		for _, s := range tt.Texts() {
			if s == i18n.T("Preview") || s == i18n.T("No preview for this voice") {
				n++
			}
		}
		return n
	}
	if want := len(recommended()) + len(g.page.Items); count() != want {
		t.Errorf("Discover: %d ▶ for %d cards and rows", count(), want)
	}
	addVoice(t, a, core.Voice{Name: "Narrator", Kind: core.VoiceCloned, RefText: "Once upon a time.", Language: "en"}, 6)
	addVoice(t, a, core.Voice{Name: "Grandma", Kind: core.VoiceDesigned, DesignAttrs: designAttrs(vd.Attributes{Gender: "female", Age: "elderly"}), Seed: 7}, 0)
	a.navigate("/voices/mine")
	tt.Frame()
	tt.Frame()
	if count() != 2 {
		t.Errorf("Mine: %d ▶ for 2 rows: %q", count(), tt.Texts())
	}

	// Narrow: the actions and the language badge stay, inside the window.
	b, _ := newVoiceTestApp(t)
	nt := ui.NewTester(b.view, 820, 520)
	nt.SetScale(1.25)
	b.updateSettings(func(s *settings.Settings) { s.Appearance.Scale = 1.25 })
	b.navigate("/voices/discover")
	b.gallery.text = "Sarah"
	nt.Frame()
	nt.Frame()
	w := 820
	for _, l := range []string{i18n.T("Preview"), i18n.X("voice row", "Use"), i18n.T("More actions"), badgeTip(langName("en"), false)} {
		r, ok := nt.Find(l)
		if !ok || r.X+r.W > float32(w) {
			t.Errorf("narrow: %q at %+v (%v), window %d: %q", l, r, ok, w, nt.Texts())
		}
	}
}
