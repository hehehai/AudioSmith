package main

import (
	"strings"
	"testing"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/services"
)

// Irodori-TTS, an optional Japanese model (thread #170): pickers say it
// speaks Japanese only, designs with it are in Japanese, a designed voice
// with a clip is cloned, clone styles reach it in Japanese, and a dub
// never falls back to it.
func TestIrodoriWiring(t *testing.T) {
	iro, vox := entry(t, "irodori-tts-v4.1-small-mf-8bit"), entry(t, "voxcpm2-8bit")
	if soleLanguage(iro) != "ja" || soleLanguage(vox) != "" || soleLanguage(entry(t, "kokoro-82m-bf16")) != "" {
		t.Errorf("soleLanguage %q", soleLanguage(iro))
	}

	t.Cleanup(func() { i18n.SetLocale("en") })
	for _, loc := range []string{"en", "zh-Hans", "ja"} {
		i18n.SetLocale(loc)
		want := i18n.T("Japanese only")
		if loc == "ja" {
			want = "" // a Japanese UI need not say it (model_picker.go)
		}
		if languageNote(iro) != want || languageNote(vox) != "" {
			t.Errorf("%s: note %q", loc, languageNote(iro))
		}
	}
	i18n.SetLocale("en")

	// Design: choosing it makes the draft Japanese, the default line too.
	var d designState
	d.reset()
	d.useModel(iro)
	if d.language != "ja" || d.speechLang() != "ja" || !containsKana(d.script) {
		t.Errorf("design %q %q", d.language, d.script)
	}
	d.script = "自分で書いた台詞です。"
	d.useModel(vox)
	d.useModel(iro)
	if d.script != "自分で書いた台詞です。" {
		t.Errorf("a typed line was replaced: %q", d.script)
	}

	designed := &core.Voice{Kind: core.VoiceDesigned, Sample: "sample.wav"}
	if !clonesOwnTake(iro, designed) {
		t.Error("clonesOwnTake")
	}

	// A clone's style: Japanese as written; Chinese or English as Japanese
	// attributes.
	if got := styleFor(iro, "優しく話す"); got != "優しく話す" {
		t.Errorf("ja style %q", got)
	}
	if got := styleFor(iro, "温暖的女性"); got != "温かみのある声の女性。" {
		t.Errorf("zh style %q", got)
	}

	// No delivery instruction: stories' directions are English or Chinese.
	if services.TakesInstruct(capNames(iro)) || services.CloneTakesInstruct(capNames(iro)) {
		t.Error("instruct")
	}
	tr := services.TraitsFor(iro.Family, capNames(iro)).WithCatalog(iro.MaxChars, iro.NonVerbal)
	if !tr.NonVerbal || !tr.Duration || tr.MaxCharsCJK != 120 {
		t.Errorf("traits %+v", tr)
	}

	// A dub without its default model falls back to a clone model that
	// speaks more than Japanese.
	onlyIrodori := func(e catalog.Entry) (bool, bool) { return e.ID == iro.ID, true }
	if got := dubDefaultModelBy(onlyIrodori); got == iro.Repo {
		t.Errorf("dub default %s", got)
	}
}

func containsKana(s string) bool {
	for _, r := range s {
		if r >= 0x3041 && r <= 0x30FA {
			return true
		}
	}
	return false
}

// TestIrodoriRecommendedInStudio (user 11-22): a Japanese script marks
// Irodori-TTS 推荐 / Recommended in the Studio's picker, first, without
// switching the model.
func TestIrodoriRecommendedInStudio(t *testing.T) {
	simulateDarwin(t)
	a, _, tt := newTestApp(t)
	before := a.preset().ID
	a.text = "今日は一日中、雨が降っていました。"
	a.pickerOpen = map[string]bool{"model": true}
	tt.Frame()
	tt.Frame()
	texts := strings.Join(tt.Texts(), "|")
	rec := strings.Index(texts, "|Recommended|Japanese · Natural|")
	if rec < 0 || strings.Index(texts, "Show all models") < rec {
		t.Errorf("Irodori not recommended first: %s", texts)
	}
	if a.preset().ID != before {
		t.Errorf("model switched to %s", a.preset().ID)
	}
}
