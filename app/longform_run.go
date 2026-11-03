package main

// Longform's renders: following them (core.RenderChanged), blockers and
// problems, start / stop / resume, and auditions; split from longform.go.

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/store"
)

// loadRenders reads the recent renders, and settles the ones the last run
// left as running.
func (a *app) loadRenders() {
	svc := a.lib.svc.Longform
	a.background(func() {
		ctx := context.Background()
		if err := svc.Reconcile(ctx); err != nil {
			a.post(func() { a.toastWarn(i18n.T("Could not read the renders: %v", err)) })
		}
		a.lib.pruneJobs(ctx) // jobs_prune.go: after Reconcile, which reads the jobs of renders left running
		l, err := svc.List(ctx, store.RenderQuery{Limit: 50})
		a.post(func() {
			if err != nil {
				return
			}
			for _, r := range l {
				if _, ok := a.lfRenders[r.ID]; !ok {
					a.putRender(r)
				}
			}
			a.sortRecent()
		})
	})
}

// putRender keeps a render's latest state.
func (a *app) putRender(r core.Render) *lfRender {
	b, err := services.DecodeRender(r)
	if err != nil {
		return nil
	}
	lr := &lfRender{rec: r, body: b, name: renderName(r, &b.Spec)}
	if old := a.lfRenders[r.ID]; lr.name == "" && old != nil { // a published body has no chapters
		lr.name = old.name
	}
	lr.body.Spec.Chapters, lr.body.Spec.Voices = nil, nil // the pages need the summary only
	a.lfRenders[r.ID] = lr
	return lr
}

func (a *app) sortRecent() {
	a.lfRecent = a.lfRecent[:0]
	for id, r := range a.lfRenders {
		if !r.rec.Audition {
			a.lfRecent = append(a.lfRecent, id)
		}
	}
	slices.SortFunc(a.lfRecent, func(x, y string) int { return strings.Compare(y, x) })
}

// renderChanged follows a render.
func (a *app) renderChanged(ev core.RenderChanged) {
	if ev.Deleted {
		delete(a.lfRenders, ev.Render.ID)
		a.sortRecent()
		a.projects.dirty = true
		return
	}
	old := a.lfRenders[ev.Render.ID]
	lr := a.putRender(ev.Render)
	if lr == nil {
		return
	}
	if old == nil {
		a.sortRecent()
	}
	if old != nil && old.rec.State == core.RenderRunning && lr.rec.State != core.RenderRunning {
		a.renderEnded(lr)
	}
}

// renderEnded tells how a render ended, and plays a finished audition.
func (a *app) renderEnded(r *lfRender) {
	a.projects.dirty = true
	if r.rec.Audition {
		if au, ok := a.lfAuditions[r.rec.ID]; ok && r.rec.State == core.RenderDone && len(r.body.Chapters) > 0 {
			if f := r.body.Chapters[0].File; f != "" {
				a.pl.Stop()
				a.pl.Toggle("audition:"+r.rec.ID, f)
			}
			_ = au
		} else if r.rec.State == core.RenderFailed {
			a.toastWarn(i18n.T("The preview failed: %s", renderErrorText(&r.body)))
		}
		return
	}
	switch r.rec.State {
	case core.RenderDone:
		a.notify(core.SeveritySuccess, i18n.T("“%s” is ready", r.rec.Title), "", "/"+lfRoute(r.rec.Kind))
	case core.RenderFailed:
		a.notify(core.SeverityError, i18n.T("“%s” did not finish", r.rec.Title), renderErrorText(&r.body), "/"+lfRoute(r.rec.Kind))
	}
}

// lfRoute is the page of a project kind.
func lfRoute(k core.ProjectKind) string {
	if k == core.ProjectAudiobook {
		return "audiobook"
	}
	return "stories"
}

// activeRender is the full render running now, if any.
func (a *app) activeRender() *lfRender {
	for _, id := range a.lfRecent {
		if r := a.lfRenders[id]; r != nil && r.rec.State == core.RenderRunning {
			return r
		}
	}
	return nil
}

// renderErrorText explains why a render failed.
func renderErrorText(b *services.RenderBody) string {
	if b.Error != nil {
		return jobErrorText(b.Error)
	}
	for i, c := range b.Chapters {
		if c.State == services.ChapterFailed && c.Error != nil {
			return i18n.T("Chapter %d: %s", i+1, jobErrorText(c.Error))
		}
	}
	return i18n.T("Some chapters did not render.")
}

// lfBlocker is the one reason a page cannot render now, or "".
func (a *app) lfBlocker(p *lfPage) string {
	if r := a.activeRender(); r != nil {
		if r.rec.ID == p.render {
			return ""
		}
		return i18n.T("Another book is rendering: “%s”.", r.rec.Title)
	}
	switch {
	case p.importing:
		return i18n.T("Importing… Generate unlocks when it finishes.")
	case a.engState == engineStarting:
		return i18n.T("The engine is starting…")
	case a.engState == engineFailed:
		return i18n.T("The engine is not running. Restart it on the Engine page.")
	}
	if p.doc.Model == "" {
		return i18n.T("Choose a model.")
	}
	for _, pr := range p.problems {
		if pr.Blocking {
			return problemText(pr)
		}
	}
	return ""
}

// problemText translates a compiler problem.
func problemText(p services.Problem) string {
	switch p.Code {
	case services.ProblemNoModel:
		return i18n.T("Choose a model.")
	case services.ProblemNothingToSay:
		return i18n.T("Add at least one spoken line to generate.")
	case services.ProblemNoVoice:
		if l := p.Params["line"]; l != "" {
			return i18n.T("Line %s has no voice: choose a default voice, or give it one.", l)
		}
		return i18n.T("Choose a default voice, or give every line a voice.")
	case services.ProblemMissingVoice:
		if looksLikeID(p.Params["voice"]) { // a saved voice deleted since (#108)
			return i18n.T("A voice it uses was deleted: choose another one.")
		}
		return i18n.T("The voice “%s” is gone or can't be used with this model: choose another.", p.Params["voice"])
	case services.ProblemUnknownVoice:
		return i18n.T("Unknown voice “%s”: the default voice reads it.", p.Params["voice"])
	case services.ProblemEmptyChapter:
		return i18n.T("Chapter “%s” has no spoken text.", p.Params["title"])
	case services.ProblemUnknownTag:
		return i18n.T("Unrecognised tag %s: it will be read aloud.", p.Params["tag"])
	case services.ProblemDuplicateWord:
		return i18n.T("“%s” is listed twice in the pronunciations: keep one.", p.Params["word"])
	}
	if s := dialogueProblemText(p); s != "" {
		return s
	}
	return p.Code
}

// dialogueProblemText translates a dialogue passage's problem ("" for
// another).
func dialogueProblemText(p services.Problem) string {
	switch p.Code {
	case services.ProblemDialogueUnclosed:
		if p.Params["at"] == "heading" {
			return i18n.T("A dialogue passage runs into a heading: it ends before it. Close it with [/dialogue].")
		}
		return i18n.T("A dialogue passage is not closed: it runs to the end of its chapter. Close it with [/dialogue].")
	case services.ProblemDialogueNested:
		return i18n.T("A dialogue passage inside another is ignored.")
	case services.ProblemDialogueStrayClose:
		return i18n.T("A [/dialogue] without its passage is ignored.")
	case services.ProblemDialogueEmpty:
		return i18n.T("An empty dialogue passage is ignored.")
	case services.ProblemDialogueUncast:
		return i18n.T("No voice yet for %s: they get voices of their own when the book is generated.", p.Params["names"])
	case services.ProblemDialogueUnknown:
		return i18n.T("%s lines of a dialogue passage have no speaker (“？”): the narrator reads them.", p.Params["count"])
	case services.ProblemDialogueOneSpeaker:
		return i18n.T("A dialogue passage has one speaker: it is read line by line.")
	case services.ProblemDialogueLanguage:
		return i18n.T("A dialogue passage is not in Chinese or English: it is read line by line.")
	case services.ProblemDialogueTags:
		return i18n.T("The dialogue model leaves out %s in a dialogue passage.", p.Params["tags"])
	}
	return ""
}

// startLf compiles a page's document and starts its render (or an
// audition of chapter or line only: what is "chapter:<n>", "line:<id>").
func (a *app) startLf(p *lfPage, what string) {
	a.compileLf(p)
	a.autoCastBook(p)                                         // a passage's speakers without a voice (audiobook_dialogue.go)
	if a.lfMakeReferences(p, func() { a.startLf(p, what) }) { // stories_dialogue_refs.go (#83)
		return
	}
	if !strings.HasPrefix(what, "line:") {
		if msg := a.lfBlocker(p); msg != "" && !(what != "" && a.activeRender() != nil) {
			a.toast(msg)
			return
		}
	}
	spec := p.spec
	spec.Project = p.project
	if spec.Title == "" { // a draft's: its first words (render_titles.go)
		spec.Title = firstOf(spec.FirstWords(renderTitleRunes), i18n.T("Untitled"))
	}
	a.bookDialogueSpec(p, &spec)
	switch {
	case strings.HasPrefix(what, "block:"):
		ch, ok := blockSpec(&spec, what)
		if !ok {
			return
		}
		spec.Chapters = []services.ChapterSpec{ch}
		spec.Audition = true
	case strings.HasPrefix(what, "chapter:"):
		n, _ := strconv.Atoi(strings.TrimPrefix(what, "chapter:"))
		if n < 0 || n >= len(spec.Chapters) {
			return
		}
		spec.Chapters = spec.Chapters[n : n+1]
		spec.Audition = true
	case strings.HasPrefix(what, "line:"):
		ch, ok := lineSpec(&spec, strings.TrimPrefix(what, "line:"))
		if !ok {
			a.toast(i18n.T("This line has nothing to say."))
			return
		}
		spec.Chapters = []services.ChapterSpec{ch}
		spec.Audition = true
	}
	if spec.Audition {
		for _, pr := range p.problems {
			if pr.Blocking && pr.Code != services.ProblemNothingToSay && (pr.Line < 0 || what == "line:"+lineID(p, pr.Line)) {
				a.toastWarn(problemText(pr))
				return
			}
		}
	}
	svc := a.lib.svc.Longform
	kind := p.kind
	a.background(func() {
		r, err := svc.Start(context.Background(), spec)
		a.post(func() {
			if err != nil {
				switch {
				case errors.Is(err, core.ErrBusy):
					a.toast(i18n.T("Another book is rendering. Stop it first."))
				default:
					a.toastWarn(i18n.T("Could not start: %v", engineErrorText(err)))
				}
				return
			}
			a.putRender(r)
			if spec.Audition {
				a.lfAuditions[r.ID] = lfAudition{kind: kind, what: what}
				p.auditions[what] = r.ID
				return
			}
			p.render = r.ID
			p.doc.Render = r.ID
			a.sortRecent()
		})
	})
}

// lineID is the id of story line i ("" for an audiobook).
func lineID(p *lfPage, i int) string {
	if i < 0 || i >= len(p.doc.Lines) {
		return ""
	}
	return p.doc.Lines[i].ID
}

// lineSpec is a chapter holding only the line id of spec. A line's
// preview is the line on its own, with its own emotion's instruction, not
// its character's track (services story_track.go): quick, and it never
// makes the track again; the chapter's render says it within its track.
// An edited line makes its track again when the chapter renders.
func lineSpec(spec *services.LongformSpec, id string) (services.ChapterSpec, bool) {
	for _, ch := range spec.Chapters {
		var lines []services.LineSpec
		for _, l := range ch.Lines {
			if l.ID == id {
				l.Paragraph, l.PauseAfter = false, 0
				lines = append(lines, l)
			}
		}
		if len(lines) > 0 {
			return services.ChapterSpec{Lines: lines}, true
		}
	}
	return services.ChapterSpec{}, false
}

// stopLf stops a page's render.
func (a *app) stopLf(p *lfPage) {
	id := p.render
	svc := a.lib.svc.Longform
	a.background(func() {
		if err := svc.Stop(context.Background(), id); err != nil {
			a.post(func() { a.toastWarn(i18n.T("Could not stop: %v", err)) })
		}
	})
}

// resumeLf renders what a stopped or failed render lacks.
func (a *app) resumeLf(p *lfPage, id string) {
	svc := a.lib.svc.Longform
	a.background(func() {
		r, err := svc.Resume(context.Background(), id)
		a.post(func() {
			if err != nil {
				if errors.Is(err, core.ErrBusy) {
					a.toast(i18n.T("Another book is rendering. Stop it first."))
				} else {
					a.toastWarn(i18n.T("Could not resume: %v", engineErrorText(err)))
				}
				return
			}
			a.putRender(r)
			if p != nil {
				p.render = id
			}
		})
	})
}

// auditionState says how a page's audition of what goes: "" (none),
// "running", "done", "failed".
func (a *app) auditionState(p *lfPage, what string) (string, *lfRender) {
	id := p.auditions[what]
	r := a.lfRenders[id]
	if r == nil {
		return "", nil
	}
	switch r.rec.State {
	case core.RenderRunning:
		return "running", r
	case core.RenderDone:
		return "done", r
	}
	return "failed", r
}

// playAudition plays (or pauses) a finished audition.
func (a *app) playAudition(r *lfRender) {
	if len(r.body.Chapters) == 0 || r.body.Chapters[0].File == "" {
		return
	}
	if err := a.pl.Toggle("audition:"+r.rec.ID, r.body.Chapters[0].File); err != nil {
		a.toastWarn(i18n.T("Can't play: %v", err))
	}
}

// looksLikeID reports whether s is an id the library makes (core.NewID:
// 26 letters and digits), not a voice's name.
func looksLikeID(s string) bool {
	if len(s) != 26 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}
