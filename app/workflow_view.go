package main

// The Workflows page (/workflows): the library and the step palette on
// the left, the toolbar and the canvas in the middle, the step's details
// or the run on the right.

import (
	"context"
	"slices"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/store"
	"github.com/hehehai/AudioSmith/internal/workflow"
)

var workflowsIcon = icons.Structure

// wfRailWidth is the library rail's width; wfPanelWidth the step/run
// panel's.
const (
	wfRailWidth  = 220
	wfPanelWidth = inspectorWidth + 40
)

// wfLayout says where the rail and the step/run panel go: beside the
// canvas (inline) when it keeps 480 DIPs, else in drawers over it.
func (a *app) wfLayout(c *ui.Context) (railInline, panelInline bool) {
	t := c.Theme()
	w := a.pageWidth(c)
	rail, panel := px(t, wfRailWidth), px(t, wfPanelWidth)
	railInline = w-rail >= px(t, 560)
	if railInline && !a.wfRailHide {
		w -= rail
	}
	panelInline = w-panel >= px(t, 480)
	return railInline, panelInline
}

// workflowsPage is /workflows.
func (a *app) workflowsPage(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	// A workflow open: its name, under Workflows.
	head := pageHead{title: i18n.T("Workflows"), count: listCount(len(a.wfList), true)}
	if a.wf != nil && a.wf.Name != "" {
		head = pageHead{title: a.wf.Name, parent: i18n.T("Workflows"), count: head.count}
	}
	railInline, panelInline := a.wfLayout(c)
	railShown := (railInline && !a.wfRailHide) || (!railInline && a.wfRailOpen)
	// In a drawer, the panel opens when a step is chosen or a run starts.
	if !panelInline && a.wf != nil {
		if a.wfSel != "" && a.wfSel != a.wfPanelFor {
			a.wfPanelOpen = true
		}
		if a.wfPanel != a.wfPanelSeen && a.wfPanel == wfPanelRun {
			a.wfPanelOpen = true
		}
	}
	a.wfPanelFor, a.wfPanelSeen = a.wfSel, a.wfPanel
	panelShown := a.wf != nil && (panelInline || a.wfPanelOpen)
	head.tools = func(bool) {
		tip := i18n.T("Show workflows and steps")
		if railShown {
			tip = i18n.T("Hide workflows and steps")
		}
		if panelToggle(c.Key("wf-rail-toggle"), icons.PanelLeft, railShown, tip).Clicked() {
			if railInline {
				a.wfRailHide = !a.wfRailHide
			} else {
				a.wfRailOpen = !a.wfRailOpen
			}
		}
		if a.wf != nil && !panelInline {
			tip := i18n.T("Show the step and the run")
			if panelShown {
				tip = i18n.T("Hide the step and the run")
			}
			if panelToggle(c.Key("wf-panel-toggle"), icons.PanelRight, panelShown, tip).Clicked() {
				a.wfPanelOpen = !panelShown
			}
		}
		a.wfHeaderActions(c)
	}
	pageHeaderBar(c, head)
	rail := func() {
		ui.Column(c).Padding(px(t, sp2), px(t, sp3), px(t, sp4)).Gap(px(t, sp2)).Children(func() { a.wfRail(c) })
	}
	panel := func() {
		ui.Column(c).Grow(1).MinHeight(0).Children(func() {
			ui.Row(c).Padding(px(t, sp2), px(t, sp3)).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
				ui.Segmented(c.Key("wf-panel-tabs"), &a.wfPanel, i18n.T("Step"), i18n.T("Run")).Label(i18n.T("Panel")).Grow(1)
				if !panelInline && iconButton(c.Key("wf-panel-close"), icons.Close, i18n.T("Close")).Clicked() {
					a.wfPanelOpen = false
				}
			})
			ui.Box(c).Height(1).Background(k.Line)
			ui.Scroll(c.Key("wf-panel-scroll")).Grow(1).MinHeight(0).Children(func() {
				ui.Column(c).Padding(px(t, sp3), px(t, sp4), px(t, sp4)).Gap(px(t, sp3)).Children(func() {
					if a.wfPanel == wfPanelRun {
						a.wfRunPanel(c)
					} else {
						a.wfInspector(c)
					}
				})
			})
		})
	}
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).BorderWidth(1, 0, 0, 0).BorderColor(k.Line).Children(func() {
		if railShown && railInline {
			ui.Scroll(c.Key("wf-rail")).Width(px(t, wfRailWidth)).Shrink(0).Background(k.Subtle).Children(rail)
			ui.Box(c).Width(1).Background(k.Line)
		}
		ui.Column(c.Key("wf-middle")).Grow(1).MinWidth(0).Children(func() {
			if a.wf == nil {
				a.wfEmpty(c)
				return
			}
			// While the step/run drawer is open, the toolbar's controls
			// move left of it, so zoom and fit stay in reach.
			right := float32(0)
			if panelShown && !panelInline {
				mid := a.pageWidth(c)
				if railShown && railInline {
					mid -= px(t, wfRailWidth) + 1
				}
				right = max(0, min(laneWidth(t, px(t, wfPanelWidth)), mid-px(t, 260)))
			}
			a.wfToolbar(c, right)
			ui.Box(c).Height(1).Background(k.Line)
			a.wfCanvasView(c)
		})
		// Too narrow for the rail: a floating list from the left.
		a.floating(c, floatPanel{key: "wf-rail-drawer", label: i18n.T("Workflows and steps"), w: px(t, wfRailWidth+20), left: true,
			close: func() { a.wfRailOpen = false }}, railShown && !railInline, rail)
		// The step and the run: in its lane, or over the canvas.
		var build func()
		var esc func()
		if a.wf != nil {
			build = panel
		}
		if !panelInline {
			esc = func() { a.wfPanelOpen = false }
		}
		a.floating(c, floatPanel{key: "wf-panel", label: i18n.T("Step and run"), w: px(t, wfPanelWidth), lane: panelInline,
			fill: true, close: esc}, panelShown, build)
	})
	a.wfConfirmDialog(c)
}

// wfHeaderActions are Run / Cancel.
func (a *app) wfHeaderActions(c *ui.Context) {
	if a.wf == nil {
		return
	}
	if a.wfBusy() {
		if ui.Button(c.Key("wf-cancel"), i18n.T("Cancel run")).Clicked() {
			a.wfCancel()
		}
		return
	}
	label := i18n.T("Run workflow")
	if a.wfResumable() {
		label = i18n.T("Resume")
	}
	if primaryButton(c.Key("wf-run"), label).Tooltip(i18n.T("Run every item through the steps")).Clicked() {
		a.wfStart(false)
	}
}

// wfResumable reports whether the last run stopped before its end.
func (a *app) wfResumable() bool {
	r := a.wfRun
	if r == nil || a.wfStale() {
		return false
	}
	if r.State == workflow.RunCancelled || r.State == workflow.RunRunning {
		return true
	}
	if r.State == workflow.RunFailed {
		done, _, total := r.Counts()
		return done < total
	}
	return false
}

// wfEmpty is the page without a workflow: the templates.
func (a *app) wfEmpty(c *ui.Context) {
	t := c.Theme()
	ui.Column(c.Key("wf-empty-page")).Grow(1).Center().Gap(px(t, sp3)).Padding(px(t, sp6)).Children(func() {
		ui.Icon(c, workflowsIcon.Linear).Size(px(t, 40), px(t, 40)).TextColor(tok(t).Text3)
		ui.Text(c, i18n.T("Design a voice workflow")).FontSize(px(t, fsSection)).FontWeight(600)
		ui.Text(c, i18n.T("Chain steps — speak, transcribe, translate, convert, normalize, export — and run up to 50 scripts or clips through them. Steps save as you go; a run you stop resumes where it stopped.")).
			TextColor(tok(t).Text2).MaxWidth(px(t, 520)).TextAlign(ui.Center)
		if !a.wfLoaded {
			return
		}
		ui.Row(c).Gap(px(t, sp2)).Wrap().Justify(ui.Center).MaxWidth(px(t, 640)).Children(func() {
			if primaryButton(c.Key("wf-empty-new"), i18n.T("New workflow")).Clicked() {
				a.wfNew("")
			}
			for _, tp := range workflow.Templates {
				if ui.Button(c.Key("wf-empty-"+tp.ID), workflow.TemplateName(tp.ID)).Tooltip(workflow.TemplateAbout(tp.ID)).Clicked() {
					a.wfNew(tp.ID)
				}
			}
		})
	})
}

// wfRail is the library, the templates and the step palette.
func (a *app) wfRail(c *ui.Context) {
	t := c.Theme()
	kt := tok(t)
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		ui.Text(c, i18n.T("Workflows")).FontSize(px(t, fsCaption)).FontWeight(600).TextColor(kt.Text2).Grow(1)
		iconButton(c.Key("wf-add"), icons.AddCircle, i18n.T("New workflow")).Menu(func(m *ui.Menu) {
			if m.Item(i18n.T("New workflow")).Chosen() {
				a.wfNew("")
			}
			m.Separator()
			for _, tp := range workflow.Templates {
				if m.Item(workflow.TemplateName(tp.ID)).Chosen() {
					a.wfNew(tp.ID)
				}
			}
		})
	})
	if a.wfLoaded && len(a.wfList) == 0 {
		caption(c, i18n.T("No workflows yet."))
	}
	ui.Column(c.Key("wf-list")).Gap(2).Children(func() {
		for _, r := range a.wfList {
			a.wfListRow(c, r)
		}
	})
	ui.Box(c).Height(1).Background(kt.Line).Margin(px(t, sp1), 0)
	ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
		ui.Text(c, i18n.X("workflow", "Steps")).FontSize(px(t, fsCaption)).FontWeight(600).TextColor(kt.Text2).Grow(1)
		infoTip(c.Key("wf-steps-info"), i18n.T("Click to add after the selected step, or drag onto the canvas."))
	})
	locked := a.wfLocked()
	ui.Column(c.Key("wf-palette")).Gap(4).Children(func() {
		for _, k := range workflow.Kinds {
			kk := k
			b := ui.ButtonBase(c.Key("wf-pal-" + string(k))).Label(i18n.T("Add %s", k.Label())).Tooltip(k.About()).Disabled(locked)
			b.Padding(px(t, 5), px(t, 8)).Radius(px(t, radiusControl)).Gap(px(t, sp2)).AlignItems(ui.Center)
			if b.Hovered() && !locked {
				b.Background(kt.Hover)
			}
			if !locked {
				b.Drag(kk)
			}
			b.Children(func() {
				ui.Icon(c, wfKindIcons[kk].Linear).Size(px(t, 16), px(t, 16)).TextColor(kt.Text2).Shrink(0)
				ui.Text(c, kk.Label()).FontSize(px(t, 13)).SingleLine().Grow(1).MinWidth(0)
			})
			if b.Clicked() {
				a.wfAddStep(kk, -1, -1)
			}
		}
	})
}

// wfListRow is a workflow in the library: open, rename, duplicate, delete.
func (a *app) wfListRow(c *ui.Context, r store.WorkflowRecord) {
	t := c.Theme()
	id := r.ID
	open := a.wf != nil && a.wf.ID == id
	if a.wfRenaming == id {
		in := ui.TextInput(c.Key("wf-rename-"+id), &a.wfRenameTx).Label(i18n.T("Name")).AutoFocus()
		if in.Submitted() {
			a.wfRename(id, a.wfRenameTx)
		}
		if in.Shortcut(0, ui.KeyEscape) {
			a.wfRenaming = ""
		}
		return
	}
	k := tok(t)
	row := ui.ButtonBase(c.Key("wf-row-"+id)).Label(r.Name).Padding(px(t, 4), px(t, 4), px(t, 4), px(t, 8)).Radius(px(t, radiusControl)).Gap(px(t, 6)).AlignItems(ui.Center)
	hovered := row.Hovered()
	switch {
	case open:
		row.Background(k.Pressed)
	case hovered:
		row.Background(k.Hover)
	}
	row.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			name := ui.Text(c, firstOf(r.Name, i18n.T("Untitled workflow"))).SingleLine().Ellipsis("…").MinWidth(0)
			if open {
				name.FontWeight(600)
			}
			if s := wfRecordState(r); s != "" {
				ui.Text(c, s).FontSize(px(t, fsMicro)).TextColor(k.Text2).SingleLine()
			}
		})
		moreMenu(c.Key("wf-row-menu-"+id), "", func(m *ui.Menu) {
			if m.Item(i18n.T("Rename")).Chosen() {
				a.wfRenaming, a.wfRenameTx = id, r.Name
			}
			if m.Item(i18n.T("Duplicate")).Chosen() {
				if !open {
					a.wfOpen(id)
				}
				a.wfDuplicateID(id)
			}
			m.Separator()
			if m.Item(i18n.T("Delete")).Chosen() {
				a.wfConfirm, a.wfAsk = id, true
			}
		})
	})
	if row.Clicked() && !open {
		a.wfOpen(id)
	}
}

// wfDuplicateID duplicates a workflow of the list.
func (a *app) wfDuplicateID(id string) {
	if a.wf != nil && a.wf.ID == id {
		a.wfDuplicate()
		return
	}
	svc := a.wfSvc
	a.background(func() {
		w, _, err := svc.Get(context.Background(), id)
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not open the workflow: %v", err))
				return
			}
			a.wf = w
			a.wfDuplicate()
		})
	})
}

// wfRecordState is a list row's second line.
func wfRecordState(r store.WorkflowRecord) string {
	switch workflow.RunState(r.State) {
	case workflow.RunRunning:
		return i18n.T("Running · %d/%d", r.Done, r.Items)
	case workflow.RunDone:
		return i18n.N(r.Done, "%d item ready", "%d items ready", r.Done)
	case workflow.RunFailed:
		return i18n.T("Stopped · %d/%d ready", r.Done, r.Items)
	case workflow.RunCancelled:
		return i18n.T("Cancelled · %d/%d ready", r.Done, r.Items)
	}
	return ""
}

// wfToolbar is above the canvas: zoom, fit, remove; right is room kept
// free at its right end (for the drawer over it).
func (a *app) wfToolbar(c *ui.Context, right float32) {
	t := c.Theme()
	rep := a.wfReport()
	ui.Row(c.Key("wf-toolbar")).Padding(px(t, sp1), px(t, sp3)+right, px(t, sp1), px(t, sp3)).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
		if n := rep.Errors(); n > 0 {
			label := i18n.N(n, "%d problem", "%d problems", n)
			b := ghostBase(c.Key("wf-errors")).Padding(px(t, 4), px(t, 8)).Gap(px(t, sp1)).AlignItems(ui.Center).Label(label).Shrink(1).MinWidth(0)
			b.Children(func() {
				ui.Icon(c, icons.DangerCircle.Linear).Size(px(t, 14), px(t, 14)).TextColor(t.Danger)
				ui.Text(c, label).FontSize(px(t, fsCaption)).TextColor(t.Danger).SingleLine().Ellipsis("…").MinWidth(0)
			})
			if b.Clicked() {
				a.wfPanel = wfPanelRun
			}
		} else if a.wf != nil && len(a.wf.Nodes) > 0 {
			caption(c, i18n.N(rep.Items, "Ready · %d item", "Ready · %d items", rep.Items)).SingleLine().Ellipsis("…").MinWidth(0).Margin(0, 0, 0, px(t, sp1))
		}
		if a.wfLocked() && a.wfBusy() {
			iconMark(c, icons.LockKeyhole, tok(t).Text2, i18n.T("Locked while it runs"))
		}
		ui.Box(c).Grow(1)
		del := a.wfSel != "" || a.wfSelEdge != ""
		if del && iconButton(c.Key("wf-remove"), icons.TrashBinMinimalistic, i18n.T("Remove")).Disabled(a.wfLocked()).
			Tooltip(i18n.T("Remove the selected step or link (Delete)")).Clicked() {
			a.wfRemoveSelected()
		}
		if iconButton(c.Key("wf-zoom-out"), icons.MagnifierZoomOut, i18n.T("Zoom out")).Tooltip(i18n.T("Zoom out (⌘−)")).Clicked() {
			a.wfZoom(1 / 1.2)
		}
		zoom := itoa(int(a.wf.View.Zoom*100+0.5)) + "%"
		zb := ghostBase(c.Key("wf-zoom-reset")).Padding(px(t, 4), px(t, 6)).Label(i18n.T("Actual size")).Tooltip(i18n.T("Actual size (⌘0)"))
		zb.Children(func() { ui.Text(c, zoom).FontSize(px(t, fsCaption)).TextColor(tok(t).Text2).SingleLine() })
		if zb.Clicked() {
			a.wfResetZoom()
		}
		if iconButton(c.Key("wf-zoom-in"), icons.MagnifierZoomIn, i18n.T("Zoom in")).Tooltip(i18n.T("Zoom in (⌘=)")).Clicked() {
			a.wfZoom(1.2)
		}
		if iconButton(c.Key("wf-fit"), icons.FullScreenSquare, i18n.T("Fit")).Tooltip(i18n.T("Show every step")).Clicked() {
			a.wfFit()
		}
	})
}

// wfConfirmDialog asks before deleting a workflow.
func (a *app) wfConfirmDialog(c *ui.Context) {
	if !a.wfAsk {
		return
	}
	id := a.wfConfirm
	name := ""
	if i := slices.IndexFunc(a.wfList, func(r store.WorkflowRecord) bool { return r.ID == id }); i >= 0 {
		name = a.wfList[i].Name
	}
	switch ui.AlertDialog(c.Key("wf-confirm"), &a.wfAsk, i18n.T("Delete “%s”?", name),
		i18n.T("The workflow, its last run and the results kept in the app are deleted. Files it exported stay."), i18n.T("Delete"), i18n.T("Cancel")) {
	case 0:
		a.wfAsk = false
		a.wfDelete(id)
	case 1:
		a.wfAsk = false
	}
}
