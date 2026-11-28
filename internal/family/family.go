// Package family is AudioSmith's side of the ActNow family protocol
// (github.com/hehehai/actnowkit; spec v1.0.4, phase 1): the app's
// manifest in $FAMILY/apps, discovery of the other family apps, and its
// model claims in $FAMILY/models/claims, so that the other apps keep the
// models AudioSmith uses (spec §6, §7.3, §15.2).
//
// The data layer works whichever apps are installed; the UI shows family
// texts only when a release build of another family app is (OthersForUI,
// spec §7.10).
//
// Order at start (Start), all from a background goroutine: the
// directory (Ensure; a directory of a newer spec is read only: nothing is
// written, and AudioSmith keeps its phase-0 behaviour), then the
// migration claims (Migrate), and only after they succeeded the manifest:
// another app protects AudioSmith's models by its pins table while
// AudioSmith has no manifest, and by its claims once it has one. Then
// discovery, tombstones and the change notifications.
//
// Files: family.go (Family, Start, discovery, notifications),
// manifest.go (the manifest), migrate.go (Migrate, the wanted claims),
// claims.go (claims after downloads and deletes), legacy.go (apps without
// a manifest), credentials.go (shared API keys and the HF token),
// shared.go (the shared settings), move.go (moving the models to the
// family's model folder).
package family

import (
	"context"
	"errors"
	"log"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	actnow "github.com/hehehai/actnowkit/family"
	"github.com/hehehai/actnowkit/models"
	"github.com/hehehai/actnowkit/registry"
)

// AppID is AudioSmith's id in the family table.
const AppID = "audiosmith"

// ReleaseBundleID is the shipped app's bundle id; DevBundleID is a build
// run outside an app bundle (go run, tests), which writes its own files
// and is never "another app" (spec §6.3 step 5).
const (
	ReleaseBundleID = "dev.hehehai.audiosmith"
	DevBundleID     = ReleaseBundleID + ".dev"
)

// ErrReadOnly: the family directory belongs to a newer spec (or cannot
// be written): AudioSmith reads what it understands and writes nothing.
var ErrReadOnly = errors.New("family directory is read only")

// runningBundlePath returns the enclosing macOS .app bundle path.
// On non-macOS platforms there are no .app bundles, and calling
// actnowkit/registry.BundlePath on Windows loops infinitely at C:\
// because filepath.Dir("C:\\") == "C:\\".
func runningBundlePath() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	return registry.BundlePath()
}

// RunningBundleID is the bundle id of the running app: its Info.plist's
// when it runs from an AudioSmith .app (the release id, or a test
// variant such as ….updtest), else DevBundleID.
func RunningBundleID() string {
	if p := runningBundlePath(); p != "" {
		if id, err := registry.BundleIDAt(p); err == nil {
			if a, _, err := actnow.ParseBundleID(id); err == nil && a.ID == AppID {
				return id
			}
		}
	}
	return DevBundleID
}

// state is how far Start got.
type state int

const (
	starting state = iota // before Start's migration: claims wait
	ready                 // migrated: claims are written
	failed                // the migration failed: no claims until the next launch
	off                   // read only, or no directory: nothing is written
)

// Options configure New. Zero values take the real ones.
type Options struct {
	Dir      actnow.Dir      // zero = actnow.Default() ($ACTNOW_FAMILY_DIR or Application Support)
	BundleID string          // "" = RunningBundleID()
	System   registry.System // nil = registry.DefaultSystem
	AppPath  string          // the running .app; "" = registry.BundlePath(), else the executable
	TeamID   *string         // nil = registry.TeamID()
	Logf     func(format string, args ...any)
	// Settle is how long notifications are gathered before they are
	// handled together; 0 = 300 ms.
	Settle time.Duration
}

// Family is AudioSmith in the family directory. Its methods are safe from
// any goroutine; Start, Update and Reread do I/O and belong on a
// background goroutine.
type Family struct {
	Dir      actnow.Dir
	BundleID string

	sys     registry.System
	appPath string
	teamID  string
	logf    func(string, ...any)
	settle  time.Duration

	run sync.Mutex // serialises Start and Update (migrations, manifest writes)

	mu    sync.Mutex
	state state
	hub   string // the model folder the manifest and claims name
	// switching: Update migrates a new model folder; download claims
	// wait for it.
	switching bool
	written   Inputs // what the manifest was last written from
	hasMan    bool
	found     []registry.Found
	stores    map[string]*models.Store
	pending   []pendingClaim
	// deleted are the models other apps deleted for all apps that
	// AudioSmith had claimed (tombstones applied): repo (lower case) →
	// the deleting app's bundle id, until AudioSmith downloads them again.
	deleted  map[string]string
	watcher  *actnow.Watcher
	watching chan struct{} // closed when the watcher's goroutine ended
	onChange func(topics []string)
	// wanted is Start's: the catalogue's claims in a folder (healClaims).
	wanted func(hub string) []models.Claim
	backup atomic.Bool // models.backup, as last read (library.go)
}

// New makes the Family; it touches no file.
func New(o Options) *Family {
	f := &Family{Dir: o.Dir, BundleID: o.BundleID, sys: o.System, appPath: o.AppPath, logf: o.Logf, settle: o.Settle,
		stores: map[string]*models.Store{}, deleted: map[string]string{}}
	if f.Dir.Root == "" {
		f.Dir = actnow.Default()
	}
	if f.BundleID == "" {
		f.BundleID = RunningBundleID()
	}
	if f.sys == nil {
		f.sys = registry.DefaultSystem
	}
	if f.appPath == "" {
		f.appPath = runningBundlePath()
	}
	if f.appPath == "" {
		f.appPath, _ = os.Executable()
	}
	if o.TeamID != nil {
		f.teamID = *o.TeamID
	} else {
		f.teamID = registry.TeamID()
	}
	if f.logf == nil {
		f.logf = log.Printf
	}
	if f.settle <= 0 {
		f.settle = 300 * time.Millisecond
	}
	return f
}

// Start sets AudioSmith up in the family directory (spec §15.2): Ensure,
// the migration claims of wanted(in.Hub), then the manifest, discovery,
// tombstones and the watcher. It returns ErrReadOnly when the directory
// is a newer spec's, and the migration's error when it failed: then no
// manifest is written, and the next launch tries again.
func (f *Family) Start(ctx context.Context, in Inputs, wanted func(hub string) []models.Claim) error {
	f.run.Lock()
	defer f.run.Unlock()
	info, err := f.Dir.Ensure(f.BundleID)
	if err == nil && info.ReadOnly() {
		err = ErrReadOnly // a newer spec's directory (§4.1)
	}
	var raw actnow.Info
	if rerr := actnow.Load(f.Dir.FamilyFile(), actnow.KindFamily, 1, &raw); err == nil && errors.Is(rerr, actnow.ErrNewerSchema) {
		err = ErrReadOnly // family.json itself is a newer format
	}
	if err != nil {
		f.setState(off)
		return err
	}
	f.Dir.CleanAllTemps()
	store := f.Store(in.Hub)
	f.mu.Lock()
	f.wanted = wanted
	f.mu.Unlock()
	f.importModels(ctx, store, wanted(in.Hub)) // the library moved (spec v1.0.9)
	n, err := Migrate(ctx, store, wanted(in.Hub))
	if err != nil {
		f.setState(failed)
		f.Rediscover() // reading is harmless
		f.watch()
		return err
	}
	if n > 0 {
		f.logf("family: claimed %d models already in %s (migrated)", n, in.Hub)
	}
	f.mu.Lock()
	f.hub = in.Hub
	f.mu.Unlock()
	f.setState(ready)
	f.flushPending(ctx)
	werr := f.writeManifest(in)
	f.Rediscover()
	f.applyTombstones(store)
	f.applyBackup()
	f.watch()
	return werr
}

// Update rewrites the manifest when what it says changed (the version,
// the dictation shortcut, the Local API, the model folder); a new model
// folder is migrated first (Migrate, then the manifest, as at start).
// Before Start finished, or when it failed, Update does nothing.
func (f *Family) Update(ctx context.Context, in Inputs, wanted func(hub string) []models.Claim) error {
	f.run.Lock()
	defer f.run.Unlock()
	f.mu.Lock()
	st, hub, same := f.state, f.hub, f.hasMan && f.written == in
	f.mu.Unlock()
	if st != ready || same {
		return nil
	}
	if !models.SameHub(hub, in.Hub) {
		f.mu.Lock()
		f.switching = true
		f.mu.Unlock()
		f.importModels(ctx, f.Store(in.Hub), wanted(in.Hub))
		n, err := Migrate(ctx, f.Store(in.Hub), wanted(in.Hub))
		f.mu.Lock()
		f.switching = false
		if err == nil {
			f.hub = in.Hub
		}
		f.mu.Unlock()
		f.flushPending(ctx)
		if err != nil {
			// The manifest keeps naming the old folder; the next launch
			// migrates the new one.
			return err
		}
		f.logf("family: model folder now %s; claimed %d models there", in.Hub, n)
		f.applyBackup()
	}
	return f.writeManifest(in)
}

func (f *Family) setState(s state) {
	f.mu.Lock()
	f.state = s
	var dropped int
	if s == failed || s == off {
		dropped, f.pending = len(f.pending), nil
	}
	f.mu.Unlock()
	if dropped > 0 {
		f.logf("family: %d download claims not written (the family directory is not usable)", dropped)
	}
}

// Ready reports whether Start migrated and AudioSmith writes claims.
func (f *Family) Ready() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state == ready
}

// Store is the model library of hub as AudioSmith sees it (one per
// folder), for reading claims and decisions. Writes go through Family's
// methods, which respect a read-only directory.
func (f *Family) Store(hub string) *models.Store {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.storeLocked(hub)
}

func (f *Family) storeLocked(hub string) *models.Store {
	for h, s := range f.stores {
		if models.SameHub(h, hub) {
			return s
		}
	}
	s := &models.Store{Family: f.Dir, Hub: hub, BundleID: f.BundleID, AppID: AppID, Apps: f.states}
	f.stores[hub] = s
	return s
}

// states is models.Store.Apps: discovery as last read. Before the first
// discovery every app counts as installed (when in doubt, keep).
func (f *Family) states() map[string]models.AppState {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.found == nil {
		return nil
	}
	return registry.States(f.found)
}

// Rediscover reads the manifests and asks LaunchServices again (spec
// §6.3); on app activation and on the apps notification.
func (f *Family) Rediscover() {
	found, errs := registry.Discover(f.Dir, f.sys)
	for _, err := range errs {
		f.logf("family: discovery: %v", err)
	}
	if found == nil {
		found = []registry.Found{}
	}
	f.mu.Lock()
	f.found = found
	f.mu.Unlock()
}

// Found is the last discovery: every family app with a manifest or known
// to LaunchServices, AudioSmith's own builds included.
func (f *Family) Found() []registry.Found {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.found)
}

// OthersForUI names the other family apps the UI may mention: installed
// release builds of apps other than AudioSmith (spec §7.10: no family
// text unless one is). Dev and test builds still count for the data
// (claims protect their models), never for the UI.
func (f *Family) OthersForUI() []string {
	return othersForUI(f.Found())
}

func othersForUI(found []registry.Found) []string {
	var out []string
	for _, a := range found {
		if !a.Installed || a.Variant != actnow.Release || a.App.ID == AppID || a.App.ID == "" {
			continue
		}
		if !slices.Contains(out, a.Name) {
			out = append(out, a.Name)
		}
	}
	slices.Sort(out)
	return out
}

// OnChange sets what runs after the family directory changed (another
// app's manifest, claims, settings…) or the app was activated, with the
// topics seen (actnow.TopicApps…), once discovery and tombstones are
// reread. It runs on the watcher's goroutine.
func (f *Family) OnChange(fn func(topics []string)) {
	f.mu.Lock()
	f.onChange = fn
	f.mu.Unlock()
}

// Reread is what a notification does, for when the app becomes active
// (spec §5.4: notifications are hints, apps reread on activation).
func (f *Family) Reread() {
	f.healClaims(context.Background())
	f.handle([]string{actnow.TopicApps, actnow.TopicModels, actnow.TopicSettings})
}

// handle rereads what the topics changed and tells OnChange.
func (f *Family) handle(topics []string) {
	f.mu.Lock()
	st, hub, fn := f.state, f.hub, f.onChange
	f.mu.Unlock()
	if st == off {
		return
	}
	if slices.Contains(topics, actnow.TopicApps) {
		f.Rediscover()
	}
	if st == ready && slices.Contains(topics, actnow.TopicModels) {
		f.applyTombstones(f.Store(hub))
	}
	if st == ready && slices.Contains(topics, actnow.TopicSettings) {
		f.applyBackup() // models.backup may have changed
	}
	if fn != nil {
		fn(topics)
	}
}

// applyTombstones drops AudioSmith's claims on models another app deleted
// for all apps, and remembers them for the Models page.
func (f *Family) applyTombstones(store *models.Store) {
	ts, err := store.ApplyTombstones()
	if err != nil {
		f.logf("family: tombstones: %v", err)
		return
	}
	if len(ts) > 0 {
		f.logf("family: %d models deleted for all apps by another app", len(ts))
	}
	f.mu.Lock()
	for _, t := range ts {
		f.deleted[strings.ToLower(t.Repo)] = t.RemovedBy
	}
	f.mu.Unlock()
}

// DeletedIn names the app (a release build of another family app; spec
// §7.10) that deleted repo for all apps while AudioSmith used it, for
// "Deleted in Dicta · Download again"; "" when none did, or AudioSmith
// downloaded it again since.
func (f *Family) DeletedIn(repo string) string {
	f.mu.Lock()
	bid := f.deleted[strings.ToLower(repo)]
	f.mu.Unlock()
	if names := VisibleNames(models.User{BundleID: bid}); len(names) > 0 {
		return names[0]
	}
	return ""
}

// topics AudioSmith follows (spec §5.4).
var topics = []string{actnow.TopicApps, actnow.TopicModels, actnow.TopicSettings, actnow.TopicCredentials, actnow.TopicRuntime, actnow.TopicMemory}

// watch subscribes once to the change notifications; topics arriving
// together are handled once.
func (f *Family) watch() {
	f.mu.Lock()
	if f.watcher != nil {
		f.mu.Unlock()
		return
	}
	w, err := f.Dir.Watch(topics...)
	if err != nil {
		f.mu.Unlock()
		f.logf("family: watch: %v", err)
		return
	}
	f.watcher = w
	done := make(chan struct{})
	f.watching = done
	f.mu.Unlock()
	go func() {
		defer close(done)
		for t := range w.C {
			seen := []string{t}
			timer := time.NewTimer(f.settle)
		gather:
			for {
				select {
				case t, ok := <-w.C:
					if !ok {
						break gather
					}
					if !slices.Contains(seen, t) {
						seen = append(seen, t)
					}
				case <-timer.C:
					break gather
				}
			}
			timer.Stop()
			f.handle(seen)
		}
	}()
}

// Close stops the watcher (at quit).
func (f *Family) Close() {
	f.mu.Lock()
	w, done := f.watcher, f.watching
	f.watcher, f.watching = nil, nil
	f.mu.Unlock()
	if w != nil {
		w.Close()
		<-done // what it was handling is done: nothing runs after Close
	}
}
