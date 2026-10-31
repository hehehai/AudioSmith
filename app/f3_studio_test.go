package main

// Behaviour tests of 0.5 F3's Text to speech: the example chips, the
// panel's 设置 | 历史, the voice row and 「✦ 推荐」, the hand-offs on a
// take (reuse, 换个音色再生成, 存为项目), the narrow window's drawer and
// Home's text landing in the script.

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// A chip fills the empty script with its example in the voice's language;
// the chips go once there is text.
func TestStudioExampleChips(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.text = ""
	tt.Frame()
	tt.Frame()
	if !tt.HasText("Get started with") || !tt.HasText("Tell a story") || !tt.HasText("Guided meditation") {
		t.Fatalf("no chips: %q", tt.Texts())
	}
	if err := tt.Click("Tell a story"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !strings.HasPrefix(a.text, "Once upon a time") { // Kokoro's af_heart speaks English
		t.Fatalf("filled %q", a.text)
	}
	if tt.HasText("Get started with") {
		t.Error("the chips stay over a script")
	}
	if a.scriptUse() != "narration" {
		t.Errorf("use %q", a.scriptUse())
	}

	// A Chinese voice: the example in Chinese, whatever the interface's language.
	a.selectModel(entry(t, "qwen3-tts-0.6b-customvoice-8bit"))
	a.voice = "Serena"
	a.text = ""
	tt.Frame()
	tt.Frame()
	if err := tt.Click("News bulletin"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(a.text, "各位听众") {
		t.Fatalf("Chinese voice: %q", a.text)
	}
	// No voice language (a multilingual model's default): the interface's.
	i18n.SetLocale("zh-Hans")
	t.Cleanup(func() { i18n.SetLocale("en") })
	a.voice = ""
	if l := a.exampleLang(); l != "zh" && l != a.studioVoiceLang(a.preset()) {
		t.Errorf("example language %q", l)
	}
}

// Generate: the take shows above the bar and heads 历史 with its voice;
// takes from elsewhere (a voice's preview) are not listed.
func TestStudioHistoryTab(t *testing.T) {
	a, eng, tt := newTestApp(t)
	at := time.Now().Add(-time.Hour)
	addTake(t, a, core.Take{ID: "V1", CreatedAt: at, Text: "A voice preview", Model: kokoro, Voice: "af_sky", Origin: core.TakeVoice, AudioSeconds: 1}, 0.3)
	a.text, a.voice = "The first words of a fresh take.", "af_bella"
	tt.Frame()
	if err := tt.Click("Generate"); err != nil {
		t.Fatal(err)
	}
	finishRender(t, eng, eng.last(t), 1.5)
	tt.Frame()
	if !tt.HasText("Latest take") {
		t.Errorf("no latest take: %q", tt.Texts())
	}
	if err := tt.Click("History"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.ttsUI.tab != studioTabHistory {
		t.Fatalf("tab %d", a.ttsUI.tab)
	}
	if !tt.HasText("The first words of a fresh take.") || !tt.HasText("af_bella · 1.5 s · just now") && !hasTextPrefix(tt.Texts(), "af_bella · 1.5 s") {
		t.Errorf("history: %q", tt.Texts())
	}
	if tt.HasText("A voice preview") {
		t.Error("a voice page's take is in the Studio's history")
	}
	takes := a.studioTakes()
	if len(takes) != 1 || takes[0].Voice != "af_bella" {
		t.Errorf("studio takes %+v", takes)
	}
	// Empty: ghost rows.
	a2, _, tt2 := newTestApp(t)
	a2.showStudioPanel(studioTabHistory)
	tt2.Frame()
	if !tt2.HasText("No takes yet") {
		t.Errorf("empty history: %q", tt2.Texts())
	}
}

func hasTextPrefix(texts []string, p string) bool {
	for _, s := range texts {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// A history row's ⋯: Reuse settings loads the take; 换个音色再生成 opens
// the voice picker, and the voice picked makes the take again.
func TestStudioReuseAndRevoice(t *testing.T) {
	a, eng, tt := newTestApp(t)
	at := time.Now().Add(-time.Minute)
	addTake(t, a, core.Take{ID: "T1", CreatedAt: at, Text: "Reuse me, please.", Model: kokoro, Voice: "am_michael", Speed: 1.2, AudioSeconds: 2}, 0.3)
	a.text, a.voice, a.speed = "Something else.", "af_heart", 1
	a.lastClipID = ""
	a.showStudioPanel(studioTabHistory)
	tt.Frame()
	chooseAway(t, tt, "More actions", "Reuse settings")
	if a.text != "Reuse me, please." || a.voice != "am_michael" || a.speed != 1.2 {
		t.Fatalf("reuse: %q %q %v", a.text, a.voice, a.speed)
	}

	a.text, a.voice = "Something else.", "af_heart"
	a.showStudioPanel(studioTabHistory)
	tt.Frame()
	chooseAway(t, tt, "More actions", "Regenerate with another voice")
	tt.Frame()
	if a.ttsUI.tab != studioTabSettings || !a.voicePickers.open[studioVoiceKey] || !a.ttsUI.regen {
		t.Fatalf("revoice: tab %d open %v regen %v", a.ttsUI.tab, a.voicePickers.open[studioVoiceKey], a.ttsUI.regen)
	}
	if a.text != "Reuse me, please." || !tt.HasText("Pick another voice: the take is made again with it.") {
		t.Errorf("revoice: %q %q", a.text, tt.Texts())
	}
	n := len(eng.sent)
	if err := tt.Click("af_bella"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(eng.sent) != n+1 {
		t.Fatalf("no take after the pick: %d sent; voice %q regen %v open %v job %v can %v", len(eng.sent)-n, a.voice, a.ttsUI.regen, a.voicePickers.open[studioVoiceKey], a.job != nil, a.canGenerate())
	}
	if p := renderOf(t, eng.last(t)); p.Take.Voice != "af_bella" || p.Take.Text != "Reuse me, please." {
		t.Errorf("regenerated %+v", p.Take)
	}
	if a.ttsUI.regen || a.voicePickers.open[studioVoiceKey] {
		t.Error("the picker stays in its re-voice state")
	}
}

// The voice row opens the shared picker (its previews); 「✦ 推荐」 heads it
// with voices in the script's language, and offers the model for another.
func TestStudioVoiceRowAndRecommend(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.text, a.voice = "Welcome back to the show, everyone.", "af_heart"
	tt.Frame()
	if err := tt.Click("Warm American woman: English's default voice."); err != nil { // the row
		t.Fatal(err)
	}
	tt.Frame()
	if !a.voicePickers.open[studioVoiceKey] || !tt.HasText("am_michael") {
		t.Fatalf("the row did not open the picker: %q", tt.Texts())
	}
	if tt.HasText("Recommended for this script") {
		t.Error("recommendations without 推荐")
	}
	a.voicePickerOpen(studioVoiceKey, false)
	tt.Frame()
	if err := tt.Click("Recommend"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Recommended for this script") {
		t.Fatalf("推荐: %q", tt.Texts())
	}
	e := a.preset()
	_, opts, _ := a.studioVoiceChoices(e)
	recs := a.recommendVoices(e, opts)
	if len(recs) == 0 {
		t.Fatal("nothing recommended for English")
	}
	for _, r := range recs {
		id, ok := strings.CutPrefix(r.value, "rec:sp:")
		if !ok {
			t.Errorf("not a speaker: %+v", r)
			continue
		}
		if l := e.SpeakerLanguage(id); l != "en" {
			t.Errorf("%s speaks %q", id, l)
		}
	}
	if a.scriptUse() != "conversational" {
		t.Errorf("use %q", a.scriptUse())
	}

	// A Chinese script on Kokoro (no Chinese voice offered): the model
	// recommended for Chinese; picking it switches.
	a.text = "大家好，欢迎收听今天的节目。"
	recs = a.recommendVoices(e, opts)
	last := recs[len(recs)-1]
	if !strings.HasPrefix(last.value, "rec-model:") {
		t.Fatalf("no model for Chinese: %+v", recs)
	}
	tt.Frame()
	if err := tt.Click(last.label); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.preset().ID != strings.TrimPrefix(last.value, "rec-model:") {
		t.Errorf("model %s", a.preset().ID)
	}
	if a.ttsUI.recommend {
		t.Error("推荐 stays on after the picker closed")
	}
}

// At 820×520 @125 % the panel is a drawer: the toolbar's toggle opens and
// closes it, Escape closes it; the summary in the bar opens it too.
func TestStudioNarrowPanel(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.SetSize(820, 520)
	a.updateSettings(func(s *settings.Settings) { s.Appearance.Scale = 1.25 })
	tt.Frame()
	tt.Frame()
	if tt.HasText("Text to speech panel") {
		t.Fatal("the panel shows over the script at first")
	}
	if err := tt.Click("Show settings and history"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Text to speech panel") || !tt.HasText("Speed") {
		t.Fatalf("drawer: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if tt.HasText("Text to speech panel") {
		t.Fatal("Escape left the drawer open")
	}
	// The bar names the model and voice while the panel is hidden; a
	// click opens the drawer at 设置.
	a.ttsUI.tab = studioTabHistory
	if err := tt.Click(a.preset().DisplayName() + " · af_heart"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Text to speech panel") || a.ttsUI.tab != studioTabSettings {
		t.Errorf("summary: tab %d %q", a.ttsUI.tab, tt.Texts())
	}
}

// Home's text lands in the script, and a long take can become a project.
func TestStudioHomeTextAndSaveAsProject(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.navigate("/home")
	tt.Frame()
	a.homeUI.text = "Read this aloud for me."
	tt.Frame()
	if err := tt.Click("Start (↩)"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.router.Path() != "/studio" || a.text != "Read this aloud for me." {
		t.Fatalf("home: %s %q", a.router.Path(), a.text)
	}

	long := strings.Repeat("A long chapter goes on and on. ", 20)
	if !projectSized(long) || projectSized("Short.") {
		t.Fatal("projectSized")
	}
	addTake(t, a, core.Take{ID: "L1", CreatedAt: time.Now(), Text: long, Model: kokoro, Voice: "af_heart", AudioSeconds: 30}, 0.3)
	a.showStudioPanel(studioTabHistory)
	tt.Frame()
	if err := tt.Click("More actions"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Save as project", "Audiobook"); err != nil {
		t.Fatalf("%v: %q", err, tt.Menu())
	}
	tt.Frame()
	if a.router.Path() != "/audiobook" || !strings.Contains(a.lfBook.doc.Script, "A long chapter goes on") {
		t.Errorf("save as audiobook: %s %q", a.router.Path(), a.lfBook.doc.Script)
	}
}
