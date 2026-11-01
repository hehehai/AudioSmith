package main

// The ActNow family's shared settings in the app (internal/family
// shared.go; family spec §9, §15.2 steps 4–5): the interface language,
// the proxy, the download source and the model folder follow the
// family's settings.json unless the user keeps one for AudioSmith only.
// Shared writes the family's values into AudioSmith's settings, so every
// reader and applier works as for a change made here (applyLocale,
// resetHub, the engine's folder, the manifest and the claims of a new
// folder); AudioSmith's own changes reach the family while they follow.
//
// The model folder: when the family's differs from the folder holding
// AudioSmith's models, AudioSmith stays there and the Models page offers
// "Move models" (a cancellable job, family.MoveModels), after which it
// follows the family's folder. The views are family_shared_view.go.

import (
	"cmp"
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/family"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/actnowkit/models"
	famset "github.com/hehehai/actnowkit/settings"
)

// kindMoveModels is the job moving AudioSmith's models to the family's
// model folder.
const kindMoveModels = "family.moveModels"

// familyShared is the app's P4a state (UI thread).
type familyShared struct {
	shared *family.Shared // nil without the family (tests, the MCP relay)
	// famShared is the family's settings as last read (no I/O).
	famShared family.SharedView
	// modelsMoveJob is the running "Move models" job, when one is.
	modelsMoveJob *jobs.Job
	// modelsMoveAsk is the confirmation of Move models…, once its size is known.
	modelsMoveAsk     bool
	modelsMovePlan    modelsMove
	modelsMoveLoading bool
	// otherShown are the settings rows that showed "… uses a different
	// …" in the settings dialog now open: closing it forgets them (shown
	// once, spec §9.4).
	otherShown []string
}

// modelsMove is what Move models… would move.
type modelsMove struct {
	From, To, Value string // Value: the family's models.root ("" = the default rules)
	Models          int
	Bytes           int64
}

// modelsMoveParams are the job's parameters.
type modelsMoveParams struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Value string `json:"value"`
}

// modelsMoveResult is the job's result.
type modelsMoveResult struct {
	Moved   int      `json:"moved"`
	Reused  int      `json:"reused"`
	Skipped []string `json:"skipped,omitempty"` // repos left where they were
	By      string   `json:"by,omitempty"`      // the app that held one of them
}

// connectShared makes the shared settings' synchroniser and the move job;
// connectFamily calls it.
func (a *app) connectShared() {
	lib := a.lib
	a.shared = family.NewShared(a.fam, family.SharedOptions{Own: lib.settings, DefaultHub: hub.CacheDir, HasModels: lib.hasModels})
	sh := a.shared
	core.On(lib.bus, func(ev settings.Changed) {
		sh.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if err := sh.Changed(ctx, ev.Old, ev.New); err != nil {
				log.Printf("family: shared settings: %v", err)
			}
			a.post(a.showShared)
		})
	})
	a.registerMoveModels()
}

// startShared merges and adopts the family's settings (Shared.Start),
// after family.Start.
func (a *app) startShared() {
	sh := a.shared
	if sh == nil {
		return
	}
	sh.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := sh.Start(ctx); err != nil {
			log.Printf("family: shared settings: %v", err)
		}
		a.post(a.showShared)
	})
}

// adoptShared reads settings.json again (its notification, activation).
func (a *app) adoptShared() {
	sh := a.shared
	if sh == nil {
		return
	}
	sh.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := sh.Adopt(ctx); err != nil {
			log.Printf("family: shared settings: %v", err)
		}
		a.post(a.showShared)
	})
}

// showShared takes the family's settings for the views.
func (a *app) showShared() {
	if a.shared != nil {
		a.famShared = a.shared.View()
	}
}

// sharedUI reports whether the views may show the family's settings:
// another family app's release build is installed (spec §7.10), the
// first-run merge is done, and the family's format is one AudioSmith
// writes.
func (a *app) sharedUI() bool {
	return a.shared != nil && len(a.famOthers) > 0 && a.prefs.Family.SharedMigrated && !a.famShared.ReadOnly
}

// hasModels reports whether AudioSmith has models in hub: an installed
// model of the catalogue or one it used, or one it claims there.
func (l *library) hasModels(hub string) bool {
	return slices.ContainsFunc(l.modelsIn(hub), func(c models.Claim) bool { return !c.Aux })
}

// modelsIn are AudioSmith's installed models in hub (the catalogue's and
// those it used, with their auxiliary repos; family.Wanted) and its
// claims there.
func (l *library) modelsIn(hub string) []models.Claim {
	var out []models.Claim
	add := func(c models.Claim) {
		if !slices.ContainsFunc(out, func(x models.Claim) bool { return x.Ref() == c.Ref() }) &&
			models.Check(hub, c).State == models.Installed {
			out = append(out, c)
		}
	}
	for _, c := range l.familyWanted(hub) {
		add(c)
	}
	if l.fam != nil {
		if mine, err := l.fam.Store(hub).Claims(); err == nil && (mine.Root == "" || models.SameHub(mine.Root, hub)) {
			for _, c := range mine.Claims {
				add(c)
			}
		}
	}
	return out
}

// downloadEndpoints are the endpoints downloads race: $HF_ENDPOINT when
// set (it beats the settings and the family's download source, spec
// §9.3), else the settings' (none: hub.DefaultEndpoints).
func downloadEndpoints(s settings.Settings) []string {
	if os.Getenv("HF_ENDPOINT") != "" {
		return nil // hub.New takes it
	}
	return s.Models.Endpoints
}

// registerMoveModels registers the move job's executor.
func (a *app) registerMoveModels() {
	fam, lib := a.fam, a.lib
	lib.sched.Register(kindMoveModels, jobs.ExecutorFunc(func(ctx context.Context, job jobs.Job, report func(jobs.Progress), _ func(json.RawMessage)) (json.RawMessage, error) {
		var p modelsMoveParams
		if err := json.Unmarshal(job.Params, &p); err != nil {
			return nil, err
		}
		progress := func(mp family.MoveProgress) {
			frac := -1.0
			if mp.TotalBytes > 0 {
				frac = float64(mp.Bytes) / float64(mp.TotalBytes)
			}
			msg := ""
			if mp.Waiting {
				msg = cmp.Or(mp.WaitingFor, "?")
			}
			report(jobs.Progress{Stage: "moving", Fraction: frac, Message: msg, Bytes: mp.Bytes, TotalBytes: mp.TotalBytes})
		}
		// The models it moves are looked for there too until every one is
		// (#101 M2): a move cancelled, skipped or interrupted part-way
		// leaves some in each folder. Moving again finishes it.
		if filepath.Clean(p.To) != filepath.Clean(p.From) {
			if err := lib.settings.Update(ctx, func(s *settings.Settings) { s.Models.MovingTo = p.To }); err != nil {
				return nil, err
			}
		}
		var out modelsMoveResult
		var moved []models.Ref
		// Twice: a download that finished meanwhile is moved too.
		for pass := 0; pass < 2; pass++ {
			todo := slices.DeleteFunc(lib.modelsIn(p.From), func(c models.Claim) bool { return slices.Contains(moved, c.Ref()) })
			if len(todo) == 0 {
				break
			}
			res, err := fam.MoveModels(ctx, p.From, p.To, todo, progress)
			moved = append(moved, res.Moved...)
			out.Moved += len(res.Moved)
			out.Reused += len(res.Reused)
			for _, s := range res.Skipped {
				out.Skipped = append(out.Skipped, s.Ref.Repo)
				out.By = cmp.Or(out.By, s.By)
			}
			if err != nil {
				log.Printf("family: move models to %s: %v (%d moved)", p.To, err, out.Moved)
				return nil, err // cancelled: what moved stays there
			}
			if len(res.Skipped) > 0 {
				break
			}
		}
		log.Printf("family: moved %d models to %s (%d there already), %d left", out.Moved, p.To, out.Reused, len(out.Skipped))
		if len(out.Skipped) == 0 {
			// Every model is there: follow the family's folder.
			err := lib.settings.Update(ctx, func(s *settings.Settings) {
				s.Models.CacheDir = p.Value
				s.Models.MovingTo = ""
				s.Family.SetFollows(famset.KeyModelsRoot, true)
				delete(s.Family.OtherValue, famset.KeyModelsRoot)
			})
			if err != nil {
				return nil, err
			}
		}
		return json.Marshal(out)
	}))
	core.On(lib.bus, func(u jobs.Updated) {
		if u.Job.Kind == kindMoveModels {
			j := u.Job
			a.post(func() { a.modelsMoveUpdated(j) })
		}
	})
}

// planModelsMove finds what Move models… moves, in the background, then asks.
func (a *app) planModelsMove() {
	if a.modelsMoveLoading || a.modelsMoveJob != nil {
		return
	}
	if slices.ContainsFunc(a.activeJobs(), func(j jobs.Job) bool { return j.Kind == hub.KindDownload }) {
		a.toast(i18n.T("Wait for the downloads to finish, then move the models."))
		return
	}
	value := a.famShared.Values[famset.KeyModelsRoot]
	to := value
	if to == "" {
		to = hub.CacheDir()
	}
	from, lib := modelCache(a.prefs), a.lib
	a.modelsMoveLoading = true
	a.async(func() {
		p := modelsMove{From: from, To: to, Value: value}
		for _, c := range lib.modelsIn(from) {
			p.Bytes += models.Check(from, c).Bytes
			if !c.Aux {
				p.Models++
			}
		}
		a.post(func() {
			a.modelsMoveLoading = false
			a.modelsMovePlan, a.modelsMoveAsk = p, true
		})
	})
}

// startModelsMove submits the move job.
func (a *app) startModelsMove(p modelsMove) {
	params, _ := json.Marshal(modelsMoveParams{From: p.From, To: p.To, Value: p.Value})
	// Idempotent: a move the app quit or crashed in goes on at the next
	// start (what moved already is reused).
	spec := jobs.Spec{Kind: kindMoveModels, Lane: jobs.LaneNet, Priority: jobs.PriorityInteractive, Params: params,
		Title: i18n.T("Move models"), Owner: "family:models", Idempotent: true}
	sched := a.lib.sched
	a.async(func() {
		if _, err := sched.Submit(context.Background(), spec); err != nil {
			a.post(func() { a.toastWarn(i18n.T("Could not move the models: %v", err)) })
		}
	})
}

// cancelModelsMove cancels the running move.
func (a *app) cancelModelsMove() {
	if a.modelsMoveJob != nil {
		a.cancelJob(a.modelsMoveJob.ID)
	}
}

// modelsMoveUpdated follows the move job.
func (a *app) modelsMoveUpdated(j jobs.Job) {
	if !j.State.Terminal() && j.State != jobs.StateInterrupted {
		a.modelsMoveJob = &j
		return
	}
	a.modelsMoveJob = nil
	var out modelsMoveResult
	_ = json.Unmarshal(j.Result, &out)
	switch {
	case j.State == jobs.StateSucceeded && len(out.Skipped) == 0:
		a.toast(i18n.N(out.Moved, "Moved %d model to %s.", "Moved %d models to %s.", out.Moved, modelCache(a.prefs)))
	case j.State == jobs.StateSucceeded:
		who := cmp.Or(out.By, i18n.T("Another app"))
		a.toast(i18n.T("%s is using %s: AudioSmith keeps its models where they are for now. Move them again later.", who, listJoin(out.Skipped)))
	case j.State == jobs.StateCancelled:
		var p modelsMoveParams
		_ = json.Unmarshal(j.Params, &p)
		a.toast(i18n.T("Move cancelled. The models moved so far are in %s; AudioSmith uses them again once the others are there too.", p.To))
	case j.Err != nil:
		a.toastWarn(i18n.T("Could not move the models: %v", j.Err.Message))
	}
	a.refreshModels()
	a.refreshFamily()
}

// useOwnSetting is "Use a different setting for this app": the key
// keeps its value, for AudioSmith only.
func (a *app) useOwnSetting(key string) {
	a.updateSettings(func(s *settings.Settings) {
		s.Family.SetFollows(key, false)
		delete(s.Family.OtherValue, key)
	})
}

// followFamily is "Use theirs" and "Follow other ActNow apps": the key
// follows the family again and takes its value (Shared.Changed). The
// model folder asks to move the models first when AudioSmith has some.
func (a *app) followFamily(key string) {
	if key != famset.KeyModelsRoot {
		a.updateSettings(func(s *settings.Settings) {
			s.Family.SetFollows(key, true)
			delete(s.Family.OtherValue, key)
		})
		return
	}
	value, set := a.famShared.Values[key]
	cur, lib := modelCache(a.prefs), a.lib
	a.async(func() {
		has := set && lib.hasModels(cur) && !models.SameHub(cur, cmp.Or(value, hub.CacheDir()))
		a.post(func() {
			if has {
				a.planModelsMove()
				return
			}
			a.updateSettings(func(s *settings.Settings) {
				s.Family.SetFollows(key, true)
				delete(s.Family.OtherValue, key)
			})
		})
	})
}

// keepSeparate is the Models page's "Keep separate": the banner goes,
// AudioSmith keeps its folder.
func (a *app) keepSeparate() {
	a.updateSettings(func(s *settings.Settings) { delete(s.Family.OtherValue, famset.KeyModelsRoot) })
}

// forgetOtherShown: the settings dialog closed after rows said "…
// uses a different …": they say it once only.
func (a *app) forgetOtherShown() {
	keys := a.otherShown
	a.otherShown = nil
	if len(keys) == 0 {
		return
	}
	a.updateSettings(func(s *settings.Settings) {
		for _, k := range keys {
			if k != famset.KeyModelsRoot {
				delete(s.Family.OtherValue, k)
			}
		}
	})
}

// sharedNoun names a shared setting inside a sentence ("a different
// proxy").
func sharedNoun(key string) string {
	switch key {
	case famset.KeyLanguage:
		return i18n.X("shared setting", "language")
	case famset.KeyProxy:
		return i18n.X("shared setting", "proxy")
	case famset.KeyHFEndpoints:
		return i18n.X("shared setting", "download source")
	}
	return i18n.X("shared setting", "model folder")
}

// sharedValueText says a family value as the row would show it.
func sharedValueText(key, v string) string {
	switch key {
	case famset.KeyLanguage:
		if v == "" {
			return i18n.T("Follow the system")
		}
		return i18n.Name(v)
	case famset.KeyProxy:
		switch v {
		case "":
			return i18n.T("Automatic (system proxy)")
		case famset.ProxyDirect:
			return i18n.T("None")
		}
	case famset.KeyHFEndpoints:
		switch v {
		case famset.EndpointsAuto:
			return sourceOptions()[0]
		case famset.EndpointsOfficial:
			return sourceOptions()[1]
		case famset.EndpointsMirror:
			return sourceOptions()[2]
		}
	case famset.KeyModelsRoot:
		if v == "" {
			return hub.CacheDir()
		}
	}
	return strings.TrimSpace(v)
}
