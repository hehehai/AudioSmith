package main

// The engine's own language model (llm.generate, docs/protocol-v2.md
// §5.4) as writing help's provider "local": registered in the llm registry
// when its catalogue entry is downloaded, and first in the automatic
// routing (then a local Ollama; cloud providers only when chosen, as
// threads/13.md decided). Settings → Writing help → "On this Mac
// (AudioSmith)" picks and downloads the model (settings.LLM.LocalModel,
// "" = catalog.DefaultLLM). A dub's translation runs on the gpu lane when
// it goes to this model, so it queues with renders instead of colliding
// with them.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/llm"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// Local LLM calls take their turn on the gpu lane (thread #53): each
// request holds the lane (jobs.Sched.Hold, interactive priority) so it
// waits for a running render instead of failing with busy, and goes
// before queued batch work. The hold is not a job: it is not persisted
// or listed (the requests are short and someone waits for them). Inside
// a gpu job (a dub's translation on the gpu lane) the context already
// holds the lane and the request goes straight to the engine. Budgets
// made with llm.WithBudget (the client's timeout, the coach's, the story
// director's, transcript clean-up's) do not count the wait; dictation
// clean-up keeps a wall-clock budget (dictation_run.go).

// localProvider is the registry id of the engine's language model.
const localProvider = "local"

// localLLMEntry is the engine's language model: the settings' choice, else
// catalog.DefaultLLM.
func (a *app) localLLMEntry() catalog.Entry {
	c := catalog.Builtin()
	if id := a.prefs.LLM.LocalModel; id != "" {
		if e, ok := c.ByID(id); ok && slices.Contains(e.Tasks, catalog.TaskLLM) {
			return e
		}
	}
	e, _ := c.ByID(catalog.DefaultLLM)
	return e
}

// localLLMOptions are the language models offered on this Mac: Qwen3 4B
// only since thread #46 (Gemma 4 E4B, E2B and Qwen3 1.7B were removed);
// Settings then names it instead of offering a select.
func localLLMOptions() []catalog.Entry {
	return catalog.Builtin().ForTask(catalog.TaskLLM)
}

// localLLMReady reports whether the engine's language model is downloaded
// (as the Models page last read it).
func (a *app) localLLMReady() bool {
	if a.models == nil {
		return false
	}
	e := a.localLLMEntry()
	if e.Repo == "" {
		return false
	}
	m := a.model(e)
	return m.known && m.local.State == hub.Downloaded
}

// registerLocalLLM registers (or removes) the engine's language model in
// the registry and reports whether it is there.
func (a *app) registerLocalLLM() bool {
	if a.llmReg == nil {
		return false
	}
	if !a.localLLMReady() {
		a.llmReg.Register(localProvider, nil)
		return false
	}
	e := a.localLLMEntry()
	var sched jobs.Scheduler
	if a.lib != nil {
		sched = a.lib.sched
	}
	a.llmReg.Register(localProvider, &llm.EngineBackend{ID: localProvider, DefaultModel: e.Repo, Call: a.localLLMCall,
		Queue:      func(ctx context.Context) (context.Context, func(), error) { return localLLMQueue(ctx, sched) },
		ListModels: func(context.Context) ([]llm.ModelInfo, error) { return []llm.ModelInfo{{ID: e.Repo}}, nil }})
	return true
}

// localLLMQueue waits for the gpu lane for one request of the engine's
// language model. No wait when the context holds the lane already (inside
// a gpu job), or when the scheduler does not run (the engine did not
// start: the request then fails at once as before). Any goroutine.
func localLLMQueue(ctx context.Context, sched jobs.Scheduler) (context.Context, func(), error) {
	h, ok := sched.(jobs.Holder)
	if !ok || jobs.Holds(ctx, jobs.LaneGPU) {
		return ctx, func() {}, nil
	}
	if st, ok := sched.(interface{ Started() bool }); ok && !st.Started() {
		return ctx, func() {}, nil
	}
	hctx, release, err := h.Hold(ctx, jobs.LaneGPU, jobs.PriorityInteractive)
	if errors.Is(err, jobs.ErrClosed) {
		err = fmt.Errorf("AudioSmith is quitting: %w", core.ErrCancelled)
	}
	return hctx, release, err
}

// localLLMCall sends llm.generate to the gpu worker, with the model's
// snapshot (the engine runs offline). Any goroutine.
func (a *app) localLLMCall(ctx context.Context, method string, params any, notify func(string, json.RawMessage)) (json.RawMessage, error) {
	if a.localLLMHook != nil {
		return a.localLLMHook(ctx, method, params, notify)
	}
	host := a.eng
	if host == nil || host.sup == nil {
		return nil, fmt.Errorf("the engine is not running: %w", core.ErrEngine)
	}
	if p, ok := params.(llm.GenerateParams); ok && p.ModelPath == "" && a.lib != nil {
		dir, err := a.lib.modelSnapshot(ctx, p.Model)
		if err != nil {
			return nil, err
		}
		p.ModelPath = dir
		params = p
	}
	return host.sup.Call(ctx, proto.RoleGPU, core.NewID(time.Now()), method, params, func(m proto.Message) {
		if notify != nil {
			notify(m.Method, m.Params)
		}
	})
}

// checkLocalLLM reads whether the engine's language model is downloaded,
// in the background; setLocal then applies it.
func (a *app) checkLocalLLM() {
	if a.lib == nil || a.models == nil {
		return
	}
	if e := a.localLLMEntry(); e.Repo != "" {
		a.refreshModel(e)
	}
}

// localLLMChanged follows the Models page: a language model was
// downloaded or deleted.
func (a *app) localLLMChanged(e catalog.Entry) {
	if slices.Contains(e.Tasks, catalog.TaskLLM) && a.llmReg != nil {
		a.applyLLM()
	}
}

// translateLane is the lane of a dub's translation: gpu when it goes to
// the engine's language model, else net. Any goroutine.
func translateLane(router *llm.Router) jobs.Lane {
	if router != nil {
		if rs := router.Routes(llm.SkillTranslate); len(rs) > 0 && rs[0].Provider == localProvider {
			return jobs.LaneGPU
		}
	}
	return jobs.LaneNet
}

// localLLMSettings is Settings → Writing help → On this Mac (AudioSmith).
func (a *app) localLLMSettings(c *ui.Context) {
	t := c.Theme()
	opts := localLLMOptions()
	if len(opts) == 0 {
		return
	}
	settingGroup(c, i18n.T("On this Mac (AudioSmith)"), func() {
		settingNote(c, "llm-local-intro", i18n.T("Runs offline inside AudioSmith: nothing leaves your Mac."))
		cur := a.localLLMEntry()
		var labels []string
		for _, e := range opts {
			labels = append(labels, i18n.T("%s (%s)", e.DisplayName(), mb(e.Size)))
		}
		i := slices.IndexFunc(opts, func(e catalog.Entry) bool { return e.ID == cur.ID })
		if i < 0 {
			i = 0
			cur = opts[0]
		}
		f := &a.llmForm
		if f.localPick == "" || !slices.Contains(labels, f.localPick) {
			f.localPick = labels[i]
		}
		m := a.model(cur)
		desc := cur.Description()
		switch {
		case m.downloading:
			pct := 0
			if m.progress.TotalBytes > 0 {
				pct = int(100 * m.progress.Bytes / m.progress.TotalBytes)
			}
			desc = i18n.T("Downloading… %d%%", pct)
		case m.known && m.local.State == hub.Downloaded:
			desc = i18n.T("Ready. Automatic writing help uses it first.")
		case m.err != "":
			desc = m.err
		}
		settingRowNote(c, "llm-engine", i18n.T("Language model"), desc, func() {
			if len(opts) == 1 {
				ui.Text(c, labels[0]).Tooltip(cur.TechnicalSummary()).SingleLine().Ellipsis("…").MinWidth(0)
			} else if ui.Select(c.Key("llm-engine-select"), &f.localPick, labels).Label(i18n.T("Language model")).Width(px(t, 260)).Changed() {
				e := opts[max(slices.Index(labels, f.localPick), 0)]
				id := e.ID
				if id == catalog.DefaultLLM {
					id = ""
				}
				a.updateSettings(func(s *settings.Settings) { s.LLM.LocalModel = id })
				a.refreshModel(e)
				a.applyLLM()
				a.syncLLMForm()
			}
			if !(m.known && m.local.State == hub.Downloaded) && !m.downloading {
				if secondaryButton(c.Key("llm-engine-download"), i18n.T("Download")).Clicked() {
					a.download(cur)
				}
			}
		})
	})
}
