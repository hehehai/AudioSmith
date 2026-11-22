package voicedesign

// Lowering a recipe for Irodori-TTS v4.1 (thread #170): a Japanese-only
// model whose voice field is a caption in Japanese ("落ち着いた自然な女性の
// 声"); its ModernBERT-ja encoder reads English or Chinese poorly. A
// description written in Japanese goes as written (with the picks beyond
// it); any other description is read for its attributes, which are
// written out in Japanese, and the rest is dropped.

import (
	"strings"
	"unicode"
)

// TargetIrodori: Irodori-TTS takes a Japanese caption (its row in
// lower.go's table has VocabTags: free text in another language is not
// passed on).
const TargetIrodori Target = "irodori"

// IsJapanese reports whether s has kana: Japanese, not Chinese.
func IsJapanese(s string) bool {
	for _, r := range s {
		if unicode.In(r, unicode.Hiragana, unicode.Katakana) && r != 'ー' && r != '・' {
			return true
		}
	}
	return false
}

func lowerIrodori(r Recipe, _ Options) Lowered {
	l := Lowered{Lang: "ja"}
	a := r.Attrs
	if IsJapanese(r.Description) {
		var extra Attributes
		for _, p := range a.Diff(Parse(r.Description).Attrs) {
			extra, _, _ = extra.With(p.Category, p.ID)
		}
		a = extra
		l.Voice = r.Description
	} else {
		l.DescriptionDropped = r.Description != ""
	}
	for _, c := range []Category{Accent, Dialect} {
		if v := a.Value(c); v != "" {
			l.Dropped = append(l.Dropped, Dropped{Value{c, v}, "Irodori-TTS speaks Japanese only"})
		}
	}
	a.Accent, a.Dialect = "", ""
	l.Voice = joinJA(l.Voice, sentenceJA(a))
	l.Used = allValues(r.Attrs)
	return l
}

func joinJA(a, b string) string {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == "" || b == "" {
		return a + b
	}
	if !strings.ContainsAny(a[len(a)-1:], ".!?") && !strings.HasSuffix(a, "。") && !strings.HasSuffix(a, "！") && !strings.HasSuffix(a, "？") {
		a += "。"
	}
	return a + b
}

var (
	jaNoun = map[string][3]string{ // male, female, unknown
		"child":       {"男の子", "女の子", "子ども"},
		"teen":        {"少年", "少女", "十代の若者"},
		"young_adult": {"若い男性", "若い女性", "若い人"},
		"middle_aged": {"中年の男性", "中年の女性", "中年の人"},
		"elderly":     {"年配の男性", "年配の女性", "年配の人"},
		"":            {"男性", "女性", ""},
	}
	jaPitch = map[string]string{
		"very_low": "とても低い", "low": "低めの", "moderate": "普通の高さの", "high": "高めの", "very_high": "とても高い",
	}
	jaStyle = map[string]string{
		"breathy": "息まじりの", "raspy": "かすれた", "nasal": "鼻にかかった", "smooth": "なめらかな", "warm": "温かみのある",
		"bright": "明るい", "crisp": "はっきりした", "soft": "柔らかい", "resonant": "よく響く", "magnetic": "深みのある",
		"sweet": "甘い", "energetic": "元気な", "calm": "落ち着いた", "authoritative": "威厳のある", "friendly": "親しみやすい",
		"playful": "いたずらっぽい", "dramatic": "表情豊かな", "professional": "洗練された",
	}
	jaPace = map[string]string{
		"very_slow": "とてもゆっくりと", "slow": "ゆっくりと", "moderate": "普通の速さで", "fast": "速めに", "very_fast": "とても速く",
	}
	jaEmotion = map[string]string{
		"neutral": "淡々と", "happy": "楽しそうに", "sad": "悲しげに", "angry": "怒った様子で", "excited": "興奮気味に",
		"surprised": "驚いた様子で", "fearful": "怯えた様子で", "tender": "優しく", "serious": "真剣に", "mysterious": "神秘的に",
	}
)

// sentenceJA describes attributes in Japanese, as Irodori's captions do:
// "温かみのある、低めの声の若い女性。ゆっくりと、優しく話す。" A slow pace
// is left out as in Sentence; "" for auto attributes.
func sentenceJA(a Attributes) string {
	a = a.Canonical()
	if slowPace(a.Pace) {
		a.Pace = ""
	}
	var adjs []string
	whisper := false
	for _, s := range a.Styles {
		if s == "whisper" {
			whisper = true
		} else if p := jaStyle[s]; p != "" {
			adjs = append(adjs, p)
		}
	}
	voice := strings.Join(adjs, "、")
	if p := jaPitch[a.Pitch]; p != "" {
		if voice != "" {
			voice += "、"
		}
		voice += p
	}
	g := map[string]int{"male": 0, "female": 1}[a.Gender]
	if a.Gender == "" {
		g = 2
	}
	noun := jaNoun[a.Age][g]
	var first string
	switch {
	case noun != "" && voice != "":
		first = voice + "声の" + noun + "。"
	case noun != "":
		first = noun + "の声。"
	case voice != "":
		first = voice + "声。"
	}
	var how []string
	if p := jaPace[a.Pace]; p != "" {
		how = append(how, p)
	}
	if whisper {
		how = append(how, "ささやくように")
	}
	if e := jaEmotion[a.Emotion]; e != "" {
		how = append(how, e)
	}
	if len(how) > 0 {
		first += strings.Join(how, "、") + "話す。"
	}
	return first
}
