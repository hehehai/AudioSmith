package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/dub"
	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
)

// pickerRecommends reports whether the open picker lists name among its
// recommended models (before "Show all models").
func pickerRecommends(texts []string, name string) bool {
	head := slices.Index(texts, "Recommended")
	all := slices.IndexFunc(texts, func(s string) bool { return strings.HasPrefix(s, "Show all models") })
	i := slices.Index(texts[max(head, 0):], name)
	return head >= 0 && i >= 0 && (all < 0 || head+i < all)
}

// TestTextLangScripts: scripts that name a language steer the
// recommendations (thread #167).
func TestTextLangScripts(t *testing.T) {
	for s, want := range map[string]string{
		"สวัสดีครับ ยินดีต้อนรับ":          "th",
		"مرحبا بكم في البرنامج":            "ar",
		"नमस्ते, आपका स्वागत है":           "hi",
		"Xin chào, tôi là người Việt Nam.": "vi",
		"Привет, как дела?":                "ru",
		"Hello, welcome to the show.":      "en",
		"Bonjour, ça va très bien.":        "en", // Latin without Vietnamese letters: English
		"大家好，欢迎收听今天的节目。":                   "zh",
		"こんにちは、今日はいい天気ですね。":                "ja",
		"": "",
	} {
		if got := textLang(s); got != want {
			t.Errorf("textLang(%q) = %q, want %q", s, got, want)
		}
	}
}

// TestVoxCPM2RecommendedStudio: a Vietnamese script recommends VoxCPM2
// in the Studio's picker and its ✦ Recommend offers it in place of a
// clone model that can't speak it; the Studio's model stays.
func TestVoxCPM2RecommendedStudio(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.SetSize(1100, 1400)
	base, vox := entry(t, "qwen3-tts-1.7b-base-8bit"), entry(t, "voxcpm2-8bit")
	a.selectModel(base)
	a.text = "Xin chào, tôi là người Việt Nam."
	tt.Frame()
	a.pickerOpen = map[string]bool{"model": true}
	tt.Frame()
	if !pickerRecommends(tt.Texts(), vox.DisplayName()) || pickerRecommends(tt.Texts(), base.DisplayName()) {
		t.Errorf("vi picker: %q", tt.Texts())
	}
	_, opts, _ := a.studioVoiceChoices(base)
	recs := a.recommendVoices(base, opts)
	if len(recs) == 0 || recs[len(recs)-1].value != "rec-model:"+vox.ID {
		t.Errorf("vi recommend: %+v", recs)
	}
	if a.preset().ID != base.ID {
		t.Errorf("the model switched to %s", a.preset().ID)
	}
	// English: as before, VoxCPM2 not recommended.
	a.text = "Hello, welcome to the show."
	a.pickerOpen = map[string]bool{"model": true}
	tt.Frame()
	if pickerRecommends(tt.Texts(), vox.DisplayName()) || !pickerRecommends(tt.Texts(), "Fast English voices") {
		t.Errorf("en picker: %q", tt.Texts())
	}
	if recs := a.recommendVoices(base, opts); slices.ContainsFunc(recs, func(o voiceOption) bool { return o.value == "rec-model:"+vox.ID }) {
		t.Errorf("en recommend: %+v", recs)
	}
}

// TestVoxCPM2RecommendedDub: a dub into Vietnamese marks VoxCPM2
// recommended in the voice model menu and the note recommends it;
// Spanish alone keeps Qwen3-TTS's list; nothing switches.
func TestVoxCPM2RecommendedDub(t *testing.T) {
	d := newDubTest(t, 4)
	a, tt := d.a, d.tt
	tt.SetSize(1100, 1400)
	a.dubSourceFile(d.src)
	a.dubSide.set, a.dubSide.open = true, true
	a.dubStart()
	base, vox := entry(t, "qwen3-tts-0.6b-base-8bit"), entry(t, "voxcpm2-8bit")
	a.dubSet(func(p *dub.Project) { p.ChooseTargets([]string{"es", "vi"}) })
	a.dubSettings(func(s *dub.Settings) { s.Model = base.Repo })
	a.dubStep = dubStepGenerate
	tt.Frame()
	note := "“" + base.DisplayName() + "” doesn't speak " + dubLangName("vi") + "; “" + vox.DisplayName() + "” does and is recommended for it."
	if !tt.HasText(note) {
		t.Errorf("note: %q", tt.Texts())
	}
	a.pickerOpen = map[string]bool{"gen-model": true}
	tt.Frame()
	if !pickerRecommends(tt.Texts(), vox.DisplayName()) || pickerRecommends(tt.Texts(), base.DisplayName()) {
		t.Errorf("vi dub picker: %q", tt.Texts())
	}
	if a.dub.Settings.Model != base.Repo {
		t.Errorf("the dub switched to %s", a.dub.Settings.Model)
	}
	a.dubSet(func(p *dub.Project) { p.ChooseTargets([]string{"es"}) })
	a.pickerOpen = map[string]bool{"gen-model": true}
	tt.Frame()
	if pickerRecommends(tt.Texts(), vox.DisplayName()) || !pickerRecommends(tt.Texts(), base.DisplayName()) {
		t.Errorf("es dub picker: %q", tt.Texts())
	}
}

// TestVoxCPM2RecommendedDialect: a Chinese dialect on the Design page
// recommends VoxCPM2 (picker and a note whose link picks it); without
// one the page recommends Voice design as before.
func TestVoxCPM2RecommendedDialect(t *testing.T) {
	withDesignCatalog(t)
	a, _ := newVoiceTestApp(t)
	tt := ui.NewTester(a.view, 1200, 1800)
	vox := entry(t, "voxcpm2-8bit")
	a.navigate("/design")
	d := &a.design
	d.detailsOpen = true
	tt.Frame()
	before := d.model
	if before == vox.Label {
		t.Fatal("VoxCPM2 is the design default")
	}
	d.attrs = vd.Attributes{Dialect: "sichuan"}
	tt.Frame()
	note, use := "For Chinese dialects, “"+vox.DisplayName()+"” is recommended.", "Use "+vox.DisplayName()
	if !tt.HasText(note) || d.model != before {
		t.Fatalf("dialect note: %q (model %s)", tt.Texts(), d.model)
	}
	a.pickerOpen = map[string]bool{"design-model": true}
	tt.Frame()
	if !pickerRecommends(tt.Texts(), vox.DisplayName()) {
		t.Errorf("dialect picker: %q", tt.Texts())
	}
	a.pickerOpen = map[string]bool{}
	tt.Frame()
	if err := tt.Click(use); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if d.model != vox.Label || tt.HasText(note) {
		t.Errorf("use: model %s", d.model)
	}
	d.model, d.attrs = before, vd.Attributes{}
	a.pickerOpen = map[string]bool{"design-model": true}
	tt.Frame()
	if tt.HasText(note) || pickerRecommends(tt.Texts(), vox.DisplayName()) {
		t.Errorf("no dialect: %q", tt.Texts())
	}
}

// TestVoxCPM2RecommendedStories: a directed story recommends VoxCPM2 in
// its model picker (it hears the directions with cloned voices); an
// undirected one doesn't, unless its script is in a language the usual
// models don't speak. The story's model stays.
func TestVoxCPM2RecommendedStories(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.SetSize(1100, 1600)
	a.navigate("/stories")
	p := a.lfStories
	vox := entry(t, "voxcpm2-8bit")
	kittenDoc(&p.doc)
	p.doc.Scene = "傍晚的家里，孩子想领养小猫，妈妈犹豫"
	model := p.doc.Model
	open := func() []string {
		a.pickerOpen = map[string]bool{"lf-model": true}
		tt.Frame()
		tt.Frame()
		return tt.Texts()
	}
	if texts := open(); !storyDirected(&p.doc) || !pickerRecommends(texts, vox.DisplayName()) {
		t.Errorf("directed: %q", texts)
	}
	p.doc.Scene = ""
	if texts := open(); storyDirected(&p.doc) || pickerRecommends(texts, vox.DisplayName()) {
		t.Errorf("undirected: %q", texts)
	}
	p.doc.Language = "th"
	if texts := open(); !pickerRecommends(texts, vox.DisplayName()) {
		t.Errorf("Thai: %q", texts)
	}
	if p.doc.Model != model {
		t.Errorf("the story switched to %s", p.doc.Model)
	}
}
