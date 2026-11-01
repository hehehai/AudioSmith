package main

// Stories and Audiobook projects for agents (thread #35): made as the
// pages make them (import, Auto-cast, Split into lines), saved in the
// project library, compiled and rendered by the longform service, and
// exported as Save does (longform_view_outputs.go). This file creates and
// reads them; updates are api_studio_longform_edit.go, renders and
// exports api_studio_longform_render.go.

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/doc"
	"github.com/hehehai/AudioSmith/internal/services"
)

// lfKind is a project kind from the API's.
func lfKind(kind string) core.ProjectKind {
	if kind == "story" {
		return core.ProjectStory
	}
	return core.ProjectAudiobook
}

// readImport opens a book or story file as the pages' import does.
func readImport(ctx context.Context, path string) (*doc.Document, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	d, err := doc.Open(ctx, path, doc.Limits{MaxFileBytes: maxBookBytes})
	switch {
	case err == nil:
		return d, nil
	case errors.Is(err, doc.ErrUnsupported):
		return nil, apiErr(http.StatusBadRequest, api.CodeUnsupportedFormat, "path", "%s: this kind of file cannot be imported (EPUB, TXT, Markdown, DOCX; stories also SRT and VTT).", filepath.Base(path))
	case errors.Is(err, doc.ErrTooLarge):
		return nil, apiErr(http.StatusBadRequest, api.CodeTooLarge, "path", "%s is too large to import (64 MB at most).", filepath.Base(path))
	case errors.Is(err, doc.ErrNoText):
		return nil, errBadRequest("path", "%s has no text to import.", filepath.Base(path))
	}
	return nil, errBadRequest("path", "Could not import %s: %v", filepath.Base(path), err)
}

// CreateLongform makes and saves a Stories or Audiobook project.
func (b *apiBackend) CreateLongform(ctx context.Context, req api.LongformCreate) (out api.LongformJSON, err error) {
	kind := lfKind(req.Kind)
	e, warn, err := ttsEntry(req.Model, b.defaultTTSRepo(), false)
	if err != nil {
		return api.LongformJSON{}, err
	}
	defer func() {
		if err == nil {
			out.Warning = warn
		}
	}()
	voices, err := b.voices(ctx)
	if err != nil {
		return api.LongformJSON{}, err
	}
	d := services.NewLongformDoc(kind, e.Repo)
	d.Language = strings.TrimSpace(req.Language)
	if req.Format == services.FormatM4B {
		if !b.CanEncode() {
			return api.LongformJSON{}, apiErr(http.StatusBadRequest, api.CodeUnsupportedFormat, "format", "M4B needs the engine's encoder, which is not available: use wav.")
		}
		d.Format = services.FormatM4B
	}
	if d.Voice, err = voiceRef(voices, e, req.Voice, "voice"); err != nil {
		return api.LongformJSON{}, err
	}
	title := strings.TrimSpace(req.Title)
	src := req.Text
	if req.Path != "" {
		bk, err := readImport(ctx, req.Path)
		if err != nil {
			return api.LongformJSON{}, err
		}
		if kind == core.ProjectAudiobook {
			src = applyBook(&d, bk)
		} else {
			src = bk.Script()
			if d.Title == "" {
				d.Title = bk.Title
			}
		}
		if d.Title == "" {
			d.Title = strings.TrimSuffix(filepath.Base(req.Path), filepath.Ext(req.Path))
		}
	}
	if title != "" {
		d.Title = title
	}
	lfv := lfVoicesOf(voices, e)
	if kind == core.ProjectAudiobook {
		d.Script = strings.TrimSpace(src) + "\n"
		if d.Voice == "" && len(lfv) > 0 {
			d.Voice = lfv[0].ref
		}
		names := services.CastNames(d.Script)
		for name, v := range req.Cast {
			cn, err := bookCastName(names, name)
			if err != nil {
				return api.LongformJSON{}, err
			}
			ref, err := voiceRef(voices, e, v, "cast")
			if err != nil {
				return api.LongformJSON{}, err
			}
			if d.VoiceCast == nil {
				d.VoiceCast = map[string]string{}
			}
			d.VoiceCast[cn] = ref
		}
		bookDialogueDefault(&d) // api_studio_longform_dialogue.go
	} else {
		if err := b.storyLines(&d, req, src, voices, e, lfv); err != nil {
			return api.LongformJSON{}, err
		}
	}
	if strings.TrimSpace(d.Title) == "" {
		d.Title = firstWords(strings.TrimLeft(services.StoryScript(d.Lines)+d.Script, "# "), 6)
	}
	if strings.TrimSpace(d.Title) == "" {
		d.Title = "Untitled"
	}
	saved, err := b.lib.svc.Library.SaveDoc(ctx, core.Project{Kind: kind, Title: d.Title}, d)
	if err != nil {
		return api.LongformJSON{}, err
	}
	return b.longformJSON(ctx, saved, d, voices)
}

// storyLines fills a story's lines and cast from lines, or from text as
// Auto-cast (default) or Split into lines does.
func (b *apiBackend) storyLines(d *services.LongformDoc, req api.LongformCreate, src string, voices []core.Voice, e catalog.Entry, lfv []lfVoice) error {
	// The cast the agent gave, first: Auto-cast keeps known names.
	cast := map[string]string{} // lower-case name → voice ref
	for name, v := range req.Cast {
		ref, err := voiceRef(voices, e, v, "cast")
		if err != nil {
			return err
		}
		cast[strings.ToLower(strings.TrimSpace(name))] = ref
		d.Cast = append(d.Cast, services.CastMember{ID: core.NewID(time.Now()), Name: strings.TrimSpace(name), Voice: ref})
	}
	member := func(name string) string {
		k := strings.ToLower(strings.TrimSpace(name))
		for _, m := range d.Cast {
			if strings.ToLower(m.Name) == k {
				return m.ID
			}
		}
		id := core.NewID(time.Now())
		d.Cast = append(d.Cast, services.CastMember{ID: id, Name: strings.TrimSpace(name)})
		return id
	}
	switch {
	case len(req.Lines) > 0:
		for i, l := range req.Lines {
			if strings.TrimSpace(l.Text) == "" {
				return errBadRequest("lines", "Line %d has no text.", i+1)
			}
			line := services.StoryLine{ID: newLineID(), Text: strings.TrimSpace(l.Text), Speed: l.Speed, PauseAfter: max(l.PauseAfter, 0), Chapter: l.Chapter}
			if l.Speed != 0 && (l.Speed < 0.5 || l.Speed > 2) {
				return errBadRequest("lines", "Line %d: speed is 0.5–2.", i+1)
			}
			if !l.Chapter {
				if s := strings.TrimSpace(l.Speaker); s != "" {
					line.Character = member(s)
				}
				if l.Voice != "" {
					ref, err := voiceRef(voices, e, l.Voice, "lines")
					if err != nil {
						return err
					}
					line.Voice = ref
				}
			}
			d.Lines = append(d.Lines, line)
		}
		// Characters without a voice get one as Auto-cast gives them.
		pick := castPicker(d, lfv)
		for i := range d.Cast {
			if d.Cast[i].Voice == "" {
				d.Cast[i].Voice = pick()
			}
		}
	case req.Split == "" || req.Split == "speakers":
		if n, _ := autoCastText(d, strings.TrimSpace(src), lfv); n == 0 {
			return errBadRequest("text", "There is nothing to say in the text.")
		}
	default:
		d.SplitBy = req.Split
		if splitStoryText(d, strings.TrimSpace(src)) == 0 {
			return errBadRequest("text", "There is nothing to say in the text.")
		}
		if d.Voice == "" && len(lfv) > 0 {
			d.Voice = lfv[0].ref
		}
	}
	return nil
}

// Longform reads a project.
func (b *apiBackend) Longform(ctx context.Context, id string) (api.LongformJSON, error) {
	p, d, err := b.loadLongform(ctx, id)
	if err != nil {
		return api.LongformJSON{}, err
	}
	voices, err := b.voices(ctx)
	if err != nil {
		return api.LongformJSON{}, err
	}
	return b.longformJSON(ctx, p, d, voices)
}

// loadLongform reads a Stories or Audiobook project.
func (b *apiBackend) loadLongform(ctx context.Context, id string) (core.Project, services.LongformDoc, error) {
	p, d, err := b.lib.svc.Library.LoadDoc(ctx, id)
	if err != nil {
		return p, d, notFound(err, "project", id)
	}
	if p.Kind != core.ProjectStory && p.Kind != core.ProjectAudiobook {
		return p, d, errBadRequest("project_id", "%s is a %s project, not a story or an audiobook.", id, p.Kind)
	}
	return p, d, nil
}

// compileFor compiles a document with the saved voices.
func (b *apiBackend) compileFor(d *services.LongformDoc, voices []core.Voice) (services.LongformSpec, []services.Problem) {
	return services.Compile(d, lfResolverOf(d, voices, b.fill, b.lib.svc.Voices.FilePath))
}

// longformJSON is a project as agents see it.
func (b *apiBackend) longformJSON(ctx context.Context, p core.Project, d services.LongformDoc, voices []core.Voice) (api.LongformJSON, error) {
	spec, probs, blocks := b.compileBook(&d, voices)
	e := entryFor(d.Model)
	out := api.LongformJSON{ID: p.ID, Kind: kindName(p.Kind), Title: firstOf(d.Title, p.Title), Model: e.ID, Voice: d.Voice,
		VoiceName: voiceLabel(voices, d.Voice), Language: d.Language, Format: firstOf(d.Format, services.FormatWAV), Speed: d.Speed, Stems: d.Stems,
		Chapters: []api.ChapterJSON{}, Updated: p.UpdatedAt,
		Book: api.BookJSON{Author: d.Book.Author, Narrator: d.Book.Narrator, Year: d.Book.Year, Genre: d.Book.Genre,
			Description: d.Book.Description, Cover: d.Book.Cover, Loudness: d.Book.Loudness}}
	for i, ch := range spec.Chapters {
		c := api.ChapterJSON{Index: i + 1, Title: ch.Title, Lines: len(ch.Lines)}
		for _, l := range ch.Lines {
			for _, sp := range l.Spans {
				c.Chars += len([]rune(sp.Text))
			}
		}
		out.Chapters = append(out.Chapters, c)
	}
	script := d.Script
	if p.Kind == core.ProjectStory {
		script = services.StoryScript(d.Lines)
		names := map[string]string{}
		count := map[string]int{}
		for _, l := range d.Lines {
			count[l.Character]++
		}
		for _, m := range d.Cast {
			names[m.ID] = m.Name
			out.Cast = append(out.Cast, api.CastJSON{Name: m.Name, Voice: m.Voice, VoiceName: voiceLabel(voices, m.Voice), Lines: count[m.ID],
				Persona: m.Persona})
		}
		for _, l := range d.Lines {
			out.Lines = append(out.Lines, api.StoryLineJSON{ID: l.ID, Text: l.Text, Speaker: names[l.Character], Voice: l.Voice,
				Speed: l.Speed, PauseAfter: l.PauseAfter, Chapter: l.Chapter, Direction: l.Direction, Intensity: l.Intensity,
				Emotion: l.Emotion, Turn: l.Turn})
		}
		out.Scene, out.Pacing, out.Continuity = d.Scene, apiPacing(d.Production.Pacing), storyContinuity(d.Model)
	} else {
		out.Cast = bookCastJSON(&d, voices) // api_studio_longform_dialogue.go
		if len(d.Script) <= maxScriptJSON {
			out.Script = d.Script
		}
		out.DialogueBlocks = b.dialogueBlocksJSON(&d, blocks)
	}
	out.Dialogue = d.Production.Dialogue
	if d.Production.Dialogue {
		if e, _, why := dialogueChoice(readSysInfo().MemoryGB(), &d); why == "" {
			out.DialogueModel = dialogueModelName(e)
		}
	}
	st := services.StatsOf(script, d.Speed)
	out.Words, out.Estimate = st.Words, float64(int(st.Seconds))
	for _, pr := range probs {
		pj := api.ProblemJSON{Code: pr.Code, Message: problemMessage(pr), Blocking: pr.Blocking}
		if pr.Chapter >= 0 {
			pj.Chapter = pr.Chapter + 1
		}
		if p.Kind == core.ProjectStory && pr.Line >= 0 && pr.Line < len(d.Lines) {
			pj.Line = d.Lines[pr.Line].ID
		}
		out.Problems = append(out.Problems, pj)
	}
	if d.Render != "" {
		if r, body, err := b.lib.svc.Longform.Get(ctx, d.Render); err == nil {
			rj := renderJSON(r, &body, len(spec.Chapters))
			out.Render = &rj
		}
	}
	return out, nil
}

// kindName is the API's name of a project kind.
func kindName(k core.ProjectKind) string {
	if k == core.ProjectStory {
		return "story"
	}
	return "audiobook"
}

// problemMessage is a compiler problem in English (the API's language).
func problemMessage(p services.Problem) string {
	switch p.Code {
	case services.ProblemNoModel:
		return "Choose a model."
	case services.ProblemNothingToSay:
		return "There is no spoken line to render."
	case services.ProblemNoVoice:
		if l := p.Params["line"]; l != "" {
			return "Line " + l + " has no voice: set a narrator, or give it a voice."
		}
		return "Set a narrator (voice), or give every line a voice."
	case services.ProblemMissingVoice:
		return "The voice " + p.Params["voice"] + " is gone or does not fit this model: choose another."
	case services.ProblemUnknownVoice:
		return "Nobody is cast as " + p.Params["voice"] + ": the narrator reads it (set it in cast)."
	case services.ProblemEmptyChapter:
		return "Chapter " + p.Params["title"] + " has no spoken text."
	case services.ProblemUnknownTag:
		return "Unrecognised tag " + p.Params["tag"] + ": it will be read aloud."
	case services.ProblemDuplicateWord:
		return p.Params["word"] + " is listed twice in the pronunciations."
	case services.ProblemDialogueUnclosed:
		if p.Params["at"] == "heading" {
			return "A dialogue block runs into a heading: it ends before it. Close it with [/dialogue]."
		}
		return "A dialogue block is not closed: it runs to the end of its chapter. Close it with [/dialogue]."
	case services.ProblemDialogueNested:
		return "A [dialogue] inside a dialogue block is ignored."
	case services.ProblemDialogueStrayClose:
		return "A [/dialogue] outside a dialogue block is ignored."
	case services.ProblemDialogueEmpty:
		return "An empty dialogue block is ignored."
	case services.ProblemDialogueUncast:
		return "No voice for " + p.Params["names"] + " in a dialogue block: render_longform gives each a voice of its own (set them in cast)."
	case services.ProblemDialogueUnknown:
		return p.Params["count"] + " lines of a dialogue block have no speaker (?:): the narrator reads them."
	case services.ProblemDialogueOneSpeaker:
		return "A dialogue block has one speaker: it is read line by line."
	case services.ProblemDialogueLanguage:
		return "A dialogue block is not in Chinese or English (" + p.Params["lang"] + "): it is read line by line."
	case services.ProblemDialogueTags:
		return "The dialogue model leaves out " + p.Params["tags"] + " in a dialogue block."
	}
	return p.Code
}

// renderJSON is a render as agents see it; chapters is how many the
// project has (fewer in the render: a partial one).
func renderJSON(r core.Render, body *services.RenderBody, chapters int) api.RenderJSON {
	j := api.RenderJSON{ID: r.ID, JobID: services.OwnerLongform + r.ID, State: string(r.State), Created: r.CreatedAt,
		Partial: chapters > 0 && len(body.Chapters) < chapters}
	for _, c := range body.Chapters {
		rc := api.RenderChapterOut{Title: c.Title, State: c.State, File: c.File, Seconds: round1(c.AudioSeconds)}
		if c.Error != nil {
			rc.Error = c.Error.Code + ": " + c.Error.Message
		}
		j.Chapter = append(j.Chapter, rc)
	}
	if body.Error != nil {
		j.Error = body.Error.Code + ": " + body.Error.Message
	}
	if o := body.Output; o != nil {
		j.Book, j.M4B, j.Cues, j.Seconds, j.Encode = o.Book, o.Encoded, o.Cues, round1(o.Seconds), o.EncodeError
		for i, c := range o.Chapters {
			if i < len(j.Chapter) {
				j.Chapter[i].File, j.Chapter[i].Seconds = c.File, round1(c.Seconds)
			}
		}
		for _, s := range o.Stems {
			if j.Stems == nil {
				j.Stems = map[string]string{}
			}
			j.Stems[s.Character] = s.File
		}
	}
	return j
}

func round1(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }

// apiPacing is a pacing's name in the API ("fixed" for none).
func apiPacing(p string) string {
	if p == services.PacingFixed {
		return "fixed"
	}
	return p
}
