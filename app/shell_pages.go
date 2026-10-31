package main

// The shell's pages (M7a; 0.5 F1): the sidebar's places, tools and foot
// in order, the ⌘1–8 shortcuts, the pages' titles and badges, and the router that
// builds the page shown. Each area keeps its own routes, titles and
// badges in its file (longform_pages.go, dub_pages.go, …).

import (
	"slices"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// navPage is a page of the sidebar.
type navPage struct {
	path, title string // title: English, translated where shown (pageTitle)
	icon        icons.Icon
}

// navGroup is a section of the sidebar; the first, without an id, is the
// places: where the user's things are, with no title over them, that do
// not fold.
type navGroup struct {
	id    string // English title, translated by groupTitle; "" the places
	pages []navPage
}

// navGroups are the sidebar's places and tools, in order (0.5 F1, after
// ElevenLabs' information architecture: plan/ui-overhaul-plan.md §2). The
// places hold what the user made (Home, Voices, Projects, Assets); the
// tools each do one thing. "More" follows the tools (morePages), and
// Developers and Settings stand at the sidebar's foot (footPages). Each
// area keeps its page's view, route and badge in its own file.
var navGroups = []navGroup{
	{"", []navPage{
		{"/home", "Home", homeIcon}, // home_view.go
		{"/voices", "Voices", voicesIcon},
		{"/projects", "Projects", projectsIcon},
		{"/assets", "Assets", assetsIcon}, // the takes (takes_view.go)
	}},
	{"Tools", []navPage{
		{"/studio", "Text to speech", studioIcon},
		{"/voices/new", "New voice", cloneIcon}, // voices_pages.go
		{"/stories", "Stories", storiesIcon},    // longform_pages.go
		{"/audiobook", "Audiobook", audiobookIcon},
		{"/dub", "Dub", dubIcon},                           // dub_pages.go
		{"/transcripts", "Speech to text", transcribeIcon}, // transcribe*.go
		{"/isolate", "Voice isolator", isolateIcon},        // tool_pages.go (F2 builds it)
		{"/dictation", "Dictation", micIcon},               // tool_pages.go
	}},
}

// morePages are the tools under "··· More" (a menu after the tools).
var morePages = []navPage{
	{"/workflows", "Workflows", workflowsIcon}, // workflow_pages.go
	{"/batch", "Batch", batchIcon},             // dub_pages.go
	{"/calls", "Calls", callsIcon},             // calls_pages.go
}

// footPages stand at the sidebar's foot, over Settings.
var footPages = []navPage{
	{"/developers", "Developers", developersIcon}, // developers_view.go
}

// otherPages are the pages the sidebar does not list: the palette, the
// Go menu, links and buttons reach them. Models, Engine, Logs and
// Integrations are Settings sections (routeAlias).
var otherPages = []navPage{
	{"/tasks", "Tasks", tasksIcon}, // shell_tasks.go; the top bar's bell
}

// shortcutPages are the pages ⌘1…⌘8 open, in that order: the ones used
// most. The others are a click or the palette (⌘K) away.
var shortcutPages = []string{"/home", "/studio", "/voices", "/projects", "/dub", "/transcripts", "/stories", "/audiobook"}

// sidebarPages are the pages of the sidebar, in its order: the places,
// the tools, More's and the foot's.
var sidebarPages = func() []navPage {
	var out []navPage
	for _, g := range navGroups {
		out = append(out, g.pages...)
	}
	out = append(out, morePages...)
	return append(out, footPages...)
}()

// pages are every page: the sidebar's, then the others.
var pages = append(slices.Clone(sidebarPages), otherPages...)

// pageKeys is the shortcut of a page ("⌘1"), "" when it has none.
func pageKeys(path string) string {
	for i, p := range shortcutPages {
		if p == path {
			return "⌘" + itoa(i+1)
		}
	}
	return ""
}

// groupTitle translates a sidebar section's title.
func groupTitle(id string) string {
	switch id {
	case "":
		return i18n.T("Main pages")
	case "Tools":
		return i18n.T("Tools")
	case "More":
		return i18n.T("More")
	}
	return id
}

// navBadge is what a page's sidebar item shows after its title: a count
// or progress, "" for nothing. Areas answer for their pages.
func (a *app) navBadge(path string) string {
	if b, ok := a.longformBadge(path); ok { // M3: longform_pages.go
		return b
	}
	if b, ok := a.dubBadge(path); ok { // M4: dub_pages.go
		return b
	}
	if b, ok := a.wfBadge(path); ok { // M6: workflow_pages.go
		return b
	}
	if b, ok := a.callsBadge(path); ok { // M6b: calls_pages.go
		return b
	}
	return ""
}

// pageTitle translates a page's title (literal calls, for the key scan).
func pageTitle(path string) string {
	switch path {
	case "/home":
		return i18n.T("Home")
	case "/studio":
		return i18n.T("Text to speech")
	case "/assets":
		return i18n.T("Assets")
	case "/settings":
		return i18n.T("Settings")
	case "/tasks":
		return i18n.T("Tasks")
	case "/transcripts": // M2
		return i18n.T("Speech to text")
	case "/more":
		return i18n.T("More")
	}
	if t, ok := toolPageTitle(path); ok { // tool_pages.go, developers_view.go (F1)
		return t
	}
	if t, ok := voicePageTitle(path); ok { // voices_pages.go
		return t
	}
	if t, ok := dubPageTitle(path); ok { // M4: dub_pages.go (thread #19)
		return t
	}
	if t, ok := longformPageTitle(path); ok { // M3: longform_pages.go (thread #18)
		return t
	}
	if t, ok := m5PageTitle(path); ok { // M5: api_pages.go (thread #20)
		return t
	}
	if t, ok := wfPageTitle(path); ok { // M6: workflow_pages.go (thread #21)
		return t
	}
	if t, ok := callsPageTitle(path); ok { // M6b: calls_pages.go (thread #23)
		return t
	}
	return path
}

var (
	homeIcon       = icons.Home2
	studioIcon     = icons.Soundwave
	historyIcon    = icons.History
	assetsIcon     = icons.Archive
	isolateIcon    = icons.MusicNotes
	developersIcon = icons.CodeSquare
	engineIcon     = icons.CpuBolt
	modelsIcon     = icons.Box
	logsIcon       = icons.ClipboardList
	settingsIcon   = icons.Settings
	bellIcon       = icons.Bell
	searchIcon     = icons.Magnifer
	tasksIcon      = icons.ChecklistMinimalistic
)

// pages builds the page the router shows. Old paths never reach it:
// navigate sends them where they went (routeAlias, shell_routes.go).
func (a *app) pages(c *ui.Context) {
	a.router.View(c, func(r *ui.Route) {
		switch {
		case r.Match("/home"):
			r.Title(i18n.T("Home"))
			a.homePage(c) // home_view.go (F1)
		case r.Match("/studio"):
			r.Title(i18n.T("Text to speech"))
			a.studioPage(c)
		case r.Match("/assets"):
			r.Title(i18n.T("Assets"))
			a.historyPage(c)
		case r.Match("/tasks"):
			r.Title(i18n.T("Tasks"))
			a.tasksPage(c) // shell_tasks.go
		case a.voicesRoute(c, r): // /voices/…, /voices/new/… (voices_pages.go)
		case r.Match("/transcripts"): // M2: transcribe_view.go
			r.Title(i18n.T("Speech to text"))
			a.transcribePage(c)
		case a.toolRoute(c, r): // /isolate, /dictation, /developers (tool_pages.go, F1)
		case a.longformRoute(c, r): // M3: /stories, /audiobook, /projects (longform_pages.go, thread #18)
		case a.dubRoute(c, r): // M4: /dub, /batch (dub_pages.go, thread #19)
		case a.callsRoute(c, r): // M6b: /calls, /calls/setup, /integrations/twilio (calls_pages.go, thread #23)
		case a.m5Route(c, r): // M5: /api-reference (api_pages.go, thread #20)
		case a.wfRoute(c, r): // M6: /workflows (workflow_pages.go, thread #21)
		default:
			r.Title(i18n.T("Not found"))
			a.notFoundPage(c, r.Path())
		}
	}).Grow(1).MinHeight(0)
}

func (a *app) notFoundPage(c *ui.Context, path string) {
	pageHeader(c, i18n.T("Not found"), nil)
	emptyState(c, icons.Magnifer, i18n.T("There is no page at %s.", path), "", func() {
		if ui.Button(c.Key("go-home"), i18n.T("Go to Home")).Clicked() {
			a.navigate("/home")
		}
	})
}
