package translate

// The AI director of stories: what it reads (StoryScript) and the
// helpers its emotion pass (story_emotions.go) shares. It routes as the
// direction_parse skill ("Direct a line" in Tools), so it uses the
// writing-help model the user chose, by default the local Qwen3 4B.

import (
	"strings"
	"time"
	"unicode"

	"github.com/hehehai/AudioSmith/internal/llm"
)

// StoryScript is what the director reads: the story with what is already
// set (kept unless the director has better).
type StoryScript struct {
	Title string
	Scene string
	Cast  []StoryRole
	Lines []StoryScriptLine
}

// StoryRole is a character and its persona; Voice describes the voice it
// is cast with ("bright young female voice"), so the persona fits it
// (read, not written, by the director).
type StoryRole struct {
	Name    string `json:"name"`
	Persona string `json:"persona"`
	Voice   string `json:"-"`
}

// StoryScriptLine is one spoken line: who ("" for the narrator) and what.
type StoryScriptLine struct {
	Speaker string
	Text    string
	// Direction is the line's direction so far; the emotion director does
	// not read it.
	Direction string
	// The line's emotion so far (DirectEmotions): Fixed when the user set
	// it, so the director keeps it.
	Emotion   string
	Intensity int
	Fixed     bool
}

// StoryDirector directs stories through the direction_parse skill.
type StoryDirector struct {
	Router *llm.Router
	// Budget bounds one batch of lines (0: 3 minutes: a local model
	// loads first).
	Budget time.Duration
}

// storyBatch is how many lines one call directs; longer stories go in
// batches that carry the scene, the cast and the lines just before.
const storyBatch = 40

// Limits of the scene the director writes: runes of Chinese (or
// Japanese) text, characters of Latin text. A word is about 1.5 Chinese
// characters but 6 Latin characters, so one limit for both cut English
// mid-word ("skeptical, testing the e", thread #44).
const (
	storySceneMax      = 60
	storySceneMaxLatin = 140
)

type storyData struct {
	Zh      bool
	Title   string
	Scene   string
	Cast    []StoryRole
	Before  []storyPromptLine // context: the lines just before the batch
	Lines   []storyPromptLine
	First   int
	Last    int
	Batched bool
}

type storyPromptLine struct {
	N         int
	Speaker   string
	Text      string
	Emotion   string
	Intensity int
	Fixed     bool
}

// Available reports whether a language model is set up for the director.
func (d *StoryDirector) Available() bool { return d.Router.Available(llm.SkillDirectionParse) }

func promptLine(i int, l StoryScriptLine) storyPromptLine {
	who := strings.TrimSpace(l.Speaker)
	return storyPromptLine{N: i + 1, Speaker: who, Text: clipRunes(strings.Join(strings.Fields(l.Text), " "), 600),
		Emotion: strings.TrimSpace(l.Emotion), Intensity: l.Intensity, Fixed: l.Fixed}
}

// castNames are the characters who speak, in order.
func castNames(s StoryScript) []string {
	var out []string
	seen := map[string]bool{}
	add := func(n string) {
		n = strings.TrimSpace(n)
		if n != "" && !seen[strings.ToLower(n)] {
			seen[strings.ToLower(n)] = true
			out = append(out, n)
		}
	}
	for _, c := range s.Cast {
		add(c.Name)
	}
	for _, l := range s.Lines {
		add(l.Speaker)
	}
	return out
}

// mergeCast is a role per character of names, with the persona and
// voice set in had.
func mergeCast(had []StoryRole, names []string) []StoryRole {
	byName := map[string]string{}
	voice := map[string]string{}
	for _, r := range had {
		byName[strings.ToLower(strings.TrimSpace(r.Name))] = strings.TrimSpace(r.Persona)
		voice[strings.ToLower(strings.TrimSpace(r.Name))] = strings.TrimSpace(r.Voice)
	}
	var out []StoryRole
	for _, n := range names {
		out = append(out, StoryRole{Name: n, Persona: byName[strings.ToLower(n)], Voice: voice[strings.ToLower(n)]})
	}
	return out
}

// cleanDirection drops brackets and quotes a model may wrap a direction in.
func cleanDirection(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "()（）[]【】\"'“”‘’")
	switch strings.ToLower(s) {
	case "none", "无", "-", "—", "n/a":
		return ""
	}
	return strings.TrimSpace(s)
}

// clipScript shortens s to cjk runes when it is mostly Chinese or
// Japanese, else to latin characters, keeping whole phrases: it cuts at
// the last clause break (comma, semicolon…) that fits, else (Latin) at
// the last space, and never leaves trailing punctuation.
func clipScript(s string, cjk, latin int) string {
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	n := latin
	if cjkShare(s) >= 0.3 {
		n = cjk
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := -1
	for i := n; i > n/3; i-- { // a clause break inside the limit
		if strings.ContainsRune(",;:，；：、。.!?！？", r[i]) {
			cut = i
			break
		}
	}
	if cut < 0 && n == latin {
		for i := n; i > n/3; i-- { // a word break
			if r[i] == ' ' {
				cut = i
				break
			}
		}
	}
	if cut < 0 {
		cut = n
	}
	return strings.TrimRightFunc(string(r[:cut]), func(c rune) bool {
		return unicode.IsSpace(c) || strings.ContainsRune(",;:，；：、-—–", c)
	})
}
