package text

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// DefaultMaxChars is the TTS chunk limit (runes) when Options.MaxChars is 0.
const DefaultMaxChars = 800

// Mode chooses what one chunk holds.
type Mode uint8

const (
	// Pack packs whole sentences into chunks of at most MaxChars: the TTS
	// default, few chunks with natural boundaries.
	Pack Mode = iota
	// Sentences makes one chunk per sentence (Stories "split by sentences").
	Sentences
	// Paragraphs makes one chunk per paragraph.
	Paragraphs
	// Chapters makes one chunk per chapter body; "# Title" headings are
	// chunks of their own in every mode.
	Chapters
)

// Options configure Chunks.
type Options struct {
	Mode Mode
	// MaxChars caps a chunk's length in runes: 0 means DefaultMaxChars, a
	// negative value means no cap. Oversized sentences are cut at a clause
	// mark, then a line break, then a space, then anywhere outside a tag.
	MaxChars int
	// CrossParagraphs lets Pack fill a chunk across blank lines. Headings
	// still break chunks.
	CrossParagraphs bool
}

// Cut says how a chunk's end was chosen.
type Cut uint8

const (
	CutNatural Cut = iota // end of a sentence, paragraph or text
	CutClause             // at a comma, semicolon, colon or dash
	CutLine               // at a line break inside a paragraph
	CutSpace              // between words
	CutHard               // anywhere (no better place in the window)
)

// Chunk is one piece of a script, with its source offsets for highlighting
// and caching. Text is src[Start:End]: trimmed, otherwise verbatim (it may
// still contain line breaks and tags).
type Chunk struct {
	Span
	Text      string
	Paragraph int  // index of the paragraph the chunk starts in
	Heading   bool // a "# Title" line
	Cut       Cut
}

// Paragraph is a block of text separated from the next by a blank line.
type Paragraph struct {
	Span
	Heading bool // a "# Title" line, always its own paragraph
}

// Chunks splits src into chunks according to opt. It never splits inside a
// bracket tag ("[pause 1.5s]") or an override ("[[word|say]]"); a single tag
// longer than MaxChars becomes an oversized chunk rather than being cut.
func Chunks(src string, opt Options) []Chunk {
	max := opt.MaxChars
	if max == 0 {
		max = DefaultMaxChars
	}
	paras := Paras(src)
	tags := Tags(src)
	var out []Chunk
	emit := func(sp Span, para int, heading bool, cut Cut) {
		sp = sp.TrimSpace(src)
		if sp.Len() == 0 {
			return
		}
		out = append(out, Chunk{Span: sp, Text: sp.Of(src), Paragraph: para, Heading: heading, Cut: cut})
	}
	type group struct {
		first int // first paragraph index
		sents []Span
	}
	flush := func(g *group, perSentence bool) {
		if len(g.sents) == 0 {
			return
		}
		if perSentence {
			for _, s := range g.sents {
				for _, p := range splitLong(src, s, max, tags) {
					emit(p.Span, paraOf(paras, p.Start, g.first), false, p.cut)
				}
			}
		} else {
			for _, p := range pack(src, g.sents, max, tags) {
				emit(p.Span, paraOf(paras, p.Start, g.first), false, p.cut)
			}
		}
		g.sents = nil
	}
	var g group
	for i, p := range paras {
		if p.Heading {
			flush(&g, opt.Mode == Sentences)
			emit(p.Span, i, true, CutNatural)
			continue
		}
		if len(g.sents) == 0 {
			g.first = i
		}
		switch opt.Mode {
		case Sentences:
			g.sents = append(g.sents, sentenceSpans(src, p.Span, tags)...)
			flush(&g, true)
		case Paragraphs:
			g.sents = sentenceSpans(src, p.Span, tags)
			flush(&g, false)
		case Chapters:
			g.sents = append(g.sents, sentenceSpans(src, p.Span, tags)...)
		default:
			g.sents = append(g.sents, sentenceSpans(src, p.Span, tags)...)
			if !opt.CrossParagraphs {
				flush(&g, false)
			}
		}
	}
	flush(&g, opt.Mode == Sentences)
	return out
}

// SentenceSpans returns the sentences of src, trimmed, in order.
func SentenceSpans(src string) []Span {
	tags := Tags(src)
	var out []Span
	for _, p := range Paras(src) {
		out = append(out, sentenceSpans(src, p.Span, tags)...)
	}
	return out
}

// Paras splits src into paragraphs at blank lines. When src has no blank
// line at all, every non-empty line is a paragraph (a script typed one line
// per thought). "# Title" lines are always paragraphs of their own.
func Paras(src string) []Paragraph {
	type line struct {
		Span
		blank, heading bool
	}
	var lines []line
	hasBlank := false
	nonBlank := 0
	for start := 0; start < len(src); {
		end := strings.IndexByte(src[start:], '\n')
		next := 0
		if end < 0 {
			end = len(src)
			next = len(src)
		} else {
			end += start
			next = end + 1
		}
		l := line{Span: Span{start, end}}
		body := strings.TrimRight(src[start:end], "\r")
		l.blank = isBlank(body)
		l.heading = IsHeading(body)
		if l.blank {
			if nonBlank > 0 {
				hasBlank = true
			}
		} else {
			nonBlank++
		}
		lines = append(lines, l)
		start = next
	}
	var out []Paragraph
	cur := Span{-1, -1}
	flush := func() {
		if cur.Start >= 0 {
			if sp := cur.TrimSpace(src); sp.Len() > 0 {
				out = append(out, Paragraph{Span: sp})
			}
		}
		cur = Span{-1, -1}
	}
	for _, l := range lines {
		switch {
		case l.blank:
			flush()
		case l.heading:
			flush()
			out = append(out, Paragraph{Span: l.Span.TrimSpace(src), Heading: true})
		default:
			if !hasBlank {
				flush()
			}
			if cur.Start < 0 {
				cur.Start = l.Start
			}
			cur.End = l.End
		}
	}
	flush()
	return out
}

// IsHeading reports whether line is a chapter heading: "#" alone or "#"
// followed by a space. "##" and deeper are body text.
func IsHeading(line string) bool {
	line = strings.TrimRight(line, " \t\r")
	return line == "#" || strings.HasPrefix(line, "# ") || strings.HasPrefix(line, "#\t")
}

func isBlank(s string) bool {
	for _, r := range s {
		if !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func paraOf(paras []Paragraph, off, from int) int {
	for i := from; i < len(paras); i++ {
		if off < paras[i].End {
			return i
		}
	}
	return len(paras) - 1
}

// inTag returns the tag containing byte offset i (strictly inside: after
// its first byte), if any.
func inTag(tags []Span, i int) (Span, bool) {
	// Tags are sorted; binary search for the last tag starting before i.
	lo, hi := 0, len(tags)
	for lo < hi {
		m := (lo + hi) / 2
		if tags[m].Start < i {
			lo = m + 1
		} else {
			hi = m
		}
	}
	if lo > 0 && i < tags[lo-1].End {
		return tags[lo-1], true
	}
	return Span{}, false
}

func isTerminator(r rune) bool {
	switch r {
	case '.', '!', '?', '。', '！', '？', '｡', '‼', '⁇', '⁈', '⁉', '؟', '۔', '।', '॥', '።', '፧', '᙮', '꓿':
		return true
	}
	return false
}

// isWideTerminator: a terminator that ends a sentence even when glued to
// the next character (CJK and other scripts written without spaces).
func isWideTerminator(r rune) bool {
	switch r {
	case '。', '！', '？', '｡', '।', '॥', '።', '፧':
		return true
	}
	return false
}

func isCloser(r rune) bool {
	switch r {
	case '"', '\'', '”', '’', '」', '』', '）', ')', '》', '〉', '】', '〕', '»', '›', '〞', '＂', '］':
		return true
	}
	return false
}

func isClauseMark(r rune) bool {
	switch r {
	case ',', ';', ':', '，', '、', '；', '：', '—', '–', '…', '‥', '·', '،', '؛', '၊', '።':
		return true
	}
	return false
}

// abbreviations that end with a period without ending a sentence
// (compared lower-case, without the final period).
var abbreviations = map[string]bool{
	"mr": true, "mrs": true, "ms": true, "dr": true, "prof": true, "st": true, "jr": true, "sr": true,
	"vs": true, "etc": true, "e.g": true, "i.e": true, "no": true, "fig": true, "vol": true, "approx": true,
	"inc": true, "ltd": true, "co": true, "mt": true, "gen": true, "gov": true, "col": true, "capt": true,
	"lt": true, "sgt": true, "rev": true, "hon": true, "ave": true, "blvd": true, "dept": true, "est": true,
	"sra": true, "srta": true, "dra": true, "ud": true, "uds": true, "núm": true, "pág": true,
	"mme": true, "mlle": true, "env": true, "cf": true, "p": true, "pp": true,
	"z.b": true, "bzw": true, "ca": true, "nr": true, "hr": true, "fr": true, "s": true, "d.h": true, "u.a": true,
	"jan": true, "feb": true, "mar": true, "apr": true, "jun": true, "jul": true, "aug": true, "sep": true,
	"sept": true, "oct": true, "nov": true, "dec": true,
}

// sentenceSpans splits one paragraph into sentences.
func sentenceSpans(src string, para Span, tags []Span) []Span {
	var out []Span
	start := para.Start
	i := para.Start
	for i < para.End {
		if t, ok := inTag(tags, i+1); ok && t.Start == i {
			i = t.End
			continue
		}
		r, n := utf8.DecodeRuneInString(src[i:para.End])
		if !isTerminator(r) {
			i += n
			continue
		}
		// Swallow a run of terminators and closing quotes/brackets.
		j := i + n
		for j < para.End {
			r2, n2 := utf8.DecodeRuneInString(src[j:para.End])
			if isTerminator(r2) || (isCloser(r2) && !isTagStart(tags, j)) {
				j += n2
				continue
			}
			break
		}
		if sentenceEnds(src, para, i, j, r) {
			if sp := (Span{start, j}).TrimSpace(src); sp.Len() > 0 {
				out = append(out, sp)
			}
			start = j
		}
		i = j
	}
	if sp := (Span{start, para.End}).TrimSpace(src); sp.Len() > 0 {
		out = append(out, sp)
	}
	return out
}

func isTagStart(tags []Span, i int) bool {
	t, ok := inTag(tags, i+1)
	return ok && t.Start == i
}

// sentenceEnds decides whether the terminator r at i (with closers up to
// j) ends a sentence.
func sentenceEnds(src string, para Span, i, j int, r rune) bool {
	if j >= para.End {
		return true
	}
	next, _ := utf8.DecodeRuneInString(src[j:para.End])
	if isWideTerminator(r) {
		return true
	}
	if IsUnspaced(next) {
		return true // "Hello.你好"
	}
	if !unicode.IsSpace(next) {
		return false // "3.14", "example.com", "e.g.x"
	}
	// Look at the next word.
	k := j
	for k < para.End {
		r2, n2 := utf8.DecodeRuneInString(src[k:para.End])
		if !unicode.IsSpace(r2) {
			break
		}
		k += n2
	}
	if k < para.End {
		nr, _ := utf8.DecodeRuneInString(src[k:para.End])
		if unicode.IsLower(nr) {
			return false // "e.g. this", "Yahoo! is"
		}
	}
	if r != '.' {
		return true
	}
	// The word before the period.
	w := i
	for w > para.Start {
		pr, pn := utf8.DecodeLastRuneInString(src[para.Start:w])
		if !(unicode.IsLetter(pr) || pr == '.') {
			break
		}
		w -= pn
	}
	word := src[w:i]
	if word == "" {
		return true
	}
	if utf8.RuneCountInString(word) == 1 {
		if wr, _ := utf8.DecodeRuneInString(word); unicode.IsUpper(wr) {
			return false // an initial: "J. K. Rowling"
		}
	}
	return !abbreviations[strings.ToLower(word)]
}

type piece struct {
	Span
	cut Cut
}

// pack groups consecutive sentences into pieces of at most max runes.
func pack(src string, sents []Span, max int, tags []Span) []piece {
	if max < 0 {
		return []piece{{Span: Span{sents[0].Start, sents[len(sents)-1].End}}}
	}
	var out []piece
	cur := Span{-1, -1}
	curRunes := 0
	for _, s := range sents {
		sr := utf8.RuneCountInString(s.Of(src))
		if cur.Start >= 0 {
			gap := utf8.RuneCountInString(src[cur.End:s.Start])
			if curRunes+gap+sr <= max {
				cur.End = s.End
				curRunes += gap + sr
				continue
			}
			out = append(out, piece{Span: cur})
			cur = Span{-1, -1}
		}
		if sr <= max {
			cur, curRunes = s, sr
			continue
		}
		long := splitLong(src, s, max, tags)
		out = append(out, long[:len(long)-1]...)
		last := long[len(long)-1]
		cur, curRunes = last.Span, utf8.RuneCountInString(last.Of(src))
	}
	if cur.Start >= 0 {
		out = append(out, piece{Span: cur})
	}
	return out
}

// splitLong cuts one sentence into pieces of at most max runes. The cut is
// the best candidate in the last 60% of the window: a clause mark, else a
// line break, else a space, else any rune boundary outside a tag.
func splitLong(src string, s Span, max int, tags []Span) []piece {
	if max < 0 {
		return []piece{{Span: s}}
	}
	var out []piece
	rest := s
	for utf8.RuneCountInString(rest.Of(src)) > max {
		// Byte offset of the end of the window (max runes).
		lim := rest.Start
		for n := 0; n < max && lim < rest.End; n++ {
			_, sz := utf8.DecodeRuneInString(src[lim:rest.End])
			lim += sz
		}
		minCut := rest.Start
		for n := 0; n < max*2/5 && minCut < lim; n++ {
			_, sz := utf8.DecodeRuneInString(src[minCut:rest.End])
			minCut += sz
		}
		cut, kind := bestCut(src, rest, minCut, lim, tags)
		if cut <= rest.Start || cut >= rest.End {
			break
		}
		out = append(out, piece{Span: Span{rest.Start, cut}.TrimSpace(src), cut: kind})
		rest = Span{cut, rest.End}.TrimSpace(src)
		if rest.Len() == 0 {
			break
		}
	}
	if rest.Len() > 0 {
		out = append(out, piece{Span: rest})
	}
	return out
}

// bestCut picks the cut position (a byte offset where the next piece
// starts) in (minCut, lim] of rest.
func bestCut(src string, rest Span, minCut, lim int, tags []Span) (int, Cut) {
	best := [4]int{-1, -1, -1, -1} // clause, line, space, hard
	for i := minCut; i <= lim && i < rest.End; {
		if t, ok := inTag(tags, i); ok && t.Start < i {
			i = t.End
			continue
		}
		r, n := utf8.DecodeRuneInString(src[i:rest.End])
		after := i + n
		switch {
		case isClauseMark(r) && after <= lim:
			j := after
			for j < rest.End {
				r2, n2 := utf8.DecodeRuneInString(src[j:rest.End])
				if !isCloser(r2) || isTagStart(tags, j) {
					break
				}
				j += n2
			}
			if j <= lim {
				best[0] = j
			}
		case r == '\n':
			best[1] = i
		case unicode.IsSpace(r):
			best[2] = i
		}
		if i > rest.Start && !unicode.Is(unicode.Mn, r) && !unicode.Is(unicode.Mc, r) {
			if _, ok := inTag(tags, i); !ok {
				best[3] = i
			}
		}
		i = after
	}
	for k, c := range best {
		if c > rest.Start {
			return c, Cut(k + 1)
		}
	}
	// No candidate in the window: the window ends inside a tag that starts
	// at rest.Start; cut after the tag.
	if t, ok := inTag(tags, lim); ok {
		if t.Start > rest.Start {
			return t.Start, CutHard
		}
		return t.End, CutHard
	}
	return lim, CutHard
}
