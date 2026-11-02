package main

// A dub whose working files were freed a while after its export
// (services/dub_free.go, code review 1102 L4): opening it says so, its
// tracks play as mixed, and what needs the decoded audio (the original,
// Mirror) decodes it again first.

import (
	"context"
	"errors"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/i18n"
)

// dubOpened notes that p was opened (its working files stay a week from
// now), and says when they were freed.
func (a *app) dubOpened(p *dub.Project) {
	svc, id := a.dubSvc, p.ID
	a.background(func() { svc.Touch(id) })
	if !p.Freed.IsZero() {
		a.dubNotice = i18n.T("Its working files were freed to save space: they are made again when needed.")
	}
}

// dubRebuild decodes the open dub's audio again.
func (a *app) dubRebuild() {
	p := a.dub
	if p == nil {
		return
	}
	if a.dubBusy() {
		a.toast(i18n.T("The working files are being made again: try again in a moment."))
		return
	}
	svc, id := a.dubSvc, p.ID
	a.toast(i18n.T("The working files were freed to save space: decoding the source again."))
	a.background(func() {
		_, err := svc.Rebuild(context.Background(), id)
		if err != nil && !errors.Is(err, core.ErrBusy) {
			a.post(func() { a.toastWarn(i18n.T("Could not decode the source again: %v", err)) })
		}
	})
}
