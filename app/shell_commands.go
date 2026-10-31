package main

// Commands: one registry behind the ⌘K palette (palette.go), the native
// menu and the window's shortcuts, plus the "?" sheet listing the
// shortcuts, and audiosmith:// deep links.

import (
	"fmt"
	"net/url"
	"runtime"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// command is something the user can do from anywhere.
type command struct {
	id      string
	group   string // translated
	title   string // translated
	en      string // English title, so English words find it in any language
	keys    string // shortcut as shown, "⌘1"
	run     func()
	enabled bool
}

func itoa(n int) string { return fmt.Sprint(n) }

// commands lists the commands, in the palette's order.
func (a *app) commands() []command {
	nav := i18n.T("Go to")
	var cmds []command
	add := func(id, group, title, en, keys string, enabled bool, run func()) {
		cmds = append(cmds, command{id: id, group: group, title: title, en: en, keys: keys, run: run, enabled: enabled})
	}
	for _, p := range pages { // every page: the sidebar's, then the others (shell_pages.go)
		add("go"+p.path, nav, pageTitle(p.path), p.title, pageKeys(p.path), true, func() { a.navigate(p.path) })
	}
	a.longformCommands(nav, add) // M3: the book's commands (longform_pages.go, thread #18)
	a.dubCommands(nav, add)      // M4: the dub's commands (dub_pages.go, thread #19)
	a.m5Commands(nav, add)       // M5: API reference (api_pages.go, thread #20)
	a.wfCommands(nav, add)       // M6: the workflow's commands (workflow_pages.go, thread #21)
	a.callsCommands(nav, add)    // M6b: Calls (calls_pages.go, thread #23)
	add("go/settings", nav, i18n.T("Settings"), "Settings", "⌘,", true, func() { a.navigate("/settings") })
	for _, s := range settingsSections() {
		add("go/settings/"+s.id, i18n.T("Settings"), s.title, s.en, "", true, func() { a.navigate("/settings/" + s.id) })
	}

	studio := i18n.T("Studio")
	eng := a.engineSummary()
	add("generate", studio, i18n.T("Generate"), "Generate", "⌘↩", !eng.Busy && eng.State == engineReady, func() {
		a.navigate("/studio")
		a.generate()
	})
	add("cancel", studio, i18n.T("Cancel generation"), "Cancel generation", "", eng.Busy, a.cancel)
	add("preload", studio, i18n.T("Preload the model"), "Preload the model", "", !eng.Busy && eng.State == engineReady, a.preload)
	add("unload", studio, i18n.T("Unload the model"), "Unload the model", "", !eng.Busy && len(eng.Loaded) > 0, a.unload)
	add("restart", studio, i18n.T("Restart engine"), "Restart engine", "", true, a.restartEngine)

	// M2 (thread #17).
	speech := i18n.T("Speech to text")
	add("dictate", speech, i18n.T("Start or stop dictation"), "Start or stop dictation dictate", dictationGlyphs(a.dictShortcut), true, a.dictToggle)
	add("transcribe-file", speech, i18n.T("Transcribe a file…"), "Transcribe a file", "", a.trJob == nil, func() {
		a.navigate("/transcripts")
		a.chooseTranscribeFile()
	})

	view := i18n.T("View")
	add("search", view, i18n.T("Search everything"), "Search everything library find projects voices takes", "⌘F", true, a.focusSearch)
	add("palette", view, i18n.T("Command palette"), "Command palette", "⌘K", true, a.openPalette)
	add("switch", view, i18n.T("Engine quick switch"), "Engine quick switch", "⌘E", true, func() { a.switchOpen = true })
	add("logs-panel", view, i18n.T("Show or hide the logs panel"), "Show or hide the logs panel", "⇧⌘L", true, func() { a.logsPanel = !a.logsPanel })
	add("notifications", view, i18n.T("Notifications"), "Notifications", "", true, func() { a.noticesOpen = true })
	add("shortcuts", view, i18n.T("Keyboard shortcuts"), "Keyboard shortcuts", "?", true, func() { a.sheetOpen = true })
	add("updates", view, i18n.T("Check for Updates…"), "Check for updates software update new version", "", true, a.checkForUpdates)   // updates.go
	add("setup", view, i18n.T("Run the setup again"), "Run the setup again first run wizard welcome", "", !a.obOpen, a.openOnboarding) // M7a

	look := i18n.T("Appearance")
	for _, th := range []struct{ id, title, en string }{
		{"system", i18n.T("Theme: follow the system"), "Theme: follow the system"},
		{"light", i18n.T("Theme: light"), "Theme: light"},
		{"dark", i18n.T("Theme: dark"), "Theme: dark"},
	} {
		add("theme/"+th.id, look, th.title, th.en, "", a.prefs.Appearance.Theme != th.id, func() {
			a.updateSettings(func(s *settings.Settings) { s.Appearance.Theme = th.id })
		})
	}
	lang := i18n.T("Language")
	for _, tag := range append([]string{""}, i18n.Supported()...) {
		title := i18n.T("Follow the system language")
		if tag != "" {
			title = i18n.Name(tag)
		}
		add("lang/"+tag, lang, title, "Language "+tag, "", a.prefs.General.Language != tag, func() {
			a.updateSettings(func(s *settings.Settings) { s.General.Language = tag })
		})
	}
	return cmds
}

// matches reports whether every word of q is in the command's words.
func (cmd command) matches(q string) bool {
	hay := strings.ToLower(cmd.title + " " + cmd.group + " " + cmd.en + " " + cmd.id)
	for _, w := range strings.Fields(strings.ToLower(q)) {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

func (a *app) paletteItems() []command {
	var out []command
	for _, cmd := range a.commands() {
		if cmd.enabled && cmd.matches(a.paletteQuery) {
			out = append(out, cmd)
		}
	}
	return out
}

func (a *app) openPalette() {
	if !a.paletteOpen {
		a.paletteOpen, a.paletteQuery, a.paletteSel = true, "", 0
		a.homeReadRecent() // its Recent (palette.go)
	}
}

// runCommand closes the overlays and runs cmd.
func (a *app) runCommand(cmd command) {
	a.paletteOpen, a.sheetOpen = false, false
	cmd.run()
}

// shortcuts handles the window's keys. Each opens rather than toggles
// what the native menu's items also open, so a key that reaches both
// does the same thing once.
func (a *app) shortcuts(c *ui.Context) {
	if c.Shortcut(ui.Cmd, ui.KeyEnter) {
		if a.currentPage() == "home" && !a.settingsOpen {
			a.homeSubmit() // Home's composer: ⌘↩ sends, as Enter does
		} else {
			a.generate()
		}
	}
	if c.Shortcut(ui.Cmd, ui.KeyK) {
		a.openPalette()
	}
	if c.Shortcut(ui.Cmd, ui.KeyF) {
		a.focusSearch() // shell_search.go
	}
	if runtime.GOOS != "darwin" {
		// ⌘[ ⌘] are the router's on the Mac (with a mouse's back and
		// forward buttons); elsewhere it takes Alt+arrows, and these too.
		if c.Shortcut(ui.Cmd, ui.KeyBracketLeft) {
			a.goBack()
		}
		if c.Shortcut(ui.Cmd, ui.KeyBracketRight) {
			a.goForward()
		}
	}
	if c.Shortcut(ui.Cmd, ui.KeyE) {
		a.switchOpen = true
	}
	if c.Shortcut(ui.Cmd, ui.KeyComma) {
		a.navigate("/settings")
	}
	for i, path := range shortcutPages {
		if c.Shortcut(ui.Cmd, ui.Key1+ui.Key(i)) {
			a.navigate(path)
		}
	}
	if c.Shortcut(ui.Cmd|ui.Shift, ui.KeyL) {
		a.logsPanel = !a.logsPanel
	}
	if !a.scriptFocused() && c.Shortcut(ui.Shift, ui.KeySlash) { // M1-G: "?" is text in the script editor
		a.sheetOpen = true
	}
}

// commandIcon is a command's icon in the palette: its page's for "Go
// to" commands, a settings section's, or nil.
func commandIcon(id string) *icons.Icon {
	if rest, ok := strings.CutPrefix(id, "go/settings"); ok {
		ic := icons.Settings
		if sec := strings.TrimPrefix(rest, "/"); sec != "" {
			ic = settingsSectionIcon(sec)
		}
		return &ic
	}
	if rest, ok := strings.CutPrefix(id, "go"); ok {
		for _, p := range pages {
			if p.path == rest {
				ic := p.icon
				return &ic
			}
		}
	}
	return nil
}

// kbd shows a shortcut as a key cap.
func kbd(c *ui.Context, keys string) ui.Element {
	t := c.Theme()
	k := tok(t)
	return ui.Text(c, keys).FontSize(px(t, fsMicro)).TextColor(k.Text2).Padding(px(t, 1), px(t, 5)).Radius(px(t, 4)).
		Background(k.Subtle).Border(1, k.Line).SingleLine().Shrink(0)
}

// shortcutSheet lists the keyboard shortcuts ("?").
func (a *app) shortcutSheet(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	ui.Modal(c, &a.sheetOpen, func() {
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.Keyboard.Linear).Size(px(t, 18), px(t, 18)).TextColor(k.Text2)
			sectionHeading(c, i18n.T("Keyboard shortcuts")).Grow(1)
		})
		groups := []struct {
			title string
			rows  [][2]string
		}{
			{i18n.T("Navigation"), [][2]string{
				{"⌘F", i18n.T("Search everything")},
				{"⌘K", i18n.T("Command palette")},
				{"⌘1 … ⌘4", i18n.T("Home, Text to speech, Voices, Projects")},
				{"⌘5 … ⌘8", i18n.T("Dub, Speech to text, Stories, Audiobook")},
				{"⌘,", i18n.T("Settings")},
				{"⌘[  ⌘]", i18n.T("Back, forward")},
				{"⌘E", i18n.T("Engine quick switch")},
				{"⇧⌘L", i18n.T("Show or hide the logs panel")},
				{"?", i18n.T("Keyboard shortcuts")},
				{"Esc", i18n.T("Close a dialog or popover")},
			}},
			{i18n.T("Studio"), [][2]string{
				{"⌘↩", i18n.T("Generate")},
			}},
		}
		ui.Column(c).Gap(px(t, sp3)).Width(px(t, 420)).MaxWidth(px(t, 420)).Children(func() {
			for _, g := range groups {
				sectionTitle(c, g.title)
				ui.Grid(c).ColumnTracks(ui.Fixed(px(t, 90)), ui.Fr(1)).GapX(px(t, sp3)).GapY(px(t, 6)).AlignItems(ui.Center).Children(func() {
					for _, r := range g.rows {
						ui.Row(c).Children(func() { kbd(c, r[0]) })
						ui.Text(c, r[1])
					}
				})
			}
		})
		ui.Row(c).Justify(ui.End).Children(func() {
			if ui.Button(c.Key("sheet-close"), i18n.T("Close")).Clicked() {
				a.sheetOpen = false
			}
		})
	})
}

// buildMenu sets the native menu bar in the current language; the
// language switch calls it again. Items with shortcuts open what the
// window's own shortcuts open (see shortcuts).
func (a *app) buildMenu() {
	a.menuLocale = i18n.Locale()
	if !a.hasMenu {
		return
	}
	act := func(fn func()) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) { a.post(fn) }
	}
	goItems := []*mygo.MenuItem{}
	groups := append(slices.Clone(navGroups), navGroup{"More", morePages}, navGroup{"Foot", footPages}, navGroup{"Others", otherPages})
	for gi, g := range groups {
		if gi > 0 {
			goItems = append(goItems, mygo.Separator())
		}
		for _, p := range g.pages {
			acc := ""
			if k := pageKeys(p.path); k != "" {
				acc = "CmdOrCtrl+" + k[len("⌘"):]
			}
			goItems = append(goItems, &mygo.MenuItem{Label: pageTitle(p.path), Accelerator: acc, Click: act(func() { a.navigate(p.path) })})
		}
	}
	mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
		{Label: "AudioSmith", Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleAbout, Label: i18n.T("About AudioSmith")},
			// Check for Updates… opens the update panel (updates.go).
			{Label: i18n.T("Check for Updates…"), Click: act(a.checkForUpdates)},
			mygo.Separator(),
			{Label: i18n.T("Settings…"), Accelerator: "CmdOrCtrl+,", Click: act(func() { a.navigate("/settings") })},
			mygo.Separator(),
			{Role: mygo.RoleServices, Label: i18n.T("Services")},
			mygo.Separator(),
			{Role: mygo.RoleHide, Label: i18n.T("Hide AudioSmith")},
			{Role: mygo.RoleHideOthers, Label: i18n.T("Hide Others")},
			{Role: mygo.RoleUnhide, Label: i18n.T("Show All")},
			mygo.Separator(),
			{Role: mygo.RoleQuit, Label: i18n.T("Quit AudioSmith")},
		}},
		{Label: i18n.T("Edit"), Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleUndo, Label: i18n.T("Undo")},
			{Role: mygo.RoleRedo, Label: i18n.T("Redo")},
			mygo.Separator(),
			{Role: mygo.RoleCut, Label: i18n.T("Cut")},
			{Role: mygo.RoleCopy, Label: i18n.T("Copy")},
			{Role: mygo.RolePaste, Label: i18n.T("Paste")},
			{Role: mygo.RoleSelectAll, Label: i18n.T("Select All")},
		}},
		{Label: i18n.T("View"), Submenu: []*mygo.MenuItem{
			{Label: i18n.T("Search everything"), Accelerator: "CmdOrCtrl+F", Click: act(a.focusSearch)},
			{Label: i18n.T("Command palette"), Accelerator: "CmdOrCtrl+K", Click: act(a.openPalette)},
			{Label: i18n.T("Engine quick switch"), Accelerator: "CmdOrCtrl+E", Click: act(func() { a.switchOpen = true })},
			{Label: i18n.T("Show or hide the logs panel"), Click: act(func() { a.logsPanel = !a.logsPanel })},
			{Label: i18n.T("Notifications"), Click: act(func() { a.noticesOpen = true })},
			mygo.Separator(),
			{Role: mygo.RoleToggleFullScreen, Label: i18n.T("Enter Full Screen")},
		}},
		{Label: i18n.T("Go"), Submenu: append(goItems,
			mygo.Separator(),
			&mygo.MenuItem{Label: i18n.T("Settings"), Click: act(func() { a.navigate("/settings") })},
		)},
		{Label: i18n.T("Window"), Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleMinimize, Label: i18n.T("Minimize")},
			{Role: mygo.RoleZoom, Label: i18n.T("Zoom")},
			mygo.Separator(),
			{Role: mygo.RoleFront, Label: i18n.T("Bring All to Front")},
		}},
		{Label: i18n.T("Help"), Submenu: []*mygo.MenuItem{
			{Label: i18n.T("Keyboard shortcuts"), Click: act(func() { a.sheetOpen = true })},
			{Label: i18n.T("Logs"), Click: act(func() { a.navigate("/logs") })},
		}},
	}))
}

// openURL follows an audiosmith:// deep link: audiosmith://models,
// audiosmith://settings/models, audiosmith:///studio. Router paths in
// notifications go through it too.
func (a *app) openURL(raw string) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "" && !strings.EqualFold(u.Scheme, "audiosmith")) {
		a.toast(i18n.T("Unknown link: %s", raw))
		return
	}
	path := u.Path
	if u.Scheme != "" && u.Host != "" {
		path = "/" + u.Host + u.Path
	}
	path = "/" + strings.Trim(path, "/")
	switch {
	case knownPath(path): // a page, an old one (shell_routes.go) or a Settings section
		if to, ok := routeAlias(path); ok {
			path = to
		}
		if section(path) != "/settings" && !strings.HasPrefix(path, "/voices/") && path != "/calls/setup" && path != "/integrations/twilio" {
			path = section(path)
		}
		a.navigate(path)
	default:
		a.toast(i18n.T("Unknown link: %s", raw))
	}
}
