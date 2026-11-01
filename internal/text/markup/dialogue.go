package markup

// Dialogue blocks of a manuscript (plan/dialogue-dub-audiobook-plan.md
// §3.2): a scene the user marks for a dialogue model, every line on a
// line of its own:
//
//	[对话]                      also [dialogue] and 【对话】; the closers
//	伊内丝：这是去哈罗角的船吗？      [/对话], [/dialogue], 【/对话】
//	托马斯（低声）：唯一的一班。      a turn, with a stage direction (not read)
//	他把缆绳抛上岸。                a line without a name: narration
//	（远处传来汽笛声。）             a line in parentheses: a stage direction
//	[pause 1s]                    a pause between two lines
//	[/对话]
//
// Only the words 对话 and dialogue make a marker (any case, spaces inside
// the brackets allowed), so 【系统提示】 and the like stay text. A marker
// may share its line: the words after an opening marker are the block's
// first line, the words before a closing one its last. A block never
// spans chapters: a heading ends it. Parse knows the markers as Dialogue
// tokens (never spoken); DialogueBlocks finds the blocks and their lines.

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/hehehai/AudioSmith/internal/text"
	"github.com/hehehai/AudioSmith/internal/text/autocast"
)

// Diagnostic codes of dialogue blocks (DialogueBlocks).
const (
	CodeNestedDialogue     = "markup.nested_dialogue"      // an opening marker inside a block: ignored
	CodeUnclosedDialogue   = "markup.unclosed_dialogue"    // a block without its closer: it ends with the chapter
	CodeStrayDialogueClose = "markup.stray_dialogue_close" // a closer outside a block: ignored
	CodeEmptyDialogue      = "markup.empty_dialogue"       // a block with nothing to say: ignored
	CodeDialogueHeading    = "markup.dialogue_heading"     // a heading inside a block: the block ends before it
)

// DialogueLineKind is what a line of a block is.
type DialogueLineKind uint8

const (
	DialogueTurn      DialogueLineKind = iota // "名字：台词": Name says Text
	DialogueNarration                         // a line without a name, or the narrator's
	DialogueDirection                         // "（他放下碗。）": not read
	DialoguePause                             // "[pause 1s]" alone: Millis between two lines
)

// DialogueLine is one line of a block. Spans are into the source.
type DialogueLine struct {
	Kind DialogueLineKind
	Line text.Span // the whole line, trimmed
	// Name is the speaker as written (a turn), "" for an unknown one
	// ("？：…"); NameSpan where it stands. Unknown marks the "？" lines
	// a conversion could not attribute.
	Name     string
	NameSpan text.Span
	Unknown  bool
	// Direction is a turn's stage direction ("低声"), DirSpan where.
	Direction string
	DirSpan   text.Span
	// Text is what is said: a turn's words (quotes around them left out)
	// or the narration, still in markup.
	Text   text.Span
	Millis int // a pause line's
}

// DialogueBlock is one block: its markers and lines.
type DialogueBlock struct {
	Span  text.Span // the opening marker to the closer (or the block's last line)
	Open  text.Span
	Close text.Span // zero when unclosed
	Lines []DialogueLine
}

// Closed reports whether the block has its closer.
func (b DialogueBlock) Closed() bool { return b.Close.End > 0 }

// Turns counts the block's turns with words.
func (b DialogueBlock) Turns() int {
	n := 0
	for _, l := range b.Lines {
		if l.Kind == DialogueTurn && l.Text.Len() > 0 {
			n++
		}
	}
	return n
}

// Speakers are the block's named speakers in order of first turn
// (compared as NameKey does; the first spelling kept).
func (b DialogueBlock) Speakers() []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range b.Lines {
		if l.Kind == DialogueTurn && l.Name != "" && !seen[NameKey(l.Name)] {
			seen[NameKey(l.Name)] = true
			out = append(out, l.Name)
		}
	}
	return out
}

// NameKey is how cast names compare: case, full-width letters and outer
// spaces do not count (Ines, ＩＮＥＳ, [voice: ines ] are one person).
func NameKey(s string) string {
	s = strings.Map(func(r rune) rune {
		if r >= 0xFF01 && r <= 0xFF5E { // full-width ASCII
			return r - 0xFEE0
		}
		if r == 0x3000 {
			return ' '
		}
		return r
	}, s)
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// marker is a dialogue marker in a line.
type marker struct {
	span text.Span
	open bool
}

// dialogueWord reports whether the inside of a bracket is a dialogue
// marker, and whether it closes.
func dialogueWord(inner string) (open, ok bool) {
	w := strings.TrimSpace(inner)
	closing := strings.HasPrefix(w, "/")
	if closing {
		w = strings.TrimSpace(w[1:])
	}
	if w == "对话" || strings.EqualFold(w, "dialogue") {
		return !closing, true
	}
	return false, false
}

func isDialogueWord(s string) bool {
	_, ok := dialogueWord(s)
	return ok
}

func openClose(open bool) string {
	if open {
		return "open"
	}
	return "close"
}

// markers finds the dialogue markers of one line ([…] and 【…】).
func markers(line string) []marker {
	var out []marker
	tags := text.Tags(line)
	inTag := func(a, b int) bool {
		for _, t := range tags {
			if a < t.End && b > t.Start {
				return true
			}
		}
		return false
	}
	for _, sp := range tags {
		raw := sp.Of(line)
		if strings.HasPrefix(raw, "[[") {
			continue
		}
		if open, ok := dialogueWord(raw[1 : len(raw)-1]); ok {
			out = append(out, marker{sp, open})
		}
	}
	for i := 0; i < len(line); {
		j := strings.Index(line[i:], "【")
		if j < 0 {
			break
		}
		a := i + j
		k := strings.Index(line[a:], "】")
		if k < 0 {
			break
		}
		b := a + k + len("】")
		if open, ok := dialogueWord(line[a+len("【") : a+k]); ok && !strings.ContainsAny(line[a+len("【"):a+k], "【\n") && !inTag(a, b) {
			out = append(out, marker{text.Span{Start: a, End: b}, open})
		}
		i = a + len("【")
	}
	if len(out) > 1 {
		sortMarkers(out)
	}
	return out
}

func sortMarkers(m []marker) {
	for i := 1; i < len(m); i++ {
		for j := i; j > 0 && m[j].span.Start < m[j-1].span.Start; j-- {
			m[j], m[j-1] = m[j-1], m[j]
		}
	}
}

// DialogueBlocks finds the dialogue blocks of src, with the problems of
// their markers. Text outside blocks is not looked at.
func DialogueBlocks(src string) ([]DialogueBlock, []text.Diagnostic) {
	var blocks []DialogueBlock
	var diags []text.Diagnostic
	diag := func(code string, sev text.Severity, sp text.Span) {
		diags = append(diags, text.Diagnostic{Code: code, Severity: sev, Span: sp, Params: map[string]string{}})
	}
	var cur *DialogueBlock
	last := 0 // the end of the current block's last line
	finish := func(end int) {
		cur.Span.End = max(end, cur.Open.End)
		if len(cur.Lines) == 0 || !hasWords(src, cur.Lines) {
			diag(CodeEmptyDialogue, text.Info, cur.Open)
		}
		blocks = append(blocks, *cur)
		cur = nil
	}
	add := func(a, b int) {
		if l, ok := blockLine(src, a, b); ok {
			cur.Lines = append(cur.Lines, l)
			last = l.Line.End
		}
	}
	for start := 0; start <= len(src); {
		end := strings.IndexByte(src[start:], '\n')
		next := 0
		if end < 0 {
			end, next = len(src), len(src)+1
		} else {
			end += start
			next = end + 1
		}
		body := strings.TrimRight(src[start:end], "\r")
		stop := start + len(body)
		if text.IsHeading(body) {
			if cur != nil {
				diag(CodeDialogueHeading, text.Warning, cur.Open)
				finish(last)
			}
			start = next
			continue
		}
		pos := start
		for _, m := range markers(body) {
			ms := text.Span{Start: start + m.span.Start, End: start + m.span.End}
			if cur != nil {
				add(pos, ms.Start)
			}
			switch {
			case m.open && cur != nil:
				diag(CodeNestedDialogue, text.Warning, ms)
			case m.open:
				cur = &DialogueBlock{Open: ms, Span: text.Span{Start: ms.Start}}
				last = ms.End
			case cur == nil:
				diag(CodeStrayDialogueClose, text.Warning, ms)
			default:
				cur.Close = ms
				finish(ms.End)
			}
			pos = ms.End
		}
		if cur != nil {
			add(pos, stop)
		}
		start = next
	}
	if cur != nil {
		diag(CodeUnclosedDialogue, text.Warning, cur.Open)
		finish(last)
	}
	return blocks, diags
}

func hasWords(src string, lines []DialogueLine) bool {
	for _, l := range lines {
		if (l.Kind == DialogueTurn || l.Kind == DialogueNarration) && wordy(l.Text.Of(src)) {
			return true
		}
	}
	return false
}

func wordy(s string) bool {
	return strings.IndexFunc(stripBrackets(s), func(r rune) bool { return text.IsWordRune(r) }) >= 0
}

// stripBrackets drops [tags] (and [[word|say]] keeps its word).
func stripBrackets(s string) string {
	var b strings.Builder
	prev := 0
	for _, sp := range text.Tags(s) {
		b.WriteString(s[prev:sp.Start])
		if raw := sp.Of(s); strings.HasPrefix(raw, "[[") {
			if w, _, ok := text.SplitOverride(raw); ok {
				b.WriteString(w)
			}
		}
		prev = sp.End
	}
	b.WriteString(s[prev:])
	return b.String()
}

// blockLine reads src[a:b], a line (or the part of one) inside a block.
func blockLine(src string, a, b int) (DialogueLine, bool) {
	sp := text.Span{Start: a, End: b}.TrimSpace(src)
	if sp.Len() == 0 {
		return DialogueLine{}, false
	}
	s := sp.Of(src)
	l := DialogueLine{Line: sp, Kind: DialogueNarration, Text: sp}
	at := func(i, j int) text.Span { return text.Span{Start: sp.Start + i, End: sp.Start + j} }
	switch {
	case pauseOnly(s) >= 0:
		l.Kind, l.Millis, l.Text = DialoguePause, pauseOnly(s), text.Span{}
		return l, true
	case parenthesised(s):
		l.Kind, l.Text = DialogueDirection, text.Span{}
		return l, true
	}
	// [voice:NAME]text: NAME's turn.
	if tags := text.Tags(s); len(tags) > 0 && tags[0].Start == 0 {
		inner := strings.TrimSpace(s[1 : tags[0].End-1])
		if low := strings.ToLower(inner); strings.HasPrefix(low, "voice:") || low == "voice" {
			name := ""
			if i := strings.IndexByte(inner, ':'); i >= 0 {
				name = strings.TrimSpace(inner[i+1:])
			}
			body := at(tags[0].End, len(s)).TrimSpace(src)
			l.Text = body
			if name != "" && !strings.EqualFold(name, "default") {
				l.Kind, l.Name = DialogueTurn, name
				l.NameSpan = at(0, tags[0].End)
				l.Text = unquote(src, body)
			}
			return l, true
		}
	}
	// "？：…": a turn nobody was found for.
	for _, q := range []string{"？：", "?:", "？:", "?："} {
		if strings.HasPrefix(s, q) {
			l.Kind, l.Unknown = DialogueTurn, true
			l.NameSpan = at(0, len(q)-len(strings.TrimLeft(q, "?？")))
			l.Text = unquote(src, at(len(q), len(s)).TrimSpace(src))
			return l, true
		}
	}
	name, dir, body, ok := speaker(s)
	empty := false
	if !ok {
		// "伊内丝：" (the template, not filled in yet): a turn without words.
		if n, d, bd, ok2 := speaker(s + "x"); ok2 && bd == len(s) {
			name, dir, body, ok, empty = n, d, bd, true, true
		}
	}
	if !ok {
		return l, true
	}
	words := at(body, len(s)).TrimSpace(src)
	if empty {
		words = at(len(s), len(s))
	}
	if name == "" { // the narrator's line
		l.Text = words
		return l, true
	}
	l.Kind, l.Name, l.Direction = DialogueTurn, name, dir
	if i := strings.IndexAny(s, ":："); i >= 0 {
		head := s[:i]
		l.NameSpan = at(0, len(strings.TrimRightFunc(head, unicode.IsSpace)))
		if p := strings.IndexAny(head, "(（"); p >= 0 {
			l.NameSpan = at(0, len(strings.TrimRightFunc(head[:p], unicode.IsSpace)))
			l.DirSpan = at(p, len(strings.TrimRightFunc(head, unicode.IsSpace)))
		}
	}
	if dir != "" && l.DirSpan.Len() == 0 {
		// "名字：（低声）台词": the direction leads the words.
		if p := strings.LastIndexAny(s[:body], ")）"); p >= 0 {
			if q := strings.LastIndexAny(s[:p], "(（"); q >= 0 {
				_, n := utf8.DecodeRuneInString(s[p:])
				l.DirSpan = at(q, p+n)
			}
		}
	}
	l.Text = unquote(src, words)
	return l, true
}

// speaker reads a block line's speaker as autocast does, and also a name
// in lower case ("ines: …"): in a block, a name and a colon start a turn.
func speaker(s string) (name, dir string, body int, ok bool) {
	if name, dir, body, ok = autocast.Speaker(s); ok {
		return
	}
	i := strings.IndexAny(s, ":：")
	if i <= 0 || unicode.IsSpace(rune(s[i-1])) || utf8.RuneCountInString(s[:i]) > 16 || len(strings.Fields(s[:i])) > 2 {
		return "", "", 0, false
	}
	head := []rune(s[:i])
	for k := range head {
		if k == 0 || unicode.IsSpace(head[k-1]) {
			head[k] = unicode.ToUpper(head[k])
		}
	}
	if len(string(head)) != i { // a letter whose capital is longer
		return "", "", 0, false
	}
	if _, dir, body, ok = autocast.Speaker(string(head) + s[i:]); !ok {
		return "", "", 0, false
	}
	name = strings.TrimSpace(s[:i])
	if p := strings.IndexAny(name, "(（"); p > 0 {
		name = strings.TrimSpace(name[:p])
	}
	return name, dir, body, true
}

// pauseOnly is the length of a line of [pause] tags only, else -1.
func pauseOnly(s string) int {
	d := Parse(s, &Options{NoChapters: true})
	ms := 0
	for _, t := range d.Tokens {
		switch {
		case t.Kind == Pause:
			ms += t.Millis
		case t.Kind == Text && strings.TrimSpace(t.Text) == "":
		default:
			return -1
		}
	}
	if len(d.Tokens) == 0 {
		return -1
	}
	return ms
}

// parenthesised reports whether a whole line is in parentheses.
func parenthesised(s string) bool {
	if !strings.HasPrefix(s, "(") && !strings.HasPrefix(s, "（") {
		return false
	}
	depth := 0
	for i, r := range s {
		switch r {
		case '(', '（':
			depth++
		case ')', '）':
			depth--
			if depth == 0 {
				return i+utf8.RuneLen(r) == len(s) // the first paren closes at the end
			}
		}
	}
	return false
}

// quotePairs are the quotes a turn's words may stand in.
var quotePairs = map[string]string{"“": "”", "\"": "\"", "「": "」", "『": "』", "‘": "’", "«": "»", "'": "'"}

// unquote leaves out a pair of quotes around all of sp.
func unquote(src string, sp text.Span) text.Span {
	s := sp.Of(src)
	for o, c := range quotePairs {
		if len(s) > len(o)+len(c) && strings.HasPrefix(s, o) && strings.HasSuffix(s, c) &&
			!strings.Contains(s[len(o):len(s)-len(c)], c) {
			return text.Span{Start: sp.Start + len(o), End: sp.End - len(c)}.TrimSpace(src)
		}
	}
	return sp
}

// Marker is a dialogue marker in a line (Markers).
type Marker struct {
	Span text.Span
	Open bool
}

// Markers are the dialogue markers of one line, in order (for editors).
func Markers(line string) []Marker {
	ms := markers(line)
	out := make([]Marker, len(ms))
	for i, m := range ms {
		out[i] = Marker{m.span, m.open}
	}
	return out
}

// DialogueLineOf reads s as a line inside a block (spans into s); ok is
// false for a blank one.
func DialogueLineOf(s string) (DialogueLine, bool) { return blockLine(s, 0, len(s)) }
