package gallery

// Japanese names of the use cases, the language facet and the Design
// page's presets (thread #173), kept apart from their tables so that
// those keep their two-language layout. A preset's description stays
// English for ja: it is the text the Design page parses, and the parser
// reads English and Chinese. The curated voices carry "ja" in
// data/curated.json.

var categoryJa = map[string][2]string{
	"narration":      {"ナレーション・オーディオブック", "物語、小説、ドキュメンタリー、長文の朗読。"},
	"conversational": {"会話・ポッドキャスト", "司会、雑談、対話、日常の話し言葉。"},
	"characters":     {"キャラクター・子ども", "キャラクター、子ども、お年寄り、悪役。"},
	"announcing":     {"広告・アナウンス", "ニュース、広告、予告編、ガイド、解説。"},
}

var languageJa = map[string]string{
	"en": "英語", "zh": "中国語", "es": "スペイン語", "fr": "フランス語", "de": "ドイツ語", "it": "イタリア語",
	"pt": "ポルトガル語", "ru": "ロシア語", "hi": "ヒンディー語", "ja": "日本語", "ko": "韓国語",
}

var presetJa = map[string]string{
	"narrator": "ナレーター", "conversational": "会話", "newsreader": "ニュースキャスター", "presenter": "プレゼンター",
	"storyteller": "語り部", "elder": "長老", "villain": "悪役", "soft": "やわらか", "upbeat": "元気",
	"whisper": "ささやき", "kid": "子ども", "northeast-buddy": "東北（中国）のあんちゃん", "calm-guide": "穏やかなガイド",
}

func init() {
	for i := range categories {
		if t, ok := categoryJa[categories[i].ID]; ok {
			categories[i].Name.Ja, categories[i].Description.Ja = t[0], t[1]
		}
	}
	for code, name := range languageJa {
		t := languageNames[code]
		t.Ja = name
		languageNames[code] = t
	}
	for i := range presets {
		presets[i].Name.Ja = presetJa[presets[i].ID]
	}
}
