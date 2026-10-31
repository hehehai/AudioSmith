package main

// Voice isolator (/isolate, 0.5 F2): drop an audio or video file and get
// the voices alone, the music and effects without them, and the voices
// denoised, with the models dubbing uses (services.Isolations: HTDemucs,
// DeepFilterNet). Each isolation is a job (the bell shows it) and a
// folder in the data directory; the list is read at start. This file:
// state, the service's wiring, starting, deleting, saving a stem and the
// hand-offs; isolate_view.go draws the pages.

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
)

// isolateState is the Voice isolator's state.
type isolateState struct {
	isoSvc         *services.Isolations
	isoList        []services.Isolation // newest first
	isoLoaded      bool
	isoPend        string // the composer's file ("" none)
	isoQuery       string
	isoErr         string // why the last start failed (translated)
	isoConfirm     string // the isolation the delete asks about
	isoConfirmOpen bool
	// isoEngine replaces the engine's executors (tests).
	isoEngine func(method string) jobs.Executor
}

// isoSaveDir is where tests save stems (no dialog).
var isoSaveDir string

// isolateExts are the files the page takes.
var isolateExts = dub.MediaExts

// openIsolations makes the service over the data directory, follows its
// events and reads the list.
func (a *app) openIsolations(lib *library) {
	host := a.eng
	a.isoSvc = services.NewIsolations(services.IsolationsConfig{
		Dir: filepath.Join(lib.store.Dir(), "isolations"), Bus: lib.bus, Scheduler: lib.sched, ModelPath: lib.modelSnapshot,
		Engine: func(method string) jobs.Executor {
			if a.isoEngine != nil {
				return a.isoEngine(method)
			}
			if host == nil || host.sup == nil {
				return nil
			}
			return engine.Executor(host.sup, dub.RoleOf(method), method, engine.ExecutorOptions{AuxPaths: lib.auxSnapshots})
		},
		Models: func() (string, string) { return isoSeparator().Repo, isoDenoiser().Repo },
	})
	core.On(lib.bus, func(ev services.IsolationChanged) { a.post(func() { a.isoChanged(ev) }) })
	svc := a.isoSvc
	a.background(func() {
		l, err := svc.List(context.Background())
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not read the isolations: %v", err))
				return
			}
			for _, x := range a.isoList { // added by events while reading
				if !slices.ContainsFunc(l, func(y services.Isolation) bool { return y.ID == x.ID }) {
					l = append(l, x)
				}
			}
			sortIsolations(l)
			a.isoList, a.isoLoaded = l, true
		})
	})
}

func sortIsolations(l []services.Isolation) {
	slices.SortStableFunc(l, func(x, y services.Isolation) int {
		if c := y.CreatedAt.Compare(x.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(y.ID, x.ID)
	})
}

// isoSeparator is the model that separates the voices (HTDemucs).
func isoSeparator() catalog.Entry {
	c := catalog.Builtin()
	if e, ok := c.ByID(catalog.DefaultSeparate); ok {
		return e
	}
	for _, e := range c.ForTask(catalog.TaskSeparate) {
		return e
	}
	return catalog.Entry{}
}

// isoDenoiser is the model that denoises the voices (DeepFilterNet).
func isoDenoiser() catalog.Entry {
	e, _ := catalog.Builtin().ByID(catalog.DefaultEnhance)
	return e
}

// isoMissing are the models an isolation needs that are not downloaded
// (the separator first); the separator alone blocks a start.
func (a *app) isoMissing() (missing []catalog.Entry, blocked bool) {
	for i, e := range []catalog.Entry{isoSeparator(), isoDenoiser()} {
		if e.Repo == "" {
			continue
		}
		if ready, known := a.modelOnDisk(e); known && !ready {
			missing = append(missing, e)
			blocked = blocked || i == 0
		}
	}
	return missing, blocked
}

// isoChanged applies a saved or deleted isolation.
func (a *app) isoChanged(ev services.IsolationChanged) {
	x := ev.Isolation
	i := slices.IndexFunc(a.isoList, func(y services.Isolation) bool { return y.ID == x.ID })
	switch {
	case ev.Deleted:
		if i >= 0 {
			a.isoList = slices.Delete(a.isoList, i, i+1)
		}
		if a.router.Path() == "/isolate/"+x.ID {
			a.navigate("/isolate")
		}
	case i >= 0:
		if a.isoList[i].UpdatedAt.After(x.UpdatedAt) {
			return // an older save, after a newer one
		}
		was := a.isoList[i].State
		a.isoList[i] = x
		if was != services.IsolateDone && x.State == services.IsolateDone {
			a.notify(core.SeveritySuccess, i18n.T("Voices isolated"), x.Title, "/isolate/"+x.ID)
		}
	default:
		a.isoList = append(a.isoList, x)
		sortIsolations(a.isoList)
	}
}

// isoByID is isolation id (false if there is none).
func (a *app) isoByID(id string) (services.Isolation, bool) {
	for _, x := range a.isoList {
		if x.ID == id {
			return x, true
		}
	}
	return services.Isolation{}, false
}

// isoTitleOf is isolation id's title, for the breadcrumb.
func (a *app) isoTitleOf(id string) string {
	if x, ok := a.isoByID(id); ok {
		return x.Title
	}
	return i18n.T("Isolation")
}

// isoJob is isolation id's job running or waiting (zero if none).
func (a *app) isoJob(id string) jobs.Job {
	for _, j := range a.jobList {
		if j.Owner == services.IsolateOwner+id && !j.State.Terminal() && j.State != jobs.StateInterrupted {
			return j
		}
	}
	return jobs.Job{}
}

// isoDropped holds the first audio or video file dropped.
func (a *app) isoDropped(paths []string) {
	for _, p := range paths {
		if dub.IsMedia(p, isolateExts) {
			a.isoPend = p
			return
		}
	}
	a.toast(i18n.T("Drop an audio or video file."))
}

// isoChoose asks for an audio or video file for the composer.
func (a *app) isoChoose() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: i18n.T("Choose a video or audio file"),
			Filters: []mygo.FileFilter{{Name: i18n.T("Video or audio"), Extensions: isolateExts}}})
		if err != nil || len(paths) == 0 {
			return
		}
		a.post(func() { a.isoPend = paths[0] })
	}()
}

// isoSubmit starts an isolation of the composer's file.
func (a *app) isoSubmit() {
	path := a.isoPend
	if path == "" {
		return
	}
	if _, blocked := a.isoMissing(); blocked {
		a.isoErr = i18n.T("%s is not downloaded yet.", modelRef(isoSeparator()))
		return
	}
	a.isoPend = ""
	a.isoStart(path)
}

// isoStart makes an isolation of path; its row shows at once.
func (a *app) isoStart(path string) {
	a.isoErr = ""
	svc := a.isoSvc
	a.async(func() {
		_, err := svc.Start(context.Background(), path)
		a.post(func() {
			switch {
			case errors.Is(err, core.ErrNotDownloaded):
				a.isoErr = i18n.T("%s is not downloaded yet.", modelRef(isoSeparator()))
			case err != nil:
				a.isoErr = i18n.T("Could not start: %v", err)
			}
		})
	})
}

// isoRetry starts isolation x's file again, in place of x.
func (a *app) isoRetry(x services.Isolation) {
	if !fileExists(x.Input) {
		a.toast(i18n.T("The file is gone: %s", x.Input))
		return
	}
	a.isoStart(x.Input)
	a.isoDelete(x.ID, false)
}

// isoDelete deletes isolation id and its stems.
func (a *app) isoDelete(id string, say bool) {
	a.pl.Stop()
	svc := a.isoSvc
	a.async(func() {
		err := svc.Delete(context.Background(), id)
		a.post(func() {
			switch {
			case err != nil:
				a.toastWarn(i18n.T("Could not delete: %v", err))
			case say:
				a.toast(i18n.T("Isolation deleted"))
			}
		})
	})
}

// isoStem is a stem of an isolation: its file name and what it is.
type isoStem struct {
	key, file, name, about string
}

// isoStems are x's stems written.
func isoStems(x services.Isolation) []isoStem {
	var out []isoStem
	for _, s := range []isoStem{
		{"vocals", x.Vocals, i18n.X("stem", "Voices"), i18n.T("The voices alone, as separated")},
		{"background", x.Background, i18n.T("Music and effects"), i18n.T("Everything but the voices")},
		{"clean", x.Clean, i18n.T("Voices, denoised"), i18n.T("The voices with the remaining noise removed")},
	} {
		if s.file != "" {
			out = append(out, s)
		}
	}
	return out
}

// isoSaveStem saves a copy of a stem where the user chooses.
func (a *app) isoSaveStem(x services.Isolation, s isoStem) {
	src := a.isoSvc.Path(x.ID, s.file)
	name := services.SafeFileName(x.Title+" - "+s.name, "stem") + ".wav"
	save := func(dst string) {
		if filepath.Ext(dst) == "" {
			dst += ".wav"
		}
		a.background(func() {
			err := copyFile(src, dst)
			a.post(func() {
				if err != nil {
					a.toastWarn(i18n.T("Export failed: %v", err))
				} else {
					a.toast(i18n.T("Saved %s", filepath.Base(dst)))
				}
			})
		})
	}
	if a.win == nil {
		if isoSaveDir != "" {
			save(filepath.Join(isoSaveDir, name))
		}
		return
	}
	go func() {
		dst, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.win, Title: i18n.T("Save audio as"), DefaultPath: name,
			Filters: []mygo.FileFilter{{Name: "WAV", Extensions: []string{"wav"}}}})
		if err != nil || dst == "" {
			return
		}
		a.post(func() { save(dst) })
	}()
}

// copyFile copies src to dst through a temporary file.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// isoToTranscribe transcribes x's voices (denoised when there are).
func (a *app) isoToTranscribe(x services.Isolation) {
	file := firstOf(x.Clean, x.Vocals)
	if file == "" {
		return
	}
	a.navigate("/transcripts")
	a.startTranscription(services.TranscribeRequest{Input: a.isoSvc.Path(x.ID, file), Source: core.SourceFile,
		Title: x.Title + " · " + i18n.X("stem", "Voices")})
}

// isoToDub dubs x's file (its own voices are separated again by the dub,
// which keeps the music); the voices when the file is gone.
func (a *app) isoToDub(x services.Isolation) {
	path := x.Input
	if !fileExists(path) {
		if x.Vocals == "" {
			a.toast(i18n.T("The file is gone: %s", x.Input))
			return
		}
		path = a.isoSvc.Path(x.ID, x.Vocals)
	}
	a.navigate("/dub")
	a.dubStartFile(path) // dub_list.go
}
