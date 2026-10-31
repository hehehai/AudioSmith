package main

// Settings → Diagnostics and about (M7a): versions and the machine, a
// self-check (library, engine, folders, network, Keychain, ffmpeg,
// permissions), "Copy diagnostics", a diagnostic bundle (a ZIP of the
// logs, the settings without secrets and the diagnostics), and Report a
// bug, which opens a prefilled GitHub issue in the browser.

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/logs"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/store/sqlitestore"
	"github.com/hehehai/actnowkit/redact"
)

// issuesURL is where bugs are reported.
const issuesURL = api.SourceURL + "/issues/new"

// AudioSmith's pages on actnow.dev, the family website that serves its
// downloads, updates and release notes (thread #69): the product page, the
// download page (a free account) and the release notes. The source code
// stays on GitHub (api.SourceURL, AGPL-3.0).
const (
	websiteURL      = "https://actnow.dev/audiosmith"
	downloadURL     = "https://actnow.dev/download/audiosmith"
	releaseNotesURL = "https://actnow.dev/audiosmith/changelog"
)

// licenceURL is AudioSmith's licence, the GNU AGPL version 3 (LICENSE).
const licenceURL = "https://www.gnu.org/licenses/agpl-3.0.html"

// thirdPartyNotices is the file with the licence texts of every third-party
// component (scripts/third-party-notices.sh), in the app's resources.
const thirdPartyNotices = "THIRD-PARTY-NOTICES.txt"

// thirdPartyNoticesURL opens the app's copy of the third-party notices in
// the default text editor, or, in a build without it (go run, tests), the
// copy on GitHub.
func thirdPartyNoticesURL() string {
	if res, err := mygo.App.Path(mygo.PathResources); err == nil {
		if path := filepath.Join(res, thirdPartyNotices); fileExists(path) {
			return (&url.URL{Scheme: "file", Path: path}).String()
		}
	}
	return api.SourceURL + "/blob/main/" + thirdPartyNotices
}

// aboutState is the Diagnostics page's state (and the machine, which the
// first-run wizard reads too).
type aboutState struct {
	sys        *sysInfo
	sysLoading bool

	checks       []selfCheck
	selfChecking bool

	usage        *sqlitestore.Usage
	usageLoading bool
	usageErr     string

	// noticesURL opens the third-party notices (thirdPartyNoticesURL).
	noticesURL string

	// Software updates (updates.go).
	updates *appUpdates
}

// selfCheck is one check's outcome.
type selfCheck struct {
	id     string
	title  string
	status string // "pass", "warn", "fail"
	detail string
}

// diagnosticsText describes this install, without anything the user
// wrote or said: for bug reports.
func (a *app) diagnosticsText() string {
	var b strings.Builder
	line := func(k, v string) { fmt.Fprintf(&b, "%s: %s\n", k, orDash(v)) }
	ver := a.version
	if ver == "" {
		ver = "dev"
	}
	line("AudioSmith", ver)
	line("Engine", a.engVersion+" ("+engStateName(a.engState)+")")
	line("Protocol", fmt.Sprint(proto.Version))
	if s := a.sys; s != nil {
		line("System", s.OS)
		line("Chip", s.Chip+" · "+s.Arch+fmt.Sprintf(" · %d cores", s.Cores))
		line("Memory", fmt.Sprintf("%d GB", s.MemoryGB()))
	}
	line("Go", runtime.Version())
	line("MyGo", moduleVersion("github.com/egoist/mygo"))
	line("Data folder", fmt.Sprintf("%s (free %s)", a.dataDir(), humanBytes(diskFree(a.dataDir()))))
	line("Model folder", fmt.Sprintf("%s (free %s)", modelCache(a.prefs), humanBytes(diskFree(modelCache(a.prefs)))))
	if tool := a.mediaTool(); tool != nil {
		line("ffmpeg", tool.Version+" "+tool.Licence+" ("+a.prefs.Media.Origin+")")
	} else {
		line("ffmpeg", "not set up")
	}
	line("Language", i18n.Locale())
	line("Loaded models", strings.Join(a.engineSummary().Loaded, ", "))
	for _, c := range a.checks {
		line("Check "+c.id, c.status+" "+c.detail)
	}
	return b.String()
}

// engStateName names an engine state in English (diagnostics).
func engStateName(s engineState) string {
	switch s {
	case engineReady:
		return "ready"
	case engineFailed:
		return "failed"
	}
	return "starting"
}

// moduleVersion is a dependency's version from the build.
func moduleVersion(path string) string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, m := range bi.Deps {
			if m.Path == path {
				return m.Version
			}
		}
	}
	return ""
}

// runSelfCheck checks what AudioSmith needs, in the background.
func (a *app) runSelfCheck() {
	if a.selfChecking {
		return
	}
	a.selfChecking = true
	st, eng, engErr, engVer := a.lib.store, a.engState, a.engError, a.engVersion
	models, data := modelCache(a.prefs), a.dataDir()
	tool, mediaPath := a.mediaTool(), a.prefs.Media.FFmpeg
	client := hub.NewHTTPClient(a.prefs.Models.Proxy, 10*time.Second)
	perms := a.perms
	if a.dictSys != nil {
		a.recheckPermissions()
		perms = a.perms
	}
	a.background(func() {
		var out []selfCheck
		add := func(id, title, status, detail string) {
			out = append(out, selfCheck{id: id, title: title, status: status, detail: detail})
		}
		// The library.
		if u, err := st.Usage(context.Background()); err != nil {
			add("library", i18n.T("Library database"), "fail", err.Error())
		} else {
			add("library", i18n.T("Library database"), "pass", i18n.N(u.Takes, "%d take", "%d takes", u.Takes))
		}
		// The engine.
		switch eng {
		case engineReady:
			add("engine", i18n.T("Engine"), "pass", i18n.T("Ready (%s)", engVer))
		case engineFailed:
			add("engine", i18n.T("Engine"), "fail", orDash(engErr))
		default:
			add("engine", i18n.T("Engine"), "warn", i18n.T("Starting…"))
		}
		// The folders.
		for _, f := range []struct{ id, title, dir string }{{"models", i18n.T("Model folder"), models}, {"data", i18n.T("Data folder"), data}} {
			os.MkdirAll(f.dir, 0o755)
			tmp, err := os.CreateTemp(f.dir, ".audiosmith-check-*")
			if err != nil {
				add(f.id, f.title, "fail", i18n.T("Cannot write in %s: %v", f.dir, err))
				continue
			}
			tmp.Close()
			os.Remove(tmp.Name())
			free := diskFree(f.dir)
			switch {
			case free > 0 && free < 5<<30:
				add(f.id, f.title, "fail", i18n.T("Only %s free: models and renders need room.", humanBytes(free)))
			case free > 0 && free < 10<<30:
				add(f.id, f.title, "warn", i18n.T("%s free: keep at least 10 GB.", humanBytes(free)))
			default:
				add(f.id, f.title, "pass", i18n.T("Writable · %s free", humanBytes(free)))
			}
		}
		// The network, through the proxy.
		hf, mirror := reach(client, "https://huggingface.co"), reach(client, "https://hf-mirror.com")
		switch {
		case hf.ok:
			add("network", i18n.T("Network"), "pass", i18n.T("huggingface.co answers in %d ms", hf.latency.Milliseconds()))
		case mirror.ok:
			add("network", i18n.T("Network"), "warn", i18n.T("huggingface.co does not answer (%s); hf-mirror.com does: use it in Settings → Models & network.", hf.err))
		default:
			add("network", i18n.T("Network"), "warn", i18n.T("No download source answers (%s): check the proxy in Settings → Models & network.", hf.err))
		}
		// The Keychain.
		if runtime.GOOS != "darwin" {
			add("keychain", i18n.T("Keychain"), "warn", i18n.T("Only on macOS"))
		} else if _, err := hub.DefaultKeychain.Get(); err != nil {
			add("keychain", i18n.T("Keychain"), "fail", err.Error())
		} else {
			add("keychain", i18n.T("Keychain"), "pass", i18n.T("Readable"))
		}
		// ffmpeg (optional).
		switch {
		case mediaPath == "":
			add("ffmpeg", "ffmpeg", "pass", i18n.T("Not set up (optional)"))
		case tool == nil:
			add("ffmpeg", "ffmpeg", "warn", i18n.T("%s does not run", mediaPath))
		default:
			add("ffmpeg", "ffmpeg", "pass", tool.Version)
		}
		// Permissions (dictation, recording).
		if perms.checked {
			if t, ok := permissionText(perms.mic); ok {
				add("microphone", i18n.T("Microphone"), "pass", t)
			} else {
				add("microphone", i18n.T("Microphone"), "warn", t)
			}
			if perms.access {
				add("accessibility", i18n.T("Accessibility"), "pass", i18n.T("Allowed"))
			} else {
				add("accessibility", i18n.T("Accessibility"), "warn", i18n.T("Not allowed: dictation copies the text instead of typing it."))
			}
		}
		a.post(func() { a.checks, a.selfChecking = out, false })
	})
}

// redactedSettings is the settings as JSON without anything secret
// (LLM provider headers can carry keys).
func redactedSettings(s settings.Settings) []byte {
	s.LLM.Providers = slices.Clone(s.LLM.Providers) // the app's own settings stay as they are
	for i := range s.LLM.Providers {
		h := map[string]string{}
		for k := range s.LLM.Providers[i].Headers {
			h[k] = "•••"
		}
		s.LLM.Providers[i].Headers = h
	}
	data, _ := json.MarshalIndent(s, "", "  ")
	return []byte(redact.Text(string(data))) // and a proxy's user:password@
}

// writeBundle writes the diagnostic bundle (a ZIP) to path.
func writeBundle(path, diag string, prefs settings.Settings, logDir string) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)
	put := func(name string, data []byte) error {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}
	err = put("diagnostics.txt", []byte(diag))
	if err == nil {
		err = put("settings.json", redactedSettings(prefs))
	}
	if err == nil && logDir != "" {
		ents, _ := os.ReadDir(logDir)
		for _, e := range ents {
			if e.IsDir() || !strings.Contains(e.Name(), ".log") {
				continue
			}
			src, rerr := os.Open(filepath.Join(logDir, e.Name()))
			if rerr != nil {
				continue
			}
			w, cerr := zw.Create("logs/" + e.Name())
			if cerr == nil {
				cerr = copyRedacted(w, src) // #100 L3: older files, and other writers', too
			}
			src.Close()
			if cerr != nil {
				err = cerr
				break
			}
		}
	}
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// bundleDir is where tests save the bundle (no dialog).
var bundleDir string

// saveBundle asks where, then writes the diagnostic bundle.
func (a *app) saveBundle() {
	diag, prefs, logs := a.diagnosticsText(), a.prefs, a.logDir
	name := "AudioSmith diagnostics " + time.Now().Format("2006-01-02 1504") + ".zip"
	write := func(dst string) {
		a.background(func() {
			err := writeBundle(dst, diag, prefs, logs)
			a.post(func() {
				if err != nil {
					a.toastWarn(i18n.T("Could not save the bundle: %v", err))
					return
				}
				a.toast(i18n.T("Saved %s", filepath.Base(dst)))
			})
		})
	}
	if a.win == nil {
		if bundleDir != "" {
			write(filepath.Join(bundleDir, name))
		}
		return
	}
	go func() {
		dst, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.win, Title: i18n.T("Save a diagnostic bundle"), DefaultPath: name,
			Filters: []mygo.FileFilter{{Name: "ZIP", Extensions: []string{"zip"}}}})
		if err != nil || dst == "" {
			return
		}
		a.post(func() { write(dst) })
	}()
}

// bugReportURL is a new GitHub issue with the diagnostics filled in.
func (a *app) bugReportURL() string {
	diag := a.diagnosticsText()
	if len(diag) > 5000 {
		diag = diag[:5000]
	}
	body := "### What happened\n\n\n### What you expected\n\n\n### Steps to reproduce\n\n1. \n\n### Diagnostics\n\n```\n" + diag + "```\n"
	q := url.Values{"body": {body}, "labels": {"bug"}}
	return issuesURL + "?" + q.Encode()
}

// aboutSettings is Settings → Diagnostics and about.
func (a *app) aboutSettings(c *ui.Context) {
	a.loadSysInfo()
	settingGroup(c, "", func() { a.aboutBlocks(c) })
	a.softwareUpdateSettings(c) // thread #50
}

// aboutBlocks are About's versions and links, then the licence notes.
func (a *app) aboutBlocks(c *ui.Context) {
	t := c.Theme()
	settingBlock(c, "about-block", func() {
		ver := a.version
		if ver == "" {
			ver = i18n.T("development build")
		}
		rows := [][2]string{
			{i18n.T("AudioSmith"), ver},
			{i18n.T("Engine"), orDash(a.engVersion)},
			{i18n.T("Engine protocol"), fmt.Sprint(proto.Version)},
		}
		if s := a.sys; s != nil {
			rows = append(rows,
				[2]string{i18n.T("System"), orDash(s.OS)},
				[2]string{i18n.T("Chip"), orDash(s.Chip)},
				[2]string{i18n.T("Memory"), i18n.T("%d GB", s.MemoryGB())},
			)
		}
		if tool := a.mediaTool(); tool != nil {
			rows = append(rows, [2]string{"ffmpeg", tool.Version})
		}
		ui.Grid(c.Key("about-grid")).ColumnTracks(ui.Fixed(px(t, 150)), ui.Fr(1)).GapX(px(t, 12)).GapY(px(t, 6)).Children(func() {
			for i, r := range rows {
				ui.Text(c, r[0]).TextColor(t.TextMuted)
				if i == 0 && isPrerelease(a.version) {
					// A beta (thread #52): the version, then a Beta badge.
					ui.Row(c.Key("about-version")).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
						ui.Text(c, r[1]).Selectable()
						betaBadge(c)
					})
					continue
				}
				ui.Text(c, r[1]).Selectable()
			}
			// AGPL-3.0: the licence, and where the source code is.
			ui.Text(c, i18n.T("Licence")).TextColor(t.TextMuted)
			ui.Link(c, "GNU AGPL-3.0", licenceURL)
			ui.Text(c, i18n.T("Source code")).TextColor(t.TextMuted)
			ui.Link(c, strings.TrimPrefix(api.SourceURL, "https://"), api.SourceURL)
			// The website (thread #69): downloads and release notes.
			for _, l := range [][2]string{{i18n.T("Website"), websiteURL}, {i18n.T("Download"), downloadURL}, {i18n.T("Release notes"), releaseNotesURL}} {
				ui.Text(c, l[0]).TextColor(t.TextMuted)
				ui.Link(c, strings.TrimPrefix(l[1], "https://"), l[1])
			}
			// The libraries' and material's licences (thread #62).
			if a.noticesURL == "" {
				a.noticesURL = thirdPartyNoticesURL()
			}
			ui.Text(c, i18n.T("Third-party licences")).TextColor(t.TextMuted)
			ui.Link(c, thirdPartyNotices, a.noticesURL)
		})
	})
	settingBlock(c, "about-notes", func() {
		caption(c, i18n.T("AudioSmith runs every model on this Mac. Each model keeps its own licence: see Settings → Models & network."))
		// 0.3: the icons' attribution (CC BY 4.0; NOTICE has it too).
		ui.Row(c.Key("about-icons")).Gap(px(t, 6)).AlignItems(ui.Center).Wrap().Children(func() {
			caption(c, i18n.T("Icons: Solar by 480 Design, under CC BY 4.0 (@iconify-json/solar %s); play and pause: Hugeicons, under the MIT licence (@iconify-json/hugeicons %s).", icons.SolarVersion, icons.HugeiconsVersion))
			ui.Link(c, i18n.T("Licence"), "https://creativecommons.org/licenses/by/4.0/").FontSize(px(t, fsCaption))
		})
	})
}

// diagnosticsSettings is Settings → Advanced's foot: the engine (its
// state, the models in memory, Details opens its page), the self-check
// and reporting a problem.
func (a *app) diagnosticsSettings(c *ui.Context) {
	a.revealAnchor(c, "diagnostics")
	eng := a.engineSummary()
	settingGroup(c, i18n.T("Diagnostics"), func() {
		state := map[engineState]string{engineStarting: i18n.T("Starting"), engineReady: i18n.T("Ready"), engineFailed: i18n.T("Stopped")}[eng.State]
		if eng.Version != "" {
			state += " · " + i18n.T("version %s", eng.Version)
		}
		model := i18n.T("No model loaded")
		if len(eng.Loaded) > 0 {
			model = i18n.T("In memory: %s", catalog.Builtin().DisplayName(eng.Loaded[0]))
		}
		settingRowNote(c, "diag-engine", i18n.T("Speech engine"), state+"\n"+model, func() {
			if ui.Button(c.Key("diag-engine-restart"), i18n.T("Restart engine")).Clicked() {
				a.restartEngine()
			}
			if ui.Button(c.Key("diag-engine-details"), i18n.T("Details")).Clicked() {
				a.navigate("/engine")
			}
		})
		settingRow(c, "diag-logs", i18n.T("Logs"), i18n.T("What AudioSmith and the engine wrote: open them when something fails without a clear error."), func() {
			if ui.Button(c.Key("diag-logs-open"), i18n.T("Show the logs")).Clicked() {
				a.navigate("/logs")
			}
		})

	})
	settingGroup(c, i18n.T("Self-check"), func() {
		settingRow(c, "about-check", i18n.T("Check AudioSmith"), i18n.T("The library, the engine, the folders, the network, the Keychain, ffmpeg and the permissions."), func() {
			if a.selfChecking {
				ui.Spinner(c)
			}
			if ui.Button(c.Key("about-check"), i18n.T("Run the check")).Disabled(a.selfChecking).Clicked() {
				a.runSelfCheck()
			}
		})
		if len(a.checks) > 0 {
			settingFoot(c, "about-checks", func() { a.selfCheckList(c) })
		}
	})
	a.reportSettings(c)
}

// selfCheckList is the self-check's results, a line each.
func (a *app) selfCheckList(c *ui.Context) {
	t := c.Theme()
	for _, ch := range a.checks {
		ui.Row(c.Key("check-" + ch.id)).Gap(px(t, 8)).AlignItems(ui.Start).Children(func() {
			mark, col := icons.CheckCircle, t.Success
			switch ch.status {
			case "warn":
				mark, col = icons.DangerTriangle, t.Warning
			case "fail":
				mark, col = icons.CloseCircle, t.Danger
			}
			ui.Icon(c, mark.Linear).Size(px(t, 14), px(t, 14)).TextColor(col).Shrink(0).Margin(px(t, 2), 0, 0)
			ui.Text(c, ch.title).Width(px(t, 150))
			ui.Text(c, ch.detail).FontSize(px(t, 12)).TextColor(t.TextMuted).Grow(1).MinWidth(0)
		})
	}
}

// reportSettings is Advanced's Report a problem group.
func (a *app) reportSettings(c *ui.Context) {
	settingGroup(c, i18n.T("Report a problem"), func() {
		settingRow(c, "about-copy", i18n.T("Copy diagnostics"), i18n.T("Versions, the machine, folders and the self-check: no text, audio or keys."), func() {
			if ui.Button(c.Key("about-copy"), i18n.T("Copy")).Clicked() {
				c.WriteClipboard(a.diagnosticsText())
				a.toast(i18n.T("Diagnostics copied"))
			}
		})
		settingRow(c, "about-bundle", i18n.T("Save a diagnostic bundle"), i18n.T("A ZIP of the diagnostics, the logs and the settings (without keys), to attach to a report."), func() {
			if ui.Button(c.Key("about-bundle"), i18n.T("Save…")).Clicked() {
				a.saveBundle()
			}
		})
		settingRow(c, "about-bug", i18n.T("Report a bug"), i18n.T("Opens a new issue on GitHub in your browser, with the diagnostics filled in. Nothing is sent until you submit it."), func() {
			if ui.Button(c.Key("about-bug"), i18n.T("Report…")).Clicked() {
				u := a.bugReportURL()
				if a.win == nil {
					c.Services().OpenURL(u)
				} else {
					go mygo.Shell.OpenExternal(u)
				}
			}
		})
	})
}

// betaBadge marks a beta version (About, thread #52).
func betaBadge(c *ui.Context) ui.Element {
	t := c.Theme()
	k := tok(t)
	return ui.Text(c, i18n.T("Beta")).FontSize(px(t, fsMicro)).FontWeight(600).TextColor(k.AccentInk).SingleLine().
		Padding(px(t, 1), px(t, 6)).Radius(px(t, 4)).Background(k.AccentSoft).Shrink(0)
}

// copyRedacted copies a log file line by line through logs.Redact.
func copyRedacted(w io.Writer, r io.Reader) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	bw := bufio.NewWriter(w)
	for sc.Scan() {
		bw.WriteString(logs.Redact(sc.Text()))
		bw.WriteByte('\n')
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return bw.Flush()
}
