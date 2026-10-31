package main

// The library's items (M3) as the app shows them: their kind, icon and
// second line, opening one, renaming a project, and recording exports.
// The Projects page (0.5 F5) lists the long pieces of work
// (projects_list.go, projects_view.go); Assets every output
// (assets*.go).

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/store"
)

// projectsState is the Projects page's state (0.5 F5, projects_view.go).
type projectsState struct {
	query   string
	filter  int                 // into projFilters
	cards   int                 // 0 list, 1 cards
	lf      []store.LibraryItem // the stories and books (projects_list.go)
	loaded  bool
	loading bool
	dirty   bool // something changed: read again when shown
	grid    ui.GridState

	renaming    string // a row's key
	renameText  string
	confirm     projItem // the row the delete asks about
	confirmOpen bool
	details     map[string]bool // renders' "How it was made" shown (Assets)
	asked       map[string]bool // renders read for their details
}

// libKey is an item's key: source and id.
func libKey(it store.LibraryItem) string { return it.Source + ":" + it.ID }

// openProjects subscribes the lists to the changes they show.
func (a *app) openProjects(lib *library) {
	a.projects.details, a.projects.asked = map[string]bool{}, map[string]bool{}
	a.projects.dirty = true
	mark := func() { a.post(func() { a.projects.dirty = true; a.history.dirty = true }) }
	core.On(lib.bus, func(core.TakeCreated) { mark() })
	core.On(lib.bus, func(core.TakeDeleted) { mark() })
	core.On(lib.bus, func(core.VoiceChanged) { mark() })
	core.On(lib.bus, func(core.ExportChanged) { mark() })
}

// itemKind names what an item is.
func itemKind(it store.LibraryItem) string {
	switch it.Source {
	case "dub": // M4 (thread #19)
		return i18n.T("Dubbing project")
	case "workflow": // M6 (thread #21)
		return i18n.T("Workflow")
	case "render":
		if it.Category == store.LibAudiobook {
			return i18n.T("Audiobook render")
		}
		return i18n.T("Story render")
	case "project":
		switch core.ProjectKind(it.Category) {
		case core.ProjectStory:
			return i18n.T("Story")
		case core.ProjectAudiobook:
			return i18n.T("Audiobook")
		case core.ProjectDub:
			return i18n.T("Dubbing project")
		case core.ProjectTranscript:
			return i18n.T("Transcript")
		}
		return i18n.T("Project")
	case "voice":
		return i18n.T("Voice")
	case "take":
		return i18n.T("Take")
	case "export":
		return i18n.T("Export")
	}
	return it.Source
}

// itemIcon is an item's icon.
func itemIcon(it store.LibraryItem) icons.Icon {
	switch it.Source {
	case "voice":
		return voicesIcon
	case "take":
		return historyIcon
	case "export":
		return exportIcon
	case "render": // its kind's icon: the row's play button plays it
		if it.Category == store.LibAudiobook {
			return audiobookIcon
		}
		return storiesIcon
	case "dub": // M4 (thread #19)
		return dubIcon
	case "workflow": // M6 (thread #21)
		return workflowsIcon
	case "call": // M6b (thread #23)
		return callsIcon
	}
	if it.Category == store.LibAudiobook {
		return audiobookIcon
	}
	return storiesIcon
}

// itemMeta is an item's second line.
func (a *app) itemMeta(it store.LibraryItem) string {
	parts := []string{itemKind(it)}
	switch it.Source {
	case "project":
		s := services.SummaryOf(it.Detail)
		if s.Chapters > 1 {
			parts = append(parts, i18n.N(s.Chapters, "%d chapter", "%d chapters", s.Chapters))
		}
		if s.Lines > 0 {
			parts = append(parts, i18n.N(s.Lines, "%d line", "%d lines", s.Lines))
		}
		if s.Words > 0 {
			parts = append(parts, i18n.N(s.Words, "%d word", "%d words", s.Words))
		}
		if s.Chars > 0 {
			parts = append(parts, i18n.N(s.Chars, "%d character", "%d characters", s.Chars))
		}
	case "render":
		if r := a.lfRenders[it.ID]; r != nil {
			parts = append(parts, a.renderRecipe(r))
		}
	case "voice":
		parts = append(parts, voiceKindText(core.VoiceKind(it.Detail)))
	case "take":
		parts = append(parts, modelName(it.Detail))
	case "export":
		parts = append(parts, it.Detail)
	case "dub": // M4: dub_pages.go (thread #19)
		parts = append(parts, dubItemPhase(it.Detail))
	case "workflow": // M6: workflow_pages.go (thread #21)
		parts = append(parts, wfItemState(it.Detail))
	case "call": // M6b: calls_pages.go (thread #23)
		parts = append(parts, callItemDetail(it.Detail))
	}
	parts = append(parts, i18n.DateTime(it.Updated))
	return strings.Join(parts, " · ")
}

// renderRecipe is a render's one-line recipe: voices · speed · engine · length.
func (a *app) renderRecipe(r *lfRender) string {
	s := r.body.Summary
	var parts []string
	if v := a.voiceNames(s.Voices); v != "" {
		parts = append(parts, v)
	}
	parts = append(parts, speedRange(s.MinSpeed, s.MaxSpeed), modelName(s.Model))
	if r.rec.AudioSeconds > 0 {
		parts = append(parts, lfClock(time.Duration(r.rec.AudioSeconds*float64(time.Second))))
	}
	return strings.Join(parts, " · ")
}

// voiceNames names voice references (VoiceIDs or speakers).
func (a *app) voiceNames(refs []string) string {
	var out []string
	for _, ref := range refs {
		out = append(out, a.lfVoiceLabel(ref))
	}
	return strings.Join(out, ", ")
}

// speedRange is "1.00×" or "0.90–1.10×".
func speedRange(lo, hi float64) string {
	if lo <= 0 {
		lo = 1
	}
	if hi <= 0 {
		hi = lo
	}
	if lo == hi {
		return fmt.Sprintf("%.2f×", lo)
	}
	return fmt.Sprintf("%.2f–%.2f×", lo, hi)
}

// renderFor is a render's state for an item: from the renders followed,
// or read in the background.
func (a *app) renderFor(id string) *lfRender {
	if r := a.lfRenders[id]; r != nil {
		return r
	}
	if a.projects.asked[id] {
		return nil
	}
	a.projects.asked[id] = true
	svc := a.lib.svc.Longform
	a.background(func() {
		r, _, err := svc.Get(context.Background(), id)
		if err != nil {
			return
		}
		a.post(func() { a.putRender(r) })
	})
	return nil
}

// openItem opens an item: a project in its page, a voice in its editor,
// a take in the Studio, a render's page.
func (a *app) openItem(it store.LibraryItem) {
	switch it.Source {
	case "dub": // M4: dub.go (thread #19)
		a.openDub(it.ID)
	case "workflow": // M6: workflow.go (thread #21)
		a.wfOpenPage(it.ID)
	case "call": // M6b: calls_pages.go (thread #23)
		a.callOpenPage(it.ID)
	case "project":
		kind := core.ProjectKind(it.Category)
		if kind != core.ProjectStory && kind != core.ProjectAudiobook {
			a.toast(i18n.T("Opening this kind of project comes later."))
			return
		}
		a.askOpenProject(it.ID, kind, it.Title)
	case "voice":
		a.openVoiceEditor(it.ID)
	case "take":
		if t := a.findTake(it.ID); t != nil {
			a.reuse(t)
		}
	case "render":
		if r := a.lfRenders[it.ID]; r != nil {
			p := a.lfPageOf(r.rec.Kind)
			p.render = r.rec.ID
			a.navigate("/" + lfRoute(r.rec.Kind))
		}
	}
}

// recordTakeExport records that a take was saved to dst in format ("wav",
// "mp3"…), from any goroutine: the Projects page lists it under Exports.
func (a *app) recordTakeExport(id, dst, format string) {
	lib := a.lib.svc.Library
	title := id
	a.post(func() {
		if t := a.findTake(id); t != nil {
			title = strings.Join(strings.Fields(t.Text), " ")
			if r := []rune(title); len(r) > 80 {
				title = string(r[:80]) + "…"
			}
		}
		a.background(func() {
			if _, err := lib.RecordExport(context.Background(), core.Export{Kind: "take", SourceID: id, Title: title, Path: dst, Format: format}); err != nil {
				a.post(func() { a.toastWarn(i18n.T("Could not record the export: %v", err)) })
			}
		})
	})
}

// recordVoiceExport records that a voice was exported to dst.
func (a *app) recordVoiceExport(id, dst string) {
	lib := a.lib.svc.Library
	a.post(func() {
		title := id
		if v := a.findVoice(id); v != nil {
			title = v.Name
		}
		a.background(func() {
			lib.RecordExport(context.Background(), core.Export{Kind: "voice", SourceID: id, Title: title, Path: dst, Format: "asvoice"})
		})
	})
}

// renameItem renames a project.
func (a *app) renameItem(it store.LibraryItem, title string) {
	lib := a.lib.svc.Library
	a.background(func() {
		_, err := lib.Rename(context.Background(), it.ID, title)
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not rename: %v", err))
			}
			a.projects.dirty = true
		})
	})
}

var (
	storiesIcon   = icons.Dialog2
	audiobookIcon = icons.NotebookMinimalistic
	projectsIcon  = icons.FolderWithFiles
	exportIcon    = icons.Export
	renderIcon    = icons.Play
)
