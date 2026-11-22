package voicedesign

// Lowering a recipe for Qwen3-TTS CustomVoice: a named speaker plus an
// instruction; split from lower.go.

import (
	"slices"
	"strings"
)

// Speaker is one of Qwen3-TTS CustomVoice's named speakers, described by
// the attributes it is closest to (from the public model card).
type Speaker struct {
	ID    string
	Lang  string // native language
	Attrs Attributes
	About Text
}

// CustomVoiceSpeakers lists the Qwen3-TTS CustomVoice speakers. The 0.6B
// and 1.7B models share them; some quantised builds ship fewer (see
// Options.Speakers).
var CustomVoiceSpeakers = []Speaker{
	{"Vivian", "zh", Attributes{Gender: "female", Age: "young_adult", Styles: []string{"bright"}}, Text{En: "Bright young female voice", Zh: "明亮的年轻女声", Ja: "明るい若い女性の声"}},
	{"Serena", "zh", Attributes{Gender: "female", Age: "young_adult", Styles: []string{"warm", "soft"}}, Text{En: "Warm, gentle young female voice", Zh: "温暖柔和的年轻女声", Ja: "温かく穏やかな若い女性の声"}},
	{"Uncle_Fu", "zh", Attributes{Gender: "male", Age: "middle_aged", Pitch: "low", Styles: []string{"smooth"}}, Text{En: "Seasoned male voice with a low, mellow timbre", Zh: "低沉醇厚的成熟男声", Ja: "低くまろやかな、落ち着いた男性の声"}},
	{"Dylan", "zh", Attributes{Gender: "male", Age: "young_adult", Dialect: "beijing"}, Text{En: "Young Beijing male voice", Zh: "北京口音的年轻男声", Ja: "北京なまりの若い男性の声"}},
	{"Eric", "zh", Attributes{Gender: "male", Age: "young_adult", Dialect: "sichuan", Styles: []string{"energetic"}}, Text{En: "Lively Chengdu (Sichuan) male voice", Zh: "活泼的成都男声", Ja: "元気な成都（四川）の男性の声"}},
	{"Ryan", "en", Attributes{Gender: "male", Age: "young_adult", Styles: []string{"energetic"}}, Text{En: "Dynamic English male voice", Zh: "富有活力的英语男声", Ja: "エネルギッシュな英語の男性の声"}},
	{"Aiden", "en", Attributes{Gender: "male", Age: "young_adult", Accent: "american", Styles: []string{"bright"}}, Text{En: "Sunny American male voice", Zh: "阳光的美式英语男声", Ja: "陽気なアメリカ英語の男性の声"}},
	{"Ono_Anna", "ja", Attributes{Gender: "female", Age: "young_adult", Styles: []string{"playful"}}, Text{En: "Playful Japanese female voice", Zh: "俏皮的日语女声", Ja: "お茶目な日本語の女性の声"}},
	{"Sohee", "ko", Attributes{Gender: "female", Age: "young_adult", Styles: []string{"warm"}}, Text{En: "Warm Korean female voice", Zh: "温暖的韩语女声", Ja: "温かい韓国語の女性の声"}},
}

// BestSpeaker picks the CustomVoice speaker closest to the attributes for
// speech in lang ("" = any), among allowed (empty = all).
func BestSpeaker(a Attributes, lang string, allowed []string) Speaker {
	best, bestScore := Speaker{}, -1<<30
	for _, s := range CustomVoiceSpeakers {
		if len(allowed) > 0 && !slices.Contains(allowed, s.ID) {
			continue
		}
		score := 0
		switch {
		case a.Gender == "":
		case a.Gender == s.Attrs.Gender:
			score += 8
		default:
			score -= 8
		}
		if a.Dialect != "" && a.Dialect == s.Attrs.Dialect {
			score += 6
		} else if s.Attrs.Dialect != "" {
			score -= 2 // a dialect speaker only when asked for
		}
		if a.Accent != "" && a.Accent == s.Attrs.Accent {
			score += 3
		}
		want := lang
		if want == "" {
			want = a.Language()
		}
		if want != "" && want == s.Lang {
			score += 4
		}
		if a.Age != "" {
			if a.Age == s.Attrs.Age {
				score += 2
			} else if (a.Age == "elderly" || a.Age == "middle_aged") && s.Attrs.Age == "middle_aged" {
				score += 2
			}
		}
		if a.Pitch != "" && a.Pitch == s.Attrs.Pitch {
			score++
		}
		for _, st := range a.Styles {
			if s.Attrs.HasStyle(st) {
				score++
			}
		}
		if score > bestScore {
			best, bestScore = s, score
		}
	}
	return best
}

func lowerCustomVoice(r Recipe, o Options) Lowered {
	a := r.Attrs
	lang := pickLang(r, o)
	var sp Speaker
	if o.Speaker != "" {
		sp = Speaker{ID: o.Speaker}
		for _, s := range CustomVoiceSpeakers {
			if strings.EqualFold(s.ID, o.Speaker) {
				sp = s
			}
		}
	} else {
		sp = BestSpeaker(a, "", o.Speakers)
	}
	l := Lowered{Speaker: sp.ID, Lang: lang, DescriptionDropped: r.Description != ""}
	// Identity comes from the speaker: report what it doesn't match.
	for _, c := range []Category{Gender, Age, Pitch, Accent, Dialect} {
		if v := a.Value(c); v != "" {
			if sp.Attrs.Value(c) == v {
				l.Used = append(l.Used, Value{c, v})
			} else {
				l.Dropped = append(l.Dropped, Dropped{Value{c, v}, "fixed by the speaker " + sp.ID})
			}
		}
	}
	// Delivery goes into the instruction.
	var styles []string
	whisper := false
	for _, s := range a.Styles {
		l.Used = append(l.Used, Value{Style, s})
		if s == "whisper" {
			whisper = true
			continue
		}
		styles = append(styles, phrase(Style, s, lang))
	}
	if a.Pace != "" {
		l.Used = append(l.Used, Value{Pace, a.Pace})
	}
	if a.Emotion != "" {
		l.Used = append(l.Used, Value{Emotion, a.Emotion})
	}
	var instr string
	if lang == "zh" {
		var parts []string
		if len(styles) > 0 {
			parts = append(parts, "用"+strings.Join(styles, "、")+"的声音说话")
		}
		if whisper {
			parts = append(parts, "轻声耳语")
		}
		if a.Emotion != "" {
			parts = append(parts, "语气"+phrase(Emotion, a.Emotion, "zh"))
		}
		if a.Pace != "" {
			parts = append(parts, phrase(Pace, a.Pace, "zh"))
		}
		if len(parts) > 0 {
			instr = strings.Join(parts, "，") + "。"
		}
	} else {
		var how []string
		if a.Pace != "" {
			how = append(how, phrase(Pace, a.Pace, "en"))
		}
		if whisper {
			how = append(how, "in a whisper")
		}
		if len(styles) > 0 {
			j := joinEN(styles)
			how = append(how, "in "+strings.ToLower(article(j))+" "+j+" voice")
		}
		if a.Emotion != "" {
			how = append(how, "sounding "+phrase(Emotion, a.Emotion, "en"))
		}
		if len(how) > 0 {
			instr = "Speak " + strings.Join(how, ", ") + "."
		}
	}
	l.Voice = sp.ID
	if instr != "" {
		l.Voice += ", " + instr
	}
	return l
}
