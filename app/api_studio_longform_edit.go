package main

// Updating Stories and Audiobook projects for agents (thread #35):
// UpdateLongform changes a project's voices and settings, updatePerformance
// a story's scene, personas, directions, pacing and AI director.

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/llm"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/text/markup"
	"github.com/hehehai/AudioSmith/internal/translate"
)

// updatePerformance applies a story's scene, personas, directions, pacing
// and the AI director of an update.
func (b *apiBackend) updatePerformance(ctx context.Context, p core.Project, d *services.LongformDoc, req api.LongformUpdate, voices []core.Voice) error {
	story := p.Kind == core.ProjectStory
	if !story && (req.Scene != nil || len(req.Personas) > 0 || len(req.Directions) > 0 || len(req.Emotions) > 0 || len(req.Intensities) > 0 || req.Pacing != "" ||
		req.Merge != nil || req.Level != nil || req.Direct != "") {
		return errBadRequest("scene", "scene, personas, directions, pacing and direct are for stories.")
	}
	if err := setDialogue(d, req); err != nil { // api_studio_longform_dialogue.go
		return err
	}
	if !story {
		return nil
	}
	if req.Scene != nil {
		d.Scene = strings.TrimSpace(*req.Scene)
	}
	for name, persona := range req.Personas {
		i, err := storyCastIndex(d, name, "personas")
		if err != nil {
			return err
		}
		d.Cast[i].Persona = strings.TrimSpace(persona)
	}
	line := func(id, param string) (*services.StoryLine, error) {
		i := slices.IndexFunc(d.Lines, func(l services.StoryLine) bool { return l.ID == id })
		if i < 0 {
			return nil, errBadRequest(param, "The story has no line %q (get_longform lists them).", id)
		}
		return &d.Lines[i], nil
	}
	for id, dir := range req.Directions {
		l, err := line(id, "directions")
		if err != nil {
			return err
		}
		l.Direction, l.EmotionByUser = strings.TrimSpace(dir), true // the director keeps it
		if l.Direction == "" && l.Emotion == "" {
			l.Intensity = 0
		}
	}
	for id, e := range req.Emotions {
		l, err := line(id, "emotions")
		if err != nil {
			return err
		}
		k := services.NormEmotion(e)
		if k == "" && strings.TrimSpace(e) != "" {
			return errBadRequest("emotions", "%q is not an emotion; the emotions are: %s.", e, strings.Join(services.Emotions, ", "))
		}
		l.Emotion, l.EmotionByUser = k, true // the director keeps it
		if k == "" {
			l.Turn = false
		}
	}
	for id, n := range req.Intensities {
		l, err := line(id, "intensities")
		if err != nil {
			return err
		}
		if n < 0 || n > services.IntensityStrong {
			return errBadRequest("intensities", "An intensity is 0 (barely, with an emotion), 1 (restrained), 2 (clear) or 3 (strong); 0 clears a direction's.")
		}
		l.Intensity, l.EmotionByUser = n, true
	}
	switch req.Pacing {
	case "":
	case "fixed":
		d.Production.Pacing = services.PacingFixed
	case services.PacingDialogue, services.PacingReading, services.PacingRelaxed:
		d.Production.Pacing = req.Pacing
	default:
		return errBadRequest("pacing", "pacing is dialogue, reading, relaxed or fixed.")
	}
	if req.Merge != nil {
		d.Production.NoMerge = !*req.Merge
	}
	if req.Level != nil {
		d.Production.NoLevel = !*req.Level
	}
	switch req.Direct {
	case "":
		return nil
	case "fill", "all":
	default:
		return errBadRequest("direct", "direct is fill (only what is empty) or all (replace).")
	}
	if len(storyScript(d, nil).Lines) == 0 {
		return errBadRequest("direct", "The story has no lines to direct.")
	}
	em, err := (&translate.StoryDirector{Router: b.router}).DirectEmotions(ctx, storyScript(d, voices), emotionVocab(storyZh(d)))
	if err != nil {
		if c := translate.Code(err); c == translate.CodeNoLLM || c == llm.CodeNoRoute {
			return apiErr(http.StatusBadRequest, api.CodeLLMUnavailable, "direct",
				"The AI director needs a language model: set one up in AudioSmith's Settings → Writing help (Qwen3 4B runs on this Mac).")
		}
		return apiErr(http.StatusBadGateway, api.CodeDirectorFailed, "direct", "The AI director failed: %v", err)
	}
	applyEmotions(d, em, req.Direct == "fill")
	return nil
}

// UpdateLongform changes a project's voices and settings.
func (b *apiBackend) UpdateLongform(ctx context.Context, req api.LongformUpdate) (out api.LongformJSON, err error) {
	p, d, err := b.loadLongform(ctx, req.ID)
	if err != nil {
		return api.LongformJSON{}, err
	}
	voices, err := b.voices(ctx)
	if err != nil {
		return api.LongformJSON{}, err
	}
	if req.Model != "" {
		e, warn, err := ttsEntry(req.Model, "", false)
		if err != nil {
			return api.LongformJSON{}, err
		}
		d.Model = e.Repo
		defer func() {
			if err == nil {
				out.Warning = warn
			}
		}()
	}
	e := entryFor(d.Model)
	ref := func(v, param string) (string, error) {
		if isDefault(v) {
			return "", nil
		}
		return voiceRef(voices, e, v, param)
	}
	if req.Voice != "" {
		if d.Voice, err = ref(req.Voice, "voice"); err != nil {
			return api.LongformJSON{}, err
		}
	}
	if req.Script != nil && p.Kind != core.ProjectAudiobook {
		return api.LongformJSON{}, errBadRequest("script", "script is for audiobooks; a story's lines change in AudioSmith (or make a new story).")
	}
	if p.Kind == core.ProjectAudiobook {
		err = updateBookVoices(&d, req, ref)
	} else {
		err = updateStoryVoices(&d, req, ref)
	}
	if err != nil {
		return api.LongformJSON{}, err
	}
	if err := b.updateLongformSettings(&d, req); err != nil {
		return api.LongformJSON{}, err
	}
	if t := strings.TrimSpace(req.Title); t != "" {
		d.Title, p.Title = t, t
	}
	if err := b.updatePerformance(ctx, p, &d, req, voices); err != nil {
		return api.LongformJSON{}, err
	}
	bookDialogueDefault(&d)
	// Refused when the project was saved meanwhile (the page, another
	// request): this edit was made on what it was before (#101 M7).
	saved, err := b.lib.svc.Library.SaveDocIf(ctx, core.Project{ID: p.ID, Kind: p.Kind, Title: p.Title}, d, services.BodyIs(p))
	if errors.Is(err, services.ErrChanged) {
		return api.LongformJSON{}, errBusy("The project was saved elsewhere (in AudioSmith, or by another request) while this edit ran: read it again with get_longform and retry.")
	}
	if err != nil {
		return api.LongformJSON{}, err
	}
	return b.longformJSON(ctx, saved, d, voices)
}

// voiceRefFunc resolves an agent's voice for a parameter ("" the
// default).
type voiceRefFunc func(v, param string) (string, error)

// bookCastName is the manuscript's name (among names) an agent's cast
// entry means.
func bookCastName(names []string, name string) (string, error) {
	i := slices.IndexFunc(names, func(n string) bool { return markup.NameKey(n) == markup.NameKey(name) })
	if i < 0 {
		return "", errBadRequest("cast", "The manuscript has no [voice:%s] nor a dialogue speaker %s; its names are: %s.", name, name, strings.Join(names, ", "))
	}
	return names[i], nil
}

// updateBookVoices applies an update's manuscript and cast to an
// audiobook.
func updateBookVoices(d *services.LongformDoc, req api.LongformUpdate, ref voiceRefFunc) error {
	if len(req.Lines) > 0 {
		return errBadRequest("line_voices", "line_voices are for stories; an audiobook switches voices with [voice:NAME] in its text.")
	}
	if req.Script != nil {
		if strings.TrimSpace(*req.Script) == "" {
			return errBadRequest("script", "The manuscript is empty.")
		}
		d.Script = *req.Script
	}
	names := services.CastNames(d.Script)
	for name, v := range req.Cast {
		cn, err := bookCastName(names, name)
		if err != nil {
			return err
		}
		r, err := ref(v, "cast")
		if err != nil {
			return err
		}
		if d.VoiceCast == nil {
			d.VoiceCast = map[string]string{}
		}
		if r == "" {
			delete(d.VoiceCast, cn)
		} else {
			d.VoiceCast[cn] = r
		}
	}
	return nil
}

// updateStoryVoices applies an update's cast and line voices to a story.
func updateStoryVoices(d *services.LongformDoc, req api.LongformUpdate, ref voiceRefFunc) error {
	var err error
	for name, v := range req.Cast {
		i, err := storyCastIndex(d, name, "cast")
		if err != nil {
			return err
		}
		if d.Cast[i].Voice, err = ref(v, "cast"); err != nil {
			return err
		}
	}
	for id, v := range req.Lines {
		i := slices.IndexFunc(d.Lines, func(l services.StoryLine) bool { return l.ID == id })
		if i < 0 {
			return errBadRequest("line_voices", "The story has no line %q (get_longform lists them).", id)
		}
		if d.Lines[i].Voice, err = ref(v, "line_voices"); err != nil {
			return err
		}
	}
	return nil
}

// storyCastIndex is the index of a story's character an agent names
// (for param).
func storyCastIndex(d *services.LongformDoc, name, param string) (int, error) {
	i := slices.IndexFunc(d.Cast, func(m services.CastMember) bool { return strings.EqualFold(m.Name, strings.TrimSpace(name)) })
	if i < 0 {
		var names []string
		for _, m := range d.Cast {
			names = append(names, m.Name)
		}
		return 0, errBadRequest(param, "The story has no character %q; its characters are: %s.", name, strings.Join(names, ", "))
	}
	return i, nil
}

// updateLongformSettings applies an update's language, speed, stems,
// format, loudness and book details.
func (b *apiBackend) updateLongformSettings(d *services.LongformDoc, req api.LongformUpdate) error {
	if req.Language != "" {
		d.Language = req.Language
		if strings.EqualFold(req.Language, "auto") {
			d.Language = ""
		}
	}
	if req.Speed != 0 {
		d.Speed = req.Speed
	}
	if req.Stems != nil {
		d.Stems = *req.Stems
	}
	if req.Format != "" {
		if req.Format == services.FormatM4B && !b.CanEncode() {
			return apiErr(http.StatusBadRequest, api.CodeUnsupportedFormat, "format", "M4B needs the engine's encoder, which is not available: use wav.")
		}
		d.Format = strings.ToLower(req.Format)
	}
	switch req.Loudness {
	case "":
	case "off":
		d.Book.Loudness = services.LoudnessOff
	default:
		d.Book.Loudness = req.Loudness
	}
	for _, f := range []struct {
		v   string
		dst *string
	}{{req.Author, &d.Book.Author}, {req.Narrator, &d.Book.Narrator}, {req.Year, &d.Book.Year}, {req.Genre, &d.Book.Genre},
		{req.About, &d.Book.Description}, {req.Cover, &d.Book.Cover}} {
		if f.v != "" {
			*f.dst = strings.TrimSpace(f.v)
		}
	}
	return nil
}
