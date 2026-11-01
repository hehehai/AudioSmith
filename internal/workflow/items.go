package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Item is one input of a run: a script, or an audio clip. The workflow
// runs once per item.
type Item struct {
	// Key identifies the item by its content: an edited script is a new
	// item, a script moved in the list is the same one.
	Key  string `json:"key"`
	Name string `json:"name"`
	Text string `json:"text,omitempty"` // a script
	// Path, Size: a clip.
	Path string `json:"path,omitempty"`
	Size int64  `json:"size,omitempty"`
	// Source is the script or clip it comes from (0-based), Part its
	// part when the script holds several ("---").
	Source int `json:"source"`
	Part   int `json:"part,omitempty"`
}

var separator = regexp.MustCompile(`(?m)^[ \t]*---+[ \t]*$`)

// SplitScript cuts a script at lines of "---" into its parts, trimmed,
// without empty ones.
func SplitScript(s string) []string {
	var out []string
	for _, p := range separator.Split(s, -1) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Items are the items of the workflow's input step, in order (nil
// without one). Duplicates of a content get distinct keys by occurrence.
func (w *Workflow) Items() []Item {
	in := w.InputNode()
	if in == nil {
		return nil
	}
	var out []Item
	seen := map[string]int{}
	add := func(it Item, content string) {
		sum := sha256.Sum256([]byte(content))
		k := hex.EncodeToString(sum[:8])
		seen[k]++
		if n := seen[k]; n > 1 {
			k = fmt.Sprintf("%s-%d", k, n)
		}
		it.Key = k
		out = append(out, it)
	}
	switch in.Kind {
	case KindStart:
		for i, s := range in.P.Scripts {
			parts := SplitScript(s.Text)
			for j, p := range parts {
				name := s.Name
				if name == "" || len(parts) > 1 {
					name = Excerpt(p, 40)
				}
				add(Item{Name: name, Text: p, Source: i, Part: j}, "t\x00"+p)
			}
		}
	case KindSource:
		for i, c := range in.P.Clips {
			add(Item{Name: c.Name, Path: c.Path, Size: c.Size, Source: i}, fmt.Sprintf("a\x00%s\x00%d\x00%d", c.Path, c.Size, c.Mod.UnixNano()))
		}
	}
	return out
}

// Excerpt is the start of a text on one line, at most n runes (with "…").
func Excerpt(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
