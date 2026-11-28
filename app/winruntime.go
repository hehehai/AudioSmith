package main

// Windows native runtime discovery and on-demand download orchestration
// (internal/winruntime). On macOS ("darwin") and Linux dev builds this is a
// no-op; on Windows, lightweight native runtimes (ONNX Runtime DirectML/CPU,
// whisper.cpp, llama.cpp — ~12–55 MB each) are downloaded automatically when
// a model needs them, and can also be managed in Settings → Models & network.
// Everyday iGPU/CPU PCs are never forced to download a 2.5 GB Python/CUDA pack.

import (
	"context"
	"errors"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/winruntime"
)

// Injectable hooks so Linux/macOS unit tests can exercise the full Windows
// runtime download/status flow with an httptest server and temp directory.
var (
	newWinRuntimeManager = func(proxy string) *winruntime.Manager {
		return winruntime.New(winruntime.Options{Proxy: proxy})
	}
	discoverPythonRuntime = winruntime.DiscoverPython
)

// winRuntimeState is embedded inside modelsState (app/shell_models.go).
type winRuntimeState struct {
	mgr         *winruntime.Manager
	statuses    map[winruntime.Name]winruntime.Status
	downloading map[winruntime.Name]bool
	progress    map[winruntime.Name]winruntime.Progress
	errors      map[winruntime.Name]string
	cancels     map[winruntime.Name]context.CancelFunc
	pythonPath  string
	known       bool
}

func (a *app) winRuntimeManager() *winruntime.Manager {
	if a.winRT.mgr == nil {
		a.winRT.mgr = newWinRuntimeManager(a.prefs.Models.Proxy)
	}
	return a.winRT.mgr
}

func (a *app) winRuntimeDisplayName(name winruntime.Name) string {
	if spec, ok := a.winRuntimeManager().Spec(name); ok && spec.DisplayName != "" {
		return spec.DisplayName
	}
	return string(name)
}

// requiredWinRuntime returns the lightweight native runtime required by e on
// Windows, or "" when none is needed (external engines, or opt-in PyTorch
// models that must never trigger an automatic Python download).
func requiredWinRuntime(e catalog.Entry) winruntime.Name {
	backend := e.EffectiveBackend("windows")
	tasks := make([]string, len(e.Tasks))
	for i, t := range e.Tasks {
		tasks[i] = string(t)
	}
	return winruntime.RequiredForBackend(string(backend), tasks)
}

// refreshWinRuntimes checks what lightweight Windows native runtimes and
// optional Python worker are installed on disk.
func (a *app) refreshWinRuntimes() {
	if a.platformOS() != "windows" {
		return
	}
	mgr := a.winRuntimeManager()
	a.async(func() {
		statuses := mgr.AllStatuses()
		pyPath := discoverPythonRuntime()
		a.post(func() {
			if a.winRT.statuses == nil {
				a.winRT.statuses = map[winruntime.Name]winruntime.Status{}
			}
			for _, st := range statuses {
				a.winRT.statuses[st.Name] = st
			}
			a.winRT.pythonPath = pyPath
			a.winRT.known = true
		})
	})
}

// downloadWinRuntime downloads, verifies and extracts one managed native
// runtime, updating winRT progress state and invoking then (may be nil) on the
// UI thread when finished.
func (a *app) downloadWinRuntime(name winruntime.Name, then func(error)) {
	if a.platformOS() != "windows" {
		if then != nil {
			then(nil)
		}
		return
	}
	if a.winRT.downloading == nil {
		a.winRT.downloading = map[winruntime.Name]bool{}
		a.winRT.progress = map[winruntime.Name]winruntime.Progress{}
		a.winRT.errors = map[winruntime.Name]string{}
		a.winRT.cancels = map[winruntime.Name]context.CancelFunc{}
	}
	if a.winRT.downloading[name] {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.winRT.downloading[name] = true
	a.winRT.progress[name] = winruntime.Progress{Runtime: name, Stage: "downloading"}
	a.winRT.errors[name] = ""
	a.winRT.cancels[name] = cancel

	mgr := a.winRuntimeManager()
	display := a.winRuntimeDisplayName(name)
	a.logs.Append(core.LogEntry{Source: "winruntime", Message: "downloading " + string(name)})

	a.async(func() {
		_, err := mgr.Ensure(ctx, name, func(p winruntime.Progress) {
			a.post(func() {
				if a.winRT.progress != nil {
					a.winRT.progress[name] = p
				}
			})
		})
		st := mgr.Status(name)
		a.post(func() {
			delete(a.winRT.downloading, name)
			delete(a.winRT.cancels, name)
			if a.winRT.statuses == nil {
				a.winRT.statuses = map[winruntime.Name]winruntime.Status{}
			}
			a.winRT.statuses[name] = st
			a.winRT.known = true
			switch {
			case err == nil:
				a.logs.Append(core.LogEntry{Source: "winruntime", Level: core.SeveritySuccess, Message: "installed " + string(name)})
				a.toast(i18n.T("Installed %s", display))
			case errors.Is(err, context.Canceled):
				a.logs.Append(core.LogEntry{Source: "winruntime", Message: "cancelled " + string(name)})
			default:
				a.logs.Append(core.LogEntry{Source: "winruntime", Level: core.SeverityError, Message: string(name) + ": " + err.Error()})
				a.winRT.errors[name] = err.Error()
			}
			if then != nil {
				then(err)
			}
		})
	})
}

func (a *app) cancelWinRuntime(name winruntime.Name) {
	if cancel := a.winRT.cancels[name]; cancel != nil {
		cancel()
	}
}

func (a *app) removeWinRuntime(name winruntime.Name) {
	if a.platformOS() != "windows" || a.winRT.downloading[name] {
		return
	}
	mgr := a.winRuntimeManager()
	display := a.winRuntimeDisplayName(name)
	a.async(func() {
		err := mgr.Remove(name)
		st := mgr.Status(name)
		a.post(func() {
			if a.winRT.statuses == nil {
				a.winRT.statuses = map[winruntime.Name]winruntime.Status{}
			}
			a.winRT.statuses[name] = st
			if err != nil {
				a.toastWarn(i18n.T("Could not remove %s: %v", display, err))
				return
			}
			a.logs.Append(core.LogEntry{Source: "winruntime", Message: "removed " + string(name)})
			a.toast(i18n.T("Removed %s", display))
		})
	})
}

// ensureModelWinRuntime checks if e needs a lightweight native runtime on
// Windows before downloading/loading. If missing, it downloads and verifies
// the runtime first and invokes startModelDownload once ready, returning true.
// On non-Windows or when no native runtime is missing, it returns false so the
// caller proceeds immediately.
func (a *app) ensureModelWinRuntime(e catalog.Entry, startModelDownload func()) bool {
	if a.platformOS() != "windows" {
		return false
	}
	if e.EffectiveOptionalRuntime("windows") == "python" && discoverPythonRuntime() == "" {
		a.toast(i18n.T("%s also needs the optional Python worker on Windows.", modelRef(e)))
		return false
	}
	rt := requiredWinRuntime(e)
	if rt == "" {
		return false
	}
	mgr := a.winRuntimeManager()
	if mgr.Status(rt).Installed {
		return false
	}

	m := a.model(e)
	m.downloading, m.cancelling, m.err, m.progress, m.jobID = true, false, "", hub.Progress{Repo: e.Repo}, ""
	m.winRuntime = rt
	a.downloadWinRuntime(rt, func(err error) {
		m := a.model(e)
		m.winRuntime = ""
		if err != nil {
			m.downloading, m.cancelling = false, false
			if !errors.Is(err, context.Canceled) {
				m.err = err.Error()
				a.notify(core.SeverityError, i18n.T("Could not download %s", modelRef(e)), err.Error(), "/settings/models")
			}
			return
		}
		if m.cancelling {
			m.downloading, m.cancelling = false, false
			return
		}
		startModelDownload()
	})
	return true
}
