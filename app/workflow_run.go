package main

// The Run panel: what stops the workflow from running, the run's
// controls (Run, Resume, Start a new run, Cancel), its progress item by
// item, and the results with Play, Show in Finder and Export all. Then
// the runs themselves (moved from workflow.go): the app's checks of a
// step, start, cancel, and stale results.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/workflow"
)

// wfRunPanel builds the Run panel.
func (a *app) wfRunPanel(c *ui.Context) {
	t := c.Theme()
	w := a.wf
	rep := a.wfReport()
	run := a.wfRun
	busy := a.wfBusy()
	k := tok(t)
	if a.wfErr != "" {
		ui.Column(c.Key("wf-run-err")).Children(func() {
			banner(c, icons.DangerCircle, t.Danger, a.wfErr, func() {
				if iconButton(c.Key("x"), icons.Close, i18n.T("Dismiss")).Clicked() {
					a.wfErr = ""
				}
			})
		})
	}
	// What stops a run.
	if errs := rep.Errors(); errs > 0 {
		statusMark(c, icons.DangerCircle, t.Danger, i18n.N(errs, "%d problem to fix before running", "%d problems to fix before running", errs)).FontWeight(600)
		for i, d := range rep.Diags {
			if d.Severity != workflow.Error {
				continue
			}
			label := d.Text()
			if n := w.Node(d.Node); n != nil {
				label = n.StepName(n.Kind.Label()) + ": " + label
			}
			node := d.Node
			row := ui.ButtonBase(c.Key(fmt.Sprintf("wf-run-diag-%d", i))).Label(label).Padding(px(t, 3), px(t, 6)).Radius(px(t, 6)).Gap(px(t, sp2)).AlignItems(ui.Center)
			if row.Hovered() {
				row.Background(k.Hover)
			}
			if row.Children(func() {
				statusDot(c, t.Danger)
				ui.Text(c, label).FontSize(px(t, fsCaption)).Grow(1).MinWidth(0)
			}).Clicked() && node != "" {
				a.wfSel, a.wfSelEdge, a.wfPanel = node, "", wfPanelStep
			}
		}
	} else if run == nil || a.wfStale() {
		ui.Text(c, i18n.N(rep.Items, "%d item to run", "%d items to run", rep.Items)).FontWeight(600)
	}
	if a.wfStale() && !busy {
		banner(c, icons.InfoCircle, k.Text2, i18n.T("Settings changed since the last run. Running again makes only what changed."), nil)
	}
	// Controls.
	upToDate := run != nil && run.State == workflow.RunDone && !a.wfStale()
	ui.Row(c.Key("wf-run-buttons")).Gap(px(t, 6)).Wrap().Children(func() {
		if busy {
			if secondaryButton(c.Key("wf-run-cancel"), i18n.T("Cancel")).Clicked() {
				a.wfCancel()
			}
			return
		}
		if upToDate {
			// Every result is made: only a new run makes anything.
			if secondaryButton(c.Key("wf-run-again"), i18n.T("Start a new run")).
				Tooltip(i18n.T("Make every result again, even those that are up to date")).Disabled(rep.Errors() > 0).Clicked() {
				a.wfStart(true)
			}
			return
		}
		label := i18n.T("Run workflow")
		switch {
		case a.wfResumable():
			label = i18n.T("Resume")
		case run != nil && run.State == workflow.RunFailed && !a.wfStale():
			label = i18n.T("Retry")
		}
		if secondaryButton(c.Key("wf-run-go"), label).Disabled(rep.Errors() > 0).Clicked() {
			a.wfStart(false)
		}
		if run != nil && run.Signature != "" && ghostButton(c.Key("wf-run-again"), i18n.T("Start a new run")).
			Tooltip(i18n.T("Make every result again, even those that are up to date")).Disabled(rep.Errors() > 0).Clicked() {
			a.wfStart(true)
		}
	})
	if run == nil || len(run.Items) == 0 {
		caption(c, i18n.T("Results show here as each item is made. A run you cancel, or that stops when AudioSmith quits, resumes where it stopped."))
		return
	}
	// Progress.
	done, failed, total := run.Counts()
	ui.Box(c).Height(1).Background(k.Line)
	ui.Row(c).AlignItems(ui.Center).Gap(px(t, sp2)).Children(func() {
		ui.Text(c, i18n.T("%d / %d items ready", done, total)).FontWeight(600).Grow(1).MinWidth(0)
		caption(c, wfRunStateText(run, busy)).SingleLine()
	})
	ui.Progress(c, float64(done)/float64(max(total, 1))).Label(i18n.T("%d / %d items ready", done, total))
	if j, ok := a.wfRunningJob(); ok {
		if n := w.Node(wfJobNode(j)); n != nil {
			msg := n.StepName(n.Kind.Label())
			if j.Progress.Message != "" {
				msg += " · " + j.Progress.Message
			}
			caption(c, msg)
		}
	}
	if failed > 0 {
		ui.Text(c, i18n.N(failed, "%d item failed: fix it, then Retry.", "%d items failed: fix them, then Retry.", failed)).FontSize(px(t, fsCaption)).TextColor(t.Danger)
	}
	if run.Code != "" && !busy {
		ui.Text(c, wfErrorText(run.Code, run.Err)).FontSize(px(t, fsCaption)).TextColor(t.Danger)
	}
	outs := services.Outputs(w, run)
	if len(outs) > 0 {
		ui.Row(c).Children(func() {
			if secondaryIconButton(c.Key("wf-export-all"), icons.Export, i18n.T("Export all…")).Label(i18n.T("Export all…")).Tooltip(i18n.T("Copy every result into a folder")).Clicked() {
				a.wfExportAll()
			}
		})
	}
	ui.Box(c).Height(1).Background(k.Line)
	for i := range run.Items {
		a.wfItemRow(c, i, &run.Items[i], outs)
	}
}

// wfRunStateText says where a run is.
func wfRunStateText(r *workflow.Run, busy bool) string {
	switch {
	case busy:
		return i18n.T("Running")
	case r.State == workflow.RunDone:
		if !r.EndedAt.IsZero() && !r.StartedAt.IsZero() {
			return i18n.T("Done in %s", r.EndedAt.Sub(r.StartedAt).Round(time.Second).String())
		}
		return i18n.T("Done")
	case r.State == workflow.RunFailed:
		return i18n.T("Stopped")
	case r.State == workflow.RunCancelled:
		return i18n.T("Cancelled")
	case r.State == workflow.RunRunning:
		return i18n.T("Interrupted")
	}
	return ""
}

// wfRunningJob is the open workflow's running job, if any.
func (a *app) wfRunningJob() (jobs.Job, bool) {
	for _, j := range a.wfJobs {
		if j.State == jobs.StateRunning {
			return j, true
		}
	}
	return jobs.Job{}, false
}

// wfJobNode is the step a job works for.
func wfJobNode(j jobs.Job) string {
	var p struct {
		Node string `json:"node"`
	}
	json.Unmarshal(j.Params, &p)
	return p.Node
}

// wfItemRow is an item: its state and, open, its results.
func (a *app) wfItemRow(c *ui.Context, i int, it *workflow.RunItem, outs []services.WorkflowOutput) {
	t := c.Theme()
	w := a.wf
	key := it.Key
	st := it.State()
	open := a.wfExpanded[key]
	k := tok(t)
	ui.Column(c.Key("wf-item-"+key)).Gap(px(t, sp1)).Padding(px(t, 2), 0).Children(func() {
		head := ui.ButtonBase(c.Key("head")).Label(it.Name).Gap(px(t, sp2)).AlignItems(ui.Center).Padding(px(t, 4), px(t, 6)).Radius(px(t, 6))
		if head.Hovered() {
			head.Background(k.Hover)
		}
		head.Children(func() {
			ui.Column(c).Size(px(t, 14), px(t, 14)).Center().Shrink(0).Children(func() {
				switch st {
				case workflow.StepDone:
					iconMark(c, icons.CheckCircle, t.Success, i18n.T("Made"))
				case workflow.StepFailed:
					iconMark(c, icons.CloseCircle, t.Danger, i18n.T("Failed"))
				case workflow.StepRunning:
					ui.Spinner(c).Size(px(t, 12), px(t, 12)).Label(i18n.T("Running"))
				default:
					statusDot(c, k.G[5])
				}
			})
			ui.Text(c, fmt.Sprintf("%02d", i+1)).Font(monoFont).FontSize(px(t, fsMicro)).TextColor(k.Text3)
			ui.Text(c, firstOf(it.Name, i18n.T("Item %d", i+1))).FontSize(px(t, fsCaption)).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
			if st == workflow.StepRunning {
				for _, id := range a.wfOrder() {
					if s := it.Steps[id]; s != nil && s.Status == workflow.StepRunning {
						if n := w.Node(id); n != nil {
							ui.Text(c, n.StepName(n.Kind.Label())).FontSize(px(t, fsMicro)).TextColor(k.AccentInk).SingleLine()
						}
						break
					}
				}
			}
			ui.Icon(c, map[bool]icons.Icon{true: icons.AltArrowDown, false: icons.AltArrowRight}[open].Linear).Size(px(t, 12), px(t, 12)).TextColor(k.Text3)
		})
		if head.Clicked() {
			a.wfExpanded[key] = !open
		}
		// Failures show at once.
		for _, id := range a.wfOrder() {
			s := it.Steps[id]
			n := w.Node(id)
			if s == nil || n == nil || s.Status != workflow.StepFailed {
				continue
			}
			ui.Text(c, n.StepName(n.Kind.Label())+": "+wfErrorText(s.Code, s.Err)).FontSize(px(t, fsMicro)).TextColor(t.Danger).MaxLines(3).Margin(0, 0, 0, px(t, 28))
		}
		if !open && st != workflow.StepDone {
			return
		}
		for j, o := range outs {
			if o.Item != key {
				continue
			}
			a.wfOutputRow(c, j, o)
		}
		if open {
			for _, id := range a.wfOrder() {
				s := it.Steps[id]
				n := w.Node(id)
				if s == nil || n == nil {
					continue
				}
				for _, wcode := range s.Warnings {
					ui.Text(c, n.StepName(n.Kind.Label())+": "+wfWarningText(wcode)).FontSize(px(t, fsMicro)).TextColor(t.Warning).Margin(0, 0, 0, px(t, 28))
				}
				if s.Status == workflow.StepSkipped && s.Code == workflow.SkipBranch {
					ui.Text(c, i18n.T("%s: not on this item's branch", n.StepName(n.Kind.Label()))).FontSize(px(t, fsMicro)).TextColor(k.Text2).Margin(0, 0, 0, px(t, 28))
				}
			}
		}
	})
}

// wfOrder is the open workflow's steps in run order.
func (a *app) wfOrder() []string { return a.wfReport().Order }

// wfOutputRow is one result: what it is, Play or its text, Show in Finder.
func (a *app) wfOutputRow(c *ui.Context, j int, o services.WorkflowOutput) {
	t := c.Theme()
	w := a.wf
	n := w.Node(o.Node)
	if n == nil {
		return
	}
	path := a.wfSvc.Path(w.ID, o.Value.File)
	ui.Column(c.Key(fmt.Sprintf("wf-out-%d", j))).Gap(px(t, 2)).Padding(px(t, 4), px(t, 4), px(t, 4), px(t, 10)).Radius(px(t, radiusControl)).
		Background(tok(t).Subtle).Margin(0, 0, 0, px(t, 22)).Children(func() {
		ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
			label := n.StepName(n.Kind.Label())
			switch o.Value.Type {
			case workflow.TypeAudio:
				label += " · " + lfClock(time.Duration(o.Value.Seconds*float64(time.Second)))
			default:
				label += " · " + i18n.N(o.Value.Chars, "%d character", "%d characters", o.Value.Chars)
			}
			ui.Text(c, label).FontSize(px(t, fsMicro)).TextColor(tok(t).Text2).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
			if o.Value.Type == workflow.TypeAudio {
				id := "wf:" + o.Item + ":" + o.Node
				tip, ic := i18n.T("Play"), icons.Play
				if a.pl.Playing(id) {
					tip, ic = i18n.T("Pause"), icons.Pause
				}
				if iconButton(c.Key("play"), ic, tip).Clicked() {
					src := path
					if strings.HasSuffix(strings.ToLower(path), ".m4a") {
						// The player reads WAV: play what was encoded.
						if in := a.wfExportInput(o); in != "" {
							src = in
						}
					}
					if err := a.pl.Toggle(id, src); err != nil {
						a.toastWarn(i18n.T("Can't play: %v", err))
					}
				}
			}
			if a.win != nil && iconButton(c.Key("reveal"), icons.FolderOpen, i18n.T("Show in Finder")).Clicked() {
				mygo.Shell.ShowItemInFolder(path)
			}
		})
		if o.Value.Type != workflow.TypeAudio && o.Value.Text != "" {
			ui.Text(c, o.Value.Text).FontSize(px(t, fsCaption)).MaxLines(4).Ellipsis("…").Selectable()
		}
	})
}

// wfExportInput is the WAV an export step encoded, for playing.
func (a *app) wfExportInput(o services.WorkflowOutput) string {
	run := a.wfRun
	if run == nil {
		return ""
	}
	i := slices.IndexFunc(run.Items, func(it workflow.RunItem) bool { return it.Key == o.Item })
	if i < 0 {
		return ""
	}
	for _, e := range a.wf.Incoming(o.Node) {
		if s := run.Items[i].Steps[e.From]; s != nil && s.Out != nil && s.Out.Type == workflow.TypeAudio {
			return a.wfSvc.Path(a.wf.ID, s.Out.File)
		}
	}
	return ""
}

// --- checks -------------------------------------------------------------------------------

// wfReport is the open workflow's validation, kept until it changes.
func (a *app) wfReport() *workflow.Report {
	if a.wf == nil {
		return &workflow.Report{}
	}
	if a.wfRep == nil || a.wfRepVer != a.wfVer {
		a.wfRep, a.wfRepVer = workflow.Validate(a.wf, a.wfCheck), a.wfVer
	}
	return a.wfRep
}

// App-side diagnostics (workflow.Check).
const (
	wfCodeNotDownloaded = "app.not_downloaded"
	wfCodeVoice         = "app.voice"
	wfCodeTranslator    = "app.translator"
	wfCodeSTT           = "app.stt"
	wfCodeEncode        = "app.encode"
)

// wfCheck is what only the app knows about a step: its model downloaded,
// its voice there and usable, a translator and a speech-to-text model
// set up. It reads memory only (the models' state, the voices).
func (a *app) wfCheck(n *workflow.Node, _ workflow.Type) []workflow.Diagnostic {
	var out []workflow.Diagnostic
	add := func(sev workflow.Severity, code, msg string) {
		out = append(out, workflow.Diagnostic{Severity: sev, Code: code, Node: n.ID, Arg: msg})
	}
	stt := func() {
		m := dubModels(a.lib)
		if m.STT == "" {
			add(workflow.Error, wfCodeSTT, i18n.T("No speech-to-text model is available: download one in Settings → Models & network."))
			return
		}
		if e, ok := catalog.Builtin().ByRepo(m.STT); ok {
			if ready, known := a.modelOnDisk(e); known && !ready {
				add(workflow.Warning, wfCodeNotDownloaded, i18n.T("%s is not downloaded yet.", modelRef(e)))
			}
		}
	}
	switch n.Kind {
	case workflow.KindSpeak, workflow.KindConvert:
		if n.P.Model == "" {
			break
		}
		e := entryFor(n.P.Model)
		if ready, known := a.modelOnDisk(e); known && !ready {
			add(workflow.Warning, wfCodeNotDownloaded, i18n.T("%s is not downloaded yet.", modelRef(e)))
		}
		if e.NeedsReference && (n.P.Voice == "" || strings.Contains(n.P.Voice, ":")) {
			add(workflow.Error, wfCodeVoice, i18n.T("%s only clones voices: choose a saved cloned voice.", modelRef(e)))
		}
		if ref := n.P.Voice; ref != "" && !strings.Contains(ref, ":") {
			v := a.findVoice(ref)
			switch {
			case v == nil:
				add(workflow.Error, wfCodeVoice, i18n.T("Its voice was deleted: choose another."))
			default:
				if ok, why := voiceUsable(v, e); !ok {
					add(workflow.Error, wfCodeVoice, why)
				}
			}
		}
		if n.Kind == workflow.KindConvert {
			stt()
		}
	case workflow.KindTranscribe:
		stt()
	case workflow.KindTranslate:
		if ok, _ := a.dubTranslatorReady(dub.Settings{Translator: n.P.Translator}); !ok {
			add(workflow.Error, wfCodeTranslator, i18n.T("Translating needs a translation engine: a local model (Ollama, LM Studio) or a provider in Settings → Writing help."))
		}
	case workflow.KindExport:
		if n.P.Format == workflow.FormatM4A && !a.wfCanEncode() {
			add(workflow.Warning, wfCodeEncode, i18n.T("M4A needs the speech engine; it is not running."))
		}
	}
	return out
}

// wfCanEncode reports whether M4A can be written now.
func (a *app) wfCanEncode() bool {
	return a.wfEngine != nil || a.eng != nil && a.eng.sup != nil
}

// --- runs ---------------------------------------------------------------------------------

// wfStart runs the open workflow (again: every result anew).
func (a *app) wfStart(again bool) {
	if a.wf == nil || a.wfBusy() {
		return
	}
	rep := a.wfReport()
	a.wfPanel = wfPanelRun
	if !rep.OK() {
		a.wfErr = i18n.T("Fix the steps marked in red first.")
		return
	}
	a.wfErr = ""
	w := a.wf.Clone()
	svc, sched := a.wfSvc, a.lib.sched
	a.background(func() {
		ctx := context.Background()
		// The working copy first: the run reads the saved one.
		_, err := svc.Save(ctx, w)
		var run *workflow.Run
		if err == nil {
			run, err = svc.Run(ctx, w.ID, services.RunOptions{Again: again, Priority: jobs.PriorityNormal})
		}
		var active []jobs.Job
		if err == nil && sched != nil {
			active, _ = svc.Active(ctx, w.ID)
		}
		a.post(func() {
			if a.wf == nil || a.wf.ID != w.ID {
				return
			}
			if err != nil {
				var inv *services.WorkflowInvalidError
				switch {
				case errors.As(err, &inv):
					a.wfErr = i18n.T("Fix the steps marked in red first.")
				case errors.Is(err, core.ErrBusy):
					a.wfErr = i18n.T("This workflow is running already.")
				default:
					a.wfErr = i18n.T("Could not start the run: %v", err)
				}
				return
			}
			// Events may have brought a later state of this run already.
			if a.wfRun == nil || a.wfRun.ID != run.ID {
				a.wfRun = run
			}
			for _, j := range active {
				if !j.State.Terminal() {
					a.wfJobs[j.ID] = j
				}
			}
			if len(run.Steps) == 0 {
				a.toast(i18n.T("Nothing changed since the last run: every result is ready."))
			}
		})
	})
}

// wfCancel stops the open workflow's run (it can resume later).
func (a *app) wfCancel() {
	if a.wf == nil {
		return
	}
	svc, id := a.wfSvc, a.wf.ID
	a.background(func() {
		if err := svc.Cancel(context.Background(), id); err != nil {
			a.post(func() { a.toastWarn(i18n.T("Could not stop the run: %v", err)) })
		}
	})
}

// wfStale reports whether the workflow changed since its last run.
func (a *app) wfStale() bool {
	if a.wf == nil || a.wfRun == nil || a.wfRun.Signature == "" {
		return false
	}
	rep := a.wfReport()
	if !rep.OK() {
		return false
	}
	items := a.wf.Items()
	return workflow.RunSignature(workflow.StepKeys(a.wf, rep, items, a.wfSalt()), items) != a.wfRun.Signature
}

// wfSalt is the service's salt (the speech-to-text model).
func (a *app) wfSalt() workflow.Salt {
	m := dubModels(a.lib)
	return func(n *workflow.Node) string {
		switch n.Kind {
		case workflow.KindTranscribe, workflow.KindConvert:
			return m.STT + "|" + m.Align
		}
		return ""
	}
}
