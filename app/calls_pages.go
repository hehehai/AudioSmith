package main

// The M6b pages in the shell (one call each from shell*.go, app.go and
// workflow_pages.go), and the Projects page's calls.

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/telephony"
)

var callsIcon = icons.PhoneCallingRounded

// callsPageTitle translates the M6b pages' titles.
func callsPageTitle(path string) (string, bool) {
	switch path {
	case "/calls":
		return i18n.T("Calls"), true
	case "/calls/setup", "/integrations/twilio":
		return i18n.T("Twilio setup"), true
	}
	return "", false
}

// callsRoute builds /calls, /calls/setup and /integrations/twilio (before
// the Integrations page's own route).
func (a *app) callsRoute(c *ui.Context, r *ui.Route) bool {
	switch {
	case r.Match("/calls"):
		r.Title(i18n.T("Calls"))
		a.callsPage(c)
	case r.Match("/calls/setup"), r.Match("/integrations/twilio"):
		r.Title(i18n.T("Twilio setup"))
		a.twilioPage(c)
	default:
		return false
	}
	return true
}

// callsBadge is the sidebar badge of /calls (the shell places the page,
// shell_pages.go navGroups): "Live" while a call is in progress.
func (a *app) callsBadge(path string) (string, bool) {
	if path != "/calls" {
		return "", false
	}
	for _, call := range a.callFull {
		if call.Live() {
			return i18n.T("Live"), true
		}
	}
	return "", true
}

// callsCommands adds the palette's entries.
func (a *app) callsCommands(nav string, add func(id, group, title, en, keys string, enabled bool, run func())) {
	group := i18n.T("Calls")
	add("calls-setup", group, i18n.T("Set up Twilio"), "Set up Twilio phone calls", "", true, func() { a.navigate("/calls/setup") })
	add("calls-new", group, i18n.T("New call"), "New phone call", "", true, func() {
		a.navigate("/calls")
		a.callTab = 0
	})
	add("calls-inbound", group, i18n.T("Incoming calls"), "Incoming calls", "", true, func() {
		a.navigate("/calls")
		a.openInbound()
	})
	live := ""
	for id, call := range a.callFull {
		if call.Live() {
			live = id
		}
	}
	add("calls-hangup", group, i18n.T("Hang up"), "Hang up the call", "", live != "", func() { a.hangupCall(live) })
}

// callItemDetail is a call's state on the Projects page (the item's
// detail is "<outcome>|<phase>").
func callItemDetail(detail string) string {
	outcome, phase, _ := strings.Cut(detail, "|")
	switch phase {
	case services.CallDialing, services.CallRinging, services.CallLive:
		return i18n.T("Live")
	}
	if outcome == "" {
		return callPhaseText(phase)
	}
	return callOutcomeText(outcome)
}

// callOpenPage opens a call on the Calls page (Projects, deep links).
func (a *app) callOpenPage(id string) {
	a.navigate("/calls")
	a.selectCall(id)
}

// callMentionsRecording: whether a disclosure says the call is recorded.
func callMentionsRecording(s string) bool { return services.MentionsRecording(s) }

// callMasked shows a number masked.
func callMasked(n string) string { return telephony.MaskNumber(n) }

// twilioBlocks are the Twilio card's setup blocks (its page is the
// guided setup; the blocks serve the integration list's checks).
func twilioBlocks(integEnv) []integBlock {
	_, run := tunnelCommands(0, settings.DefaultGatewayPort)
	return []integBlock{{Title: i18n.T("Tunnel to the call gateway"), Hint: i18n.T("Then finish the setup on the Twilio setup page."), Lang: "shell", Code: run}}
}
