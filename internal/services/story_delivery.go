package services

// Delivery of a story's lines (story_pacing.go): the instruction made of
// a character's persona, the scene and the line's direction
// (StoryInstruct, withDelivery, TakesInstruct), and what a model does
// with them (ContinuityOf); split from story_pacing.go.

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/text"
)

// Instruction limits (runes): each part is cut to its limit, so the
// instruction stays short (the model reads it with every line).
const (
	instructPersona   = 32
	instructScene     = 48
	instructDirection = 32
	instructLatin     = 3 // Latin text: limits × this (in characters)
)

// StoryInstruct is a story line's delivery instruction for models that
// take one: the character's persona, the scene, and the line's direction
// with its intensity, in Chinese when the line is (else English). "" when
// all three are empty.
func StoryInstruct(lineText, scene, persona, direction string, intensity int) string {
	zh := cjkShare(lineText) >= 0.3
	lim := func(s string, n int) string {
		s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
		s = strings.TrimRightFunc(s, func(r rune) bool { return strings.ContainsRune("。.，,；;：:！!", r) || unicode.IsSpace(r) })
		if !zh {
			n *= instructLatin
		}
		if utf8.RuneCountInString(s) > n {
			s = strings.TrimSpace(string([]rune(s)[:n])) + "…"
		}
		return s
	}
	persona, scene, direction = lim(persona, instructPersona), lim(scene, instructScene), lim(direction, instructDirection)
	var parts []string
	if zh {
		if persona != "" {
			parts = append(parts, persona+"。")
		}
		if scene != "" {
			parts = append(parts, "场景："+scene+"。")
		}
		if direction != "" {
			d := "这一句：" + direction
			if turnLines(direction) {
				d = "这几句：" + direction
			}
			switch intensity {
			case IntensityLight:
				d += "，情绪克制"
			case IntensityStrong:
				d += "，情绪饱满强烈"
			}
			parts = append(parts, d+"。")
		}
		return strings.Join(parts, "")
	}
	if persona != "" {
		parts = append(parts, upperFirst(persona)+".")
	}
	if scene != "" {
		parts = append(parts, "Scene: "+scene+".")
	}
	if direction != "" {
		d := "This line: " + direction
		if turnLines(direction) {
			d = "These lines: " + direction
		}
		switch intensity {
		case IntensityLight:
			d += ", restrained"
		case IntensityStrong:
			d += ", with strong emotion"
		}
		parts = append(parts, d+".")
	}
	return strings.Join(parts, " ")
}

// withDelivery puts a line's delivery into its recipe: the instruction
// into the voice field of models that take one (after the speaker of
// Qwen3 CustomVoice, after the description of a designed voice: the
// engine's own instruct field is dropped when a voice is set). A clone
// (reference clip or voice prompt) takes no instruction, except on models
// whose clones do (CloneInstruct: VoxCPM2).
func withDelivery(t *core.Take, info ModelInfo, instruct string) {
	if !info.Instruct || instruct == "" || (t.RefAudio != "" || t.VoicePrompt != "") && !info.CloneInstruct {
		return
	}
	switch {
	case t.Voice == "":
		t.Voice = instruct
	case strings.ContainsAny(t.Voice, ",，"):
		// "Speaker, instruction" or a description already: goes on.
		t.Voice = strings.TrimRight(t.Voice, " ") + " " + instruct
	default:
		t.Voice += ", " + instruct
	}
}

// Continuity levels of a model for stories (ContinuityOf).
const (
	ContinuityInstruct  = "instruct"  // takes the scene, personas and directions
	ContinuityIntensity = "intensity" // takes the emotions' intensities only (Chatterbox)
	ContinuityLines     = "lines"     // pacing and merged turns only
)

// ContinuityOf is what a model does with a story's scene, personas and
// directions.
func ContinuityOf(info Traits) string {
	switch {
	case info.Instruct:
		return ContinuityInstruct
	case info.Exaggeration:
		return ContinuityIntensity
	}
	return ContinuityLines
}

// TakesInstruct reports whether a model with capabilities caps takes a
// delivery instruction: "instruct" (Qwen3-TTS CustomVoice and VoiceDesign,
// as the engine reports them), or a free-form voice field on a model that
// does not clone (an entry that only says "voiceStyle"). OmniVoice's
// voice field is a closed list of design attributes, not a delivery.
func TakesInstruct(caps []string) bool {
	return slices.Contains(caps, "instruct") || slices.Contains(caps, "voiceStyle") && !slices.Contains(caps, "clone")
}

// CloneTakesInstruct reports whether a model's clones take a delivery
// instruction as well: one that both clones and takes "instruct"
// (VoxCPM2's "(instruction)" before the text, thread #165). Qwen3-TTS's
// instruct models don't clone, and its Base models take no instruction.
func CloneTakesInstruct(caps []string) bool {
	return slices.Contains(caps, "instruct") && slices.Contains(caps, "clone")
}

// cjkShare is the share of a text's letters in CJK scripts.
func cjkShare(s string) float64 {
	cjk, all := 0, 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			all++
			if text.IsCJK(r) {
				cjk++
			}
		}
	}
	if all == 0 {
		return 0
	}
	return float64(cjk) / float64(all)
}

func upperFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}
