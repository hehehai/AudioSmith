package main

// Deleting from Home's Recent (thread #143): a row's ⋯ (Open, Delete…)
// and Delete on a focused row ask first, then delete the item the way its
// own list does — a take as Assets does, a story or a book as Projects
// does (its renders stay), a render with its files, a dub, a transcript
// or an isolation as their tools do.

import (
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/store"
)

// homeReadGap and homeReadMax bound how often Home's Recent is read
// again: after a change, not sooner than the gap since the last read;
// without one, once in a while.
const (
	homeReadGap = time.Second
	homeReadMax = time.Minute
)

// homeFollow marks Recent stale when the library changes: a take, a
// project, a render or a dub (perf review 11-02 H1: it was read every
// 5 s while Home showed).
func (a *app) homeFollow(bus *core.Bus) {
	bus.Subscribe(func(core.Event) { a.post(func() { a.homeUI.changed++ }) },
		"take.", core.ProjectChanged{}.Topic(), core.RenderChanged{}.Topic(), services.DubChanged{}.Topic())
}

// stale reports whether the library changed since Recent was read.
func (hp *homePage) stale() bool { return hp.changed != hp.readGen }

// homeLibDelete is how a library item of Recent is deleted, and the
// question's second line; nil for what Recent does not delete.
func (a *app) homeLibDelete(it store.LibraryItem, title string) (func(), string) {
	id := it.ID
	switch it.Source {
	case "take":
		return func() {
			if tk := a.findTake(id); tk != nil {
				a.deleteClip(tk)
			}
		}, ""
	case "dub":
		return func() { a.deleteDub(id) }, i18n.T("The project and every file made for it are deleted. The source file is kept.")
	case "render":
		return func() { a.deleteRenderAsset(id) }, i18n.T("Its audio is deleted; the files you exported are kept.")
	case "project":
		kind := projStory
		switch core.ProjectKind(it.Category) {
		case core.ProjectStory:
		case core.ProjectAudiobook:
			kind = projBook
		default:
			return nil, ""
		}
		return func() { a.deleteProj(projItem{kind: kind, id: id, title: title}) }, i18n.T("Its finished renders and exported files are kept; its unfinished renders are deleted.")
	}
	return nil, ""
}

// homeAsk asks before a row of Recent is deleted; once said, the row
// leaves the list at once (the library's next read has the truth).
func (a *app) homeAsk(c *ui.Context) {
	hp := &a.homeUI
	if !hp.askOpen {
		return
	}
	it := hp.ask
	name := core.TruncateName(strings.Join(strings.Fields(it.title), " "), 40) // a take's title is its whole script
	note := firstOf(it.delNote, i18n.T("This cannot be undone."))
	if ui.AlertDialog(c.Key("home-ask"), &hp.askOpen, i18n.T("Delete “%s”?", name), note, i18n.T("Cancel"), i18n.T("Delete")) == 1 && it.del != nil {
		a.homeDelete(it)
	}
}

// homeDelete deletes a row of Recent (asked already).
func (a *app) homeDelete(it homeItem) {
	it.del()
	a.homeUI.recent = slices.DeleteFunc(a.homeUI.recent, func(x store.LibraryItem) bool { return libKey(x) == it.key })
}
