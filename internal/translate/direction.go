package translate

import (
	"context"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/hehehai/AudioSmith/internal/llm"
)

// Direction is a parsed performance note.
type Direction struct {
	Emotions []string `json:"emotions"` // Taxonomy tags, strongest first (≤ 3)
	// Instruct is an English delivery instruction for instruct-capable TTS
	// (Qwen3-TTS instruct, voice design prompts).
	Instruct string  `json:"instruct"`
	Rate     float64 `json:"rate"`   // speed bias, -0.5…0.5: speed × (1 + Rate)
	Pitch    float64 `json:"pitch"`  // semitones, -4…4
	Energy   float64 `json:"energy"` // loudness / intensity, -1 (whisper)…1 (shout)
	// TranslateHint is a short register note for translation, "" if none.
	TranslateHint string `json:"translateHint,omitempty"`
	Source        string `json:"source"`             // "llm" | "keywords"
	Fallback      string `json:"fallback,omitempty"` // why the keyword parser was used (llm/translate code)
}

// Taxonomy is the fixed set of emotion / delivery tags.
var Taxonomy = []string{
	"neutral", "happy", "excited", "sad", "angry", "fearful", "nervous", "surprised", "disgusted",
	"calm", "tender", "serious", "playful", "sarcastic", "confident", "tired", "cold", "urgent",
	"whisper", "shout",
}

// DefaultDirectionBudget bounds a direction reading. Qwen3 4B takes
// 4.6–5.6 s for its ~1k-token prompt on a busy Mac (thread #44 §4.4), too
// close to the 6 s it had.
const DefaultDirectionBudget = 10 * time.Second

// Director parses directions, with the direction_parse skill when routed
// and the keyword parser otherwise (or when the model fails).
type Director struct {
	Router *llm.Router   // nil: keywords only
	Budget time.Duration // 0: DefaultDirectionBudget
}

var directionSchema = func() *llm.Schema {
	s := llm.Object(map[string]*llm.Schema{
		// Not an enum: a near miss ("Fearful") is cleaned up rather than failing the call.
		"emotions":      llm.Array(llm.String()).Count(0, 5),
		"instruct":      llm.String(),
		"rate":          llm.Number().Range(-0.5, 0.5),
		"pitch":         llm.Number().Range(-4, 4),
		"energy":        llm.Number().Range(-1, 1),
		"translateHint": llm.String(),
	}, "emotions", "instruct", "rate", "pitch", "energy")
	s.Name = "direction"
	return s
}()

type directionData struct {
	Text     string
	Taxonomy []string
	Zh       bool
}

// Parse turns a natural-language note into a Direction. It never fails.
func (d *Director) Parse(ctx context.Context, note string) Direction {
	note = strings.TrimSpace(note)
	if note == "" {
		return Direction{Source: "keywords"}
	}
	if !d.Router.Available(llm.SkillDirectionParse) {
		k := ParseDirection(note)
		k.Fallback = FallbackNoLLM
		return k
	}
	budget := d.Budget
	if budget <= 0 {
		budget = DefaultDirectionBudget
	}
	bctx, cancel := llm.WithBudget(ctx, budget) // from when the model's turn comes (the gpu lane may be busy)
	defer cancel()
	msgs, err := messages("direction", directionData{Text: clipRunes(note, 1000), Taxonomy: Taxonomy, Zh: cjkShare(note) > 0})
	if err != nil {
		k := ParseDirection(note)
		k.Fallback = FallbackError
		return k
	}
	var out Direction
	_, err = d.Router.JSON(bctx, llm.SkillDirectionParse, llm.Request{Messages: msgs, Schema: directionSchema, Timeout: budget}, &out, 1)
	if err != nil {
		k := ParseDirection(note)
		k.Fallback = Code(err)
		if k.Fallback == "" {
			k.Fallback = FallbackError
		}
		return k
	}
	out.Source, out.Fallback = "llm", ""
	out.Emotions = cleanEmotions(out.Emotions)
	out.Rate = clamp(out.Rate, -0.5, 0.5)
	out.Pitch = clamp(out.Pitch, -4, 4)
	out.Energy = clamp(out.Energy, -1, 1)
	out.Instruct = strings.TrimSpace(out.Instruct)
	out.TranslateHint = strings.TrimSpace(out.TranslateHint)
	return out
}

func cleanEmotions(in []string) []string {
	var out []string
	for _, e := range in {
		e = strings.ToLower(strings.TrimSpace(e))
		if slices.Contains(Taxonomy, e) && !slices.Contains(out, e) && len(out) < 3 {
			out = append(out, e)
		}
	}
	if len(out) > 1 {
		out = slices.DeleteFunc(out, func(e string) bool { return e == "neutral" })
	}
	return out
}

func clamp(x, lo, hi float64) float64 {
	if math.IsNaN(x) {
		return 0
	}
	return math.Max(lo, math.Min(hi, x))
}

// cue is one keyword's effect.
type cue struct {
	emotion             string  // Taxonomy tag, or "" for pure rate/pitch/energy cues
	phrase              string  // English adjective / phrase for Instruct
	rate, pitch, energy float64 // at weight 1
}

// Keyword tables: English (word-bounded, lower case) and Chinese (substrings).
var (
	cueEmotions = map[string]cue{
		"urgent":    {"urgent", "urgent", 0.2, 1, 0.3},
		"surprised": {"surprised", "surprised", 0, 2, 0.2},
		"happy":     {"happy", "cheerful", 0.05, 1, 0.2},
		"excited":   {"excited", "excited", 0.15, 2, 0.4},
		"sad":       {"sad", "sad", -0.15, -1, -0.3},
		"angry":     {"angry", "angry", 0.1, 0.5, 0.5},
		"fearful":   {"fearful", "frightened", 0.1, 1, -0.1},
		"nervous":   {"nervous", "nervous", 0.05, 0.5, -0.1},
		"calm":      {"calm", "calm", -0.1, -0.5, -0.2},
		"tender":    {"tender", "tender", -0.1, 0, -0.2},
		"serious":   {"serious", "serious", -0.05, -1, 0},
		"playful":   {"playful", "playful", 0.05, 1, 0.1},
		"sarcastic": {"sarcastic", "sarcastic", 0, 0, 0},
		"confident": {"confident", "confident", 0, -0.5, 0.2},
		"tired":     {"tired", "tired", -0.2, -1, -0.3},
		"cold":      {"cold", "cold, detached", -0.05, -0.5, -0.2},
		"disgusted": {"disgusted", "disgusted", 0, -0.5, 0.1},
		"whisper":   {"whisper", "whispering", -0.05, 0, -0.8},
		"shout":     {"shout", "shouting", 0.05, 1, 0.8},
	}
	cueFaster  = cue{"", "faster", 0.2, 0, 0}
	cueSlower  = cue{"", "slower", -0.2, 0, 0}
	cueHigher  = cue{"", "higher-pitched", 0, 2, 0}
	cueLower   = cue{"", "lower-pitched", 0, -2, 0}
	cueLouder  = cue{"", "louder", 0, 0, 0.4}
	cueQuieter = cue{"", "quieter", 0, 0, -0.4}

	enCues = map[string]cue{}
	zhCues = map[string]cue{}
)

func init() {
	add := func(m map[string]cue, c cue, words ...string) {
		for _, w := range words {
			m[w] = c
		}
	}
	e := cueEmotions
	add(enCues, e["urgent"], "urgent", "urgently", "urgency", "hurried", "rushed", "in a hurry", "pressing", "frantic", "panicked", "panicky")
	add(enCues, e["surprised"], "surprised", "surprise", "shocked", "astonished", "amazed", "startled", "stunned", "incredulous")
	add(enCues, e["happy"], "happy", "cheerful", "joyful", "joyous", "upbeat", "bright", "delighted", "glad", "smiling")
	add(enCues, e["excited"], "excited", "enthusiastic", "energetic", "thrilled", "eager", "hyped")
	add(enCues, e["sad"], "sad", "sadly", "sorrowful", "melancholy", "melancholic", "gloomy", "mournful", "tearful", "crying", "heartbroken", "grieving")
	add(enCues, e["angry"], "angry", "angrily", "furious", "mad", "annoyed", "irritated", "enraged", "outraged", "irate", "frustrated")
	add(enCues, e["fearful"], "scared", "afraid", "fearful", "terrified", "frightened", "fearfully")
	add(enCues, e["nervous"], "nervous", "nervously", "anxious", "hesitant", "unsure", "shaky", "uneasy", "worried")
	add(enCues, e["calm"], "calm", "calmly", "relaxed", "soothing", "peaceful", "steady", "composed", "serene", "laid-back", "laid back")
	add(enCues, e["tender"], "tender", "tenderly", "loving", "lovingly", "affectionate", "warm", "warmly", "gentle", "gently", "kindly", "sweet")
	add(enCues, e["serious"], "serious", "seriously", "grave", "stern", "solemn", "sombre", "somber", "formal")
	add(enCues, e["playful"], "playful", "playfully", "teasing", "mischievous", "cheeky", "silly", "fun", "lighthearted", "light-hearted")
	add(enCues, e["sarcastic"], "sarcastic", "sarcastically", "ironic", "ironically", "mocking", "dry", "deadpan", "snarky")
	add(enCues, e["confident"], "confident", "confidently", "assertive", "bold", "commanding", "authoritative", "firm", "firmly", "decisive")
	add(enCues, e["tired"], "tired", "sleepy", "exhausted", "weary", "drowsy", "bored", "lethargic")
	add(enCues, e["cold"], "cold", "coldly", "icy", "detached", "flat", "emotionless", "monotone", "aloof")
	add(enCues, e["disgusted"], "disgusted", "disgust", "repulsed", "grossed out", "revolted")
	add(enCues, e["whisper"], "whisper", "whispered", "whispering", "hushed", "under your breath", "under his breath", "under her breath")
	add(enCues, e["shout"], "shout", "shouting", "shouted", "yell", "yelling", "scream", "screaming", "bellowing")
	add(enCues, cueFaster, "faster", "quicker", "quick", "quickly", "fast", "rapid", "rapidly", "speed up", "pick up the pace", "brisk")
	add(enCues, cueSlower, "slower", "slow", "slowly", "unhurried", "take your time", "drawn out", "drawn-out", "leisurely", "deliberate", "deliberately")
	add(enCues, cueHigher, "higher", "high-pitched", "higher pitch", "raise the pitch")
	add(enCues, cueLower, "lower", "deeper", "deep", "low-pitched", "lower pitch")
	add(enCues, cueLouder, "louder", "loud", "projected", "project", "more volume", "more energy", "energetically", "punchy", "intense", "intensely")
	add(enCues, cueQuieter, "quieter", "quiet", "quietly", "softer", "soft", "softly", "low-key", "subdued", "muted")

	add(zhCues, e["urgent"], "紧急", "紧迫", "着急", "急促", "急切", "焦急", "赶时间", "慌张", "急")
	add(zhCues, e["surprised"], "惊讶", "吃惊", "震惊", "意外", "惊奇", "难以置信", "诧异")
	add(zhCues, e["happy"], "开心", "高兴", "愉快", "欢快", "快乐", "喜悦", "明快", "轻快")
	add(zhCues, e["excited"], "兴奋", "激动", "热情", "亢奋", "雀跃")
	add(zhCues, e["sad"], "伤心", "难过", "悲伤", "沮丧", "哀伤", "低落", "忧伤", "哭腔", "哽咽")
	add(zhCues, e["angry"], "生气", "愤怒", "恼火", "气愤", "暴怒", "不耐烦", "烦躁", "火大")
	add(zhCues, e["fearful"], "害怕", "恐惧", "惊恐", "惊慌", "胆怯")
	add(zhCues, e["nervous"], "紧张", "犹豫", "不安", "忐忑", "担心", "迟疑")
	add(zhCues, e["calm"], "平静", "冷静", "放松", "舒缓", "沉稳", "淡定", "从容")
	add(zhCues, e["tender"], "温柔", "深情", "亲切", "温暖", "柔和", "温和")
	add(zhCues, e["serious"], "严肃", "严厉", "庄重", "郑重", "正式", "认真")
	add(zhCues, e["playful"], "俏皮", "调皮", "开玩笑", "逗趣", "活泼", "撒娇")
	add(zhCues, e["sarcastic"], "讽刺", "挖苦", "嘲讽", "阴阳怪气", "反讽", "揶揄")
	add(zhCues, e["confident"], "自信", "坚定", "果断", "有力", "霸气", "笃定")
	add(zhCues, e["tired"], "疲惫", "疲倦", "累", "困倦", "无聊", "慵懒", "有气无力")
	add(zhCues, e["cold"], "冷淡", "冷漠", "冰冷", "平淡", "面无表情", "毫无感情")
	add(zhCues, e["disgusted"], "厌恶", "恶心", "嫌弃", "反感")
	add(zhCues, e["whisper"], "低声", "耳语", "悄悄", "悄声", "轻声细语")
	add(zhCues, e["shout"], "大喊", "喊叫", "吼", "尖叫", "咆哮", "呐喊")
	add(zhCues, cueFaster, "快一点", "快点", "加快", "语速快", "快速", "更快", "快些", "快")
	add(zhCues, cueSlower, "慢一点", "慢点", "放慢", "语速慢", "缓慢", "更慢", "慢些", "慢")
	add(zhCues, cueHigher, "音调高", "高一点", "提高音调", "尖细", "高亢")
	add(zhCues, cueLower, "低沉", "深沉", "音调低", "低一点", "浑厚")
	add(zhCues, cueLouder, "大声", "响亮", "洪亮", "有力量", "大声一点")
	add(zhCues, cueQuieter, "小声", "轻一点", "轻声", "柔声", "小声一点")
}

var (
	enWeak   = []string{"a bit", "a little", "a little bit", "slightly", "somewhat", "a touch", "kind of", "kinda", "a tad", "mildly", "faintly", "a hint of", "hint of"}
	enStrong = []string{"very", "really", "extremely", "super", "so", "totally", "incredibly", "way", "much", "much more", "highly", "deeply", "utterly", "truly"}
	enNeg    = []string{"less", "not", "not so", "not too", "not as", "don't be", "no", "never", "without", "stop being", "less of"}
	zhWeak   = []string{"有点", "有些", "稍微", "稍稍", "略微", "略", "一点", "一些", "些许", "微微"}
	zhStrong = []string{"非常", "很", "特别", "超级", "超", "极其", "极", "十分", "太", "相当", "格外", "无比"}
	zhNeg    = []string{"不要", "别", "不", "没那么", "少点", "少一点", "不再", "减少"}
	zhMore   = []string{"更", "再"}
)

var wordRE = regexp.MustCompile(`[\p{L}\p{N}'’-]+`)

type hit struct {
	c      cue
	weight float64
	pos    int
}

// ParseDirection is the deterministic keyword parser (English and Chinese).
func ParseDirection(note string) Direction {
	low := strings.ToLower(note)
	var hits []hit
	hits = append(hits, enHits(low)...)
	hits = append(hits, zhHits(low)...)
	d := Direction{Source: "keywords"}
	if len(hits) == 0 {
		d.Instruct = strings.TrimSpace(note) // free text still helps instruct-capable engines
		return d
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].pos < hits[j].pos })
	score := map[string]float64{}
	phraseW := map[string]float64{}
	var phrases []string
	for _, h := range hits {
		d.Rate += h.c.rate * h.weight
		d.Pitch += h.c.pitch * h.weight
		d.Energy += h.c.energy * h.weight
		if h.weight > 0 {
			if h.c.emotion != "" {
				score[h.c.emotion] += h.weight
			}
			if _, ok := phraseW[h.c.phrase]; !ok {
				phrases = append(phrases, h.c.phrase)
			}
			phraseW[h.c.phrase] = math.Max(phraseW[h.c.phrase], h.weight)
		} else {
			neg := "less " + h.c.phrase
			if h.c.emotion == "" {
				neg = opposite(h.c.phrase)
			}
			if _, ok := phraseW[neg]; !ok {
				phrases = append(phrases, neg)
				phraseW[neg] = 1
			}
		}
	}
	d.Rate = round2(clamp(d.Rate, -0.5, 0.5))
	d.Pitch = round2(clamp(d.Pitch, -4, 4))
	d.Energy = round2(clamp(d.Energy, -1, 1))
	for e := range score {
		d.Emotions = append(d.Emotions, e)
	}
	sort.SliceStable(d.Emotions, func(i, j int) bool {
		a, b := d.Emotions[i], d.Emotions[j]
		if score[a] != score[b] {
			return score[a] > score[b]
		}
		return slices.Index(Taxonomy, a) < slices.Index(Taxonomy, b)
	})
	if len(d.Emotions) > 3 {
		d.Emotions = d.Emotions[:3]
	}
	d.Instruct = instruct(phrases, phraseW)
	d.TranslateHint = translateHint(d.Emotions)
	return d
}

func opposite(phrase string) string {
	switch phrase {
	case "faster":
		return "not rushed"
	case "slower":
		return "not dragging"
	case "louder":
		return "not too loud"
	case "quieter":
		return "not too quiet"
	case "higher-pitched":
		return "not too high"
	case "lower-pitched":
		return "not too low"
	}
	return "less " + phrase
}

func instruct(phrases []string, w map[string]float64) string {
	var parts []string
	for _, p := range phrases {
		switch {
		case w[p] < 0.75:
			p = "slightly " + p
		case w[p] > 1.25:
			p = "very " + p
		}
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		return ""
	}
	list := parts[0]
	if len(parts) > 1 {
		list = strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}
	return "Deliver the line " + list + "."
}

func translateHint(emotions []string) string {
	hints := map[string]string{
		"urgent": "short, clipped sentences", "angry": "blunt, forceful wording", "tender": "warm, gentle wording",
		"serious": "formal, measured wording", "playful": "casual, light wording", "sarcastic": "keep the irony",
		"tired": "short, low-energy phrasing", "cold": "curt, distant wording", "excited": "lively, exclamatory wording",
		"whisper": "short, intimate phrasing", "nervous": "hesitant phrasing",
	}
	for _, e := range emotions {
		if h, ok := hints[e]; ok {
			return h
		}
	}
	return ""
}

// enHits finds English cues on word boundaries, longest first, and weighs
// them by the modifiers just before them.
func enHits(low string) []hit {
	return cueHits(low, enCues, func(s, e int, _ string) (float64, bool) {
		if !wordEdge(low, s, e) {
			return 0, false
		}
		return enWeight(low[:s]), true
	})
}

// cueHits finds the cues of cues in low, longest first, each span used
// once; weigh gives a hit's weight from its span and keyword, or false to
// skip the match.
func cueHits(low string, cues map[string]cue, weigh func(s, e int, k string) (float64, bool)) []hit {
	type kw struct {
		k string
		c cue
	}
	var kws []kw
	for k, c := range cues {
		kws = append(kws, kw{k, c})
	}
	sort.Slice(kws, func(i, j int) bool {
		if len(kws[i].k) != len(kws[j].k) {
			return len(kws[i].k) > len(kws[j].k)
		}
		return kws[i].k < kws[j].k
	})
	used := make([]bool, len(low))
	var out []hit
	for _, x := range kws {
		for off := 0; ; {
			i := strings.Index(low[off:], x.k)
			if i < 0 {
				break
			}
			s, e := off+i, off+i+len(x.k)
			off = e
			if anyUsed(used, s, e) {
				continue
			}
			w, ok := weigh(s, e, x.k)
			if !ok {
				continue
			}
			for k := s; k < e; k++ {
				used[k] = true
			}
			out = append(out, hit{c: x.c, weight: w, pos: s})
		}
	}
	return out
}

func wordEdge(s string, start, end int) bool {
	if start > 0 {
		r := rune(s[start-1])
		if s[start-1] < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			return false
		}
	}
	if end < len(s) {
		r := rune(s[end])
		if s[end] < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			return false
		}
	}
	return true
}

func anyUsed(u []bool, s, e int) bool {
	for k := s; k < e; k++ {
		if u[k] {
			return true
		}
	}
	return false
}

// enWeight reads the last few words before a cue in the same clause.
func enWeight(before string) float64 {
	if i := strings.LastIndexAny(before, ",;.!?\n"); i >= 0 {
		before = before[i+1:]
	}
	words := wordRE.FindAllString(before, -1)
	if len(words) > 3 {
		words = words[len(words)-3:]
	}
	tail := " " + strings.Join(words, " ") + " "
	has := func(list []string) bool {
		for _, m := range list {
			if strings.Contains(tail, " "+m+" ") {
				return true
			}
		}
		return false
	}
	w := 1.0
	switch {
	case has(enWeak):
		w = 0.5
	case has(enStrong):
		w = 1.5
	}
	if has(enNeg) {
		w = -w
	}
	return w
}

// zhHits finds Chinese cues (substrings), longest first, weighing them by
// the modifiers right before (and, for 一点/些, right after) them.
func zhHits(low string) []hit {
	return cueHits(low, zhCues, func(s, e int, k string) (float64, bool) {
		w := zhWeight(low[:s], low[e:])
		if strings.HasSuffix(k, "点") || strings.HasSuffix(k, "些") {
			w *= 0.5 // 快一点, 慢些: "a bit"
		}
		return w, true
	})
}

func zhWeight(before, after string) float64 {
	rb := []rune(before)
	if i := strings.LastIndexAny(before, "，。,.;；!！?？"); i >= 0 {
		rb = []rune(before[i:])
	}
	if len(rb) > 4 {
		rb = rb[len(rb)-4:]
	}
	pre := string(rb)
	ra := []rune(after)
	if len(ra) > 3 {
		ra = ra[:3]
	}
	post := string(ra)
	ends := func(list []string) bool {
		for _, m := range list {
			if strings.HasSuffix(pre, m) || strings.HasSuffix(strings.TrimRight(pre, "得地的一些点"), m) {
				return true
			}
		}
		return false
	}
	w := 1.0
	switch {
	case ends(zhWeak) || strings.HasPrefix(post, "一点") || strings.HasPrefix(post, "一些") || strings.HasPrefix(post, "些") || strings.HasPrefix(post, "点"):
		w = 0.5
	case ends(zhStrong):
		w = 1.5
	case ends(zhMore):
		w = 1
	}
	for _, m := range zhNeg {
		if strings.Contains(pre, m) {
			w = -w
			break
		}
	}
	return w
}
