package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// engineSettingsSection is Settings → Engine (the Engine page before 0.5
// F1): Restart and ⋯ (the log, the model cache), the engine's processes,
// and its files.
func (a *app) engineSettingsSection(c *ui.Context) {
	t := c.Theme()
	stopped := a.engState == engineFailed
	for _, w := range a.workers {
		stopped = stopped || w.State == engine.WorkerFailed
	}
	ui.Row(c.Key("engine-actions")).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
		caption(c, i18n.T("AudioSmith runs speech models in audiosmith-engine, a helper built on mlx-audio-swift that runs on the Mac's GPU with MLX. It loads them, without the network, from the model folder Settings → Models & network downloads into (the Hugging Face cache the Python tools share).")).Wrap().Grow(1).MinWidth(0)
		restart := ghostIconButton
		if stopped {
			restart = primaryIconButton // the one thing to do now
		}
		if restart(c.Key("restart"), icons.Restart, i18n.T("Restart engine")).Label(i18n.T("Restart engine")).Shrink(0).Clicked() {
			a.restartEngine()
		}
		moreMenu(c.Key("engine-more"), "", func(m *ui.Menu) {
			if m.Item(i18n.T("Open log")).Disabled(a.logPath == "").Chosen() {
				mygo.Shell.OpenPath(a.logPath)
			}
			if m.Item(i18n.T("Open model cache")).Chosen() {
				dir := modelCache(a.prefs)
				os.MkdirAll(dir, 0o755)
				mygo.Shell.OpenPath(dir)
			}
		})
	})
	sectionTitle(c, i18n.T("Processes"))
	workers := a.workers
	if len(workers) == 0 {
		workers = []engine.WorkerStatus{{Role: proto.RoleGPU, State: engine.WorkerStarting}}
	}
	for _, w := range workers {
		a.workerCard(c, w)
	}
	sectionTitle(c, i18n.T("Files"))
	card(c.Key("engine-files")).Children(func() {
		infoGrid(c, [][2]string{
			{i18n.T("Engine"), orDash(a.eng.Path())},
			{i18n.T("Takes"), a.takeStore.AudioDir()},
			{i18n.T("Model cache"), modelCache(a.prefs)},
			{i18n.T("Log"), orDash(a.logPath)},
		})
	})
}

// workerCard shows one engine process: its state, models, memory and jobs,
// and why it stopped.
func (a *app) workerCard(c *ui.Context, w engine.WorkerStatus) {
	t := c.Theme()
	k := tok(t)
	fillCard(c.Key("worker-" + string(w.Role))).Children(func() {
		color, state := t.Warning, i18n.T("Starting")
		switch w.State {
		case engine.WorkerReady:
			color, state = t.Success, i18n.T("Ready")
		case engine.WorkerFailed:
			color, state = t.Danger, i18n.T("Stopped")
		case engine.WorkerStopped:
			// Not started yet (the listen and media workers start when
			// first needed), or stopped while quitting.
			color, state = k.G[5], i18n.T("Not running: starts when needed")
		}
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			statusDot(c, color)
			ui.Text(c, roleTitle(w.Role)).FontWeight(600).SingleLine()
			caption(c, state).SingleLine().Ellipsis("…").MinWidth(0)
		})
		var rows [][2]string
		if w.Hello != nil {
			rows = append(rows, [2]string{i18n.T("Version"), w.Hello.Version})
			if d := w.Hello.Device; d.Name != "" {
				rows = append(rows, [2]string{i18n.T("Device"), fmt.Sprintf("%s · %s · %s", d.Name, catalog.HumanSize(d.Memory), d.OS)})
			}
		}
		if w.PID != 0 {
			rows = append(rows, [2]string{i18n.T("Process"), fmt.Sprint(w.PID)})
		}
		var models []string
		if w.Stats != nil {
			for _, m := range w.Stats.Models {
				name := catalog.Builtin().DisplayName(m.Model)
				if m.Bytes > 0 {
					name += " · " + catalog.HumanSize(m.Bytes)
				}
				models = append(models, name)
			}
			rows = append(rows, [2]string{i18n.T("Memory"), i18n.T("%s in use · %s at most",
				catalog.HumanSize(w.Stats.Memory.Active), catalog.HumanSize(w.Stats.Memory.Peak))})
		}
		if w.Stats == nil && w.Role == proto.RoleGPU {
			for _, m := range a.engineSummary().Loaded {
				models = append(models, catalog.Builtin().DisplayName(m))
			}
		}
		if w.State == engine.WorkerReady {
			loaded := i18n.T("No model loaded")
			if len(models) > 0 {
				loaded = strings.Join(models, "\n")
			}
			rows = append(rows, [2]string{i18n.T("Loaded models"), loaded})
		}
		if len(w.Running) > 0 {
			rows = append(rows, [2]string{i18n.T("Jobs"), strings.Join(w.Running, "\n")})
		}
		if w.Restarts > 0 {
			rows = append(rows, [2]string{i18n.T("Restarts"), fmt.Sprint(w.Restarts)})
		}
		if len(rows) > 0 {
			infoGrid(c, rows)
		}
		if msg := failureText(w); msg != "" {
			ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Start).Children(func() {
				ui.Icon(c, icons.DangerCircle.Linear).Size(px(t, 14), px(t, 14)).TextColor(t.Danger).Shrink(0)
				ui.Text(c, msg).FontSize(px(t, fsCaption)).TextColor(t.Danger).Selectable().Grow(1).MinWidth(0)
			})
		}
		if w.StderrTail != "" && w.State == engine.WorkerFailed {
			ui.Text(c, w.StderrTail).Font(monoFont).FontSize(px(t, fsMicro)).Padding(px(t, sp3)).Radius(px(t, radiusControl)).Background(k.Bg).Selectable()
		}
	})
}

func infoGrid(c *ui.Context, rows [][2]string) {
	t := c.Theme()
	ui.Grid(c).ColumnTracks(ui.Fixed(px(t, 120)), ui.Fr(1)).GapX(px(t, sp3)).GapY(px(t, 6)).Children(func() {
		for _, r := range rows {
			ui.Text(c, r[0]).FontSize(px(t, fsCaption)).TextColor(tok(t).Text2)
			ui.Text(c, r[1]).FontSize(px(t, fsCaption)).Selectable().MinWidth(0)
		}
	})
}

// roleTitle names an engine process for people.
func roleTitle(r proto.Role) string {
	switch r {
	case proto.RoleListen:
		return i18n.T("Microphone engine")
	case proto.RoleMedia:
		return i18n.T("Media engine")
	}
	return i18n.T("Speech engine")
}
