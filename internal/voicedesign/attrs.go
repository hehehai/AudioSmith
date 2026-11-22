// Package voicedesign is the data and logic layer of Voice Design: the
// attribute model a designed voice is made of, a deterministic parser from
// English and Chinese free text to attributes, and the lowering of a recipe
// (description + attributes + seed) to what each design-capable engine
// takes as its voice/instruct field.
//
// Everything here is pure Go, allocation-light and safe for concurrent use;
// the tables are package data built once.
package voicedesign

import (
	"fmt"
	"slices"
	"strings"
)

// Text is a user-visible string in English, Simplified Chinese and
// (thread #173, mostly for labels) Japanese. The UI picks one by locale;
// no i18n catalog is involved.
type Text struct {
	En string `json:"en"`
	Zh string `json:"zh"`
	Ja string `json:"ja,omitempty"`
}

// In returns the Chinese text for "zh…" locales and the Japanese one for
// "ja…" locales when present, else English.
func (t Text) In(locale string) string {
	l := strings.ToLower(locale)
	switch {
	case strings.HasPrefix(l, "zh") && t.Zh != "":
		return t.Zh
	case strings.HasPrefix(l, "ja") && t.Ja != "":
		return t.Ja
	}
	return t.En
}

// Category is one dimension of a designed voice.
type Category string

const (
	Gender  Category = "gender"
	Age     Category = "age"
	Pitch   Category = "pitch"
	Pace    Category = "pace"
	Style   Category = "style" // several values allowed (up to MaxStyles)
	Emotion Category = "emotion"
	Accent  Category = "accent"  // English accent; excludes Dialect
	Dialect Category = "dialect" // Chinese dialect / regional Mandarin; excludes Accent
)

// Categories lists every category in display order.
var Categories = []Category{Gender, Age, Pitch, Pace, Style, Emotion, Accent, Dialect}

// MaxStyles is how many style values one voice may combine.
const MaxStyles = 3

// Option is one value of a category.
type Option struct {
	ID    string // stable value stored in recipes: "young_adult", "british"
	Label Text   // short label for chips: "Young adult" / "青年"
	// Phrase is how the value reads inside a sentence (Qwen3 instruct):
	// en "warm", zh "温暖"; for accents/dialects the full phrase
	// ("a British accent" / "英式口音").
	Phrase Text
	// Omni is OmniVoice's tag for the value, in its English and Chinese
	// forms ("" = not in OmniVoice's vocabulary; dialects only have Zh).
	Omni Text
}

// CategoryInfo describes a category and its values.
type CategoryInfo struct {
	ID      Category
	Label   Text
	Options []Option
	Multi   bool // Style: several values allowed
}

func o(id, en, zh, pen, pzh, oen, ozh string) Option {
	return Option{ID: id, Label: Text{En: en, Zh: zh}, Phrase: Text{En: pen, Zh: pzh}, Omni: Text{En: oen, Zh: ozh}}
}

// The tables. Option order is display order and the canonical order of
// Styles. OmniVoice tags follow its published voice-design vocabulary
// (gender, age, 5 pitch levels, whisper, 10 English accents, 12 dialects).
var categoryTable = []CategoryInfo{
	{ID: Gender, Label: Text{En: "Gender", Zh: "性别"}, Options: []Option{
		o("male", "Male", "男", "male", "男性", "male", "男"),
		o("female", "Female", "女", "female", "女性", "female", "女"),
	}},
	{ID: Age, Label: Text{En: "Age", Zh: "年龄"}, Options: []Option{
		o("child", "Child", "儿童", "child", "儿童", "child", "儿童"),
		o("teen", "Teenager", "少年", "teenage", "少年", "teenager", "少年"),
		o("young_adult", "Young adult", "青年", "young adult", "青年", "young adult", "青年"),
		o("middle_aged", "Middle-aged", "中年", "middle-aged", "中年", "middle-aged", "中年"),
		o("elderly", "Elderly", "老年", "elderly", "老年", "elderly", "老年"),
	}},
	{ID: Pitch, Label: Text{En: "Pitch", Zh: "音高"}, Options: []Option{
		o("very_low", "Very low", "很低", "very deep, low-pitched", "非常低沉", "very low pitch", "极低音调"),
		o("low", "Low", "低", "deep, low-pitched", "低沉", "low pitch", "低音调"),
		o("moderate", "Moderate", "适中", "medium-pitched", "音调适中", "moderate pitch", "中音调"),
		o("high", "High", "高", "high-pitched", "音调偏高", "high pitch", "高音调"),
		o("very_high", "Very high", "很高", "very high-pitched", "音调很高", "very high pitch", "极高音调"),
	}},
	{ID: Pace, Label: Text{En: "Pace", Zh: "语速"}, Options: []Option{
		o("very_slow", "Very slow", "很慢", "very slowly", "语速很慢", "", ""),
		o("slow", "Slow", "慢", "slowly", "语速偏慢", "", ""),
		o("moderate", "Moderate", "适中", "at a moderate pace", "语速适中", "", ""),
		o("fast", "Fast", "快", "quickly", "语速偏快", "", ""),
		o("very_fast", "Very fast", "很快", "very quickly", "语速很快", "", ""),
	}},
	{ID: Style, Label: Text{En: "Style", Zh: "风格"}, Multi: true, Options: []Option{
		o("whisper", "Whisper", "耳语", "whispering", "耳语般", "whisper", "耳语"),
		o("breathy", "Breathy", "气声", "breathy", "带气声", "", ""),
		o("raspy", "Raspy", "沙哑", "raspy", "沙哑", "", ""),
		o("nasal", "Nasal", "鼻音", "nasal", "带鼻音", "", ""),
		o("smooth", "Smooth", "顺滑", "smooth", "圆润顺滑", "", ""),
		o("warm", "Warm", "温暖", "warm", "温暖", "", ""),
		o("bright", "Bright", "明亮", "bright", "明亮", "", ""),
		o("crisp", "Crisp", "清晰", "crisp", "清晰干净", "", ""),
		o("soft", "Soft", "柔和", "soft", "轻柔", "", ""),
		o("resonant", "Resonant", "浑厚", "resonant", "浑厚", "", ""),
		o("magnetic", "Magnetic", "磁性", "magnetic", "富有磁性", "", ""),
		o("sweet", "Sweet", "甜美", "sweet", "甜美", "", ""),
		o("energetic", "Energetic", "活力", "energetic", "充满活力", "", ""),
		o("calm", "Calm", "沉稳", "calm", "沉稳", "", ""),
		o("authoritative", "Authoritative", "权威", "authoritative", "有权威感", "", ""),
		o("friendly", "Friendly", "亲切", "friendly", "亲切", "", ""),
		o("playful", "Playful", "俏皮", "playful", "俏皮", "", ""),
		o("dramatic", "Dramatic", "戏剧化", "dramatic", "富有戏剧性", "", ""),
		o("professional", "Professional", "专业", "polished", "专业", "", ""),
	}},
	{ID: Emotion, Label: Text{En: "Emotion", Zh: "情绪"}, Options: []Option{
		o("neutral", "Neutral", "中性", "neutral", "平和", "", ""),
		o("happy", "Happy", "开心", "happy", "开心", "", ""),
		o("sad", "Sad", "悲伤", "sad", "悲伤", "", ""),
		o("angry", "Angry", "愤怒", "angry", "愤怒", "", ""),
		o("excited", "Excited", "兴奋", "excited", "兴奋", "", ""),
		o("surprised", "Surprised", "惊讶", "surprised", "惊讶", "", ""),
		o("fearful", "Fearful", "害怕", "fearful", "害怕", "", ""),
		o("tender", "Tender", "温柔", "tender", "温柔", "", ""),
		o("serious", "Serious", "严肃", "serious", "严肃", "", ""),
		o("mysterious", "Mysterious", "神秘", "mysterious", "神秘", "", ""),
	}},
	{ID: Accent, Label: Text{En: "English accent", Zh: "英语口音"}, Options: []Option{
		o("american", "American", "美式", "an American accent", "美式口音", "american accent", ""),
		o("british", "British", "英式", "a British accent", "英式口音", "british accent", ""),
		o("australian", "Australian", "澳洲", "an Australian accent", "澳洲口音", "australian accent", ""),
		o("canadian", "Canadian", "加拿大", "a Canadian accent", "加拿大口音", "canadian accent", ""),
		o("indian", "Indian", "印度", "an Indian accent", "印度口音", "indian accent", ""),
		o("chinese", "Chinese", "中式", "a Chinese accent", "中式口音", "chinese accent", ""),
		o("korean", "Korean", "韩式", "a Korean accent", "韩式口音", "korean accent", ""),
		o("japanese", "Japanese", "日式", "a Japanese accent", "日式口音", "japanese accent", ""),
		o("portuguese", "Portuguese", "葡萄牙", "a Portuguese accent", "葡萄牙口音", "portuguese accent", ""),
		o("russian", "Russian", "俄式", "a Russian accent", "俄式口音", "russian accent", ""),
		o("irish", "Irish", "爱尔兰", "an Irish accent", "爱尔兰口音", "", ""),
		o("scottish", "Scottish", "苏格兰", "a Scottish accent", "苏格兰口音", "", ""),
		o("new_zealand", "New Zealand", "新西兰", "a New Zealand accent", "新西兰口音", "", ""),
		o("south_african", "South African", "南非", "a South African accent", "南非口音", "", ""),
		o("southern_us", "Southern US", "美国南方", "a Southern American accent", "美国南方口音", "", ""),
		o("french", "French", "法式", "a French accent", "法式口音", "", ""),
		o("german", "German", "德式", "a German accent", "德式口音", "", ""),
		o("spanish", "Spanish", "西班牙", "a Spanish accent", "西班牙口音", "", ""),
	}},
	{ID: Dialect, Label: Text{En: "Chinese dialect", Zh: "中文方言"}, Options: []Option{
		o("sichuan", "Sichuan", "四川话", "a Sichuan accent", "四川话", "", "四川话"),
		o("northeastern", "Northeastern", "东北话", "a Northeastern Chinese accent", "东北话", "", "东北话"),
		o("henan", "Henan", "河南话", "a Henan accent", "河南话", "", "河南话"),
		o("shaanxi", "Shaanxi", "陕西话", "a Shaanxi accent", "陕西话", "", "陕西话"),
		o("guizhou", "Guizhou", "贵州话", "a Guizhou accent", "贵州话", "", "贵州话"),
		o("yunnan", "Yunnan", "云南话", "a Yunnan accent", "云南话", "", "云南话"),
		o("guilin", "Guilin", "桂林话", "a Guilin accent", "桂林话", "", "桂林话"),
		o("jinan", "Jinan", "济南话", "a Jinan (Shandong) accent", "济南话", "", "济南话"),
		o("qingdao", "Qingdao", "青岛话", "a Qingdao accent", "青岛话", "", "青岛话"),
		o("shijiazhuang", "Shijiazhuang", "石家庄话", "a Shijiazhuang (Hebei) accent", "石家庄话", "", "石家庄话"),
		o("gansu", "Gansu", "甘肃话", "a Gansu accent", "甘肃话", "", "甘肃话"),
		o("ningxia", "Ningxia", "宁夏话", "a Ningxia accent", "宁夏话", "", "宁夏话"),
		o("beijing", "Beijing", "北京话", "a Beijing accent", "京腔", "", ""),
		o("tianjin", "Tianjin", "天津话", "a Tianjin accent", "天津话", "", ""),
		o("cantonese", "Cantonese-accented", "广普", "a Cantonese accent", "广东口音的普通话", "", ""),
		o("taiwanese", "Taiwanese Mandarin", "台湾腔", "a Taiwanese accent", "台湾腔", "", ""),
		o("shanghai", "Shanghai", "上海口音", "a Shanghainese accent", "上海口音", "", ""),
	}},
}

var (
	catIndex = map[Category]int{}
	optIndex = map[Category]map[string]int{}
)

func init() {
	for i, c := range categoryTable {
		catIndex[c.ID] = i
		m := map[string]int{}
		for j, op := range c.Options {
			if _, dup := m[op.ID]; dup {
				panic("voicedesign: duplicate option " + string(c.ID) + "/" + op.ID)
			}
			m[op.ID] = j
		}
		optIndex[c.ID] = m
	}
}

// Info returns a category's description (false for unknown categories).
// The returned value shares the package tables: do not modify it.
func Info(c Category) (CategoryInfo, bool) {
	i, ok := catIndex[c]
	if !ok {
		return CategoryInfo{}, false
	}
	return categoryTable[i], true
}

// ValuesOf lists a category's values in display order.
func ValuesOf(c Category) []Option {
	info, _ := Info(c)
	return info.Options
}

// Lookup finds one value of a category.
func Lookup(c Category, id string) (Option, bool) {
	j, ok := optIndex[c][id]
	if !ok {
		return Option{}, false
	}
	return categoryTable[catIndex[c]].Options[j], true
}

// Valid reports whether id is a value of c ("" = Auto is valid).
func Valid(c Category, id string) bool {
	if id == "" {
		return true
	}
	_, ok := optIndex[c][id]
	return ok
}

func rank(c Category, id string) int {
	if j, ok := optIndex[c][id]; ok {
		return j
	}
	return 1 << 20
}

// Attributes is a designed voice. An empty field is Auto: the model
// decides.
type Attributes struct {
	Gender  string   `json:"gender,omitempty"`
	Age     string   `json:"age,omitempty"`
	Pitch   string   `json:"pitch,omitempty"`
	Pace    string   `json:"pace,omitempty"`
	Styles  []string `json:"styles,omitempty"` // canonical order (Canonical)
	Emotion string   `json:"emotion,omitempty"`
	Accent  string   `json:"accent,omitempty"`
	Dialect string   `json:"dialect,omitempty"`
}

// IsAuto reports whether every category is Auto.
func (a Attributes) IsAuto() bool {
	return a.Gender == "" && a.Age == "" && a.Pitch == "" && a.Pace == "" &&
		len(a.Styles) == 0 && a.Emotion == "" && a.Accent == "" && a.Dialect == ""
}

// Get returns the values of a category (none = Auto).
func (a Attributes) Get(c Category) []string {
	if c == Style {
		return slices.Clone(a.Styles)
	}
	if p := a.field(c); p != nil && *p != "" {
		return []string{*p}
	}
	return nil
}

// Value returns the single value of a non-Style category ("" = Auto).
func (a Attributes) Value(c Category) string {
	if p := a.field(c); p != nil {
		return *p
	}
	return ""
}

// HasStyle reports whether the voice has a style value.
func (a Attributes) HasStyle(id string) bool { return slices.Contains(a.Styles, id) }

func (a *Attributes) field(c Category) *string {
	switch c {
	case Gender:
		return &a.Gender
	case Age:
		return &a.Age
	case Pitch:
		return &a.Pitch
	case Pace:
		return &a.Pace
	case Emotion:
		return &a.Emotion
	case Accent:
		return &a.Accent
	case Dialect:
		return &a.Dialect
	}
	return nil
}

// Canonical returns a copy with Styles deduplicated, unknown values
// dropped and Styles sorted in table order. It does not resolve conflicts.
func (a Attributes) Canonical() Attributes {
	out := a
	for _, c := range Categories {
		if p := out.field(c); p != nil && !Valid(c, *p) {
			*p = ""
		}
	}
	var st []string
	for _, s := range a.Styles {
		if Valid(Style, s) && s != "" && !slices.Contains(st, s) {
			st = append(st, s)
		}
	}
	slices.SortStableFunc(st, func(x, y string) int { return rank(Style, x) - rank(Style, y) })
	out.Styles = st
	return out
}

// With returns a copy where category c is set to value ("" = Auto; for
// Style, value is added — use Without to remove one). Values that can no
// longer coexist are cleared and reported, e.g. picking a dialect clears
// the English accent, and picking "calm" clears "energetic".
func (a Attributes) With(c Category, value string) (Attributes, []Cleared, error) {
	if !Valid(c, value) {
		return a, nil, fmt.Errorf("voicedesign: %q is not a %s", value, c)
	}
	out := a.Canonical()
	var cleared []Cleared
	switch c {
	case Style:
		if value == "" {
			out.Styles = nil
			return out, nil, nil
		}
		if !out.HasStyle(value) {
			out.Styles = append(out.Styles, value)
		}
	default:
		*out.field(c) = value
	}
	if value == "" {
		return out, nil, nil
	}
	// Exclusive categories and hard conflicts: keep the new value, clear
	// whatever contradicts it.
	if c == Accent && out.Dialect != "" {
		cleared = append(cleared, Cleared{Dialect, out.Dialect})
		out.Dialect = ""
	}
	if c == Dialect && out.Accent != "" {
		cleared = append(cleared, Cleared{Accent, out.Accent})
		out.Accent = ""
	}
	for _, k := range conflicts {
		var other Value
		switch {
		case k.a.Category == c && k.a.ID == value:
			other = k.b
		case k.b.Category == c && k.b.ID == value:
			other = k.a
		default:
			continue
		}
		if out.has(other) {
			out = out.drop(other)
			cleared = append(cleared, Cleared(other))
		}
	}
	if c == Style && len(out.Styles) > MaxStyles {
		// Drop the oldest picks beyond the limit (a.Styles keeps pick order
		// only until Canonical; this drops the first in table order that
		// isn't the new value).
		for len(out.Styles) > MaxStyles {
			i := 0
			if out.Styles[0] == value {
				i = 1
			}
			cleared = append(cleared, Cleared{Style, out.Styles[i]})
			out.Styles = slices.Delete(out.Styles, i, i+1)
		}
	}
	out = out.Canonical()
	return out, cleared, nil
}

// Without returns a copy with one value removed (for single-valued
// categories, the category goes back to Auto when it holds value; value ""
// clears the whole category).
func (a Attributes) Without(c Category, value string) Attributes {
	out := a.Canonical()
	if c == Style {
		if value == "" {
			out.Styles = nil
		} else {
			out.Styles = slices.DeleteFunc(out.Styles, func(s string) bool { return s == value })
		}
		return out
	}
	if p := out.field(c); p != nil && (value == "" || *p == value) {
		*p = ""
	}
	return out
}

// Value names one value of one category.
type Value struct {
	Category Category `json:"category"`
	ID       string   `json:"id"`
}

// Cleared is a value that With removed because it contradicts the pick.
type Cleared Value

func (a Attributes) has(v Value) bool {
	if v.Category == Style {
		return a.HasStyle(v.ID)
	}
	return a.Value(v.Category) == v.ID
}

func (a Attributes) drop(v Value) Attributes { return a.Without(v.Category, v.ID) }

type conflict struct{ a, b Value }

func cv(c Category, id string) Value { return Value{c, id} }

// conflicts are pairs that contradict each other outright. They are kept
// small on purpose: unusual-but-possible combinations (an angry whisper, a
// high-pitched man) are the user's call.
var conflicts = []conflict{
	{cv(Style, "whisper"), cv(Style, "energetic")},
	{cv(Style, "whisper"), cv(Style, "resonant")},
	{cv(Style, "whisper"), cv(Pace, "very_fast")},
	{cv(Style, "calm"), cv(Style, "energetic")},
	{cv(Style, "calm"), cv(Emotion, "angry")},
	{cv(Style, "calm"), cv(Emotion, "excited")},
	{cv(Style, "calm"), cv(Emotion, "fearful")},
	{cv(Style, "smooth"), cv(Style, "raspy")},
	{cv(Style, "soft"), cv(Style, "authoritative")},
	{cv(Style, "energetic"), cv(Emotion, "sad")},
	{cv(Style, "playful"), cv(Emotion, "serious")},
	{cv(Age, "child"), cv(Pitch, "very_low")},
	{cv(Age, "child"), cv(Style, "raspy")},
	{cv(Age, "child"), cv(Style, "magnetic")},
	{cv(Age, "child"), cv(Style, "authoritative")},
	{cv(Pace, "very_slow"), cv(Style, "energetic")},
	{cv(Pace, "very_fast"), cv(Style, "calm")},
}

// Conflict is a pair of values in one Attributes that contradict each
// other, or a broken invariant (accent with dialect, too many styles,
// unknown value).
type Conflict struct {
	A, B   Value
	Reason string
}

// Conflicts lists what is contradictory in a. A canonical voice from With
// never has any; parsed or imported attributes may.
func (a Attributes) Conflicts() []Conflict {
	var out []Conflict
	for _, c := range Categories {
		for _, v := range a.Get(c) {
			if !Valid(c, v) {
				out = append(out, Conflict{A: cv(c, v), Reason: "unknown value"})
			}
		}
	}
	if a.Accent != "" && a.Dialect != "" {
		out = append(out, Conflict{cv(Accent, a.Accent), cv(Dialect, a.Dialect), "an English accent and a Chinese dialect can't be combined"})
	}
	if len(a.Styles) > MaxStyles {
		out = append(out, Conflict{A: cv(Style, a.Styles[MaxStyles]), Reason: fmt.Sprintf("more than %d styles", MaxStyles)})
	}
	for _, k := range conflicts {
		if a.has(k.a) && a.has(k.b) {
			out = append(out, Conflict{k.a, k.b, "contradictory"})
		}
	}
	return out
}

// Validate returns an error describing the first conflict, if any.
func (a Attributes) Validate() error {
	if cs := a.Conflicts(); len(cs) > 0 {
		c := cs[0]
		if c.B.Category == "" {
			return fmt.Errorf("voicedesign: %s %q: %s", c.A.Category, c.A.ID, c.Reason)
		}
		return fmt.Errorf("voicedesign: %s %q and %s %q: %s", c.A.Category, c.A.ID, c.B.Category, c.B.ID, c.Reason)
	}
	return nil
}

// Summary is the one-line recipe shown in the UI: "Female · Middle-aged ·
// Low · Warm · British", or "Auto — the model decides".
func (a Attributes) Summary(locale string) string {
	a = a.Canonical()
	var parts []string
	for _, c := range Categories {
		for _, v := range a.Get(c) {
			if op, ok := Lookup(c, v); ok {
				parts = append(parts, op.Label.In(locale))
			}
		}
	}
	if len(parts) == 0 {
		return Text{En: "Auto — the model decides", Zh: "自动——由模型决定", Ja: "自動：モデルにおまかせ"}.In(locale)
	}
	return strings.Join(parts, " · ")
}

// Language is the speech language the attributes imply: "zh" with a
// dialect, "en" with an English accent, else "".
func (a Attributes) Language() string {
	switch {
	case a.Dialect != "":
		return "zh"
	case a.Accent != "":
		return "en"
	}
	return ""
}

// Diff lists the values set in a that b doesn't have (b's view of the same
// category differs or is Auto). Used to find the explicit picks layered on
// top of a description.
func (a Attributes) Diff(b Attributes) []Value {
	var out []Value
	for _, c := range Categories {
		for _, v := range a.Get(c) {
			if !b.has(cv(c, v)) {
				out = append(out, cv(c, v))
			}
		}
	}
	return out
}
