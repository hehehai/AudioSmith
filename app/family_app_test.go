package main

// The ActNow family in the app (family_app.go, models_family.go): Models
// → Delete of a model another app uses, Erase everything and Clean up
// keep what other apps claim, and name them only when a release build of
// another family app is installed.

import (
	"context"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/family"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/actnowkit/conformance"
	"github.com/hehehai/actnowkit/models"
)

const dictaID = "dev.hehehai.dicta"

// sharedFamily is a family directory (conformance) for the test app: the
// apps of register installed (registered release or dev builds),
// AudioSmith started there (its models migrated, claimed).
type sharedFamily struct {
	*conformance.Fixture
	a   *app
	fam *family.Family
}

func useFamily(t *testing.T, a *app, register ...string) *sharedFamily {
	t.Helper()
	// An AudioSmith that was set up: the migration claims its models.
	a.updateSettings(func(s *settings.Settings) { s.Setup.Done = true })
	return startFamily(t, a, register...)
}

// startFamily is useFamily as the settings are (a first run when the
// setup was never done: nothing migrated).
func startFamily(t *testing.T, a *app, register ...string) *sharedFamily {
	t.Helper()
	fx := conformance.New(t)
	for _, bid := range register {
		fx.Register(bid)
	}
	team := ""
	fam := family.New(family.Options{Dir: fx.Family, BundleID: family.ReleaseBundleID, System: conformance.FixtureSystem{},
		AppPath: "/Applications/AudioSmith.app", TeamID: &team, Logf: t.Logf})
	t.Cleanup(fam.Close)
	a.fam = fam
	a.lib.setFamily(fam)
	if err := fam.Start(context.Background(), familyInputs(a.prefs, "0.4.0", t.TempDir()), a.lib.familyWanted); err != nil {
		t.Fatal(err)
	}
	a.showFamily(readFamilyView(fam, nil, modelCache(a.prefs)))
	return &sharedFamily{fx, a, fam}
}

// claim has app bid claim e's repo (and its aux repos) in the app's
// model folder.
func (tf *sharedFamily) claim(t *testing.T, bid string, e catalog.Entry) {
	t.Helper()
	s := &models.Store{Family: tf.Family, Hub: modelCache(tf.a.prefs), BundleID: bid}
	refs := []models.Ref{{Repo: e.Repo, Revision: e.Revision}}
	for _, x := range e.Aux {
		refs = append(refs, models.Ref{Repo: x.Repo, Revision: x.Revision})
	}
	for _, r := range refs {
		if err := s.Claim(context.Background(), models.Claim{Repo: r.Repo, Revision: r.Revision}); err != nil {
			t.Fatal(err)
		}
	}
}

func catalogEntry(t *testing.T, id string) catalog.Entry {
	t.Helper()
	e, ok := catalog.Builtin().ByID(id)
	if !ok {
		t.Fatalf("no %s in the catalogue", id)
	}
	return e
}

// Models → Delete of a model Dicta uses: the family's dialog; "Remove
// from AudioSmith" keeps the files and drops AudioSmith's claim; "Delete
// for all apps" deletes them and leaves Dicta a tombstone.
func TestDeleteSharedModel(t *testing.T) {
	i18n.SetLocale("en")
	a, _, tt := newTestApp(t)
	tf := useFamily(t, a, dictaID)
	e := catalogEntry(t, "qwen3-asr-0.6b-4bit")
	ref := models.Ref{Repo: e.Repo, Revision: e.Revision}
	if !tf.Claimed(family.ReleaseBundleID, ref) {
		t.Fatal("not migrated")
	}
	tf.claim(t, dictaID, e)
	a.refreshModel(e)
	openModels(a)
	a.askDelete(e)
	if !a.deleteAsk || !slices.Equal(a.deleteShared, []string{"Dicta"}) {
		t.Fatalf("ask: open %v, shared %v", a.deleteAsk, a.deleteShared)
	}
	tt.Frame()
	for _, want := range []string{"Remove “Chinese & English transcription” from AudioSmith?", "Dicta also uses this model", "Remove from AudioSmith", "Delete for all apps"} {
		if !tt.HasText(want) {
			t.Errorf("the dialog lacks %q", want)
		}
	}
	a.deleteAsk = false
	a.deleteModel(e, models.ThisApp)
	if l, _ := hub.Status(modelCache(a.prefs), e); l.State != hub.Downloaded || tf.Claimed(family.ReleaseBundleID, ref) {
		t.Errorf("remove from AudioSmith: %v, still claimed %v", l.State, tf.Claimed(family.ReleaseBundleID, ref))
	}
	if !slices.ContainsFunc(a.toasts, func(s string) bool { return strings.HasPrefix(s, "Dicta also uses this model") }) {
		t.Errorf("toasts %q", a.toasts)
	}
	a.toasts = nil
	a.askDelete(e)
	a.deleteAllAsk = true
	tt.Frame()
	if !tt.HasText("Delete “Chinese & English transcription” for all apps?") || !tt.HasText("Dicta will need to download it again") {
		t.Error("no second confirmation")
	}
	a.deleteAsk, a.deleteAllAsk = false, false
	a.deleteModel(e, models.AllApps)
	if l, _ := hub.Status(modelCache(a.prefs), e); l.State != hub.Missing {
		t.Errorf("delete for all apps: %v", l.State)
	}
	ts, _ := models.ReadTombstones(tf.Family)
	if len(ts) != 1 || ts[0].Ref() != ref || ts[0].RemovedBy != family.ReleaseBundleID {
		t.Errorf("tombstones %+v", ts)
	}
}

// Only a dev build (or nothing named) uses the model: the plain dialog;
// the files stay all the same, and the log says why.
func TestDeleteDevOnly(t *testing.T) {
	i18n.SetLocale("en")
	a, _, _ := newTestApp(t)
	tf := useFamily(t, a, dictaID+".dev")
	e := catalogEntry(t, "kokoro-82m-bf16")
	tf.claim(t, dictaID+".dev", e)
	if len(a.famOthers) != 0 {
		t.Fatalf("a dev build counts for the UI: %v", a.famOthers)
	}
	a.askDelete(e)
	if len(a.deleteShared) != 0 {
		t.Fatalf("family dialog for a dev build: %v", a.deleteShared)
	}
	a.deleteAsk = false
	a.deleteModel(e, models.ThisApp)
	if l, _ := hub.Status(modelCache(a.prefs), e); l.State != hub.Downloaded {
		t.Errorf("a dev build's model went: %v", l.State)
	}
	if !slices.Contains(a.toasts, "Removed “Fast English voices”") {
		t.Errorf("toasts %q", a.toasts)
	}
	logs := a.logs.Tail(20, "hub")
	if !slices.ContainsFunc(logs, func(l core.LogEntry) bool { return strings.Contains(l.Message, "Dicta (dev)") }) {
		t.Errorf("no log line naming the dev build")
	}
}

// TestEraseKeepsFamilyModels: erasing everything with the models deletes
// the catalogue's repos except those Dicta claims (Qwen3-ASR; Whisper
// Turbo and its tokenizer repo), lists them by name, and forgets
// AudioSmith's claims.
func TestEraseKeepsFamilyModels(t *testing.T) {
	i18n.SetLocale("en")
	a, _, _ := newTestApp(t)
	tf := useFamily(t, a, dictaID)
	tf.claim(t, dictaID, catalogEntry(t, "qwen3-asr-0.6b-4bit"))
	tf.claim(t, dictaID, catalogEntry(t, "whisper-large-v3-turbo-4bit"))
	cache := modelCache(a.prefs)
	os.MkdirAll(hub.RepoDir(cache, "someone/else"), 0o755)
	a.eraseKeeps()
	want := "These models are also used by Dicta and will be kept: "
	if !strings.HasPrefix(a.resetKeeps, want) || !strings.Contains(a.resetKeeps, "Chinese & English transcription") {
		t.Errorf("keeps %q", a.resetKeeps)
	}
	freed, kept, err := hub.RemoveLeftovers(context.Background(), cache, catalogRepos(), a.hubClient().Options().Family)
	if err != nil || len(kept) != 3 { // (freed counts blobs; the test snapshots hold plain files)
		t.Fatalf("freed %d, kept %+v, %v", freed, kept, err)
	}
	a.familyErase(cache)
	for r, want := range map[string]bool{"mlx-community/Kokoro-82M-bf16": false, "mlx-community/Qwen3-ASR-0.6B-4bit": true,
		"openai/whisper-large-v3": true, "mlx-community/whisper-large-v3-turbo-4bit": true, "someone/else": true} {
		if _, err := os.Stat(hub.RepoDir(cache, r)); (err == nil) != want {
			t.Errorf("%s: there %v, want %v", r, err == nil, want)
		}
	}
	if _, err := os.Stat(tf.Family.ClaimsFile(family.ReleaseBundleID)); !os.IsNotExist(err) {
		t.Errorf("AudioSmith's claims stayed: %v", err)
	}
}

// Without another family app's release build there is no family wording:
// the delete dialog is the plain one, Erase lists nothing.
func TestNoFamilyWording(t *testing.T) {
	i18n.SetLocale("en")
	a, _, tt := newTestApp(t)
	tf := useFamily(t, a)
	e := catalogEntry(t, "qwen3-asr-0.6b-4bit")
	tf.claim(t, "dev.hehehai.dicta.conformance", e) // a test build of Dicta
	openModels(a)
	a.askDelete(e)
	tt.Frame()
	if len(a.deleteShared) != 0 || !tt.HasText("Delete “Chinese & English transcription”?") {
		t.Errorf("dialog: shared %v", a.deleteShared)
	}
	for _, s := range []string{"Dicta", "ActNow", "AudioSmith?", "for all apps"} {
		// Words, not parts of them: Settings lists Dictation.
		word := regexp.MustCompile(`(^|\PL)` + regexp.QuoteMeta(s) + `($|\PL)`)
		if slices.ContainsFunc(tt.Texts(), word.MatchString) {
			t.Errorf("family wording %q shown", s)
		}
	}
	a.deleteAsk = false
	a.eraseKeeps()
	if a.resetKeeps != "" {
		t.Errorf("erase keeps %q", a.resetKeeps)
	}
	// P3: no family lines, no [Use] (TestNoFamilyWordingFirstRun has the rest).
	a.deleteModel(e, models.ThisApp) // the test build keeps the files
	a.refreshFamily()
	if m := a.model(e); a.familyLine(e, m) != "" || a.installedBy(e, m) != "" || a.modelStateText(e, m) != "Downloaded · 9 B" {
		t.Errorf("models page: %q %q %q", a.familyLine(e, m), a.installedBy(e, m), a.modelStateText(e, m))
	}
}
