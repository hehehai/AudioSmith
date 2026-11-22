package voicedesign

// Japanese labels of the categories and their values (thread #173), kept
// apart from the tables so that attrs.go keeps its two-language layout.
// Only labels are UI: phrases and OmniVoice tags go to models, whose
// languages are English and Chinese.

var categoryJa = map[Category]string{
	Gender: "性別", Age: "年齢", Pitch: "声の高さ", Pace: "話す速さ", Style: "スタイル",
	Emotion: "感情", Accent: "英語のアクセント", Dialect: "中国語の方言",
}

var optionJa = map[Category]map[string]string{
	Gender: {"male": "男性", "female": "女性"},
	Age:    {"child": "子ども", "teen": "ティーン", "young_adult": "若者", "middle_aged": "中年", "elderly": "高齢"},
	Pitch:  {"very_low": "とても低い", "low": "低い", "moderate": "普通", "high": "高い", "very_high": "とても高い"},
	Pace:   {"very_slow": "とてもゆっくり", "slow": "ゆっくり", "moderate": "普通", "fast": "速い", "very_fast": "とても速い"},
	Style: {
		"whisper": "ささやき", "breathy": "息まじり", "raspy": "かすれ声", "nasal": "鼻にかかった", "smooth": "なめらか",
		"warm": "温かい", "bright": "明るい", "crisp": "歯切れのよい", "soft": "柔らかい", "resonant": "よく響く",
		"magnetic": "魅力的", "sweet": "甘い", "energetic": "元気", "calm": "落ち着いた", "authoritative": "威厳のある",
		"friendly": "親しみやすい", "playful": "お茶目", "dramatic": "ドラマチック", "professional": "プロらしい",
	},
	Emotion: {
		"neutral": "ニュートラル", "happy": "うれしい", "sad": "悲しい", "angry": "怒り", "excited": "興奮",
		"surprised": "驚き", "fearful": "おびえ", "tender": "優しい", "serious": "真剣", "mysterious": "ミステリアス",
	},
	Accent: {
		"american": "アメリカ", "british": "イギリス", "australian": "オーストラリア", "canadian": "カナダ",
		"indian": "インド", "chinese": "中国", "korean": "韓国", "japanese": "日本", "portuguese": "ポルトガル",
		"russian": "ロシア", "irish": "アイルランド", "scottish": "スコットランド", "new_zealand": "ニュージーランド",
		"south_african": "南アフリカ", "southern_us": "アメリカ南部", "french": "フランス", "german": "ドイツ", "spanish": "スペイン",
	},
	Dialect: {
		"sichuan": "四川", "northeastern": "東北", "henan": "河南", "shaanxi": "陝西", "guizhou": "貴州",
		"yunnan": "雲南", "guilin": "桂林", "jinan": "済南", "qingdao": "青島", "shijiazhuang": "石家荘",
		"gansu": "甘粛", "ningxia": "寧夏", "beijing": "北京", "tianjin": "天津", "cantonese": "広東なまり",
		"taiwanese": "台湾華語", "shanghai": "上海",
	},
}

func init() {
	for i := range categoryTable {
		c := &categoryTable[i]
		c.Label.Ja = categoryJa[c.ID]
		for j := range c.Options {
			c.Options[j].Label.Ja = optionJa[c.ID][c.Options[j].ID]
		}
	}
}
