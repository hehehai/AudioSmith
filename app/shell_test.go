package main

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// fakeHF serves every repo as a tiny model (config.json and a 2 KB LFS
// weights file), as the Hugging Face API and resolve endpoints do.
type fakeHF struct {
	*httptest.Server
	mu     sync.Mutex
	served map[string]int // files served, by repo
}

func newFakeHF(t *testing.T) *fakeHF {
	t.Helper()
	f := &fakeHF{served: map[string]int{}}
	files := func(repo string) map[string][]byte {
		return map[string][]byte{
			"config.json":       []byte(`{"model":"` + repo + `"}`),
			"model.safetensors": bytes.Repeat([]byte(repo), 2048/len(repo)+1)[:2048],
		}
	}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if rest, ok := strings.CutPrefix(p, "/api/models/"); ok {
			repo, rev, _ := strings.Cut(rest, "/revision/")
			rev, _, _ = strings.Cut(rev, "?")
			sha := strings.Repeat("a", 40)
			if len(rev) == 40 {
				sha = rev // a pinned commit
			}
			var sibs []map[string]any
			for name, b := range files(repo) {
				s := map[string]any{"rfilename": name, "size": len(b)}
				h := sha1.New()
				fmt.Fprintf(h, "blob %d\x00", len(b))
				h.Write(b)
				s["blobId"] = hex.EncodeToString(h.Sum(nil))
				if strings.HasSuffix(name, ".safetensors") {
					sum := sha256.Sum256(b)
					s["lfs"] = map[string]any{"sha256": hex.EncodeToString(sum[:]), "size": len(b)}
				}
				sibs = append(sibs, s)
			}
			json.NewEncoder(w).Encode(map[string]any{"id": repo, "sha": sha, "gated": false, "siblings": sibs})
			return
		}
		// /{owner}/{name}/resolve/{commit}/{file}
		parts := strings.SplitN(strings.TrimPrefix(p, "/"), "/", 5)
		if len(parts) == 5 && parts[2] == "resolve" {
			repo := parts[0] + "/" + parts[1]
			if b, ok := files(repo)[parts[4]]; ok {
				f.mu.Lock()
				f.served[repo]++
				f.mu.Unlock()
				http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(b))
				return
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

// useFakeHub points the app's downloads at a fake hub and a temporary
// model folder.
func useFakeHub(t *testing.T, a *app) *fakeHF {
	t.Helper()
	f := newFakeHF(t)
	dir := testDir(t)
	a.updateSettings(func(s *settings.Settings) {
		s.Models.CacheDir = dir
		s.Models.Endpoints = []string{f.URL}
	})
	a.resetHub()
	if fs, ok := unwrapSup(a.eng.sup).(*fakeSup); ok && a.engState == engineStarting {
		fs.f.ready() // the engine restarted for the new model folder
	}
	saved := hubToken
	hubToken = func(func() (string, error)) string { return "" }
	t.Cleanup(func() { hubToken = saved })
	return f
}

func entry(t *testing.T, id string) catalog.Entry {
	t.Helper()
	e, ok := catalog.Builtin().ByID(id)
	if !ok {
		t.Fatal("no entry", id)
	}
	return e
}

func TestNavigation(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.Frame()
	for i, want := range []string{"home", "studio", "voices", "projects", "dub", "transcripts", "stories", "audiobook"} {
		tt.Key(ui.Cmd, ui.Key1+ui.Key(i))
		if a.currentPage() != want {
			t.Errorf("⌘%d: %s", i+1, a.currentPage())
		}
	}
	tt.Key(ui.Cmd, ui.KeyComma)
	if !a.settingsOpen || a.shownPath() != "/settings/general" || !tt.HasText("Interface size") && !tt.HasText("Language") {
		t.Errorf("⌘,: %s %q", a.shownPath(), tt.Texts())
	}
	// Settings is a dialog over the page (0.3): the page stays.
	if a.router.Path() != "/audiobook" {
		t.Errorf("under the settings: %s", a.router.Path())
	}
	if err := tt.Click("Close"); err != nil || a.settingsOpen {
		t.Fatalf("close: %v %v", err, a.settingsOpen)
	}
	// The sidebar.
	if err := tt.Click("Assets"); err != nil {
		t.Fatal(err)
	}
	if a.currentPage() != "assets" {
		t.Errorf("sidebar: %s", a.router.Path())
	}
	// Back.
	a.router.Back()
	tt.Frame()
	if a.router.Path() != "/audiobook" {
		t.Errorf("back: %s", a.router.Path())
	}
	// The same page again adds no history.
	a.showPage("studio")
	a.showPage("studio")
	a.router.Back()
	if a.router.Path() != "/audiobook" {
		t.Errorf("showPage twice pushed twice: %s", a.router.Path())
	}
	// Settings remembers its section; going to a page closes it.
	a.navigate("/settings/appearance")
	a.showPage("studio")
	if a.settingsOpen {
		t.Error("a page left the settings open")
	}
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyComma)
	if a.shownPath() != "/settings/general" { // Appearance is part of General
		t.Errorf("settings section: %s", a.shownPath())
	}
}

func TestDeepLinks(t *testing.T) {
	a, _, tt := newTestApp(t)
	for link, want := range map[string]string{
		"audiosmith://models":           "/settings/models", // a Settings section
		"audiosmith://settings/models":  "/settings/models",
		"audiosmith:///history":         "/assets", // old paths go on (0.5 F1)
		"audiosmith://studio/":          "/studio",
		"/settings/engine":              "/settings/engine",
		"/settings/network":             "/settings/models", // part of Models & network
		"audiosmith://integrations/n8n": "/settings/integrations/n8n",
		"audiosmith://tasks":            "/tasks",
		"audiosmith://logs?source=app":  "/settings/logs",
		"AudioSmith://engine":           "/settings/engine",
		"audiosmith://workflows":        "/workflows", // M7a: every sidebar page
		"audiosmith://dub":              "/dub",
		"audiosmith://home":             "/home",
		"audiosmith://voices/new/clone": "/voices/new/clone",
	} {
		a.openURL(link)
		if a.shownPath() != want {
			t.Errorf("%s → %s, want %s", link, a.shownPath(), want)
		}
	}
	a.settingsOpen = false
	a.openURL("audiosmith://dub")
	a.openURL("audiosmith://nowhere")
	a.openURL("https://example.com/")
	tt.Frame()
	if len(a.toasts) != 0 || a.router.Path() != "/dub" {
		t.Errorf("unknown links moved: %s", a.router.Path())
	}
}

func TestPalette(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.SetFocused(true) // the window has the keyboard
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyK)
	if !a.paletteOpen || !tt.HasText("Command palette") && !tt.HasText("Go to") {
		t.Fatalf("⌘K: %v %q", a.paletteOpen, tt.Texts())
	}
	if !tt.Focused("Search commands") {
		t.Fatalf("search not focused: %q", tt.Texts())
	}
	// The Tester takes typing only into an input clicked first.
	tt.Click("Search commands")
	tt.Type("models")
	if a.paletteQuery != "models" {
		t.Fatalf("query %q", a.paletteQuery)
	}
	items := a.paletteItems()
	if len(items) == 0 || items[0].id != "go/settings/models" {
		t.Fatalf("items %+v", items)
	}
	tt.Key(0, ui.KeyEnter)
	if a.paletteOpen || a.shownPath() != "/settings/models" {
		t.Errorf("enter: open %v path %s", a.paletteOpen, a.shownPath())
	}
	a.settingsOpen = false
	tt.Frame()

	// Arrows move the choice; Escape closes.
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Click("Search commands")
	tt.Type("theme")
	tt.Key(0, ui.KeyDown)
	if a.paletteSel != 1 {
		t.Errorf("down: %d", a.paletteSel)
	}
	tt.Key(0, ui.KeyUp)
	tt.Key(0, ui.KeyUp)
	if n := len(a.paletteItems()); a.paletteSel != n-1 {
		t.Errorf("up wraps: %d of %d", a.paletteSel, n)
	}
	tt.Key(0, ui.KeyEscape)
	if a.paletteOpen {
		t.Error("escape left it open")
	}
	// English words find commands in Chinese too.
	t.Cleanup(func() { i18n.SetLocale("en") })
	i18n.SetLocale("zh-Hans")
	a.paletteQuery = "settings"
	if items := a.paletteItems(); len(items) == 0 || items[0].title != "设置" {
		t.Errorf("zh items %+v", items)
	}
}

func TestShortcutSheet(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.Frame()
	// Typing "?" in the script is text, not the sheet.
	tt.Click("Script")
	if !tt.Focused("Script") {
		t.Fatal("script not focused")
	}
	before := strings.Count(a.text, "?")
	tt.TypeKey(ui.Shift, ui.KeySlash, "?")
	// Where the click put the caret depends on the platform's fonts.
	if a.sheetOpen || strings.Count(a.text, "?") != before+1 {
		t.Errorf("? in the script: sheet %v text %q", a.sheetOpen, a.text)
	}
	a.navigate("/history")
	tt.Frame()
	tt.Key(ui.Shift, ui.KeySlash)
	if !a.sheetOpen || !tt.HasText("Keyboard shortcuts") || !tt.HasText("⌘K") {
		t.Fatalf("sheet: %v %q", a.sheetOpen, tt.Texts())
	}
	tt.Click("Close")
	if a.sheetOpen {
		t.Error("not closed")
	}
}

func TestLanguageSwitch(t *testing.T) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	a, _, tt := newTestApp(t)
	a.navigate("/settings/general")
	tt.Frame()
	a.settingsForm.language = "简体中文"
	a.updateSettings(func(s *settings.Settings) { s.General.Language = "zh-Hans" })
	tt.Frame()
	if i18n.Locale() != "zh-Hans" || !tt.HasText("文字转语音") || !tt.HasText("设置") || !tt.HasText("首页") {
		t.Fatalf("zh: %s %q", i18n.Locale(), tt.Texts())
	}
	// Saved: a new app over the same library starts in Chinese.
	if got := a.settings.Get().General.Language; got != "zh-Hans" {
		t.Errorf("saved %q", got)
	}
	// Following the system.
	a.systemLocale = "zh_CN"
	a.updateSettings(func(s *settings.Settings) { s.General.Language = "" })
	if i18n.Locale() != "zh-Hans" {
		t.Errorf("system zh_CN: %s", i18n.Locale())
	}
	a.systemLocale = "fr-FR"
	a.applyLocale()
	tt.Frame()
	if i18n.Locale() != "en" || !tt.HasText("Text to speech") {
		t.Errorf("system fr: %s", i18n.Locale())
	}
}

func TestSettingsPersist(t *testing.T) {
	dir := t.TempDir()
	a, _, tt := newTestAppIn(t, dir)
	a.navigate("/settings/appearance")
	tt.Frame()
	if err := tt.Click("Dark"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("125%"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if s := a.settings.Get(); s.Appearance.Theme != "dark" || s.Appearance.Scale != 1.25 {
		t.Fatalf("saved %+v", s.Appearance)
	}
	// The theme follows: text is larger at 125 %.
	r, ok := tt.Find("Interface size")
	if !ok || r.H < 18 {
		t.Errorf("scaled text: %+v %v", r, ok)
	}
	a.navigate("/settings/engine")
	tt.Frame()
	a.memoryGB = 12
	a.updateSettings(func(s *settings.Settings) { s.Engine.MemoryBudgetGB = 12 })

	// Another app over the same database reads them back.
	b, _, _ := newTestAppIn(t, dir)
	if s := b.prefs; s.Appearance.Theme != "dark" || s.Appearance.Scale != 1.25 || s.Engine.MemoryBudgetGB != 12 {
		t.Errorf("reopened %+v", s)
	}
}

// Engine settings reach the supervisor while it runs.
func TestEngineSettingsApply(t *testing.T) {
	a, eng, _ := newTestApp(t)
	a.updateSettings(func(s *settings.Settings) {
		s.Engine.MemoryBudgetGB = 6
		s.Models.AutoUnload = 15
	})
	if eng.budget != 6<<30 || eng.idle != 15*time.Minute {
		t.Errorf("budget %d idle %v", eng.budget, eng.idle)
	}
	dir := t.TempDir()
	restarts := eng.restarts
	a.updateSettings(func(s *settings.Settings) { s.Models.CacheDir = dir })
	if eng.cacheDir != dir || eng.restarts != restarts+1 {
		t.Errorf("cache dir %q, restarts %d", eng.cacheDir, eng.restarts-restarts)
	}
}
