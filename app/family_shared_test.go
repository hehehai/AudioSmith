package main

// The shared settings in the app (family_shared.go): another app's
// language, proxy and download source reach AudioSmith's appliers; the
// scope lines and the "uses a different" line show only with another
// release app; the family's model folder is offered by "Move models…",
// which moves AudioSmith's models and then follows it; the environment
// still wins where the spec says so.

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/family"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/actnowkit/models"
	"github.com/hehehai/actnowkit/registry"
	famset "github.com/hehehai/actnowkit/settings"
)

// useShared connects the shared settings of tf's app as connectFamily
// and startFamily do, and waits for the first-run merge.
func useShared(t *testing.T, tf *sharedFamily) {
	t.Helper()
	tf.a.connectShared()
	tf.a.startShared()
	tf.a.shared.Wait()
	tf.a.showFamily(readFamilyView(tf.fam, nil, modelCache(tf.a.prefs)))
}

// familySet is another app (Dicta) changing a shared setting.
func (tf *sharedFamily) familySet(t *testing.T, key, value string) {
	t.Helper()
	if _, err := famset.Set(context.Background(), tf.Family, dictaID, key, value); err != nil {
		t.Fatal(err)
	}
}

// adopt is settings.json's notification reaching the app.
func (tf *sharedFamily) adopt() {
	tf.a.adoptShared()
	tf.a.shared.Wait()
}

func (tf *sharedFamily) familyValue(t *testing.T, key string) string {
	t.Helper()
	s, _, err := famset.Read(tf.Family)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := s.Get(key)
	return v
}

// Dicta changes the language, the proxy and the download source:
// AudioSmith's appliers run (the UI's language, a new downloader); its
// own change reaches the family, and the rows say they apply to all
// ActNow apps.
func TestSharedSettingsAdopted(t *testing.T) {
	i18n.SetLocale("en")
	t.Cleanup(func() { i18n.SetLocale("en") })
	a, _, tt := newTestApp(t)
	tf := useFamily(t, a, dictaID)
	useShared(t, tf)
	if !a.prefs.Family.SharedMigrated {
		t.Fatal("not migrated")
	}
	// AudioSmith's own folder became the family's (it had none).
	if got := tf.familyValue(t, famset.KeyModelsRoot); got != filepath.Clean(modelCache(a.prefs)) {
		t.Errorf("family models.root %q", got)
	}
	before := a.lib.hub()
	tf.familySet(t, famset.KeyLanguage, "zh-Hans")
	tf.familySet(t, famset.KeyProxy, "http://127.0.0.1:7897")
	tf.familySet(t, famset.KeyHFEndpoints, famset.EndpointsMirror)
	tf.adopt()
	if a.prefs.General.Language != "zh-Hans" || i18n.Locale() != "zh-Hans" {
		t.Errorf("language %q, UI %q", a.prefs.General.Language, i18n.Locale())
	}
	cli := a.lib.hub()
	if cli == before || cli.Options().Proxy != "http://127.0.0.1:7897" || !slices.Equal(cli.Options().Endpoints, []string{"https://hf-mirror.com"}) {
		t.Errorf("downloader %v: proxy %q endpoints %v", cli == before, cli.Options().Proxy, cli.Options().Endpoints)
	}
	i18n.SetLocale("en")
	a.updateSettings(func(s *settings.Settings) { s.General.Language = "en" })
	a.shared.Wait()
	if got := tf.familyValue(t, famset.KeyLanguage); got != "en" {
		t.Errorf("family language %q after AudioSmith chose English", got)
	}
	a.navigate("/settings/network")
	tt.Frame()
	for _, want := range []string{"Applies to all ActNow apps", "Use a different setting for this app"} {
		if !tt.HasText(want) {
			t.Errorf("Network lacks %q", want)
		}
	}
	// Use a different setting for this app: Dicta's next proxy stays Dicta's.
	a.useOwnSetting(famset.KeyProxy)
	a.shared.Wait()
	tf.familySet(t, famset.KeyProxy, "direct")
	tf.adopt()
	if a.prefs.Models.Proxy != "http://127.0.0.1:7897" {
		t.Errorf("proxy %q while not following", a.prefs.Models.Proxy)
	}
	tt.Frame()
	if !tt.HasText("Only for this app") || !tt.HasText("Follow other ActNow apps") {
		t.Error("no 'Only for this app' line")
	}
	a.followFamily(famset.KeyProxy)
	a.shared.Wait()
	if a.prefs.Models.Proxy != "direct" {
		t.Errorf("follow again: proxy %q", a.prefs.Models.Proxy)
	}
}

// The migration finds Dicta's language differs from the user's own: the
// row says so once, naming Dicta, with Use theirs.
func TestSharedOtherValue(t *testing.T) {
	i18n.SetLocale("en")
	t.Cleanup(func() { i18n.SetLocale("en") })
	a, _, tt := newTestApp(t)
	a.updateSettings(func(s *settings.Settings) { s.Models.Proxy = "socks5://127.0.0.1:1080" })
	tf := useFamily(t, a, dictaID)
	tf.familySet(t, famset.KeyProxy, "direct")
	useShared(t, tf)
	if a.prefs.Family.Follows(famset.KeyProxy) || !a.prefs.Family.OtherValue[famset.KeyProxy] {
		t.Fatalf("family %+v", a.prefs.Family)
	}
	a.openSettings("/network")
	tt.Frame()
	if !tt.HasText("Dicta uses a different proxy.") || !tt.HasText("Use theirs") {
		t.Error("no conflict line")
	}
	// Once: closing the settings forgets it.
	a.settingsOpen = false
	tt.Frame()
	a.shared.Wait()
	if a.prefs.Family.OtherValue[famset.KeyProxy] {
		t.Error("still to show after the settings closed")
	}
	a.openSettings("/network")
	tt.Frame()
	if tt.HasText("Dicta uses a different proxy.") || !tt.HasText("Only for this app") {
		t.Error("shown twice")
	}
}

// Without another family app's release build the settings pages are
// exactly as before (pixel for pixel), though the data layer works.
func TestSharedNoFamilyWording(t *testing.T) {
	i18n.SetLocale("en")
	pages := []string{"/general", "/models", "/network"}
	shots := func(a *app, tt interface {
		Frame()
		HasText(string) bool
	}, img func() []byte) [][]byte {
		var out [][]byte
		for _, p := range pages {
			a.settingsOpen = false // opened afresh: the same 设置 ‹ › each time
			a.openSettings(p)
			tt.Frame()
			tt.Frame()
			for _, s := range []string{"ActNow", "Use theirs", "Only for this app"} {
				if tt.HasText(s) {
					t.Errorf("%s: family wording %q", p, s)
				}
			}
			out = append(out, img())
		}
		return out
	}
	a, _, tt := newTestApp(t)
	before := shots(a, tt, func() []byte { return tt.Image().Pix })
	tf := useFamily(t, a, dictaID+".dev") // only a dev build of Dicta
	useShared(t, tf)
	if !a.prefs.Family.SharedMigrated || tf.familyValue(t, famset.KeyModelsRoot) == "" {
		t.Fatal("the data layer did not migrate") // AudioSmith's folder is the family's now
	}
	after := shots(a, tt, func() []byte { return tt.Image().Pix })
	for i := range pages {
		if string(before[i]) != string(after[i]) {
			t.Errorf("%s differs with the family", pages[i])
		}
	}
	openModels(a)
	tt.Frame()
	if tt.HasText("Move models…") {
		t.Error("Move models banner without a release app")
	}
}

// Dicta keeps models elsewhere: the Models page offers to move
// AudioSmith's there; the move (a job) moves them, AudioSmith follows
// the family's folder, claims them there and names it in its manifest.
func TestMoveModelsToFamilyFolder(t *testing.T) {
	i18n.SetLocale("en")
	a, _, tt := newTestApp(t)
	a.updateSettings(func(s *settings.Settings) { s.Setup.Done = true }) // a user of AudioSmith: the catalogue's models are its
	tf := useFamily(t, a, dictaID)
	from := modelCache(a.prefs)
	to := filepath.Join(t.TempDir(), "Shared models")
	tf.familySet(t, famset.KeyModelsRoot, to)
	useShared(t, tf)
	if a.prefs.Family.Follows(famset.KeyModelsRoot) || !a.prefs.Family.OtherValue[famset.KeyModelsRoot] {
		t.Fatalf("family %+v", a.prefs.Family)
	}
	openModels(a)
	tt.Frame()
	want := "Dicta keeps models in " + to + ". Move AudioSmith's models there to share them?"
	if !tt.HasText(want) || !tt.HasText("Keep separate") {
		t.Fatalf("no banner %q", want)
	}
	// AudioSmith's models (complete there, spec §7.5; the test catalogue's
	// snapshots lack some models' files).
	mine := a.lib.modelsIn(from)
	if len(mine) < 10 {
		t.Fatalf("models in %s: %d", from, len(mine))
	}
	a.planModelsMove()
	if !a.modelsMoveAsk || a.modelsMovePlan.To != to || a.modelsMovePlan.Models == 0 {
		t.Fatalf("plan %+v", a.modelsMovePlan)
	}
	tt.Frame()
	if !tt.HasText("Move AudioSmith's models to " + to + "?") {
		t.Error("no confirmation")
	}
	a.modelsMoveAsk = false
	// The move changes settings, and each change starts a shared-settings
	// worker that posts showShared from its goroutine while the move's own
	// posts run on this one: queue every post and run them here, as the
	// window's main loop would (or -race sees two writers).
	var mu sync.Mutex
	var queue []func()
	a.postQueue = func(fn func()) { mu.Lock(); queue = append(queue, fn); mu.Unlock() }
	a.startModelsMove(a.modelsMovePlan)
	for {
		a.shared.Wait()
		mu.Lock()
		q := queue
		queue = nil
		mu.Unlock()
		if len(q) == 0 {
			break
		}
		for _, fn := range q {
			fn()
		}
	}
	a.postQueue = nil
	if a.prefs.Models.CacheDir != to || a.prefs.Models.MovingTo != "" || !a.prefs.Family.Follows(famset.KeyModelsRoot) || a.prefs.Family.OtherValue[famset.KeyModelsRoot] {
		t.Fatalf("after the move: cache %q, %+v", a.prefs.Models.CacheDir, a.prefs.Family)
	}
	for _, c := range mine {
		if models.Check(to, c).State != models.Installed {
			t.Errorf("%s not in the family folder", c.Ref())
		}
		if _, err := os.Stat(hub.RepoDir(from, c.Repo)); !os.IsNotExist(err) {
			t.Errorf("%s stayed in %s", c.Ref(), from)
		}
	}
	if !slices.ContainsFunc(a.toasts, func(s string) bool {
		return strings.HasPrefix(s, "Moved ") && strings.HasSuffix(s, " models to "+to+".")
	}) {
		t.Errorf("toasts %q", a.toasts)
	}
	// What startFamily's worker does next: the new folder's claims, the manifest.
	ctx := context.Background()
	if err := tf.fam.Update(ctx, familyInputs(a.prefs, "0.4.0", t.TempDir()), a.lib.familyWanted); err != nil {
		t.Fatal(err)
	}
	e := catalogEntry(t, "qwen3-asr-0.6b-4bit")
	if c := tf.Claims(family.ReleaseBundleID); !models.SameHub(c.Root, to) || !tf.Claimed(family.ReleaseBundleID, models.Ref{Repo: e.Repo, Revision: e.Revision}) {
		t.Errorf("claims root %q", c.Root)
	}
	if m, err := registry.Read(tf.Family, family.ReleaseBundleID); err != nil || m.Models == nil || !models.SameHub(m.Models.Root, to) {
		t.Errorf("manifest %+v %v", m.Models, err)
	}
	tt.Frame()
	if tt.HasText("Move models…") {
		t.Error("the banner stayed")
	}
}

// #101 M2: while a move of the models has not finished (cancelled, a
// model skipped, the app quit), what it moved is still found: in the old
// folder or in the move's target.
func TestModelFoundDuringUnfinishedMove(t *testing.T) {
	a, _, _ := newTestApp(t)
	ctx := context.Background()
	from := modelCache(a.prefs)
	var repo string
	for _, e := range catalog.Builtin().Entries {
		if _, err := a.lib.modelSnapshot(ctx, e.Repo); err == nil && len(e.Aux) == 0 {
			repo = e.Repo
			break
		}
	}
	if repo == "" {
		t.Fatal("no downloaded model")
	}
	to := filepath.Join(t.TempDir(), "Shared models")
	os.MkdirAll(to, 0o755)
	if err := os.Rename(hub.RepoDir(from, repo), hub.RepoDir(to, repo)); err != nil { // what the move did before it stopped
		t.Fatal(err)
	}
	if _, err := a.lib.modelSnapshot(ctx, repo); err == nil {
		t.Fatal("found without the move's target")
	}
	a.updateSettings(func(s *settings.Settings) { s.Models.MovingTo = to })
	dir, err := a.lib.modelSnapshot(ctx, repo)
	if err != nil || !strings.HasPrefix(dir, to) {
		t.Fatalf("snapshot %q %v", dir, err)
	}
	e := entryFor(repo)
	if l, err := a.lib.hub().Status(ctx, e); err != nil || l.State != hub.Downloaded {
		t.Errorf("the models page's status: %+v %v", l, err)
	}
}

// Keep separate: the banner goes, AudioSmith keeps its folder.
func TestKeepSeparate(t *testing.T) {
	i18n.SetLocale("en")
	a, _, tt := newTestApp(t)
	tf := useFamily(t, a, dictaID)
	tf.familySet(t, famset.KeyModelsRoot, filepath.Join(t.TempDir(), "elsewhere"))
	useShared(t, tf)
	from := a.prefs.Models.CacheDir
	openModels(a)
	a.keepSeparate()
	a.shared.Wait()
	tt.Frame()
	if tt.HasText("Keep separate") || a.prefs.Models.CacheDir != from || a.prefs.Family.Follows(famset.KeyModelsRoot) {
		t.Errorf("after Keep separate: %q %+v", a.prefs.Models.CacheDir, a.prefs.Family)
	}
}

// The environment where the spec keeps it first (§9.3, §7.1):
// $HF_ENDPOINT beats the download source; $HTTPS_PROXY counts only with
// the proxy on automatic; the model folder of the settings (AudioSmith's
// or the family's) beats the default, and $HF_HUB_CACHE plays no part.
func TestSharedEnvPrecedence(t *testing.T) {
	s := settings.Defaults()
	s.Models.Endpoints = []string{"https://hf-mirror.com"}
	t.Setenv("HF_ENDPOINT", "")
	if got := downloadEndpoints(s); !slices.Equal(got, s.Models.Endpoints) {
		t.Errorf("endpoints %v", got)
	}
	t.Setenv("HF_ENDPOINT", "https://hub.example")
	if got := hub.New(hub.Options{CacheDir: t.TempDir(), Endpoints: downloadEndpoints(s)}).Options().Endpoints; !slices.Equal(got, []string{"https://hub.example"}) {
		t.Errorf("with $HF_ENDPOINT: %v", got)
	}
	// Go reads the proxy variables once per process: automatic is what it
	// read (when set), the other modes ignore them.
	t.Setenv("HTTPS_PROXY", "http://10.0.0.1:8080")
	req, _ := http.NewRequest(http.MethodGet, "https://huggingface.co/api", nil)
	env := ""
	if u, _ := http.ProxyFromEnvironment(req); u != nil {
		env = u.String()
	}
	cases := map[string]string{"direct": "", "socks5://127.0.0.1:1080": "socks5://127.0.0.1:1080"}
	if env != "" {
		cases[""] = env
	}
	for mode, want := range cases {
		u, err := hub.ProxyFunc(mode)(req)
		got := ""
		if u != nil {
			got = u.String()
		}
		if err != nil || got != want {
			t.Errorf("proxy %q: %q %v, want %q", mode, got, err, want)
		}
	}
	// $HF_HUB_CACHE no longer chooses the folder (spec v1.0.9): the
	// default is the family's library.
	t.Setenv("ACTNOW_FAMILY_DIR", "/fam")
	t.Setenv("HF_HUB_CACHE", "/env/hub")
	if got := modelCache(s); got != "/fam/models/hub" {
		t.Errorf("default folder %q", got)
	}
	s.Models.CacheDir = "/family/models"
	if got := modelCache(s); got != "/family/models" {
		t.Errorf("folder %q: $HF_HUB_CACHE won", got)
	}
}
