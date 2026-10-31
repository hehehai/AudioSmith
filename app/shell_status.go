package main

// What goes on, which the window shows without a status bar (the ActNow
// family's window layout): the unfinished jobs of every owner (the
// scheduler's jobs.Updated, and the list at start), each cancellable by
// id, for the activity cards (shell_activity.go) and the Tasks page
// (shell_tasks.go); the downloads; the ⌘E quick switch, a dialog with the
// engine's state and models; and the notification centre, whose bell is
// at the sidebar's foot.

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/oplog"
	"github.com/hehehai/AudioSmith/internal/services"
)

// downloads are the models downloading, catalogue first, in its order.
func (a *app) downloads() []*modelState {
	var out []*modelState
	for _, e := range catalog.Builtin().Entries {
		if m := a.models[e.ID]; m != nil && m.downloading {
			out = append(out, m)
		}
	}
	keys := slices.Sorted(maps.Keys(a.models))
	for _, k := range keys {
		if m := a.models[k]; strings.HasPrefix(k, "repo:") && m.downloading {
			out = append(out, m)
		}
	}
	return out
}

// jobsChanged keeps the snapshot of unfinished jobs (every owner) the
// status bar lists; it runs on the main thread.
func (a *app) jobsChanged(j jobs.Job) {
	if j.State.Terminal() || j.State == jobs.StateInterrupted {
		if _, ok := a.jobList[j.ID]; ok {
			a.jobEnded(j) // shell_activity.go: "done/total", the finished card
		}
		delete(a.jobList, j.ID)
		return
	}
	a.jobList[j.ID] = j
}

// activeJobs are the unfinished jobs: running first, then by age.
func (a *app) activeJobs() []jobs.Job {
	list := slices.Collect(maps.Values(a.jobList))
	slices.SortFunc(list, func(x, y jobs.Job) int {
		if rx, ry := x.State == jobs.StateRunning, y.State == jobs.StateRunning; rx != ry {
			if rx {
				return -1
			}
			return 1
		}
		if c := x.Created.Compare(y.Created); c != 0 {
			return c
		}
		return strings.Compare(x.ID, y.ID)
	})
	return list
}

// cancelJobByID cancels any job by its id, as the Studio's Cancel does for
// its own; a dub's job stops the dub, as its page's Cancel and its card's
// Stop do.
func (a *app) cancelJobByID(j jobs.Job) {
	if a.job != nil && a.job.id == j.ID {
		a.cancel()
		return
	}
	if strings.HasPrefix(j.Owner, services.DubOwner) && a.dubSvc != nil {
		a.stopActivity(j.Owner, []string{j.ID})
		return
	}
	for _, m := range a.models {
		if m.jobID == j.ID && m.downloading {
			a.cancelDownload(m.entry)
			return
		}
	}
	a.cancelJob(j.ID)
}

// quickSwitchDialog is ⌘E: a small dialog with the engine's state, the
// models in memory and the speech model to use, and what to do with them
// (the model shown nowhere else since the status bar went).
func (a *app) quickSwitchDialog(c *ui.Context) {
	if !a.switchOpen {
		return
	}
	t := c.Theme()
	eng := a.engineSummary()
	k := tok(t)
	state := map[engineState]string{engineStarting: i18n.T("Starting"), engineReady: i18n.T("Ready"), engineFailed: i18n.T("Stopped")}[eng.State]
	info := state
	if eng.Version != "" {
		info += " · " + i18n.T("version %s", eng.Version)
	}
	if eng.PID != 0 {
		info += " · PID " + itoa(eng.PID)
	}
	dialogFrame(c, "quick-switch", &a.switchOpen, i18n.T("Speech engine"), info, px(t, 400), func() {
		if eng.Err != "" {
			ui.Text(c, eng.Err).FontSize(px(t, 12)).TextColor(t.Danger).MaxLines(3).Ellipsis("…")
		}
		if mem := a.gpuMemory(); mem != "" {
			caption(c, mem)
		}
		sectionTitle(c, i18n.T("In memory"))
		if len(eng.Loaded) == 0 {
			ui.Text(c, i18n.T("No model loaded")).FontSize(px(t, 12))
		}
		for _, repo := range eng.Loaded {
			ui.Row(c.Key("loaded-" + repo)).Gap(px(t, 8)).AlignItems(ui.Center).Children(func() {
				ui.Text(c, catalog.Builtin().DisplayName(repo)).Grow(1).SingleLine().Ellipsis("…")
				if ghostButton(c.Key("unload"), i18n.T("Unload")).Disabled(eng.Busy).Clicked() {
					a.unload()
				}
			})
		}
		sectionTitle(c, i18n.T("Speech model"))
		_, winH := c.Size()
		ui.Scroll(c.Key("switch-models")).MaxHeight(max(winH*0.4, px(t, 120))).Children(func() {
			for _, e := range catalog.Builtin().ForTask(catalog.TaskTTS) {
				m := a.models[modelKey(e)]
				chosen := a.preset().Repo == e.Repo
				row := ui.ButtonBase(c.Key("switch-"+e.ID)).Padding(px(t, 5), px(t, sp2)).Radius(px(t, radiusControl)).Label(e.DisplayName()).Tooltip(modelTip(e)).Checked(chosen)
				if chosen {
					row.Background(k.Pressed)
				} else if row.Hovered() {
					row.Background(k.Hover)
				}
				row.Children(func() {
					ui.Row(c).Grow(1).Gap(px(t, 8)).AlignItems(ui.Center).Children(func() {
						ui.Text(c, e.DisplayName()).Grow(1).SingleLine().Ellipsis("…").MinWidth(0)
						if m != nil && m.known && m.local.State == hub.Downloaded {
							ui.Text(c, i18n.T("Downloaded")).FontSize(px(t, fsMicro)).TextColor(k.Text3)
						}
						if chosen {
							ui.Icon(c, icons.Check.Linear).Size(px(t, 14), px(t, 14)).TextColor(k.AccentInk)
						}
					})
				})
				if row.Clicked() {
					a.selectModel(e)
				}
			}
		})
	}, func() {
		if ui.Button(c.Key("switch-preload"), i18n.T("Preload")).Disabled(eng.Busy || eng.State != engineReady).Clicked() {
			a.preload()
		}
		if iconButton(c.Key("switch-restart"), icons.Restart, i18n.T("Restart engine")).Clicked() {
			a.restartEngine()
		}
		ui.Spacer(c)
		moreMenu(c.Key("switch-more"), "", func(m *ui.Menu) {
			if m.Item(i18n.T("Unload")).Disabled(a.loadedModel == "" || a.job != nil).Chosen() {
				a.unload()
			}
			if m.Item(i18n.T("Clear cache")).Disabled(eng.State != engineReady).Chosen() {
				a.flushEngine()
			}
			if m.Item(i18n.T("Details")).Chosen() {
				a.switchOpen = false
				a.navigate("/engine")
			}
		})
	})
}

// gpuMemory describes the gpu worker's memory, from its last stats.
func (a *app) gpuMemory() string {
	for _, w := range a.workers {
		if w.Role == proto.RoleGPU && w.Stats != nil && w.State == engine.WorkerReady {
			return i18n.T("Memory: %s in use · %s cached", catalog.HumanSize(w.Stats.Memory.Active), catalog.HumanSize(w.Stats.Memory.Cache))
		}
	}
	return ""
}

// noticePosted adds a notification to the centre's snapshot and shows it
// as a toast.
func (a *app) noticePosted(n core.Notification) {
	for i := range a.notices {
		if a.notices[i].ID == n.ID {
			a.notices = append(a.notices[:i], a.notices[i+1:]...)
			break
		}
	}
	a.notices = append([]core.Notification{n}, a.notices...)
	msg := n.Title
	if n.Body != "" {
		msg += ": " + n.Body
	}
	a.toast(msg)
}

// notify posts a notification (on any goroutine).
func (a *app) notify(sev core.Severity, title, body, link string) {
	a.notes.Post(core.Notification{Severity: sev, Title: title, Body: body, Link: link})
}

// noticeCentreIn lists the notifications, newest first, under their
// heading (Mark all read, Clear): the bell's popover (shell_topbar.go),
// at most maxH tall.
func (a *app) noticeCentreIn(c *ui.Context, maxH float32) {
	t := c.Theme()
	ui.Column(c).Gap(px(t, sp2)).Padding(0, px(t, 4)).Children(func() {
		ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
			sectionHeading(c, i18n.T("Notifications")).Grow(1)
			if ghostButton(c.Key("notices-read"), i18n.T("Mark all read")).Disabled(len(a.notices) == 0).Clicked() {
				for i := range a.notices {
					a.notices[i].Read = true
				}
				notes := a.notes
				a.async(func() { oplog.Err("notifications.read", notes.MarkRead(context.Background(), "")) })
			}
			if ghostButton(c.Key("notices-clear"), i18n.T("Clear")).Disabled(len(a.notices) == 0).Clicked() {
				a.notices = nil
				notes := a.notes
				a.async(func() { oplog.Err("notifications.clear", notes.Clear(context.Background())) })
			}
		})
		if len(a.notices) == 0 {
			ui.Text(c, i18n.T("Nothing new.")).TextColor(t.TextMuted).Padding(px(t, 8), 0)
			return
		}
		ui.Scroll(c).MaxHeight(maxH).Children(func() {
			ui.Column(c).Gap(px(t, 6)).Children(func() {
				for _, n := range a.notices {
					a.noticeRow(c, n)
				}
			})
		})
	})
}

func (a *app) noticeRow(c *ui.Context, n core.Notification) {
	t := c.Theme()
	color := t.TextMuted
	switch n.Severity {
	case core.SeverityError:
		color = t.Danger
	case core.SeverityWarning:
		color = t.Warning
	case core.SeveritySuccess:
		color = t.Success
	}
	k := tok(t)
	row := ui.Row(c.Key("notice-" + n.ID)).Gap(px(t, sp2)).Padding(px(t, sp2)).Radius(px(t, radiusControl)).AlignItems(ui.Start)
	if !n.Read {
		row.Background(k.Subtle)
	}
	row.Children(func() {
		ui.Box(c).Size(px(t, 6), px(t, 6)).Radius(px(t, 3)).Background(color).Margin(px(t, 6), 0, 0, 0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 2)).Children(func() {
			ui.Text(c, n.Title).FontSize(px(t, fsBody)).FontWeight(600).MaxLines(2).Ellipsis("…")
			if n.Body != "" {
				ui.Text(c, n.Body).FontSize(px(t, fsCaption)).TextColor(k.Text2).MaxLines(4).Ellipsis("…").Selectable()
			}
			ui.Text(c, timeAgo(n.Time)).FontSize(px(t, fsMicro)).TextColor(k.Text3)
		})
		if n.Link != "" {
			if ghostButton(c.Key("open"), i18n.T("Open")).Clicked() {
				a.noticesOpen = false
				a.openURL(n.Link)
			}
		}
	})
}

// timeAgo says how long ago t was, briefly.
func timeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return i18n.T("just now")
	case d < time.Hour:
		return i18n.N(int(d.Minutes()), "%d minute ago", "%d minutes ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return i18n.N(int(d.Hours()), "%d hour ago", "%d hours ago", int(d.Hours()))
	}
	return i18n.DateTime(t)
}
