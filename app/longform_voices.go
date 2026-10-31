package main

// Longform's voices: the pickers' choices, voice references resolved for
// the document's model, the voice pages' seams (Use in Stories, Narrate an
// audiobook) and the automatic model; split from longform.go.

import (
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/services"
)

// lfVoice is a choice of the voice pickers: a VoiceRef and its label.
type lfVoice struct{ ref, label string }

// lfVoices are the voices model e can speak with: the saved voices it
// can use, then its own speakers.
func (a *app) lfVoices(e catalog.Entry) []lfVoice { return lfVoicesOf(a.voices, e) }

// lfVoicesOf are the voices model e can speak with among voices, then
// its own speakers (the API's projects use it too: api_studio*.go).
func lfVoicesOf(voices []core.Voice, e catalog.Entry) []lfVoice {
	var out []lfVoice
	for i := range voices {
		v := &voices[i]
		if ok, _ := voiceUsable(v, e); ok {
			out = append(out, lfVoice{v.ID, v.Name})
		}
	}
	for _, id := range e.VoiceIDsIn(i18n.Locale()) {
		out = append(out, lfVoice{services.SpeakerRef + id, id})
	}
	return out
}

// lfVoiceLabel names a voice reference.
func (a *app) lfVoiceLabel(ref string) string {
	switch {
	case ref == "":
		return i18n.T("Default voice")
	case strings.HasPrefix(ref, services.SpeakerRef):
		return strings.TrimPrefix(ref, services.SpeakerRef)
	}
	if v := a.findVoice(ref); v != nil {
		return v.Name
	}
	return voiceGone() // voices_delete.go
}

// lfResolver resolves voice references and [voice:] names for a document's
// model: saved voices (by id or name) through the voice library's
// fillVoice, the model's own speakers.
func (a *app) lfResolver(d *services.LongformDoc) services.VoiceResolver {
	return lfResolverOf(d, a.voices, a.fillVoice, a.lib.svc.Voices.FilePath)
}

// lfResolverOf is lfResolver over a list of voices, on any goroutine
// (fill and path read only the library).
func lfResolverOf(d *services.LongformDoc, all []core.Voice, fill func(*core.Take, *core.Voice, catalog.Entry), path func(core.Voice, string) string) services.VoiceResolver {
	e := entryFor(d.Model)
	lang := modelLanguage(e, d.Language)
	voices := slices.Clone(all)
	byName := map[string]int{}
	for i, v := range voices {
		if _, dup := byName[strings.ToLower(v.Name)]; !dup {
			byName[strings.ToLower(v.Name)] = i
		}
	}
	speakers := e.AllVoiceIDs() // hidden ones too: a book may name one
	return func(ref string) (string, core.Take, bool) {
		t := core.Take{Language: lang}
		if id, ok := strings.CutPrefix(ref, services.SpeakerRef); ok {
			if !slices.Contains(speakers, id) && e.ID != "custom" {
				return "", t, false
			}
			t.Voice = id
			return ref, t, true
		}
		var v *core.Voice
		for i := range voices {
			if voices[i].ID == ref {
				v = &voices[i]
			}
		}
		if v == nil {
			if i, ok := byName[strings.ToLower(ref)]; ok {
				v = &voices[i]
			}
		}
		if v == nil {
			if slices.Contains(speakers, ref) {
				t.Voice = ref
				return services.SpeakerRef + ref, t, true
			}
			return "", t, false
		}
		if ok, _ := voiceUsable(v, e); !ok {
			return "", t, false
		}
		fill(&t, v, e)
		if t.RefAudio == "" && v.Kind == core.VoiceDesigned && v.RefAudio != "" {
			// Spoken by a model that designs it; whole dialogue clones it
			// from its reference (thread #83).
			t.CloneRef, t.CloneRefText = path(*v, v.RefAudio), v.RefText
		}
		if e.Has(catalog.CapVoicePrompt) && v.Kind == core.VoiceCloned && !v.Locked && v.Prompts[e.Repo] != "" {
			t.VoicePrompt = path(*v, v.Prompts[e.Repo])
		}
		return v.ID, t, true
	}
}

// --- seams for the voice pages ---------------------------------------------

// useVoiceInStories adds v to the story's cast (once) and opens Stories:
// the gallery's and the voice editor's "Use in Stories".
func (a *app) useVoiceInStories(v *core.Voice) {
	p := a.lfStories
	if !slices.ContainsFunc(p.doc.Cast, func(m services.CastMember) bool { return m.Voice == v.ID }) {
		p.doc.Cast = append(p.doc.Cast, services.CastMember{ID: core.NewID(time.Now()), Name: v.Name, Voice: v.ID})
	}
	a.lfFitModel(p, v)
	p.open["cast"] = true
	p.opened, p.listing = true, false
	a.navigate("/stories")
}

// useVoiceInAudiobook makes v the audiobook's narrator ("Set as audiobook
// default") and opens the Audiobook page.
func (a *app) useVoiceInAudiobook(v *core.Voice) {
	p := a.lfBook
	p.doc.Voice = v.ID
	a.lfFitModel(p, v)
	p.opened, p.listing = true, false
	a.navigate("/audiobook")
}

// lfFitModel switches a page to a model that can speak with v when its
// own can't.
func (a *app) lfFitModel(p *lfPage, v *core.Voice) {
	if ok, _ := voiceUsable(v, entryFor(p.doc.Model)); ok {
		return
	}
	// The page's recommended models first (a designed voice speaks best
	// cloned from its sample on Qwen3-TTS Base, thread #41).
	pick := catalog.Builtin().Recommended(lfWorkflow(p), i18n.Locale())
	if v.Kind == core.VoiceDesigned {
		pick = append(pick, designModels()...)
	}
	for _, e := range append(pick, cloneModels()...) {
		if ok, _ := voiceUsable(v, e); ok {
			p.doc.Model = e.Repo
			return
		}
	}
}

// longformVoiceItems adds Use in Stories and Narrate an audiobook to a
// voice menu; use hands the voice (saved first, for an archetype) on.
func (a *app) longformVoiceItems(m *ui.Menu, use func(then func(*core.Voice))) {
	if m.Item(i18n.T("Use in Stories")).Chosen() {
		use(a.useVoiceInStories)
	}
	if m.Item(i18n.T("Narrate an audiobook")).Chosen() {
		use(a.useVoiceInAudiobook)
	}
}

// voiceLongformMenu is the voice editor's "More" menu with them.
func (a *app) voiceLongformMenu(c *ui.Context, v *core.Voice) {
	id := v.ID
	iconButton(c.Key("editor-longform"), icons.MenuDots, i18n.T("More actions")).Menu(func(m *ui.Menu) {
		a.longformVoiceItems(m, func(then func(*core.Voice)) {
			if v := a.findVoice(id); v != nil {
				then(v)
			}
		})
	})
}

// lfWorkflow is a page's workflow in the presets.
func lfWorkflow(p *lfPage) catalog.Workflow {
	if p.kind == core.ProjectAudiobook {
		return catalog.WorkflowAudiobook
	}
	return catalog.WorkflowStory
}

// autoLfModel picks a new user's model and voice for a page by the
// presets: the model for the interface's language and whether they have
// saved voices (Chinese audiobook: Qwen3-TTS 0.6B Base narrated by the
// starter voice, or CustomVoice + Serena without one; English: Kokoro +
// af_heart). It acts while the page is automatic and empty.
func (a *app) autoLfModel(p *lfPage) {
	if !p.auto || !p.empty() {
		return
	}
	saved := a.savedVoices()
	starter := a.starterVoice()
	e, ok := catalog.Builtin().DefaultModel(lfWorkflow(p), i18n.Locale(), len(saved) > 0)
	if !ok {
		return
	}
	d := &p.doc
	d.Model, d.Voice = e.Repo, ""
	pr, _ := catalog.PresetFor(e.ID)
	if pr.NeedsVoice != "" {
		if starter != nil && p.kind == core.ProjectAudiobook {
			saved = append([]*core.Voice{starter}, saved...)
		}
		for _, v := range saved {
			if ok, _ := voiceUsable(v, e); ok {
				d.Voice = v.ID
				break
			}
		}
	} else if sp := pr.VoiceFor(i18n.Locale()); sp != "" && slices.Contains(e.VoiceIDsIn(i18n.Locale()), sp) {
		d.Voice = services.SpeakerRef + sp
	}
}

// autoLfModels re-picks the automatic pages' models (voices changed).
func (a *app) autoLfModels() {
	for _, p := range []*lfPage{a.lfStories, a.lfBook} {
		if p != nil {
			a.autoLfModel(p)
		}
	}
}
