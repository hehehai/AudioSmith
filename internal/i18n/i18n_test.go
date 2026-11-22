package i18n

import (
	"testing"
	"time"
)

func TestEnglishIsIdentity(t *testing.T) {
	SetLocale("en")
	if got := T("Generate"); got != "Generate" {
		t.Error(got)
	}
	if got := T("%d takes", 3); got != "3 takes" {
		t.Error(got)
	}
	if got := T("100%"); got != "100%" {
		t.Error("a message without args must not be formatted:", got)
	}
	if got := N(1, "%d take", "%d takes", 1); got != "1 take" {
		t.Error(got)
	}
	if got := N(2, "%d take", "%d takes", 2); got != "2 takes" {
		t.Error(got)
	}
	// A singular without the count (dub_view_player.go's "Generate dub").
	if got := N(1, "Generate dub", "Generate %d dubs", 1); got != "Generate dub" {
		t.Error(got)
	}
	if got := N(3, "Generate dub", "Generate %d dubs", 3); got != "Generate 3 dubs" {
		t.Error(got)
	}
	if got := X("verb", "Export"); got != "Export" {
		t.Error(got)
	}
}

func TestChinese(t *testing.T) {
	defer SetLocale("en")
	if got := SetLocale("zh_CN"); got != "zh-Hans" || Locale() != "zh-Hans" {
		t.Fatal(got)
	}
	if got := T("Generate"); got != "生成" {
		t.Error(got)
	}
	// Missing translations fall back to English.
	if got := T("A message nobody translated"); got != "A message nobody translated" {
		t.Error(got)
	}
	if got := N(1, "%d take", "%d takes", 1); got != "1 条生成结果" {
		t.Error("zh-Hans has no singular, so it uses the translation of other:", got)
	}
}

func TestJapanese(t *testing.T) {
	defer SetLocale("en")
	if got := SetLocale("ja-JP"); got != "ja" || Locale() != "ja" {
		t.Fatal(got)
	}
	if got := T("Generate"); got != "生成" {
		t.Error(got)
	}
	if got := N(1, "%d take", "%d takes", 1); got != "1件のテイク" {
		t.Error("ja has no singular, so it uses the translation of other:", got)
	}
}

func TestMatch(t *testing.T) {
	for in, want := range map[string]string{
		"": "en", "en": "en", "en-GB": "en", "fr-FR": "en",
		"zh": "zh-Hans", "zh-CN": "zh-Hans", "zh-Hans": "zh-Hans", "zh_Hans_CN": "zh-Hans", "zh-TW": "en", "ZH-HANS": "zh-Hans",
		"ja": "ja", "ja-JP": "ja", "ja_JP": "ja", "JA-jp": "ja", "jv": "en",
	} {
		if got := Match(in); got != want {
			t.Errorf("Match(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCatalogsParse(t *testing.T) {
	defer SetLocale("en")
	for _, tag := range Supported() {
		if got := SetLocale(tag); got != tag {
			t.Errorf("SetLocale(%s) = %s", tag, got)
		}
	}
}

func TestNames(t *testing.T) {
	if Name("en") != "English" || Name("zh-Hans") != "简体中文" || Name("ja") != "日本語" || Name("xx") != "xx" {
		t.Error(Name("en"), Name("zh-Hans"), Name("ja"))
	}
}

func TestDates(t *testing.T) {
	defer SetLocale("en")
	now := time.Now()
	this := time.Date(now.Year(), 3, 4, 15, 6, 0, 0, time.Local)
	old := time.Date(2024, 11, 22, 9, 5, 0, 0, time.Local)
	SetLocale("en")
	if got := DateTime(this); got != "Mar 4 15:06" {
		t.Errorf("en this year: %q", got)
	}
	if got := DateTime(old); got != "Nov 22, 2024 09:05" {
		t.Errorf("en: %q", got)
	}
	SetLocale("zh-Hans")
	if got := DateTime(this); got != "3月4日 15:06" {
		t.Errorf("zh this year: %q", got)
	}
	if got, want := DateTime(old), "2024年11月22日 09:05"; got != want {
		t.Errorf("zh: %q", got)
	}
	if got := Date(old); got != "2024年11月22日" {
		t.Errorf("zh date: %q", got)
	}
	SetLocale("ja")
	if got := DateTime(this); got != "3月4日 15:06" {
		t.Errorf("ja this year: %q", got)
	}
	if got := DateTime(old); got != "2024年11月22日 09:05" {
		t.Errorf("ja: %q", got)
	}
	if got := Date(old); got != "2024年11月22日" {
		t.Errorf("ja date: %q", got)
	}
}
