package main

// 「✦ 推荐」 (0.5 F3, after ElevenLabs' Recommend): voices for the script,
// by rule — no language model. The script's language (chosen, else read
// from its letters) and its use (an example's, else keywords: a story,
// news, a podcast…) score the voices the picker offers; the best few
// head the picker under 推荐. When the model has no voice in the script's
// language, the row offers the model recommended for that language.

import (
	"slices"
	"strings"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/gallery"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/lang"
)

// maxRecommended is how many voices 推荐 lists.
const maxRecommended = 3

// useWords are keywords of each use case (gallery categories), lower case.
var useWords = map[string][]string{
	"narration":      {"从前", "故事", "第一章", "章", "小说", "旁白", "冥想", "呼吸", "once upon", "chapter", "story", "novel", "breathe", "meditat", "むかし", "物語"},
	"announcing":     {"新闻", "播报", "发布", "全新", "广告", "限时", "欢迎来到", "news", "introducing", "launch", "sale", "meet our", "announce", "ニュース", "新しい"},
	"conversational": {"大家好", "播客", "欢迎收听", "聊聊", "podcast", "welcome back", "hi everyone", "hey", "こんにちは"},
	"characters":     {"“", "「", "\"", "说：", "said", "shouted"},
}

// scriptUse is the use the script calls for: the example's while the
// script is still it, else the first use case whose keywords it has, else
// "".
func (a *app) scriptUse() string {
	if u := a.ttsUI.use; u != "" {
		return u
	}
	low := strings.ToLower(a.text)
	for _, u := range []string{"narration", "announcing", "conversational", "characters"} {
		for _, w := range useWords[u] {
			if strings.Contains(low, w) {
				return u
			}
		}
	}
	return ""
}

// useName names a use case in the interface's language.
func useName(id string) string {
	for _, c := range gallery.Categories() {
		if c.ID == id {
			return c.Name.In(i18n.Locale())
		}
	}
	return ""
}

// langPlain names a language (base code) in the interface's language,
// without a flag.
func langPlain(code string) string {
	if l, ok := lang.Lookup(code); ok {
		return l.NameIn(i18n.Locale())
	}
	return code
}

// speakerArchetype is the curated voice of model e's speaker id, if any.
func speakerArchetype(e catalog.Entry, id string) (gallery.Archetype, bool) {
	for _, ar := range gallery.Default().All() {
		if ar.Model == e.ID && ar.Speaker == id {
			return ar, true
		}
	}
	return gallery.Archetype{}, false
}

// optionVoiceInfo is what a picker row's voice speaks and is used for:
// its language (base code, "" unknown) and use cases.
func optionVoiceInfo(e catalog.Entry, op voiceOption) (string, []string) {
	switch {
	case op.voice != nil:
		v := op.voice
		var uses []string
		if id, ok := strings.CutPrefix(v.Origin, "gallery:"); ok {
			if ar, ok := gallery.Default().ByID(id); ok {
				uses = ar.Uses
			}
		}
		return catalog.BaseLanguage(v.Language), uses
	case strings.HasPrefix(op.value, "sp:"):
		id := strings.TrimPrefix(op.value, "sp:")
		var uses []string
		if ar, ok := speakerArchetype(e, id); ok {
			uses = ar.Uses
		}
		return catalog.BaseLanguage(e.SpeakerLanguage(id)), uses
	}
	return "", nil
}

// recommendVoices are the rows 推荐 heads the picker with: the best few
// of opts for the script (language first, then use, then the model's own
// recommendation), as copies whose values start "rec:"; and, when none
// speaks the script's language and the model is not the one recommended
// for it, a row offering that model ("rec-model:<id>").
func (a *app) recommendVoices(e catalog.Entry, opts []voiceOption) []voiceOption {
	l, use := a.studioScriptLang(), a.scriptUse()
	speaks := len(e.Languages) == 0 || slices.ContainsFunc(e.Languages, func(x catalog.Language) bool { return catalog.BaseLanguage(x.Code) == l })
	type scored struct {
		op    voiceOption
		score int
	}
	var cands []scored
	for _, op := range opts {
		if op.action || op.warn || op.value == "default" || op.value == "clip" {
			continue
		}
		vl, uses := optionVoiceInfo(e, op)
		score := 0
		switch {
		case vl == l:
			score = 4
		case vl == "" && speaks:
			score = 1 // a multilingual voice: it speaks the script
		default:
			continue // another language's voice
		}
		if use != "" && slices.Contains(uses, use) {
			score += 2
		}
		if op.sub == i18n.T("Recommended") {
			score++
		}
		why := langPlain(l)
		if use != "" && slices.Contains(uses, use) {
			why += " · " + useName(use)
		}
		rec := op
		rec.value, rec.group, rec.sub = "rec:"+op.value, i18n.T("Recommended for this script"), why
		cands = append(cands, scored{rec, score})
	}
	slices.SortStableFunc(cands, func(x, y scored) int { return y.score - x.score })
	var out []voiceOption
	for _, s := range cands[:min(len(cands), maxRecommended)] {
		out = append(out, s.op)
	}
	// The model recommended for the language, when it is another: last,
	// or alone when this model has no voice in the language.
	if m, ok := recommendedModel(l); ok && m.ID != e.ID && !catalog.Builtin().IsRecommended(catalog.WorkflowStudio, l, e.ID) {
		why := i18n.T("Recommended for %s", langPlain(l))
		if len(out) == 0 || !speaks {
			why = i18n.T("It speaks %s; this model has no voice in it", langPlain(l))
		}
		out = append(out, voiceOption{value: "rec-model:" + m.ID, group: i18n.T("Recommended for this script"),
			label: i18n.T("Switch to %s", m.DisplayName()), sub: why})
	}
	return out
}

// recommendedModel is the Studio model recommended for language l.
func recommendedModel(l string) (catalog.Entry, bool) {
	recs := catalog.Builtin().Recommended(catalog.WorkflowStudio, l)
	if len(recs) == 0 {
		return catalog.Entry{}, false
	}
	return recs[0], true
}
