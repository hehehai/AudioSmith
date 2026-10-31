package main

import (
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/store"
	"github.com/hehehai/AudioSmith/internal/telephony"
)

// callsHistoryWidth is the recent calls' column.
const callsHistoryWidth = 280

// callsPage is /calls: recent calls, the call shown, a new call. The
// recent calls are a drawer when the window is narrow; the new call is a
// third column when it is wide, else a tab beside the call.
func (a *app) callsPage(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	pw := a.pageWidth(c)
	histInline := pw-px(t, callsHistoryWidth) >= px(t, 480)
	wide := histInline && pw-px(t, callsHistoryWidth+380) >= px(t, 480)
	pageHeader(c, i18n.T("Calls"), func() {
		if !histInline {
			tip := i18n.T("Show recent calls")
			if a.callHistOn {
				tip = i18n.T("Hide recent calls")
			}
			if panelToggle(c.Key("calls-hist-toggle"), icons.PanelLeft, a.callHistOn, tip).Clicked() {
				a.callHistOn = !a.callHistOn
			}
		}
		if labelOrIcon(c.Key("calls-inbound"), icons.PhoneCallingRounded, i18n.T("Incoming calls…"), pw < px(t, 640)).Clicked() {
			a.openInbound()
		}
		if iconButton(c.Key("calls-setup"), icons.Settings, i18n.T("Set up Twilio")).Clicked() {
			a.navigate("/calls/setup")
		}
	})
	main := func() {
		if wide {
			ui.Scroll(c.Key("calls-call")).Grow(1).MinWidth(0).Children(func() {
				ui.Column(c).Padding(px(t, sp1), px(t, sp6), px(t, sp6)).Gap(px(t, sp3)).Children(func() {
					a.callsBanner(c)
					a.callView(c)
				})
			})
			ui.Box(c).Width(1).Background(k.Line)
			ui.Scroll(c.Key("calls-new")).Width(px(t, 380)).Shrink(0).Children(func() {
				ui.Column(c).Padding(px(t, sp1), px(t, sp5), px(t, sp6)).Gap(px(t, sp3)).Children(func() { a.newCallForm(c) })
			})
			return
		}
		ui.Scroll(c.Key("calls-main")).Grow(1).MinWidth(0).Children(func() {
			ui.Column(c).Padding(px(t, sp1), px(t, sp6), px(t, sp6)).Gap(px(t, sp3)).MaxWidth(px(t, 760)).Children(func() {
				a.callsBanner(c)
				ui.Row(c).Children(func() {
					ui.Segmented(c.Key("calls-tab"), &a.callTab, i18n.T("New call"), i18n.T("Call")).Label(i18n.T("Show"))
				})
				if a.callTab == 0 {
					a.newCallForm(c)
				} else {
					a.callView(c)
				}
			})
		})
	}
	ui.Row(c.Key("calls-body")).Bind(&a.callPageH).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		if histInline {
			ui.Column(c.Key("calls-history")).Width(px(t, callsHistoryWidth)).Shrink(0).Background(k.Subtle).Children(func() {
				a.callHistory(c)
			})
			ui.Box(c).Width(1).Background(k.Line)
		}
		main()
	})
	if !histInline && a.callHistOn {
		ui.Overlay(c, func() {
			w, h := c.Size()
			d := ui.Column(c.Key("calls-history-drawer")).Absolute().Left(w-pw).Top(pageTop).Width(px(t, callsHistoryWidth)).
				Height(h-pageTop-1).Background(k.Bg).BorderWidth(0, 1, 0, 0).BorderColor(k.Line).
				Shadow(8, 0, 24, 0, ui.RGBA(0, 0, 0, 0.12)).Label(i18n.T("Recent calls"))
			d.Children(func() { a.callHistory(c) })
			if d.PressedOutside() || d.OverlayShortcut(0, ui.KeyEscape) {
				a.callHistOn = false
			}
		})
	}
	a.callDialogs(c)
}

// callsBanner lists what is missing before calls can be placed.
func (a *app) callsBanner(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	checks := a.callChecks()
	ready, n := callsReady(checks)
	if ready {
		return
	}
	fillCard(c.Key("calls-banner")).Gap(px(t, sp2)).Padding(px(t, sp3), px(t, sp4)).Children(func() {
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.DangerTriangle.Linear).Size(px(t, 16), px(t, 16)).TextColor(t.Warning)
			ui.Text(c, i18n.T("Finish setup to place calls")).FontWeight(600).Grow(1).MinWidth(0)
			caption(c, i18n.T("%d of %d ready", n, len(checks))).SingleLine()
			if iconButton(c.Key("calls-recheck"), icons.Refresh, i18n.T("Check again")).Disabled(a.tw.checking).Clicked() {
				a.checkTwilioAccount()
			}
		})
		for _, ck := range checks {
			if ck.ok {
				continue
			}
			ui.Row(c.Key("ck-" + ck.id)).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
				statusDot(c, t.Warning)
				ui.Column(c).Grow(1).MinWidth(0).Children(func() {
					ui.Text(c, ck.title)
					ui.Text(c, ck.detail).FontSize(px(t, fsCaption)).TextColor(k.Text2).Wrap()
				})
				if ghostButton(c.Key("fix"), i18n.T("Fix")).Clicked() {
					a.navigate(ck.fix)
				}
			})
		}
	})
}

// callHistory is the recent calls.
func (a *app) callHistory(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	ui.Row(c).Height(px(t, 40)).Padding(0, px(t, sp2), 0, px(t, sp4)).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
		live, max := 0, 1
		if a.callSvc != nil {
			live, max = a.callSvc.Running()
		}
		ui.Text(c, i18n.T("Recent calls")).FontSize(px(t, fsCaption)).FontWeight(600).TextColor(k.Text2).Grow(1).SingleLine()
		ui.Text(c, i18n.T("%d of %d lines in use", live, max)).FontSize(px(t, fsMicro)).TextColor(k.Text3).SingleLine().Ellipsis("…").MinWidth(0)
		if iconButton(c.Key("calls-refresh"), icons.Refresh, i18n.T("Refresh")).Clicked() {
			a.loadCalls()
		}
	})
	if len(a.callList) == 0 {
		ui.Column(c).Padding(px(t, sp4)).Children(func() {
			caption(c, i18n.T("No calls yet. Your calls will show up here.")).Wrap()
		})
		return
	}
	ui.Scroll(c.Key("calls-list")).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Padding(px(t, sp1), px(t, sp2)).Gap(px(t, 2)).Children(func() {
			for _, r := range a.callList {
				a.callRow(c, r)
			}
		})
	})
}

// callRow is a call in the list.
func (a *app) callRow(c *ui.Context, r store.CallRecord) {
	t := c.Theme()
	sel := a.callSel == r.ID
	k := tok(t)
	b := ui.ButtonBase(c.Key("call-"+r.ID)).Padding(px(t, sp2), px(t, 10)).Radius(px(t, radiusControl)).Label(i18n.T("Open %s", r.Title))
	switch {
	case sel:
		b.Background(k.Selected) // as the row kit's open row (ui_list.go)
	case b.Hovered():
		b.Background(k.Hover)
	}
	b.Children(func() {
		ui.Column(c).Gap(px(t, 2)).Grow(1).MinWidth(0).Children(func() {
			ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
				ic, tip := icons.ArrowRightUp, i18n.T("Outgoing")
				if r.Direction == services.CallInbound {
					ic, tip = icons.ArrowLeftDown, i18n.T("Incoming")
				}
				iconMark(c, ic, k.Text2, tip)
				ui.Text(c, telephony.MaskNumber(r.Number)).FontWeight(600).Grow(1).SingleLine().Ellipsis("…").MinWidth(0)
				a.callBadge(c, r.Status, r.Outcome)
			})
			title := r.Title
			if title == "" {
				title = i18n.T("No task")
			}
			ui.Text(c, title).FontSize(px(t, fsCaption)).MaxLines(2).Ellipsis("…")
			meta := i18n.DateTime(r.CreatedAt)
			if r.Duration > 0 {
				meta += " · " + callClock(time.Duration(r.Duration*float64(time.Second)))
			}
			ui.Text(c, meta).FontSize(px(t, fsMicro)).TextColor(k.Text3)
		})
	})
	if b.Clicked() {
		a.selectCall(r.ID)
	}
}

// callBadge is "Live" or the outcome.
func (a *app) callBadge(c *ui.Context, phase, outcome string) {
	t := c.Theme()
	switch {
	case phase == services.CallDialing || phase == services.CallRinging || phase == services.CallLive:
		ui.Row(c).Gap(px(t, 4)).AlignItems(ui.Center).Shrink(0).Children(func() {
			statusDot(c, t.Success)
			ui.Text(c, i18n.T("Live")).FontSize(px(t, fsMicro)).FontWeight(600).TextColor(t.Success)
		})
	case outcome != "":
		col := tok(t).Text2
		switch outcome {
		case telephony.OutcomeBooked, telephony.OutcomeDone:
			col = t.Success
		case telephony.OutcomeNeedsYou:
			col = t.Warning
		case telephony.OutcomeFailed:
			col = t.Danger
		}
		ui.Text(c, callOutcomeText(outcome)).FontSize(px(t, fsMicro)).FontWeight(600).TextColor(col).SingleLine().Shrink(0)
	}
}
