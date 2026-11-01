package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/actnowkit/family"
	akup "github.com/hehehai/actnowkit/updates"

	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// fakeFeed is MyGo's updater in tests: the release a check finds, how its
// install goes, and what AudioSmith asked of it.
type fakeFeed struct {
	mu       sync.Mutex
	enabled  bool
	release  *akup.Release
	checkErr error
	checks   int
	installs int
	// during runs inside the install, at half the download (the test
	// looks at the window then); installErr fails it.
	during     func()
	installErr error
}

func (f *fakeFeed) funcs() akup.Funcs {
	return akup.Funcs{
		EnabledFunc: func() bool { return f.enabled },
		CheckFunc: func(context.Context) (*akup.Release, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.checks++
			return f.release, f.checkErr
		},
	}
}

// offer makes the feed offer version, with English notes from the
// manifest; its install reports progress and calls during.
func (f *fakeFeed) offer(version string) {
	f.release = &akup.Release{Version: version, Notes: "### Added\n\n- From the manifest", Date: time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC),
		Install: func(ctx context.Context, progress func(done, total int64)) error {
			f.installs++
			progress(0, 24<<20)
			time.Sleep(120 * time.Millisecond) // the manager passes ~10 a second on
			progress(12<<20, 24<<20)
			if f.during != nil {
				f.during()
			}
			if f.installErr != nil {
				return f.installErr
			}
			progress(24<<20, 24<<20)
			return nil
		}}
}

// changelogTransport answers /api/changelog/audiosmith like actnow.dev,
// without a network: 0.4.0 in both languages, 0.4.0-beta.6 (beta) in
// English only, 0.4.0-beta.5; betas only when asked for.
type changelogTransport struct {
	mu    sync.Mutex
	asked []string // the query of each request
	down  bool
}

func (c *changelogTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.asked = append(c.asked, r.URL.RawQuery)
	down := c.down
	c.mu.Unlock()
	if down || r.URL.Path != "/api/changelog/audiosmith" {
		return nil, errors.New("dial tcp: no route to host")
	}
	zh := r.URL.Query().Get("locale") == "zh-Hans"
	beta := r.URL.Query().Get("channel") == "beta"
	entry := func(v, channel, date, en, cn string) map[string]any {
		notes, loc := en, "en"
		if zh && cn != "" {
			notes, loc = cn, "zh-Hans"
		}
		return map[string]any{"version": v, "channel": channel, "date": date, "notes": notes, "locale": loc, "requiresPaidLicence": false}
	}
	entries := []any{
		entry("0.4.0", "stable", "2026-10-30", "### Added\n\n- **Audiobooks** with a cast of voices\n- Dictation's sounds to choose\n\n### Fixed\n\n- Designed voices no longer say leftover reference text",
			"### 新增\n\n- 有多个角色的**有声书**\n- 听写的提示音可以自己选\n\n### 修复\n\n- 设计的声音不再念出参考文本的残留部分"),
	}
	if beta {
		entries = append(entries, entry("0.4.0-beta.6", "beta", "2026-10-25", "### Fixed\n\n- Exports keep the chosen sample rate", ""))
	}
	entries = append(entries, entry("0.4.0-beta.5", "beta", "2026-10-19", "### Added\n\n- The first beta on actnow.dev", "### 新增\n\n- 第一个在 actnow.dev 发布的测试版"))
	body, _ := json.Marshal(map[string]any{"app": "audiosmith", "locale": map[bool]string{true: "zh-Hans", false: "en"}[zh], "paidSince": nil, "entries": entries})
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body))), Request: r}, nil
}

type updatesEnv struct {
	a         *app
	tt        *ui.Tester
	feed      *fakeFeed
	site      *changelogTransport
	restarted int
	channel   []bool // each SetPrereleases
}

// newUpdatesApp is a test app at version with the in-app updater over a
// fake feed and changelog, on Settings → About.
func newUpdatesApp(t *testing.T, version string, enabled bool) *updatesEnv {
	t.Helper()
	a, _, _ := newTestApp(t)
	e := &updatesEnv{a: a, feed: &fakeFeed{enabled: enabled}, site: &changelogTransport{}}
	a.version = version
	a.updates = &appUpdates{setPrereleases: func(on bool) { e.channel = append(e.channel, on) }}
	a.updates.mgr = akup.New(akup.Config{
		Updater: e.feed.funcs(), Current: version,
		History:  &akup.HistoryClient{App: "audiosmith", HTTPClient: &http.Client{Transport: e.site}},
		Language: i18n.Locale,
		Beta:     func() bool { return betaUpdates(a.prefs, a.version) },
		Restart:  func() { e.restarted++ },
	})
	a.connectUpdates()
	e.tt = ui.NewTester(a.view, 1100, 900)
	pinTester(e.tt)
	return e
}

func (e *updatesEnv) has(t *testing.T, texts ...string) {
	t.Helper()
	for _, s := range texts {
		if !e.tt.HasText(s) {
			t.Errorf("no %q in %q", s, e.tt.Texts())
		}
	}
}

func (e *updatesEnv) click(t *testing.T, label string) {
	t.Helper()
	if err := e.tt.Click(label); err != nil {
		t.Fatalf("%v: %q", err, e.tt.Texts())
	}
	e.tt.Frame()
}

// TestUpdateFlow: the daily check only shows "Update available" at the
// sidebar's foot (no window); the panel has the version, its notes from
// actnow.dev, the download's progress, and the restart, which relaunches.
func TestUpdateFlow(t *testing.T) {
	e := newUpdatesApp(t, "0.4.0-beta.5", true)
	e.feed.offer("0.4.0")
	e.a.updates.mgr.Check(context.Background()) // as AutoCheck does
	e.tt.Frame()
	e.has(t, "Update available")
	if e.a.updates.panelOpen || e.tt.HasText("Download and Install") {
		t.Fatal("an automatic check opened the panel")
	}
	e.click(t, "Update available")
	e.has(t, "AudioSmith 0.4.0 is available", "You have 0.4.0-beta.5 · Oct 30, 2026", "Audiobooks", "Download and Install", "Later")
	if e.tt.HasText("From the manifest") {
		t.Error("the manifest's notes, not actnow.dev's")
	}
	// Half way, the panel and the sidebar show the download.
	var during []string
	e.feed.during = func() {
		e.tt.Frame()
		during = e.tt.Texts()
	}
	e.a.installUpdate() // as Download and Install does, outside a frame
	e.tt.Frame()
	for _, s := range []string{"Downloading AudioSmith 0.4.0", "Downloading… 50% · " + humanBytes(12<<20) + " / " + humanBytes(24<<20), "Updating… 50%", "Hide"} {
		if !slices.Contains(during, s) {
			t.Errorf("no %q while downloading: %q", s, during)
		}
	}
	e.has(t, "AudioSmith 0.4.0 is installed", "The new version runs once AudioSmith restarts.", "Restart to Finish the Update", "Restart to update")
	e.click(t, "Restart to Finish the Update")
	if e.restarted != 1 {
		t.Errorf("restarted %d", e.restarted)
	}
	// The beta copy asked for betas too, in English.
	if q := e.site.asked; len(q) == 0 || !strings.Contains(q[0], "channel=beta") || !strings.Contains(q[0], "locale=en") {
		t.Errorf("changelog asked %q", q)
	}
}

// TestUpdateFailed: a failed download says AudioSmith is unchanged and
// offers Try Again, which checks again.
func TestUpdateFailed(t *testing.T) {
	e := newUpdatesApp(t, "0.4.0", true)
	e.feed.offer("0.4.1")
	e.feed.installErr = errors.New("mygo: signature does not match")
	e.a.navigate("/settings/about")
	e.tt.Frame()
	e.click(t, "Check for Updates")
	e.click(t, "Download and Install")
	e.has(t, "The update did not install: signature does not match. AudioSmith is unchanged.", "Try again", "Close")
	if e.tt.HasText("Restart to update") {
		t.Error("a failed install asks for a restart")
	}
	e.feed.installErr = nil
	e.click(t, "Try again")
	if e.feed.checks != 2 {
		t.Errorf("checks %d", e.feed.checks)
	}
	e.has(t, "Download and Install")

	// A check that fails, asked for: the panel says so.
	e2 := newUpdatesApp(t, "0.4.0", true)
	e2.feed.checkErr = errors.New("GET https://actnow.dev/updates/audiosmith/update-darwin-arm64.json: 503")
	e2.a.checkForUpdates()
	e2.tt.Frame()
	e2.has(t, "Cannot check for updates: GET https://actnow.dev/updates/audiosmith/update-darwin-arm64.json: 503")
}

// TestUpdateNotesLanguage: in Chinese the notes are actnow.dev's Chinese
// ones; a version without them shows the English, marked; without the
// changelog API the manifest's notes show.
func TestUpdateNotesLanguage(t *testing.T) {
	defer i18n.SetLocale("en")
	e := newUpdatesApp(t, "0.4.0-beta.5", true)
	e.a.updateSettings(func(s *settings.Settings) { s.General.Language = "zh-Hans" })
	e.feed.offer("0.4.0")
	e.a.checkForUpdates()
	e.tt.Frame()
	e.has(t, "AudioSmith 0.4.0 已发布", "下载并安装")
	if !strings.Contains(strings.Join(e.tt.Texts(), " "), "有声书") || e.tt.HasText("这个版本暂无中文说明，以下为英文。") {
		t.Errorf("not the Chinese notes: %q", e.tt.Texts())
	}
	e.a.updates.panelOpen = false
	e.a.updates.mgr.Dismiss()
	e.feed.offer("0.4.0-beta.6")
	e.a.checkForUpdates()
	e.tt.Frame()
	e.has(t, "这个版本暂无中文说明，以下为英文。", "Exports keep the chosen sample rate")

	e.site.down = true
	e.a.updates.panelOpen = false
	e.a.updates.mgr.Dismiss()
	e.a.checkForUpdates()
	e.tt.Frame()
	e.has(t, "From the manifest", "这个版本暂无中文说明，以下为英文。")
}

// TestUpdateInstallWithoutAsking: with Install without asking on, a version
// the daily check found installs at once; the restart is the user's.
func TestUpdateInstallWithoutAsking(t *testing.T) {
	e := newUpdatesApp(t, "0.4.0", true)
	e.a.updateSettings(func(s *settings.Settings) { s.Updates.AutoInstall = true })
	e.feed.offer("0.4.1")
	e.a.updates.mgr.Check(context.Background())
	e.tt.Frame()
	if e.feed.installs != 1 || e.a.updates.st.Phase != akup.Ready {
		t.Fatalf("installs %d, phase %s", e.feed.installs, e.a.updates.st.Phase)
	}
	e.has(t, "Restart to update")
	if e.restarted != 0 {
		t.Error("restarted without asking")
	}
	// A failure is not retried in a loop.
	e2 := newUpdatesApp(t, "0.4.0", true)
	e2.a.updateSettings(func(s *settings.Settings) { s.Updates.AutoInstall = true })
	e2.feed.offer("0.4.1")
	e2.feed.installErr = errors.New("disk full")
	e2.a.updates.mgr.Check(context.Background())
	e2.a.updates.mgr.Check(context.Background())
	if e2.feed.installs != 1 {
		t.Errorf("installs %d", e2.feed.installs)
	}
}

// TestSoftwareUpdateSettings: Settings → Diagnostics and about → Software
// updates: a build without updates says so with its switches off; a
// release shows the last check, checks now, and keeps its choices.
func TestSoftwareUpdateSettings(t *testing.T) {
	e := newUpdatesApp(t, "0.4.0", false)
	e.tt.SetSize(1100, 2400)
	e.a.navigate("/settings/about")
	e.tt.Frame()
	e.tt.Frame()
	e.has(t, "Software updates", "This copy was built on this Mac and can't update itself, so the options below are off. Copies downloaded from actnow.dev update themselves.", "Download the latest version from actnow.dev")
	if e.tt.HasText("Development builds and copies built on this Mac don't update themselves; copies from actnow.dev do.") {
		t.Error("the reason is said twice")
	}
	e.click(t, "Check for updates automatically")
	if e.a.prefs.Updates.AutoCheck != nil {
		t.Error("a disabled switch turned automatic checks off")
	}
	e.click(t, "Check for Updates")
	e.has(t, "Download the latest version from actnow.dev")
	e.click(t, "OK")

	e = newUpdatesApp(t, "0.4.0", true)
	e.tt.SetSize(1100, 2400)
	e.a.updateSettings(func(s *settings.Settings) { s.Updates.LastCheck = time.Date(2026, 10, 16, 9, 30, 0, 0, time.Local) })
	e.a.navigate("/settings/about")
	e.tt.Frame()
	e.tt.Frame()
	e.has(t, "Last checked Oct 16 09:30.", "Changelog", "Show Changelog")
	e.click(t, "Check for Updates")
	e.has(t, "AudioSmith is up to date (0.4.0)")
	if e.feed.checks != 1 {
		t.Errorf("checks %d", e.feed.checks)
	}
	e.click(t, "OK")
	e.click(t, "Install without asking")
	if !e.a.prefs.Updates.AutoInstall {
		t.Error("installing without asking not turned on")
	}
	e.click(t, "Check for updates automatically")
	if e.a.prefs.Updates.AutoCheckOn() || e.a.updates.autoOn.Load() {
		t.Error("automatic checks not turned off")
	}
	if s, err := settings.Migrate(mustJSON(t, e.a.prefs)); err != nil || s.Updates.AutoCheckOn() || !s.Updates.AutoInstall {
		t.Errorf("not kept: %+v %v", s.Updates, err)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestKeepUpdaterChoices: the daily check and installing without asking,
// chosen in MyGo's update window (updater.json), carry over once.
func TestKeepUpdaterChoices(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.dataHome = t.TempDir()
	if err := os.WriteFile(filepath.Join(a.dataHome, "updater.json"), []byte(`{"automaticChecks":false,"automaticDownloads":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	a.keepUpdaterChoices()
	if u := a.prefs.Updates; u.AutoCheckOn() || !u.AutoInstall {
		t.Fatalf("carried over %+v", u)
	}
	// Once: a later choice stays.
	on := true
	a.updateSettings(func(s *settings.Settings) { s.Updates.AutoCheck = &on })
	a.keepUpdaterChoices()
	if !a.prefs.Updates.AutoCheckOn() {
		t.Error("carried over again")
	}
}

// TestChangelogHistory: Settings → About → Show Changelog… lists every
// version in AudioSmith's language, betas marked; English ones marked in
// Chinese; an unreachable site says so.
func TestChangelogHistory(t *testing.T) {
	defer i18n.SetLocale("en")
	e := newUpdatesApp(t, "0.4.0-beta.5", true)
	e.tt.SetSize(1100, 1600)
	e.a.navigate("/settings/about")
	e.tt.Frame()
	e.click(t, "Show Changelog")
	e.has(t, "Changelog", "AudioSmith · English", "0.4.0", "0.4.0-beta.6", "0.4.0-beta.5", "This version", "Exports keep the chosen sample rate", "On actnow.dev")
	e.click(t, "Close")
	e.a.updateSettings(func(s *settings.Settings) { s.General.Language = "zh-Hans" })
	e.a.openHistory()
	e.tt.Frame()
	e.has(t, "更新日志", "第一个在 actnow.dev 发布的测试版", "这个版本暂无中文说明，以下为英文。")

	e.site.down = true
	e.a.updates.hist = nil
	e.a.openHistory()
	e.tt.Frame()
	if !strings.Contains(strings.Join(e.tt.Texts(), " "), "无法读取更新日志") {
		t.Errorf("no error: %q", e.tt.Texts())
	}
}

func TestCheckForUpdatesCommand(t *testing.T) {
	e := newUpdatesApp(t, "0.4.0", true)
	for _, c := range e.a.commands() {
		if c.id == "updates" {
			c.run()
		}
	}
	if e.feed.checks != 1 || !e.a.updates.panelOpen {
		t.Fatalf("palette: %d checks, panel %v", e.feed.checks, e.a.updates.panelOpen)
	}
}

// TestUpdateTextsMatchKit: the updater's texts say, in English and in
// Chinese, what actnowkit's reference texts say (the family's apps word
// them alike).
func TestUpdateTextsMatchKit(t *testing.T) {
	defer i18n.SetLocale("en")
	msg := func(s string) string {
		return strings.NewReplacer("{app}", "AudioSmith", "{version}", "%s", "{percent}", "%s", "{error}", "%s").Replace(s)
	}
	for _, tx := range []family.Text{
		akup.TextUpdateAvailable, akup.TextNewVersion, akup.TextInstall, akup.TextDownloading, akup.TextRestart,
		akup.TextLater, akup.TextUpToDate, akup.TextChecking, akup.TextFailed, akup.TextCheckFailed,
		akup.TextChangelog, akup.TextAutoCheck, akup.TextNotesInEnglish,
	} {
		en := msg(tx.EN)
		if tx == akup.TextNotesInEnglish {
			en = "These notes are not translated yet; here they are in English."
		}
		i18n.SetLocale("zh-Hans")
		zh := i18n.T(en)
		if zh == en {
			t.Errorf("%q: AudioSmith has no such message (or no translation)", en)
			continue
		}
		if want := msg(tx.ZH); strings.NewReplacer("“", `"`, "”", `"`).Replace(zh) != want {
			t.Errorf("%q: zh %q, the kit says %q", en, zh, want)
		}
	}
}

// The channel (thread #52): betas are on in a beta copy and off in a
// release until the user chooses.
func TestBetaUpdatesDefault(t *testing.T) {
	on, off := true, false
	for _, c := range []struct {
		version string
		beta    *bool
		want    bool
	}{
		{"0.4.0-beta.1", nil, true},
		{"0.4.0-rc.2+build.7", nil, true},
		{"0.4.0", nil, false},
		{"0.4.0+build-7", nil, false},
		{"", nil, false}, // development builds
		{"0.4.0-beta.1", &off, false},
		{"0.4.0", &on, true},
	} {
		var s settings.Settings
		s.Updates.Beta = c.beta
		if got := betaUpdates(s, c.version); got != c.want {
			t.Errorf("betaUpdates(%s, %v) = %t", c.version, c.beta, got)
		}
	}
	// The setting survives a save: nil stays nil (the default follows the
	// copy), a choice stays.
	data, _ := json.Marshal(settings.Defaults())
	if s, err := settings.Migrate(data); err != nil || s.Updates.Beta != nil {
		t.Errorf("defaults: %+v, %v", s.Updates, err)
	}
	s := settings.Defaults()
	s.Updates.Beta = &off
	data, _ = json.Marshal(s)
	if s, err := settings.Migrate(data); err != nil || s.Updates.Beta == nil || *s.Updates.Beta {
		t.Errorf("a choice: %+v, %v", s.Updates, err)
	}
}

func TestBetaUpdatesSettings(t *testing.T) {
	beta := "Betas get new features sooner but may be less stable. Turned off, AudioSmith only updates to releases."
	stays := "Betas get new features sooner but may be less stable. This copy is a beta: turned off, AudioSmith never goes back to an older version, and stays on this one until a release newer than it is out."

	// A beta copy takes betas until the user turns them off.
	e := newUpdatesApp(t, "0.4.0-beta.1", true)
	e.tt.SetSize(1100, 2400)
	if !slices.Equal(e.channel, []bool{true}) {
		t.Fatalf("a beta copy: SetPrereleases %v", e.channel)
	}
	e.a.navigate("/settings/about")
	e.tt.Frame()
	e.tt.Frame()
	e.has(t, "Receive beta updates", stays)
	if !slices.Contains(e.tt.Texts(), "0.4.0-beta.1") || !slices.Contains(e.tt.Texts(), "Beta") {
		t.Errorf("About has no Beta badge: %q", e.tt.Texts())
	}
	e.click(t, "Beta updates")
	if e.channel[len(e.channel)-1] {
		t.Errorf("turned off: SetPrereleases %v", e.channel)
	}
	if b := e.a.settings.Get().Updates.Beta; b == nil || *b {
		t.Errorf("saved %v", b)
	}
	// The next launch keeps the choice.
	e.a.connectUpdates()
	if e.channel[len(e.channel)-1] {
		t.Error("the choice was not kept")
	}

	// A release copy: off until chosen, no badge, no note about staying.
	e2 := newUpdatesApp(t, "0.4.0", true)
	e2.tt.SetSize(1100, 2400)
	if !slices.Equal(e2.channel, []bool{false}) {
		t.Fatalf("a release copy: SetPrereleases %v", e2.channel)
	}
	e2.a.navigate("/settings/about")
	e2.tt.Frame()
	e2.tt.Frame()
	if !e2.tt.HasText(beta) || e2.tt.HasText(stays) || slices.Contains(e2.tt.Texts(), "Beta") {
		t.Errorf("a release copy: %q", e2.tt.Texts())
	}
	e2.click(t, "Beta updates")
	if !e2.channel[len(e2.channel)-1] {
		t.Errorf("turned on: SetPrereleases %v", e2.channel)
	}

	// A copy that can't update itself shows the switch off and disabled.
	e3 := newUpdatesApp(t, "0.4.0-beta.1", false)
	e3.tt.SetSize(1100, 2400)
	e3.a.navigate("/settings/about")
	e3.tt.Frame()
	e3.tt.Frame()
	e3.tt.Click("Beta updates")
	e3.tt.Frame()
	if len(e3.channel) != 1 || e3.a.prefs.Updates.Beta != nil {
		t.Errorf("a disabled switch changed the channel: %v, %v", e3.channel, e3.a.prefs.Updates.Beta)
	}
}

// mygo build needs CHANGELOG.md's section of the version it builds for
// the release notes in the update's manifest ("## 0.3.0 — date"), and an
// updates section, when there is one, with a real public key and our
// repository. CHANGELOG.zh-Hans.md has the same versions in Chinese (the
// site checks them at a release: changelog_mismatch).
func TestReleaseConfig(t *testing.T) {
	var cfg struct {
		Identifier string `json:"identifier"`
		Version    string `json:"version"`
		Updates    *struct {
			PublicKey string `json:"publicKey"`
			GitHub    string `json:"github"`
		} `json:"updates"`
	}
	data, err := os.ReadFile("../mygo.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Identifier != "dev.hehehai.audiosmith" {
		t.Errorf("identifier %q: keep it once released", cfg.Identifier)
	}
	// A release (0.4.0) or a beta (0.4.0-beta.1, a GitHub prerelease).
	if !regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`).MatchString(cfg.Version) {
		t.Errorf("version %q", cfg.Version)
	}
	log, err := os.ReadFile("../CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	// The heading's first word is the version, exactly: "## 0.4.0-beta.1"
	// is not 0.4.0's section (MyGo's update.ReleaseNotes).
	if !regexp.MustCompile(`(?m)^## \[?` + regexp.QuoteMeta(cfg.Version) + `(\]|\s|$)`).Match(log) {
		t.Errorf("CHANGELOG.md has no \"## %s\" section, which mygo build makes the update's release notes", cfg.Version)
	}
	zh, err := os.ReadFile("../CHANGELOG.zh-Hans.md")
	if err != nil {
		t.Fatal(err)
	}
	if en, cn := changelogVersions(string(log)), changelogVersions(string(zh)); !slices.Equal(en, cn) {
		t.Errorf("CHANGELOG.zh-Hans.md's versions %q, CHANGELOG.md's %q", cn, en)
	}
	if u := cfg.Updates; u != nil {
		if key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(u.PublicKey)); err != nil || len(key) != 32 {
			t.Errorf("updates.publicKey is not mygo-update.pub (an Ed25519 public key)")
		}
		if u.GitHub != "hehehai/AudioSmith" {
			t.Errorf("updates.github %q", u.GitHub)
		}
	}
}

// changelogVersions are the "## <version>" headings of a changelog, in
// order (Unreleased too).
func changelogVersions(s string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`(?m)^## \[?([^\]\s]+)`).FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}
