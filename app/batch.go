package main

// Batch dubbing (/batch, M4): queue N videos × M languages with the batch
// settings, each video a dub project of the batch queue (services.Dubs,
// batch "queue") that runs straight through at background priority; job
// cards per video with their stage; cancel, retry, delete, export per
// language; and a watch folder that adds new videos by itself (persisted
// across restarts).

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/oplog"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/store"
)

// batchState is the Batch page's state.
type batchState struct {
	files    []string // chosen, not queued yet
	settings services.BatchSettings
	loaded   bool
	items    []store.DubRecord
	docs     map[string]*dub.Project
	jobs     map[string]map[string]jobs.Job // unfinished jobs by project
	tab      int                            // 0 active, 1 completed, 2 failed
	watch    services.WatchState
	confirm  string // a project to delete
	errs     []string
	exports  map[string]string // export job → project id
}

// openBatch reads the batch settings and the queue.
func (a *app) openBatch() {
	b := &a.batch
	b.docs, b.jobs, b.exports = map[string]*dub.Project{}, map[string]map[string]jobs.Job{}, map[string]string{}
	b.settings = services.DefaultBatchSettings()
	a.loadBatch()
}

// loadBatch reads the settings, the queue and its projects.
func (a *app) loadBatch() {
	svc := a.dubSvc
	sched := a.lib.sched
	a.background(func() {
		ctx := context.Background()
		bs := svc.BatchSettings(ctx)
		items, err := svc.Queue(ctx)
		docs := map[string]*dub.Project{}
		act := map[string]map[string]jobs.Job{}
		for _, r := range items {
			if p, err := svc.Get(ctx, r.ID); err == nil {
				docs[r.ID] = p
			}
			if sched != nil {
				if l, err := svc.Active(ctx, r.ID); err == nil && len(l) > 0 {
					act[r.ID] = map[string]jobs.Job{}
					for _, j := range l {
						act[r.ID][j.ID] = j
					}
				}
			}
		}
		w := svc.Watch()
		a.post(func() {
			b := &a.batch
			if err != nil {
				a.toastWarn(i18n.T("Could not read the batch queue: %v", err))
				return
			}
			b.settings, b.items, b.loaded, b.watch = bs, items, true, w
			if m := bs.Model; a.replaceRemovedRepo(&m, true) { // models_removed.go: dubs clone
				a.batchSetSettings(func(s *services.BatchSettings) { s.Model = m })
			}
			for id, p := range docs {
				b.docs[id] = p
			}
			for id, m := range act {
				b.jobs[id] = m
			}
		})
	})
}

// batchChanged applies a saved batch project.
func (a *app) batchChanged(p *dub.Project) {
	b := &a.batch
	if p.Batch == "" || b.docs == nil {
		return
	}
	// Finished: the tracks are ready and the run is over. The two come in
	// separate saves (the mix's tracks, then the stage's end, which ends
	// the run), so the change of both together is what counts.
	finished := func(p *dub.Project) bool { return p.TracksReady() && p.Run == nil }
	if old := b.docs[p.ID]; old != nil && !finished(old) && finished(p) {
		a.notify(core.SeveritySuccess, i18n.T("Batch dub finished"), firstOf(p.Title, filepath.Base(p.Source.Path)), "/batch")
	}
	b.docs[p.ID] = p
	r := store.DubRecord{ID: p.ID, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, Batch: p.Batch, Title: p.Title, Source: p.Source.Path,
		Languages: p.Targets, Phase: string(p.Phase()), Duration: p.Source.Duration}
	if i := slices.IndexFunc(b.items, func(x store.DubRecord) bool { return x.ID == p.ID }); i >= 0 {
		b.items[i] = r
	} else {
		b.items = append([]store.DubRecord{r}, b.items...)
	}
}

// batchDeleted forgets a deleted project.
func (a *app) batchDeleted(id string) {
	b := &a.batch
	b.items = slices.DeleteFunc(b.items, func(r store.DubRecord) bool { return r.ID == id })
	delete(b.docs, id)
	delete(b.jobs, id)
}

// batchJobUpdated follows the queue's jobs.
func (a *app) batchJobUpdated(j jobs.Job) {
	b := &a.batch
	if b.jobs == nil {
		return
	}
	if pid, ok := b.exports[j.ID]; ok || strings.HasPrefix(j.Owner, services.DubExportOwner) {
		if !ok {
			return
		}
		switch j.State {
		case jobs.StateSucceeded:
			delete(b.exports, j.ID)
			var res services.ExportResult
			dubJSON(j.Result, &res)
			a.toast(i18n.T("Exported %s", filepath.Base(res.Output)))
		case jobs.StateFailed, jobs.StateCancelled, jobs.StateInterrupted:
			delete(b.exports, j.ID)
			a.toastWarn(dubErrorText(j.Err))
		}
		_ = pid
		return
	}
	id := strings.TrimPrefix(j.Owner, services.DubOwner)
	if _, ok := b.docs[id]; !ok {
		return
	}
	if j.State.Terminal() || j.State == jobs.StateInterrupted {
		if m := b.jobs[id]; m != nil {
			delete(m, j.ID)
			if len(m) == 0 {
				delete(b.jobs, id)
			}
		}
		return
	}
	if b.jobs[id] == nil {
		b.jobs[id] = map[string]jobs.Job{}
	}
	b.jobs[id][j.ID] = j
}

// batchStatus is where a queued project is: "active", "done" or "failed".
func (a *app) batchStatus(id string) string {
	b := &a.batch
	p := b.docs[id]
	switch {
	case len(b.jobs[id]) > 0:
		return "active"
	case p == nil:
		return "active"
	case p.TracksReady() && len(p.Failed()) == 0:
		return "done"
	case len(p.Failed()) > 0 || p.Run != nil:
		return "failed"
	}
	return "failed"
}

// batchAddFiles asks for videos.
func (a *app) batchAddFiles() {
	a.openFiles(mygo.OpenDialogOptions{Title: i18n.T("Add videos"), Multiple: true,
		Filters: fileTypes(i18n.T("Video or audio"), dub.MediaExts...)}, a.batchDropped)
}

// batchDropped adds dropped files to the list to queue.
func (a *app) batchDropped(paths []string) {
	b := &a.batch
	for _, p := range paths {
		if dub.IsMedia(p, dub.MediaExts) && !slices.Contains(b.files, p) {
			b.files = append(b.files, p)
		}
	}
}

// batchSetSettings changes the batch settings and saves them.
func (a *app) batchSetSettings(fn func(*services.BatchSettings)) {
	b := &a.batch
	fn(&b.settings)
	bs := b.settings
	svc := a.dubSvc
	a.background(func() {
		if err := svc.SetBatchSettings(context.Background(), bs); err != nil {
			a.post(func() { a.toastWarn(i18n.T("Could not save the batch settings: %v", err)) })
		}
	})
}

// batchEnqueue queues the files chosen.
func (a *app) batchEnqueue() {
	b := &a.batch
	if len(b.files) == 0 || len(b.settings.Targets) == 0 {
		return
	}
	bs := b.settings
	if bs.Model == "" {
		bs.Model = a.dubDefaultModel()
	}
	if ok, _ := a.dubTranslatorReady(dub.Settings{Translator: bs.Translator}); !ok {
		a.toast(i18n.T("Translating needs a translation engine: a local model (Ollama, LM Studio) or a provider in Settings → Writing help."))
		return
	}
	files := slices.Clone(b.files)
	svc := a.dubSvc
	b.errs = nil
	a.async(func() {
		ids, errs := svc.Enqueue(context.Background(), files, bs)
		a.post(func() {
			b.files = slices.DeleteFunc(b.files, func(f string) bool { _, failed := errs[f]; return !failed })
			for f, err := range errs {
				b.errs = append(b.errs, i18n.T("Could not add %s: %v", filepath.Base(f), err))
			}
			if len(ids) > 0 {
				b.tab = 0
				a.loadBatch()
			}
		})
	})
}

// batchCancel cancels a queued project.
func (a *app) batchCancel(id string) {
	svc := a.dubSvc
	a.async(func() { oplog.Err("batch.cancel", svc.Cancel(context.Background(), id), "id", id) })
}

// batchRetry runs a project again from where it stopped.
func (a *app) batchRetry(id string) {
	svc := a.dubSvc
	a.async(func() {
		if err := svc.Retry(context.Background(), id); err != nil {
			a.post(func() { a.toastWarn(err.Error()) })
		}
	})
}

// batchExport saves one language of a finished video: an MP4 with the
// dubbed track (WAV for audio sources), "<name>_<lang>.mp4".
func (a *app) batchExport(id, lang string) {
	p := a.batch.docs[id]
	if p == nil {
		return
	}
	format := dub.ExportMP4
	if !p.Source.HasVideo || a.dubExportBlocked(dub.ExportMP4) != "" {
		format = dub.ExportWAV
	}
	req := services.ExportRequest{Dub: id, Format: format, Lang: lang, Tracks: []string{lang}, Default: lang, Bits: 24}
	name := dub.ExportName(p.Title, lang, format)
	start := func(dst string) {
		req.Output = dst
		svc := a.dubSvc
		a.async(func() {
			jid, err := svc.ExportJob(context.Background(), req)
			a.post(func() {
				if err != nil {
					a.toastWarn(err.Error())
					return
				}
				a.batch.exports[jid] = id
			})
		})
	}
	if a.win == nil {
		if dubExportDir != "" {
			start(filepath.Join(dubExportDir, name))
		}
		return
	}
	a.saveFile(mygo.SaveDialogOptions{Title: i18n.T("Export the dub"), DefaultPath: name}, start)
}

// batchWatchChoose picks a folder to watch.
func (a *app) batchWatchChoose() {
	a.openFile(mygo.OpenDialogOptions{Title: i18n.T("Watch a folder"), Directory: true}, a.batchWatch)
}

// batchWatch starts watching dir.
func (a *app) batchWatch(dir string) {
	svc := a.dubSvc
	a.async(func() {
		err := svc.StartWatch(context.Background(), dir)
		w := svc.Watch()
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not watch the folder: %v", err))
				return
			}
			a.batch.watch = w
		})
	})
}

// batchWatchPause pauses or resumes the watch folder; batchWatchStop stops it.
func (a *app) batchWatchPause(paused bool) {
	svc := a.dubSvc
	a.batch.watch.Paused = paused
	a.async(func() { svc.PauseWatch(context.Background(), paused) })
}

func (a *app) batchWatchStop() {
	svc := a.dubSvc
	a.batch.watch = services.WatchState{}
	a.async(func() { svc.StopWatch(context.Background()) })
}
