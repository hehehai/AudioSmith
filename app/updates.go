package main

// Software updates: AudioSmith's own update UI on actnowkit's updates
// package (thread #50 had MyGo's updater plugin and its window until
// 0.4.0-beta.5; the ActNow project's licensing phase replaced them). MyGo
// only provides the updater's API: mygo.Updater.Check reads AudioSmith's
// update feed on the family website, https://actnow.dev/updates/audiosmith
// (thread #69), and Update.Install downloads the signed archive (or a
// delta), checks it against the public key `mygo build` links in from
// mygo.json's updates.publicKey (scripts/release-config.sh), and swaps the
// bundle (putting the old one back if that fails). actnowkit's
// updates.Manager runs the state machine over it — idle → checking →
// available → downloading → ready (restart to finish) or failed — and reads
// each version's notes in AudioSmith's language from actnow.dev's
// changelog API (English where a version has no translation).
//
// What the user sees (updates_view.go):
//
//   - nothing pops up: a check that finds a version shows "Update
//     available" at the foot of the sidebar (a dot on the rail's download
//     button);
//   - the update panel: the version, its notes, Download and Install, the
//     download's progress, Restart to Finish the Update; a failure says
//     AudioSmith is unchanged and offers Try Again;
//   - Settings → Diagnostics and about → Software updates: check now,
//     check automatically (once a day), install without asking, the beta
//     channel, the changelog's history.
//
// Builds without updates (no updates section: development builds, the
// ad-hoc builds of scripts/mac.sh, `go test`) never check:
// mygo.Updater.Enabled() is false in them and the Manager is Disabled.
// docs/release.md tells how releases are signed and published.
//
// Beta channel (thread #52): betas are prereleases (0.4.0-beta.5). Their
// files are in the feed's beta directory, …/updates/audiosmith/beta/, whose
// manifest names the newest beta or release. With Settings → Receive beta
// updates on, MyGo's updater (mygo.Updater.SetPrereleases, with
// mygo.json's updates.prereleases) reads it as well as the releases' and
// offers the newer; the changelog then lists betas too. The setting
// (settings.Updates.Beta) defaults to on in a beta copy, off in a release.
// Turning it off never goes back to an older version: a beta stays until a
// release newer than it is out.

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui/markdown"
	akup "github.com/hehehai/actnowkit/updates"

	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// updateSlug is AudioSmith on actnow.dev (feed, changelog API).
const updateSlug = "audiosmith"

// updateInterval is how often the automatic check runs.
const updateInterval = 24 * time.Hour

// updateManager is what AudioSmith uses of actnowkit's *updates.Manager.
type updateManager interface {
	State() akup.State
	Subscribe(fn func(akup.State)) (unsubscribe func())
	Check(ctx context.Context) akup.State
	Install(ctx context.Context, acceptPaid bool) akup.State
	Restart() error
	Dismiss()
	History(ctx context.Context) (*akup.History, error)
	AutoCheck(ctx context.Context, interval time.Duration, enabled func() bool)
}

// appUpdates is the updater as the window sees it.
type appUpdates struct {
	// live: the real updater (main), whose Manager connectUpdates makes
	// over mygo.Updater once the version is known. Tests set mgr instead.
	live bool
	mgr  updateManager
	// setPrereleases chooses the feed's channel (mygo.Updater.SetPrereleases;
	// a recorder in tests, nil: none).
	setPrereleases func(on bool)
	connected      bool

	// st is the manager's state, as last posted.
	st akup.State
	// panelOpen shows the update panel; userCheck: the running check was
	// asked for (its answer shows in the panel, or as a toast).
	panelOpen bool
	userCheck bool
	// autoTried is the version an automatic install was started for
	// (Install without asking), so a failure is not retried in a loop.
	autoTried string

	// The changelog's history (Settings → About).
	historyOpen bool
	hist        *akup.History
	histErr     string
	histLoading bool

	// md: the notes' Markdown views (their parse is kept between frames).
	md markdown.Options

	stop   context.CancelFunc
	autoOn atomic.Bool // "check automatically", read by the manager's loop
}

// useUpdates is the real updater; it runs in main, before App.Run.
func useUpdates() *appUpdates {
	return &appUpdates{live: true, setPrereleases: mygo.Updater.SetPrereleases}
}

// connectUpdates makes the update manager (the real one, once the version
// is known), sets the channel and follows the manager's state; it runs
// once the window is open (openWindow), and again in tests.
func (a *app) connectUpdates() {
	u := a.updates
	if u.live && u.mgr == nil {
		var client *http.Client
		if a.lib != nil {
			set := a.lib.settings
			client = updateHTTPClient(func() string { return set.Get().Models.Proxy })
			mygo.Updater.SetTransport(client.Transport)
		}
		u.mgr = akup.New(akup.Config{
			Updater: akup.Funcs{
				EnabledFunc: mygo.Updater.Enabled,
				CheckFunc: func(ctx context.Context) (*akup.Release, error) {
					up, err := mygo.Updater.Check(ctx)
					if err != nil || up == nil {
						return nil, err
					}
					return &akup.Release{Version: up.Version, Notes: up.Notes, Date: up.Date, Install: up.Install}, nil
				},
			},
			Current:  a.version,
			History:  &akup.HistoryClient{App: updateSlug, HTTPClient: client},
			Language: i18n.Locale,
			Beta:     func() bool { return betaUpdates(a.prefs, a.version) },
			Restart:  func() { a.post(func() { mygo.App.Relaunch() }) },
			Log:      func(s string) { log.Printf("updates: %s", s) },
		})
		a.keepUpdaterChoices()
	}
	if u.setPrereleases != nil {
		u.setPrereleases(betaUpdates(a.prefs, a.version))
	}
	if u.mgr == nil || u.connected {
		return
	}
	u.connected = true
	u.st = u.mgr.State()
	u.mgr.Subscribe(func(st akup.State) { a.post(func() { a.setUpdateState(st) }) })
	if a.win != nil && u.enabled() {
		ctx, cancel := context.WithCancel(context.Background())
		u.stop = cancel
		u.autoOn.Store(a.prefs.Updates.AutoCheckOn())
		// The first check a little after launch, not with the window's
		// first frames.
		go func() {
			select {
			case <-time.After(10 * time.Second):
				u.mgr.AutoCheck(ctx, updateInterval, u.autoOn.Load)
			case <-ctx.Done():
			}
		}()
	}
}

// closeUpdates ends the automatic checks (quit).
func (a *app) closeUpdates() {
	if a.updates.stop != nil {
		a.updates.stop()
		a.updates.stop = nil
	}
}

// keepUpdaterChoices carries the daily check and "install without asking"
// over from MyGo's updater plugin (its updater.json, up to 0.4.0-beta.5)
// the first time.
func (a *app) keepUpdaterChoices() {
	if a.prefs.Updates.AutoCheck != nil || a.dataHome == "" {
		return
	}
	data, err := os.ReadFile(filepath.Join(a.dataHome, "updater.json"))
	if err != nil {
		return
	}
	var old struct {
		AutomaticChecks    *bool `json:"automaticChecks"`
		AutomaticDownloads bool  `json:"automaticDownloads"`
	}
	if json.Unmarshal(data, &old) != nil || old.AutomaticChecks == nil {
		return
	}
	on, install := *old.AutomaticChecks, old.AutomaticDownloads
	a.updateSettings(func(s *settings.Settings) {
		s.Updates.AutoCheck = &on
		s.Updates.AutoInstall = install
	})
}

// enabled says whether this build can update itself.
func (u *appUpdates) enabled() bool { return u.mgr != nil && u.st.Phase != akup.Disabled }

// setUpdateState takes the manager's new state.
func (a *app) setUpdateState(st akup.State) {
	u := a.updates
	prev := u.st
	u.st = st
	if !st.CheckedAt.IsZero() && !st.CheckedAt.Equal(prev.CheckedAt) && st.Phase != akup.Failed {
		at := st.CheckedAt
		a.updateSettings(func(s *settings.Settings) { s.Updates.LastCheck = at })
	}
	switch st.Phase {
	case akup.Available:
		if u.userCheck {
			u.userCheck, u.panelOpen = false, true
			return
		}
		// Install without asking: a version the daily check found
		// downloads at once; the restart is the user's.
		if a.prefs.Updates.AutoInstall && st.Release != nil && u.autoTried != st.Release.Version {
			u.autoTried = st.Release.Version
			log.Printf("updates: installing %s without asking (Settings → Diagnostics and about)", st.Release.Version)
			a.installUpdate()
		}
	case akup.UpToDate:
		if u.userCheck {
			u.userCheck = false
			if !u.panelOpen {
				a.toast(i18n.T("AudioSmith is up to date (%s)", st.Current))
			}
		}
	case akup.Failed:
		if u.userCheck && st.Step == "check" && !u.panelOpen {
			a.toastWarn(i18n.T("Cannot check for updates: %s", updateErrText(st.Err)))
		}
		u.userCheck = false
	}
}

// checkForUpdates checks now, as the user asked (menu, palette, Settings):
// the panel opens with the answer.
func (a *app) checkForUpdates() {
	u := a.updates
	log.Print("updates: check asked")
	if u.mgr == nil {
		u.panelOpen = true
		return
	}
	switch u.st.Phase {
	case akup.Available, akup.Downloading, akup.Ready, akup.Disabled:
		u.panelOpen = true
		return
	case akup.Checking:
		u.userCheck, u.panelOpen = true, true
		return
	}
	u.userCheck, u.panelOpen = true, true
	mgr := u.mgr
	a.async(func() { mgr.Check(context.Background()) })
}

// installUpdate downloads and installs the available update.
func (a *app) installUpdate() {
	u := a.updates
	if u.mgr == nil || u.st.Phase != akup.Available {
		return
	}
	mgr := u.mgr
	a.async(func() { mgr.Install(context.Background(), false) })
}

// restartForUpdate relaunches into the installed update.
func (a *app) restartForUpdate() {
	if u := a.updates; u.mgr != nil && u.st.Phase == akup.Ready {
		if err := u.mgr.Restart(); err == nil {
			return
		}
	}
	if a.win != nil {
		mygo.App.Relaunch()
	}
}

// setAutoCheck changes whether updates are checked once a day; it is saved
// in the settings.
func (a *app) setAutoCheck(on bool) {
	a.updates.autoOn.Store(on)
	a.updateSettings(func(s *settings.Settings) { s.Updates.AutoCheck = &on })
}

// openHistory opens the changelog's history and reads it from actnow.dev.
func (a *app) openHistory() {
	u := a.updates
	u.historyOpen = true
	if u.mgr == nil || u.histLoading {
		return
	}
	u.histLoading, u.histErr = true, ""
	mgr := u.mgr
	a.async(func() {
		h, err := mgr.History(context.Background())
		a.post(func() {
			u.histLoading = false
			if err != nil {
				u.histErr = updateErrText(err)
				return
			}
			u.hist = h
		})
	})
}

// updateErrText is an error for a sentence ("" for none).
func updateErrText(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	for _, p := range []string{"mygo: ", "updates: "} {
		s = strings.TrimPrefix(s, p)
	}
	return s
}

// betaUpdates reports whether AudioSmith takes beta versions: as the user
// chose, else when this copy is a beta itself (someone who installed a
// beta wants the next one).
func betaUpdates(s settings.Settings, version string) bool {
	if b := s.Updates.Beta; b != nil {
		return *b
	}
	return isPrerelease(version)
}

// setBetaUpdates keeps the user's choice of channel and tells the updater.
func (a *app) setBetaUpdates(on bool) {
	a.updateSettings(func(s *settings.Settings) {
		v := on
		s.Updates.Beta = &v
	})
	if a.updates.setPrereleases != nil {
		a.updates.setPrereleases(on)
	}
}

// isPrerelease reports whether a version such as 0.4.0-beta.1 is a
// prerelease (semantic versioning, as MyGo's updater compares them).
func isPrerelease(version string) bool {
	version, _, _ = strings.Cut(version, "+")
	_, pre, _ := strings.Cut(version, "-")
	return pre != ""
}

// updateHTTPClient is the updater's HTTP client: through the proxy that
// Settings → Models names at the time of each request ("" automatic: the
// environment, then the macOS system proxy), with a bound on the wait for
// response headers; downloads themselves are bounded by their context.
func updateHTTPClient(mode func() string) *http.Client {
	c := hub.NewHTTPClient("", 30*time.Second)
	if t, ok := c.Transport.(*http.Transport); ok {
		t.Proxy = func(r *http.Request) (*url.URL, error) { return hub.ProxyFunc(mode())(r) }
	}
	return c
}
