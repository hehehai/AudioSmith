package main

// The activity cards (the ActNow family's window spec §2): what goes on,
// instead of a status bar; since 0.5 F1 the tasks bell counts them
// and lists them in its popover (shell_bell.go), the Tasks page in
// full. In order: a stopped
// engine (red, Restart), dictation and calls under way (Stop, Hang up),
// the jobs by kind — each story or book render, dub and workflow run its
// own card, the Studio's takes, exports and transcriptions one card a
// kind, "2/5" when there are several — and the model downloads. A kind's
// finished card stays 3 s. actnowkit/activity is the logic (jobs by kind,
// finished cards, the stack's geometry); this file words and draws them.
//
// A click on a card goes where it is managed; its button stops it.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/oplog"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/actnowkit/activity"
)

// activityState is the cards' view state.
type activityState struct {
	actTracker activity.Tracker
	actCards   []actCard // this frame's (buildActivity)
	actTick    bool      // a card counts time or goes soon: build again
}

// actCard is a card: a row of the bell's popover and the Tasks page.
type actCard struct {
	key    string
	icon   icons.Icon
	title  string
	detail string  // on the right: "42%", "2/5", "1:05", "Waiting"
	frac   float64 // the bar: 0–1, -1 going on (no fraction), 2 none
	danger bool    // needs the user: red, its button always shows
	done   bool    // finished: a check, no bar
	open   func()  // where it is managed
	action *actAction
}

// actAction is a card's button.
type actAction struct {
	icon icons.Icon
	tip  string
	run  func()
}

// activityKind is the card a job belongs to: "" none (a call's, the
// dictation's, a download's, which have cards of their own; the voice
// pages' previews). Each render, dub and workflow run is one card, by its
// owner; the rest one card a kind.
func (a *app) activityKind(j jobs.Job) string {
	o := j.Owner
	switch {
	case strings.HasPrefix(o, services.CallOwner), strings.HasPrefix(o, "dictation:"), strings.HasPrefix(o, "model:"),
		strings.HasPrefix(o, "preview:"), strings.HasPrefix(o, "archetype:"), strings.HasPrefix(o, "family:"):
		return ""
	case a.dict != nil && a.dict.trID != "" && o == services.TranscriptOwner+a.dict.trID:
		return ""
	case strings.HasPrefix(o, services.OwnerLongform), strings.HasPrefix(o, services.DubOwner), strings.HasPrefix(o, services.WorkflowOwner):
		return o
	case strings.HasPrefix(o, services.DubExportOwner), j.Kind == services.KindEncode:
		return "export"
	case strings.HasPrefix(o, services.TranscriptOwner):
		return "transcribe"
	case strings.HasPrefix(o, services.IsolateOwner):
		return "isolate"
	case strings.HasPrefix(o, voiceWorkOwner):
		return "voice"
	case j.Kind == services.KindRender, strings.HasPrefix(o, "take:"):
		return "speech"
	}
	return j.Kind
}

// activityTitle names a kind's card when it has several jobs ("" none:
// the first job's title), done its finished card.
func (a *app) activityTitle(kind string, done bool) string {
	id := kind[strings.IndexByte(kind, ':')+1:]
	switch {
	case kind == "speech":
		if done {
			return i18n.T("Speech generated")
		}
		return i18n.T("Generating speech")
	case kind == "export":
		if done {
			return i18n.T("Exported")
		}
		return i18n.T("Exporting")
	case kind == "transcribe":
		if done {
			return i18n.T("Transcribed")
		}
		return i18n.T("Transcribing")
	case kind == "voice":
		if done {
			return i18n.T("Voice work done")
		}
		return i18n.T("Working on a voice")
	case kind == "isolate":
		if done {
			return i18n.T("Voices isolated")
		}
		return i18n.T("Isolating voices")
	case strings.HasPrefix(kind, services.OwnerLongform):
		name := ""
		if r := a.lfRenders[id]; r != nil {
			name = r.rec.Title
		}
		if name == "" {
			name = i18n.T("Render")
		}
		if done {
			return i18n.T("%s is ready", name)
		}
		return name
	case strings.HasPrefix(kind, services.DubOwner):
		name := i18n.T("Dub")
		for _, r := range a.dubList {
			if r.ID == id && r.Title != "" {
				name = r.Title
			}
		}
		if done {
			return i18n.T("%s is dubbed", name)
		}
		return name
	case strings.HasPrefix(kind, services.WorkflowOwner):
		name := i18n.T("Workflow")
		for _, r := range a.wfList {
			if r.ID == id && r.Name != "" {
				name = r.Name
			}
		}
		if done {
			return i18n.T("%s finished", name)
		}
		return name
	}
	return ""
}

// jobEnded tells the tracker a job it showed ended (jobsChanged).
func (a *app) jobEnded(j jobs.Job) {
	kind := a.activityKind(j)
	if kind == "" {
		return
	}
	title := a.activityTitle(kind, true)
	if title == "" {
		title = i18n.T("%s: done", orDash(j.Title))
	}
	a.actTracker.Ended(kind, title, j.State == jobs.StateSucceeded)
}

// buildActivity makes this frame's cards.
func (a *app) buildActivity() {
	a.actCards, a.actTick = a.actCards[:0], false
	add := func(c actCard) { a.actCards = append(a.actCards, c) }
	if a.engState == engineFailed {
		add(actCard{key: "engine", icon: icons.DangerTriangle, title: i18n.T("Engine stopped"), frac: 2, danger: true,
			open:   func() { a.navigate("/engine") },
			action: &actAction{icons.Restart, i18n.T("Restart engine"), a.restartEngine}})
	}
	if s := a.dict; s != nil && s.phase.live() {
		d := ""
		if !s.started.IsZero() {
			d = clock(time.Since(s.started).Seconds())
			a.actTick = true
		}
		add(actCard{key: "dictation", icon: micIcon, title: i18n.T("Dictating"), detail: d, frac: -1,
			open:   func() { a.navigate("/settings/dictation") },
			action: &actAction{icons.Stop, i18n.T("Stop dictation"), a.dictToggle}})
	}
	for _, c := range a.callFull {
		if c == nil || !c.Live() {
			continue
		}
		id, d := c.ID, ""
		if !c.Answered.IsZero() {
			d = clock(time.Since(c.Answered).Seconds())
			a.actTick = true
		}
		add(actCard{key: "call-" + id, icon: callsIcon, title: c.Title(), detail: d, frac: -1,
			open:   func() { a.navigate("/calls") },
			action: &actAction{icons.EndCallRounded, i18n.T("Hang up"), func() { a.hangupCall(id) }}})
	}
	var active []activity.Job
	running := map[string]jobs.Job{} // each kind's first running job
	for _, j := range a.activeJobs() {
		kind := a.activityKind(j)
		if kind == "" {
			continue
		}
		if _, ok := running[kind]; !ok && j.State == jobs.StateRunning {
			running[kind] = j
		}
		title := j.Title
		if title == "" {
			title = j.Kind
		}
		active = append(active, activity.Job{ID: j.ID, Kind: kind, Title: title, Running: j.State == jobs.StateRunning, Fraction: j.Progress.Fraction})
	}
	cards := a.actTracker.Cards(time.Now(), active, func(kind string) string { return a.activityTitle(kind, false) })
	a.actTick = a.actTick || activity.Showing(cards)
	for _, c := range cards {
		add(a.jobCard(c, running[c.Kind]))
	}
	for _, m := range a.downloads() {
		e := m.entry
		d, f := "", -1.0
		if m.progress.TotalBytes > 0 {
			f = float64(m.progress.Bytes) / float64(m.progress.TotalBytes)
			d = fmt.Sprintf("%d%%", int(100*f))
		}
		add(actCard{key: "dl-" + modelKey(e), icon: icons.DownloadMinimalistic, title: i18n.T("Downloading %s", modelRef(e)), detail: d, frac: f,
			open:   func() { a.navigate("/settings/models") },
			action: &actAction{icons.CloseCircle, i18n.T("Cancel the download"), func() { a.cancelDownload(e) }}})
	}
}

// jobCard words a card of jobs; run is the kind's running job (zero if
// none). Several jobs show "done/total"; a dub's also how far its running
// step is ("37% · 0/5": the download), with its bar.
func (a *app) jobCard(c activity.Card, run jobs.Job) actCard {
	kind := c.Kind
	ic := icons.Restart
	switch {
	case kind == "speech":
		ic = studioIcon
	case kind == "export":
		ic = icons.Export
	case kind == "transcribe":
		ic = transcribeIcon
	case kind == "voice":
		ic = voicesIcon
	case kind == "isolate":
		ic = isolateIcon
	case strings.HasPrefix(kind, services.OwnerLongform):
		ic = storiesIcon
		if r := a.lfRenders[kind[len(services.OwnerLongform):]]; r != nil && r.rec.Kind == core.ProjectAudiobook {
			ic = audiobookIcon
		}
	case strings.HasPrefix(kind, services.DubOwner):
		ic = dubIcon
	case strings.HasPrefix(kind, services.WorkflowOwner):
		ic = workflowsIcon
	}
	out := actCard{key: c.Key, icon: ic, title: c.Title, frac: c.Frac}
	switch {
	case c.Finished:
		out.done, out.frac, out.icon = true, 2, icons.CheckCircle
		if c.Done > 1 {
			out.detail = i18n.N(c.Done, "%d item", "%d items", c.Done)
		}
	case c.Total > 1 && strings.HasPrefix(kind, services.DubOwner):
		// A dub's steps differ (a download, then transcribing…): the bar
		// is the running step's, when it says how far it is.
		out.detail, out.frac = itoa(c.Done)+"/"+itoa(c.Total), -1
		if f := determinate(run); f > 0 {
			out.detail, out.frac = percent(f)+" · "+out.detail, f
		}
	case c.Total > 1:
		// Like items (chapters, exports): the bar is all of them, with
		// how far the running one is.
		out.detail = itoa(c.Done) + "/" + itoa(c.Total)
		out.frac = (float64(c.Done) + max(determinate(run), 0)) / float64(c.Total)
	case c.Waiting:
		out.detail, out.frac = i18n.T("Waiting"), 0
	case c.Frac >= 0:
		out.detail = fmt.Sprintf("%d%%", int(100*c.Frac))
	}
	ids := c.Jobs
	out.open = func() { a.openActivity(kind) }
	if !c.Finished {
		tipText := i18n.T("Cancel")
		if strings.HasPrefix(kind, services.OwnerLongform) || strings.HasPrefix(kind, services.DubOwner) || strings.HasPrefix(kind, services.WorkflowOwner) {
			tipText = i18n.T("Stop")
		}
		out.action = &actAction{icons.CloseCircle, tipText, func() { a.stopActivity(kind, ids) }}
	}
	return out
}

// openActivity goes where a kind's work is managed.
func (a *app) openActivity(kind string) {
	id := kind[strings.IndexByte(kind, ':')+1:]
	switch {
	case kind == "speech":
		if a.job != nil {
			a.navigate("/studio")
		} else {
			a.navigate("/history")
		}
	case kind == "transcribe":
		a.navigate("/transcripts")
	case kind == "voice":
		a.navigate("/voices")
	case kind == "isolate":
		a.navigate("/isolate")
	case strings.HasPrefix(kind, services.OwnerLongform):
		if r := a.lfRenders[id]; r != nil {
			a.navigate("/" + lfRoute(r.rec.Kind))
			return
		}
		a.navigate("/tasks")
	case strings.HasPrefix(kind, services.DubOwner):
		a.openDub(id)
	case strings.HasPrefix(kind, services.WorkflowOwner):
		a.navigate("/workflows")
	default:
		a.navigate("/tasks")
	}
}

// stopActivity stops a card's work: a render, dub or workflow run through
// its service (what was made is kept), other jobs one by one.
func (a *app) stopActivity(kind string, ids []string) {
	id := kind[strings.IndexByte(kind, ':')+1:]
	ctx := context.Background()
	switch {
	case strings.HasPrefix(kind, services.OwnerLongform) && a.lib != nil && a.lib.svc != nil:
		svc := a.lib.svc.Longform
		a.background(func() { oplog.Err("job.stop.ask", svc.Stop(ctx, id), "id", id) })
	case strings.HasPrefix(kind, services.DubOwner) && a.dubSvc != nil:
		svc := a.dubSvc
		a.background(func() { oplog.Err("job.cancel.ask", svc.Cancel(ctx, id), "id", id) })
	case strings.HasPrefix(kind, services.WorkflowOwner) && a.wfSvc != nil:
		svc := a.wfSvc
		a.background(func() { oplog.Err("job.cancel.ask", svc.Cancel(ctx, id), "id", id) })
	default:
		for _, jid := range ids {
			if j, ok := a.jobList[jid]; ok {
				a.cancelJobByID(j)
			}
		}
	}
}
