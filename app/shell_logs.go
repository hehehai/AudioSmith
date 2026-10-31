package main

// Logs: the newest lines of the app, the downloads and (once the
// supervisor feeds them) the engine workers, as a Settings section
// (/settings/logs) and as a panel under any page, filtered by source and text. Lines are copied
// and selected as shell_logs_select.go says.

import (
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/actnowkit/listsel"
)

// logLines is how many lines the logs view shows at most.
const logLines = 500

// logsSettings is Settings → Logs (the Logs page before 0.5 F1): the
// panel's switch, then the log, as tall as the section (which does not
// scroll: settings_dialog.go).
func (a *app) logsSettings(c *ui.Context) {
	t := c.Theme()
	ui.Row(c.Key("logs-head")).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
		caption(c, i18n.T("The newest lines of the app, the engine and the downloads. The panel shows them under any page.")).Wrap().Grow(1).MinWidth(0)
		label := i18n.T("Show the logs panel")
		if a.logsPanel {
			label = i18n.T("Hide the logs panel")
		}
		if ui.Button(c.Key("logs-panel-toggle"), label).Tooltip(tip(i18n.T("Show or hide the logs panel"), "⇧⌘L")).Shrink(0).Clicked() {
			a.logsPanel = !a.logsPanel
		}
	})
	ui.Column(c.Key("logs-box")).Grow(1).MinHeight(px(t, 160)).Radius(px(t, radiusCard)).Border(1, tok(t).Line).Clip().Children(func() {
		a.logsView(c, false)
	})
}

// logSources are the filters of the logs view: "" every source.
var logSources = []string{"", "app", "engine:", "hub"}

func logSourceLabel(s string) string {
	switch s {
	case "":
		return i18n.T("All sources")
	case "app":
		return i18n.T("App")
	case "engine:":
		return i18n.T("Engine")
	case "hub":
		return i18n.T("Downloads")
	}
	return s
}

// visibleLogs are the lines the filters select, the newest first: the
// order the view shows (copies go back to the log's order, copyLogLines).
func (a *app) visibleLogs() []core.LogEntry {
	lines := a.logs.Tail(logLines*4, a.logsSource)
	q := strings.ToLower(strings.TrimSpace(a.logsQuery))
	var out []core.LogEntry
	for _, e := range slices.Backward(lines) {
		if q == "" || strings.Contains(strings.ToLower(e.Message), q) || strings.Contains(strings.ToLower(e.Source), q) {
			out = append(out, e)
			if len(out) == logLines {
				break
			}
		}
	}
	return out
}

// logsView shows the log; panel is the compact form under a page.
func (a *app) logsView(c *ui.Context, panel bool) {
	t := c.Theme()
	k := tok(t)
	lines := a.visibleLogs()
	picked := &a.logSel.m
	picked.Prune(logSeqs(lines)) // lines filtered out or gone from the log
	ui.Row(c).Padding(px(t, 6), px(t, sp3), px(t, 6), px(t, sp6)).Gap(px(t, 6)).AlignItems(ui.Center).Shrink(0).Children(func() {
		if panel {
			ui.Text(c, i18n.T("Logs")).FontWeight(600).FontSize(px(t, fsCaption)).SingleLine().Shrink(0)
		}
		var labels []string
		for _, s := range logSources {
			labels = append(labels, logSourceLabel(s))
		}
		chosen := logSourceLabel(a.logsSource)
		sel := ui.Select(c.Key("logs-source"), &chosen, labels).Label(i18n.T("Source")).MinWidth(px(t, 110)).Shrink(1)
		if sel.Changed() {
			for _, s := range logSources {
				if logSourceLabel(s) == chosen {
					a.logsSource = s
				}
			}
		}
		ui.TextInput(c.Key("logs-search"), &a.logsQuery).Placeholder(i18n.T("Filter")).Width(px(t, 200)).Shrink(1).MinWidth(px(t, 80)).Label(i18n.T("Filter the logs"))
		ui.Spacer(c)
		if n := picked.Count(); n > 0 {
			// While lines are selected: how many, Copy and Deselect.
			ui.Text(c, i18n.N(n, "%d line selected", "%d lines selected", n)).FontSize(px(t, fsMicro)).FontWeight(600).TextColor(k.AccentInk).SingleLine().Shrink(0)
			if quietButton(c.Key("logs-copy-selected"), icons.Copy, i18n.T("Copy")).Tooltip(i18n.N(n, "Copy %d selected line", "Copy %d selected lines", n)).Clicked() {
				a.copyLogLines(c.Services(), listsel.Items(picked, lines, entrySeq))
			}
			if quietButton(c.Key("logs-deselect"), icons.Close, i18n.T("Deselect")).Clicked() {
				picked.Clear()
			}
		} else {
			ui.Text(c, i18n.N(len(lines), "%d line", "%d lines", len(lines))).FontSize(px(t, fsMicro)).TextColor(k.Text3).SingleLine()
			if iconButton(c.Key("logs-copy"), icons.Copy, i18n.T("Copy all lines shown")).Disabled(len(lines) == 0).Clicked() {
				a.copyLogLines(c.Services(), lines)
			}
		}
		if path := a.logs.Path(); path != "" {
			if iconButton(c.Key("logs-open"), icons.FolderOpen, i18n.T("Open log file")).Clicked() {
				mygo.Shell.OpenPath(path)
			}
		}
		if panel {
			if iconButton(c.Key("logs-close"), icons.Close, i18n.T("Close")).Clicked() {
				a.logsPanel = false
			}
		}
	})
	ui.Divider(c)
	if len(lines) == 0 {
		ui.Column(c).Grow(1).Center().Children(func() {
			ui.Text(c, i18n.T("No log lines yet.")).TextColor(t.TextMuted)
		})
		return
	}
	a.logRows(c, lines, viewOf(panel))
}

// logRows are the lines, the newest first, in a list that keeps its place
// as lines come (shell_logs_order.go) and that the pointer selects
// (shell_logs_select.go): the line under it lit, the lines selected in the
// accent's tint.
func (a *app) logRows(c *ui.Context, lines []core.LogEntry, v logView) {
	t := c.Theme()
	k := tok(t)
	s := &a.logSel
	o := &s.views[v]
	a.logsFollow(o, lines) // before the list: where it goes
	// A drag at the list's edge scrolls it (before the list, which takes
	// the offset as it is built).
	edge := s.m.Dragging() && logsAutoScroll(t, s.drag.y, s.drag.h, &o.scroll)
	pressing := s.m.Pressed()
	s.spans = s.spans[:0]
	ui.Column(c.Key("logs-area")).Grow(1).MinHeight(0).Children(func() {
		list := ui.List(c.Key("logs-rows"), &o.list, len(lines), func(i int) {
			e := lines[i]
			color := t.Text
			switch e.Level {
			case core.SeverityError:
				color = t.Danger
			case core.SeverityWarning:
				color = t.Warning
			case core.SeverityDebug:
				color = k.Text3
			}
			row := ui.Row(c).Gap(px(t, 8)).Padding(px(t, 1), px(t, 4)).Radius(px(t, 4))
			// The hover waits while the pointer presses: a drag shows
			// what it selects.
			hovered := !pressing && row.Hovered()
			switch selected := s.m.IsSelected(e.Seq); {
			case selected && hovered:
				row.Background(k.SelectedHover)
			case selected:
				row.Background(k.Selected)
			case hovered:
				row.Background(k.Hover)
			}
			if b := row.Bounds(); b.H > 0 { // in the window, until the list's box is known
				s.spans = append(s.spans, logSpan{seq: e.Seq, top: b.Y, bottom: b.Y + b.H})
			}
			a.logRowMenu(c, row, e, lines)
			row.Children(func() {
				ui.Text(c, e.Time.Format("15:04:05")).Font("monospace").FontSize(px(t, fsMicro)).TextColor(k.Text3).Shrink(0)
				ui.Text(c, e.Source).Font("monospace").FontSize(px(t, fsMicro)).TextColor(k.Text2).Width(px(t, 80)).Shrink(0).SingleLine().Ellipsis("…")
				// Not Selectable: the pointer chooses lines, not characters.
				ui.Text(c, e.Message).Font("monospace").FontSize(px(t, 11)).TextColor(color).Grow(1).MinWidth(0)
			})
		})
		list.TrackScroll(&o.scroll).Grow(1).MinHeight(0).Padding(px(t, 5), px(t, sp3-4), px(t, 5), px(t, sp6-4)).Focusable().Label(i18n.T("Log lines"))
		// The rows' spans against the list's box, as on screen (the last
		// frame's, as the rows'): the pointer's events come in its box.
		origin := list.Bounds()
		s.origin = origin
		for i := range s.spans {
			s.spans[i].top -= origin.Y
			s.spans[i].bottom -= origin.Y
		}
		list.HandleInput(a.logsInput(c.Services(), lines))
		a.logsKeys(c, list, lines)
		a.newLinesPill(c, o, lines) // over the list's top
		changed := a.logsResolve(c, list, lines)
		if changed || edge {
			c.AnimationFrame() // show the selection the events made
		}
	})
}

// mygoClipboard copies text through the window's services.
func mygoClipboard(c *ui.Context, text string) {
	c.Services().WriteClipboard(text)
}
