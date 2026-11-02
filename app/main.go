// AudioSmith is a TTS workbench for macOS: write a script, pick a model and
// voice, generate takes on the GPU with MLX, and listen, compare and export
// them. The window is MyGo native UI in Go; speech comes from
// audiosmith-engine, a Swift helper on mlx-audio-swift (see engine/).
//
// Package main is the UI (views and their state); the domain lives in
// internal/ (see docs/architecture.md). main only wires them together:
// each step is a function in the file of the area that owns it.
package main

import (
	"log"

	"github.com/egoist/mygo"
	// MyGo's own strings (its controls, menus, dialogs) in Simplified
	// Chinese; English is always linked.
	_ "github.com/egoist/mygo/locales/zh-Hans"
	"github.com/hehehai/AudioSmith/internal/i18n"
)

func main() {
	// M5 (thread #20): "AudioSmith --mcp-stdio" relays an MCP client to the running app (api_stdio.go).
	if runMCPStdio() {
		return
	}
	// Dev builds are "AudioSmith Dev"; share data with the installed app.
	mygo.App.SetName("AudioSmith")
	// MyGo 0.5: the app's languages, which App.Locale picks from (else
	// English alone) and which MyGo's own strings and the text engine
	// follow (applyLocale sets the one Settings chose).
	mygo.App.SetLanguages(i18n.Supported()...)
	// M2 (thread #17): one AudioSmith at a time (one library, one
	// dictation shortcut); a second launch shows the first one's window.
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}

	home, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		log.Fatal(err)
	}
	// M7a: the data folder may have moved (Settings → Storage); a move or
	// an erase asked for finishes here, before the library opens.
	dataDir, err := resolveDataDir(home) // storage_move.go
	if err != nil {
		showFatal(err)
		return
	}
	resources, _ := mygo.App.Path(mygo.PathResources)
	logDir, _ := mygo.App.Path(mygo.PathLogs)

	// Wiring: one call per area, each defined in the file of that area.
	lib, err := openTakeStore(dataDir) // takes.go
	if err != nil {
		showFatal(err) // shell.go: explains, then quits
		return
	}
	eng := newEngineProcess(resources, logDir) // enginelink.go
	a := newApp(eng, lib)                      // app.go
	a.updates = useUpdates()                   // updates.go: the in-app updater
	a.dataHome = home                          // M7a: settings_storage.go
	a.connectShell(logDir)                     // shell.go
	a.connectFamily()                          // family_app.go (thread #72): the ActNow family (before the engine: its memory budget)
	a.connectEngine(eng)                       // enginelink.go
	a.registerDubMedia()                       // M4: dub_pages.go (the video preview's scheme)

	mygo.App.WhenReady(func() {
		a.openWindow() // shell.go
		a.startEngine()
		a.startDubs()          // M4: dub.go (the watch folder)
		a.startAPI(dataDir)    // M5: api.go — the Local API and MCP (thread #20)
		a.connectDictation()   // M2: dictation.go (shortcut, menu bar icon, close to tray)
		a.startFamily(dataDir) // family_app.go: manifest and claims, in the background
		a.startSweeps()        // storage_clean.go: disk space nothing needs (code review 1102)
		mygo.App.OnSecondInstance(func([]string, string) { a.post(a.showMainWindow) })
	})
	// The engines stop on OnWillQuit (enginelink.go), so the scheduler
	// records interrupted jobs before the library closes the database.
	mygo.App.OnQuit(func() {
		a.quit()
		lib.close()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
