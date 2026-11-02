package services

import "testing"

func TestLongformSpecFirstWords(t *testing.T) {
	line := func(s ...string) LineSpec {
		var l LineSpec
		for _, x := range s {
			l.Spans = append(l.Spans, SpanSpec{Text: x})
		}
		return l
	}
	for _, c := range []struct {
		name string
		spec LongformSpec
		want string
	}{
		{"short", LongformSpec{Chapters: []ChapterSpec{{Lines: []LineSpec{line("Hello there.")}}}}, "Hello there."},
		{"spaced: at a word", LongformSpec{Chapters: []ChapterSpec{{Lines: []LineSpec{line("[pause 300ms] The lighthouse keeper counted the ships.")}}}}, "The lighthouse…"},
		{"unspaced", LongformSpec{Chapters: []ChapterSpec{{Lines: []LineSpec{line("林间小路上，一只狐狸正在追逐月光下的萤火虫，它跑得飞快。")}}}}, "林间小路上，一只狐狸正在追逐月光下的萤火…"},
		{"override keeps the word", LongformSpec{Chapters: []ChapterSpec{{Lines: []LineSpec{line("[[GIF|jif]] files"), line("load.")}}}}, "GIF files load."},
		{"lines across chapters", LongformSpec{Chapters: []ChapterSpec{{Title: "One"}, {Lines: []LineSpec{line("  A  ", "B")}}}}, "A B"},
		{"a title only", LongformSpec{Chapters: []ChapterSpec{{Title: "Chapter One", Lines: []LineSpec{line("[pause 1s]")}}}}, "Chapter One"},
		{"nothing", LongformSpec{}, ""},
	} {
		if got := c.spec.FirstWords(20); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
