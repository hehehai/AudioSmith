package main

// The old paths and where they go now (0.5 F1, plan/ui-overhaul-plan.md
// §2): the voice pages became Voices' tabs and New voice's flows, History
// became Assets, Tools became Developers, and Models, Engine, Logs and
// Integrations are Settings sections. navigate sends every old path on,
// so links, deep links (audiosmith://gallery), notifications and the
// agents' pages keep working. navKey is the sidebar item a path lights.

import "strings"

// routeAlias is where an old path goes now, and whether it is one.
func routeAlias(path string) (string, bool) {
	switch path {
	case "/gallery":
		return "/voices/discover", true
	case "/voices", "/voices/":
		return "/voices/mine", true
	case "/clone":
		return "/voices/new/clone", true
	case "/design":
		return "/voices/new/design", true
	case "/history":
		return "/assets", true
	case "/tools":
		return "/developers/tools", true
	case "/models", "/engine", "/logs", "/integrations":
		return "/settings" + path, true
	}
	// An integration's setup: its Settings section (Twilio's is the
	// Calls setup page).
	if strings.HasPrefix(path, "/integrations/") && path != "/integrations/twilio" {
		return "/settings" + path, true
	}
	return "", false
}

// navKey is the sidebar item that stands for path: its section, the New
// voice tool for the flows under it, More for More's pages.
func navKey(path string) string {
	switch {
	case path == "/voices/new" || strings.HasPrefix(path, "/voices/new/"):
		return "/voices/new"
	case path == "/api-reference":
		return "/developers"
	case path == "/integrations/twilio":
		return "/more"
	}
	sec := section(path)
	for _, p := range morePages {
		if p.path == sec {
			return "/more"
		}
	}
	return sec
}

// knownPath reports whether path (after routeAlias) is a page of the
// window: a page, one under it, or a Settings section.
func knownPath(path string) bool {
	if to, ok := routeAlias(path); ok {
		path = to
	}
	sec := section(path)
	if sec == "/settings" {
		return true
	}
	for _, p := range pages {
		if p.path == sec || strings.HasPrefix(p.path, sec+"/") {
			return true
		}
	}
	return sec == "/api-reference"
}
