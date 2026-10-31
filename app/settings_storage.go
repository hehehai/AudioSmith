package main

// Settings → Storage (M7a): what takes room (by category, the models, the
// free space), clearing the caches, opening the folders, moving the data
// folder (storage_move.go) and the reset tiers: the look, all settings,
// everything.

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/family"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/oplog"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/store/sqlitestore"
)

// storageState is the Storage page's state.
type storageState struct {
	dataHome string // the default data folder (location.json lives there); "" = the one in use
	logDir   string

	stReport      *storageReport
	stLoading     bool
	stOrphansBusy bool // cleaning up what only uninstalled apps used (storage_family.go)
	stLoc         dataLocation

	moving     bool
	moveDone   int64
	moveTotal  int64
	moveTo     string
	moveErr    string
	moveCancel context.CancelFunc
	moveStage  string // moveChecking, moveCopying, moveRollingBack

	resetAsk   string // "settings" | "everything": the confirmation shown
	resetText  string // typed confirmation
	resetModel bool   // erase the downloaded models too
	resetKeys  bool   // and the Keychain items
	// resetKeeps lists the models other ActNow apps may use, which
	// erasing keeps (eraseKeeps); "" when none.
	resetKeeps string
}

// storageReport is what takes room.
type storageReport struct {
	cats      []storageCat
	data      int64 // the data folder, all of it
	models    []storageCat
	modelsAll int64 // the model folder, all of it
	ours      int64 // catalogue models in it
	free      int64 // free space on the data folder's disk
	freeModel int64 // and on the model folder's
	// The ActNow family (storage_family.go): AudioSmith's models only it
	// uses and shared, and those only uninstalled apps used.
	usage       family.Usage
	orphans     int
	orphanBytes int64
}

// storageCat is a category of files.
type storageCat struct {
	id, title string
	bytes     int64
	dir       string // to open; "" = none
}

// Test seams: relaunching the app, choosing a folder.
var (
	appRelaunch   = func() { mygo.App.Relaunch() }
	chooseMoveDir = func(a *app, then func(string)) {
		go func() {
			dirs, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: i18n.T("Move the data folder to…"),
				Directory: true, CreateDirectories: true, ButtonLabel: i18n.T("Move here")})
			if err != nil || len(dirs) == 0 {
				return
			}
			a.post(func() { then(dirs[0]) })
		}()
	}
)

func (a *app) dataDir() string  { return a.lib.store.Dir() }
func (a *app) cacheDir() string { return filepath.Dir(a.lib.peaks.Dir) }

// home is the default data folder.
func (a *app) home() string {
	if a.dataHome != "" {
		return a.dataHome
	}
	return a.dataDir()
}

// diskFree is the space free for the user on path's disk (a variable:
// the snapshot tests pin it, snapshot_env_test.go).
var diskFree = func(path string) int64 {
	var st syscall.Statfs_t
	for p := path; p != "" && p != "/" && p != "."; p = filepath.Dir(p) {
		if syscall.Statfs(p, &st) == nil {
			return int64(st.Bavail) * int64(st.Bsize)
		}
	}
	if syscall.Statfs("/", &st) == nil {
		return int64(st.Bavail) * int64(st.Bsize)
	}
	return 0
}

// dataCategories are the data folder's parts, by folder.
func dataCategories() []storageCat {
	return []storageCat{
		{id: "takes", title: i18n.T("Takes"), dir: "clips"},
		{id: "voices", title: i18n.T("Voices"), dir: "voices"},
		{id: "references", title: i18n.T("Unsaved voice clips"), dir: "references"},
		{id: "renders", title: i18n.T("Stories and audiobooks"), dir: "renders"},
		{id: "covers", title: i18n.T("Covers"), dir: "covers"},
		{id: "dubs", title: i18n.T("Dubs"), dir: "dubs"},
		{id: "transcripts", title: i18n.T("Transcripts"), dir: "transcripts"},
		{id: "dictation", title: i18n.T("Dictation recordings"), dir: "dictation"},
		{id: "workflows", title: i18n.T("Workflows"), dir: "workflows"},
		{id: "tools", title: i18n.T("Tools (ffmpeg, yt-dlp)"), dir: "tools"},
	}
}

// loadStorage measures the folders in the background.
func (a *app) loadStorage() {
	if a.stLoading {
		return
	}
	a.stLoading = true
	data, cache, logs, models := a.dataDir(), a.cacheDir(), a.logDir, modelCache(a.prefs)
	a.stLoc = readLocation(a.home())
	a.background(func() {
		ctx := context.Background()
		r := &storageReport{}
		var known int64
		for _, c := range dataCategories() {
			dir := filepath.Join(data, c.dir)
			c.bytes, _ = treeSize(ctx, dir, nil)
			c.dir = dir
			known += c.bytes
			r.cats = append(r.cats, c)
		}
		var db int64
		if ents, err := os.ReadDir(data); err == nil {
			for _, e := range ents {
				if strings.HasPrefix(e.Name(), sqlitestore.FileName) {
					if fi, err := e.Info(); err == nil {
						db += fi.Size()
					}
				}
			}
		}
		r.cats = append(r.cats, storageCat{id: "database", title: i18n.T("Library database"), bytes: db})
		r.data, _ = treeSize(ctx, data, nil)
		r.cats = append(r.cats, storageCat{id: "other", title: i18n.T("Other"), bytes: max(r.data-known-db, 0), dir: data})
		cb, _ := treeSize(ctx, cache, nil)
		r.cats = append(r.cats, storageCat{id: "caches", title: i18n.T("Caches"), bytes: cb, dir: cache})
		if logs != "" {
			lb, _ := treeSize(ctx, logs, nil)
			r.cats = append(r.cats, storageCat{id: "logs", title: i18n.T("Logs"), bytes: lb, dir: logs})
		}
		seen := map[string]bool{}
		for _, e := range catalog.Builtin().Entries {
			repos := []string{e.Repo}
			for _, x := range e.Aux {
				repos = append(repos, x.Repo)
			}
			var n int64
			for _, repo := range repos {
				if seen[repo] {
					continue
				}
				seen[repo] = true
				s, _ := treeSize(ctx, hub.RepoDir(models, repo), nil)
				n += s
			}
			if n > 0 {
				r.models = append(r.models, storageCat{id: e.ID, title: e.DisplayName(), bytes: n, dir: hub.RepoDir(models, e.Repo)})
				r.ours += n
			}
		}
		slices.SortFunc(r.models, func(x, y storageCat) int { return int(y.bytes>>20) - int(x.bytes>>20) })
		r.modelsAll, _ = treeSize(ctx, models, nil)
		r.free, r.freeModel = diskFree(data), diskFree(models)
		a.measureFamily(r, models) // storage_family.go
		a.post(func() { a.stReport, a.stLoading = r, false })
	})
}

// cacheParts are the caches Clear removes (all rebuilt when needed).
var cacheParts = []string{"peaks", "chunks", filepath.Join("longform", "chunks"), "auditions", "voice-previews", "ffmpeg"}

// clearCaches empties the caches; work in progress may use them, so it
// waits until no job runs.
func (a *app) clearCaches() {
	dir := a.cacheDir()
	a.background(func() {
		var freed int64
		for _, p := range cacheParts {
			d := filepath.Join(dir, p)
			n, _ := treeSize(context.Background(), d, nil)
			if os.RemoveAll(d) == nil {
				freed += n
			}
		}
		a.post(func() {
			a.toast(i18n.T("Freed %s", humanBytes(freed)))
			a.stReport = nil
			a.loadStorage()
		})
	})
}

// removePrevious deletes the folder the data was moved from.
func (a *app) removePrevious() {
	home, loc := a.home(), a.stLoc
	if loc.Previous == "" {
		return
	}
	a.background(func() {
		var err error
		if filepath.Clean(loc.Previous) == filepath.Clean(home) {
			err = removeDataExcept(home)
		} else {
			err = os.RemoveAll(loc.Previous)
		}
		loc.Previous = ""
		if err == nil {
			err = writeJSON(filepath.Join(home, locationFile), loc)
		}
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not remove it: %v", err))
			}
			a.stLoc = loc
			a.stReport = nil
		})
	})
}

// dismissMoveFailure forgets the last failed move.
func (a *app) dismissMoveFailure() {
	loc := a.stLoc
	loc.Failed = ""
	a.stLoc = loc
	home := a.home()
	a.background(func() { oplog.Err("storage.location.write", writeJSON(filepath.Join(home, locationFile), loc)) })
}

// resetLook restores the appearance and the language.
func (a *app) resetLook() {
	d := settings.Defaults()
	a.updateSettings(func(s *settings.Settings) { s.Appearance, s.General.Language = d.Appearance, d.General.Language })
	a.sideW, a.navOpen = 0, nil
	a.toast(i18n.T("The look is back to its defaults"))
}

// resetSettings restores every setting (the setup stays done; keys stay
// in the Keychain).
func (a *app) resetSettings() {
	a.updateSettings(func(s *settings.Settings) {
		*s = settings.Defaults()
		s.Setup.Done = true
	})
	a.sideW, a.navOpen = 0, nil
	a.toast(i18n.T("Every setting is back to its default"))
}

// keychainAccounts are the Keychain items AudioSmith may have saved.
func (a *app) keychainAccounts() []string {
	acc := []string{"huggingface-token", "api.key", "mt.deepl", "mt.google", "mt.microsoft"}
	for _, p := range a.prefs.LLM.Providers {
		acc = append(acc, "llm."+p.ID)
	}
	for _, p := range []string{"openai", "anthropic", "gemini", "openrouter", "groq", "deepseek", "mistral", "together"} {
		acc = append(acc, "llm."+p)
	}
	return acc
}

// eraseAll removes the models (when asked) and the Keychain items (when
// asked) now, then restarts: the start erases the data, the caches and
// the logs (storage_move.go).
func (a *app) eraseAll() {
	if len(a.activeJobs()) > 0 {
		a.toast(i18n.T("Wait for the jobs in progress to finish, or cancel them, first."))
		return
	}
	models, keys := a.resetModel, a.resetKeys
	cache, home, logs := modelCache(a.prefs), a.home(), a.logDir
	accounts := a.keychainAccounts()
	a.resetAsk = ""
	stopAPI := a.apiStopper()
	fam := a.hubClient().Options().Family
	a.background(func() {
		if stopAPI != nil {
			stopAPI()
		}
		if models {
			// Not what other ActNow apps may use (spec §7.6 step 3).
			freed, kept, err := hub.RemoveLeftovers(context.Background(), cache, catalogRepos(), fam)
			a.familyErase(cache) // family_app.go
			log.Printf("erase: models freed %s, kept %d, err %v", humanBytes(freed), len(kept), err)
		}
		if keys {
			for _, acc := range accounts {
				hub.Secret(acc, "").Set("")
			}
			a.forgetFamilyKeys() // family_keys.go: shared keys stay for the other apps
		}
		err := writeJSON(filepath.Join(home, resetFile), pendingReset{Everything: true, Also: []string{a.cacheDir(), logs}})
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not erase: %v", err))
				return
			}
			appRelaunch()
		})
	})
}

// storageSettings is Settings → Storage.
func (a *app) storageSettings(c *ui.Context) {
	t := c.Theme()
	if a.stReport == nil && !a.stLoading {
		a.loadStorage()
	}
	r := a.stReport
	muted := func(s string) { ui.Text(c, s).FontSize(px(t, 12)).TextColor(t.TextMuted) }
	open := func(key, dir string) {
		if dir != "" && a.win != nil && secondaryButton(c.Key(key), i18n.T("Open")).Clicked() {
			mygo.Shell.OpenPath(dir)
		}
	}

	settingGroup(c, i18n.T("Data folder"), func() {
		settingRow(c, "st-data", shortPath(a.dataDir()), i18n.T("Your library: takes, voices, projects, dubs, transcripts and the database."), func() {
			open("st-open-data", a.dataDir())
			if secondaryButton(c.Key("st-move"), i18n.T("Move…")).Disabled(a.moving).Clicked() {
				chooseMoveDir(a, a.startMove)
			}
		})
		if l := a.stLoc; l.Previous != "" {
			settingRowNote(c, "st-prev", i18n.T("The previous data folder is still there"), shortPath(l.Previous), func() {
				if secondaryButton(c.Key("st-prev-rm"), i18n.T("Remove it")).Clicked() {
					a.removePrevious()
				}
			})
		}
		if l := a.stLoc; l.Failed != "" {
			settingRowNote(c, "st-failed", i18n.T("The last move did not happen"), l.Failed, func() {
				if secondaryButton(c.Key("st-failed-ok"), i18n.T("Dismiss")).Clicked() {
					a.dismissMoveFailure()
				}
			})
		}
		if a.moveErr != "" {
			settingNote(c, "st-move-err", a.moveErr).TextColor(t.Danger)
		}

	})
	settingGroupBlock(c, i18n.T("What takes room"), "st-room", func() {
		if r == nil {
			ui.Row(c).Gap(px(t, 8)).Children(func() {
				ui.Spinner(c)
				muted(i18n.T("Measuring…"))
			})
		} else {
			muted(i18n.T("Data folder: %s · free on its disk: %s", humanBytes(r.data), humanBytes(r.free)))
			ui.Grid(c.Key("st-cats")).ColumnTracks(ui.Fr(1), ui.Fixed(px(t, 90)), ui.Fixed(px(t, 80))).GapX(px(t, 12)).GapY(px(t, 6)).Children(func() {
				for _, cat := range r.cats {
					ui.Text(c, cat.title)
					ui.Text(c, humanBytes(cat.bytes)).TextColor(t.TextMuted).TextAlign(ui.End)
					ui.Row(c.Key("cat-" + cat.id)).Children(func() {
						if cat.id == "caches" {
							busy := len(a.activeJobs()) > 0
							b := secondaryButton(c.Key("st-clear"), i18n.T("Clear")).Disabled(busy || cat.bytes == 0)
							if busy {
								b.Tooltip(i18n.T("Wait for the jobs in progress"))
							}
							if b.Clicked() {
								a.clearCaches()
							}
							return
						}
						open("open", cat.dir)
					})
				}
			})
			muted(i18n.T("Caches are rebuilt when needed: waveforms, rendered chunks, voice previews."))
		}

	})
	settingGroup(c, i18n.T("Models"), func() {
		shared := i18n.T("Change it in Settings → Models & network.")
		if len(a.famOthers) > 0 {
			shared = i18n.T("Shared by all ActNow apps. Change it in Settings → Models & network.") // spec §7.10.2
		}
		settingRow(c, "st-models", shortPath(modelCache(a.prefs)), shared, func() {
			open("st-open-models", modelCache(a.prefs))
		})
		if fam := a.lib.fam; fam != nil {
			// Time Machine (family spec §7.12): the family's models.backup,
			// off by default.
			on := fam.Backup()
			hint := i18n.T("Models can be downloaded again, so Time Machine leaves them out.")
			if len(a.famOthers) > 0 {
				hint = i18n.T("Models can be downloaded again, so Time Machine leaves them out. Applies to all ActNow apps.")
			}
			settingRow(c, "st-models-backup", i18n.T("Back up models with Time Machine"), hint, func() {
				if inkSwitch(c.Key("st-models-backup-switch"), &on).Changed() {
					go func(on bool) {
						if err := fam.SetBackup(context.Background(), on); err != nil {
							log.Printf("family: Time Machine: %v", err)
						}
					}(on)
				}
			})
		}
		if r != nil {
			settingBlock(c, "st-models-use", func() {
				muted(i18n.T("AudioSmith's models: %s of %s in the folder · free on its disk: %s", humanBytes(r.ours), humanBytes(r.modelsAll), humanBytes(r.freeModel)))
				ui.Grid(c.Key("st-models-list")).ColumnTracks(ui.Fr(1), ui.Fixed(px(t, 90))).GapX(px(t, 12)).GapY(px(t, 4)).Children(func() {
					for i, m := range r.models {
						if i >= 8 {
							break
						}
						ui.Text(c, m.title).FontSize(px(t, 12))
						ui.Text(c, humanBytes(m.bytes)).FontSize(px(t, 12)).TextColor(t.TextMuted).TextAlign(ui.End)
					}
				})
			})
			a.familyStorage(c, r) // storage_family.go
		}

	})
	settingGroup(c, i18n.T("Reset"), func() {
		settingRow(c, "st-reset-look", i18n.T("Reset the look"), i18n.T("Theme, colours, size, sidebar and language."), func() {
			if secondaryButton(c.Key("st-reset-look"), i18n.T("Reset")).Clicked() {
				a.resetLook()
			}
		})
		settingRow(c, "st-reset-settings", i18n.T("Reset all settings"), i18n.T("Every setting goes back to its default. Your library, models and saved keys stay."), func() {
			if secondaryButton(c.Key("st-reset-settings"), i18n.T("Reset…")).Clicked() {
				a.resetAsk = "settings"
			}
		})
		settingRow(c, "st-erase", i18n.T("Erase everything"), i18n.T("Deletes your library (takes, voices, projects, dubs, transcripts), settings, caches and logs, then restarts AudioSmith as new."), func() {
			if secondaryButton(c.Key("st-erase"), i18n.T("Erase…")).Clicked() {
				a.resetAsk, a.resetText = "everything", ""
				a.eraseKeeps()
			}
		})
		a.storageDialogs(c)
	})
}

// storageDialogs are the confirmations and the move's progress.
func (a *app) storageDialogs(c *ui.Context) {
	t := c.Theme()
	open := a.resetAsk == "settings"
	ui.Modal(c.Key("st-ask-settings"), &open, func() {
		ui.Text(c, i18n.T("Reset all settings?")).Bold()
		ui.Text(c, i18n.T("Every setting goes back to its default. Your library, models and saved keys stay.")).Width(px(t, 380))
		ui.Row(c).Gap(px(t, 8)).Justify(ui.End).Children(func() {
			if secondaryButton(c.Key("st-ask-cancel"), i18n.T("Cancel")).AutoFocus().Clicked() {
				a.resetAsk = ""
			}
			if secondaryButton(c.Key("st-ask-ok"), i18n.T("Reset the settings")).Clicked() {
				a.resetAsk = ""
				a.resetSettings()
			}
		})
	})
	if !open && a.resetAsk == "settings" {
		a.resetAsk = ""
	}
	erase := a.resetAsk == "everything"
	ui.Modal(c.Key("st-ask-erase"), &erase, func() {
		ui.Text(c, i18n.T("Erase everything?")).Bold()
		ui.Text(c, i18n.T("This deletes your takes, voices, projects, stories, audiobooks, dubs, transcripts and workflows, every setting, the caches and the logs. It cannot be undone. AudioSmith restarts as on its first day.")).Width(px(t, 420))
		ui.Checkbox(c.Key("st-erase-models"), &a.resetModel, i18n.T("Also delete the models AudioSmith downloaded"))
		if a.resetModel && a.resetKeeps != "" {
			ui.Text(c, a.resetKeeps).FontSize(px(t, 12)).TextColor(t.TextMuted).Width(px(t, 420))
		}
		ui.Checkbox(c.Key("st-erase-keys"), &a.resetKeys, i18n.T("Also delete the tokens and keys saved in the Keychain"))
		ui.Text(c, i18n.T("Type DELETE to confirm.")).FontSize(px(t, 12)).TextColor(t.TextMuted)
		ui.TextInput(c.Key("st-erase-text"), &a.resetText).Placeholder("DELETE").Width(px(t, 200)).Label(i18n.T("Type DELETE to confirm."))
		ui.Row(c).Gap(px(t, 8)).Justify(ui.End).Children(func() {
			if secondaryButton(c.Key("st-erase-cancel"), i18n.T("Cancel")).AutoFocus().Clicked() {
				a.resetAsk = ""
			}
			if secondaryButton(c.Key("st-erase-ok"), i18n.T("Erase and restart")).Disabled(strings.TrimSpace(a.resetText) != "DELETE").Clicked() {
				a.eraseAll()
			}
		})
	})
	if !erase && a.resetAsk == "everything" {
		a.resetAsk = ""
	}
	moving := a.moving
	ui.DialogBase(c.Key("st-moving"), &moving, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.3))
		panel.Width(px(t, 440)).Radius(12).Background(t.Background).Border(1, t.Border).Padding(px(t, 20)).Label(i18n.T("Moving the data folder"))
		ui.Column(c).Gap(px(t, 10)).Children(func() {
			ui.Text(c, i18n.T("Moving the data folder")).Bold()
			ui.Text(c, i18n.T("Copying to %s. AudioSmith restarts when the copy is checked, and finishes the move as it starts.", shortPath(a.moveTo))).FontSize(px(t, 12))
			frac := 0.0
			if a.moveTotal > 0 {
				frac = float64(a.moveDone) / float64(a.moveTotal)
			}
			if a.moveStage == moveCopying || a.moveStage == "" {
				ui.Progress(c, frac)
			} else {
				ui.Progress(c, -1)
			}
			status := fmt.Sprintf("%s / %s", humanBytes(a.moveDone), humanBytes(a.moveTotal))
			switch a.moveStage {
			case moveChecking:
				status = i18n.T("Measuring the data folder…")
			case moveRollingBack:
				status = i18n.T("Removing the partial copy…")
			}
			ui.Text(c, status).FontSize(px(t, 12)).TextColor(t.TextMuted)
			ui.Row(c).Justify(ui.End).Children(func() {
				if secondaryButton(c.Key("st-move-cancel"), i18n.T("Cancel")).Disabled(a.moveCancel == nil).Clicked() && a.moveCancel != nil {
					a.moveCancel()
				}
			})
		})
	})
}
