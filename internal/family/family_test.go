package family

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/actnowkit/conformance"
	actnow "github.com/hehehai/actnowkit/family"
	"github.com/hehehai/actnowkit/models"
	"github.com/hehehai/actnowkit/registry"
)

// Commits of the test repos.
const (
	revA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	revB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	revG = "cccccccccccccccccccccccccccccccccccccccc"
)

// testCatalog: a speech model with an auxiliary repo, and a speech
// recognition model.
var testCatalog = []catalog.Entry{
	{ID: "tts-a", Repo: "acme/tts-a", Revision: revA, Tasks: []catalog.Task{catalog.TaskTTS},
		Aux: []catalog.Aux{{Repo: "acme/g2p", Revision: revG}}},
	{ID: "asr-b", Repo: "acme/asr-b", Revision: revB, Tasks: []catalog.Task{catalog.TaskSTT}},
}

// fakeSystem is LaunchServices knowing the apps of paths (bundle id →
// .app), none running.
type fakeSystem map[string]string

func (s fakeSystem) AppPaths(id string) []string {
	if p, ok := s[id]; ok {
		return []string{p}
	}
	return nil
}
func (fakeSystem) Running(string) []registry.Process        { return nil }
func (fakeSystem) BundleID(appPath string) (string, error)  { return registry.BundleIDAt(appPath) }
func wantedOf(custom ...string) func(string) []models.Claim { return wantedIn(testCatalog, custom...) }
func wantedIn(es []catalog.Entry, custom ...string) func(string) []models.Claim {
	return func(hub string) []models.Claim { return Wanted(hub, es, custom) }
}

type testFamily struct {
	*Family
	fx  *conformance.Fixture
	sys fakeSystem
}

// newFamily is AudioSmith's Family in the fixture, as bundleID ("" =
// the release id), running from a fake .app.
func newFamily(t *testing.T, fx *conformance.Fixture, bundleID string) *testFamily {
	t.Helper()
	if bundleID == "" {
		bundleID = ReleaseBundleID
	}
	app, err := conformance.FakeApp(filepath.Join(t.TempDir(), "Applications"), "AudioSmith", bundleID)
	if err != nil {
		t.Fatal(err)
	}
	sys := fakeSystem{}
	team := "779M533DL5"
	f := New(Options{Dir: fx.Family, BundleID: bundleID, System: sys, AppPath: app, TeamID: &team,
		Logf: t.Logf, Settle: 10 * time.Millisecond})
	t.Cleanup(f.Close)
	return &testFamily{f, fx, sys}
}

func inputs(hub string) Inputs {
	return Inputs{Version: "0.4.0-beta.6", Beta: true, Hub: hub, DataDir: "/Users/u/Library/Application Support/AudioSmith",
		Shortcut: "CmdOrCtrl+Shift+Space", ShortcutEnabled: true, APIEnabled: true, APIPort: 3901, APIKey: true}
}

// install puts the test repos in the fixture's hub.
func install(fx *conformance.Fixture, refs ...models.Ref) {
	for _, r := range refs {
		fx.Install(conformance.TestRepo(r.Repo, r.Revision, 64))
	}
}

var (
	refA = models.Ref{Repo: "acme/tts-a", Revision: revA}
	refB = models.Ref{Repo: "acme/asr-b", Revision: revB}
	refG = models.Ref{Repo: "acme/g2p", Revision: revG}
)

func TestStartWritesManifestAndMigratedClaims(t *testing.T) {
	fx := conformance.New(t)
	install(fx, refA, refG, refB)
	f := newFamily(t, fx, "")
	if err := f.Start(context.Background(), inputs(fx.Hub), wantedOf()); err != nil {
		t.Fatal(err)
	}
	m, err := registry.Read(fx.Family, ReleaseBundleID)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(); err != nil {
		t.Errorf("manifest invalid: %v", err)
	}
	if m.App != "audiosmith" || m.Name != "AudioSmith" || m.Variant != actnow.Release || m.Version != "0.4.0-beta.6" ||
		m.Channel != "beta" || m.Path != f.appPath || m.TeamID != "779M533DL5" || m.URLScheme != "audiosmith" ||
		m.SpecVersion != 1 || m.DataDir == "" || m.Models == nil || m.Models.Root != fx.Hub {
		t.Errorf("manifest %+v", m)
	}
	wantCaps := []string{"models.hf", "asr.local", "tts.local", "llm.local", "dictation", "api.local"}
	if !slices.Equal(m.Capabilities, wantCaps) {
		t.Errorf("capabilities %v", m.Capabilities)
	}
	if m.Links["models"] != "audiosmith://models" || m.Links["shortcuts"] != "audiosmith://settings/dictation" ||
		m.Links["network"] != "audiosmith://settings/network" {
		t.Errorf("links %v", m.Links)
	}
	if len(m.Shortcuts) != 1 {
		t.Fatalf("shortcuts %+v", m.Shortcuts)
	}
	if s := m.Shortcuts[0]; s.ID != "dictation" || s.Keys != "Shift+Command+Space" || s.Trigger != "press" || !s.Enabled ||
		s.Mechanism != "carbon" || s.Exclusive {
		t.Errorf("shortcut %+v", s)
	}
	if len(m.Services) != 1 {
		t.Fatalf("services %+v", m.Services)
	}
	s := m.Services[0]
	if s.ID != "audiosmith.api" || s.Kind != "openai-audio" || s.URL != "http://127.0.0.1:3901/v1" ||
		s.Auth == nil || s.Auth.Type != "bearer" || s.Auth.Keychain == nil || *s.Auth.Keychain != (registry.KeychainRef{Service: "AudioSmith", Account: "api.key"}) ||
		filepath.ToSlash(s.PortFile) != "/Users/u/Library/Application Support/AudioSmith/api.json" || !slices.Contains(s.Endpoints, "/audio/speech") {
		t.Errorf("service %+v", s)
	}

	c := fx.Claims(ReleaseBundleID)
	if c.App != "audiosmith" || !models.SameHub(c.Root, fx.Hub) || len(c.Claims) != 3 {
		t.Fatalf("claims %+v", c)
	}
	for ref, want := range map[models.Ref]models.Claim{
		refA: {Model: "tts-a", Role: "tts"},
		refG: {Model: "tts-a", Role: "other", Aux: true},
		refB: {Model: "asr-b", Role: "asr"},
	} {
		got, ok := c.Find(ref)
		if !ok || got.Origin != models.OriginMigrated || got.Model != want.Model || got.Role != want.Role || got.Aux != want.Aux || got.Bytes == 0 {
			t.Errorf("%s: %+v", ref, got)
		}
	}
	if !f.Ready() {
		t.Error("not ready after Start")
	}
}

// Hold to talk, no shortcut, no Local API, the stable channel.
func TestManifestVariants(t *testing.T) {
	fx := conformance.New(t)
	f := newFamily(t, fx, DevBundleID)
	in := Inputs{Version: "0.4.0", Hub: fx.Hub, Shortcut: "Alt+Space", ShortcutHold: true}
	m := f.Manifest(in)
	if m.Channel != "stable" || len(m.Services) != 0 || m.Shortcuts[0].Trigger != "hold" || m.Shortcuts[0].Keys != "Option+Space" || m.Shortcuts[0].Enabled {
		t.Errorf("manifest %+v", m)
	}
	in.Shortcut, in.APIEnabled, in.APIPort = "", true, 4000
	m = f.Manifest(in)
	if len(m.Shortcuts) != 0 || len(m.Services) != 1 || m.Services[0].Auth.Type != "none" || m.Services[0].URL != "http://127.0.0.1:4000/v1" {
		t.Errorf("manifest %+v", m)
	}
	if err := f.Start(context.Background(), in, wantedOf()); err != nil {
		t.Fatal(err)
	}
	if m, err := registry.Read(fx.Family, DevBundleID); err != nil || m.Variant != actnow.Dev {
		t.Errorf("dev manifest %+v %v", m, err)
	}
}

// tree lists the files under dir with their contents.
// tree is the family directory's files and their contents, without the
// model library inside it ($FAMILY/models/hub, spec v1.0.9): models and
// their locks are not family files.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() && p == filepath.Join(dir, "models", "hub") {
			return filepath.SkipDir
		}
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(p)
			out[p] = string(b)
		}
		return nil
	})
	return out
}

// A family directory of a newer spec: AudioSmith writes nothing there,
// not even after a download.
func TestReadOnlyDirectoryWritesNothing(t *testing.T) {
	t.Run("specVersion", func(t *testing.T) { testReadOnly(t, "specVersion", "2") })
	t.Run("schema", func(t *testing.T) { testReadOnly(t, "schema", `"actnow.family/2"`) })
}

func testReadOnly(t *testing.T, key, value string) {
	fx := conformance.New(t)
	install(fx, refA, refG, refB)
	addMember(t, fx.Family.FamilyFile(), key, value)
	before := tree(t, fx.Family.Root)
	f := newFamily(t, fx, "")
	ctx := context.Background()
	if err := f.Claimed(ctx, fx.Hub, ClaimOf(testCatalog[1], refB.Repo, revB, nil, false, "")); err != nil {
		t.Fatal(err)
	}
	if err := f.Start(ctx, inputs(fx.Hub), wantedOf()); !errors.Is(err, ErrReadOnly) {
		t.Errorf("Start: %v, want ErrReadOnly", err)
	}
	f.Claimed(ctx, fx.Hub, ClaimOf(testCatalog[1], refB.Repo, revB, nil, false, ""))
	f.Release(fx.Hub, "asr-b", refB.Repo)
	f.Update(ctx, inputs(fx.Hub+"2"), wantedOf())
	if after := tree(t, fx.Family.Root); !mapsEqual(before, after) {
		t.Errorf("wrote in a read-only directory:\nbefore %v\nafter  %v", keys(before), keys(after))
	}
	if f.Ready() {
		t.Error("ready in a read-only directory")
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// When the migration fails, no manifest is written (another app keeps
// protecting AudioSmith's models by its pins table), nor any claim; the
// next launch migrates and registers.
func TestMigrateFailureWritesNoManifest(t *testing.T) {
	if runtime.GOOS != "windows" && os.Geteuid() == 0 {
		t.Skip("root writes into read-only directories")
	}
	fx := conformance.New(t)
	install(fx, refA, refG, refB)
	claims := fx.Family.ClaimsDir()
	restoreClaims := func() {
		if runtime.GOOS == "windows" {
			_ = os.Remove(claims)
			_ = os.MkdirAll(claims, 0o700)
			return
		}
		_ = os.Chmod(claims, 0o700)
	}
	if runtime.GOOS == "windows" {
		// NTFS ignores read-only mode bits on directories; replacing the
		// empty claims directory with a regular file makes writes fail.
		if err := os.Remove(claims); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(claims, []byte("block"), 0o600); err != nil {
			t.Fatal(err)
		}
	} else if err := os.Chmod(claims, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restoreClaims)
	f := newFamily(t, fx, "")
	ctx := context.Background()
	if err := f.Start(ctx, inputs(fx.Hub), wantedOf()); err == nil {
		t.Fatal("Start succeeded with an unwritable claims directory")
	}
	if _, err := os.Stat(fx.Family.AppFile(ReleaseBundleID)); !os.IsNotExist(err) {
		t.Errorf("manifest written after a failed migration: %v", err)
	}
	if f.Ready() {
		t.Error("ready after a failed migration")
	}
	restoreClaims()
	// The rest of this launch writes no claims (the next one migrates).
	f.Claimed(ctx, fx.Hub, ClaimOf(testCatalog[1], refB.Repo, revB, nil, false, ""))
	if _, err := os.Stat(fx.Family.ClaimsFile(ReleaseBundleID)); !os.IsNotExist(err) {
		t.Errorf("claims written after a failed migration: %v", err)
	}

	f2 := newFamily(t, fx, "")
	if err := f2.Start(ctx, inputs(fx.Hub), wantedOf()); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Read(fx.Family, ReleaseBundleID); err != nil {
		t.Errorf("no manifest at the next launch: %v", err)
	}
	if c := fx.Claims(ReleaseBundleID); len(c.Claims) != 3 {
		t.Errorf("claims at the next launch: %+v", c.Claims)
	}
}

// A claim failing half-way leaves no claims file: the migration is not
// marked done.
func TestMigrateRollsBack(t *testing.T) {
	fx := conformance.New(t)
	install(fx, refA, refG, refB)
	f := newFamily(t, fx, "")
	store := f.Store(fx.Hub)
	ctx, cancel := context.WithCancel(context.Background())
	wanted := wantedOf()(fx.Hub)
	// The second claim's repo lock is held exclusively by "Dicta", and
	// the context ends while the migration waits for it.
	held, err := models.TryLockRepo(fx.Hub, wanted[1].Repo, actnow.Exclusive, models.Owner{BundleID: "dev.hehehai.dicta", Op: models.OpDelete})
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	if _, err := Migrate(ctx, store, wanted); err == nil {
		t.Fatal("Migrate succeeded")
	}
	if _, err := os.Stat(fx.Family.ClaimsFile(ReleaseBundleID)); !os.IsNotExist(err) {
		t.Errorf("claims file left after a failed migration: %v", err)
	}
}

// Migrating twice claims nothing new, and a model removed from AudioSmith
// is not claimed again at the next start though its files stayed.
func TestMigrateIsIdempotent(t *testing.T) {
	fx := conformance.New(t)
	install(fx, refA, refG, refB)
	ctx := context.Background()
	f := newFamily(t, fx, "")
	if err := f.Start(ctx, inputs(fx.Hub), wantedOf()); err != nil {
		t.Fatal(err)
	}
	if n, err := Migrate(ctx, f.Store(fx.Hub), wantedOf()(fx.Hub)); n != 0 || err != nil {
		t.Errorf("second Migrate: %d %v", n, err)
	}
	if err := f.Release(fx.Hub, "asr-b", refB.Repo); err != nil {
		t.Fatal(err)
	}
	if fx.Claimed(ReleaseBundleID, refB) || !fx.Installed(refB) {
		t.Fatal("release did not drop the claim, or deleted the files")
	}
	f.Close()
	f2 := newFamily(t, fx, "")
	if err := f2.Start(ctx, inputs(fx.Hub), wantedOf()); err != nil {
		t.Fatal(err)
	}
	if fx.Claimed(ReleaseBundleID, refB) {
		t.Error("a model removed from AudioSmith was claimed again")
	}
	if !fx.Claimed(ReleaseBundleID, refA) || !fx.Claimed(ReleaseBundleID, refG) {
		t.Error("lost claims")
	}
}

// Nothing installed: an empty claims file of this folder marks the
// migration done.
func TestEmptyClaimsMarker(t *testing.T) {
	fx := conformance.New(t)
	ctx := context.Background()
	f := newFamily(t, fx, "")
	if err := f.Start(ctx, inputs(fx.Hub), wantedOf()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(fx.Family.ClaimsFile(ReleaseBundleID))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	json.Unmarshal(b, &raw)
	if string(raw["claims"]) != "[]" || !strings.Contains(string(raw["schema"]), "actnow.models.claims/1") {
		t.Errorf("marker %s", b)
	}
	if c := fx.Claims(ReleaseBundleID); !models.SameHub(c.Root, fx.Hub) || c.BundleID != ReleaseBundleID || c.App != "audiosmith" {
		t.Errorf("marker %+v", c)
	}
	// A model that appears later (another app's) is not migrated.
	install(fx, refB)
	if n, err := Migrate(ctx, f.Store(fx.Hub), wantedOf()(fx.Hub)); n != 0 || err != nil || fx.Claimed(ReleaseBundleID, refB) {
		t.Errorf("migrated again: %d %v", n, err)
	}
	// A claims file of another folder does not count: the new one is migrated.
	other := filepath.Join(t.TempDir(), "hub")
	os.MkdirAll(other, 0o755)
	if n, err := Migrate(ctx, f.Store(other), wantedOf()(other)); n != 0 || err != nil {
		t.Fatal(n, err)
	}
	if c := fx.Claims(ReleaseBundleID); !models.SameHub(c.Root, other) {
		t.Errorf("marker of the new folder: %+v", c)
	}
}
