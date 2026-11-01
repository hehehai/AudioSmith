package main

// The models the voice pages use (design and clone models, the target of
// a design) and the Studio's seam: which voices the Studio offers, filling
// its recipe from a saved voice, Use in Studio; split from voices.go.

import (
	"strings"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
)

// --- models -------------------------------------------------------------

// voiceCatalog is the catalogue the voice pages offer models from (tests
// swap it for one with more design-capable models).
var voiceCatalog = catalog.Builtin

// canDesign reports whether a model makes voices from descriptions.
func canDesign(e catalog.Entry) bool {
	_, ok := vd.EngineFor(e.Family, e.Repo)
	return ok && e.Has(catalog.CapDesign)
}

// designModels are the models that can design voices, in catalogue order.
func designModels() []catalog.Entry {
	var out []catalog.Entry
	for _, e := range voiceCatalog().ForTask(catalog.TaskTTS) {
		if canDesign(e) {
			out = append(out, e)
		}
	}
	return out
}

// cloneModels are the models that clone from a reference.
func cloneModels() []catalog.Entry {
	var out []catalog.Entry
	for _, e := range voiceCatalog().ForTask(catalog.TaskTTS) {
		if e.Has(catalog.CapClone) {
			out = append(out, e)
		}
	}
	return out
}

// designModelFor is the model a designed voice renders with: repo when it
// can design, else the first that can.
func (a *app) designModelFor(repo string) (catalog.Entry, bool) {
	ms := designModels()
	for _, e := range ms {
		if e.Repo == repo {
			return e, true
		}
	}
	if len(ms) == 0 {
		return catalog.Entry{}, false
	}
	return ms[0], true
}

// designTarget is the lowering of a design-capable model.
func designTarget(e catalog.Entry) vd.Target {
	eng, _ := vd.EngineFor(e.Family, e.Repo)
	return eng.Target
}

// designTextLang is the language a design description is written in for
// speech in lang: Chinese for Chinese, English otherwise.
func designTextLang(lang string) string {
	if strings.HasPrefix(lang, "zh") {
		return "zh"
	}
	return "en"
}

// --- the Studio's seam ---------------------------------------------------

// voiceUsable reports whether model e can speak with v, and why not.
func voiceUsable(v *core.Voice, e catalog.Entry) (bool, string) {
	if e.NeedsReference && !voiceHasReference(v) {
		return false, i18n.T("%s only clones voices, and this voice has no reference clip.", modelRef(e))
	}
	switch {
	case v.Kind == core.VoiceBuiltin:
		if removedVoice(v) {
			return false, i18n.T("This voice belongs to %s, which AudioSmith removed: it can't be used any more.", catalog.Builtin().DisplayName(v.Model))
		}
		if v.Model == "" || v.Model == e.Repo {
			return true, ""
		}
		if e.Has(catalog.CapClone) {
			return false, i18n.T("This voice belongs to %s. Make a portable copy to use it here: an approximate clone.", catalog.Builtin().DisplayName(v.Model))
		}
		return false, i18n.T("This voice belongs to %s.", catalog.Builtin().DisplayName(v.Model))
	case v.Locked && v.Sample != "" && e.Has(catalog.CapClone):
		return true, ""
	case v.Kind == core.VoiceCloned:
		if e.Has(catalog.CapClone) {
			return true, ""
		}
		return false, i18n.T("%s can't clone voices. Choose a model that clones, such as %s or %s.", modelRef(e), modelRefID(idCloneHiFi), modelRefID(idOmniVoice))
	case v.Kind == core.VoiceDesigned:
		if canDesign(e) {
			return true, ""
		}
		if (v.Sample != "" || v.RefAudio != "") && e.Has(catalog.CapClone) {
			return true, ""
		}
		if e.Has(catalog.CapClone) {
			return false, i18n.T("%s can't design voices. Make a portable copy to use it here (an approximate clone), or choose a model that designs.", modelRef(e))
		}
		return false, i18n.T("%s can't design voices. Choose a model that can, such as %s.", modelRef(e), modelRefID(idOmniVoice))
	}
	return false, ""
}

// voiceHasReference reports whether fillVoice gives a clone of v a
// reference clip: a cloned voice's reference, a designed voice's reference
// or sample, or a locked voice's take (what models that only clone can
// speak with).
func voiceHasReference(v *core.Voice) bool {
	switch {
	case v.Kind == core.VoiceBuiltin:
		return false
	case v.Kind == core.VoiceDesigned && v.RefAudio != "":
		return true
	case v.Sample != "" && (v.Locked || v.Kind == core.VoiceDesigned):
		return true
	case v.Kind == core.VoiceCloned:
		return v.RefAudio != ""
	}
	return false
}

// studioUsesSavedVoice reports whether the Studio's voice comes from the
// picker (a saved voice or an unsaved clip) for model e, so the Studio
// can hide its own voice and reference controls.
func (a *app) studioUsesSavedVoice(e catalog.Entry) bool {
	if v := a.findVoice(a.studioVoice); v != nil {
		ok, _ := voiceUsable(v, e)
		return ok
	}
	return a.studioClip != nil && e.Has(catalog.CapClone)
}

// applyStudioVoice fills the voice of a take for model e from the
// picker's choice: VoiceID, and what the model takes to speak with it —
// a reference and its transcript (clones, locked voices), the design
// lowered to the model's vocabulary and the seed (designed voices), a
// style instruction, a built-in speaker. The language is filled only when
// the take has none. A choice the model can't use leaves t as it is.
//
// Thread G calls it where the Studio builds a take's recipe:
// a.applyStudioVoice(&t, p)
func (a *app) applyStudioVoice(t *core.Take, e catalog.Entry) {
	if a.studioVoice == "" && a.studioClip != nil {
		if e.Has(catalog.CapClone) {
			t.RefAudio, t.RefText = a.studioClip.Path, a.studioClip.Text
			if s := styleFor(e, a.studioClip.Style); s != "" {
				t.Voice = s
			}
		}
		return
	}
	v := a.findVoice(a.studioVoice)
	if v == nil {
		return
	}
	if ok, _ := voiceUsable(v, e); !ok {
		return
	}
	a.fillVoice(t, v, e)
}

// fillVoice sets a take's voice fields for v on model e (v usable on e).
func (a *app) fillVoice(t *core.Take, v *core.Voice, e catalog.Entry) {
	t.VoiceID = v.ID
	if t.Language == "" {
		t.Language = modelLanguage(e, v.Language)
	}
	switch {
	case v.Kind == core.VoiceBuiltin:
		t.Voice = v.Speaker
	case v.Kind == core.VoiceDesigned && canDesign(e) && !(v.Locked && v.Sample != "" && e.Has(catalog.CapClone)):
		r := voiceRecipe(v)
		low, err := vd.Lower(designTarget(e), r, vd.Options{Lang: designTextLang(firstOf(r.Attrs.Language(), v.Language))})
		if err == nil {
			t.Voice, t.Seed = low.Voice, low.Seed
		}
	case v.Locked && v.Sample != "":
		t.RefAudio, t.RefText, t.Seed = a.voicePath(v, v.Sample), v.SampleText, v.Seed
		if s := styleFor(e, v.Style); s != "" {
			t.Voice = s
		}
	case v.Kind == core.VoiceDesigned: // its reference (else its sample), cloned
		t.RefAudio, t.RefText = a.voicePath(v, v.Sample), v.SampleText
		if v.RefAudio != "" {
			t.RefAudio, t.RefText = a.voicePath(v, v.RefAudio), v.RefText
		}
		t.Seed = v.Seed
	default:
		t.RefAudio, t.RefText = a.voicePath(v, v.RefAudio), v.RefText
		if v.Seed != 0 {
			t.Seed = v.Seed
		}
		if s := styleFor(e, v.Style); s != "" {
			t.Voice = s
		}
	}
}

// studioVoicePrompt is the cached voice prompt (tts.encodeVoice) of the
// Studio's voice for model e, an absolute path, or "". The engine prefers
// it to the reference, which applyStudioVoice still sets as the fallback.
//
// Thread G sends it as SynthesizeParams.VoicePrompt once core.Take
// carries one: t.VoicePrompt = a.studioVoicePrompt(p)
func (a *app) studioVoicePrompt(e catalog.Entry) string {
	v := a.findVoice(a.studioVoice)
	if v == nil || !e.Has(catalog.CapVoicePrompt) || v.Prompts[e.Repo] == "" {
		return ""
	}
	if ok, _ := voiceUsable(v, e); !ok || (v.Locked && v.Sample != "") || v.Kind != core.VoiceCloned {
		return "" // the prompt is of the reference, not of a locked take or a design
	}
	return a.voicePath(v, v.Prompts[e.Repo])
}

// styleFor is a clone's style hint as model e takes it: tag engines get
// it reduced to their vocabulary, free-form ones as written, others none.
func styleFor(e catalog.Entry, style string) string {
	style = strings.TrimSpace(style)
	if style == "" || !(e.Has(catalog.CapVoiceStyle) || e.Has(catalog.CapInstruct)) {
		return ""
	}
	eng, ok := vd.EngineFor(e.Family, e.Repo)
	if ok && eng.Vocabulary == vd.VocabTags {
		r := vd.Recipe{Attrs: vd.Parse(style).Attrs}
		low, err := vd.Lower(eng.Target, r, vd.Options{Lang: vd.TextLang(style)})
		if err != nil {
			return ""
		}
		return low.Voice
	}
	if ok && eng.Vocabulary == vd.VocabSpeaker {
		return "" // CustomVoice speaks only its own speakers: no clones
	}
	return style
}

func firstOf(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

// useVoiceInStudio makes v the Studio's voice and opens the Studio,
// switching to a model that can speak with it when the Studio's can't.
func (a *app) useVoiceInStudio(v *core.Voice) {
	a.studioVoice, a.studioClip = v.ID, nil
	if ok, _ := voiceUsable(v, a.preset()); !ok {
		// The recommended models first: a designed voice speaks best
		// cloned from its sample on Qwen3-TTS Base (thread #41).
		pick := catalog.Builtin().Recommended(catalog.WorkflowStudio, i18n.Locale())
		if v.Kind == core.VoiceDesigned {
			pick = append(pick, designModels()...)
		}
		pick = append(pick, cloneModels()...)
		for _, e := range pick {
			if ok, _ := voiceUsable(v, e); ok {
				a.selectModel(e)
				break
			}
		}
	}
	a.navigate("/studio")
}
