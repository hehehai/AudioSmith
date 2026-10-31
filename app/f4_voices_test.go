package main

// Behaviour tests of 0.5 F4's voices: Discover's chips, language menu,
// sections and table, previews one at a time, the detail and its
// hand-offs; Mine's table, type filter, rename and delete with Undo; New
// voice's cards, Quick clone's steps ending in the hand-offs, Design's
// preset chips and candidates; the Studio picker's way to the library;
// the narrow window.

import (
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/gallery"
	"github.com/hehehai/AudioSmith/internal/settings"
	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
)

// Discover: 推荐 then 全部 while nothing filters; a use chip and the
// language menu narrow the table (推荐 steps aside).
func TestDiscoverFilters(t *testing.T) {
	a, _ := newVoiceTestApp(t)
	tt := ui.NewTester(a.view, 1300, 1800)
	g := &a.gallery
	a.navigate("/voices/discover")
	tt.Frame()
	all := gallery.Default().Search(gallery.Query{Limit: gallery.MaxLimit}).Total
	if !tt.HasText("Recommended") || !tt.HasText("All voices") || !tt.HasText("Heart") || !tt.HasText("Late-Night Radio") {
		t.Fatalf("sections: %q", tt.Texts())
	}
	if len(recommended()) != 6 || len(g.page.Items) != all {
		t.Errorf("recommended %d, table %d of %d", len(recommended()), len(g.page.Items), all)
	}
	// A use chip.
	ads := gallery.Default().Search(gallery.Query{Category: "announcing"}).Total
	if err := tt.Click("Ads & announcing " + itoa(ads)); err != nil {
		t.Fatalf("%v: %q", err, tt.Texts())
	}
	tt.Frame()
	if g.page.Total != ads || tt.HasText("Recommended") {
		t.Errorf("chip: %d of %d, %q", g.page.Total, ads, tt.Texts())
	}
	for _, it := range g.page.Items {
		if !slices.Contains(it.Uses, "announcing") {
			t.Fatalf("%s is no announcer", it.ID)
		}
	}
	// The language menu.
	tt.Click("Language")
	var zh string
	for _, it := range tt.Menu() {
		if strings.HasPrefix(it, "Chinese") {
			zh = it
		}
	}
	if err := tt.ChooseMenuItem(zh); err != nil {
		t.Fatalf("%v: %q", err, tt.Menu())
	}
	tt.Frame()
	tt.Frame()
	if g.filters[gallery.FacetLanguage] != "zh" || g.page.Total == 0 || g.page.Total >= ads {
		t.Fatalf("language: %v %d", g.filters, g.page.Total)
	}
	for _, it := range g.page.Items {
		if it.Languages[0] != "zh" || !slices.Contains(it.Uses, "announcing") {
			t.Fatalf("filtered %+v", it)
		}
	}
	if !tt.HasText("Headline Desk") || tt.HasText("Trailer Thunder") {
		t.Errorf("table: %q", tt.Texts())
	}
	// The chip again: off.
	tt.Click("Ads & announcing " + itoa(g.page.Total))
	tt.Frame()
	if g.tab != 0 {
		t.Errorf("chip off: %d", g.tab)
	}
}

// Discover's ▶ plays one preview at a time and marks its row; a click
// on a row opens the detail; 「用于文字转语音」 lands in the Studio with
// the voice; 「保存到我的音色」 saves it to Mine.
func TestDiscoverPreviewDetailHandoff(t *testing.T) {
	a, eng := newVoiceTestApp(t)
	tt := ui.NewTester(a.view, 1300, 1800)
	g := &a.gallery
	a.navigate("/voices/discover")
	g.text = "Sarah"
	tt.Frame()
	sarah, _ := gallery.Default().ByID("c-af_sarah")
	heart, _ := gallery.Default().ByID("c-af_heart")
	sent := len(eng.sent)
	if err := tt.Click("Preview"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.pl.Playing(archetypePreview(sarah).id) || !tt.HasText("Playing") || !tt.HasText("Stop") {
		t.Fatalf("not playing: %q", tt.Texts())
	}
	// Another one: the first stops.
	a.previewArchetype(heart)
	if a.pl.Playing(archetypePreview(sarah).id) || !a.pl.Playing(archetypePreview(heart).id) {
		t.Error("two previews at once")
	}
	tt.Frame()
	if tt.HasText("Playing") {
		t.Error("Sarah's row still says it plays") // Heart is not in this table
	}
	a.previewArchetype(heart)
	if a.pl.Playing(archetypePreview(heart).id) || len(eng.sent) != sent {
		t.Error("toggle / jobs")
	}

	// A click on the row opens the detail beside the table.
	if err := tt.Click("Sarah"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if g.detail != sarah.ID || !tt.HasText("Voice details") || !tt.HasText("Try a line") || !tt.HasText("Save to My voices") ||
		!tt.HasText("Clear, poised American woman.") {
		t.Fatalf("detail: %q", tt.Texts())
	}
	// 「试一句」: the speaker's model says it.
	g.try.text = "A line for Sarah."
	tt.Frame()
	tt.Click("Generate")
	req := eng.last(t)
	if req.Cmd != "synthesize" || req.Voice != "af_sarah" || req.Text != "A line for Sarah." || !strings.Contains(req.Model, "Kokoro") {
		t.Errorf("try: %+v", req.SynthesizeParams)
	}
	// Save to My voices: saved once, the page stays.
	tt.Click("Save to My voices")
	tt.Frame()
	if len(a.voices) != 1 || a.voices[0].Origin != "gallery:"+sarah.ID || a.router.Path() != "/voices/discover" || !tt.HasText("In My voices") {
		t.Fatalf("saved: %+v %s", a.voices, a.router.Path())
	}
	// 「用于文字转语音」: the Studio, with this voice.
	if err := tt.Click("Use in Text to speech"); err != nil {
		t.Fatal(err)
	}
	if a.router.Path() != "/studio" || a.studioVoice != a.voices[0].ID || len(a.voices) != 1 {
		t.Fatalf("studio: %s %q", a.router.Path(), a.studioVoice)
	}
	// It shows in Mine as 收藏.
	a.navigate("/voices/mine")
	a.voiceKind = kindSaved
	tt.Frame()
	if !tt.HasText("Sarah") || len(a.shownVoices()) != 1 {
		t.Errorf("mine, saved: %q", tt.Texts())
	}
}

// Mine: the type filter, rename, delete with Undo; ghost rows when empty.
func TestMineTable(t *testing.T) {
	a, _ := newVoiceTestApp(t)
	tt := ui.NewTester(a.view, 1300, 1800)
	pinTester(tt)
	a.navigate("/voices/mine")
	tt.Frame()
	if !tt.HasText("No saved voices yet") || !tt.HasText("New voice") {
		t.Fatalf("empty: %q", tt.Texts())
	}
	cl := addVoice(t, a, core.Voice{Name: "Narrator", Kind: core.VoiceCloned, RefText: "Once upon a time.", Language: "en"}, 6)
	addVoice(t, a, core.Voice{Name: "Grandma", Kind: core.VoiceDesigned, DesignAttrs: designAttrs(vd.Attributes{Gender: "female", Age: "elderly"}), Seed: 7}, 0)
	addVoice(t, a, core.Voice{Name: "Heart", Kind: core.VoiceBuiltin, Model: kokoro, Speaker: "af_heart", Origin: "gallery:c-af_heart"}, 0)
	addTake(t, a, core.Take{ID: "T1", Text: "Hi.", Model: qwenBase, VoiceID: cl.ID, AudioSeconds: 1}, 0.3)
	a.mine.usesFor = ""
	tt.Frame()
	tt.Frame()
	for _, s := range []string{"Narrator", "Grandma", "Heart", "Cloned", "Designed", "Saved", "English", "1 time"} {
		if !tt.HasText(s) {
			t.Errorf("no %q: %q", s, tt.Texts())
		}
	}
	for kind, want := range map[int][]string{kindCloned: {"Narrator"}, kindDesigned: {"Grandma"}, kindSaved: {"Heart"}, kindAll: {"Grandma", "Heart", "Narrator"}} {
		a.voiceKind = kind
		var got []string
		for _, v := range a.shownVoices() {
			got = append(got, v.Name)
		}
		if !slices.Equal(got, want) {
			t.Errorf("kind %d: %v", kind, got)
		}
	}
	tt.Click("Saved") // the segmented control
	tt.Frame()
	if a.voiceKind != kindSaved || tt.HasText("Narrator") {
		t.Errorf("segmented: %d", a.voiceKind)
	}
	tt.Click("All")
	tt.Frame()

	// Rename from the row's ⋯ (the first row: Grandma).
	gm := a.shownVoices()[0]
	chooseAway(t, tt, "More actions", "Rename")
	if a.mine.renaming != gm.ID {
		t.Fatalf("renaming %q", a.mine.renaming)
	}
	a.mine.renameText = "Old Grandma"
	tt.Frame()
	if err := tt.Click("Save name"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if v := a.findVoice(gm.ID); v == nil || v.Name != "Old Grandma" || !tt.HasText("Old Grandma") {
		t.Fatalf("renamed: %+v", v)
	}

	// Duplicate: a copy with its reference.
	a.duplicateVoice(cl.ID)
	tt.Frame()
	if len(a.voices) != 4 || !tt.HasText("Narrator (copy)") {
		t.Fatalf("duplicate: %d %q", len(a.voices), tt.Texts())
	}

	// A click on a row opens the detail; Delete… asks, Undo restores.
	tt.Click("Narrator")
	tt.Frame()
	if a.edit == nil || a.edit.id != cl.ID || !tt.HasText("Voice details") {
		t.Fatal("no detail")
	}
	gid := a.edit.id
	tt.Click("Delete")
	tt.Frame()
	tt.Click("Delete voice")
	tt.Frame()
	if a.findVoice(gid) != nil || !tt.HasText("Undo") {
		t.Fatalf("delete: %q", tt.Texts())
	}
	tt.Click("Undo")
	tt.Frame()
	if a.findVoice(gid) == nil {
		t.Error("not restored")
	}
}

// New voice: the cards lead into the flows; recent voices link to them.
func TestNewVoiceCards(t *testing.T) {
	a, _ := newVoiceTestApp(t)
	tt := ui.NewTester(a.view, 1300, 1000)
	a.navigate("/voices/new")
	tt.Frame()
	if !tt.HasText("10 seconds or more of clear speech, 30 seconds is best") || tt.HasText("Voices you made lately") {
		t.Fatalf("cards: %q", tt.Texts())
	}
	tt.Click("Clone a voice")
	if a.router.Path() != "/voices/new/clone" || a.clone.step != cloneAdd {
		t.Errorf("clone: %s", a.router.Path())
	}
	a.navigate("/voices/new")
	tt.Frame()
	tt.Click("Start designing")
	if a.router.Path() != "/voices/new/design" {
		t.Errorf("design: %s", a.router.Path())
	}
	v := addVoice(t, a, core.Voice{Name: "Made Here", Kind: core.VoiceCloned}, 6)
	addVoice(t, a, core.Voice{Name: "From Discover", Kind: core.VoiceDesigned, Origin: "gallery:c-villain"}, 0)
	a.navigate("/voices/new")
	tt.Frame()
	if !tt.HasText("Voices you made lately") || !tt.HasText("Made Here") || tt.HasText("From Discover") {
		t.Fatalf("recent: %q", tt.Texts())
	}
	tt.Click("Made Here")
	tt.Frame()
	if a.router.Path() != "/voices/mine" || a.edit == nil || a.edit.id != v.ID {
		t.Errorf("open recent: %s", a.router.Path())
	}
	// Home's tiles.
	a.navigate("/home")
	tt.Frame()
	tt.Click("Clone")
	if a.router.Path() != "/voices/new/clone" {
		t.Errorf("home clone: %s", a.router.Path())
	}
}

// Quick clone's steps with the fake engine: a clip moves to 检查;
// 下一步; a name; 保存音色 → 完成 with 「用于文字转语音」.
func TestCloneStepsHandoff(t *testing.T) {
	a, eng := newVoiceTestApp(t)
	tt := ui.NewTester(a.view, 1300, 1400)
	a.startClone("")
	tt.Frame()
	if !tt.HasText("Step 1 of 4: Add a recording") {
		t.Fatalf("step 1: %q", tt.Texts())
	}
	clip := testDir(t) + "/me.wav"
	writeSpeech(clip, 12, []span{{0.3, 5}, {5.4, 11.6}})
	a.cloneDropped([]string{clip})
	answerTranscribe(t, eng, "Hello, this is me talking.")
	tt.Frame()
	s := &a.clone
	if s.step != cloneCheck || !tt.HasText("Step 2 of 4: Check") || !tt.HasText("Transcript") {
		t.Fatalf("step 2: %d %q", s.step, tt.Texts())
	}
	tt.Click("Next ›")
	tt.Frame()
	if s.step != cloneName {
		t.Fatalf("step 3: %d", s.step)
	}
	// Back to 检查 through the indicator, and on again.
	tt.Click("Back to “Check”")
	tt.Frame()
	if s.step != cloneCheck {
		t.Errorf("back: %d", s.step)
	}
	tt.Click("Next ›")
	s.name = "Me"
	tt.Frame()
	if err := tt.Click("Save voice"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if s.step != cloneDone || len(a.voices) != 1 || !tt.HasText("Step 4 of 4: Done") || !tt.HasText("Saved to My voices") {
		t.Fatalf("done: %d %q", s.step, tt.Texts())
	}
	id := a.voices[0].ID
	tt.Click("Listen")
	if !a.savedPlaying(a.findVoice(id)) {
		t.Error("Listen doesn't play")
	}
	if err := tt.Click("Use in Text to speech"); err != nil {
		t.Fatal(err)
	}
	if a.router.Path() != "/studio" || a.studioVoice != id {
		t.Errorf("hand-off: %s %q", a.router.Path(), a.studioVoice)
	}
}

// Design: a preset chip fills the prompt and the details.
func TestDesignPresetChip(t *testing.T) {
	a, _ := newVoiceTestApp(t)
	tt := ui.NewTester(a.view, 1300, 1400)
	a.startDesign()
	tt.Frame()
	d := &a.design
	if len(gallery.Presets()) != 8 || !tt.HasText("Generate candidates") {
		t.Fatalf("presets %d: %q", len(gallery.Presets()), tt.Texts())
	}
	if err := tt.Click("Villain"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if d.description != "Very low, smooth and menacing." || d.attrs.Pitch != "very_low" || d.preset != "villain" {
		t.Fatalf("preset: %q %+v", d.description, d.attrs)
	}
	// Again: off, and the prompt it filled goes.
	tt.Click("Villain")
	if d.description != "" || d.preset != "" {
		t.Errorf("off: %q", d.description)
	}
}

// The Studio's voice picker leads to the library.
func TestStudioPickerBrowse(t *testing.T) {
	a, _, _ := newTestApp(t)
	tt := ui.NewTester(func(ctx *ui.Context) {
		ui.Column(ctx).Fill().Children(func() { a.studioVoiceField(ctx, a.preset()) })
	}, 400, 1400) // the list shows all of Kokoro's voices
	a.voicePickerOpen(studioVoiceKey, true)
	tt.Frame()
	if err := tt.Click("Browse the voice library ›"); err != nil {
		t.Fatalf("%v: %q", err, tt.Texts())
	}
	if a.router.Path() != "/voices/discover" {
		t.Errorf("browse: %s", a.router.Path())
	}
}

// 820×520 at 125 %: Discover's detail is a drawer over the table, which
// keeps its name, ▶ and 「使用」; Mine's too; the clone steps fit.
func TestVoicesNarrow(t *testing.T) {
	a, _ := newVoiceTestApp(t)
	tt := ui.NewTester(a.view, 820, 520)
	a.updateSettings(func(s *settings.Settings) { s.Appearance.Scale = 1.25 })
	a.navigate("/voices/discover")
	tt.Frame()
	tt.Frame()
	// The table has no column titles (user, 10-31); a row keeps its
	// name, ▶ and 「使用」 inside the window.
	if tt.HasText("Gender · age") || !tt.HasText("Serena") || !tt.HasText("Use") {
		t.Errorf("narrow table: %q", tt.Texts())
	}
	w, _ := tt.Find("Use")
	if w.X+w.W > 820 {
		t.Errorf("table off the window: %+v", w)
	}
	a.openArchetype("c-serena")
	tt.Frame()
	tt.Frame()
	r, ok := tt.Find("Use in Text to speech")
	if !ok || r.X+r.W > 820 || r.X < 0 {
		t.Fatalf("drawer: %v %+v", ok, r)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.gallery.detail != "" {
		t.Error("Escape keeps the drawer")
	}
	a.startClone("")
	tt.Frame()
	if r, ok := tt.Find("Record"); !ok || r.X+r.W > 820 {
		t.Errorf("clone: %v %+v", ok, r)
	}
}
