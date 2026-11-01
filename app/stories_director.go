package main

// Stories → AI director: the writing-help language model (by default the
// local Qwen3 4B) reads the story and proposes its scene, a persona per
// character and a direction per line (translate.StoryDirector); the page
// fills them in, where the user edits them. The API's update_longform
// (direct: fill | all) runs it too.

import (
	"context"
	"errors"
	"strings"
	"unicode"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/llm"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/text"
	"github.com/hehehai/AudioSmith/internal/translate"
	"github.com/hehehai/AudioSmith/internal/voicedesign"
)

// storyScript is a story as the director reads it: its spoken lines, and
// its characters with the voices they are cast with (among voices).
func storyScript(d *services.LongformDoc, voices []core.Voice) translate.StoryScript {
	s := translate.StoryScript{Title: d.Title, Scene: d.Scene}
	names := map[string]string{}
	zh := strings.HasPrefix(d.Language, "zh") || d.Language == "" && i18n.Locale() == "zh-Hans"
	for _, m := range d.Cast {
		names[m.ID] = m.Name
		ref := m.Voice
		if ref == "" {
			ref = d.Voice
		}
		s.Cast = append(s.Cast, translate.StoryRole{Name: m.Name, Persona: m.Persona, Voice: voiceHint(ref, voices, zh)})
	}
	for _, l := range d.Lines {
		if l.Chapter || strings.TrimSpace(l.Text) == "" {
			continue
		}
		s.Lines = append(s.Lines, translate.StoryScriptLine{Speaker: names[l.Character], Text: l.Text, Direction: l.Direction,
			Emotion: l.Emotion, Intensity: l.Intensity, Fixed: l.EmotionByUser && l.Emotion != ""})
	}
	return s
}

// voiceHint describes a voice reference for the director: a speaker's
// description, a designed voice's, else the voice's name.
func voiceHint(ref string, voices []core.Voice, zh bool) string {
	pick := func(t voicedesign.Text) string {
		if zh && t.Zh != "" {
			return t.Zh
		}
		return t.En
	}
	if id, ok := strings.CutPrefix(ref, services.SpeakerRef); ok {
		for _, sp := range voicedesign.CustomVoiceSpeakers {
			if strings.EqualFold(sp.ID, id) {
				return pick(sp.About)
			}
		}
		return id
	}
	for _, v := range voices {
		if v.ID == ref {
			if strings.TrimSpace(v.Design) != "" {
				return v.Design
			}
			return v.Name
		}
	}
	return ""
}

// storyZh says whether a story's lines are mostly Chinese (or
// Japanese): the director then writes in Chinese.
func storyZh(d *services.LongformDoc) bool {
	cjk, all := 0, 0
	for _, l := range d.Lines {
		for _, r := range l.Text {
			if unicode.IsLetter(r) {
				all++
				if text.IsCJK(r) {
					cjk++
				}
			}
		}
	}
	return all > 0 && float64(cjk) >= 0.3*float64(all)
}

// emotionVocab is the emotion director's vocabulary, its words in
// Chinese (zh) or English.
func emotionVocab(zh bool) []translate.EmotionWord {
	out := make([]translate.EmotionWord, len(services.Emotions))
	for i, k := range services.Emotions {
		out[i] = translate.EmotionWord{Key: k, Word: services.EmotionWord(k, zh)}
	}
	return out
}

// applyEmotions writes the emotion director's curve into d: the scene
// when empty (fill) or always, each character's baseline, and each line's
// emotion, intensity, turn and note, but for the lines the user set
// (EmotionByUser) and, when fill, the lines already directed; a line it
// directs drops a direction of the first director. The story's emotions
// are then smoothed (services.SmoothEmotions) chapter by chapter, the
// user's lines and those kept leading. It returns how many lines it
// directed.
func applyEmotions(d *services.LongformDoc, em translate.StoryEmotions, fill bool) int {
	if em.Scene != "" && (!fill || strings.TrimSpace(d.Scene) == "") {
		d.Scene = em.Scene
	}
	for _, b := range em.Baselines {
		for i := range d.Cast {
			m := &d.Cast[i]
			if strings.EqualFold(strings.TrimSpace(m.Name), b.Name) && (!fill || m.Baseline == "") {
				if e := services.NormEmotion(b.Baseline); e != "" {
					m.Baseline = e
				}
			}
		}
	}
	var spoken []int
	for i, l := range d.Lines {
		if !l.Chapter && strings.TrimSpace(l.Text) != "" {
			spoken = append(spoken, i)
		}
	}
	set := map[int]bool{}
	for _, le := range em.Lines {
		if le.N < 1 || le.N > len(spoken) {
			continue
		}
		i := spoken[le.N-1]
		l := &d.Lines[i]
		if l.EmotionByUser || fill && (l.Emotion != "" || strings.TrimSpace(l.Direction) != "") {
			continue
		}
		l.Emotion, l.Intensity, l.Turn, l.Note, l.Direction = services.NormEmotion(le.Emotion), le.Intensity, le.Turn, le.Note, ""
		if l.Emotion == "" {
			l.Emotion = services.EmotionSteady
		}
		set[i] = true
	}
	smoothEmotions(d, set)
	return len(set)
}

// smoothEmotions smooths the emotions of d's lines, chapter by chapter;
// only the lines in free change.
func smoothEmotions(d *services.LongformDoc, free map[int]bool) {
	var idx []int
	flush := func() {
		beats := make([]services.EmotionBeat, len(idx))
		for k, i := range idx {
			l := d.Lines[i]
			beats[k] = services.EmotionBeat{Who: l.Character, Emotion: l.Emotion, Intensity: l.Intensity, Turn: l.Turn, Fixed: !free[i]}
		}
		for k, b := range services.SmoothEmotions(beats) {
			if l := &d.Lines[idx[k]]; free[idx[k]] {
				l.Emotion, l.Intensity, l.Turn = b.Emotion, b.Intensity, b.Turn
			}
		}
		idx = idx[:0]
	}
	for i, l := range d.Lines {
		if l.Chapter {
			flush()
			continue
		}
		if l.Emotion != "" {
			idx = append(idx, i)
		}
	}
	flush()
}

// storyDirected says whether a story has any scene, persona, direction or
// emotion.
func storyDirected(d *services.LongformDoc) bool {
	if strings.TrimSpace(d.Scene) != "" {
		return true
	}
	for _, m := range d.Cast {
		if strings.TrimSpace(m.Persona) != "" || m.Baseline != "" {
			return true
		}
	}
	for _, l := range d.Lines {
		if strings.TrimSpace(l.Direction) != "" || l.Emotion != "" {
			return true
		}
	}
	return false
}

// noDirectorText says the director has no language model.
func noDirectorText() string {
	return i18n.T("The AI director needs a language model: download one in Settings → Writing help (%s runs on this Mac).", modelRefID(catalog.DefaultLLM))
}

// directorError is the page's message for a director that failed.
func directorError(err error) string {
	switch c := translate.Code(err); {
	case c == translate.CodeNoLLM || c == llm.CodeNoRoute:
		return noDirectorText()
	case c == llm.CodeTimeout || errors.Is(err, context.DeadlineExceeded):
		return i18n.T("The language model took too long. Try again, or direct a shorter story.")
	case c == llm.CodeInvalidJSON || c == llm.CodeSchema:
		return i18n.T("The language model's answer could not be read. Try again.")
	}
	return i18n.T("The AI director failed: %v", err)
}

// directStory runs the AI director over a page's story in the background
// and fills in what it proposes (all: replacing what is set), with Undo.
func (a *app) directStory(p *lfPage, all bool) {
	if p.directing {
		return
	}
	if len(storyScript(&p.doc, nil).Lines) == 0 {
		a.toast(i18n.T("Nothing to direct: add lines first."))
		return
	}
	if !a.llmCan(llm.SkillDirectionParse) {
		a.toast(noDirectorText())
		return
	}
	script := storyScript(&p.doc, a.voices)
	vocab := emotionVocab(storyZh(&p.doc))
	router := a.llmRouter
	p.directing = true
	a.background(func() {
		em, err := (&translate.StoryDirector{Router: router}).DirectEmotions(context.Background(), script, vocab)
		a.post(func() {
			p.directing = false
			if err != nil {
				a.toastWarn(directorError(err))
				return
			}
			before := p.doc
			before.Lines = append([]services.StoryLine(nil), p.doc.Lines...)
			before.Cast = append([]services.CastMember(nil), p.doc.Cast...)
			n := applyEmotions(&p.doc, em, !all)
			p.open["scene"] = true
			a.toastUndo(i18n.N(n, "The AI director directed %d line", "The AI director directed %d lines", n), func() {
				p.doc.Scene, p.doc.Lines, p.doc.Cast = before.Scene, before.Lines, before.Cast
			})
		})
	})
}
