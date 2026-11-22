package voicedesign

import "testing"

// TestJapaneseLabels: every category and value has a Japanese label, and
// In picks it for ja locales (thread #173).
func TestJapaneseLabels(t *testing.T) {
	for _, c := range Categories {
		info, _ := Info(c)
		if info.Label.In("ja") == info.Label.En {
			t.Errorf("%s has no Japanese label", c)
		}
		for _, o := range info.Options {
			if o.Label.Ja == "" {
				t.Errorf("%s/%s has no Japanese label", c, o.ID)
			}
		}
	}
	if op, ok := Lookup(Gender, "female"); !ok || op.Label.In("ja-JP") != "女性" || op.Label.In("zh-Hans") != "女" || op.Label.In("en") != "Female" {
		t.Error(op.Label)
	}
	if got := (Text{En: "x", Zh: "y"}).In("ja"); got != "x" {
		t.Error("a Text without Japanese falls back to English:", got)
	}
	if got := (Attributes{}).Summary("ja"); got != "自動：モデルにおまかせ" {
		t.Error(got)
	}
}
