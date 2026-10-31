package main

// Dubbing (M4, thread #19): the Dub page (/dub) works on one dub project
// at a time — source, transcript, translations, cast, generated tracks,
// exports — kept by services.Dubs (internal/dub.Project, migration 005)
// and made by its pipeline of stage jobs (owner "dub:<id>"). The page
// keeps a working copy of the open project: every edit is applied to it
// and, in the same order, to the saved document (dubSave); the saved
// document comes back with DubChanged and is adopted when no save is
// pending, which is also how the pipeline's results arrive.
//
// Files of the area:
//
//	dub.go                this: state, open/close, projects (load, save, adopt)
//	dub_models.go         wiring: models and caps, voices, translators, the TTS model
//	dub_run.go            jobs and runs: start, cancel, resume, stale tracks
//	dub_source.go         sources: file, drop, URL import (yt-dlp), peaks
//	dub_text.go           texts: errors, stages, warnings, phases, languages, hints
//	dub_edit.go           edits of the transcript with undo, paste translation, glossary, cast
//	dub_view.go           the page: header, empty state, stepper, banners, workspace
//	dub_view_player.go    the player bar, the timeline lane, the footer
//	dub_view_source.go    the Source and Transcribe panels
//	dub_view_translate.go the Translate, glossary and paste panels
//	dub_view_cast.go      the Cast and Generate panels, confirmations, Dub → Story
//	dub_editor.go         the transcript editor (virtualised rows, badges, bulk bar)
//	dub_player.go         the A/B player over the live mix, audition, the video preview
//	dub_export.go         the export panel
//	timeline.go           the timeline lane widget
//	batch*.go             /batch: the batch queue and the watch folder
//	dub_settings.go       Settings → Dubbing: URL import (yt-dlp), defaults

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/llm"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/store"
	"github.com/hehehai/AudioSmith/internal/translate"
)

// dubDefaultModel is the dub's TTS model until one is chosen: a model
// that clones (Qwen3-TTS Base, Apache-2.0).
const dubDefaultModel = "mlx-community/Qwen3-TTS-12Hz-0.6B-Base-8bit"

// dubState is the Dub page's state.
type dubState struct {
	dubSvc    *services.Dubs
	dubList   []store.DubRecord // the Dub page's projects, newest change first
	dubLoaded bool
	dub       *dub.Project // the open project (the working copy); nil = none
	dubHist   dub.History
	dubJobs   map[string]jobs.Job // unfinished jobs of the open project, by id
	dubSaving int                 // saves in flight; the saved copy is adopted at 0
	dubSaveQ  *saveQueue          // saves, in order (nil in tests: they run at once); quit.go
	dubStale  *dub.Project        // a saved copy that arrived while saves were in flight

	dubStep         int    // the step shown (dubSteps)
	dubLang         string // the target language the editor shows
	dubCur          string // the segment selected
	dubSel          map[string]bool
	dubEdit         string // the segment whose text is being edited ("" = none)
	dubEditL        string // its language ("" = the source)
	dubBuf          string // the text being edited
	dubOpen         string // the segment whose options are open
	dubList2        ui.ListState
	dubEdFocus      ui.Handle // the editor's own focus: the Edit menu's undo and redo come to it
	dubSideFocus    ui.Handle // the step's panel (the editor leaves its focus alone)
	dubWork         ui.Handle // the workspace's scroll, whose height the workspace fills
	dubWorkWin      float32   // the window's height when the workspace was laid out
	dubOpenProjects bool      // the projects popover

	dubURL         string
	dubURLMore     bool
	dubCaptions    bool
	dubCookies     string
	dubErr         string // why the last action failed (translated)
	dubErrCode     string
	dubNeed        *catalog.Entry // a model the last action found missing
	dubDismissed   map[string]bool
	dubConfirm     string // "remove", "transcribe", "delete:<id>", "story"
	dubConfirmOpen bool

	dubPaste    dubPasteState
	dubGloss    dubGlossState
	dubExport   dubExportState
	dubPlayer   dubPlayerState
	dubTL       timelineState
	dubPeaks    *dub.Peaks
	dubPeaksFor string
	dubAudition dubAuditionState
	dubVideo    dubVideoState
	dubNotice   string     // a one-time message under the toolbar ("Imported 12 cues…")
	dubSide     panelState // the step's panel: beside the workspace, or a drawer
	dubDlgNow   dubDlg     // the dialogue model as the line list shows it (dub_view_dialogue.go)
	dubNarrow   bool       // the workspace is narrow: toolbars show icons, rows fold actions
	dubWorkW    float32    // the workspace's width, as last laid out
	// dubProbes are the projects whose source is being read (Dubs.Probe);
	// dubDescOpen shows all of the video's description.
	dubProbes    map[string]bool
	dubDescOpen  bool
	dubCardAbove bool // the source's card is drawn above the step's panel (narrow)

	dubListState // dub_list.go (0.5 F2): the composer and the list

	ytdlp ytdlpState
	batch batchState
	// dubEngine replaces the engine's executors (tests: dubtest's fake).
	dubEngine func(method string) jobs.Executor
}

// The steps of the stepper.
const (
	dubStepSource = iota
	dubStepTranscribe
	dubStepTranslate
	dubStepCast
	dubStepGenerate
	dubStepExport
)

// openDubs makes the dub service over the library, follows its events and
// jobs, and reads the projects.
func (a *app) openDubs(lib *library) {
	a.dubSel = map[string]bool{}
	a.dubJobs = map[string]jobs.Job{}
	a.dubDismissed = map[string]bool{}
	a.dubTL.zoom = 1
	a.dubWant = -1
	a.dubCovers = map[string]string{}
	host := a.eng
	router := a.llmRouter
	ytdlpPath := func() string { return lib.settings.Get().Dubbing.YtDlp }
	a.dubSvc = services.NewDubs(services.DubsConfig{
		Store: lib.store.Dubs(), Bus: lib.bus, Scheduler: lib.sched, Render: lib.svc.Render,
		Documents: lib.store.Documents(),
		Engine: func(method string) jobs.Executor {
			if a.dubEngine != nil {
				return a.dubEngine(method)
			}
			if host == nil || host.sup == nil {
				return nil
			}
			return engine.Executor(host.sup, dub.RoleOf(method), method, engine.ExecutorOptions{AuxPaths: lib.auxSnapshots})
		},
		ModelPath: lib.modelSnapshot,
		Models:    func() services.DubModels { return dubModels(lib) },
		Caps:      dubCaps,
		Voice:     a.dubVoiceResolver(lib),
		Translator: func(ctx context.Context, s dub.Settings) (translate.Translator, error) {
			return dubMakeTranslator(router, s)
		},
		TranslateLane: func() jobs.Lane { return translateLane(router) }, // local_llm.go
		Extractor: func() *translate.Extractor {
			if router == nil || !router.Available(llm.SkillGlossaryExtract) {
				return nil
			}
			return &translate.Extractor{Router: router}
		},
		YtDlp: func() dub.YtDlp {
			s := lib.settings.Get()
			if !s.Dubbing.URLImport {
				return dub.YtDlp{}
			}
			y := dub.YtDlp{Path: ytdlpPath(), Proxy: ytdlpProxy(s.Models.Proxy)}
			if t := a.mediaTool(); t != nil {
				y.FFmpeg = t.FFmpeg
			} else if found := ffmpegFind(); len(found) > 0 {
				y.FFmpeg = found[0] // yt-dlp only merges streams with it
			}
			return y
		},
	})
	if a.win != nil {
		a.dubSaveQ = newSaveQueue()
	}
	core.On(lib.bus, func(ev services.DubChanged) { a.post(func() { a.dubChanged(ev) }) })
	core.On(lib.bus, func(u jobs.Updated) {
		if u.Chunk == nil && (strings.HasPrefix(u.Job.Owner, services.DubOwner) || strings.HasPrefix(u.Job.Owner, services.DubExportOwner)) {
			a.post(func() { a.dubJobUpdated(u.Job) })
		}
	})
	core.On(lib.bus, func(ev services.WatchChanged) { a.post(func() { a.batch.watch = ev.State }) })
	core.On(lib.bus, func(u jobs.Updated) {
		if u.Chunk == nil && strings.HasPrefix(u.Job.Owner, "preview:") {
			a.post(func() { a.dubAuditionUpdated(u.Job) })
		}
	})
	a.loadDubs()
	a.openBatch()
}

// startDubs resumes what needs the engine and the scheduler running: the
// watch folder, and the jobs of the open project.
func (a *app) startDubs() {
	svc := a.dubSvc
	a.background(func() { svc.RestoreWatch(context.Background()) })
}

// closeDubs stops the watch folder's loop (it resumes at the next start).
func (a *app) closeDubs() {
	if a.dubSvc != nil {
		a.dubSvc.CloseWatch()
	}
	a.dubStopPlayer()
}

// --- the projects ------------------------------------------------------------------

// loadDubs reads the Dub page's projects, then (in the background) the
// covers of the latest.
func (a *app) loadDubs() {
	svc := a.dubSvc
	a.background(func() {
		l, err := svc.List(context.Background(), store.DubQuery{Limit: 200})
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not read the dubbing projects: %v", err))
				return
			}
			a.dubList, a.dubLoaded = l, true
			a.loadDubCovers(l[:min(len(l), 40)])
		})
	})
}

// loadDubCovers reads the covers of dubs (their projects say them).
func (a *app) loadDubCovers(l []store.DubRecord) {
	svc := a.dubSvc
	a.background(func() {
		covers := map[string]string{}
		for _, r := range l {
			if p, err := svc.Get(context.Background(), r.ID); err == nil {
				if path := a.dubCoverPath(p); path != "" {
					covers[r.ID] = path
				}
			}
		}
		a.post(func() {
			for id, path := range covers {
				a.dubCovers[id] = path
			}
		})
	})
}

// openDub opens a dub's editor (/dub/{id}).
func (a *app) openDub(id string) {
	a.navigate("/dub/" + id)
	a.dubLoad(id)
}

// dubLoad makes dub id the open project, reading it in the background
// (nothing to do when it is open or being read).
func (a *app) dubLoad(id string) {
	if a.dub != nil && a.dub.ID == id || a.dubOpening == id {
		if a.dub != nil && a.dub.ID == id && a.dubWant >= 0 {
			a.dubStep, a.dubWant = a.dubWant, -1
		}
		return
	}
	a.dubOpening = id
	svc := a.dubSvc
	a.background(func() {
		p, err := svc.Get(context.Background(), id)
		active, _ := svc.Active(context.Background(), id)
		a.post(func() {
			if a.dubOpening == id {
				a.dubOpening = ""
			}
			if err != nil {
				a.toastWarn(i18n.T("Could not open the dub: %v", err))
				if a.router.Path() == "/dub/"+id {
					a.navigate("/dub")
				}
				return
			}
			a.dubShow(p, active)
		})
	})
}

// dubShow makes p the open project.
func (a *app) dubShow(p *dub.Project, active []jobs.Job) {
	a.dubStopPlayer()
	a.dub = p
	a.dubHist.Reset()
	a.dubSel, a.dubCur, a.dubEdit, a.dubOpen = map[string]bool{}, "", "", ""
	a.dubErr, a.dubErrCode, a.dubNeed, a.dubNotice = "", "", nil, ""
	a.dubDescOpen = false
	a.dubJobs = map[string]jobs.Job{}
	for _, j := range active {
		a.dubJobs[j.ID] = j
	}
	a.dubLang = p.Primary()
	a.dubStep = a.dubAutoStep()
	if a.dubWant >= 0 {
		a.dubStep, a.dubWant = a.dubWant, -1
	}
	a.dubTL = timelineState{zoom: 1}
	a.dubPeaks, a.dubPeaksFor = nil, ""
	a.dubExport = dubExportState{}
	a.dubPaste = dubPasteState{}
	a.dubLoadPeaks()
	if m := p.Settings.Model; a.replaceRemovedRepo(&m, true) { // models_removed.go: a dub clones
		a.dubSettings(func(s *dub.Settings) { s.Model = m })
	}
	if a.prefs.Dubbing.Open != p.ID {
		id := p.ID
		a.updateSettings(func(s *settings.Settings) { s.Dubbing.Open = id })
	}
}

// dubAutoStep is the step a project opens at: the first not done.
func (a *app) dubAutoStep() int {
	p := a.dub
	switch {
	case p == nil || len(p.Segments) == 0:
		return dubStepSource
	case !p.AllTranslated():
		return dubStepTranslate
	case !p.TracksReady():
		return dubStepGenerate
	}
	return dubStepExport
}

// newDub makes a project of src and opens its editor without running
// anything (the composer's dubCreate starts it too).
func (a *app) newDub(src dub.Source) {
	p := newDubProject(src, a.prefs.Dubbing, a.dubDefaultModel())
	svc := a.dubSvc
	a.async(func() {
		created, err := svc.Create(context.Background(), p)
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not make the dub: %v", err))
				return
			}
			a.dubShow(created, nil)
			a.dubPutRecord(created)
			a.navigate("/dub/" + created.ID)
			a.dubProbe(created.ID) // what the source is, before anything runs
		})
	})
}

// newDubProject is a new dub of src with Settings → Dubbing's choices
// (model: def when they name none); the API's create_dub uses it too.
func newDubProject(src dub.Source, d settings.Dubbing, def string) *dub.Project {
	p := dub.New("", time.Now())
	p.Source = src
	p.Title = strings.TrimSuffix(src.Name, filepath.Ext(src.Name))
	if src.Kind == dub.SourceURL {
		p.Title = src.Name
	}
	p.Settings.Model = firstOf(d.Model, def)
	p.Settings.Review = !d.NoReview
	if len(d.Targets) > 0 {
		p.ChooseTargets(d.Targets) // else dub.DefaultTarget, switched for a video in that language
	}
	if d.Translator != "" {
		p.Settings.Translator = d.Translator
	}
	return p
}

// dubPutRecord updates the list from a project.
func (a *app) dubPutRecord(p *dub.Project) {
	if p.Batch != "" {
		return
	}
	r := store.DubRecord{ID: p.ID, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, Title: p.Title, Source: p.Source.Path,
		Languages: p.Targets, Phase: string(p.Phase()), Duration: p.Source.Duration}
	if path := a.dubCoverPath(p); path != "" && a.dubCovers != nil {
		a.dubCovers[p.ID] = path
	}
	if i := slices.IndexFunc(a.dubList, func(x store.DubRecord) bool { return x.ID == p.ID }); i >= 0 {
		a.dubList[i] = r
	} else {
		a.dubList = append([]store.DubRecord{r}, a.dubList...)
	}
	slices.SortStableFunc(a.dubList, func(x, y store.DubRecord) int { return y.UpdatedAt.Compare(x.UpdatedAt) })
}

// dubChanged applies a saved project.
func (a *app) dubChanged(ev services.DubChanged) {
	if ev.Deleted {
		a.dubList = slices.DeleteFunc(a.dubList, func(r store.DubRecord) bool { return r.ID == ev.ID })
		if a.dub != nil && a.dub.ID == ev.ID {
			a.dubStopPlayer()
			a.dub = nil
		}
		if a.router.Path() == "/dub/"+ev.ID {
			a.navigate("/dub") // its editor was shown
		}
		a.batchDeleted(ev.ID)
		return
	}
	p := ev.Project
	if i := slices.IndexFunc(a.dubList, func(r store.DubRecord) bool { return r.ID == p.ID }); i >= 0 && p.Batch == "" &&
		a.dubList[i].Phase == string(dub.PhaseGenerating) && p.Phase() == dub.PhaseDone {
		a.notify(core.SeveritySuccess, i18n.T("Dub finished"), firstOf(p.Title, p.Source.Name), "/dub/"+p.ID)
	}
	a.dubPutRecord(p)
	a.batchChanged(p)
	if a.dub == nil || a.dub.ID != p.ID {
		return
	}
	if a.dubSaving > 0 {
		a.dubStale = p
		return
	}
	a.dubAdopt(p)
}

// dubAdopt takes the saved copy of the open project.
func (a *app) dubAdopt(p *dub.Project) {
	old := a.dub
	a.dub = p
	if len(old.Segments) > 0 && len(p.Segments) > 0 && old.Segments[0].ID != p.Segments[0].ID && old.Stage(dub.StageTranscribe, "").At != p.Stage(dub.StageTranscribe, "").At {
		a.dubHist.Reset() // a new transcript
	}
	if p.Media.Peaks != "" && a.dubPeaksFor != p.Media.Peaks {
		a.dubLoadPeaks()
	}
	if !slices.Contains(p.Languages(), a.dubLang) {
		a.dubLang = p.Primary()
	}
	if a.dubCur != "" && p.Index(a.dubCur) < 0 {
		a.dubCur = ""
	}
	// A stage that finished moves the stepper on.
	if len(old.Segments) == 0 && len(p.Segments) > 0 && a.dubStep <= dubStepTranscribe {
		a.dubStep = dubStepTranscribe
	}
}

// dubSave applies an edit to the saved project, after the edits before it.
func (a *app) dubSave(fn func(*dub.Project)) {
	if a.dub == nil {
		return
	}
	id := a.dub.ID
	svc := a.dubSvc
	a.dubSaving++
	work := func() {
		_, err := svc.Update(context.Background(), id, func(p *dub.Project) error { fn(p); return nil })
		a.post(func() {
			a.dubSaving--
			if err != nil {
				a.toastWarn(i18n.T("Could not save the dub: %v", err))
			}
			if a.dubSaving == 0 && a.dubStale != nil {
				p := a.dubStale
				a.dubStale = nil
				if a.dub != nil && a.dub.ID == p.ID {
					a.dubAdopt(p)
				}
			}
		})
	}
	if a.dubSaveQ == nil {
		work()
		return
	}
	a.dubSaveQ.push(work)
}

// dubSettings changes the open project's settings.
func (a *app) dubSettings(fn func(*dub.Settings)) {
	if a.dub == nil {
		return
	}
	fn(&a.dub.Settings)
	a.dubSave(func(p *dub.Project) { fn(&p.Settings) })
}

// dubSet changes other fields of the project (title, targets, speakers…).
func (a *app) dubSet(fn func(*dub.Project)) {
	if a.dub == nil {
		return
	}
	fn(a.dub)
	a.dubSave(fn)
}

// deleteDub deletes a project and its files.
func (a *app) deleteDub(id string) {
	svc := a.dubSvc
	a.async(func() {
		err := svc.Delete(context.Background(), id)
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not delete the dub: %v", err))
				return
			}
			a.toast(i18n.T("Dub deleted"))
		})
	})
}
