package main

// Audiobooks' dialogue blocks for agents (thread #88): the manuscript goes
// in and out as text (update_longform script, get_longform script), its
// blocks are listed with how each is made (get_longform dialogue_blocks),
// the dialogue model is set as a story's is (dialogue, dialogue_model), a
// block can be previewed (render_longform dialogue_block), and speakers
// without a voice get their own when the book renders.

import (
	"encoding/json"
	"strings"

	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/text/markup"
)

// maxScriptJSON is the longest manuscript get_longform returns.
const maxScriptJSON = 100_000

// setDialogue applies an update's dialogue and dialogue_model.
func setDialogue(d *services.LongformDoc, req api.LongformUpdate) error {
	if req.DialogueModel != "" {
		e, ok := dialogueByName(req.DialogueModel)
		if !ok {
			return errBadRequest("dialogue_model", "dialogue_model is soulx, moss or vibevoice.")
		}
		if why := dialogueBlockedOn(readSysInfo().MemoryGB(), d, e); why != "" {
			return errBadRequest("dialogue_model", "%s", why)
		}
		d.Production.DialogueModel = e.Repo
	}
	if req.Dialogue != nil {
		d.Production.Dialogue = *req.Dialogue
		if *req.Dialogue {
			e, _, why := dialogueChoice(readSysInfo().MemoryGB(), d)
			if why != "" {
				return errBadRequest("dialogue", "%s", why)
			}
			d.Production.DialogueModel = e.Repo
		}
	}
	return nil
}

// bookDialogueDefault turns whole dialogue on for an audiobook with
// dialogue blocks nobody chose a dialogue model for, as the page does.
func bookDialogueDefault(d *services.LongformDoc) {
	if d.Kind != core.ProjectAudiobook || d.Production.DialogueModel != "" || !strings.ContainsAny(d.Script, "[【") {
		return
	}
	if blocks, _ := markup.DialogueBlocks(d.Script); len(blocks) == 0 {
		return
	}
	if e, _, why := dialogueChoice(readSysInfo().MemoryGB(), d); why == "" {
		d.Production.Dialogue, d.Production.DialogueModel = true, e.Repo
	}
}

// castRef is the voice a manuscript's name is cast as (VoiceCast, names
// compared as markup.NameKey does).
func castRef(cast map[string]string, name string) string {
	if ref, ok := cast[name]; ok {
		return ref
	}
	for n, ref := range cast {
		if markup.NameKey(n) == markup.NameKey(name) {
			return ref
		}
	}
	return ""
}

// bookDialogueModel is the dialogue model an audiobook renders its blocks
// with, or why none ("" when it can): dialogue_off, memory, language,
// not_downloaded.
func (b *apiBackend) bookDialogueModel(d *services.LongformDoc) (catalog.Entry, string) {
	gb := readSysInfo().MemoryGB()
	e, _, why := dialogueChoice(gb, d)
	switch {
	case !d.Production.Dialogue:
		return e, "dialogue_off"
	case why != "" && gb > 0 && float64(gb) < e.MinMemoryGB:
		return e, "memory"
	case why != "":
		return e, "language"
	}
	if ready, known := b.onDisk(e); known && !ready {
		return e, "not_downloaded"
	}
	return e, ""
}

// dialogueBlocksJSON are an audiobook's blocks as agents see them.
func (b *apiBackend) dialogueBlocksJSON(d *services.LongformDoc, blocks []services.DialogueBlockInfo) []api.DialogueBlockJSON {
	if len(blocks) == 0 {
		return nil
	}
	_, why := b.bookDialogueModel(d)
	var out []api.DialogueBlockJSON
	for _, bl := range blocks {
		j := api.DialogueBlockJSON{Chapter: bl.Chapter + 1, Index: bl.Index, Start: bl.Span.Start, End: bl.Span.End,
			Speakers: bl.Speakers, Lines: bl.Turns, Uncast: bl.Uncast, Mode: "dialogue"}
		if j.Speakers == nil {
			j.Speakers = []string{}
		}
		switch {
		case !bl.Language:
			j.Reason = "language"
		case len(bl.Speakers) < 2:
			j.Reason = "one_speaker"
		default:
			j.Reason = why
		}
		if j.Reason != "" {
			j.Mode = "per_line"
		}
		out = append(out, j)
	}
	return out
}

// bookCastJSON is an audiobook's cast: its [voice:] names and its blocks'
// speakers.
func bookCastJSON(d *services.LongformDoc, voices []core.Voice) []api.CastJSON {
	var out []api.CastJSON
	inBlocks := services.DialogueNames(d.Script)
	for _, n := range services.CastNames(d.Script) {
		ref := castRef(d.VoiceCast, n)
		c := api.CastJSON{Name: n, Voice: ref, VoiceName: voiceLabel(voices, ref)}
		for _, x := range inBlocks {
			if markup.NameKey(x) == markup.NameKey(n) {
				c.Dialogue = true
			}
		}
		out = append(out, c)
	}
	return out
}

// castBookUncast gives the blocks' uncast speakers voices of their own in
// d (name → voice; the caller saves it once the render started).
func (b *apiBackend) castBookUncast(p core.Project, d *services.LongformDoc, voices []core.Voice) map[string]string {
	if p.Kind != core.ProjectAudiobook {
		return nil
	}
	_, _, blocks := b.compileBook(d, voices)
	got := castUncast(d, blocks, lfVoicesOf(voices, entryFor(d.Model)))
	if len(got) == 0 {
		return nil
	}
	given := map[string]string{}
	for _, g := range got {
		name, voice, _ := strings.Cut(g, ": ")
		given[name] = voice
	}
	return given
}

// compileBook compiles a document with the saved voices, with its blocks.
func (b *apiBackend) compileBook(d *services.LongformDoc, voices []core.Voice) (services.LongformSpec, []services.Problem, []services.DialogueBlockInfo) {
	return services.CompileBook(d, lfResolverOf(d, voices, b.fill, b.lib.svc.Voices.FilePath))
}

// withGiven adds the voices given to uncast speakers to a render job's
// result ("voices_given").
func withGiven(j api.JobJSON, given map[string]string) api.JobJSON {
	if len(given) == 0 {
		return j
	}
	res := map[string]any{}
	json.Unmarshal(j.Result, &res)
	res["voices_given"] = given
	if data, err := json.Marshal(res); err == nil {
		j.Result = data
	}
	return j
}

// specHasBlocks reports whether a compiled book has dialogue blocks.
func specHasBlocks(spec *services.LongformSpec) bool {
	for _, ch := range spec.Chapters {
		for _, l := range ch.Lines {
			if l.Block > 0 {
				return true
			}
		}
	}
	return false
}
