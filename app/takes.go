package main

// The take library as the UI sees it: a snapshot of the library's takes,
// kept current by the services' bus events (never re-read on the main
// thread), their waveforms (peaks computed in the background and cached
// on disk) and the streaming player.

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/audio"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/family"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/licence"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/store"
	"github.com/hehehai/AudioSmith/internal/store/sqlitestore"
)

// peakCount is how many peaks a waveform draws.
const peakCount = 160

// library is the app's domain, opened once by main (openTakeStore): the
// SQLite store and the bus, the settings and the licence gate kept in it,
// the job scheduler over its jobs table, the model downloader the
// settings configure, and the services over all of them. The UI areas
// reach the domain through it; everything in it is goroutine-safe.
type library struct {
	store    *sqlitestore.Store
	bus      *core.Bus
	settings *settings.Store
	licences *licence.Gate
	sched    jobs.Scheduler
	svc      *services.Services
	peaks    audio.PeakCache
	// peakSlots bounds how many waveforms decode at once.
	peakSlots chan struct{}

	hubMu  sync.Mutex
	hubCli *hub.Client    // for the current settings; nil until needed
	fam    *family.Family // claims what downloads finish (family_app.go); nil in tests
	keys   *family.Keys   // own and shared API keys, HF token (family_keys.go); nil in tests
	// keyGone ends a key whose shared copy was deleted (family_keys_gone.go).
	keyGone func(keySlot)

	synthMu sync.Mutex
	synth   func() jobs.Executor // the engine's tts.synthesize, for renders (useEngine)
	// engineFn is the engine's executor of other gpu methods, for renders
	// that align or check what they made (useEngine).
	engineFn func(method string) jobs.Executor
	encode   func() jobs.Executor // M3: the engine's media.encode, for M4B books (useEncoder, longform.go)

	wm watermarkState // watermark_app.go (thread #14)
}

// libraryOptions adjust openLibrary for tests.
type libraryOptions struct {
	// Sched replaces the job scheduler (tests drive a fake); nil makes the
	// real one over the store's jobs table.
	Sched jobs.Scheduler
	// Async runs the services' background work; nil = goroutines.
	Async func(func())
}

// openLibrary opens the store in dataDir (migrating it, and importing the
// MVP's history.json the first time), then the domain over it (see
// library). Peaks are cached in cacheDir/peaks. Executors that need the
// engine are registered later (connectEngine), and the scheduler starts
// when the engine does (startEngine).
func openLibrary(dataDir, cacheDir string, opts ...libraryOptions) (*library, error) {
	var o libraryOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	ctx := context.Background()
	st, err := sqlitestore.Open(ctx, dataDir)
	if err != nil {
		return nil, err
	}
	if n, err := st.ImportHistoryJSON(ctx); err != nil {
		log.Printf("history.json: %v", err) // tried again at the next start
	} else if n > 0 {
		log.Printf("history.json: imported %d takes", n)
	}
	go cleanPartFiles(time.Hour, dataDir, cacheDir) // writes a crash or a quit cut short (audit A13, #101 L3)
	bus := core.NewBus()
	logJobFailures(bus)
	l := &library{
		store:     st,
		bus:       bus,
		peaks:     audio.PeakCache{Dir: filepath.Join(cacheDir, "peaks"), N: peakCount},
		peakSlots: make(chan struct{}, 2),
	}
	l.settings, err = settings.Open(ctx, st.Documents(), func(ev settings.Changed) { bus.Publish(ev) })
	if err != nil {
		log.Printf("settings: %v", err) // the defaults, until the next change saves them
	}
	core.On(bus, func(ev settings.Changed) {
		m, n := ev.Old.Models, ev.New.Models
		if !slices.Equal(m.Endpoints, n.Endpoints) || m.CacheDir != n.CacheDir || m.MovingTo != n.MovingTo || m.Proxy != n.Proxy {
			l.resetHub()
		}
	})
	l.licences = licence.New(st.Licences(), nil, bus)
	l.sched = o.Sched
	if l.sched == nil {
		l.sched = jobs.New(jobs.Options{
			Store:   st.Jobs(),
			Publish: func(u jobs.Updated) { bus.Publish(u) },
			NewID:   func() string { return core.NewID(time.Now()) },
		})
	}
	l.sched.Register(hub.KindDownload, hub.DownloadExecutor(
		func() hub.Downloader { return l.hub() },
		func(p hub.Progress) { bus.Publish(hub.DownloadProgress{Progress: p}) }))
	l.svc = services.New(services.Config{
		Store: st, Bus: bus, Scheduler: l.sched, Licences: l.licences,
		Ready: l.modelReady, Async: o.Async,
		Synth:      l.synthExecutor,
		ModelPath:  l.modelSnapshot,
		Models:     modelInfo,
		Replace:    replaceRemovedTake, // models_removed.go
		CacheDir:   cacheDir,
		KeepTakes:  func() int { return l.settings.Get().Studio.KeepTakes },
		Encode:     l.encodeExecutor,  // M3: longform.go
		Watermark:  l.watermarkRender, // watermark_app.go (thread #14)
		Engine:     l.engineExecutor,
		Aligner:    modelRepo(catalog.DefaultAlign),
		Recognizer: modelRepo(catalog.DefaultSTT),
	})
	async := o.Async
	if async == nil {
		async = func(fn func()) { go fn() }
	}
	core.On(bus, func(ev settings.Changed) {
		if n := ev.New.Studio.KeepTakes; n > 0 && (ev.Old.Studio.KeepTakes == 0 || n < ev.Old.Studio.KeepTakes) {
			async(func() {
				if _, err := l.svc.Render.Prune(context.Background()); err != nil {
					log.Printf("history: %v", err)
				}
			})
		}
	})
	return l, nil
}

// capNames are a catalogue entry's capabilities as the services name them.
func capNames(e catalog.Entry) []string {
	caps := make([]string, len(e.Capabilities))
	for i, c := range e.Capabilities {
		caps[i] = string(c)
	}
	return caps
}

// modelInfo describes a model to the services: its render traits (by
// family), pinned revision, and the languages of its language values and
// voices.
func modelInfo(repo string) services.ModelInfo {
	e := entryFor(repo)
	caps := capNames(e)
	info := services.ModelInfo{Traits: services.TraitsFor(e.Family, caps).WithCatalog(e.MaxChars, e.NonVerbal), Revision: e.Revision,
		Languages: map[string]string{}, VoiceLanguages: map[string]string{}}
	info.NeedsReference = e.NeedsReference
	if e.ID != "custom" {
		info.Label = e.Label
	}
	for _, l := range e.Languages {
		if l.Value != "" {
			info.Languages[l.Value] = l.Code
		}
	}
	for _, v := range e.Voices {
		if v.Language != "" {
			info.VoiceLanguages[v.ID] = v.Language
		}
	}
	info.Defaults = presetDefaults(e)
	return info
}

// presetDefaults are entry e's recommended parameters per workflow (the
// catalogue's presets), as the services take them.
func presetDefaults(e catalog.Entry) map[string]services.ParamDefaults {
	p, ok := catalog.PresetFor(e.ID)
	if !ok {
		return nil
	}
	f := func(v *float64) float64 {
		if v == nil {
			return 0
		}
		return *v
	}
	conv := func(pp catalog.Params) services.ParamDefaults {
		d := services.ParamDefaults{Temperature: f(pp.Temperature), TopP: f(pp.TopP), Guidance: f(pp.Guidance)}
		if pp.Steps != nil {
			d.Steps = *pp.Steps
		}
		return d
	}
	out := map[string]services.ParamDefaults{"": conv(p.ParamsFor(catalog.WorkflowStudio))}
	for _, w := range []catalog.Workflow{catalog.WorkflowStory, catalog.WorkflowAudiobook, catalog.WorkflowDub} {
		out[string(w)] = conv(p.ParamsFor(w))
	}
	return out
}

// hubToken finds the HF token for downloads (tests replace it); shared
// reads the family's shared token (nil = none: family_keys.go).
var hubToken = func(shared func() (string, error)) string {
	t, _ := hub.TokenFrom(shared, hub.DefaultKeychain)
	return t
}

// modelCache is the model folder of the settings: AudioSmith's own, or
// the family's while it follows it (written into Models.CacheDir by
// family.Shared) (spec §7.1);
// else the family's library (hub.CacheDir, spec v1.0.9).
func modelCache(s settings.Settings) string {
	if s.Models.CacheDir != "" {
		return s.Models.CacheDir
	}
	return hub.CacheDir()
}

// modelCaches are the folders models are looked for in: the model folder,
// then the target of a move that did not finish (#101 M2).
func modelCaches(s settings.Settings) []string {
	dirs := []string{modelCache(s)}
	if to := s.Models.MovingTo; to != "" && filepath.Clean(to) != filepath.Clean(dirs[0]) {
		dirs = append(dirs, to)
	}
	return dirs
}

// hub is the downloader for the current settings (folder, endpoints, proxy).
func (l *library) hub() *hub.Client {
	l.hubMu.Lock()
	defer l.hubMu.Unlock()
	if l.hubCli == nil {
		s := l.settings.Get()
		m := s.Models
		o := hub.Options{CacheDir: modelCache(s), AlsoIn: modelCaches(s)[1:], Endpoints: downloadEndpoints(s), Proxy: m.Proxy, Token: func() string { return hubToken(l.sharedHFToken()) }}
		l.familyOptions(&o)
		l.hubCli = hub.New(o)
	}
	return l.hubCli
}

// resetHub makes the next download use the settings as they are now;
// downloads running keep their client.
func (l *library) resetHub() {
	l.hubMu.Lock()
	l.hubCli = nil
	l.hubMu.Unlock()
}

// entryFor is the catalogue entry of a repo (a removed model's
// replacement: models_removed.go), or a custom entry for it.
func entryFor(repo string) catalog.Entry {
	if e, ok := catalog.Builtin().ByRepo(repo); ok {
		return e
	}
	if e, _, ok := replaceRemovedAny(repo); ok {
		return e
	}
	return catalog.Custom(repo)
}

// modelSnapshot is the local snapshot directory of a model, or an error
// wrapping core.ErrNotDownloaded. Generate checks it before queueing, and
// the engine executors fill it into the job as it starts (engines run
// offline: they load only what the hub downloaded).
func (l *library) modelSnapshot(ctx context.Context, repo string) (string, error) {
	loc, err := hub.StatusIn(modelCaches(l.settings.Get()), entryFor(repo))
	if err != nil {
		return "", err
	}
	if loc.State != hub.Downloaded {
		return "", fmt.Errorf("%s: %w", catalog.Builtin().DisplayName(repo), core.ErrNotDownloaded)
	}
	return loc.Snapshot, nil
}

// auxSnapshots are the snapshot directories of the auxiliary repos of a
// model (engine.ExecutorOptions.AuxPaths).
func (l *library) auxSnapshots(ctx context.Context, repo string) map[string]string {
	return hub.AuxSnapshotsIn(modelCaches(l.settings.Get()), entryFor(repo))
}

// modelReady is services.Config.Ready.
func (l *library) modelReady(ctx context.Context, repo string) error {
	_, err := l.modelSnapshot(ctx, repo)
	return err
}

// close writes pending drafts, waits for takes being saved and closes the store.
// Bounded (#101 L6): it runs on the main thread at quit.
func (l *library) close() {
	ctx, cancel := context.WithTimeout(context.Background(), quitStepLimit)
	defer cancel()
	if !waitFor(func() {
		if err := l.svc.Close(ctx); err != nil {
			log.Printf("library: %v", err)
		}
	}, quitStepLimit+time.Second) {
		log.Printf("library: closing the services took over %s; going on", quitStepLimit)
	}
	if err := l.store.Close(); err != nil {
		log.Printf("library: %v", err)
	}
}

// openTakeStore opens the library in the app's data directory; main
// closes it when the app quits. Without its database the app cannot keep
// takes: when it cannot be opened (a database from a newer AudioSmith, a
// missing SQLite library) main shows why and quits (showFatal).
func openTakeStore(dataDir string) (*library, error) {
	cacheDir, err := mygo.App.Path(mygo.PathCache)
	if err != nil {
		cacheDir = filepath.Join(dataDir, "cache")
	}
	lib, err := openLibrary(dataDir, cacheDir)
	if err != nil {
		return nil, fmt.Errorf("opening the library in %s: %w", dataDir, err)
	}
	return lib, nil
}

// takesState is the take library's view state.
type takesState struct {
	lib *library
	// takeStore is the store's take repository, for its pure AudioDir and
	// AudioPath (the Engine page shows the directory).
	takeStore    store.Takes
	takes        []core.Take          // snapshot, newest first
	peaks        map[string][]float32 // waveform by take id; empty when there is none
	peaksPending map[string]bool      // waveforms being computed
	pl           audio.Player
	lastClipID   string // the Studio's "Latest take"
	awaitTake    string // id of the take the Studio's job makes; "" when none
	historyList  ui.ListState
	checkOpen    map[string]bool // takes whose Check audio findings show
	checking     map[string]bool // takes being checked
	history      historyView
}

// openTakes connects the take pages to the library: it subscribes to take
// events (once, here) and loads the first snapshot and the Studio's draft.
func (a *app) openTakes(lib *library) {
	a.lib = lib
	// Renders run each chunk through the engine's tts.synthesize, on the
	// supervisor connectEngine makes (asked as each render starts).
	host := a.eng
	lib.useEngine(func() jobs.Executor {
		if host == nil || host.sup == nil {
			return nil
		}
		return engine.Executor(host.sup, proto.RoleGPU, proto.MethodTTSSynthesize, engine.ExecutorOptions{AuxPaths: lib.auxSnapshots})
	})
	lib.useEngineFor(func(method string) jobs.Executor {
		if host == nil || host.sup == nil {
			return nil
		}
		return engine.Executor(host.sup, proto.RoleGPU, method, engine.ExecutorOptions{AuxPaths: lib.auxSnapshots})
	})
	a.takeStore = lib.store.Takes()
	a.peaks = map[string][]float32{}
	a.peaksPending = map[string]bool{}
	a.checkOpen, a.checking = map[string]bool{}, map[string]bool{}
	lib.bus.Subscribe(func(ev core.Event) {
		// On the publisher's goroutine: hand the event to the main thread.
		switch ev := ev.(type) {
		case core.TakeCreated:
			a.post(func() { a.takeCreated(ev.Take) })
		case core.TakeUpdated:
			a.post(func() { a.putTake(ev.Take) })
		case core.TakeDeleted:
			a.post(func() { a.takeDeleted(ev.ID) })
		}
	}, "take.")
	a.homeFollow(lib.bus) // home_recent.go
	a.followRenders()
	a.loadTakes()
	a.restoreStudioDraft()
}

// loadTakes reads the snapshot in the background.
func (a *app) loadTakes() {
	svc := a.lib.svc.Takes
	a.background(func() {
		takes, err := svc.List(context.Background(), core.TakeQuery{})
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not read the history: %v", err))
				return
			}
			// Keep takes that events added while the list was read.
			for _, t := range a.takes {
				if !slices.ContainsFunc(takes, func(x core.Take) bool { return x.ID == t.ID }) {
					takes = append(takes, t)
				}
			}
			sortTakes(takes)
			a.takes = takes
			if a.lastClipID == "" && len(takes) > 0 {
				a.lastClipID = takes[0].ID
			}
		})
	})
}

func sortTakes(takes []core.Take) {
	slices.SortStableFunc(takes, func(x, y core.Take) int {
		if c := y.CreatedAt.Compare(x.CreatedAt); c != 0 {
			return c
		}
		return cmp.Compare(y.ID, x.ID)
	})
}

// putTake adds or replaces a take in the snapshot.
func (a *app) putTake(t core.Take) {
	if i := slices.IndexFunc(a.takes, func(x core.Take) bool { return x.ID == t.ID }); i >= 0 {
		a.takes[i] = t
		return
	}
	a.takes = append(a.takes, t)
	sortTakes(a.takes)
	if a.lastClipID == "" {
		a.lastClipID = t.ID
	}
}

// takeDeleted drops a take from the snapshot and its cached waveform.
func (a *app) takeDeleted(id string) {
	a.takes = slices.DeleteFunc(a.takes, func(t core.Take) bool { return t.ID == id })
	if a.pl.Current() == id {
		a.pl.Stop()
	}
	delete(a.peaks, id)
	if a.lastClipID == id {
		a.lastClipID = ""
	}
	peaks := a.lib.peaks
	a.background(func() { peaks.Remove(id) })
}

func (a *app) findTake(id string) *core.Take {
	for i := range a.takes {
		if a.takes[i].ID == id {
			return &a.takes[i]
		}
	}
	return nil
}

func (a *app) lastClip() *core.Take {
	if a.lastClipID == "" {
		return nil
	}
	return a.findTake(a.lastClipID)
}

// takeCreated adds a saved take to the snapshot. The take the Studio
// waits for (its last Generate) also becomes the latest take, and plays
// when autoplay is on.
func (a *app) takeCreated(t core.Take) {
	a.putTake(t)
	if t.ID != a.awaitTake {
		return
	}
	a.awaitTake = ""
	a.lastClipID = t.ID
	a.renderTake, a.renderChunk, a.renderChunks = "", 0, 0
	if a.preview != nil && a.previewID == t.ID {
		a.preview.Finish()
	}
	// A preview already playing goes on; the file plays next time.
	if a.autoplay && !a.previewStarted() {
		if err := a.pl.Toggle(t.ID, a.takeStore.AudioPath(t)); err != nil {
			a.toastWarn(i18n.T("Could not play: %v", err))
		}
	}
}

func (a *app) play(t *core.Take) {
	if err := a.pl.Toggle(t.ID, a.takeStore.AudioPath(*t)); err != nil {
		a.toastWarn(i18n.T("Could not play: %v", err))
	}
}

// deleteClip deletes a take in the background; TakeDeleted updates the views.
func (a *app) deleteClip(t *core.Take) {
	id := t.ID
	if a.pl.Current() == id {
		a.pl.Stop()
	}
	svc := a.lib.svc.Takes
	a.background(func() {
		if err := svc.Delete(context.Background(), id); err != nil {
			a.post(func() { a.toastWarn(i18n.T("Could not delete: %v", err)) })
		}
	})
}

// requestPeaks computes a take's waveform in the background, once at a
// time per take; the view draws a placeholder until it arrives.
func (a *app) requestPeaks(t *core.Take) {
	id := t.ID
	if a.peaksPending[id] {
		return
	}
	a.peaksPending[id] = true
	lib, path := a.lib, a.takeStore.AudioPath(*t)
	a.background(func() {
		lib.peakSlots <- struct{}{}
		p, err := lib.peaks.Get(id, path)
		<-lib.peakSlots
		if err != nil {
			p = []float32{} // drawn as a flat line
		}
		a.post(func() {
			delete(a.peaksPending, id)
			if a.findTake(id) != nil {
				a.peaks[id] = p
			}
		})
	})
}

// sentenceEnd is the byte index of the mark that ends s's first sentence
// ("." "!" "?" before a space or the end; "。" "！" "？" "；" anywhere), or
// -1.
func sentenceEnd(s string) int {
	for i, r := range s {
		switch r {
		case '。', '！', '？', '；':
			return i
		case '.', '!', '?', ';':
			if rest := s[i+1:]; rest == "" || rest[0] == ' ' {
				return i
			}
		}
	}
	return -1
}

// exportName is the name a take's file is exported under: its script's
// first words (without markup) up to its first sentence's end, at most 40
// characters; "AudioSmith.wav" for an empty one.
func exportName(t *core.Take) string {
	words := strings.Fields(markupRE.ReplaceAllString(t.Text, " "))
	if len(words) > 6 {
		words = words[:6]
	}
	base := strings.Join(words, " ")
	if i := sentenceEnd(base); i > 0 {
		base = base[:i] // a script without spaces (Chinese) is one "word"
	}
	base = strings.TrimSpace(strings.TrimRight(strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) {
			return '-'
		}
		return r
	}, base), ",，、:：-– "))
	if len([]rune(base)) > 40 {
		base = strings.TrimSpace(string([]rune(base)[:40]))
	}
	if base == "" {
		base = "AudioSmith"
	}
	return base + ".wav"
}
