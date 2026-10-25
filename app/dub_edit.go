package main

// Edits of the open dub: every one is applied to the working copy and to
// the saved document (dubSave), after an undo snapshot (dubHist; typing
// in one line makes one step).

import (
	"os"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/translate"
)

// dubDo applies an edit of the transcript or the cast: fn reports
// whether it changed anything.
func (a *app) dubDo(label string, fn func(*dub.Project) bool) bool {
	p := a.dub
	if p == nil || a.dubLocked() {
		return false
	}
	snap := dub.Take(p, label)
	if !fn(p) {
		return false
	}
	if !a.dubHist.Coalesce(label) || !strings.HasPrefix(label, "type:") {
		a.dubHist.Push(snap)
	}
	a.dubSave(func(q *dub.Project) { fn(q) })
	return true
}

// dubUndo and dubRedo step through the history; the saved document
// takes the result.
func (a *app) dubUndo() { a.dubStep2(a.dubHist.Undo) }
func (a *app) dubRedo() { a.dubStep2(a.dubHist.Redo) }

func (a *app) dubStep2(step func(*dub.Project) bool) {
	p := a.dub
	if p == nil || a.dubLocked() || !step(p) {
		return
	}
	after := dub.Take(p, "")
	a.dubEdit = ""
	a.dubSave(func(q *dub.Project) { after.Restore(q) })
}

// --- lines ----------------------------------------------------------------------------

// dubBeginEdit opens a line's text for editing, in lang ("" = the source).
func (a *app) dubBeginEdit(id, lang string) {
	p := a.dub
	if p == nil || a.dubLocked() {
		return
	}
	s, ok := p.Segment(id)
	if !ok {
		return
	}
	a.dubCommitEdit()
	a.dubEdit, a.dubEditL, a.dubCur = id, lang, id
	a.dubBuf = shownText(p, *s, lang)
}

// shownText is the text a line shows in lang: its translation, or the
// source it is not translated to yet (the same text dubLineText shows),
// so the editor opens on what the user clicked.
func shownText(p *dub.Project, s dub.Segment, lang string) string {
	if lang != "" {
		if tr := p.TextFor(s, lang); strings.TrimSpace(tr) != "" {
			return tr
		}
	}
	return s.Text
}

// dubCommitEdit keeps the text being edited.
func (a *app) dubCommitEdit() {
	id, l, text := a.dubEdit, a.dubEditL, a.dubBuf
	if id == "" {
		return
	}
	a.dubEdit = ""
	if l != "" {
		if s, ok := a.dub.Segment(id); ok {
			// Done without a change to the source shown for an untranslated
			// line keeps it untranslated.
			if strings.TrimSpace(a.dub.TextFor(*s, l)) == "" && text == s.Text {
				return
			}
			l = dub.LangFor(*s, l)
			if l == a.dub.SourceLanguage() {
				l = ""
			}
		}
	}
	a.dubDo("type:"+id+":"+l, func(p *dub.Project) bool { return p.SetText(id, l, text) })
}

// dubCancelEdit drops the text being edited.
func (a *app) dubCancelEdit() { a.dubEdit = "" }

// --- the cast -------------------------------------------------------------------------

// dubCast casts a speaker ("" = their automatic clone).
func (a *app) dubCast(speaker, ref string) {
	if a.dubDo("cast", func(p *dub.Project) bool { return p.CastSpeaker(speaker, ref) }) {
		a.toast(i18n.T("Cast %s as %s", a.dubVoiceLabel(ref), a.dub.SpeakerName(speaker)))
	}
}

// dubRenameSpeaker names a speaker.
func (a *app) dubRenameSpeaker(label, name string) {
	name = strings.TrimSpace(name)
	a.dubSet(func(p *dub.Project) {
		if p.Names == nil {
			p.Names = map[string]string{}
		}
		if name == "" || name == label {
			delete(p.Names, label)
		} else {
			p.Names[label] = name
		}
	})
}

// dubVoiceChoice is a voice the casting board and pickers offer.
type dubVoiceChoice struct {
	ref, label, group string
}

// dubVoiceChoices are the voices a dub can use with its model: its
// automatic clone, the default voice, saved voices the model can speak
// with, the design presets, and the model's own speakers.
func (a *app) dubVoiceChoices() []dubVoiceChoice {
	out := []dubVoiceChoice{{"", i18n.T("Auto · their own voice"), ""}, {dub.VoiceDefault, i18n.T("Default voice"), ""}}
	if a.dub == nil {
		return out
	}
	e := entryFor(a.dub.Settings.Model)
	if e.NeedsReference {
		// It only clones: no default voice, no designs.
		out = out[:1]
	}
	for i := range a.voices {
		v := &a.voices[i]
		if ok, _ := voiceUsable(v, e); ok {
			out = append(out, dubVoiceChoice{v.ID, v.Name, i18n.T("Saved voices")})
		}
	}
	for _, pr := range dubPresets() {
		if e.NeedsReference {
			break
		}
		out = append(out, dubVoiceChoice{dub.PresetPrefix + pr.ID, pr.Name.In(i18n.Locale()), i18n.T("Design presets")})
	}
	for _, id := range e.VoiceIDsIn(i18n.Locale()) {
		out = append(out, dubVoiceChoice{dub.SpeakerPrefix + id, id, i18n.T("Model voices")})
	}
	return out
}

// dubVoiceLabel names a voice reference.
func (a *app) dubVoiceLabel(ref string) string {
	switch {
	case ref == "" || ref == dub.VoiceAuto:
		return i18n.T("Auto")
	case ref == dub.VoiceDefault:
		return i18n.T("Default voice")
	case strings.HasPrefix(ref, dub.SpeakerPrefix):
		return strings.TrimPrefix(ref, dub.SpeakerPrefix)
	case strings.HasPrefix(ref, dub.PresetPrefix):
		// Hidden presets too: a cast may name one offered before.
		if pr, ok := dubPreset(strings.TrimPrefix(ref, dub.PresetPrefix)); ok {
			return pr.Name.In(i18n.Locale())
		}
	case strings.HasPrefix(ref, dub.DesignPrefix):
		return strings.TrimPrefix(ref, dub.DesignPrefix)
	case strings.HasPrefix(ref, dub.GalleryPrefix):
		if arch, ok := galleryArchetype(strings.TrimPrefix(ref, dub.GalleryPrefix)); ok {
			return arch.Name.In(i18n.Locale())
		}
	}
	if v := a.findVoice(ref); v != nil {
		return v.Name
	}
	if strings.Contains(ref, ":") {
		return i18n.T("Missing voice") // a preset or gallery voice this version lacks
	}
	return voiceGone() // a saved voice deleted since (voices_delete.go)
}

// --- glossary ----------------------------------------------------------------------------

// dubGlossState is the glossary panel's form.
type dubGlossState struct {
	open             bool
	src, tgt, note   string
	editing          int // index being edited; -1 none
	eSrc, eTgt, eNot string
	extracting       bool
}

// dubAddTerm adds a glossary term.
func (a *app) dubAddTerm() {
	g := &a.dubGloss
	src, tgt := strings.TrimSpace(g.src), strings.TrimSpace(g.tgt)
	if src == "" || tgt == "" {
		return
	}
	term := translate.Term{Source: src, Target: tgt, Note: strings.TrimSpace(g.note)}
	a.dubSet(func(p *dub.Project) { p.Glossary = append(p.Glossary, term).Normalize() })
	g.src, g.tgt, g.note = "", "", ""
}

// dubSaveTerm keeps an edited term.
func (a *app) dubSaveTerm(i int) {
	g := &a.dubGloss
	term := translate.Term{Source: strings.TrimSpace(g.eSrc), Target: strings.TrimSpace(g.eTgt), Note: strings.TrimSpace(g.eNot)}
	g.editing = -1
	if term.Source == "" || term.Target == "" {
		return
	}
	a.dubSet(func(p *dub.Project) {
		if i < len(p.Glossary) {
			p.Glossary[i] = term
			p.Glossary = p.Glossary.Normalize()
		}
	})
}

// dubDeleteTerm removes a term.
func (a *app) dubDeleteTerm(i int) {
	a.dubSet(func(p *dub.Project) {
		if i < len(p.Glossary) {
			p.Glossary = slices.Delete(p.Glossary, i, i+1)
		}
	})
}

// dubExtractTerms asks the language model for terms (Auto).
func (a *app) dubExtractTerms() {
	p := a.dub
	if p == nil || a.llmRouter == nil {
		return
	}
	x := &translate.Extractor{Router: a.llmRouter}
	var segs []translate.Segment
	for _, s := range p.Segments {
		segs = append(segs, translate.Segment{ID: s.ID, Text: s.Text})
	}
	src, tgt, existing, style := p.SourceLanguage(), a.dubLang, p.Glossary, p.Settings.Style
	a.dubGloss.extracting = true
	a.background(func() {
		cands, _, err := x.Extract(bgCtx(), src, tgt, segs, existing, style)
		a.post(func() {
			a.dubGloss.extracting = false
			if err != nil {
				a.toastWarn(i18n.T("Could not find terms: %v", err))
				return
			}
			if len(cands) == 0 {
				a.toast(i18n.T("Auto-extract found no new terms."))
				return
			}
			var auto translate.Glossary
			for _, c := range cands {
				t := c.Term
				t.Auto = true
				auto = append(auto, t)
			}
			a.dubSet(func(p *dub.Project) { p.Glossary = p.Glossary.Merge(auto) })
			a.toast(i18n.N(len(auto), "Added %d term", "Added %d terms", len(auto)))
		})
	})
}

// --- paste a translation ----------------------------------------------------------------

// dubPasteState is the Paste translation panel.
type dubPasteState struct {
	open    bool
	text    string
	parsed  string // the text the preview is of
	preview dub.PastePreview
	err     string
}

// dubPastePreview parses the pasted text (when it changed).
func (a *app) dubPastePreview() {
	ps := &a.dubPaste
	if a.dub == nil || ps.text == ps.parsed {
		return
	}
	ps.parsed = ps.text
	mode, entries := dub.ParsePaste(ps.text)
	ps.preview = a.dub.MatchPaste(mode, entries, a.dubLang)
}

// dubApplyPaste writes the pasted lines into the language shown.
func (a *app) dubApplyPaste() {
	pv := a.dubPaste.preview
	l := a.dubLang
	n := 0
	a.dubDo("paste", func(p *dub.Project) bool {
		n = p.ApplyPaste(pv, l)
		return n > 0
	})
	a.dubPaste = dubPasteState{}
	a.toast(i18n.N(n, "Pasted %d line", "Pasted %d lines", n))
}

// dubPasteFile loads a subtitle or text file into the paste panel.
func (a *app) dubPasteFile() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: i18n.T("Load a translation"),
			Filters: []mygo.FileFilter{{Name: i18n.T("Subtitles or text"), Extensions: []string{"srt", "vtt", "txt"}}}})
		if err != nil || len(paths) == 0 {
			return
		}
		data, err := os.ReadFile(paths[0])
		a.post(func() {
			if err != nil {
				a.dubPaste.err = err.Error()
				return
			}
			a.dubPaste.text = decodeText(data)
		})
	}()
}

// --- importing subtitles as the transcript ---------------------------------------------

// dubImportSubtitles replaces the transcript with a subtitle file.
func (a *app) dubImportSubtitles() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: i18n.T("Import subtitles"),
			Filters: []mygo.FileFilter{{Name: i18n.T("Subtitles"), Extensions: []string{"srt", "vtt"}}}})
		if err != nil || len(paths) == 0 {
			return
		}
		data, err := os.ReadFile(paths[0])
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not read the file: %v", err))
				return
			}
			a.dubImportSubtitleText(decodeText(data))
		})
	}()
}

// dubImportSubtitleText replaces the transcript with subtitles.
func (a *app) dubImportSubtitleText(text string) {
	if a.dub == nil {
		return
	}
	var st dub.ImportStats
	d := a.dub.Source.Duration
	a.dubDo("import", func(p *dub.Project) bool {
		st = p.ImportSubtitles(text, d)
		return true
	})
	a.dubHist.Reset()
	msg := i18n.N(st.Imported, "Imported %d cue", "Imported %d cues", st.Imported)
	var extra []string
	if st.Malformed > 0 {
		extra = append(extra, i18n.N(st.Malformed, "%d skipped (malformed)", "%d skipped (malformed)", st.Malformed))
	}
	if st.Dropped > 0 {
		extra = append(extra, i18n.N(st.Dropped, "%d dropped (overlap)", "%d dropped (overlap)", st.Dropped))
	}
	if st.Clamped > 0 {
		extra = append(extra, i18n.N(st.Clamped, "%d clamped to the media's length", "%d clamped to the media's length", st.Clamped))
	}
	if len(extra) > 0 {
		msg += " · " + strings.Join(extra, " · ")
	}
	a.dubNotice = msg
	if a.dubStep < dubStepTranscribe {
		a.dubStep = dubStepTranscribe
	}
}
