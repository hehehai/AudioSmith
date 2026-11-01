package voicedesign

// Matching description pieces to values for Parse: intensifiers, English
// and Chinese phrases, and ages; split from parse.go.

import (
	"slices"
	"strconv"
	"strings"
)

// intensify upgrades pitch and pace one step outwards.
func intensify(as []assign) ([]assign, bool) {
	up := map[string]string{"low": "very_low", "high": "very_high", "slow": "very_slow", "fast": "very_fast"}
	out := slices.Clone(as)
	changed := false
	for i, a := range out {
		if (a.cat == Pitch || a.cat == Pace) && up[a.val] != "" {
			out[i].val = up[a.val]
			changed = true
		}
	}
	return out, changed
}

func matchPieces(toks []token) []piece {
	l := lex()
	var out []piece
	for i := 0; i < len(toks); {
		t := toks[i]
		if t.punct {
			out = append(out, piece{from: i, to: i + 1, punct: true})
			i++
			continue
		}
		if p, ok := matchAge(toks, i); ok {
			out = append(out, p)
			i = p.to
			continue
		}
		if t.cjk {
			if r, n := matchZH(l, toks, i); r != nil {
				out = append(out, piece{from: i, to: i + n, kind: r.kind, assigns: r.assigns})
				i += n
				continue
			}
		} else if r, n := matchEN(l, toks, i); r != nil {
			out = append(out, piece{from: i, to: i + n, kind: r.kind, assigns: r.assigns})
			i += n
			continue
		}
		out = append(out, piece{from: i, to: i + 1, unmapped: true})
		i++
	}
	return out
}

func matchEN(l *lexicon, toks []token, i int) (*rule, int) {
	try := func(r *rule) int {
		if i+len(r.words) > len(toks) {
			return 0
		}
		for k, w := range r.words {
			t := toks[i+k]
			if t.cjk || t.punct {
				return 0
			}
			last := k == len(r.words)-1
			if last && r.prefix {
				if !strings.HasPrefix(t.text, w) {
					return 0
				}
			} else if t.text != w {
				return 0
			}
		}
		return len(r.words)
	}
	var best *rule
	bestN := 0
	for _, r := range l.en[toks[i].text] {
		if n := try(r); n > bestN || (n == bestN && n > 0 && best.prefix && !r.prefix) {
			best, bestN = r, n
		}
	}
	if bestN == 0 {
		// whole-word prefix rules ("whisper*"); exact words win above
		for _, r := range l.enPrefix {
			if try(r) == 1 {
				return r, 1
			}
		}
	}
	return best, bestN
}

func matchZH(l *lexicon, toks []token, i int) (*rule, int) {
	first := []rune(toks[i].text)[0]
	for _, r := range l.zh[first] {
		ph := r.words[0]
		n := 0
		ok := true
		for _, want := range ph {
			if i+n >= len(toks) || !toks[i+n].cjk || []rune(toks[i+n].text)[0] != want ||
				(n > 0 && toks[i+n].start != toks[i+n-1].end) {
				ok = false
				break
			}
			n++
		}
		if ok {
			return r, n
		}
	}
	return nil, 0
}

// matchAge reads ages written as numbers: "40-year-old", "in her 40s",
// "aged 70", "35岁", "四十多岁".
func matchAge(toks []token, i int) (piece, bool) {
	num, rest, n := -1, "", 0
	if t := toks[i]; !t.cjk && !t.punct {
		num, rest = leadingInt(t.text)
		n = 1
	} else if t.cjk {
		num, n = zhNumber(toks, i)
	}
	if num < 0 || n == 0 {
		return piece{}, false
	}
	j := i + n // next token
	// Chinese suffix: [多|来|几]? 岁 (directly attached)
	if rest == "" {
		k := j
		if k < len(toks) && toks[k].cjk && toks[k].start == toks[k-1].end && strings.Contains("多来几", toks[k].text) {
			k++
		}
		if k < len(toks) && toks[k].cjk && toks[k].text == "岁" && toks[k].start == toks[k-1].end {
			return agePiece(i, k+1, num), true
		}
	}
	if toks[i].cjk {
		return piece{}, false
	}
	switch {
	case rest == "s" && num >= 10 && num%10 == 0 && num < 100: // "40s"
		return agePiece(i, j, num), true
	case rest == "yo":
		return agePiece(i, j, num), true
	case rest != "":
		return piece{}, false
	}
	if j+1 < len(toks) && (toks[j].text == "year" || toks[j].text == "years" || toks[j].text == "yr") && toks[j+1].text == "old" {
		return agePiece(i, j+2, num), true
	}
	if j < len(toks) && toks[j].text == "yo" {
		return agePiece(i, j+1, num), true
	}
	if i > 0 && (toks[i-1].text == "aged" || toks[i-1].text == "age") {
		return agePiece(i, j, num), true
	}
	return piece{}, false
}

// zhNumber reads a Chinese numeral below 100 ("七", "十五", "四十", "两")
// from consecutive CJK tokens.
func zhNumber(toks []token, i int) (int, int) {
	total, cur, n := 0, 0, 0
	for i+n < len(toks) && toks[i+n].cjk && (n == 0 || toks[i+n].start == toks[i+n-1].end) {
		r := []rune(toks[i+n].text)[0]
		if r == '十' {
			if cur == 0 {
				cur = 1
			}
			total += cur * 10
			cur = 0
		} else if d := strings.IndexRune("零一二三四五六七八九", r); d >= 0 {
			cur = len([]rune("零一二三四五六七八九"[:d]))
		} else if r == '两' {
			cur = 2
		} else {
			break
		}
		n++
	}
	if n == 0 {
		return -1, 0
	}
	return total + cur, n
}

func leadingInt(s string) (int, string) {
	j := 0
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j == 0 || j > 3 {
		return -1, s
	}
	v, _ := strconv.Atoi(s[:j])
	return v, s[j:]
}

func agePiece(from, to, years int) piece {
	return piece{from: from, to: to, kind: kindMap, assigns: []assign{{cat: Age, val: AgeForYears(years)}}}
}

// AgeForYears maps an age in years to the Age value.
func AgeForYears(y int) string {
	switch {
	case y < 13:
		return "child"
	case y < 20:
		return "teen"
	case y < 40:
		return "young_adult"
	case y < 60:
		return "middle_aged"
	}
	return "elderly"
}
