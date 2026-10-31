package main

// The Projects list (0.5 F5): every long piece of work across the tools
// as one kind of row — stories and audiobooks (the library's projects,
// with their summaries), dubs, transcripts and voice isolations (the
// tools' lists, in memory) — newest change first. The Stories and
// Audiobook pages list theirs from it (longform_entry.go), and ⌘K
// searches it (palette.go).

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/store"
)

// projKind is what a project is.
type projKind string

const (
	projStory      projKind = "story"
	projBook       projKind = "audiobook"
	projDub        projKind = "dub"
	projTranscript projKind = "transcript"
	projIsolate    projKind = "isolate"
)

// projFilters are the type filter's kinds, in order ("" all).
var projFilters = []projKind{"", projStory, projBook, projDub, projTranscript, projIsolate}

// projItem is a row of the list.
type projItem struct {
	kind      projKind
	id, title string
	sub       string // a source, the first words; "" none
	updated   time.Time
	length    string // "12 行", "3:05"; "" unknown
	status    string
	running   bool
	frac      float64 // < 0: no bar
	failed    bool
	draft     bool // a page's draft never saved (a story or a book)
}

func (it projItem) key() string { return string(it.kind) + ":" + it.id }

// projKindLabel names a kind (the filter, a row's type).
func projKindLabel(k projKind) string {
	switch k {
	case projStory:
		return i18n.T("Story")
	case projBook:
		return i18n.T("Audiobook")
	case projDub:
		return i18n.X("project kind", "Dub")
	case projTranscript:
		return i18n.T("Transcript")
	case projIsolate:
		return i18n.X("project kind", "Isolation")
	}
	return i18n.T("All")
}

// projKindIcon is a kind's icon.
func projKindIcon(k projKind) icons.Icon {
	switch k {
	case projBook:
		return audiobookIcon
	case projDub:
		return dubIcon
	case projTranscript:
		return transcribeIcon
	case projIsolate:
		return isolateIcon
	}
	return storiesIcon
}

// refreshProjects reads the stories and books when something changed
// (the other kinds are in memory).
func (a *app) refreshProjects() {
	s := &a.projects
	if s.loading || (s.loaded && !s.dirty) || a.lib == nil || a.lib.svc.Library == nil {
		return
	}
	s.loading, s.dirty = true, false
	lib := a.lib.svc.Library
	a.background(func() {
		var out []store.LibraryItem
		var err error
		for _, cat := range []string{store.LibStory, store.LibAudiobook} {
			items, e := lib.Search(context.Background(), store.LibraryQuery{Category: cat, Limit: 2000})
			if e != nil {
				err = e
				break
			}
			for _, it := range items {
				if it.Source == "project" {
					out = append(out, it)
				}
			}
		}
		a.post(func() {
			s.loading = false
			if err != nil {
				a.toastWarn(i18n.T("Could not read the library: %v", err))
				return
			}
			s.lf, s.loaded = out, true
		})
	})
}

// projItems are the list's rows of kind ("" all) whose title or source
// has q, newest change first.
func (a *app) projItems(kind projKind, q string) []projItem {
	a.refreshProjects()
	var out []projItem
	add := func(it projItem) {
		if kind != "" && it.kind != kind {
			return
		}
		if q != "" && !strings.Contains(strings.ToLower(it.title+" "+it.sub), strings.ToLower(q)) {
			return
		}
		out = append(out, it)
	}
	for _, p := range []*lfPage{a.lfStories, a.lfBook} {
		if p != nil && p.project == "" && !p.empty() {
			add(a.lfDraftItem(p))
		}
	}
	for _, it := range a.projects.lf {
		add(a.lfProjItem(it))
	}
	for _, r := range a.dubList {
		add(a.dubProjItem(r))
	}
	for _, tr := range a.trList {
		if tr.Source == core.SourceDictation {
			continue
		}
		add(projItem{kind: projTranscript, id: tr.ID, title: transcriptTitle(tr), sub: trFirstWords(tr), updated: tr.UpdatedAt,
			length: durText(tr.Duration), status: i18n.T("Done"), frac: -1})
	}
	for _, x := range a.isoList {
		st, running, frac, failed := a.isoStatus(x)
		add(projItem{kind: projIsolate, id: x.ID, title: x.Title, sub: shortPath(x.Input), updated: x.UpdatedAt,
			length: durText(x.Duration), status: st, running: running, frac: frac, failed: failed})
	}
	slices.SortStableFunc(out, func(x, y projItem) int {
		if x.draft != y.draft { // the drafts first
			if x.draft {
				return -1
			}
			return 1
		}
		return y.updated.Compare(x.updated)
	})
	return out
}

// durText is a length in seconds as a clock, "" for none.
func durText(s float64) string {
	if s <= 0 {
		return ""
	}
	return clock(s)
}

// trFirstWords is a transcript's first words, for its row.
func trFirstWords(tr core.Transcript) string {
	var b strings.Builder
	for _, s := range tr.Segments {
		b.WriteString(strings.TrimSpace(s.Text))
		b.WriteString(" ")
		if b.Len() > 120 {
			break
		}
	}
	r := []rune(strings.TrimSpace(b.String()))
	if len(r) > 60 {
		return string(r[:60]) + "…"
	}
	return string(r)
}

// lfProjItem is a story or a book: its length from the summary, its
// state from its renders.
func (a *app) lfProjItem(it store.LibraryItem) projItem {
	kind := projStory
	if core.ProjectKind(it.Category) == core.ProjectAudiobook {
		kind = projBook
	}
	out := projItem{kind: kind, id: it.ID, title: firstOf(strings.TrimSpace(it.Title), i18n.T("Untitled")), updated: it.Updated, frac: -1}
	sum := services.SummaryOf(it.Detail)
	switch {
	case kind == projBook && sum.Chapters > 0:
		out.length = i18n.N(sum.Chapters, "%d chapter", "%d chapters", sum.Chapters)
	case sum.Lines > 0:
		out.length = i18n.N(sum.Lines, "%d line", "%d lines", sum.Lines)
	}
	if sum.Seconds > 0 {
		out.length = strings.TrimPrefix(out.length+" · ~"+clock(sum.Seconds), " · ")
	}
	out.status = i18n.T("Draft")
	for _, rid := range a.lfRecent { // newest first
		r := a.lfRenders[rid]
		if r == nil || r.rec.ProjectID != it.ID || r.rec.Audition {
			continue
		}
		switch r.rec.State {
		case core.RenderRunning:
			out.status, out.running = i18n.T("Rendering %d/%d", r.body.Done(), len(r.body.Chapters)), true
			if n := len(r.body.Chapters); n > 0 {
				out.frac = float64(r.body.Done()) / float64(n)
			}
		case core.RenderDone:
			out.status = i18n.T("Rendered")
		case core.RenderFailed:
			out.status, out.failed = i18n.T("Failed"), true
		default:
			continue
		}
		break
	}
	if p := a.lfPageOf(core.ProjectKind(kind)); p.project == it.ID && p.unsaved() {
		out.sub = i18n.T("Unsaved changes")
	}
	return out
}

// lfDraftItem is a page's draft that was never saved as a project.
func (a *app) lfDraftItem(p *lfPage) projItem {
	kind := projStory
	if p.kind == core.ProjectAudiobook {
		kind = projBook
	}
	return projItem{kind: kind, id: "draft", title: lfProjectName(p), sub: i18n.T("Not saved yet"), updated: time.Now(),
		status: i18n.T("Draft"), frac: -1, draft: true}
}

// dubProjItem is a dub, live while it runs.
func (a *app) dubProjItem(r store.DubRecord) projItem {
	out := projItem{kind: projDub, id: r.ID, title: firstOf(r.Title, i18n.T("Untitled dub")), sub: dubSourceLabel(r.Source),
		updated: r.UpdatedAt, length: durText(r.Duration), status: dubPhaseText(dub.Phase(r.Phase)), frac: -1}
	if j := a.dubRowJob(r.ID); j.ID != "" {
		out.status, out.running, out.frac = dubStageName(dubStageOfKind(j.Kind)), true, max(determinate(j), 0)
		if j.State != jobs.StateRunning {
			out.status = i18n.T("Waiting: %s", out.status)
		}
	} else if dub.Phase(r.Phase) == dub.PhaseFailed {
		out.failed = true
	}
	return out
}

// openProj opens a row's item in its tool.
func (a *app) openProj(it projItem) {
	switch it.kind {
	case projStory, projBook:
		kind := core.ProjectKind(it.kind)
		p := a.lfPageOf(kind)
		if it.draft {
			p.listing = false
			a.navigate("/" + lfRoute(kind))
			return
		}
		p.listing = false
		if p.project == it.id {
			a.navigate("/" + lfRoute(kind))
			return
		}
		if a.lfLocked(p) {
			a.toast(i18n.T("Longform work is active: stop the render first."))
			return
		}
		a.lfStash(p) // what was open is saved, as going back saves it
		a.openLfProject(it.id)
	case projDub:
		a.openDub(it.id)
	case projTranscript:
		a.selectTranscript(it.id)
	case projIsolate:
		a.navigate("/isolate/" + it.id)
	}
}

// deleteProj deletes a row's item (asked first: projectsState.confirm).
func (a *app) deleteProj(it projItem) {
	switch it.kind {
	case projStory, projBook:
		p := a.lfPageOf(core.ProjectKind(it.kind))
		if it.draft {
			if !a.lfLocked(p) {
				a.newLf(p)
				p.opened = false
			}
			return
		}
		open := p.project == it.id
		svc := a.lib.svc.Projects
		a.background(func() {
			err := svc.Delete(context.Background(), it.id)
			a.post(func() {
				if err != nil {
					a.toastWarn(i18n.T("Could not delete: %v", err))
					return
				}
				if open && !a.lfLocked(p) { // the page closes it
					a.newLf(p)
					p.opened = false
				}
				a.projects.dirty = true
				a.toast(i18n.T("Project deleted"))
			})
		})
	case projDub:
		a.deleteDub(it.id)
	case projTranscript:
		a.deleteTranscript(it.id)
	case projIsolate:
		a.isoDelete(it.id, true)
	}
}

// duplicateProj saves a copy of a story or a book.
func (a *app) duplicateProj(it projItem) {
	lib := a.lib.svc.Library
	a.background(func() {
		ctx := context.Background()
		pr, d, err := lib.LoadDoc(ctx, it.id)
		if err == nil {
			title := i18n.T("%s (copy)", pr.Title)
			d.Title, d.Render = title, ""
			_, err = lib.SaveDoc(ctx, core.Project{Kind: pr.Kind, Title: title}, d)
		}
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not duplicate: %v", err))
				return
			}
			a.projects.dirty = true
		})
	})
}
