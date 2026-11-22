package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/family"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/actnowkit/conformance"
	actnow "github.com/hehehai/actnowkit/family"
	"github.com/hehehai/actnowkit/models"
)

// TestMacReuseScenario plays the family spec §16.1 scenario 1 (zero
// download reuse) on a Mac, with the real LaunchServices and a real
// download, in throwaway directories: "Dicta" (a release build, its .app
// a stand-in under the family directory's parent) downloads Silero VAD
// from Hugging Face and claims it; AudioSmith's first run then migrates
// nothing, its models step shows "Shared across ActNow", "Use them" claims
// the model (origin reused) next to Dicta's claim, and finishing the
// setup downloads only the fast pack's other models (recorded, not run):
// none of Silero VAD.
//
//	AUDIOSMITH_MAC=1 ACTNOW_FAMILY_DIR=/tmp/c75/ActNow HF_HUB_CACHE=/tmp/c75/hub \
//	go test -run TestMacReuseScenario -v .
func TestMacReuseScenario(t *testing.T) {
	if os.Getenv("AUDIOSMITH_MAC") == "" || runtime.GOOS != "darwin" {
		t.Skip("set AUDIOSMITH_MAC=1 on a Mac")
	}
	dir, hubDir := os.Getenv(actnow.EnvDir), os.Getenv("HF_HUB_CACHE")
	home, _ := os.UserHomeDir()
	if dir == "" || hubDir == "" {
		t.Skip("set ACTNOW_FAMILY_DIR and HF_HUB_CACHE to throwaway directories")
	}
	if models.SameHub(dir, filepath.Join(home, "Library", "Application Support", "ActNow")) || models.SameHub(hubDir, filepath.Join(home, ".cache", "huggingface", "hub")) {
		t.Fatal("ACTNOW_FAMILY_DIR and HF_HUB_CACHE must be throwaway directories")
	}
	i18n.SetLocale("en")
	stubMachine(t, 8, true) // the fast pack
	ctx := context.Background()
	fx := &conformance.Fixture{T: t, Family: actnow.At(dir), Hub: hubDir, Apps: map[string]models.AppState{}}

	// Dicta: installed (a release build), downloads Silero VAD and claims it.
	fx.Register(dictaID)
	vad := catalogEntry(t, catalog.DefaultVAD)
	dl := hub.New(hub.Options{CacheDir: hubDir, Family: hub.Family{BundleID: dictaID}})
	if err := dl.Download(ctx, vad, nil); err != nil {
		t.Fatalf("Dicta's download: %v", err)
	}
	dicta := &models.Store{Family: fx.Family, Hub: hubDir, BundleID: dictaID, AppID: "dicta"}
	if err := dicta.Claim(ctx, family.ClaimOf(vad, vad.Repo, vad.Revision, nil, false, models.OriginDownloaded)); err != nil {
		t.Fatal(err)
	}

	// AudioSmith's first run.
	a, eng, _ := newTestApp(t)
	delete(eng.executors, hub.KindDownload) // record downloads, do not run them
	a.updateSettings(func(s *settings.Settings) { s.Models.CacheDir = hubDir })
	a.lib.resetHub()
	app, err := conformance.FakeApp(filepath.Join(filepath.Dir(dir), "Applications", family.ReleaseBundleID), "AudioSmith", family.ReleaseBundleID)
	if err != nil {
		t.Fatal(err)
	}
	fam := family.New(family.Options{Dir: fx.Family, BundleID: family.ReleaseBundleID, AppPath: app, Logf: t.Logf}) // the real LaunchServices
	t.Cleanup(fam.Close)
	a.fam = fam
	a.lib.setFamily(fam)
	if err := fam.Start(ctx, familyInputs(a.prefs, "0.4.0", t.TempDir()), a.lib.familyWanted); err != nil {
		t.Fatal(err)
	}
	a.showFamily(readFamilyView(fam, nil, hubDir))
	t.Logf("other apps the UI may name: %v", a.famOthers)
	if !slices.Contains(a.famOthers, "Dicta") {
		t.Fatalf("Dicta not found: %+v", fam.Found())
	}
	if c := fx.Claims(family.ReleaseBundleID); len(c.Claims) != 0 {
		t.Fatalf("the first run migrated %+v", c.Claims)
	}
	a.refreshModels()
	tt := ui.NewTester(a.view, 1100, 800)
	a.openOnboarding()
	a.obStep = 1
	tt.Frame()
	tt.Frame()
	var card []string
	for _, s := range tt.Texts() {
		if strings.Contains(s, "already on this Mac") || strings.Contains(s, "no download needed") || strings.Contains(s, "still to download") {
			card = append(card, s)
		}
	}
	t.Logf("card: %q", card)
	if !slices.ContainsFunc(card, func(s string) bool { return strings.HasPrefix(s, "Shared across ActNow · ") }) {
		t.Fatalf("no card naming Dicta: %q", tt.Texts())
	}
	if err := tt.Click("Use them"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	ref := models.Ref{Repo: vad.Repo, Revision: vad.Revision}
	c, ok := fx.Claims(family.ReleaseBundleID).Find(ref)
	if !ok || c.Origin != models.OriginReused || !fx.Claimed(dictaID, ref) {
		t.Fatalf("claims: AudioSmith %+v %v, Dicta %v", c, ok, fx.Claimed(dictaID, ref))
	}
	a.obStep = 3
	tt.Frame()
	if err := tt.Click("Start using AudioSmith"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	got := downloadsSent(eng)
	t.Logf("downloads asked for (recorded, not run): %v", got)
	if slices.Contains(got, vad.Repo) {
		t.Errorf("Silero VAD downloaded again")
	}
	for _, bid := range []string{dictaID, family.ReleaseBundleID} {
		for _, c := range fx.Claims(bid).Claims {
			t.Logf("claims/%s.json: %s origin %s model %s", bid, c.Ref(), c.Origin, c.Model)
		}
	}
}
