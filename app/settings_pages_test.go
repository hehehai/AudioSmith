package main

import (
	"archive/zip"
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/actnowkit/mirror"
)

// stubRelaunch records restarts instead of restarting.
func stubRelaunch(t *testing.T) *int {
	t.Helper()
	n := 0
	saved := appRelaunch
	appRelaunch = func() { n++ }
	t.Cleanup(func() { appRelaunch = saved })
	return &n
}

func TestStoragePage(t *testing.T) {
	a, _, _ := newTestApp(t)
	tt := ui.NewTester(a.view, 1100, 2000)
	addTake(t, a, core.Take{ID: core.NewID(time.Now()), Text: "hi", Model: kokoro, Voice: "af_heart", SampleRate: 24000}, 1)
	os.MkdirAll(filepath.Join(a.cacheDir(), "chunks"), 0o755)
	os.WriteFile(filepath.Join(a.cacheDir(), "chunks", "x.wav"), make([]byte, 5000), 0o644)
	a.navigate("/settings/storage")
	tt.Frame()
	tt.Frame()
	if a.stReport == nil || !tt.HasText("Takes") || !tt.HasText("Caches") || !tt.HasText("Library database") {
		t.Fatalf("page %q", tt.Texts())
	}
	var takes, caches int64
	for _, c := range a.stReport.cats {
		switch c.id {
		case "takes":
			takes = c.bytes
		case "caches":
			caches = c.bytes
		}
	}
	if takes < 48000 || caches < 5000 {
		t.Errorf("takes %d caches %d", takes, caches)
	}
	if err := tt.Click("Clear"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, err := os.Stat(filepath.Join(a.cacheDir(), "chunks", "x.wav")); !os.IsNotExist(err) {
		t.Error("cache not cleared")
	}
	// The look, then every setting.
	a.updateSettings(func(s *settings.Settings) {
		s.Appearance.Theme, s.Appearance.Rail, s.Studio.Autoplay = "dark", true, false
	})
	tt.Frame()
	a.resetLook()
	tt.Frame()
	if p := a.settings.Get(); p.Appearance.Theme != "system" || p.Appearance.Rail || p.Studio.Autoplay {
		t.Errorf("after resetting the look %+v %+v", p.Appearance, p.Studio)
	}
	a.navigate("/settings/storage")
	tt.Frame()
	a.resetAsk = "settings" // its Reset button (the look's row has one too)
	tt.Frame()
	if err := tt.Click("Reset the settings"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if p := a.settings.Get(); !p.Studio.Autoplay || !p.Setup.Done {
		t.Errorf("after resetting the settings %+v %+v", p.Studio, p.Setup)
	}
}

func TestMoveDataFolder(t *testing.T) {
	relaunched := stubRelaunch(t)
	from := filepath.Join(t.TempDir(), "AudioSmith")
	a, _, tt := newTestAppIn(t, from)
	a.dataHome = from
	take := core.Take{ID: core.NewID(time.Now()), Text: "moved", Model: kokoro, Voice: "af_heart", SampleRate: 24000}
	addTake(t, a, take, 1)
	ctx := context.Background()
	if _, err := a.lib.svc.Library.RecordExport(ctx, core.Export{Kind: "take", Title: "x", Path: filepath.Join(from, "renders", "r", "book.wav")}); err != nil {
		t.Fatal(err)
	}
	yt := filepath.Join(from, "tools", "yt-dlp")
	if err := a.settings.Update(ctx, func(s *settings.Settings) { s.Dubbing.YtDlp = yt }); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(t.TempDir(), "Drive & Co")
	os.MkdirAll(to, 0o755)
	os.WriteFile(filepath.Join(to, "busy.txt"), []byte("x"), 0o644) // not empty: goes into to/AudioSmith
	savedChoose := chooseMoveDir
	chooseMoveDir = func(a *app, then func(string)) { then(to) }
	t.Cleanup(func() { chooseMoveDir = savedChoose })
	a.navigate("/settings/storage")
	tt.Frame()
	if err := tt.Click("Move"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	target := filepath.Join(to, "AudioSmith")
	if *relaunched != 1 {
		t.Fatalf("no restart after the copy (moving %v, err %q, toasts %q)", a.moving, a.moveErr, a.toasts)
	}
	if _, err := os.Stat(filepath.Join(target, "clips", take.ID+".wav")); err != nil {
		t.Fatalf("clip not copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "audiosmith.db")); !os.IsNotExist(err) {
		t.Error("the database is copied at the start, not while it is open")
	}
	// The app quits; the next start finishes the move.
	a.lib.close()
	dir, err := resolveDataDir(from)
	if err != nil || dir != target {
		t.Fatalf("resolved %s %v (location %+v)", dir, err, readLocation(from))
	}
	if loc := readLocation(from); loc.Previous != from || loc.Failed != "" {
		t.Errorf("location %+v", loc)
	}
	lib := openTestLibrary(t, target)
	got, err := lib.svc.Takes.Get(ctx, take.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lib.store.Takes().AudioPath(got)); err != nil {
		t.Errorf("the take's audio in the new folder: %v", err)
	}
	if s := lib.settings.Get(); s.Dubbing.YtDlp != filepath.Join(target, "tools", "yt-dlp") {
		t.Errorf("yt-dlp %s", s.Dubbing.YtDlp)
	}
	exps, _ := lib.store.Exports().List(ctx, "", 10)
	if len(exps) != 1 || !strings.HasPrefix(exps[0].Path, target) {
		t.Errorf("exports %+v", exps)
	}
	// Starting again changes nothing.
	if d, err := resolveDataDir(from); err != nil || d != target {
		t.Errorf("second start %s %v", d, err)
	}
	// A missing folder (its drive unplugged) is an error, not a new library.
	lib.close()
	os.RemoveAll(target)
	if _, err := resolveDataDir(from); err == nil {
		t.Error("a missing data folder opened")
	}
}

func TestMoveRefusals(t *testing.T) {
	dir := t.TempDir()
	if err := checkMoveTarget(dir, dir); err == nil {
		t.Error("same folder")
	}
	if err := checkMoveTarget(dir, filepath.Join(dir, "sub")); err == nil {
		t.Error("inside")
	}
	full := t.TempDir()
	os.WriteFile(filepath.Join(full, "x"), nil, 0o644)
	if err := checkMoveTarget(dir, full); err == nil {
		t.Error("not empty")
	}
	if err := checkMoveTarget(dir, t.TempDir()); err != nil {
		t.Error(err)
	}
	// A failed finish rolls back: the copy goes, the old folder stays.
	from, to := t.TempDir(), filepath.Join(t.TempDir(), "AudioSmith")
	os.MkdirAll(to, 0o755)
	os.WriteFile(filepath.Join(from, "audiosmith.db"), []byte("not a database"), 0o644)
	writeJSON(filepath.Join(from, moveFile), pendingMove{From: from, To: to})
	d, err := resolveDataDir(from)
	if err != nil || d != from {
		t.Fatalf("after a failed move %s %v", d, err)
	}
	if _, err := os.Stat(to); !os.IsNotExist(err) {
		t.Error("the copy stayed")
	}
	if loc := readLocation(from); loc.Failed == "" {
		t.Error("the failure is not recorded")
	}
}

func TestEraseEverything(t *testing.T) {
	relaunched := stubRelaunch(t)
	home := filepath.Join(t.TempDir(), "AudioSmith")
	a, _, _ := newTestAppIn(t, home)
	tt := ui.NewTester(a.view, 1100, 2000)
	a.dataHome = home
	a.logDir = t.TempDir()
	os.WriteFile(filepath.Join(a.logDir, "audiosmith.log"), []byte("log"), 0o644)
	addTake(t, a, core.Take{ID: core.NewID(time.Now()), Text: "bye", Model: kokoro, Voice: "af_heart", SampleRate: 24000}, 1)
	a.navigate("/settings/storage")
	tt.Frame()
	if err := tt.Click("Erase"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Erase and restart"); err != nil {
		t.Fatal(err)
	}
	if *relaunched != 0 {
		t.Fatal("erased without typing DELETE")
	}
	a.resetText = "DELETE"
	tt.Frame()
	if err := tt.Click("Erase and restart"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if *relaunched != 1 {
		t.Fatalf("no restart %q", a.toasts)
	}
	a.lib.close()
	if d, err := resolveDataDir(home); err != nil || d != home {
		t.Fatalf("%s %v", d, err)
	}
	if ents, _ := os.ReadDir(home); len(ents) != 0 {
		t.Errorf("left %v", ents)
	}
	if _, err := os.Stat(filepath.Join(a.logDir, "audiosmith.log")); !os.IsNotExist(err) {
		t.Error("logs kept")
	}
}

func TestAboutPage(t *testing.T) {
	a, _, _ := newTestApp(t)
	tt := ui.NewTester(a.view, 1100, 2000)
	savedSys, savedReach := readSysInfo, reach
	t.Cleanup(func() { readSysInfo, reach = savedSys, savedReach })
	readSysInfo = func() sysInfo {
		return sysInfo{OS: "macOS 15.4", Chip: "Apple M4", Memory: 16 << 30, Cores: 10, Arch: "arm64"}
	}
	reach = func(_ *http.Client, host string) netCheck {
		return netCheck{host: host, ok: true, latency: 42 * time.Millisecond}
	}
	a.updateSettings(func(s *settings.Settings) {
		s.LLM.Providers = []settings.LLMProvider{{ID: "x", Headers: map[string]string{"Authorization": "Bearer secret-123"}}}
	})
	a.navigate("/settings/about")
	tt.Frame()
	tt.Frame()
	if !tt.HasText("Apple M4") || !tt.HasText("16 GB") {
		t.Fatalf("about %q", tt.Texts())
	}
	// The licence and the source code (AGPL-3.0).
	if !tt.HasText("GNU AGPL-3.0") || !tt.HasText("Source code") || !tt.HasText("github.com/hehehai/AudioSmith") {
		t.Fatalf("about %q", tt.Texts())
	}
	// The website's pages (thread #69): the source stays on GitHub.
	for _, link := range []string{"actnow.dev/audiosmith", "actnow.dev/download/audiosmith", "actnow.dev/audiosmith/changelog"} {
		if !tt.HasText(link) {
			t.Fatalf("about: no %s in %q", link, tt.Texts())
		}
	}
	a.navigate("/settings/advanced") // the self-check is in Advanced's diagnostics
	tt.SetSize(1100, 2600)           // the whole section, its foot included
	tt.Frame()
	tt.Frame()
	if err := tt.Click("Run the check"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.checks) < 6 || !tt.HasText("Library database") {
		t.Fatalf("checks %+v", a.checks)
	}
	for _, c := range a.checks {
		if c.status == "fail" {
			t.Errorf("check %+v", c)
		}
	}
	diag := a.diagnosticsText()
	if !strings.Contains(diag, "Apple M4") || strings.Contains(diag, "secret-123") {
		t.Errorf("diagnostics:\n%s", diag)
	}
	u, err := url.Parse(a.bugReportURL())
	if err != nil || u.Host != "github.com" || !strings.Contains(u.Query().Get("body"), "Apple M4") {
		t.Errorf("bug URL %v %v", u, err)
	}
	bundleDir = t.TempDir()
	t.Cleanup(func() { bundleDir = "" })
	a.logDir = t.TempDir()
	os.WriteFile(filepath.Join(a.logDir, "audiosmith.log"), []byte("a log line"), 0o644)
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(bundleDir)
	if len(ents) != 1 {
		t.Fatalf("bundle %v", ents)
	}
	zr, err := zip.OpenReader(filepath.Join(bundleDir, ents[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
		if f.Name == "settings.json" {
			r, _ := f.Open()
			buf := make([]byte, 1<<20)
			n, _ := r.Read(buf)
			r.Close()
			if strings.Contains(string(buf[:n]), "secret-123") {
				t.Error("the bundle has a key")
			}
		}
	}
	if !names["diagnostics.txt"] || !names["settings.json"] || !names["logs/audiosmith.log"] {
		t.Errorf("bundle files %v", names)
	}
}

func TestNetworkUsagePrivacyAppearance(t *testing.T) {
	a, _, _ := newTestApp(t)
	tt := ui.NewTester(a.view, 1100, 2000)
	savedReach := reach
	t.Cleanup(func() { reach = savedReach })
	reach = func(_ *http.Client, host string) netCheck {
		if strings.Contains(host, "github") {
			return netCheck{host: host, err: "timeout"}
		}
		return netCheck{host: host, ok: true, latency: 80 * time.Millisecond}
	}
	a.navigate("/settings/network")
	tt.Frame()
	if !tt.HasText("Download from") || !tt.HasText("Proxy") || !tt.HasText(mirror.TextSourceAuto.EN) {
		t.Fatalf("network %q", tt.Texts())
	}
	if err := tt.Click("Test"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.netChecks) != 4 || !tt.HasText("80 ms") || !tt.HasText("timeout") || !tt.HasText("mirror.actnow.dev") {
		t.Errorf("checks %+v", a.netChecks)
	}
	// It is a part of Models & network now, under the models.
	if a.shownPath() != "/settings/models" || !tt.HasText("Fast English voices") {
		t.Errorf("models and network %s %q", a.shownPath(), tt.Texts())
	}

	addTake(t, a, core.Take{ID: core.NewID(time.Now()), Text: "x", Model: kokoro, Voice: "af_heart", Language: "en", SampleRate: 24000, AudioSeconds: 65, GenSeconds: 13}, 1)
	a.navigate("/settings/usage")
	tt.Frame()
	tt.Frame()
	if a.usage == nil || a.usage.Takes != 1 || !tt.HasText("Takes generated") || !tt.HasText("Kokoro 82M") {
		t.Errorf("usage %+v %q", a.usage, tt.Texts())
	}

	a.navigate("/settings/privacy")
	tt.Frame()
	if !tt.HasText("No analytics") || !tt.HasText("Keep at most") {
		t.Fatalf("privacy %q", tt.Texts())
	}
	if err := tt.Click("Keep text on this Mac"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.settings.Get().Privacy.LocalOnlyLLM {
		t.Error("local-only not saved")
	}

	a.navigate("/settings/appearance")
	tt.Frame()
	if err := tt.Click("Icons only"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.settings.Get().Appearance.Rail {
		t.Error("rail not saved")
	}
}
