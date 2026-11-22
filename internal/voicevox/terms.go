package voicevox

// The characters' terms and the credit their audio carries.
//
// VOICEVOX: every character may be used once its audio is credited
// "VOICEVOX:<name>"; a few limit commercial use. The table below is from
// the voicevox_vvm README (release 0.17.1, checked 2026-11-22, research
// voicevox/report.md §2); a character it does not know is credited the
// usual way and its terms are to be checked (UseAsk). The engine's own
// /speaker_info policy is the character's terms in full: the app shows it.
//
// AivisSpeech: the software needs no credit; each model carries its
// licence (/speaker_info policy), mostly the Aivis Common Model License
// (ACML 1.0: commercial use, credit optional), ACML-NC (non-commercial)
// or CC0. Another licence is the model author's own: to be read, and
// credited "AivisSpeech:<name>" to be safe.

import "strings"

// Use is what a character's terms allow.
type Use string

const (
	UseCommercial    Use = "commercial"    // commercial and other use, with the credit
	UseIndividual    Use = "individual"    // individuals commercially; companies ask first
	UseNonCommercial Use = "noncommercial" // non-commercial use only (fan works, streams); ask for more
	UseAsk           Use = "ask"           // ask the rights holder before commercial use, or unknown terms
)

// Terms are a character's (or an AivisSpeech model's) terms in short.
type Terms struct {
	Use Use `json:"use"`
	// Credit is the attribution its audio carries ("VOICEVOX:ずんだもん");
	// "" when none is needed.
	Credit string `json:"credit,omitempty"`
	// Licence names an AivisSpeech model's licence ("ACML 1.0"); "" for
	// VOICEVOX's characters.
	Licence string `json:"licence,omitempty"`
	// Note is an extra condition, in English (the UI translates it).
	Note string `json:"note,omitempty"`
	// URL is where the terms are.
	URL string `json:"url,omitempty"`
}

// Confirm reports whether the first use asks the user to confirm the
// terms: non-commercial characters and those to ask about.
func (t Terms) Confirm() bool { return t.Use == UseNonCommercial || t.Use == UseAsk }

// Terms URLs.
const (
	voicevoxTermsURL = "https://voicevox.hiroshiba.jp/term/"
	zunkoTermsURL    = "https://zunko.jp/con_ongen_kiyaku.html"
	acmlURL          = "https://github.com/Aivis-Project/ACML"
)

// Notes of the table (English; translated by the UI).
const (
	NoteCompanies = "Companies and organisations must ask the rights holder first."
	NoteMochiko   = "Not for voice works, voice material or games; companies ask first."
	NoteZunko     = "Tohoku Zunko project: no political or religious use, no misleading content, see the project's terms."
	NoteUnknown   = "Terms not in AudioSmith's table: read them in the engine before using its audio."
	NoteCustom    = "The model's own licence: read it before using its audio."
)

// voicevoxSpecial are the characters whose terms differ from "credit
// VOICEVOX:<name>, any use", by the name /speakers gives.
var voicevoxSpecial = map[string]Terms{
	"No.7":     {Use: UseNonCommercial},
	"ユーレイちゃん":  {Use: UseNonCommercial, Credit: "VOICEVOX:ユーレイちゃん(CV:神崎零)"},
	"青山龍星":     {Use: UseIndividual, Note: NoteCompanies},
	"後鬼":       {Use: UseIndividual, Note: NoteCompanies},
	"もち子さん":    {Use: UseIndividual, Credit: "VOICEVOX:もち子(cv 明日葉よもぎ)", Note: NoteMochiko},
	"Voidoll":  {Use: UseIndividual, Credit: "VOICEVOX:Voidoll(CV:丹下桜)", Note: NoteCompanies},
	"ぞん子":      {Use: UseAsk},
	"里石ユカ":     {Use: UseCommercial, Credit: "VOICEVOX:里石ユカ（つぼみ）"},
	"雨晴はう":     {Use: UseAsk, Note: NoteUnknown},
}

// voicevoxKnown are the characters of voicevox_vvm 0.17.1 credited the
// usual way: the rest of the table. Those of the Tohoku Zunko project
// carry its common prohibitions.
var voicevoxKnown = map[string]bool{
	"四国めたん": true, "ずんだもん": true, "春日部つむぎ": true, "波音リツ": true, "玄野武宏": true,
	"白上虎太郎": true, "冥鳴ひまり": true, "九州そら": true, "剣崎雌雄": true, "WhiteCUL": true,
	"ちび式じい": true, "櫻歌ミコ": true, "小夜/SAYO": true, "ナースロボ＿タイプＴ": true, "†聖騎士 紅桜†": true,
	"雀松朱司": true, "麒ヶ島宗麟": true, "春歌ナナ": true, "猫使アル": true, "猫使ビィ": true,
	"中国うさぎ": true, "栗田まろん": true, "あいえるたん": true, "満別花丸": true, "琴詠ニア": true,
	"中部つるぎ": true, "離途": true, "黒沢冴白": true, "東北ずん子": true, "東北きりたん": true,
	"東北イタコ": true, "あんこもん": true,
}

// zunko are the Tohoku Zunko project's characters.
var zunko = map[string]bool{
	"ずんだもん": true, "四国めたん": true, "九州そら": true, "東北ずん子": true, "東北きりたん": true,
	"東北イタコ": true, "中国うさぎ": true, "中部つるぎ": true, "あんこもん": true,
}

// VoicevoxTerms are the terms of VOICEVOX's character name.
func VoicevoxTerms(name string) Terms {
	name = strings.TrimSpace(name)
	t, special := voicevoxSpecial[name]
	if !special {
		t = Terms{Use: UseCommercial}
		if !voicevoxKnown[name] {
			t = Terms{Use: UseAsk, Note: NoteUnknown}
		}
	}
	if t.Credit == "" {
		t.Credit = "VOICEVOX:" + name
	}
	if zunko[name] {
		t.Note, t.URL = NoteZunko, zunkoTermsURL
	}
	if t.URL == "" {
		t.URL = voicevoxTermsURL
	}
	return t
}

// AivisTerms are the terms of AivisSpeech's character name, whose model's
// licence is policy (Markdown or text; "" = not known yet).
func AivisTerms(name, policy string) Terms {
	p := strings.ToLower(policy)
	acml := strings.Contains(p, "aivis common model license") || strings.Contains(p, "acml")
	switch {
	case acml && (strings.Contains(p, "non-commercial") || strings.Contains(p, "non commercial") || strings.Contains(p, "acml-nc")):
		return Terms{Use: UseNonCommercial, Licence: "ACML-NC 1.0", URL: acmlURL}
	case acml:
		return Terms{Use: UseCommercial, Licence: "ACML 1.0", URL: acmlURL}
	case strings.Contains(p, "cc0") || strings.Contains(p, "creative commons zero") || strings.Contains(p, "public domain dedication"):
		return Terms{Use: UseCommercial, Licence: "CC0 1.0"}
	}
	return Terms{Use: UseAsk, Credit: "AivisSpeech:" + strings.TrimSpace(name), Licence: "custom", Note: NoteCustom}
}

// TermsOf are the terms of a character of engine kind.
func TermsOf(k Kind, name, policy string) Terms {
	if k == Aivis {
		return AivisTerms(name, policy)
	}
	return VoicevoxTerms(name)
}
