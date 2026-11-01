package main

// WorkflowStudio (M6, thread #21): /workflows edits node-graph workflows
// (internal/workflow) on a canvas and runs them through services.Workflows
// (migration 006; one job per step, owner "workflow:<id>"). The page keeps
// a working copy of the open workflow: every edit changes it and saves a
// copy in the background (autosave); the service's WorkflowChanged
// brings runs back. Editing is locked while a run goes on.
//
// Files of the area:
//
//	workflow.go               this: state, wiring, the library
//	workflow_edit.go          edits: autosave, add / place / remove / connect steps
//	workflow_run.go           runs: the run panel (progress, results), checks, start, cancel
//	workflow_files.go         files: export all, the inputs (scripts, clips, a folder)
//	workflow_text.go          texts: errors and warnings
//	workflow_canvas.go        the canvas: state, geometry (ports, links, hits), pan and zoom
//	workflow_canvas_input.go  the canvas's gestures and keys
//	workflow_canvas_draw.go   drawing the canvas: links, step cards, minimap
//	workflow_view.go          the page: library rail, toolbar, the right panel
//	workflow_inspector.go     the step details and inputs (scripts, clips)
//	workflow_pages.go         sidebar, route, title, palette

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/store"
	"github.com/hehehai/AudioSmith/internal/translate"
	"github.com/hehehai/AudioSmith/internal/workflow"
)

// wfState is the Workflows page's state.
type wfState struct {
	wfSvc    *services.Workflows
	wfList   []store.WorkflowRecord // newest change first
	wfLoaded bool
	wf       *workflow.Workflow // the open workflow (the working copy); nil = none
	wfRun    *workflow.Run      // its last run
	wfJobs   map[string]jobs.Job
	wfVer    int // bumps on every edit (wfReport follows it)
	wfRep    *workflow.Report
	wfRepVer int

	wfSaving, wfPending bool // a save in flight, and another wanted after it

	wfSel      string // the selected step
	wfSelEdge  string // the selected link
	wfPanel    int    // the right panel: wfPanelStep or wfPanelRun
	wfRenaming string // the workflow being renamed in the list
	wfRenameTx string
	wfConfirm  string // the workflow asked to delete
	wfAsk      bool
	wfErr      string // why the last action failed (translated)
	wfExpanded map[string]bool
	wfCanvas   wfCanvasState
	wfCanvasH  ui.Handle

	// The side panels (0.3): the library rail can be hidden; when the
	// page is narrow the rail and the step/run panel are drawers.
	wfRailHide  bool   // the rail hidden while it would fit
	wfRailOpen  bool   // the rail's drawer open
	wfPanelOpen bool   // the step/run panel's drawer open
	wfPanelFor  string // the step the drawer opened for
	wfPanelSeen int    // the panel tab the drawer opened for

	// wfEngine replaces the engine's executors (tests).
	wfEngine func(method string) jobs.Executor
}

// The right panel.
const (
	wfPanelStep = iota
	wfPanelRun
)

// wfSchedFor wraps the workflow service's scheduler (tests).
var wfSchedFor func(jobs.Scheduler) jobs.Scheduler

// openWorkflows makes the workflow service over the library and follows
// its events and jobs.
func (a *app) openWorkflows(lib *library) {
	a.wfJobs = map[string]jobs.Job{}
	a.wfExpanded = map[string]bool{}
	host := a.eng
	router := a.llmRouter
	sched := lib.sched
	if wfSchedFor != nil {
		sched = wfSchedFor(sched)
	}
	a.wfSvc = services.NewWorkflows(services.WorkflowsConfig{
		Store: lib.store.Workflows(), Bus: lib.bus, Scheduler: sched, Render: lib.svc.Render, Async: a.background,
		Engine: func(method string) jobs.Executor {
			if a.wfEngine != nil {
				return a.wfEngine(method)
			}
			if host == nil || host.sup == nil {
				return nil
			}
			return engine.Executor(host.sup, dub.RoleOf(method), method, engine.ExecutorOptions{AuxPaths: lib.auxSnapshots})
		},
		ModelPath: lib.modelSnapshot,
		STT:       func() services.WorkflowSTT { return workflowSTT(lib) },
		Caps:      dubCaps,
		Voice:     a.dubVoiceResolver(lib),
		Translator: func(ctx context.Context, name string) (translate.Translator, error) {
			return dubMakeTranslator(router, dub.Settings{Translator: name})
		},
		TranslateLane: func() jobs.Lane { return translateLane(router) }, // local_llm.go
	})
	core.On(lib.bus, func(ev services.WorkflowChanged) { a.post(func() { a.wfChanged(ev) }) })
	core.On(lib.bus, func(u jobs.Updated) {
		if u.Chunk == nil && strings.HasPrefix(u.Job.Owner, services.WorkflowOwner) {
			a.post(func() { a.wfJobUpdated(u.Job) })
		}
	})
	a.wfLoad()
}

// wfLoad reads the library, and opens the workflow shown last.
func (a *app) wfLoad() {
	svc := a.wfSvc
	a.background(func() {
		list, err := svc.List(context.Background())
		a.post(func() {
			a.wfLoaded = true
			if err != nil {
				a.toastWarn(i18n.T("Could not read the workflows: %v", err))
				return
			}
			a.wfList = list
			if a.wf == nil && len(list) > 0 {
				a.wfOpen(list[0].ID)
			}
		})
	})
}

// wfOpen opens a workflow, with its run and its unfinished jobs.
func (a *app) wfOpen(id string) {
	svc := a.wfSvc
	a.background(func() {
		ctx := context.Background()
		w, run, err := svc.Get(ctx, id)
		var active []jobs.Job
		if err == nil {
			active, _ = svc.Active(ctx, id)
		}
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not open the workflow: %v", err))
				return
			}
			a.wfShow(w, run, active)
		})
	})
}

// wfShow makes w the open workflow.
func (a *app) wfShow(w *workflow.Workflow, run *workflow.Run, active []jobs.Job) {
	a.wf, a.wfRun = w, run
	a.wfJobs = map[string]jobs.Job{}
	for _, j := range active {
		a.wfJobs[j.ID] = j
	}
	a.wfSel, a.wfSelEdge, a.wfErr = "", "", ""
	a.wfCanvas = wfCanvasState{w: a.wfCanvas.w, h: a.wfCanvas.h, fit: true}
	a.wfVer++
	if a.replaceRemovedWorkflow(w) { // models_removed.go
		a.wfSave()
	}
	if a.wfBusy() {
		a.wfPanel = wfPanelRun
	}
}

// wfChanged follows the service: the list, and the open workflow's run.
func (a *app) wfChanged(ev services.WorkflowChanged) {
	i := slices.IndexFunc(a.wfList, func(r store.WorkflowRecord) bool { return r.ID == ev.ID })
	// The Projects page lists names and run states.
	if i < 0 || ev.Deleted || a.wfList[i].Name != ev.Record.Name || a.wfList[i].State != ev.Record.State {
		a.projects.dirty = true
	}
	if ev.Deleted {
		if i >= 0 {
			a.wfList = slices.Delete(a.wfList, i, i+1)
		}
		if a.wf != nil && a.wf.ID == ev.ID {
			a.wf, a.wfRun = nil, nil
			if len(a.wfList) > 0 {
				a.wfOpen(a.wfList[0].ID)
			}
		}
		return
	}
	if ev.Record.ID != "" {
		if i >= 0 {
			a.wfList[i] = ev.Record
		} else {
			a.wfList = append(a.wfList, ev.Record)
		}
		slices.SortStableFunc(a.wfList, func(x, y store.WorkflowRecord) int { return y.UpdatedAt.Compare(x.UpdatedAt) })
	}
	if a.wf != nil && a.wf.ID == ev.ID && ev.Run != nil {
		a.wfRun = ev.Run
	}
}

// wfJobUpdated follows the open workflow's jobs.
func (a *app) wfJobUpdated(j jobs.Job) {
	if a.wf == nil || j.Owner != services.WorkflowOwner+a.wf.ID {
		return
	}
	if j.State.Terminal() || j.State == jobs.StateInterrupted {
		delete(a.wfJobs, j.ID)
		if j.State == jobs.StateFailed && j.Err != nil && j.Err.Code != jobs.CodeShutdown {
			a.wfErr = wfErrorText(j.Err.Code, j.Err.Message)
		}
		if !a.wfBusy() && j.State == jobs.StateSucceeded && a.wfRun != nil {
			done, failed, total := a.wfRun.Counts()
			if failed == 0 && done == total {
				a.notify(core.SeveritySuccess, i18n.T("Workflow finished"), i18n.T("%s: %d of %d items ready.", a.wf.Name, done, total), "/workflows")
			}
		}
		return
	}
	a.wfJobs[j.ID] = j
}

// wfBusy reports whether the open workflow is running.
func (a *app) wfBusy() bool { return len(a.wfJobs) > 0 }

// wfLocked reports whether the open workflow can't be edited now.
func (a *app) wfLocked() bool { return a.wf == nil || a.wfBusy() }

// --- the library ----------------------------------------------------------------------

// wfNames are the names in the library.
func (a *app) wfNames() []string {
	var out []string
	for _, r := range a.wfList {
		out = append(out, r.Name)
	}
	return out
}

// wfTemplateOptions are the Studio's model and voice, for new steps.
func (a *app) wfTemplateOptions() workflow.TemplateOptions {
	e := a.preset()
	o := workflow.TemplateOptions{Model: e.Repo}
	if a.studioVoice != "" {
		if v := a.findVoice(a.studioVoice); v != nil {
			if ok, _ := voiceUsable(v, e); ok {
				o.Voice = v.ID
			}
		}
	}
	return o
}

// wfNew makes a workflow: Start → Speak → Export, or a template ("" =
// the blank one), and opens it.
func (a *app) wfNew(template string) {
	now := time.Now()
	var w *workflow.Workflow
	if tp, ok := workflow.TemplateByID(template); ok {
		w = tp.Make(core.NewID(now), workflow.TemplateName(tp.ID), now, a.wfTemplateOptions())
	} else {
		w = workflow.New(core.NewID(now), i18n.T("Untitled workflow"), now)
		o := a.wfTemplateOptions()
		for i := range w.Nodes {
			if w.Nodes[i].Kind == workflow.KindSpeak {
				w.Nodes[i].P.Model, w.Nodes[i].P.Voice = o.Model, o.Voice
			}
		}
	}
	if slices.Contains(a.wfNames(), w.Name) {
		w.Name = workflow.CopyName(w.Name, a.wfNames())
	}
	w.Normalize()
	a.wfAdopt(w)
}

// wfAdopt opens a new workflow and saves it.
func (a *app) wfAdopt(w *workflow.Workflow) {
	a.wfShow(w, nil, nil)
	a.wfList = append([]store.WorkflowRecord{{ID: w.ID, Name: w.Name, CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt}}, a.wfList...)
	a.wfPanel = wfPanelStep
	if in := w.InputNode(); in != nil {
		a.wfSel = in.ID
	}
	a.wfSave()
}

// wfDuplicate copies the open workflow (without its run).
func (a *app) wfDuplicate() {
	if a.wf == nil {
		return
	}
	now := time.Now()
	a.wfAdopt(a.wf.Duplicate(core.NewID(now), workflow.CopyName(a.wf.Name, a.wfNames()), now))
}

// wfRename renames a workflow of the list.
func (a *app) wfRename(id, name string) {
	name = strings.TrimSpace(name)
	a.wfRenaming = ""
	if name == "" {
		return
	}
	if a.wf != nil && a.wf.ID == id {
		a.wfEdit(func(w *workflow.Workflow) { w.Name = name })
		return
	}
	svc := a.wfSvc
	a.background(func() {
		ctx := context.Background()
		w, _, err := svc.Get(ctx, id)
		if err == nil {
			w.Name = name
			_, err = svc.Save(ctx, w)
		}
		if err != nil {
			a.post(func() { a.toastWarn(i18n.T("Could not rename the workflow: %v", err)) })
		}
	})
}

// wfDelete deletes a workflow, its run and its files.
func (a *app) wfDelete(id string) {
	svc := a.wfSvc
	a.background(func() {
		if err := svc.Delete(context.Background(), id); err != nil {
			a.post(func() { a.toastWarn(i18n.T("Could not delete the workflow: %v", err)) })
		}
	})
}

// workflowSTT are the speech-to-text models of Transcribe and Convert
// steps: the dub pipeline's (Qwen3-ASR, Whisper for other languages).
func workflowSTT(lib *library) services.WorkflowSTT {
	m := dubModels(lib)
	return services.WorkflowSTT{Model: m.STT, Aligner: m.Align, Words: m.WordTimes, Languages: m.STTLanguages, Other: m.OtherSTT, OtherWords: m.OtherWordTimes}
}
