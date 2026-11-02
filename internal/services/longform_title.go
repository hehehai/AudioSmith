package services

// The first words of a longform script: the name of a render made from
// a draft that has no title (thread #143), instead of 「未命名」.

import (
	"strings"
	"unicode/utf8"

	"github.com/hehehai/AudioSmith/internal/text"
)

// FirstWords is the start of the spec's script — its lines' words, tags
// dropped ("[[word|say]]" keeps the word) — on one line, at most n runes,
// cut with "…"; the first chapter title when no line says anything; ""
// when the spec has neither.
func (s *LongformSpec) FirstWords(n int) string {
	var b strings.Builder
	for _, ch := range s.Chapters {
		for _, l := range ch.Lines {
			for _, sp := range l.Spans {
				b.WriteString(plainScript(sp.Text))
				b.WriteByte(' ')
			}
			if utf8.RuneCountInString(b.String()) > 4*n+16 {
				return cutWords(b.String(), n)
			}
		}
	}
	if out := cutWords(b.String(), n); out != "" {
		return out
	}
	for _, ch := range s.Chapters {
		if t := cutWords(ch.Title, n); t != "" {
			return t
		}
	}
	return ""
}

// plainScript is script markup without its tags.
func plainScript(s string) string {
	tags := text.Tags(s)
	if len(tags) == 0 {
		return s
	}
	var b strings.Builder
	prev := 0
	for _, t := range tags {
		b.WriteString(s[prev:t.Start])
		tag := s[t.Start:t.End]
		if strings.HasPrefix(tag, "[[") {
			if word, _, ok := text.SplitOverride(tag); ok {
				b.WriteString(word)
			}
		}
		b.WriteByte(' ')
		prev = t.End
	}
	b.WriteString(s[prev:])
	return b.String()
}

// cutWords is s on one line, at most n runes (then "…"): at a space when
// the cut would split a word of a spaced script.
func cutWords(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := r[:n]
	if r[n] != ' ' {
		if i := strings.LastIndexByte(string(cut), ' '); i > len(string(cut))/2 {
			cut = []rune(string(cut)[:i])
		}
	}
	return strings.TrimRight(string(cut), " ,.;:，。、；：！？!?") + "…"
}
