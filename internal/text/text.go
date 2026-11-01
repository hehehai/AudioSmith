// Package text is the script pipeline shared by the Studio (M1), Stories and
// Audiobook (M3) and Dubbing (M4): segmentation into TTS chunks with source
// offsets, content-addressed chunk keys, word counts and runtime estimates.
//
// Subpackages:
//   - markup: the SSML-lite parser ([pause 500ms], [voice:NAME], # chapters…)
//   - normalize: numbers, times, dates, currency and abbreviations → words
//   - lexicon: the pronunciation dictionary and [[word|say]] overrides
//   - autocast: speaker detection for Stories
//
// Everything here is pure, allocation-light and goroutine-safe. Offsets are
// byte offsets into the source string; lengths that users see (max chars per
// chunk) count runes. Nothing here returns user-visible strings: problems are
// reported as Diagnostics (a code plus parameters) that the UI translates.
package text

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Span is a half-open byte range [Start, End) into a source string.
type Span struct {
	Start, End int
}

// Len is the span's length in bytes.
func (s Span) Len() int { return s.End - s.Start }

// Of returns the part of src the span covers.
func (s Span) Of(src string) string { return src[s.Start:s.End] }

// TrimSpace shrinks the span to exclude src's white space (including
// U+3000) at both ends.
func (s Span) TrimSpace(src string) Span {
	for s.Start < s.End {
		r, n := utf8.DecodeRuneInString(src[s.Start:s.End])
		if !unicode.IsSpace(r) {
			break
		}
		s.Start += n
	}
	for s.End > s.Start {
		r, n := utf8.DecodeLastRuneInString(src[s.Start:s.End])
		if !unicode.IsSpace(r) {
			break
		}
		s.End -= n
	}
	return s
}

// Contains reports whether byte offset i lies inside the span.
func (s Span) Contains(i int) bool { return i >= s.Start && i < s.End }

// Severity ranks a diagnostic.
type Severity uint8

const (
	Info Severity = iota
	Warning
	Error
)

func (s Severity) String() string {
	switch s {
	case Info:
		return "info"
	case Warning:
		return "warning"
	default:
		return "error"
	}
}

// Diagnostic is one finding about a script, for validation UIs. Code is a
// stable identifier ("markup.unknown_tag"); Params carry the values a
// translated message needs ({"tag": "[foo]"}). Span points into the source.
type Diagnostic struct {
	Code     string
	Severity Severity
	Span     Span
	Params   map[string]string
}

// Tags returns the bracketed spans of s in order: "[tag]" and "[[override]]".
// A tag never crosses a line break; an unmatched "[" is plain text. When a
// "[" appears before the closing "]", the scan restarts there, so in
// "[a [b]" only "[b]" is a tag.
func Tags(s string) []Span {
	var out []Span
	for i := 0; i < len(s); {
		if s[i] != '[' {
			i++
			continue
		}
		if strings.HasPrefix(s[i:], "[[") {
			if end := closeOverride(s, i); end > 0 {
				out = append(out, Span{i, end})
				i = end
				continue
			}
		}
		end := -1
		for j := i + 1; j < len(s); j++ {
			c := s[j]
			if c == '\n' || c == '\r' || c == '[' {
				break
			}
			if c == ']' {
				end = j + 1
				break
			}
		}
		if end > 0 {
			out = append(out, Span{i, end})
			i = end
			continue
		}
		i++
	}
	return out
}

// closeOverride returns the end of a "[[…]]" starting at i, or -1.
func closeOverride(s string, i int) int {
	for j := i + 2; j+1 < len(s); j++ {
		switch s[j] {
		case '\n', '\r', '[':
			return -1
		case ']':
			if s[j+1] == ']' && j > i+2 {
				return j + 2
			}
			return -1
		}
	}
	return -1
}

// Outside calls fn for each maximal piece of s outside bracket tags, in
// order, and returns the concatenation of fn's results with the tags kept
// verbatim. It is how normalisation and the lexicon skip "[…]" spans.
func Outside(s string, fn func(string) string) string {
	tags := Tags(s)
	if len(tags) == 0 {
		return fn(s)
	}
	var b strings.Builder
	b.Grow(len(s))
	prev := 0
	for _, t := range tags {
		if t.Start > prev {
			b.WriteString(fn(s[prev:t.Start]))
		}
		b.WriteString(s[t.Start:t.End])
		prev = t.End
	}
	if prev < len(s) {
		b.WriteString(fn(s[prev:]))
	}
	return b.String()
}

// RuneLen is utf8.RuneCountInString, named for readability at call sites.
func RuneLen(s string) int { return utf8.RuneCountInString(s) }
