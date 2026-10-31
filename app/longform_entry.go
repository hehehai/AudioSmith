package main

// The Stories and Audiobook pages' way in (0.5 F5, after ElevenLabs'
// tool pages): while no story or book is open, a composer over the list
// of them. The composer makes a project at once (a description or a
// script for a story; a manuscript and its narrator for a book) and
// opens it in the editor, whose header leads back here (「故事 /」, ‹).
// Going back saves what changed and closes it; while it renders it stays
// open behind the list.

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/services"
)

// lfEditing reports whether a page shows its editor: a document is open
// (made, opened, or holding text) and the list was not asked for.
func (a *app) lfEditing(p *lfPage) bool {
	if p.listing {
		return false
	}
	return p.opened || p.project != "" || !p.empty() || p.importing
}

// lfKindOf is the Projects list's kind of a page.
func lfKindOf(p *lfPage) projKind {
	if p.kind == core.ProjectAudiobook {
		return projBook
	}
	return projStory
}

// lfCreate makes a new project on page p (fill writes its content),
// saves it and opens it; the work open before is saved first.
func (a *app) lfCreate(p *lfPage, fill func()) bool {
	if a.lfLocked(p) {
		a.toast(i18n.T("Longform work is active: stop the render first."))
		return false
	}
	a.lfStash(p)
	a.newLf(p)
	if fill != nil {
		fill()
	}
	p.opened, p.listing = true, false
	a.saveLfWith(p, false, false, true)
	a.navigate("/" + lfRoute(p.kind))
	return true
}

// lfStash saves a page's unsaved work as it is now, to its project (or a
// new one), without touching the page: a project changed elsewhere since
// is kept, and this is saved as a copy.
func (a *app) lfStash(p *lfPage) {
	if !p.unsaved() || p.empty() {
		return
	}
	name := lfProjectName(p)
	doc := p.doc
	if doc.Title == "" {
		doc.Title = name
	}
	pr := core.Project{ID: p.project, Kind: p.kind, Title: name}
	var unchanged func(core.Project, services.LongformDoc) bool
	if base := p.savedJSON; pr.ID != "" && base != "" {
		unchanged = func(_ core.Project, cur services.LongformDoc) bool { return sameAsSaved(base, cur) }
	}
	lib := a.lib.svc.Library
	a.background(func() {
		ctx := context.Background()
		_, err := lib.SaveDocIf(ctx, pr, doc, unchanged)
		if errors.Is(err, services.ErrChanged) {
			pr.ID = ""
			_, err = lib.SaveDoc(ctx, pr, doc)
		}
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not save: %v", err))
			}
			a.projects.dirty = true
		})
	})
}

// lfClose goes back from the editor to the list: the work is saved and
// closed; while it renders it stays open behind the list.
func (a *app) lfClose(p *lfPage) {
	if a.lfLocked(p) {
		p.listing = true
		return
	}
	a.lfStash(p)
	a.newLf(p)
	p.opened, p.listing = false, false
}

// lfEntryNew opens a page's composer (「+ 新建」, ⌘K): the work open
// there is saved and closed first.
func (a *app) lfEntryNew(path string) {
	p := a.lfStories
	if path == "/audiobook" {
		p = a.lfBook
	}
	if a.lfEditing(p) {
		a.lfClose(p)
	}
	p.entryFocus = true
	a.navigate(path)
}

// lfEntryPage is a page's composer over its list.
func (a *app) lfEntryPage(c *ui.Context, p *lfPage) {
	t := c.Theme()
	if p.listing && !a.lfLocked(p) {
		a.lfClose(p) // its render ended: close it now
	}
	kind := lfKindOf(p)
	all := a.projItems(kind, "")
	q := strings.TrimSpace(p.entryQuery)
	items := all
	if q != "" {
		items = a.projItems(kind, q)
	}
	head := pageHead{title: pageTitle("/" + lfRoute(p.kind)), count: listCount(len(all), a.projects.loaded)}
	if len(all) > 0 {
		ph := i18n.T("Search stories")
		if p.kind == core.ProjectAudiobook {
			ph = i18n.T("Search audiobooks")
		}
		head.search = &headSearch{key: "lf-search-" + string(p.kind), value: &p.entryQuery, placeholder: ph}
	}
	pageHeaderBar(c, head)
	a.toolPage(c, "lf-entry-"+string(p.kind), func() {
		a.lfComposer(c, p)
		switch {
		case len(all) == 0:
			title, body := i18n.T("No stories yet"), i18n.T("Describe a story or paste a script above: it opens in the editor and shows here.")
			if p.kind == core.ProjectAudiobook {
				title, body = i18n.T("No audiobooks yet"), i18n.T("Paste or import a manuscript above: it opens in the editor and shows here.")
			}
			ghostRows(c.Key("lf-ghost"), 4, projKindIcon(kind), title, body)
		case len(items) == 0:
			ui.Column(c).Padding(px(t, sp5)).AlignItems(ui.Center).Children(func() { caption(c, i18n.T("No projects match.")) })
		default:
			a.projTable(c, items, false)
		}
	})
	a.projConfirm(c)
}

// lfComposer is the composer: the text (a story's description or script,
// a book's manuscript; files dropped on it are imported), then Import…,
// a book's narrator, the sample and 「新建」.
func (a *app) lfComposer(c *ui.Context, p *lfPage) {
	t := c.Theme()
	book := p.kind == core.ProjectAudiobook
	ph, create := i18n.T("Describe a story in a sentence, or paste a script…"), i18n.X("create", "New story")
	if book {
		ph, create = i18n.T("Paste a manuscript, or drop a TXT, Markdown, EPUB or DOCX file…"), i18n.X("create", "New audiobook")
	}
	toolComposer(c, "lf-composer", func(files []string) { a.lfCreateFrom(p, files[0]) }, func() {
		box := ui.TextArea(c.Key("lf-entry-text"), &p.entryText).Placeholder(ph).Label(ph).Lines(3, 8).FillWidth().MinHeight(0)
		if p.entryFocus {
			p.entryFocus = false
			box.Focus()
		}
		if files := box.DroppedFiles(); len(files) > 0 {
			a.lfCreateFrom(p, files[0])
		}
	}, func() {
		if p.writing {
			a.storyWriteProgress(c, p) // stories_write.go
			return
		}
		narrow := a.toolNarrow(c)
		if labelOrIcon(c.Key("lf-entry-import"), icons.Import, i18n.T("Import…"), narrow).Clicked() {
			a.lfEntryImport(p)
		}
		if book {
			if p.entryVoice == "" {
				p.entryVoice = p.doc.Voice
			}
			ui.Row(c).MaxWidth(px(t, 240)).Shrink(1).MinWidth(0).Children(func() {
				a.lfVoiceSelect(c, "ab-entry-voice", &p.entryVoice, entryFor(p.doc.Model), i18n.T("Narrator voice"), false)
			})
		}
		if !book && a.storyWriteReady() {
			a.storyWriteToggle(c, p)
		}
		if ghostButton(c.Key("lf-entry-sample"), i18n.T("Load sample")).Clicked() {
			a.lfCreate(p, func() { a.lfFillSample(p) })
		}
		ui.Spacer(c)
		if primaryButton(c.Key("lf-entry-new"), create).Clicked() {
			a.lfSubmit(p)
		}
	})
}

// lfSubmit makes a project of the composer's text.
func (a *app) lfSubmit(p *lfPage) {
	text := strings.TrimSpace(p.entryText)
	if p.kind == core.ProjectStory && p.helpWrite && text != "" && a.storyWriteReady() {
		a.storyWrite(p, text) // stories_write.go
		return
	}
	voice := p.entryVoice
	if a.lfCreate(p, func() {
		p.doc.Title = lfTitleFrom(text)
		if p.kind == core.ProjectAudiobook {
			if voice != "" {
				p.doc.Voice = voice
			}
			a.setManuscript(p, text)
			return
		}
		p.doc.ImportText = text // the review buffer: Auto-cast or Split from there
		p.open["review"] = true
	}) {
		p.entryText = ""
	}
}

// lfFillSample writes the sample into a page's new document.
func (a *app) lfFillSample(p *lfPage) {
	if p.kind == core.ProjectAudiobook {
		a.loadSample(p)
		return
	}
	a.loadSampleStory(p)
}

// lfTitleFrom names a project after its text's first line, when short.
func lfTitleFrom(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	line = strings.TrimFunc(strings.TrimSpace(line), func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSpace(r) })
	r := []rune(line)
	if len(r) == 0 {
		return ""
	}
	if len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return line
}

// lfCreateFrom makes a project of a file: a story's script or a book.
func (a *app) lfCreateFrom(p *lfPage, path string) {
	if !a.lfCreate(p, func() { p.doc.Title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)) }) {
		return
	}
	if p.kind == core.ProjectAudiobook {
		a.importBookFrom(p, path)
	} else {
		a.importStoryFrom(p, path)
	}
}

// lfEntryImport asks for a file, then makes a project of it.
func (a *app) lfEntryImport(p *lfPage) {
	title, name, exts := i18n.T("Import a story"), i18n.T("Stories"), []string{"txt", "md", "srt", "vtt", "epub", "docx"}
	if p.kind == core.ProjectAudiobook {
		title, name, exts = i18n.T("Import a book"), i18n.T("Books"), []string{"txt", "md", "markdown", "epub", "docx"}
	}
	if a.win == nil {
		return
	}
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: title, Filters: []mygo.FileFilter{{Name: name, Extensions: exts}}})
		if err != nil || len(paths) == 0 {
			return
		}
		a.post(func() { a.lfCreateFrom(p, paths[0]) })
	}()
}

// lfEditorHead is an open story's or book's header: 「故事 /」 and its
// title, ‹ › (‹ and the list's name close it), what it holds, then the
// page's tools.
func (a *app) lfEditorHead(p *lfPage, count string, tools func(bool)) pageHead {
	title := strings.TrimSpace(p.doc.Title)
	if title == "" {
		title = lfProjectName(p)
	}
	return pageHead{title: title, parent: pageTitle("/" + lfRoute(p.kind)), up: "/" + lfRoute(p.kind), upFn: func() { a.lfClose(p) },
		count: count, tools: tools}
}

// lfSaveItems are the editor's ⋯ items to save (the list replaced the
// old Project menu).
func (a *app) lfSaveItems(m *ui.Menu, p *lfPage) {
	save := i18n.T("Save")
	if p.project != "" && p.unsaved() {
		save = i18n.T("Save (unsaved changes)")
	}
	if m.Item(save).Chosen() {
		a.saveLf(p, false)
	}
	if p.project != "" && m.Item(i18n.T("Save a copy")).Chosen() {
		a.saveLf(p, true)
	}
	if m.Item(i18n.T("All projects…")).Chosen() {
		a.navigate("/projects")
	}
	m.Separator()
}
