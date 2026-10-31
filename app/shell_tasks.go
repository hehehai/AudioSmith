package main

// The Tasks page (/tasks): the activity cards in full (the ActNow family's
// window spec §2.4). What goes on now — a stopped engine, dictation, calls,
// the work of each project, downloads — then every job that runs or
// waits, with its stage, how far it is and Cancel. The sidebar does not
// list it: the cards' "View all", the rail's count and the palette open it.

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
)

func (a *app) tasksPage(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	active := a.activeJobs()
	note := ""
	if n := len(active); n > 0 {
		note = i18n.N(n, "%d job", "%d jobs", n)
	}
	pageHeaderBar(c, pageHead{title: i18n.T("Tasks"), count: note})
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Padding(px(t, sp1), px(t, sp6), px(t, sp6)).Gap(px(t, sp5)).MaxWidth(px(t, 760)).Children(func() {
			caption(c, i18n.T("What AudioSmith is doing: renders, dubs, takes, exports and downloads go on in the background, a few at a time.")).Wrap()
			if len(a.actCards) == 0 && len(active) == 0 {
				emptyState(c, tasksIcon, i18n.T("Nothing is running."), i18n.T("Renders, dubs, exports and downloads show here while they run."), nil)
				return
			}
			if len(a.actCards) > 0 {
				settingGroup(c, i18n.N(len(a.actCards), "Going on · %d", "Going on · %d", len(a.actCards)), func() {
					for _, cd := range a.actCards {
						a.taskCardRow(c, cd)
					}
				})
			}
			if len(active) == 0 {
				return
			}
			settingGroup(c, i18n.N(len(active), "Running and waiting · %d", "Running and waiting · %d", len(active)), func() {
				for _, j := range active {
					settingBlock(c, "job-"+j.ID, func() {
						ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
							ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 4)).Children(func() {
								title := j.Title
								if title == "" {
									title = j.Kind
								}
								if p := a.dub; p != nil && j.Owner == services.DubOwner+p.ID && p.Source.URL != "" && p.Title != p.Source.URL {
									title = strings.ReplaceAll(title, p.Source.URL, p.Title) // named before the video's title was known
								}
								truncText(c, title).FontSize(px(t, 13))
								state := i18n.T("Waiting")
								if j.State == jobs.StateRunning {
									state = stageText(j.Progress.Stage)
									if j.Progress.Fraction >= 0 {
										state += fmt.Sprintf(" · %d%%", int(100*j.Progress.Fraction))
									}
									if j.Kind == services.KindDubFetch {
										state = dubFetchStatus(j.Progress) // with the size and time left
									}
									if f := determinate(j); f > 0 {
										ui.Progress(c, f).Label(title)
									}
								}
								ui.Text(c, state).FontSize(px(t, 11)).TextColor(k.Text3)
							})
							if iconButton(c.Key("cancel"), icons.CloseCircle, i18n.T("Cancel")).Clicked() {
								a.cancelJobByID(j)
							}
						})
					})
				}
			})
		})
	})
}

// taskCardRow is an activity card as a row of the Tasks page: a click on
// it goes where it is managed, its button stops it.
func (a *app) taskCardRow(c *ui.Context, cd actCard) {
	t := c.Theme()
	k := tok(t)
	settingBlock(c, "card-"+cd.key, func() {
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			b := ui.ButtonBase(c.Key("open")).Grow(1).MinWidth(0).Gap(px(t, sp2)).AlignItems(ui.Center).Padding(px(t, 2)).Radius(px(t, radiusControl)).Label(cd.title)
			if b.Hovered() {
				b.Background(k.Hover)
			}
			b.Children(func() {
				col := k.Text2
				switch {
				case cd.danger:
					col = t.Danger
				case cd.done:
					col = t.Success
				}
				ui.Icon(c, cd.icon.Linear).Size(px(t, 16), px(t, 16)).TextColor(col).Shrink(0)
				ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 4)).Children(func() {
					ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
						truncText(c, cd.title).FontWeight(600).Grow(1)
						if cd.detail != "" {
							ui.Text(c, cd.detail).FontSize(px(t, fsCaption)).TextColor(k.Text3).SingleLine().Shrink(0)
						}
					})
					if cd.frac > 0 && cd.frac <= 1 { // as on the card: only real progress
						ui.Progress(c, cd.frac).Label(cd.title)
					}
				})
			})
			if b.Clicked() && cd.open != nil {
				cd.open()
			}
			if cd.action != nil {
				if iconButton(c.Key("action"), cd.action.icon, cd.action.tip).Clicked() {
					cd.action.run()
				}
			}
		})
	})
}
