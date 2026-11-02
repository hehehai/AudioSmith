package services

// Dub runs: RunRequest and the stage graph (kinds, lanes, titles), Run,
// Plan (which stages a run submits, in which order), Resume and
// Interrupted; split from dub.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

// RunRequest asks for the pipeline up to a stage.
type RunRequest struct {
	// Through is the last stage: dub.StageIngest (the decoded audio
	// alone: Rebuild), dub.StageReferences (the transcript),
	// StageTranslate, StageMix (the dub) or StageQC (Verify).
	Through dub.Stage
	// Langs are the languages (nil = every language the project speaks).
	Langs []string
	// Force runs these stages again although they are done (a new
	// transcript, every line translated again).
	Force []dub.Stage
	// Only re-renders these segments ("Regen"): nil = the stale ones.
	Only     []string
	Priority jobs.Priority
	// Batch: the batch queue's run (background priority, no review).
	Batch bool
}

// StageParams are a stage job's params.
type StageParams struct {
	Dub   string   `json:"dub"`
	Lang  string   `json:"lang,omitempty"`
	Force bool     `json:"force,omitempty"`
	Only  []string `json:"only,omitempty"`
}

var stageKinds = map[dub.Stage]string{
	dub.StageFetch: KindDubFetch, dub.StageIngest: KindDubIngest, dub.StageSeparate: KindDubSeparate,
	dub.StageTranscribe: KindDubTranscribe, dub.StageReferences: KindDubReferences, dub.StageTranslate: KindDubTranslate,
	dub.StageSynthesize: KindDubSynthesize, dub.StageMix: KindDubMix, dub.StageQC: KindDubQC,
}

var stageLanes = map[dub.Stage]jobs.Lane{
	dub.StageFetch: jobs.LaneNet, dub.StageIngest: jobs.LaneMedia, dub.StageSeparate: jobs.LaneGPU,
	dub.StageTranscribe: jobs.LaneGPU, dub.StageReferences: jobs.LaneGPU, dub.StageTranslate: jobs.LaneNet,
	dub.StageSynthesize: jobs.LaneGPU, dub.StageMix: jobs.LaneCPU, dub.StageQC: jobs.LaneGPU,
}

func stageTitle(st dub.Stage, title, lang string) string {
	switch st {
	case dub.StageFetch:
		return i18n.T("Dub: download %s", title)
	case dub.StageIngest:
		return i18n.T("Dub: read %s", title)
	case dub.StageSeparate:
		return i18n.T("Dub: separate voices of %s", title)
	case dub.StageTranscribe:
		return i18n.T("Dub: transcribe %s", title)
	case dub.StageReferences:
		return i18n.T("Dub: pick voices of %s", title)
	case dub.StageTranslate:
		return i18n.T("Dub: translate %s into %s", title, lang)
	case dub.StageSynthesize:
		return i18n.T("Dub: speak %s in %s", title, lang)
	case dub.StageMix:
		return i18n.T("Dub: mix %s in %s", title, lang)
	case dub.StageQC:
		return i18n.T("Dub: verify %s in %s", title, lang)
	}
	return title
}

// Run queues the stages a request needs, as one graph, and returns the
// job ids by stage key. Stages already done are skipped unless forced;
// a stage whose prerequisites are not done brings them in.
func (s *Dubs) Run(ctx context.Context, id string, req RunRequest) (map[string]string, error) {
	if s.cfg.Scheduler == nil {
		return nil, fmt.Errorf("dub: no job queue: %w", core.ErrUnsupported)
	}
	// Checking for a run and submitting one are one step (#101 M4): two
	// callers (the UI and the API, the batch queue) would otherwise both
	// start a pipeline writing the same files.
	s.runMu.Lock()
	defer s.runMu.Unlock()
	p, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if p.Source.Path == "" && p.Source.URL == "" {
		return nil, fmt.Errorf("dub %s: no source: %w", id, core.ErrInvalid)
	}
	active, _ := s.Active(ctx, id)
	if len(active) > 0 {
		return nil, fmt.Errorf("dub %s: a run is going on: %w", id, core.ErrBusy)
	}
	// The renders of lines and languages removed go now (code review
	// 1102 L4, dub_free.go): no job of the dub runs.
	s.pruneStale(p)
	nodes := s.plan(p, req)
	if len(nodes) == 0 {
		return map[string]string{}, nil
	}
	// The run is recorded before its jobs start (#101 L12): a run whose
	// stages all end before a later Update would keep Run set for good,
	// and look interrupted.
	prev := p.Run
	if _, err := s.Update(ctx, id, func(p *dub.Project) error {
		p.Run = &dub.Run{Through: req.Through, Langs: req.Langs, At: s.cfg.Now(), Batch: req.Batch}
		return nil
	}); err != nil {
		return nil, err
	}
	ids, err := s.cfg.Scheduler.SubmitGraph(ctx, nodes)
	if err != nil {
		s.Update(context.WithoutCancel(ctx), id, func(p *dub.Project) error { p.Run = prev; return nil })
		return nil, err
	}
	return ids, nil
}

// Plan lists the stage keys a request would run (for the UI's summary).
func (s *Dubs) Plan(p *dub.Project, req RunRequest) []string {
	var out []string
	for _, n := range s.plan(p, req) {
		out = append(out, n.Key)
	}
	return out
}

func (s *Dubs) plan(p *dub.Project, req RunRequest) []jobs.Node {
	done := func(st dub.Stage, lang string) bool {
		if slices.Contains(req.Force, st) {
			return false
		}
		// Files a freed dub lacks (dub_free.go) are made again.
		if st == dub.StageIngest && s.audioGone(p) || st == dub.StageSeparate && s.stemsGone(p) {
			return false
		}
		switch p.Stage(st, lang).Status {
		case dub.StatusDone, dub.StatusSkipped:
			return true
		}
		return false
	}
	order := map[dub.Stage]int{dub.StageReferences: 1, dub.StageTranslate: 2, dub.StageSynthesize: 3, dub.StageMix: 3, dub.StageQC: 4}
	through := order[req.Through]
	if through == 0 {
		through = 3
	}
	prio := req.Priority
	if req.Batch && prio == 0 {
		prio = jobs.PriorityBackground
	}
	var nodes []jobs.Node
	add := func(st dub.Stage, lang string, after []string, only []string) string {
		key := dub.StageKey(st, lang)
		params, _ := json.Marshal(StageParams{Dub: p.ID, Lang: lang, Force: slices.Contains(req.Force, st), Only: only})
		lane := stageLanes[st]
		if st == dub.StageTranslate && s.cfg.TranslateLane != nil {
			lane = s.cfg.TranslateLane()
		}
		nodes = append(nodes, jobs.Node{Key: key, Spec: jobs.Spec{
			Kind: stageKinds[st], Lane: lane, Priority: prio, Params: params, Title: stageTitle(st, p.Title, lang),
			Owner: DubOwner + p.ID, After: after, MaxAttempts: 2, Idempotent: true,
		}})
		return key
	}
	var last []string
	chain := func(st dub.Stage) {
		if !done(st, "") {
			last = []string{add(st, "", last, nil)}
		}
	}
	// QC alone checks tracks that exist.
	if req.Through == dub.StageQC {
		for _, l := range langsOf(p, req.Langs) {
			after := []string(nil)
			if p.Tracks[l].File == "" {
				after = []string{add(dub.StageSynthesize, l, nil, nil)}
				after = []string{add(dub.StageMix, l, after, nil)}
			}
			add(dub.StageQC, l, after, nil)
		}
		return nodes
	}
	if p.Source.Kind == dub.SourceURL && p.Source.Path == "" {
		chain(dub.StageFetch)
	}
	chain(dub.StageIngest)
	if req.Through == dub.StageIngest { // Rebuild: the audio alone
		return nodes
	}
	if p.Settings.KeepBackground || slices.Contains(req.Force, dub.StageSeparate) || s.stemsGone(p) {
		chain(dub.StageSeparate)
	}
	transcribing := false
	if len(p.Segments) == 0 || slices.Contains(req.Force, dub.StageTranscribe) {
		last = []string{add(dub.StageTranscribe, "", last, nil)}
		transcribing = true
	}
	refsStale := false
	for _, sp := range p.SpeakerLabels() {
		if !slices.Contains(p.RefTried, sp) {
			refsStale = true
		}
	}
	if transcribing || refsStale || !done(dub.StageReferences, "") {
		last = []string{add(dub.StageReferences, "", last, nil)}
	}
	if through < 2 {
		return nodes
	}
	prep := last
	for _, l := range langsOf(p, req.Langs) {
		after := prep
		if len(p.Untranslated(l)) > 0 || slices.Contains(req.Force, dub.StageTranslate) || len(p.Segments) == 0 {
			after = []string{add(dub.StageTranslate, l, prep, nil)}
		}
		if through < 3 {
			continue
		}
		after = []string{add(dub.StageSynthesize, l, after, req.Only)}
		add(dub.StageMix, l, after, nil)
	}
	return nodes
}

// langsOf are the languages a request runs in.
func langsOf(p *dub.Project, langs []string) []string {
	if len(langs) > 0 {
		return langs
	}
	return p.Languages()
}

// Resume continues an interrupted run (Project.Run) where it stopped.
func (s *Dubs) Resume(ctx context.Context, id string) (map[string]string, error) {
	p, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if p.Run == nil {
		return nil, fmt.Errorf("dub %s: nothing to resume: %w", id, core.ErrInvalid)
	}
	return s.Run(ctx, id, RunRequest{Through: p.Run.Through, Langs: p.Run.Langs, Batch: p.Run.Batch})
}

// Interrupted reports whether p's last run stopped without finishing (no
// job of it is left, yet it did not get through).
func Interrupted(p *dub.Project, active []jobs.Job) bool {
	if p.Run == nil || len(active) > 0 {
		return false
	}
	for _, st := range p.Stages {
		if st.Status == dub.StatusRunning || st.Status == dub.StatusFailed {
			return true
		}
	}
	return false
}
