package main

// The link between the UI and the engines: the protocol-2 supervisor (one
// audiosmith-engine process per role; M0 runs the gpu one) and the job
// scheduler of the library, which runs engine work as jobs. The UI
// follows its jobs through jobs.Updated and the workers through
// engine.StatusChanged, both on the library's bus.
//
// The Studio's takes are made by TakeService.Generate: their jobs are
// owned by "take:<id>", and the take arrives with TakeCreated once the
// service saved it. Preloads are the Studio's own models.load jobs, owned
// by "studio:load:…". Engines run offline: the engine executors fill in
// the local snapshot of the model (downloaded by the hub, Models page).
//
// Files: enginelink.go (the link, the workers, engine calls outside the
// queue), enginelink_run.go (the Studio's jobs), enginelink_text.go
// (failure, stage and error texts).

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

type engineState int

const (
	engineStarting engineState = iota
	engineReady
	engineFailed
)

// studioOwner prefixes the owners of the jobs the Studio submits itself
// (preloads); takeOwner those of the jobs TakeService submits.
const (
	studioOwner = "studio:"
	takeOwner   = "take:"
)

// job is the UI's view of the Studio's job: one at a time.
type job struct {
	id           string // "" until Submit returns (or its first update arrives)
	owner        string // studioOwner + …: matches updates before the id is known
	kind         string // "synthesize" or "load"
	model        string
	started      time.Time
	status       string
	fraction     float64 // 0–1, or -1 when unknown
	audioSeconds float64
	takeID       string // the take a "synthesize" job makes
	cancelling   bool
}

// engineHost is the engine side of the app: the supervisor, and the job
// scheduler (the library's) it runs jobs for. connectEngine makes the
// supervisor, unless a test set a fake.
type engineHost struct {
	bin    string // audiosmith-engine; "" when not found
	logDir string
	sup    engine.Supervisor
	sched  jobs.Scheduler
	stop   sync.Once
}

func (h *engineHost) Path() string { return h.bin }

// Stop interrupts the running jobs (they re-run or wait for Retry at the
// next start) and shuts the engines down, side by side: an engine that
// exits ends the engine jobs too. It runs on the main thread at quit, so
// it waits at most quitStopLimit (quit.go): the scheduler bounds its own
// wait for executors that ignore their cancel, the supervisor kills the
// engines that do not exit.
func (h *engineHost) Stop() {
	h.stop.Do(func() {
		var steps []func()
		if h.sched != nil {
			sched := h.sched
			steps = append(steps, func() {
				if err := sched.Close(); err != nil {
					log.Printf("jobs: %v", err)
				}
			})
		}
		if h.sup != nil {
			steps = append(steps, h.sup.Stop)
		}
		if !runBounded(quitStopLimit, steps...) {
			log.Printf("engine: stopping took over %s; going on", quitStopLimit)
		}
	})
}

// engineLink is the engine's view state.
type engineLink struct {
	eng     *engineHost
	logPath string

	workers     []engine.WorkerStatus // by role, as last published
	engState    engineState           // the gpu worker's
	engVersion  string
	engPID      int
	engError    string // translated
	engTail     string
	loaded      []string // resident models of the gpu worker, most recent first
	loadedModel string   // the model the Studio used last (or loaded), "" after unloading

	job      *job
	jobError string
	// needDownload is the model the last Generate or preload found missing:
	// the Studio offers to download it next to the error.
	needDownload *catalog.Entry

	// enginePost brings engine events to the main thread: a.post, or a
	// queue in tests that drive real engines.
	enginePost func(func())
}

// engineSummary is what the shell shows about the engines: the sidebar
// status today, the status bar and the ⌘E switcher (thread C) next. Thread
// B keeps it filled from the supervisor; the shell reads nothing else of
// the engine link.
type engineSummary struct {
	State   engineState
	Version string
	PID     int
	Err     string
	Loaded  []string // repos of resident models, most recent first
	Busy    bool     // a job runs
}

func (a *app) engineSummary() engineSummary {
	s := engineSummary{State: a.engState, Version: a.engVersion, PID: a.engPID, Err: a.engError, Busy: a.job != nil}
	s.Loaded = append(s.Loaded, a.loaded...)
	if len(s.Loaded) == 0 && a.loadedModel != "" {
		s.Loaded = []string{a.loadedModel}
	}
	return s
}

// newEngineProcess finds the engine among the app's resources.
func newEngineProcess(resources, logDir string) *engineHost {
	return &engineHost{bin: engine.Find(resources), logDir: logDir}
}

// connectEngine makes the supervisor (unless a test set one), registers
// the engine's job executors on the library's scheduler and routes engine
// and job events to the main thread. startEngine starts both.
func (a *app) connectEngine(h *engineHost) {
	a.eng = h
	if a.enginePost == nil {
		a.enginePost = a.post
	}
	if h.logDir != "" {
		a.logPath = filepath.Join(h.logDir, "engine-gpu.log")
	}
	h.sched = a.lib.sched
	if h.sup == nil {
		lib := a.lib
		ring := a.logs
		h.sup = engine.NewSupervisor(engine.Config{
			Bin: h.bin, Roles: []proto.Role{proto.RoleGPU}, AppVersion: mygo.App.Version(),
			// The microphone and media workers start when first used.
			OnDemand: []proto.Role{proto.RoleListen, proto.RoleMedia},
			CacheDir: modelCache(a.prefs), Offline: true, LogDir: h.logDir, Bus: a.bus,
			Log:          func(e core.LogEntry) { ring.Append(e) },
			MemoryBudget: gb(a.prefs.Engine.MemoryBudgetGB),
			Family:       a.engineFamily(), // family_coord.go: the ActNow family's share
			IdleUnload:   time.Duration(a.prefs.Models.AutoUnload) * time.Minute,
			// A failed load leaves an empty mlx-audio/<repo> dir behind,
			// and the next load would then wipe the repo's cache.
			// Only the gpu worker loads models: cleaning while another
			// worker starts could remove a copy the gpu worker is writing.
			Prepare: func(role proto.Role) {
				if role != proto.RoleGPU {
					return
				}
				cacheDir := modelCache(lib.settings.Get())
				removed, err := hub.CleanEngineCopies(cacheDir)
				if err != nil {
					ring.Append(core.LogEntry{Level: core.SeverityWarning, Source: "engine:gpu", Message: "cleaning the engine's model copies: " + err.Error()})
				} else if len(removed) > 0 {
					ring.Append(core.LogEntry{Source: "engine:gpu", Message: "removed incomplete model copies: " + strings.Join(removed, ", ")})
				}
			},
		})
		// Before OnQuit, where the library closes the database the
		// scheduler records the interrupted jobs in.
		// The family's runtime file goes first: the engines' last updates
		// are not written after it (family_coord.go).
		// Pending edits are written before (quit.go).
		mygo.App.OnWillQuit(func(*mygo.QuitEvent) { a.willQuit(h) })
	}
	h.sup = withMediaTools(h.sup, a.mediaTool) // M7a: ffmpeg behind the media methods (ffmpeg_engine.go)
	engine.RegisterExecutors(h.sched, h.sup, engine.ExecutorOptions{
		ModelPath: a.lib.modelSnapshot,
		AuxPaths:  a.lib.auxSnapshots,
		Licences:  a.lib.licences,
	})
	post := a.enginePost
	core.On(a.bus, func(ev engine.StatusChanged) { post(func() { a.workerChanged(ev.Status) }) })
	core.On(a.bus, func(u jobs.Updated) {
		if u.Chunk != nil {
			return
		}
		post(func() {
			a.jobsChanged(u.Job) // shell_status.go: every job, for the status bar
			if u.Job.Kind == hub.KindDownload {
				a.downloadUpdated(u.Job) // shell_models.go
			}
			if strings.HasPrefix(u.Job.Owner, studioOwner) || strings.HasPrefix(u.Job.Owner, takeOwner) {
				a.jobUpdated(u.Job)
			}
		})
	})
}

// gb converts the settings' gigabytes to bytes.
func gb(n float64) int64 { return int64(n * (1 << 30)) }

// startEngine starts the gpu worker, then the scheduler: jobs left from
// the last run (downloads, takes) continue once the engine is up. It runs
// once, when the window opens.
func (a *app) startEngine() {
	a.engState, a.engError, a.engTail = engineStarting, "", ""
	sup, sched, post := a.eng.sup, a.eng.sched, a.enginePost
	tools := a.ytdlpDir()
	a.async(func() {
		// Before the jobs left from the last run start: a yt-dlp a crash
		// left running would write the same files (#101 L4).
		if _, err := os.Stat(tools); err == nil {
			if killed := dub.KillOrphans(tools); len(killed) > 0 {
				log.Printf("yt-dlp: stopped %d runs left by an earlier AudioSmith (pids %v)", len(killed), killed)
			}
		}
		ctx := context.Background()
		err := sup.Start(ctx)
		if err == nil {
			err = sched.Start(ctx)
		}
		active, _ := sched.List(ctx, jobs.Filter{})
		workers := sup.Status()
		post(func() {
			// Workers whose events have not arrived yet (events that did
			// are newer than this snapshot may be).
			for _, st := range workers {
				known := slices.ContainsFunc(a.workers, func(w engine.WorkerStatus) bool { return w.Role == st.Role })
				if !known && st.State != engine.WorkerStopped {
					a.workerChanged(st)
				}
			}
			if err != nil {
				a.engState, a.engError = engineFailed, err.Error()
			}
			for _, j := range active {
				a.jobsChanged(j)
				if j.Kind == hub.KindDownload {
					a.downloadUpdated(j)
				}
			}
		})
	})
}

// unload frees every model of the gpu worker.
func (a *app) unload() {
	if a.job != nil || (a.loadedModel == "" && len(a.loaded) == 0) {
		return
	}
	sup, post := a.eng.sup, a.enginePost
	a.async(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := sup.Call(ctx, proto.RoleGPU, "", proto.MethodModelsUnload, proto.UnloadParams{}, nil)
		post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Engine: %s", err.Error()))
				return
			}
			a.loaded, a.loadedModel = nil, ""
			a.toast(i18n.T("Model unloaded"))
		})
	})
}

// flushEngine drops MLX's buffer cache in the gpu worker (⌘E).
func (a *app) flushEngine() {
	sup, post := a.eng.sup, a.enginePost
	a.async(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := sup.Call(ctx, proto.RoleGPU, "", proto.MethodFlush, proto.Empty{}, nil)
		post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Engine: %s", err.Error()))
				return
			}
			a.toast(i18n.T("Cache cleared"))
		})
	})
}

// restartEngine starts the gpu worker, or replaces it.
func (a *app) restartEngine() {
	a.step("restart_engine")
	a.loaded, a.loadedModel = nil, ""
	a.engState, a.engError, a.engTail = engineStarting, "", ""
	sup, post := a.eng.sup, a.enginePost
	a.async(func() {
		if err := sup.Restart(proto.RoleGPU); err != nil {
			post(func() { a.engState, a.engError = engineFailed, err.Error() })
		}
	})
}

// workerChanged applies a worker's status; it runs on the main thread.
func (a *app) workerChanged(st engine.WorkerStatus) {
	replaced := false
	for i := range a.workers {
		if a.workers[i].Role == st.Role {
			a.workers[i], replaced = st, true
		}
	}
	if !replaced {
		a.workers = append(a.workers, st)
	}
	if st.Role != proto.RoleGPU {
		return
	}
	a.engPID, a.engTail, a.engError = st.PID, st.StderrTail, failureText(st)
	a.engVersion = ""
	if st.Hello != nil {
		a.engVersion = st.Hello.Version
	}
	switch st.State {
	case engine.WorkerReady:
		a.engState = engineReady
	case engine.WorkerStarting:
		a.engState = engineStarting
	default:
		a.engState = engineFailed
	}
	if st.State != engine.WorkerReady {
		a.loaded, a.loadedModel = nil, ""
		return
	}
	if st.Stats != nil {
		a.loaded = a.loaded[:0]
		for _, m := range st.Stats.Models {
			a.loaded = append(a.loaded, m.Model)
		}
		a.loadedModel = ""
		if len(a.loaded) > 0 {
			a.loadedModel = a.loaded[0]
		}
	}
}

// Engine calls outside the job queue, for the areas (thread V's reference
// recorder, imports). Each runs on a goroutine and calls then on the main
// thread; errors explain themselves with engineErrorText.

// probeMedia describes a media file (media.probe on the media worker).
func (a *app) probeMedia(path string, then func(proto.ProbeResult, error)) {
	sup, post := a.eng.sup, a.enginePost
	a.async(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		info, err := engine.Probe(ctx, sup, path)
		post(func() { then(info, err) })
	})
}

// captureDevices lists the audio inputs, the system default first.
func (a *app) captureDevices(then func([]proto.CaptureDevice, error)) {
	sup, post := a.eng.sup, a.enginePost
	a.async(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		devs, err := engine.Devices(ctx, sup)
		post(func() { then(devs, err) })
	})
}

// startRecording records p from the microphone. level gets the meter
// (about 20 Hz) and then the started recording (or why it could not
// start), both on the main thread; stopRecording or Recording.Cancel end
// it. The recording also ends by itself (p.MaxSeconds, the device going
// away): watch Recording.Done, or call stopRecording, which is safe then.
func (a *app) startRecording(p proto.CaptureParams, level func(proto.LevelParams), then func(*engine.Recording, error)) {
	sup, post := a.eng.sup, a.enginePost
	a.async(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute) // the permission prompt waits for the user
		defer cancel()
		rec, err := engine.StartCapture(ctx, sup, "", p, func(l proto.LevelParams) {
			if level != nil {
				post(func() { level(l) })
			}
		})
		post(func() { then(rec, err) })
	})
}

// stopRecording ends rec and calls then with the file.
func (a *app) stopRecording(rec *engine.Recording, then func(proto.CaptureResult, error)) {
	post := a.enginePost
	a.async(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := rec.Stop(ctx)
		post(func() { then(res, err) })
	})
}
