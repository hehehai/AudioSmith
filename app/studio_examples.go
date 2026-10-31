package main

// Text to speech's example chips (0.5 F3, after ElevenLabs' "Get started
// with"): under an empty script, a row of uses — a story, a product
// narration, a podcast intro… A click fills the script with an example of
// it, in the chosen voice's language when we have one in it (Chinese,
// English, Japanese), else in the interface's.

import (
	"strings"
	"unicode"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/lang"
)

// studioExample is one chip: its label (translated when shown), and the
// example in each language it has (base codes).
type studioExample struct {
	key   string
	label string
	icon  icons.Icon
	use   string // the gallery's use case the example calls for (recommendations)
	text  map[string]string
}

// studioExamples are the chips, in their order.
func studioExamples() []studioExample {
	return []studioExample{
		{"story", i18n.T("Tell a story"), storiesIcon, "narration", map[string]string{
			"zh": "从前，海边有一座小小的灯塔。每天傍晚，守塔的老人都会点亮灯火，给夜里归航的渔船指路。直到有一天，一只迷路的海鸥落在了他的窗台上……",
			"en": "Once upon a time, a small lighthouse stood at the edge of the sea. Every evening the old keeper lit its lamp to guide the fishing boats home. Then one night, a lost gull landed on his windowsill…",
			"ja": "むかしむかし、海辺に小さな灯台がありました。毎晩、灯台守のおじいさんは明かりをともし、夜の海から帰る漁船を導いていました。ある夜、迷子のカモメが窓辺に舞い降りてきたのです……",
		}},
		{"product", i18n.T("Product narration"), studioIcon, "announcing", map[string]string{
			"zh": "全新一代降噪耳机，轻到几乎感觉不到它的存在。一次充电，畅听三十小时。戴上它，世界安静下来，只留下你想听的声音。",
			"en": "Meet our lightest noise-cancelling headphones yet. Thirty hours of listening on a single charge. Put them on, and the world goes quiet — except for the sound you love.",
			"ja": "これまでで最も軽いノイズキャンセリングヘッドホン。一度の充電で三十時間。身につければ、世界は静まり、聴きたい音だけが残ります。",
		}},
		{"podcast", i18n.T("Podcast intro"), icons.Microphone3, "conversational", map[string]string{
			"zh": "大家好，欢迎收听新一期的《周末慢生活》。我是主持人小林。今天我们要聊的话题，是如何在忙碌的一周里，给自己留出一点安静的时间。",
			"en": "Hi everyone, and welcome back to Slow Weekends. I'm your host, Sam. Today we're talking about how to carve out a little quiet time in a busy week.",
			"ja": "みなさん、こんにちは。「週末スローライフ」の時間です。パーソナリティのリンです。今日は、忙しい一週間の中で自分だけの静かな時間をつくる方法についてお話しします。",
		}},
		{"news", i18n.T("News bulletin"), icons.FileText, "announcing", map[string]string{
			"zh": "各位听众，晚上好。今天上午，本市新开通的地铁三号线正式运营，全长二十八公里，设站二十一座，预计每天将服务约四十万人次。",
			"en": "Good evening. This morning the city's new Line 3 subway opened to passengers. The twenty-eight-kilometre line has twenty-one stations and is expected to carry about four hundred thousand riders a day.",
			"ja": "こんばんは。今朝、市内の地下鉄三号線が開業しました。全長二十八キロ、駅の数は二十一で、一日およそ四十万人の利用が見込まれています。",
		}},
		{"meditation", i18n.T("Guided meditation"), icons.SmileCircle, "narration", map[string]string{
			"zh": "找一个舒服的姿势坐下，轻轻闭上眼睛。慢慢吸气……再慢慢呼气。感受肩膀一点点放松下来。此刻，你什么都不需要做。",
			"en": "Find a comfortable position and gently close your eyes. Breathe in slowly… and breathe out. Feel your shoulders soften, little by little. Right now, there is nothing you need to do.",
			"ja": "楽な姿勢で座り、そっと目を閉じてください。ゆっくり息を吸って……ゆっくり吐きます。肩の力が少しずつ抜けていくのを感じましょう。今は、何もしなくていいのです。",
		}},
		{"audiobook", i18n.T("Audiobook excerpt"), audiobookIcon, "narration", map[string]string{
			"zh": "第一章　夜航渡轮\n\n雾从江面上漫过来的时候，渡轮已经离岸很远了。林舟靠在栏杆边，望着对岸零星的灯火，忽然想起了十年前离开家乡的那个夜晚。",
			"en": "Chapter One: The Night Ferry\n\nBy the time the fog rolled in over the river, the ferry was far from shore. Leaning on the rail, Lin watched the scattered lights of the far bank and remembered the night he left home, ten years ago.",
			"ja": "第一章　夜の渡し船\n\n川面に霧が広がるころ、渡し船はもう岸から遠く離れていた。手すりにもたれたリンは、対岸にまばらに灯る明かりを見つめ、十年前に故郷を出たあの夜を思い出していた。",
		}},
	}
}

// exampleLang is the language an example is filled in: the chosen voice's
// when an example has it, else the interface's (Chinese or English).
func (a *app) exampleLang() string {
	if l := a.studioVoiceLang(a.preset()); l != "" {
		if l == "zh" || l == "en" || l == "ja" {
			return l
		}
		return "en"
	}
	return catalog.BaseLanguage(i18n.Locale())
}

// fillExample puts example ex into the script (empty until then).
func (a *app) fillExample(ex studioExample) {
	text := ex.text[a.exampleLang()]
	if text == "" {
		text = ex.text["en"]
	}
	a.text = text // the editor takes it (syncScript)
	a.ttsUI.use = ex.use
}

// exampleChips is "Get started with" and the chips, under an empty script.
func (a *app) exampleChips(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	ui.Column(c.Key("examples")).Gap(px(t, sp2)).Shrink(0).Children(func() {
		ui.Text(c, i18n.T("Get started with")).FontSize(px(t, fsCaption)).TextColor(k.Text2)
		ui.Row(c).Wrap().GapX(px(t, sp2)).GapY(px(t, sp2)).Children(func() {
			for _, ex := range studioExamples() {
				b := ui.ButtonBase(c.Key("example-"+ex.key)).Gap(px(t, 6)).AlignItems(ui.Center).Padding(px(t, 5), px(t, 10)).
					Radius(px(t, radiusControl)).Border(1, k.Line).Label(ex.label).Tooltip(i18n.T("Fill the script with an example"))
				switch {
				case b.Pressed():
					b.Background(k.Pressed)
				case b.Hovered():
					b.Background(k.Hover)
				}
				b.Children(func() {
					ui.Icon(c, ex.icon.Linear).Size(px(t, 14), px(t, 14)).TextColor(k.Text2).Shrink(0)
					ui.Text(c, ex.label).FontSize(px(t, fsCaption)).SingleLine()
				})
				if b.Clicked() {
					a.fillExample(ex)
				}
			}
		})
	})
}

// studioVoiceLang is the language the Studio's voice speaks (base code),
// or "" when it speaks several or nobody knows: a saved voice's language,
// a speaker's, else the one language the model speaks.
func (a *app) studioVoiceLang(e catalog.Entry) string {
	if v := a.findVoice(a.studioVoice); v != nil {
		return catalog.BaseLanguage(v.Language)
	}
	if sp, _ := splitVoice(a.voice, e.AllVoiceIDs()); sp != "" {
		if l := e.SpeakerLanguage(sp); l != "" {
			return catalog.BaseLanguage(l)
		}
	}
	if len(e.Languages) == 1 {
		return catalog.BaseLanguage(e.Languages[0].Code)
	}
	return ""
}

// studioScriptLang is the language of the script (base code): the one
// chosen for it, else what its letters say (textLang), else the
// interface's.
func (a *app) studioScriptLang() string {
	if v := strings.TrimSpace(a.language); v != "" {
		for _, l := range a.preset().Languages {
			if l.Value == v {
				return catalog.BaseLanguage(l.Code)
			}
		}
		if l, ok := lang.Lookup(v); ok {
			return catalog.BaseLanguage(l.Code)
		}
	}
	if l := textLang(a.text); l != "" {
		return l
	}
	return catalog.BaseLanguage(i18n.Locale())
}

// textLang guesses a text's language from its letters: kana → ja, hangul
// → ko, Han → zh, Latin → en (Latin text is taken as English: the rule
// only steers recommendations); "" for no letters.
func textLang(s string) string {
	var han, kana, hangul, latin int
	for _, r := range s {
		switch {
		case unicode.In(r, unicode.Hiragana, unicode.Katakana):
			kana++
		case unicode.Is(unicode.Hangul, r):
			hangul++
		case unicode.Is(unicode.Han, r):
			han++
		case unicode.Is(unicode.Latin, r):
			latin++
		}
	}
	switch {
	case kana > 0 && kana*5 >= han:
		return "ja"
	case hangul > 0 && hangul >= han:
		return "ko"
	case han > 0 && han*2 >= latin/3:
		return "zh"
	case latin > 0:
		return "en"
	}
	return ""
}
