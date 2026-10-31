package main

// The window shell: the window, the sidebar and the router of pages, and
// the overlays every page shares (command palette, the shortcut sheet,
// the engine quick switch, the licence dialog), and the theme, in the
// ActNow family's window layout (no status bar: what goes on is counted
// by the sidebar's bell), organised as 0.5 F1 has it
// (plan/ui-overhaul-plan.md §2: places, tools, Home). There is no bar
// over the pages (0.5 H): each page's top is its own header
// (ui_page_header.go: title, ‹ ›, count, the page's tools). Its parts:
//
//	shell_pages.go        sidebar places, tools and pages, titles, badges, the router
//	shell_routes.go       old paths and where they go now, the sidebar item of a path
//	shell_nav.go          the sidebar, "··· More", its foot and the icon rail
//	shell_search.go       the sidebar's search (⌘F; it opens the ⌘K palette)
//	shell_bell.go         the tasks bell at the sidebar's foot and its popover
//	shell_activity.go     the activity cards (the bell's popover, the Tasks page)
//	shell_tasks.go        the Tasks page: every job that runs or waits
//	shell_commands.go     command registry, ⌘K palette, ? sheet, native menu, deep links
//	shell_status.go       jobs, notification centre, ⌘E quick switch
//	shell_logs.go         logs page and panel
//	shell_settings.go     settings service wiring, language, theme
//	settings_dialog.go    the settings dialog, its sections' router, rows and titles
//	settings_general.go   Settings → General, Appearance, Models, Engine, Studio
//	shell_models.go       Models state, downloads, licence gate and dialog
//	shell_models_view.go  Models page

import (
	"context"
	"io"
	"log"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/licence"
	"github.com/hehehai/AudioSmith/internal/logs"
	"github.com/hehehai/AudioSmith/internal/notify"
	"github.com/hehehai/AudioSmith/internal/oplog"
	"github.com/hehehai/AudioSmith/internal/settings"
)

const (
	titleBarHeight = 52
	// pageTop is where a page's content starts in the window: under its
	// header (ui_page_header.go), titleBarHeight tall. Drawers and pages
	// that size themselves by the window use it.
	pageTop        = titleBarHeight
	sidebarWidth   = 200
	inspectorWidth = 300
)

// baseFontSize is the platform's text size at 100 %: px scales sizes
// designed for it with the UI scale (settings.Appearance.Scale).
var baseFontSize = ui.LightTheme().FontSize

// px scales a size in DIPs designed at 100 % to the theme in use:
// ui.Text(c, s).FontSize(px(t, 12)).
func px(t *ui.Theme, n float32) float32 { return n * t.FontSize / baseFontSize }

// shellState is the shell's view state and the services it owns.
type shellState struct {
	router *ui.Router
	// nav is the sidebar's choice; navShown the router path it was last
	// synced from (see syncNav).
	nav, navShown string
	lastSettings  string    // settings section shown last
	stopOpLog     func()    // stops the operations log's watcher (ops_watch.go)
	quitStart     time.Time // when the quit began (quit.go's log)
	// The settings dialog (0.3): open, and the router of its sections
	// ("/general"), which navigate drives from "/settings/…" paths.
	settingsOpen bool
	setRouter    *ui.Router
	sectionW     float32 // while a section builds, its width (pageWidth)
	sectionH     float32 // the height a section shows in
	// settingsReveal is the part a section scrolls to (an old section's
	// id: revealAnchor), "" none.
	settingsReveal string

	bus      *core.Bus
	settings settings.Service
	prefs    settings.Settings // the settings, as this thread last saw them
	notes    *notify.Center
	notices  []core.Notification // newest first
	logs     *logs.Ring
	licences *licence.Gate
	accepted []core.LicenceAcceptance

	version string // the app's, for Settings → About; "" in tests

	paletteOpen   bool
	paletteQuery  string
	paletteSel    int
	sheetOpen     bool
	switchOpen    bool
	jobList       map[string]jobs.Job // unfinished jobs of every owner, by id
	activityState                     // shell_activity.go: the cards at the sidebar's foot
	noticesOpen   bool
	logsPanel     bool
	logsSource    string
	logsQuery     string
	logSel        logSelection // the lines selected (shell_logs_select.go)
	logsHeight    float32
	logsDirty     atomic.Bool // a redraw for new lines is on its way

	modelsState
	navState        // shell_nav.go (M7a)
	mediaToolsState // ffmpeg_settings.go (M7a): the optional ffmpeg
	storageState    // settings_storage.go (M7a)
	networkState    // settings_network.go (M7a)
	aboutState      // settings_about.go (M7a)
	onboardingState // onboarding.go (M7a): the first-run wizard
	settingsForm
	licenceDialog
	menuLocale string // locale the native menu was built in
	hasMenu    bool   // the app runs (not a test): it has a menu bar to build
}

func newShellState() shellState {
	return shellState{
		router:       ui.NewRouter("/home"),
		setRouter:    ui.NewRouter("/general"),
		lastSettings: "general",
		logsHeight:   220,
		prefs:        settings.Defaults(),
		aboutState:   aboutState{updates: &appUpdates{}},
	}
}

// openShell wires the shell to the library: its bus, the settings and
// the licence gate it keeps in the database, and the notification centre,
// which keeps info and warnings there too (document "notifications").
// Logs stay in memory until main's wiring (connectShell) adds the files.
func (a *app) openShell(lib *library) {
	a.bus = lib.bus
	a.router.Transition = ui.TransitionFade
	a.settings = lib.settings
	a.prefs = lib.settings.Get()
	notes, err := notify.Open(context.Background(), lib.store.Documents(), a.bus)
	if err != nil {
		log.Printf("notifications: %v", err)
	}
	a.notes = notes
	a.notices, _ = notes.List(context.Background())
	a.logs = logs.New(logs.Options{Bus: a.bus})
	a.stopOpLog = useOpLog(a.logs, a.bus) // ops_watch.go (#108)
	a.licences = lib.licences
	a.models = map[string]*modelState{}
	a.jobList = map[string]jobs.Job{}
	a.loadSettingsForm()

	core.On(a.bus, func(ev core.NotificationPosted) {
		a.post(func() { a.noticePosted(ev.Notification) })
	})
	core.On(a.bus, func(ev settings.Changed) {
		a.post(func() { a.settingsChanged(ev.Old, ev.New) })
	})
	core.On(a.bus, func(core.LicenceChanged) { a.loadAcceptances() })
	core.On(a.bus, func(core.LogAppended) {
		// Many lines may come at once: one redraw for all of them.
		if a.logsDirty.CompareAndSwap(false, true) {
			a.post(func() { a.logsDirty.Store(false) })
		}
	})
	a.applyLocale()
	a.loadAcceptances()
	a.loadMediaTool()          // M7a: ffmpeg_settings.go
	a.migrateRemovedSettings() // models_removed.go (thread #43)
}

// connectShell adds what only the real app has: log files in logDir (the
// Go log package writes there too), the app's version, the system
// language and deep links. main calls it before App.Run.
func (a *app) connectShell(logDir string) {
	a.logDir = logDir // M7a: Settings → Storage, Diagnostics
	if logDir != "" {
		a.logs.Close()
		a.logs = logs.New(logs.Options{Dir: logDir, Bus: a.bus})
		a.stopOpLog()
		a.stopOpLog = useOpLog(a.logs, a.bus)
		log.SetOutput(io.MultiWriter(os.Stderr, a.logs.Writer("app")))
	}
	mygo.App.OnOpenURL(func(u string) { a.post(func() { a.openURL(u) }) })
	oplog.Info("app.start", "version", mygo.App.Version(), "pid", os.Getpid())
	logSettingsMigrations(a.lib.settings.Migrations())
}

// logSettingsMigrations logs the stored settings the start migrated
// (settings.Open), e.g. #109's idle limit 0 → 30 minutes.
func logSettingsMigrations(ms []settings.Migration) {
	for _, m := range ms {
		oplog.Info("settings.migrate", "field", m.Field, "from", m.From, "to", m.To)
	}
}

// navigate goes to a router path ("/models"); it does nothing when that
// page shows already. "/settings/general" opens the settings dialog at
// that section, over the page, and "/settings" at the section shown last;
// any other path closes it.
func (a *app) navigate(path string) {
	if to, ok := routeAlias(path); ok { // an old path (shell_routes.go)
		path = to
	}
	if path == "/settings" || path == "/settings/" {
		path = "/settings/" + a.lastSettings
	}
	if rest, ok := strings.CutPrefix(path, "/settings/"); ok && rest != "" {
		a.lastSettings, _, _ = strings.Cut(rest, "/")
		a.openSettings("/" + rest)
		return
	}
	a.settingsOpen = false
	if path == a.router.Path() {
		return
	}
	a.leavePage()
	a.router.Push(path)
}

// showPage goes to a page by name ("studio", "assets", "settings";
// "models" opens Settings → Models & network). Other areas navigate only
// through it (and read currentPage).
func (a *app) showPage(name string) { a.navigate("/" + name) }

// goBack and goForward go through the window's history (the headers'
// ‹ ›, ⌘[ ⌘]): the page before or after, closing Settings.
func (a *app) goBack() {
	a.settingsOpen = false
	a.leavePage()
	a.router.Back()
}

func (a *app) goForward() {
	a.settingsOpen = false
	a.leavePage()
	a.router.Forward()
}

// leavePage keeps what the page shown has open for editing before
// another shows: a dub's line being edited.
func (a *app) leavePage() {
	if strings.HasPrefix(a.router.Path(), "/dub/") {
		a.dubCommitEdit()
	}
}

// currentPage is the name of the page shown: the first part of its path.
func (a *app) currentPage() string { return section(a.router.Path())[1:] }

// section is the first part of a path: "/settings/models" → "/settings".
func section(path string) string {
	if i := strings.IndexByte(path[1:], '/'); i >= 0 {
		return path[:i+1]
	}
	return path
}

// openWindow opens the main window; it runs in App.WhenReady.
func (a *app) openWindow() {
	a.version = mygo.App.Version()
	a.systemLocale = systemLanguage()
	a.hasMenu = true
	a.applyLocale() // the system's language, unless the settings name one
	a.buildMenu()
	a.win = mygo.NewWindow(mygo.WindowOptions{
		Title:          "AudioSmith",
		Width:          1100,
		Height:         720,
		MinWidth:       820,
		MinHeight:      520,
		StateKey:       "main",
		TitleBarStyle:  mygo.TitleBarHiddenInset,
		TitleBarHeight: titleBarHeight,
		Content:        ui.View(a.view),
	})
	a.refreshModels()
	a.connectUpdates() // updates.go (thread #50)
	a.maybeOnboard()   // M7a: a fresh install opens the setup (onboarding.go)
}

func (a *app) view(c *ui.Context) {
	a.applyTheme(c)
	t := c.Theme()
	if a.win == nil || a.prefs.Appearance.ReduceMotion {
		// Tests and snapshots show every change at once, as does the
		// user's Reduce motion.
		c.DisableAnimations()
	}
	for _, m := range a.toasts {
		c.Toast(m)
	}
	a.toasts = nil
	for _, t := range a.actionToasts {
		c.AddToast(t)
	}
	a.actionToasts = nil
	a.shortcuts(c)
	if a.engineSummary().Busy {
		// Keep the elapsed time current.
		c.After(500 * time.Millisecond)
	}
	a.buildActivity() // shell_activity.go: the cards of this frame
	if a.pl.Active() {
		// Notice the end of playback, so Pause turns back into Play.
		c.After(200 * time.Millisecond)
	}
	a.syncNav()
	// The headers' ‹ › (ui_page_header.go): the window's history.
	headerNav.canBack, headerNav.canForward = a.router.CanGoBack(), a.router.CanGoForward()
	headerNav.back, headerNav.forward = a.goBack, a.goForward
	headerNav.open, headerNav.pageW = a.navigate, a.pageWidth

	ui.Column(c).Fill().AlignItems(ui.Stretch).Children(func() {
		a.sidebarFrame(c, func() { // shell_nav.go: the sidebar or the icon rail
			a.pages(c)
			if a.logsPanel {
				ui.Divider(c)
				ui.Column(c.Key("logs-panel")).Height(px(t, a.logsHeight)).Shrink(0).Children(func() {
					a.logsView(c, true)
				})
			}
		})
	})
	a.settingsDialog(c) // settings_dialog.go
	a.updatePanel(c)    // updates_view.go: over Settings, whose About asks for it
	a.historyDialog(c)
	a.palette(c)
	a.quickSwitchDialog(c) // shell_status.go: ⌘E
	a.shortcutSheet(c)
	a.onboarding(c) // M7a: the first-run wizard, under the licence dialog
	a.licenceModal(c)
	a.exportDialog(c) // export_dialog.go (thread #31)
	// family_coord.go: a shortcut conflict's card (not modal).
	a.shortcutConflictCard(c)
	a.modelsAlerts(c)
	a.keyAlerts(c)    // family_keys_view.go
	a.removedAlert(c) // models_removed.go
	a.toastsView(c)   // last: over dialogs too
}

// toastsView gives the toasts their place and look: small ink toasts at
// the foot of the page, in its middle (the family's window spec §8); on
// the pages whose foot is taken by a bar of their own (toastsTop: the
// Studio's Generate and its take, the dub's player, the books' render
// bar) in the middle of the page's header instead, between its title and
// its actions. More stack beside the first. While the Settings dialog
// shows, they go to the foot of the dialog.
func (a *app) toastsView(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	ui.ToastViewportBase(c, func(vp ui.Element, toasts []ui.Toast) {
		w, h := c.Size()
		page := a.pageWidth(c)
		vp.AlignItems(ui.Center).Gap(px(t, sp2))
		switch {
		case a.settingsOpen:
			// Over the Settings dialog's foot: its title row is at the top.
			dh := min(h-2*px(t, sp6), px(t, settingsH))
			vp.Justify(ui.End).Padding(0, px(t, sp4), (h-dh)/2+px(t, sp4))
		case a.toastsTop():
			// In the page's header, at the window's top.
			vp.Justify(ui.Start).Padding(max(px(t, 4), (titleBarHeight-px(t, 36))/2), px(t, sp4), 0, w-page+px(t, sp4))
		default:
			vp.Justify(ui.End).Padding(0, px(t, sp4), px(t, sp6), w-page+px(t, sp4))
		}
		for _, ts := range toasts {
			p := ui.ToastBase(c, ts)
			box := p.Root.Row().AlignItems(ui.Center).Gap(px(t, sp3)).Padding(px(t, 9), px(t, 14)).Radius(px(t, radiusControl)).
				MaxWidth(min(px(t, 480), page-px(t, sp8))).Background(k.Ink).TextColor(k.OnInk).Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.22))
			// It fades out at the end.
			const fade = 200 * time.Millisecond
			switch left := p.Left(); {
			case left > 0 && left < fade:
				box.Opacity(float32(left) / float32(fade))
				c.AnimationFrame()
			case left > 0:
				c.After(left - fade)
			}
			box.Children(func() {
				ui.Text(c, ts.Title).MinWidth(0).Shrink(1)
				if ts.Action == "" {
					return
				}
				b := p.ActionButton().Padding(px(t, 3), px(t, 8)).Radius(px(t, 6)).TextColor(k.OnInk).FontWeight(600).Label(ts.Action).Shrink(0)
				if b.Hovered() {
					b.Background(k.OnInk.Alpha(0.14))
				}
				b.Children(func() { ui.Text(c, ts.Action).SingleLine() })
			})
		}
	})
}

// toastsTop reports whether the page shown has a bar of its own along its
// foot, which toasts at the bottom would cover: they go to its header.
func (a *app) toastsTop() bool {
	switch section(a.router.Path()) {
	case "/studio", "/stories", "/audiobook":
		return true
	case "/dub":
		return strings.HasPrefix(a.router.Path(), "/dub/") // the editor, not the list
	}
	return false
}

// orDash shows "—" for an empty value.
func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// showFatal explains, in a dialog, why AudioSmith cannot start (its
// library did not open), then quits. It runs instead of the app.
func showFatal(err error) {
	log.Print(err)
	mygo.App.WhenReady(func() {
		i18n.SetLocale(mygo.App.Locale())
		go func() {
			dataDir, _ := mygo.App.Path(mygo.PathUserData)
			res, _ := mygo.Dialog.Message(mygo.MessageOptions{
				Type:    mygo.MessageError,
				Title:   "AudioSmith",
				Message: i18n.T("AudioSmith cannot open its library"),
				Detail: i18n.T("Your takes and settings are kept in a database in %s. It could not be opened: %v", dataDir, err) +
					"\n\n" + i18n.T("If a newer AudioSmith created it, use that version. Nothing was changed."),
				Buttons: []string{i18n.T("Quit"), i18n.T("Show the folder")},
			})
			if res.Button == 1 {
				mygo.Shell.OpenPath(dataDir)
			}
			mygo.App.Quit()
		}()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
