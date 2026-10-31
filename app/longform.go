package main

// Longform (M3, thread #18): what the Stories (/stories) and Audiobook
// (/audiobook) pages share — their documents (services.LongformDoc) with
// autosaved drafts, the voices they offer and how a voice reference
// becomes a recipe for the document's model, compiling a document into a
// render, following renders (core.RenderChanged), and saving projects.
//
// Files of the area:
//
//	longform.go              this: state, drafts, compiling, the encoder
//	longform_voices.go       voices: pickers, resolving references, Use in Stories, the automatic model
//	longform_run.go          renders: following them, blockers, start / stop / resume, auditions
//	longform_projects.go     projects: load, save, open, delete, new
//	longform_view.go         panels both pages show: layout, setup, the confirmation, clearing
//	longform_view_book.go    the production overrides and the book card (cover, loudness)
//	longform_view_render.go  the render bar and its chapter tracker
//	longform_view_outputs.go the outputs, the loudness report, saving files
//	longform_player.go       the outputs' player and the read-along (word timings)
//	stories*.go              /stories: review buffer, auto-cast, the line list, the cast
//	audiobook*.go            /audiobook: the manuscript editor, cast, lexicon, chapter plan
//	projects*.go             /projects: the library

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
)

// Draft keys of the two pages.
const (
	storiesDraftKey   = "stories"
	audiobookDraftKey = "audiobook"
)

// longformState is the state of the Stories, Audiobook and Projects pages.
type longformState struct {
	lfStories   *lfPage
	lfBook      *lfPage
	lfRenders   map[string]*lfRender // renders followed (bodies as published), by id
	lfRecent    []string             // full renders, newest first (interrupted ones offer Resume)
	lfAuditions map[string]lfAudition
	lfEncode    func() bool // whether books can be M4B (tests replace it)
	lfUndo      []lfUndoToast
	projects    projectsState
}

// lfRender is a render as the pages see it.
type lfRender struct {
	rec  core.Render
	body services.RenderBody
}

// lfAudition is what an audition render plays: a line or a chapter.
type lfAudition struct {
	kind core.ProjectKind
	what string // "line:<id>" or "chapter:<n>"
}

// lfPage is one page's document and view state.
type lfPage struct {
	kind      core.ProjectKind
	doc       services.LongformDoc
	project   string // the project it is saved as; "" = a draft
	savedJSON string // the document as saved or opened ("" for a draft never saved)
	draftJSON string // as last handed to the draft service
	render    string // the render whose progress and outputs show
	auditions map[string]string
	importing bool
	directing bool // the AI director is at work (stories_director.go)
	scriptRev int  // bumped when the manuscript changes

	// Derived from the document when its fingerprint changes.
	fp       string
	spec     services.LongformSpec
	problems []services.Problem
	stats    services.ScriptStats
	blocks   []services.DialogueBlockInfo // a manuscript's dialogue passages (audiobook_dialogue.go)
	cast     []string                     // bookCast, at castRev
	castRev  int
	lfc      lfCompile // a large document's deferred compile (longform_compile.go)
	deco     bookDeco  // the manuscript's passage styling (audiobook_dialogue_editor.go)

	// View state.
	lines       ui.ListState
	sel         int
	open        map[string]bool // inspector sections
	saveName    string
	confirm     string // a project id to open after confirming, "clear", "delete:<id>"
	confirmOpen bool
	projects    []core.Project // saved projects of this kind, newest first
	ed          *editor.State  // the manuscript (Audiobook)
	edSynced    string
	edVersion   uint64
	dismissed   string // fp when the warnings were dismissed
	// auto: the model and voice were picked for a new user (the
	// presets); they follow the saved voices while the page is empty and
	// the user has not chosen a model (autoLfModel).
	auto      bool
	reactions bool
	ra        readAlong
	panel     panelState // the inspector: beside the page, or a drawer
	narrow    bool       // the main column is narrow: rows fold actions into ⋯

	// The way in (longform_entry.go): opened shows the editor though the
	// document is empty (a new project); listing shows the list though
	// one is open (it renders); the composer's text, narrator and focus,
	// and the list's search.
	opened, listing bool
	entryText       string
	entryVoice      string
	entryQuery      string
	entryFocus      bool
}

func newLfPage(kind core.ProjectKind, model string) *lfPage {
	p := &lfPage{kind: kind, doc: services.NewLongformDoc(kind, model), auditions: map[string]string{}, sel: -1,
		open: map[string]bool{"setup": true, "cast": true}}
	return p
}

// lfDraft is a page as saved between runs.
type lfDraft struct {
	Version int                  `json:"version"`
	Doc     services.LongformDoc `json:"doc"`
	Project string               `json:"project,omitempty"`
	Saved   string               `json:"saved,omitempty"`
	Render  string               `json:"render,omitempty"`
	Auto    bool                 `json:"auto,omitempty"` // lfPage.auto
}

// openLongform connects the longform pages to the library: render events
// (subscribed once, here), the drafts, the renders and the projects.
func (a *app) openLongform(lib *library) {
	model := a.preset().Repo
	a.lfStories = newLfPage(core.ProjectStory, model)
	a.lfBook = newLfPage(core.ProjectAudiobook, model)
	a.lfBook.ed = newManuscriptEditor()
	a.lfRenders = map[string]*lfRender{}
	a.lfAuditions = map[string]lfAudition{}
	host := a.eng
	lib.useEncoder(func() jobs.Executor {
		if host == nil || host.sup == nil || !slices.Contains(engine.ExecutorKinds, services.KindEncode) {
			return nil
		}
		return engine.Executor(host.sup, proto.RoleMedia, services.KindEncode)
	})
	if a.lfEncode == nil {
		a.lfEncode = a.engineEncodes
	}
	core.On(lib.bus, func(ev core.RenderChanged) { a.post(func() { a.renderChanged(ev) }) })
	core.On(lib.bus, func(ev core.ProjectChanged) { a.post(func() { a.projectChanged(ev) }) })
	a.openProjects(lib)
	a.followAlignments(lib)
	for _, p := range []*lfPage{a.lfStories, a.lfBook} {
		a.restoreLfDraft(p)
		a.loadLfProjects(p)
	}
	a.loadRenders()
}

func (p *lfPage) draftKey() string {
	if p.kind == core.ProjectAudiobook {
		return audiobookDraftKey
	}
	return storiesDraftKey
}

// lfPageOf is the page of a project kind.
func (a *app) lfPageOf(kind core.ProjectKind) *lfPage {
	if kind == core.ProjectAudiobook {
		return a.lfBook
	}
	return a.lfStories
}

// restoreLfDraft loads a page's draft in the background and applies it
// unless the page was edited meanwhile.
func (a *app) restoreLfDraft(p *lfPage) {
	drafts := a.lib.svc.Drafts
	key := p.draftKey()
	a.background(func() {
		var d lfDraft
		ok, err := drafts.Load(context.Background(), key, &d)
		if err != nil || !ok || d.Doc.Version == 0 {
			a.post(func() {
				if p.draftJSON == "" && p.empty() {
					p.auto = true // a new user's page
					a.autoLfModel(p)
				}
			})
			return
		}
		a.post(func() {
			if p.draftJSON != "" {
				return // edited before the draft came
			}
			a.setLfDoc(p, d.Doc, d.Project, d.Saved)
			p.render = d.Render
			p.draftJSON = docJSON(&p.doc)
			p.auto = d.Auto
			a.autoLfModel(p)
		})
	})
}

// setLfDoc replaces a page's document (a draft, an opened project, a
// sample).
func (a *app) setLfDoc(p *lfPage, d services.LongformDoc, project, saved string) {
	d.Kind = p.kind
	a.replaceRemovedDoc(&d) // a removed model's replacement (models_removed.go)
	if d.Production == (services.Production{}) {
		d.Production = services.DefaultProduction()
	}
	p.doc, p.project, p.savedJSON = d, project, saved
	p.saveName = d.Title
	var sd services.LongformDoc
	if saved != "" && json.Unmarshal([]byte(saved), &sd) == nil && sd.Title != "" {
		p.saveName = sd.Title // the title saved: a draft may have changed it
	}
	p.sel = -1
	p.fp = ""
	p.lfc.compiled, p.lfc.deferring = false, false
	p.scriptRev++
	p.render = ""
	if p.ed != nil {
		p.ed.Buffer.Reset(d.Script)
		p.edSynced, p.edVersion = d.Script, p.ed.Buffer.Version()
	}
	p.ra = readAlong{}
}

// docJSON is a document as compared for unsaved changes: the render it
// last made is not a change.
func docJSON(d *services.LongformDoc) string {
	c := *d
	c.Render = ""
	b, _ := json.Marshal(c)
	return string(b)
}

// --- the encoder -------------------------------------------------------------

// useEncoder gives books the engine's media.encode executor (nil while
// the engine has none).
func (l *library) useEncoder(fn func() jobs.Executor) {
	l.synthMu.Lock()
	l.encode = fn
	l.synthMu.Unlock()
}

func (l *library) encodeExecutor() jobs.Executor {
	l.synthMu.Lock()
	fn := l.encode
	l.synthMu.Unlock()
	if fn == nil {
		return nil
	}
	return fn()
}

// engineEncodes reports whether the engine side runs media.encode: the
// Go executors know the method and, once the media worker said hello,
// it lists it. (Thread #14 adds it; until then books are WAV.)
func (a *app) engineEncodes() bool {
	if !slices.Contains(engine.ExecutorKinds, services.KindEncode) {
		return false
	}
	for _, w := range a.workers {
		if w.Role == proto.RoleMedia && w.Hello != nil && !slices.Contains(w.Hello.Methods, services.KindEncode) {
			return false
		}
	}
	return true
}
