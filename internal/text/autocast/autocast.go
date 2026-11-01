// Package autocast detects who speaks each line of a story, for the
// Stories editor's Auto-cast: tagged lines ("[Mara] Hello"), screenplay
// lines ("MARA: Hello", "小明：你好") and quoted prose attributed with a
// speech verb (`"Hello," said Mara.`, `小明说：“你好。”`). Everything else
// is narration. It is a local, rule-based parser: no model, no network.
package autocast

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/hehehai/AudioSmith/internal/text"
)

// Kind says how a line's speaker was found.
type Kind uint8

const (
	Narration  Kind = iota // no speaker: the narrator reads it
	Tagged                 // "[Name] line"
	Screenplay             // "NAME: line"
	Quote                  // quoted prose, attributed by a speech verb (or not: Speaker "")
)

// Line is one line of the cast script.
type Line struct {
	Speaker string    // "" is the narrator
	Text    string    // what is spoken: the name prefix and quote marks removed
	Span    text.Span // Text's range in the source
	Kind    Kind
	// Direction is a stage direction of a speaker line, never spoken:
	// "妈妈（温柔地）：…", "MOM (softly): …", "[Mom] (softly) …" give
	// "温柔地" and "softly". Screenplay extensions (V.O., O.S., CONT'D) are
	// not directions.
	Direction string
}

// Result lists the lines in order and the distinct speakers in order of
// first appearance (the narrator is not listed).
type Result struct {
	Lines    []Line
	Speakers []string
}

// Options configure Detect. The zero value is the default.
type Options struct {
	// Reserved are extra bracket words that are markup, not names (the
	// markup tags pause, voice, slow, fast, emphasis, spell, the default
	// non-verbal tags and a few common extras are always reserved).
	Reserved []string
	// KeepQuotes keeps the quotation marks in quoted lines' Text.
	KeepQuotes bool
}

var reserved = map[string]bool{
	"pause": true, "voice": true, "slow": true, "fast": true, "emphasis": true, "spell": true, "em": true,
	"break": true, "breath": true, "whisper": true, "laughter": true, "laugh": true, "sigh": true,
	"confirmation-en": true, "question-en": true, "question-ah": true, "question-oh": true, "question-ei": true,
	"question-yi": true, "surprise-ah": true, "surprise-oh": true, "surprise-wa": true, "surprise-yo": true,
	"dissatisfaction-hnn": true, "music": true, "applause": true, "silence": true, "inaudible": true,
}

// Narrator names in screenplay or tagged lines that mean the narrator.
var narratorNames = map[string]bool{
	"narrator": true, "narration": true, "旁白": true, "叙述者": true, "解说": true, "ナレーション": true,
	"ナレーター": true, "내레이션": true, "해설": true, "narrador": true, "narrateur": true, "erzähler": true,
}

// Words that end in a colon at the start of a line without being a speaker.
var notSpeakers = map[string]bool{
	"note": true, "notes": true, "warning": true, "chapter": true, "example": true, "summary": true, "update": true,
	"source": true, "sources": true, "tip": true, "important": true, "caution": true, "edit": true, "ps": true,
	"p.s": true, "re": true, "subject": true, "date": true, "time": true, "from": true, "to": true, "cc": true,
	"title": true, "author": true, "scene": true, "act": true, "int": true, "ext": true, "http": true, "https": true,
	"注": true, "注意": true, "备注": true, "提示": true, "第一章": true, "时间": true, "地点": true, "人物": true,
}

// Detect splits src into cast lines. Blank-line paragraphs are processed
// in order; within one, a tagged or screenplay line starts a new speaker
// line and following lines without a prefix continue it.
func Detect(src string, opt *Options) Result {
	var o Options
	if opt != nil {
		o = *opt
	}
	res := Result{}
	seen := map[string]bool{}
	add := func(l Line) {
		if strings.TrimSpace(l.Text) == "" {
			return
		}
		if l.Speaker != "" {
			k := strings.ToLower(l.Speaker)
			if !seen[k] {
				seen[k] = true
				res.Speakers = append(res.Speakers, l.Speaker)
			} else {
				for _, s := range res.Speakers { // reuse the first spelling
					if strings.ToLower(s) == k {
						l.Speaker = s
						break
					}
				}
			}
		}
		res.Lines = append(res.Lines, l)
	}
	extra := map[string]bool{}
	for _, r := range o.Reserved {
		extra[strings.ToLower(r)] = true
	}
	// Walk the lines. A speaker line starts a block that runs to the next
	// speaker line, heading or blank line; other lines form narration
	// blocks (split per line when the text has no blank lines, as
	// text.Paras does), which go through quote detection. A text without
	// blank lines is one line per line for speakers too: a line after
	// "Name: …" is narration (Mac test #136: an agent's script read the
	// narrator's lines in the characters' voices); only a name alone on
	// its line ("[Mara]") takes the lines under it.
	lines, hasBlank := allLines(src)
	var cur *Line
	curBare := false // cur's name stood alone on its line
	narr := text.Span{Start: -1}
	flushNarr := func() {
		if narr.Start >= 0 {
			for _, l := range prose(src, narr, o.KeepQuotes) {
				add(l)
			}
		}
		narr = text.Span{Start: -1}
	}
	flushCur := func() {
		if cur != nil {
			add(*cur)
		}
		cur = nil
	}
	for _, ln := range lines {
		switch body := ln.Of(src); {
		case ln.Len() == 0: // blank line
			flushCur()
			flushNarr()
		case text.IsHeading(body):
			flushCur()
			flushNarr()
			add(Line{Text: body, Span: ln, Kind: Narration})
		default:
			if l, ok := speakerLine(src, ln, extra); ok {
				flushCur()
				flushNarr()
				cur, curBare = &l, l.Text == ""
				continue
			}
			if cur != nil && !hasBlank && !curBare && !markupOnly(body) {
				flushCur()
			}
			switch {
			case cur != nil: // continuation of a speaker line
				if cur.Text == "" {
					cur.Span = ln
				} else {
					cur.Span.End = ln.End
				}
				cur.Text = cur.Span.Of(src)
			case narr.Start < 0:
				narr = ln
			case !hasBlank:
				flushNarr()
				narr = ln
			default:
				narr.End = ln.End
			}
		}
	}
	flushCur()
	flushNarr()
	return res
}

// allLines returns every line of src trimmed (blank lines as empty spans)
// and whether a blank line separates two non-blank ones.
func allLines(src string) ([]text.Span, bool) {
	var out []text.Span
	hasBlank, seen := false, false
	start := 0
	for i := 0; i <= len(src); i++ {
		if i == len(src) || src[i] == '\n' {
			sp := trim(src, text.Span{Start: start, End: i})
			if sp.Len() == 0 {
				sp = text.Span{Start: start, End: start}
				if seen {
					hasBlank = true
				}
			} else {
				seen = true
			}
			out = append(out, sp)
			start = i + 1
		}
	}
	return out, hasBlank
}

func trim(src string, sp text.Span) text.Span {
	for sp.Start < sp.End {
		r, n := utf8.DecodeRuneInString(src[sp.Start:sp.End])
		if !unicode.IsSpace(r) {
			break
		}
		sp.Start += n
	}
	for sp.End > sp.Start {
		r, n := utf8.DecodeLastRuneInString(src[sp.Start:sp.End])
		if !unicode.IsSpace(r) {
			break
		}
		sp.End -= n
	}
	return sp
}

var (
	tagRe = regexp.MustCompile(`^\[([^\[\]]{1,40})\][ \t]*`)
	// Screenplay: a name, an optional parenthetical "(V.O.)", a colon.
	playRe = regexp.MustCompile(`^([^:：\[\]()（）"“”「」]{1,40}?)[ \t]*(?:[(（]([^()（）]{1,20})[)）])?[ \t]*[:：][ \t]*`)
	// A parenthetical at the start of a speaker's line: a stage direction.
	leadRe = regexp.MustCompile(`^[(（]([^()（）]{1,30})[)）][ \t]*`)
)

// extensions are screenplay parentheticals that say where a voice comes
// from, not how it is said.
var extensions = map[string]bool{
	"v.o.": true, "vo": true, "v.o": true, "o.s.": true, "os": true, "o.s": true, "o.c.": true, "oc": true,
	"cont'd": true, "cont’d": true, "contd": true, "continuing": true, "off": true, "off-screen": true, "voice-over": true,
	"画外音": true, "画外": true, "旁白": true, "内心": true, "独白": true, "续": true,
}

// direction is a parenthetical's text as a stage direction ("" for an
// extension).
func direction(p string) string {
	p = strings.TrimSpace(p)
	if extensions[strings.ToLower(p)] {
		return ""
	}
	return p
}

// withDirection moves a parenthetical at the start of a speaker line's
// text into its Direction (when it has none).
func withDirection(src string, l Line) Line {
	m := leadRe.FindStringSubmatchIndex(l.Text)
	if m == nil {
		return l
	}
	d := direction(l.Text[m[2]:m[3]])
	if d == "" {
		return l
	}
	if l.Direction == "" {
		l.Direction = d
	}
	l.Span.Start += m[1]
	l.Text = l.Span.Of(src)
	return l
}

// Speaker recognises one line as a speaker's: "[Name] text" or
// "NAME: text" ("名字（说明）：台词"), as Detect does. name is "" for a
// narrator alias ("旁白：…"); body is where the spoken text starts in
// line, after the name, colon and any leading stage direction.
func Speaker(line string) (name, direction string, body int, ok bool) {
	l, ok := speakerLine(line, text.Span{End: len(line)}, nil)
	if !ok {
		return "", "", 0, false
	}
	return l.Speaker, l.Direction, l.Span.Start, true
}

// speakerLine recognises "[Name] text" and "NAME: text".
func speakerLine(src string, ln text.Span, extra map[string]bool) (Line, bool) {
	s := ln.Of(src)
	if m := tagRe.FindStringSubmatchIndex(s); m != nil {
		name := strings.TrimSpace(s[m[2]:m[3]])
		low := strings.ToLower(name)
		base, _, _ := strings.Cut(low, " ")
		base, _, _ = strings.Cut(base, ":")
		if !reserved[base] && !extra[low] && !strings.HasPrefix(low, "/") && isName(name) {
			body := text.Span{Start: ln.Start + m[1], End: ln.End}
			return withDirection(src, Line{Speaker: cleanName(name), Text: body.Of(src), Span: body, Kind: Tagged}), true
		}
	}
	if m := playRe.FindStringSubmatchIndex(s); m != nil && m[1] < len(s) {
		name := strings.TrimSpace(s[m[2]:m[3]])
		rest := s[m[1]:]
		if strings.HasPrefix(rest, "//") {
			return Line{}, false
		}
		if isScreenplayName(name) {
			body := text.Span{Start: ln.Start + m[1], End: ln.End}
			l := Line{Speaker: cleanName(name), Text: body.Of(src), Span: body, Kind: Screenplay}
			if m[4] >= 0 {
				l.Direction = direction(s[m[4]:m[5]])
			}
			return withDirection(src, l), true
		}
	}
	return Line{}, false
}

// isName: starts with a letter, then letters, marks, digits, spaces and
// . ' - ·
func isName(s string) bool {
	for i, r := range s {
		switch {
		case unicode.IsLetter(r):
		case i > 0 && (unicode.IsDigit(r) || unicode.Is(unicode.Mn, r) || strings.ContainsRune(" .'’-·・", r)):
		default:
			return false
		}
	}
	return s != ""
}

// isScreenplayName: up to four words each starting with an upper-case
// letter (or all caps), or up to eight CJK/Hangul letters; not a common
// label such as "Note".
func isScreenplayName(s string) bool {
	if !isName(s) || notSpeakers[strings.ToLower(strings.TrimRight(s, "."))] {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s)
	if text.IsCJK(r) {
		if zhVerbEnd.MatchString(s) {
			return false // "小明说：“…”" is prose
		}
		n := 0
		for _, c := range s {
			if !text.IsCJK(c) && c != '·' && c != '・' {
				return false
			}
			n++
		}
		return n <= 8
	}
	words := strings.Fields(s)
	if len(words) > 4 {
		return false
	}
	for _, w := range words {
		f, _ := utf8.DecodeRuneInString(w)
		if !unicode.IsUpper(f) && !unicode.Is(unicode.Lo, f) {
			return false
		}
	}
	return true
}

// cleanName title-cases all-caps Latin names, drops a leading "the" and
// maps narrator aliases to "".
func cleanName(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if narratorNames[strings.ToLower(s)] {
		return ""
	}
	if low := strings.ToLower(s); strings.HasPrefix(low, "the ") {
		s = s[4:]
	}
	words := strings.Fields(s)
	for i, w := range words {
		if w == strings.ToUpper(w) || w == strings.ToLower(w) {
			rs := []rune(strings.ToLower(w))
			if len(rs) > 0 && !text.IsCJK(rs[0]) {
				rs[0] = unicode.ToUpper(rs[0])
			}
			words[i] = string(rs)
		}
	}
	return strings.Join(words, " ")
}

// markupOnly reports whether a line is only markup tags ("[pause 1s]"):
// it stays with the speaker line above it.
func markupOnly(line string) bool {
	line = strings.TrimSpace(line)
	for line != "" {
		if line[0] != '[' {
			return false
		}
		end := strings.IndexByte(line, ']')
		if end < 0 {
			return false
		}
		line = strings.TrimSpace(line[end+1:])
	}
	return true
}
