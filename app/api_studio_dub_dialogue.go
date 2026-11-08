package main

// Multi-speaker dialogue in the dub's API (thread #89, plan §2.8):
// create_dub, edit_dub and batch_dub take dialogue ("auto" | "off") and
// dialogue_model ("soulx"), edit_dub a line's no_dialogue; get_dub says
// whether a track's conversations are recorded together (and why not),
// lists them, and says of each line which conversation it is in and how
// its audio was made (made_by). run_dub segments naming a conversation
// line makes it again by continuation (the dub service).

import (
	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/dialogue"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/services"
)

// dubDialogueMode is a dialogue setting of the API ("auto", "off"; ""
// unchanged) as a project's, or a bad request.
func dubDialogueMode(mode string) (string, error) {
	switch mode {
	case "":
		return "", nil
	case dub.DialogueAuto, "on":
		return dub.DialogueAuto, nil
	case dub.DialogueOff:
		return dub.DialogueOff, nil
	}
	return "", errBadRequest("dialogue", "dialogue is auto or off.")
}

// applyDubDialogue sets a project's dialogue settings from the API's.
func applyDubDialogue(s *dub.Settings, mode, model string) error {
	m, err := dubDialogueMode(mode)
	if err != nil {
		return err
	}
	if m != "" {
		s.Dialogue = m
	}
	if model != "" {
		e, ok := dialogueByName(model)
		if !ok || services.OneTrackDialogue(e.Family) {
			return errBadRequest("dialogue_model", "dialogue_model is soulx: MOSS-TTSD and VibeVoice do not dub yet.")
		}
		s.DialogueModel = e.Repo
	}
	return nil
}

// dubDialogueJSON fills a dub's dialogue fields for track l.
func (b *apiBackend) dubDialogueJSON(out *api.DubJSON, p *dub.Project, l string) {
	out.Dialogue = p.Settings.Dialogue
	if out.Dialogue == "" {
		out.Dialogue = dub.DialogueOff
	}
	es := dubDialogueEntries()
	if len(es) > 0 {
		e := es[0]
		for _, x := range es {
			if x.Repo == p.Settings.DialogueModel {
				e = x
			}
		}
		out.DialogueModel = dialogueModelName(e)
	}
	if l == "" {
		return
	}
	m := dubModels(b.lib)
	if out.DialogueBlocked = m.DialogueBlocked(p, l); out.DialogueBlocked != "" {
		return
	}
	d, _ := m.DialogueFor(p.Settings.DialogueModel)
	sts := p.DialoguePreview(l, dialogue.LimitsFor(d.Family))
	t := p.Track(l)
	for _, st := range sts {
		out.DialogueStretches = append(out.DialogueStretches, api.DubStretchJSON{Dialogue: st.N, Segments: st.IDs, Speakers: len(st.Voices)})
	}
	for i := range out.Segments {
		sj := &out.Segments[i]
		if st, ok := dub.StretchOf(sts, sj.ID); ok {
			sj.Dialogue = st.N
		}
		r, ok := t.Lines[sj.ID]
		if !ok || r.File == "" || r.Err != "" {
			continue
		}
		sj.MadeBy = "single"
		if r.Via == dub.ViaDialogue {
			sj.MadeBy = "dialogue"
		}
		sj.FallbackReason, sj.Flag, sj.Heard = r.FallbackReason, r.Flag, r.Heard
	}
}
