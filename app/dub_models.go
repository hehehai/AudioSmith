package main

// Dubbing's wiring to models: the pipeline's models and their caps, the
// voice resolver, translators, and the TTS model choice; split from
// dub.go.

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/gallery"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/llm"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/translate"
	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
)

// dubModels are the pipeline's models: the catalogue's defaults, and
// the first diarization and separation entries once the catalogue has
// them (thread #14). Speech to text is Qwen3-ASR, and Whisper for the
// languages Qwen3-ASR does not know (thread #46), unless Settings →
// Transcribe names an Accurate model: that one for every language.
func dubModels(lib *library) services.DubModels {
	c := catalog.Builtin()
	repo := func(id string) string {
		if e, ok := c.ByID(id); ok {
			return e.Repo
		}
		return ""
	}
	first := func(task catalog.Task) string {
		for _, e := range c.ForTask(task) {
			return e.Repo
		}
		return ""
	}
	m := services.DubModels{Align: repo(catalog.DefaultAlign), Enhance: repo(catalog.DefaultEnhance),
		Diarize: first(catalog.TaskDiarize), Separate: first(catalog.TaskSeparate)}
	if e, ok := c.ByID(catalog.DefaultSTT); ok {
		m.STT, m.WordTimes = e.Repo, e.Has(catalog.CapWordTimes)
		for _, l := range e.Languages {
			m.STTLanguages = append(m.STTLanguages, catalog.BaseLanguage(l.Code))
		}
	}
	if e, ok := c.ByID(catalog.FallbackSTT); ok && m.STTLanguages != nil {
		m.OtherSTT, m.OtherWordTimes = e.Repo, e.Has(catalog.CapWordTimes)
	}
	// Who speaks (thread #48): the speakers model when it is downloaded
	// (the pipeline falls back to STT + Sortformer when it is not).
	if e, ok := c.SpeakersModel(); ok {
		m.Speakers = e.Repo
		m.SpeakersLanguages = []string{}
		for _, l := range e.Languages {
			m.SpeakersLanguages = append(m.SpeakersLanguages, catalog.BaseLanguage(l.Code))
		}
	}
	// Conversations (thread #89): the dialogue models a dub speaks with.
	m.Dialogue = dubDialogueModels(machineMemoryGB())
	if lib != nil {
		s := lib.settings.Get()
		if s.Transcribe.AccurateModel != "" {
			if e, ok := c.ByID(s.Transcribe.AccurateModel); ok && slices.Contains(e.Tasks, catalog.TaskSTT) {
				m.STT, m.WordTimes = e.Repo, e.Has(catalog.CapWordTimes)
				m.STTLanguages, m.OtherSTT, m.OtherWordTimes = nil, "", false
			}
		}
	}
	return m
}

// dubDialogueModels are the dialogue models a dub can speak its
// conversations with on a Mac of gb GB (0: unknown): SoulX-Podcast (P2;
// MOSS-TTSD comes later, plan §2, P3), blocked where it lacks memory.
func dubDialogueModels(gb int) []services.DubDialogueModel {
	var out []services.DubDialogueModel
	for _, e := range dubDialogueEntries() {
		d := services.DubDialogueModel{Repo: e.Repo, Family: e.Family, Revision: e.Revision}
		for _, l := range e.Languages {
			d.Languages = append(d.Languages, catalog.BaseLanguage(l.Code))
		}
		if gb > 0 && float64(gb) < e.MinMemoryGB {
			d.Blocked = services.DubDialogueMemory
		}
		out = append(out, d)
	}
	return out
}

// dubDialogueEntries are the catalogue's dialogue models a dub speaks
// with, the default first.
func dubDialogueEntries() []catalog.Entry {
	var out []catalog.Entry
	for _, e := range dialogueEntries() {
		if e.Family != services.FamilyMossTTSD { // P3
			out = append(out, e)
		}
	}
	return out
}

// machineMemoryGB is this machine's memory in GB (read once).
var machineMemoryGB = sync.OnceValue(func() int { return readSysInfo().MemoryGB() })

// dubCaps describes a TTS model to the pipeline.
func dubCaps(repo string) services.DubModelCaps {
	e := entryFor(repo)
	c := services.DubModelCaps{Clone: e.Has(catalog.CapClone), Duration: e.Has(catalog.CapDuration),
		Instruct: services.TakesInstruct(capNames(e)), Speed: e.Has(catalog.CapSpeed),
		Steps: e.Has(catalog.CapSteps), Guidance: e.Has(catalog.CapGuidance), NeedsReference: e.NeedsReference,
		CloneAudioOnly: e.Has(catalog.CapCloneAudioOnly), CloneInstruct: services.CloneTakesInstruct(capNames(e))}
	if e.Has(catalog.CapLanguage) {
		c.Languages = map[string]string{}
		for _, l := range e.Languages {
			if l.Value != "" {
				c.Languages[l.Code] = l.Value
			}
		}
		if len(c.Languages) == 0 {
			c.Languages = nil
		}
	}
	return c
}

// dubVoiceResolver resolves the cast's voices for a model, on any
// goroutine: saved voices through the voice library's fillVoice, the
// model's speakers, designs and gallery voices lowered for design models
// (else their delivery as an instruction).
func (a *app) dubVoiceResolver(lib *library) func(ctx context.Context, ref, model, lang string) (dub.VoiceSpec, error) {
	return func(ctx context.Context, ref, model, lang string) (dub.VoiceSpec, error) {
		e := entryFor(model)
		switch {
		case strings.HasPrefix(ref, dub.SpeakerPrefix):
			return dub.VoiceSpec{Voice: strings.TrimPrefix(ref, dub.SpeakerPrefix)}, nil
		case strings.HasPrefix(ref, dub.GalleryPrefix) && !canDesign(e):
			// A curated voice speaks as it does elsewhere: a built-in
			// speaker on its own model, a designed voice cloned from its
			// bundled reference.
			if arch, ok := gallery.Default().ByID(strings.TrimPrefix(ref, dub.GalleryPrefix)); ok && arch.Curated() {
				if spec, ok := a.dubGallerySpec(arch, e); ok {
					return spec, nil
				}
			}
			fallthrough
		case strings.HasPrefix(ref, dub.DesignPrefix), strings.HasPrefix(ref, dub.GalleryPrefix), strings.HasPrefix(ref, dub.PresetPrefix):
			r, hint, ok := dubDesignRecipe(ref)
			if !ok {
				return dub.VoiceSpec{}, core.ErrNotFound
			}
			if canDesign(e) {
				low, err := vd.Lower(designTarget(e), r, vd.Options{Lang: designTextLang(firstOf(r.Attrs.Language(), lang))})
				if err != nil {
					return dub.VoiceSpec{}, err
				}
				return dub.VoiceSpec{Voice: low.Voice, Seed: low.Seed}, nil
			}
			// A model that cannot design: the delivery as an instruction.
			return dub.VoiceSpec{Instruct: hint}, nil
		}
		v, err := lib.svc.Voices.Get(ctx, ref)
		if err != nil {
			return dub.VoiceSpec{}, err
		}
		if ok, why := voiceUsable(&v, e); !ok {
			return dub.VoiceSpec{}, errors.New(why)
		}
		var t core.Take
		t.Language = modelLanguage(e, lang)
		a.fillVoice(&t, &v, e)
		spec := dub.VoiceSpec{Voice: t.Voice, RefAudio: t.RefAudio, RefText: t.RefText, Seed: t.Seed, Language: t.Language}
		if e.Has(catalog.CapVoicePrompt) && v.Kind == core.VoiceCloned && !v.Locked && v.Prompts[e.Repo] != "" {
			spec.VoicePrompt = lib.svc.Voices.FilePath(v, v.Prompts[e.Repo])
		}
		return spec, nil
	}
}

// dubGallerySpec is how curated voice arch speaks on model e when e
// cannot design: its own speaker on its own model, its bundled reference
// on a model that clones; false otherwise.
func (a *app) dubGallerySpec(arch gallery.Archetype, e catalog.Entry) (dub.VoiceSpec, bool) {
	switch {
	case arch.Kind == gallery.KindBuiltin && arch.Model == e.ID:
		return dub.VoiceSpec{Voice: arch.Speaker}, true
	case arch.Kind == gallery.KindDesigned && e.Has(catalog.CapClone):
		p, err := galleryRefPath(a.voiceCacheDir(), arch)
		if err != nil {
			return dub.VoiceSpec{}, false
		}
		return dub.VoiceSpec{RefAudio: p, RefText: arch.RefText, Seed: arch.Seed}, true
	}
	return dub.VoiceSpec{}, false
}

// dubDesignRecipe is the design recipe of a design, preset or gallery
// voice reference, and its delivery hint for models that cannot design.
func dubDesignRecipe(ref string) (vd.Recipe, string, bool) {
	switch {
	case strings.HasPrefix(ref, dub.PresetPrefix):
		p, ok := gallery.PresetByID(strings.TrimPrefix(ref, dub.PresetPrefix))
		if !ok {
			return vd.Recipe{}, "", false
		}
		return vd.Recipe{Attrs: p.Attrs}, p.StyleHint.In("en"), true
	case strings.HasPrefix(ref, dub.GalleryPrefix):
		// Curated voices and the earlier gallery's (they still resolve).
		arch, ok := gallery.Default().ByID(strings.TrimPrefix(ref, dub.GalleryPrefix))
		if !ok || !arch.Designed() {
			return vd.Recipe{}, "", false
		}
		return arch.Recipe(), arch.Description.In("en"), true
	}
	desc := strings.TrimSpace(strings.TrimPrefix(ref, dub.DesignPrefix))
	if desc == "" {
		return vd.Recipe{}, "", false
	}
	return vd.Recipe{Description: desc, Attrs: vd.Parse(desc).Attrs}, desc, true
}

// dubMakeTranslator makes translators (tests replace it).
var dubMakeTranslator = dubTranslator

// dubTranslator is the translator a project's settings ask for: the
// language-model router, or an online MT service with its key in the
// Keychain (cloud services only when chosen).
func dubTranslator(router *llm.Router, s dub.Settings) (translate.Translator, error) {
	switch s.Translator {
	case "", "llm":
		if router == nil || !router.Available(llm.SkillTranslate) {
			return nil, &translate.Error{Code: translate.CodeNoLLM}
		}
		return &translate.LLM{Router: router}, nil
	}
	cfg := translate.MTConfig{Client: hub.NewHTTPClient("", 0), Secrets: llmSecrets}
	var t translate.Translator
	switch s.Translator {
	case "deepl":
		t = translate.NewDeepL(cfg)
	case "google":
		t = translate.NewGoogle(cfg)
	case "microsoft":
		t = translate.NewMicrosoft(cfg, "")
	default:
		return nil, &translate.Error{Code: translate.CodeConfig}
	}
	if router != nil && router.Available(llm.SkillFitRewrite) {
		return translate.WithFit(t, &translate.Fitter{Router: router}), nil
	}
	return translate.WithFit(t, &translate.Fitter{}), nil
}

// dubTranslatorReady says whether the project's translator can run, and
// its name.
func (a *app) dubTranslatorReady(s dub.Settings) (bool, string) {
	switch s.Translator {
	case "", "llm":
		if a.llmRouter == nil {
			return false, ""
		}
		rs := a.llmRouter.Routes(llm.SkillTranslate)
		if len(rs) == 0 {
			return false, ""
		}
		return true, a.routeName(rs[0])
	case "deepl":
		return true, "DeepL"
	case "google":
		return true, "Google Cloud Translation"
	case "microsoft":
		return true, "Microsoft Translator"
	}
	return false, ""
}

// dubDefaultModel is the TTS model a new dub uses: the default if it is
// downloaded, else the first downloaded model that clones.
func (a *app) dubDefaultModel() string { return dubDefaultModelBy(a.modelOnDisk) }

// dubDefaultModelBy is dubDefaultModel with onDisk saying whether a
// model is downloaded (and whether that is known).
func dubDefaultModelBy(onDisk func(catalog.Entry) (ready, known bool)) string {
	if e, ok := catalog.Builtin().ByRepo(dubDefaultModel); ok {
		if ready, known := onDisk(e); ready || !known {
			return e.Repo
		}
	}
	for _, e := range dubTTSModels() {
		if ready, _ := onDisk(e); ready && e.Has(catalog.CapClone) {
			return e.Repo
		}
	}
	return dubDefaultModel
}

// dubTTSModels are the models a dub can speak with: those that clone,
// then the others.
func dubTTSModels() []catalog.Entry {
	var clone, other []catalog.Entry
	for _, e := range catalog.Builtin().ForTask(catalog.TaskTTS) {
		if e.Hidden {
			continue
		}
		if e.Has(catalog.CapClone) {
			clone = append(clone, e)
		} else {
			other = append(other, e)
		}
	}
	return append(clone, other...)
}
