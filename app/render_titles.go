package main

// The names of renders made from drafts never saved (thread #143): such
// a render was stored as 「未命名」 ("Untitled"); the lists show the first
// words of its script instead, without rewriting what is stored, and new
// ones are stored under those words (startLf).

import (
	"strings"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/services"
)

// renderTitleRunes is how long a draft render's name is, at most.
const renderTitleRunes = 20

// untitledNames are the names a draft render was stored under, in every
// language the app speaks (the language may have changed since).
var untitledNames = []string{"Untitled", "未命名"}

// untitled reports whether title is no name.
func untitled(title string) bool {
	title = strings.TrimSpace(title)
	if title == "" || title == i18n.T("Untitled") {
		return true
	}
	for _, s := range untitledNames {
		if title == s {
			return true
		}
	}
	return false
}

// renderName is the name a render shows: its title, or for a draft's
// render without one the first words of its script (spec: its manifest,
// with its chapters); "" when it has neither.
func renderName(rec core.Render, spec *services.LongformSpec) string {
	if !untitled(rec.Title) {
		return strings.TrimSpace(rec.Title)
	}
	if rec.ProjectID == "" && spec != nil {
		return spec.FirstWords(renderTitleRunes)
	}
	return ""
}

// renderTitle is render id's name for a list: stored is the title the
// list has. A render not followed yet is read in the background
// (renderFor); until then, and when its script says nothing, 「未命名」.
func (a *app) renderTitle(id, stored string) string {
	if !untitled(stored) {
		return strings.TrimSpace(stored)
	}
	if r := a.lfRenders[id]; r != nil {
		return firstOf(r.name, i18n.T("Untitled"))
	}
	if a.lib != nil && a.projects.asked != nil {
		a.renderFor(id)
	}
	return i18n.T("Untitled")
}
