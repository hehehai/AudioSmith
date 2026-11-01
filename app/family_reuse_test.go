package main

// Reusing other ActNow apps' models (family_reuse.go): the first run's
// reuse card, the Models page's family lines and [Use], the claim of a
// model in use, licences accepted in another app, the storage page's
// model folder row; and none of it without another app's release build.

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/family"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/licence"
	"github.com/hehehai/actnowkit/conformance"
	"github.com/hehehai/actnowkit/models"
	famruntime "github.com/hehehai/actnowkit/runtime"
)

func refOf(e catalog.Entry) models.Ref { return models.Ref{Repo: e.Repo, Revision: e.Revision} }

// downloadsSent are the repos the downloads asked of the engine.
func downloadsSent(eng *fakeEngine) []string {
	var out []string
	for _, r := range eng.sent {
		if r.Spec.Kind == hub.KindDownload {
			out = append(out, r.Model)
		}
	}
	return out
}

// A first run with Dicta's models on disk (spec §16.1 scenario 1): nothing
// is migrated; the models step shows the card for the fast pack ("3
// models are already on this Mac · Installed by Dicta"); "Use them"
// claims them (origin reused, Dicta's claims stay), and finishing
// downloads only what is missing.
func TestReuseCard(t *testing.T) {
	i18n.SetLocale("en")
	stubMachine(t, 8, true) // the fast pack
	a, eng, _ := newTestApp(t)
	delete(eng.executors, hub.KindDownload) // record downloads, do not run them
	tt := ui.NewTester(a.view, 1100, 800)
	tf := startFamily(t, a, dictaID)
	cache := modelCache(a.prefs)
	kokoro, asr, vad, wm := catalogEntry(t, "kokoro-82m-bf16"), catalogEntry(t, catalog.DefaultDictation),
		catalogEntry(t, catalog.DefaultVAD), catalogEntry(t, catalog.DefaultWatermark)
	if c := tf.Claims(family.ReleaseBundleID); len(c.Claims) != 0 {
		t.Fatalf("a first run migrated %d models", len(c.Claims))
	}
	tf.claim(t, dictaID, asr)
	tf.claim(t, dictaID, vad)
	os.RemoveAll(hub.RepoDir(cache, wm.Repo)) // still to download
	a.refreshModels()
	a.openOnboarding()
	a.obStep = 1
	tt.Frame()
	tt.Frame()
	for _, s := range []string{"3 models are already on this Mac", "no download needed", "Use them", "Choose models", " still to download"} {
		if !slices.ContainsFunc(tt.Texts(), func(x string) bool { return strings.Contains(x, s) }) {
			t.Errorf("the card lacks %q: %q", s, tt.Texts())
		}
	}
	if !slices.ContainsFunc(tt.Texts(), func(x string) bool { return strings.HasPrefix(x, "Installed by Dicta · ") }) {
		t.Errorf("not named: %q", tt.Texts())
	}
	if err := tt.Click("Use them"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("In use: nothing to download for them") {
		t.Errorf("after Use them: %q", tt.Texts())
	}
	for _, e := range []catalog.Entry{kokoro, asr, vad} {
		c, ok := tf.Claims(family.ReleaseBundleID).Find(refOf(e))
		if !ok || c.Origin != models.OriginReused || c.Model != e.ID {
			t.Errorf("%s: %+v %v", e.ID, c, ok)
		}
	}
	if !tf.Claimed(dictaID, refOf(asr)) {
		t.Error("Dicta's claim went")
	}
	a.obStep = 3
	tt.Frame()
	if err := tt.Click("Start using AudioSmith"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := downloadsSent(eng); !slices.Equal(got, []string{wm.Repo}) {
		t.Errorf("downloads %v, want only %s", got, wm.Repo)
	}
}

// Nobody named installed them (no claims; Dicta installed): "Already on
// this Mac"; finishing with the card's default claims them all the same.
func TestReuseCardUnnamed(t *testing.T) {
	i18n.SetLocale("en")
	stubMachine(t, 8, true)
	a, eng, _ := newTestApp(t)
	delete(eng.executors, hub.KindDownload)
	tt := ui.NewTester(a.view, 1100, 800)
	tf := startFamily(t, a, dictaID)
	a.refreshModels()
	a.openOnboarding()
	a.obStep = 1
	tt.Frame()
	tt.Frame()
	if !tt.HasText("3 models are already on this Mac") ||
		!slices.ContainsFunc(tt.Texts(), func(x string) bool { return strings.HasPrefix(x, "Already on this Mac · ") }) {
		t.Fatalf("card %q", tt.Texts())
	}
	if slices.ContainsFunc(tt.Texts(), func(x string) bool { return strings.Contains(x, "by Dicta") }) {
		t.Error("names an app that did not install them")
	}
	a.finishOnboarding()
	asr := catalogEntry(t, catalog.DefaultDictation)
	if c, ok := tf.Claims(family.ReleaseBundleID).Find(refOf(asr)); !ok || c.Origin != models.OriginReused {
		t.Errorf("not claimed at the end of the setup: %+v", c)
	}
	// (The watermark model's test files lack one it needs: it downloads.)
	wm := catalogEntry(t, catalog.DefaultWatermark)
	if got := downloadsSent(eng); !slices.Equal(got, []string{wm.Repo}) {
		t.Errorf("downloads %v", got)
	}
}

// "Choose models": nothing is claimed; the setup ends on the Models
// page (no welcome line, which would leave it for the Studio), where Dicta's models say "Installed · from Dicta" with [Use];
// [Use] claims one, which is then "Shared with Dicta".
func TestReuseChooseModels(t *testing.T) {
	i18n.SetLocale("en")
	stubMachine(t, 8, true)
	a, eng, _ := newTestApp(t)
	delete(eng.executors, hub.KindDownload)
	tt := ui.NewTester(a.view, 1100, 800)
	tf := startFamily(t, a, dictaID)
	asr := catalogEntry(t, catalog.DefaultDictation)
	tf.claim(t, dictaID, asr)
	a.refreshModels()
	a.openOnboarding()
	a.obStep = 1
	tt.Frame()
	if err := tt.Click("Choose models"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Models lists them after the setup: use the ones you want.") {
		t.Errorf("after Choose: %q", tt.Texts())
	}
	a.finishOnboarding()
	if c := tf.Claims(family.ReleaseBundleID); len(c.Claims) != 0 {
		t.Errorf("claimed %+v", c.Claims)
	}
	if a.shownPath() != "/settings/models" || a.firstSound {
		t.Errorf("page %q, first sound %v", a.shownPath(), a.firstSound)
	}
	a.refreshFamily()
	a.modelsFind = asr.DisplayName()
	tt.Frame()
	if !tt.HasText("Installed · from Dicta") || !tt.HasText("Use") {
		t.Fatalf("models page %q", tt.Texts())
	}
	if err := tt.Click("Use"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if c, ok := tf.Claims(family.ReleaseBundleID).Find(refOf(asr)); !ok || c.Origin != models.OriginReused {
		t.Errorf("Use: %+v %v", c, ok)
	}
	if !tt.HasText("Shared with Dicta") || tt.HasText("Installed · from Dicta") {
		t.Errorf("after Use %q", tt.Texts())
	}
}

// After "Remove from AudioSmith" (Dicta keeps the files) the model says
// "Installed · from Dicta" with [Use]; the engine loading it claims it
// again (claimInUse), and the Models page says "Shared with Dicta",
// "Also loaded in Dicta" while Dicta holds it.
func TestRemovedModelReclaimedOnUse(t *testing.T) {
	i18n.SetLocale("en")
	a, _, tt := newTestApp(t)
	tf := useFamily(t, a, dictaID)
	asr := catalogEntry(t, catalog.DefaultDictation)
	tf.claim(t, dictaID, asr)
	a.refreshModel(asr)
	a.deleteModel(asr, models.ThisApp)
	if tf.Claimed(family.ReleaseBundleID, refOf(asr)) {
		t.Fatal("still claimed")
	}
	openModels(a)
	a.modelsFind = asr.DisplayName()
	tt.Frame()
	if got := a.modelStateText(asr, a.model(asr)); got != "Installed · from Dicta" || !tt.HasText("Use") {
		t.Errorf("after the removal: %q %q", got, tt.Texts())
	}
	a.claimInUse([]string{asr.Repo})
	if c, ok := tf.Claims(family.ReleaseBundleID).Find(refOf(asr)); !ok || c.Origin != models.OriginReused || c.Model != asr.ID {
		t.Fatalf("not claimed in use: %+v %v", c, ok)
	}
	// Dicta holds it loaded.
	if err := famruntime.Write(tf.Family, famruntime.File{BundleID: dictaID, Loaded: []famruntime.Loaded{
		{Repo: asr.Repo, Revision: asr.Revision, Worker: "listen", Bytes: 1 << 29}}}); err != nil {
		t.Fatal(err)
	}
	a.famRT = tf.fam.Runtime(family.RuntimeOptions{})
	a.refreshFamily()
	tt.Frame()
	if line := a.familyLine(asr, a.model(asr)); line != "Shared with Dicta · Also loaded in Dicta" || !tt.HasText(line) {
		t.Errorf("line %q, texts %q", line, tt.Texts())
	}
	if a.installedBy(asr, a.model(asr)) != "" || tt.HasText("Installed · from Dicta") {
		t.Error("still installed by Dicta")
	}
}

// A licence the user accepted in Dicta (spec §7.9): the dialog still
// shows it, says so, and asks to Continue; Continue accepts in AudioSmith
// and leaves Dicta's record. Accepting in AudioSmith records it for the
// family; revoking revokes only AudioSmith's.
func TestLicenceAcceptedInDicta(t *testing.T) {
	i18n.SetLocale("en")
	ctx := context.Background()
	a, _, tt := newTestApp(t)
	tf := useFamily(t, a, dictaID)
	omni := catalogEntry(t, idOmniVoice)
	if err := models.AcceptLicence(ctx, tf.Family, omni.Repo, "CC-BY-NC-4.0", dictaID); err != nil {
		t.Fatal(err)
	}
	a.askLicence(omni, nil)
	tt.Frame()
	if !a.licenceOpen || !slices.ContainsFunc(tt.Texts(), func(s string) bool { return strings.HasPrefix(s, "You accepted this licence in Dicta on ") }) ||
		!tt.HasText("Read the licence") || tt.HasText("I have read the licence and will use this model only as it allows.") {
		t.Fatalf("dialog %q", tt.Texts())
	}
	if err := tt.Click("Continue"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.licenceOpen || !licence.Covers(a.accepted, omni) {
		t.Fatalf("Continue: open %v covered %v", a.licenceOpen, licence.Covers(a.accepted, omni))
	}
	if acc, _ := models.LicenceAccepted(tf.Family, omni.Repo, "CC-BY-NC-4.0"); acc.By != dictaID {
		t.Errorf("Dicta's record replaced: %+v", acc)
	}
	// Revoked in AudioSmith: only its own record.
	for _, acc := range a.accepted {
		a.revokeLicence(acc)
	}
	a.accepted, _ = a.licences.List(ctx)
	if licence.Covers(a.accepted, omni) {
		t.Fatal("not revoked")
	}
	if _, ok := models.LicenceAccepted(tf.Family, omni.Repo, "CC-BY-NC-4.0"); !ok {
		t.Error("the family's record went")
	}
	// Accepting in AudioSmith (Dicta gone from the record): AudioSmith's.
	if err := os.Remove(tf.Family.LicencesFile()); err != nil {
		t.Fatal(err)
	}
	a.askLicence(omni, nil)
	tt.Frame()
	if !tt.HasText("I have read the licence and will use this model only as it allows.") {
		t.Fatalf("plain dialog %q", tt.Texts())
	}
	a.acceptLicence(omni, nil)
	if acc, ok := models.LicenceAccepted(tf.Family, omni.Repo, "CC-BY-NC-4.0"); !ok || acc.By != family.ReleaseBundleID {
		t.Errorf("AudioSmith's acceptance %+v %v", acc, ok)
	}
}

// Settings → Storage's model folder row: "Shared by all ActNow apps" with
// Dicta installed.
func TestStorageFolderShared(t *testing.T) {
	i18n.SetLocale("en")
	a, _, tt := newTestApp(t)
	useFamily(t, a, dictaID)
	tt.SetSize(1100, 1700)
	a.navigate("/settings/storage")
	tt.Frame()
	tt.Frame()
	if !tt.HasText("Shared by all ActNow apps. Change it in Settings → Models & network.") {
		t.Errorf("storage %q", tt.Texts())
	}
}

// Without another app's release build (a dev build of Dicta installed
// and claiming): no card, no family lines, the plain licence dialog and
// storage row; the claims are written all the same.
func TestNoFamilyWordingFirstRun(t *testing.T) {
	i18n.SetLocale("en")
	ctx := context.Background()
	stubMachine(t, 8, true)
	a, eng, _ := newTestApp(t)
	delete(eng.executors, hub.KindDownload)
	tt := ui.NewTester(a.view, 1100, 800)
	tf := startFamily(t, a, dictaID+".dev")
	asr, omni := catalogEntry(t, catalog.DefaultDictation), catalogEntry(t, idOmniVoice)
	tf.claim(t, dictaID+".dev", asr)
	if err := models.AcceptLicence(ctx, tf.Family, omni.Repo, "CC-BY-NC-4.0", dictaID+".dev"); err != nil {
		t.Fatal(err)
	}
	a.refreshModels()
	a.openOnboarding()
	a.obStep = 1
	tt.Frame()
	tt.Frame()
	noFamily := func(where string) {
		t.Helper()
		for _, s := range []string{"by Dicta", "from Dicta", "with Dicta", "in Dicta", "Dicta (", "ActNow", "already on this Mac", "Use them", "Installed · from", "Also loaded in", "You accepted"} {
			if slices.ContainsFunc(tt.Texts(), func(x string) bool { return strings.Contains(x, s) }) {
				t.Errorf("%s: family wording %q", where, s)
			}
		}
	}
	noFamily("onboarding")
	a.finishOnboarding()
	if c, ok := tf.Claims(family.ReleaseBundleID).Find(refOf(asr)); !ok || c.Origin != models.OriginReused {
		t.Errorf("the pack's models not claimed: %+v", c)
	}
	a.refreshFamily()
	openModels(a)
	a.modelsFind = asr.DisplayName()
	tt.Frame()
	noFamily("models")
	a.askLicence(omni, nil)
	tt.Frame()
	if !tt.HasText("I have read the licence and will use this model only as it allows.") {
		t.Errorf("licence %q", tt.Texts())
	}
	noFamily("licence")
	a.licenceOpen = false
	tt.SetSize(1100, 1700)
	a.navigate("/settings/storage")
	tt.Frame()
	tt.Frame()
	if !tt.HasText("Change it in Settings → Models & network.") || !tt.HasText("Models can be downloaded again, so Time Machine leaves them out.") {
		t.Errorf("storage %q", tt.Texts())
	}
	noFamily("storage")
}

// The wizard opens before the family's start (discovery) ends: the card
// appears, naming Dicta, once it has.
func TestReuseCardAfterDiscovery(t *testing.T) {
	i18n.SetLocale("en")
	stubMachine(t, 8, true)
	a, _, _ := newTestApp(t)
	tt := ui.NewTester(a.view, 1100, 800)
	fx := conformance.New(t)
	fx.Register(dictaID)
	team := ""
	fam := family.New(family.Options{Dir: fx.Family, BundleID: family.ReleaseBundleID, System: conformance.FixtureSystem{},
		AppPath: "/Applications/AudioSmith.app", TeamID: &team, Logf: t.Logf})
	t.Cleanup(fam.Close)
	a.fam = fam
	a.lib.setFamily(fam)
	cache := modelCache(a.prefs)
	asr := catalogEntry(t, catalog.DefaultDictation)
	dicta := &models.Store{Family: fx.Family, Hub: cache, BundleID: dictaID}
	if err := dicta.Claim(context.Background(), models.Claim{Repo: asr.Repo, Revision: asr.Revision}); err != nil {
		t.Fatal(err)
	}
	a.refreshModels()
	a.openOnboarding()
	a.obStep = 1
	tt.Frame()
	if slices.ContainsFunc(tt.Texts(), func(s string) bool { return strings.Contains(s, "already on this Mac") }) {
		t.Fatal("a card before discovery")
	}
	if err := fam.Start(context.Background(), familyInputs(a.prefs, "0.4.0", t.TempDir()), a.lib.familyWanted); err != nil {
		t.Fatal(err)
	}
	a.showFamily(readFamilyView(fam, nil, cache))
	tt.Frame()
	tt.Frame()
	if !slices.ContainsFunc(tt.Texts(), func(s string) bool { return strings.HasPrefix(s, "Installed by Dicta · ") }) {
		t.Errorf("card %q", tt.Texts())
	}
}

// A first run (the setup never done) still claims, origin migrated, what
// AudioSmith shows it downloaded or used — a finished download, a take's
// model — but not the models only Dicta put there (hard constraint 1 of
// the phase-1 plan: the manifest ends Dicta's legacy protection).
func TestFirstRunClaimsOwnModels(t *testing.T) {
	i18n.SetLocale("en")
	a, eng, _ := newTestApp(t)
	delete(eng.executors, hub.KindDownload)
	kokoro, base, asr := catalogEntry(t, "kokoro-82m-bf16"), catalogEntry(t, "qwen3-tts-0.6b-base-8bit"), catalogEntry(t, catalog.DefaultDictation)
	a.download(kokoro)
	eng.succeed(eng.sent[len(eng.sent)-1].ID, nil) // it finished
	if js, _ := a.lib.sched.List(context.Background(), jobs.Filter{Kind: hub.KindDownload, States: []jobs.State{jobs.StateSucceeded}}); len(js) != 1 {
		t.Fatalf("download jobs %v", js)
	}
	addTake(t, a, core.Take{ID: "take-base", Text: "Hello", Model: base.Repo, SampleRate: 24000}, 1)
	tf := startFamily(t, a, dictaID)
	tf.claim(t, dictaID, asr)
	mine := tf.Claims(family.ReleaseBundleID)
	for _, e := range []catalog.Entry{kokoro, base} {
		if c, ok := mine.Find(refOf(e)); !ok || c.Origin != models.OriginMigrated {
			t.Errorf("%s: %+v %v", e.ID, c, ok)
		}
	}
	if _, ok := mine.Find(refOf(asr)); ok {
		t.Error("claimed a model only Dicta put there")
	}
	main := slices.DeleteFunc(slices.Clone(mine.Claims), func(c models.Claim) bool { return c.Aux })
	if len(main) != 2 { // (and Kokoro's auxiliary repos)
		t.Errorf("claims %+v", mine.Claims)
	}
}
