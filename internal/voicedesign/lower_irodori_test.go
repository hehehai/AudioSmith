package voicedesign

import (
	"strings"
	"testing"
)

// Every value Irodori can say has a Japanese phrase (accents and dialects
// are dropped: it speaks Japanese only).
func TestIrodoriPhrases(t *testing.T) {
	tables := map[Category]map[string]string{Pitch: jaPitch, Pace: jaPace, Emotion: jaEmotion, Style: jaStyle}
	for c, m := range tables {
		for _, op := range ValuesOf(c) {
			if m[op.ID] == "" && !(c == Style && op.ID == "whisper") {
				t.Errorf("%s/%s has no Japanese phrase", c, op.ID)
			}
		}
	}
	for _, op := range ValuesOf(Age) {
		if n := jaNoun[op.ID]; n[0] == "" || n[1] == "" || n[2] == "" {
			t.Errorf("age %s has no Japanese noun", op.ID)
		}
	}
}

func TestLowerIrodori(t *testing.T) {
	attrs := func(kv ...string) Attributes {
		var a Attributes
		for i := 0; i < len(kv); i += 2 {
			a, _, _ = a.With(Category(kv[i]), kv[i+1])
		}
		return a
	}
	cases := []struct {
		r       Recipe
		want    string
		dropped bool
	}{
		{Recipe{Attrs: attrs("gender", "female", "age", "young_adult", "style", "warm", "pitch", "low", "emotion", "tender")},
			"温かみのある、低めの声の若い女性。優しく話す。", false},
		{Recipe{Attrs: attrs("gender", "male", "style", "whisper", "pace", "fast")}, "男性の声。速めに、ささやくように話す。", false},
		// A Japanese description goes as written, with the picks beyond it.
		{Recipe{Description: "落ち着いた自然な女性の声", Attrs: attrs("gender", "female", "emotion", "happy")},
			"落ち着いた自然な女性の声。楽しそうに話す。", false},
		// Chinese or English: only what maps, in Japanese.
		{Recipe{Description: "一位温暖的中年男性", Attrs: attrs("gender", "male", "age", "middle_aged", "style", "warm")},
			"温かみのある声の中年の男性。", true},
		{Recipe{Description: "A bright girl with a British accent", Attrs: attrs("gender", "female", "age", "child", "style", "bright", "accent", "british")},
			"明るい声の女の子。", true},
	}
	for _, c := range cases {
		l, err := Lower(TargetIrodori, c.r, Options{Lang: "zh"})
		if err != nil || l.Voice != c.want || l.DescriptionDropped != c.dropped || l.Lang != "ja" {
			t.Errorf("%+v: %q dropped %v lang %s (%v)", c.r, l.Voice, l.DescriptionDropped, l.Lang, err)
		}
	}
	l, _ := Lower(TargetIrodori, Recipe{Attrs: attrs("accent", "british", "gender", "female")}, Options{})
	if len(l.Dropped) != 1 || l.Dropped[0].Category != Accent || strings.Contains(l.Voice, "British") {
		t.Errorf("accent: %+v", l)
	}
	if !IsJapanese("ゆっくり話す") || IsJapanese("温暖的声音") || IsJapanese("warm voice") {
		t.Error("IsJapanese")
	}
}
