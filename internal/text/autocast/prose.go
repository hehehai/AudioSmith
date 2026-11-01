package autocast

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/hehehai/AudioSmith/internal/text"
)

// quote is a quoted span: outer includes the marks, inner excludes them.
type quote struct {
	outer, inner text.Span
	speaker      string
	named        bool
}

var quotePairs = map[rune]string{
	'“': "”", '"': `"`, '「': "」", '『': "』", '«': "»", '„': "“”", '‘': "’", '‹': "›", '﹁': "﹂",
}

// findQuotes returns the top-level quotes of p in order. An unclosed quote
// runs to the end of the paragraph (novels leave a quote open when it
// continues in the next paragraph).
func findQuotes(src string, p text.Span) []quote {
	var out []quote
	for i := p.Start; i < p.End; {
		r, n := utf8.DecodeRuneInString(src[i:p.End])
		closers, ok := quotePairs[r]
		if !ok || (r == '‘' && i > p.Start && isLetterBefore(src, i)) {
			i += n
			continue
		}
		j := i + n
		end := -1
		for j < p.End {
			c, cn := utf8.DecodeRuneInString(src[j:p.End])
			if strings.ContainsRune(closers, c) {
				if c == '’' && j+cn < p.End && isLetterAt(src, j+cn) {
					j += cn // an apostrophe: it’s
					continue
				}
				end = j
				j += cn
				break
			}
			j += cn
		}
		q := quote{outer: text.Span{Start: i, End: j}, inner: text.Span{Start: i + n, End: j}}
		if end >= 0 {
			q.inner.End = end
		}
		out = append(out, q)
		i = j
	}
	return out
}

func isLetterBefore(src string, i int) bool {
	r, _ := utf8.DecodeLastRuneInString(src[:i])
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func isLetterAt(src string, i int) bool {
	r, _ := utf8.DecodeRuneInString(src[i:])
	return unicode.IsLetter(r)
}

const enVerbs = `said|says|say|asked|asks|replied|replies|answered|answers|whispered|whispers|shouted|shouts|` +
	`cried|cries|yelled|yells|muttered|mutters|murmured|murmurs|called|calls|exclaimed|exclaims|added|adds|` +
	`continued|continues|snapped|snaps|laughed|laughs|sighed|sighs|began|begins|explained|explains|insisted|` +
	`insists|repeated|repeats|warned|warns|screamed|screams|demanded|demands|admitted|admits|agreed|agrees|` +
	`growled|growls|hissed|hisses|pleaded|pleads|protested|protests|remarked|remarks|responded|responds|` +
	`stammered|stammers|suggested|suggests|whimpered|wondered|wonders|mumbled|mumbles|gasped|gasps|` +
	`chuckled|retorted|observed|announced|declared|interrupted|offered|promised|told|tells|added`

const (
	enName = `((?:[Tt]he\s+(?:[a-z]+\s+)?[a-z]+)|(?:[A-Z][\p{L}'’-]*\.?(?:\s+[A-Z][\p{L}'’-]*){0,2}))`
	adverb = `(?:[a-z]+ly\s+)?`
	// zh: verbs, then adverbial phrases that may sit between name and verb.
	zhVerbs   = `说道|问道|答道|喊道|叫道|笑道|嚷道|骂道|叹道|回答道|回答|说|道|问|答|喊|叫|吼`
	jaVerbs   = `言った|言いました|言う|叫んだ|叫びました|尋ねた|聞いた|答えた|呟いた|つぶやいた|笑った|囁いた|ささやいた`
	zhPronoun = `他|她|我|你|它|他们|她们|我们|你们|大家|对方|众人`
)

var (
	// After the closing quote: `, said John` / `, John said` / `, said the captain`.
	enAfterVerbName = regexp.MustCompile(`^[\s,]*(?:` + enVerbs + `)\s+` + enName)
	enAfterNameVerb = regexp.MustCompile(`^[\s,]*` + enName + `\s+` + adverb + `(?:` + enVerbs + `)\b`)
	// Before the opening quote: `John said, ` / `said John: `.
	enBeforeNameVerb = regexp.MustCompile(enName + `\s+` + adverb + `(?:` + enVerbs + `)(?:\s+to\s+\S+)?[\s,:]*$`)
	enBeforeVerbName = regexp.MustCompile(`(?:` + enVerbs + `)\s+` + enName + `[\s,:]*$`)

	zhBefore  = regexp.MustCompile(`(\p{Han}{1,12}?)(?:` + zhVerbs + `)[：:，,]?\s*$`)
	zhAfter   = regexp.MustCompile(`^[，,\s]*(\p{Han}{1,12}?)(?:` + zhVerbs + `)`)
	zhVerbEnd = regexp.MustCompile(`(?:` + zhVerbs + `)$`)
	jaAfter   = regexp.MustCompile(`^と[、\s]*([\p{Han}\p{Katakana}ー]{1,8})(?:は|が)[\p{Han}\p{Hiragana}、]{0,8}?(?:` + jaVerbs + `)`)

	zhAdverbs = []string{"低声", "轻声", "大声", "小声", "笑着", "冷冷地", "淡淡地", "忍不住", "又", "也", "便", "就",
		"却", "连忙", "急忙", "赶紧", "喃喃", "轻轻地", "缓缓", "开口", "不禁", "马上", "立刻", "微笑着", "皱眉",
		"点头", "高声", "沉声", "怒", "笑", "冷笑", "叹气", "对他", "对她", "对我", "对大家", "接着", "继续", "突然", "地"}
	enPronouns = map[string]bool{"he": true, "she": true, "they": true, "i": true, "we": true, "you": true, "it": true,
		"him": true, "her": true, "someone": true, "everyone": true, "somebody": true}
	zhPronouns = func() map[string]bool {
		m := map[string]bool{}
		for _, p := range strings.Split(zhPronoun, "|") {
			m[p] = true
		}
		return m
	}()
)

// prose splits one paragraph into narration and quotes.
func prose(src string, p text.Span, keepQuotes bool) []Line {
	qs := findQuotes(src, p)
	if len(qs) == 0 {
		return []Line{{Text: p.Of(src), Span: p, Kind: Narration}}
	}
	// Narration around each quote, limited to the same sentence.
	for i := range qs {
		prevEnd := p.Start
		if i > 0 {
			prevEnd = qs[i-1].outer.End
		}
		nextStart := p.End
		if i+1 < len(qs) {
			nextStart = qs[i+1].outer.Start
		}
		before := lastClause(src[prevEnd:qs[i].outer.Start])
		after := firstClause(src[qs[i].outer.End:nextStart])
		name, ok := attribute(before, after)
		qs[i].speaker, qs[i].named = name, ok && name != ""
	}
	// One named speaker in the paragraph also speaks its unattributed quotes.
	only := ""
	for _, q := range qs {
		if q.named {
			if only != "" && !strings.EqualFold(only, q.speaker) {
				only = ""
				break
			}
			only = q.speaker
		}
	}
	var out []Line
	narr := func(a, b int) {
		sp := text.Span{Start: a, End: b}.TrimSpace(src)
		if sp.Len() > 0 {
			out = append(out, Line{Text: sp.Of(src), Span: sp, Kind: Narration})
		}
	}
	prev := p.Start
	for _, q := range qs {
		narr(prev, q.outer.Start)
		sp := q.inner
		if keepQuotes {
			sp = q.outer
		}
		sp = sp.TrimSpace(src)
		speaker := q.speaker
		if !q.named {
			speaker = only
		}
		if sp.Len() > 0 {
			out = append(out, Line{Speaker: speaker, Text: sp.Of(src), Span: sp, Kind: Quote})
		}
		prev = q.outer.End
	}
	narr(prev, p.End)
	return out
}

// lastClause is the part of s after its last sentence end.
func lastClause(s string) string {
	if i := strings.LastIndexAny(s, ".!?。！？;；\n"); i >= 0 {
		_, n := utf8.DecodeRuneInString(s[i:])
		return s[i+n:]
	}
	return s
}

// firstClause is the part of s up to its first sentence end.
func firstClause(s string) string {
	if i := strings.IndexAny(s, ".!?。！？;；\n"); i >= 0 {
		return s[:i]
	}
	return s
}

// attribute finds the speaker named around a quote; ok is false when no
// speech verb is found. A pronoun gives ok with an empty name.
func attribute(before, after string) (string, bool) {
	for _, re := range []*regexp.Regexp{enAfterVerbName, enAfterNameVerb} {
		if m := re.FindStringSubmatch(after); m != nil {
			return enSpeaker(m[1])
		}
	}
	if m := zhAfter.FindStringSubmatch(after); m != nil {
		return zhSpeaker(m[1])
	}
	if m := jaAfter.FindStringSubmatch(after); m != nil {
		return m[1], true
	}
	for _, re := range []*regexp.Regexp{enBeforeNameVerb, enBeforeVerbName} {
		if m := re.FindStringSubmatch(before); m != nil {
			return enSpeaker(m[1])
		}
	}
	if m := zhBefore.FindStringSubmatch(before); m != nil {
		return zhSpeaker(m[1])
	}
	return "", false
}

func enSpeaker(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if enPronouns[strings.ToLower(name)] {
		return "", true
	}
	return cleanName(name), true
}

// zhSpeaker strips adverbs between name and verb ("小明低声" → "小明") and
// rejects pronouns and phrases too long to be a name.
func zhSpeaker(s string) (string, bool) {
	// Only the part after the last clause mark is the subject.
	if i := strings.LastIndexAny(s, "，,、：:"); i >= 0 {
		s = s[i+1:]
	}
	for changed := true; changed; {
		changed = false
		for _, a := range zhAdverbs {
			if strings.HasSuffix(s, a) && len(s) > len(a) {
				s, changed = strings.TrimSuffix(s, a), true
			}
		}
	}
	switch {
	case zhPronouns[s]:
		return "", true
	case slices.Contains(zhAdverbs, s):
		return "", false // "妈妈放下碗，轻声说": the adverb is no name
	case utf8.RuneCountInString(s) > 4 || s == "":
		return "", false
	case strings.HasSuffix(s, "知") || strings.HasSuffix(s, "难") || strings.HasSuffix(s, "味"):
		return "", false // 知道, 难道, 味道: not speech
	}
	return s, true
}
