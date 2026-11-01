package services

// Emotion direction of stories, v2 (plan/story-emotion-context-plan.md
// §3 A and E): the AI director reads the whole scene and gives each line
// an emotion from a small fixed vocabulary with an intensity 0–3, marks
// the turns (转折) where a feeling may change at once, and gives each
// character a baseline; rules then keep a character's feeling continuous
// (SmoothEmotions). A line's instruction is a short phrase made from its
// emotion (EmotionInstruct), the same wording for every line of a
// character, without persona or scene: long instructions make voices
// drift, and a model that guesses each line's feeling from its words
// alone jumps between lines. The gaps between lines follow the emotion
// too (EmotionGapFactor): excited lines come quicker, hesitant and sad
// ones after a longer pause.
//
// A line without an emotion is directed as before (StoryInstruct), so
// stories directed by the first director render as they did.

import (
	"math"
	"slices"
	"strings"
	"unicode/utf8"
)

// Emotions (StoryLine.Emotion). EmotionSteady is the character's baseline
// going on: most lines of a scene.
const (
	EmotionSteady    = "steady"
	EmotionHappy     = "happy"
	EmotionExcited   = "excited"
	EmotionTender    = "tender"
	EmotionPleading  = "pleading"
	EmotionHesitant  = "hesitant"
	EmotionWorried   = "worried"
	EmotionHurt      = "hurt"
	EmotionSad       = "sad"
	EmotionAngry     = "angry"
	EmotionScared    = "scared"
	EmotionSurprised = "surprised"
	// EmotionApologetic: sorry, guilty, delivering bad news (added after
	// round 1: without it the director called a father announcing a
	// trip "very scared").
	EmotionApologetic = "apologetic"
)

// emotionInfo is how an emotion is said: its word (Chinese, English),
// a manner added at the strongest intensity, its valence (+1 pleasant,
// −1 unpleasant, 0 neither) and how much it stretches (>1) or shortens
// (<1) the pause before and after it, at full intensity.
type emotionInfo struct {
	zh, en             string
	zhStrong, enStrong string
	valence            int
	gap                float64
}

var emotions = map[string]emotionInfo{
	EmotionSteady:     {"平稳", "even", "", "", 0, 1},
	EmotionHappy:      {"开心", "happy", "带着笑", "laughing", 1, 0.9},
	EmotionExcited:    {"兴奋", "excited", "语速快", "fast", 1, 0.7},
	EmotionTender:     {"温柔", "tender", "轻声", "softly", 1, 1.15},
	EmotionPleading:   {"央求", "pleading", "急切", "urgent", 0, 0.9},
	EmotionHesitant:   {"犹豫", "hesitant", "支支吾吾", "faltering", 0, 1.45},
	EmotionWorried:    {"担心", "worried", "焦急", "anxious", -1, 1.15},
	EmotionHurt:       {"委屈", "hurt", "带哭腔", "tearful", -1, 1.2},
	EmotionSad:        {"难过", "sad", "带哭腔", "tearful", -1, 1.35},
	EmotionAngry:      {"生气", "angry", "大声", "loud", -1, 0.75},
	EmotionScared:     {"害怕", "scared", "声音发抖", "trembling", -1, 0.85},
	EmotionSurprised:  {"惊讶", "surprised", "声调上扬", "rising", 0, 0.75},
	EmotionApologetic: {"愧疚", "apologetic", "声音低沉", "low-voiced", -1, 1.2},
}

// Emotions lists the vocabulary in the order the UI and the director
// show it.
var Emotions = []string{EmotionSteady, EmotionHappy, EmotionExcited, EmotionTender, EmotionPleading, EmotionHesitant,
	EmotionWorried, EmotionHurt, EmotionSad, EmotionAngry, EmotionScared, EmotionSurprised, EmotionApologetic}

// emotionAliases are words a language model (or a user) may write for an
// emotion of the vocabulary.
var emotionAliases = map[string]string{
	"calm": EmotionSteady, "neutral": EmotionSteady, "平静": EmotionSteady, "平稳延续": EmotionSteady, "none": EmotionSteady,
	"joyful": EmotionHappy, "glad": EmotionHappy, "高兴": EmotionHappy, "欣喜": EmotionHappy,
	"eager": EmotionExcited, "激动": EmotionExcited, "雀跃": EmotionExcited,
	"gentle": EmotionTender, "warm": EmotionTender, "soft": EmotionTender, "fond": EmotionTender, "宠溺": EmotionTender,
	"coaxing": EmotionPleading, "begging": EmotionPleading, "撒娇": EmotionPleading, "恳求": EmotionPleading,
	"unsure": EmotionHesitant, "reluctant": EmotionHesitant, "迟疑": EmotionHesitant, "无奈": EmotionHesitant,
	"anxious": EmotionWorried, "concerned": EmotionWorried, "担忧": EmotionWorried, "焦虑": EmotionWorried,
	"wronged": EmotionHurt, "aggrieved": EmotionHurt, "sulky": EmotionHurt,
	"sorrowful": EmotionSad, "伤心": EmotionSad, "悲伤": EmotionSad, "失落": EmotionSad,
	"mad": EmotionAngry, "annoyed": EmotionAngry, "愤怒": EmotionAngry, "恼火": EmotionAngry,
	"afraid": EmotionScared, "fearful": EmotionScared, "frightened": EmotionScared, "恐惧": EmotionScared,
	"sorry": EmotionApologetic, "guilty": EmotionApologetic, "regretful": EmotionApologetic, "抱歉": EmotionApologetic, "内疚": EmotionApologetic,
	"歉意": EmotionApologetic, "shocked": EmotionSurprised, "amazed": EmotionSurprised, "吃惊": EmotionSurprised, "震惊": EmotionSurprised,
}

// NormEmotion is the vocabulary's key for an emotion a model or user
// wrote (a key, its Chinese word or a common synonym), "" for none.
func NormEmotion(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	if _, ok := emotions[s]; ok {
		return s
	}
	if k, ok := emotionAliases[s]; ok {
		return k
	}
	for _, k := range Emotions {
		if strings.Contains(s, emotions[k].zh) {
			return k
		}
	}
	return ""
}

// EmotionWord is an emotion's word in Chinese (zh) or English.
func EmotionWord(emotion string, zh bool) string {
	e, ok := emotions[NormEmotion(emotion)]
	if !ok {
		return ""
	}
	if zh {
		return e.zh
	}
	return e.en
}

// instructEmotionMax caps an emotion instruction (runes; Latin text:
// characters × instructLatin): the plan's 8–10 Chinese characters.
const instructEmotionMax = 10

// EmotionInstruct is the instruction of a line directed with an emotion
// at an intensity (0–3): "略带委屈", "非常委屈，带哭腔", "语气平稳". own,
// when not "", is the user's own wording, which takes the emotion's
// place. cont adds "carry on from your last line" when it fits.
func EmotionInstruct(emotion string, intensity int, own string, cont, zh bool) string {
	e, ok := emotions[NormEmotion(emotion)]
	if !ok && strings.TrimSpace(own) == "" {
		return ""
	}
	limit := instructEmotionMax
	if !zh {
		limit *= instructLatin
	}
	if turnLines(own) {
		limit *= 2 // a merged turn's lines, in order
	}
	var s string
	switch {
	case strings.TrimSpace(own) != "":
		s = strings.TrimRight(strings.Join(strings.Fields(own), " "), "。.，,；;！!")
		if utf8.RuneCountInString(s) > limit {
			s = string([]rune(s)[:limit])
		}
	case NormEmotion(emotion) == EmotionSteady && zh:
		s = "语气平稳"
	case NormEmotion(emotion) == EmotionSteady:
		s = "calm, even"
	case zh:
		switch min(max(intensity, 0), 3) {
		case 0:
			s = "平静，带点" + e.zh
		case 1:
			s = "略带" + e.zh
		case 2:
			s = e.zh
		default:
			s = "非常" + e.zh
			if e.zhStrong != "" {
				s += "，" + e.zhStrong
			}
		}
	default:
		switch min(max(intensity, 0), 3) {
		case 0:
			s = "calm, a hint of " + e.en
		case 1:
			s = "slightly " + e.en
		case 2:
			s = e.en
		default:
			s = "very " + e.en
			if e.enStrong != "" {
				s += ", " + e.enStrong
			}
		}
	}
	if cont {
		tail := ", carrying on"
		if zh {
			tail = "，接着说"
		}
		if utf8.RuneCountInString(s+tail) <= limit {
			s += tail
		}
	}
	return s
}

// emotionBack is how far back (lines) a character's previous line still
// counts for carrying on.
const emotionBack = 4

// emotionCont says whether line li carries on its character's feeling:
// their previous line (within emotionBack lines, in the same voice) has the
// same emotion, and li is no turn.
func emotionCont(lines []LineSpec, li int) bool {
	l := lines[li]
	if l.Emotion == "" || l.Turn || l.Character == "" {
		return false
	}
	for j := li - 1; j >= max(li-emotionBack, 0); j-- {
		if p := lines[j]; p.Character == l.Character {
			return p.Emotion == l.Emotion && len(p.Spans) > 0 && len(l.Spans) > 0 && p.Spans[len(p.Spans)-1].Voice == l.Spans[0].Voice
		}
	}
	return false
}

// turnEmotion is the emotion of a merged turn (turnDirection) whose
// lines have emotions: one emotion at the strongest intensity, or, when
// they differ, their phrases in order as the turn's own wording
// ("先略带委屈，再委屈"). emotion is "" when no line has one.
func turnEmotion(lines []LineSpec) (emotion, own string, intensity int) {
	var phrases []string
	zh := false
	for _, l := range lines {
		if len(l.Spans) > 0 && cjkShare(l.Spans[0].Text) >= 0.3 {
			zh = true
		}
	}
	same := true
	for _, l := range lines {
		if l.Emotion == "" {
			continue
		}
		if emotion == "" {
			emotion = l.Emotion
		}
		same = same && l.Emotion == emotion && l.Direction == ""
		intensity = max(intensity, l.Intensity)
		if ph := EmotionInstruct(l.Emotion, l.Intensity, l.Direction, false, zh); len(phrases) == 0 || phrases[len(phrases)-1] != ph {
			phrases = append(phrases, ph)
		}
	}
	if emotion == "" || same || len(phrases) < 2 {
		return emotion, "", intensity
	}
	if zh {
		return emotion, "先" + strings.Join(phrases, "，再"), intensity
	}
	return emotion, "first " + strings.Join(phrases, ", then "), intensity
}

// LineInstruct is the instruction line li of a chapter's lines is sent
// when it is said on its own (not merged into a turn), "" for none: its
// emotion's, else the first director's (previews, evaluation).
func LineInstruct(lines []LineSpec, li int, scene string) string {
	l := lines[li]
	if len(l.Spans) == 0 {
		return ""
	}
	say := l.Spans[0].Text
	switch {
	case l.Emotion != "":
		return EmotionInstruct(l.Emotion, l.Intensity, l.Direction, emotionCont(lines, li), cjkShare(say) >= 0.3)
	case l.Direction != "" || l.Persona != "" || scene != "":
		return StoryInstruct(say, scene, l.Persona, l.Direction, l.Intensity)
	}
	return ""
}

// EmotionExaggeration is Chatterbox's emotion exaggeration for an
// intensity (its default is 0.5).
func EmotionExaggeration(intensity int) float64 {
	return []float64{0.35, 0.5, 0.7, 0.9}[min(max(intensity, 0), 3)]
}

// emotionGapWeight scales an emotion's pause factor by intensity: a
// faint feeling barely changes the pause.
var emotionGapWeight = []float64{0.25, 0.5, 0.8, 1}

// EmotionGapFactor is how much an emotion at an intensity stretches (>1)
// or shortens (<1) the pause next to its line; 1 for none.
func EmotionGapFactor(emotion string, intensity int) float64 {
	e, ok := emotions[NormEmotion(emotion)]
	if !ok {
		return 1
	}
	return 1 + (e.gap-1)*emotionGapWeight[min(max(intensity, 0), 3)]
}

// Limits of an emotion-paced gap (seconds).
const (
	emotionGapMin = 0.06
	emotionGapMax = 1.5
)

// emotionGap is the gap before cur after prev (gap: the pacing's) with
// the emotions of both: the next speaker's feeling counts more (how soon
// they answer) than the last one's (how long it hangs in the air). A cut
// in (… —) is never lengthened; lines without an emotion leave the gap
// as it is.
func emotionGap(gap float64, prev, cur LineSpec) float64 {
	if prev.Emotion == "" && cur.Emotion == "" || prev.PauseAfter > 0 {
		return gap
	}
	f := math.Pow(EmotionGapFactor(cur.Emotion, cur.Intensity), 0.6) * math.Pow(EmotionGapFactor(prev.Emotion, prev.Intensity), 0.4)
	if lineEnding(prev) == endTrailing {
		f = min(f, 1)
	}
	return math.Round(min(max(gap*f, emotionGapMin), max(gap, emotionGapMax))*1000) / 1000
}

// --- smoothing ---------------------------------------------------------------

// EmotionBeat is one line as the smoothing sees it: who says it, its
// emotion and intensity, whether it is a turn, and whether the user set
// it (Fixed: kept as it is).
type EmotionBeat struct {
	Who       string
	Emotion   string
	Intensity int
	Turn      bool
	Fixed     bool
}

// Sparse emotion: at most this share of a scene's lines are strong
// (intensity 2 or more), at most peakShare of them 3; at most turnShare
// are turns. A scene where every line is emotional sounds like a list of
// examples; the plan's "情绪要省着用".
const (
	strongShare = 0.4
	peakShare   = 1.0 / 8
	turnShare   = 1.0 / 10
)

// SmoothEmotions applies the continuity rules to a scene's beats, in
// order, and returns them: emotions normalised (unknown ones are
// steady), a steady line at most intensity 1, turns and strong lines
// kept few, and the same character's consecutive lines at most one
// intensity apart, without swinging between pleasant and unpleasant
// feelings, unless the later one is a turn. Fixed beats are kept as they
// are and the others follow them.
func SmoothEmotions(beats []EmotionBeat) []EmotionBeat {
	out := slices.Clone(beats)
	n := len(out)
	for i := range out {
		b := &out[i]
		if b.Fixed {
			continue
		}
		b.Emotion = NormEmotion(b.Emotion)
		if b.Emotion == "" {
			b.Emotion = EmotionSteady
		}
		b.Intensity = min(max(b.Intensity, 0), 3)
		if b.Emotion == EmotionSteady {
			b.Intensity = min(b.Intensity, 1)
		}
	}
	// Turns: the largest changes keep their flag.
	prevOf := func(i int) int {
		for j := i - 1; j >= 0; j-- {
			if out[j].Who == out[i].Who {
				return j
			}
		}
		return -1
	}
	jump := func(i int) int {
		j := prevOf(i)
		if j < 0 {
			return out[i].Intensity
		}
		d := abs(out[i].Intensity - out[j].Intensity)
		if out[i].Emotion != out[j].Emotion {
			d += 2
		}
		return d
	}
	limitFlags(out, max(1, int(math.Round(float64(n)*turnShare))), func(b EmotionBeat) bool { return b.Turn }, jump,
		func(b *EmotionBeat) { b.Turn = false })
	// Peaks: turns first, then the strongest change.
	peak := func(i int) int { return jump(i) + 10*btoi(out[i].Turn) }
	limitFlags(out, max(1, int(math.Round(float64(n)*peakShare))), func(b EmotionBeat) bool { return b.Intensity >= 3 }, peak,
		func(b *EmotionBeat) { b.Intensity = 2 })
	limitFlags(out, max(1, int(math.Ceil(float64(n)*strongShare))), func(b EmotionBeat) bool { return b.Intensity >= 2 }, peak,
		func(b *EmotionBeat) { b.Intensity = 1 })
	// A character's feeling moves one step at a time, except at a turn.
	for i := range out {
		b := &out[i]
		j := prevOf(i)
		if b.Fixed || b.Turn || j < 0 {
			continue
		}
		p := out[j]
		switch {
		case b.Intensity > p.Intensity+1:
			b.Intensity = p.Intensity + 1
		case b.Intensity < p.Intensity-1:
			b.Intensity = p.Intensity - 1
		}
		va, vb := emotions[p.Emotion].valence, emotions[b.Emotion].valence
		if va*vb < 0 && p.Intensity >= 2 && b.Intensity >= 2 {
			b.Intensity = 1
		}
		if b.Emotion == EmotionSteady {
			b.Intensity = min(b.Intensity, 1)
		}
	}
	return out
}

// limitFlags keeps at most keep of the beats that have a flag (has): the
// fixed ones, then those that rank highest; clear drops the flag of the
// others.
func limitFlags(beats []EmotionBeat, keep int, has func(EmotionBeat) bool, rank func(int) int, clear func(*EmotionBeat)) {
	var idx []int
	fixed := 0
	for i, b := range beats {
		if has(b) {
			if b.Fixed {
				fixed++
				continue
			}
			idx = append(idx, i)
		}
	}
	room := keep - fixed
	if len(idx) <= max(room, 0) {
		return
	}
	ranks := make(map[int]int, len(idx))
	for _, i := range idx {
		ranks[i] = rank(i)
	}
	slices.SortStableFunc(idx, func(a, b int) int { return ranks[b] - ranks[a] })
	for _, i := range idx[max(room, 0):] {
		clear(&beats[i])
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
