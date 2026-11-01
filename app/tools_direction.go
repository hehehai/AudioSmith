package main

// The Rate fit, Direction and Probe tools; split from tools.go.

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/llm"
	"github.com/hehehai/AudioSmith/internal/translate"
)

// --- rate fit ----------------------------------------------------------------

// rateFitLangs are the languages the predictor knows, by name.
func rateFitLangs() (labels, codes []string) {
	for code := range translate.DefaultRates {
		codes = append(codes, code)
	}
	slices.SortFunc(codes, func(x, y string) int { return strings.Compare(langName(x), langName(y)) })
	for _, c := range codes {
		labels = append(labels, langName(c))
	}
	return labels, codes
}

func (a *app) runRateFit() {
	rf := &a.rf
	labels, codes := rateFitLangs()
	code := "en"
	if i := slices.Index(labels, rf.lang); i >= 0 {
		code = codes[i]
	}
	req := translate.FitRequest{Text: rf.text, Lang: code, Slot: rf.slot, Speed: rf.speed, Rewrite: rf.rewrite}
	router := a.llmRouter
	rf.busy, rf.err = true, ""
	a.async(func() {
		res, err := (&translate.Fitter{Router: router}).Check(context.Background(), req)
		a.post(func() {
			a.rf.busy = false
			if err != nil {
				a.rf.err, a.rf.res = err.Error(), nil
				return
			}
			a.rf.res = &res
		})
	})
}

// llmCan reports whether a writing-help skill has a model.
func (a *app) llmCan(s llm.Skill) bool { return a.llmRouter != nil && a.llmRouter.Available(s) }

// --- direction -------------------------------------------------------------------

func (a *app) runDirection() {
	d := &a.dir
	note := strings.TrimSpace(d.note)
	if note == "" {
		return
	}
	router := a.llmRouter
	budget := coachBudget
	d.warming = a.localLLMCold(llm.SkillDirectionParse)
	if d.warming {
		budget = coldCoachBudget // the model still loads (local_llm_warm.go)
	}
	d.busy, d.waiting = true, false
	d.run++
	run := d.run
	post := a.post
	a.async(func() {
		ctx, cancel := context.WithCancelCause(context.Background())
		defer cancel(nil)
		ctx = llm.OnWait(ctx, coachWaitWatch(cancel, func(waiting bool) {
			post(func() {
				if a.dir.run == run && a.dir.busy {
					a.dir.waiting = waiting
				}
			})
		}))
		res := (&translate.Director{Router: router, Budget: budget}).Parse(ctx, note)
		if errors.Is(context.Cause(ctx), errCoachBusy) {
			res.Fallback = coachFallbackBusy
		}
		post(func() {
			if a.dir.run != run {
				return
			}
			a.dir.busy, a.dir.waiting = false, false
			a.dir.res = &res
		})
	})
}

// The direction coach and the gpu lane (thread #53): its budget counts
// from when the model's turn comes (llm.WithBudget), but the user is
// watching, so it waits for a running render at most coachQueueWait,
// saying so after coachWaitShow, then reads the note by keywords.
var coachQueueWait = 30 * time.Second // a var for tests

const (
	coachWaitShow     = 300 * time.Millisecond
	coachFallbackBusy = "busy" // translate.Direction.Fallback: the gpu lane stayed busy
)

var errCoachBusy = errors.New("the engine stayed busy")

// coachWaitWatch is the coach's llm.OnWait observer: show(true) when a
// wait lasts coachWaitShow, show(false) when it ends, and cancel with
// errCoachBusy when it lasts coachQueueWait.
func coachWaitWatch(cancel context.CancelCauseFunc, show func(bool)) func(bool) {
	var mu sync.Mutex
	var giveUp, reveal *time.Timer
	return func(waiting bool) {
		mu.Lock()
		defer mu.Unlock()
		if giveUp != nil {
			giveUp.Stop()
			reveal.Stop()
			giveUp, reveal = nil, nil
		}
		if waiting {
			giveUp = time.AfterFunc(coachQueueWait, func() { cancel(errCoachBusy) })
			reveal = time.AfterFunc(coachWaitShow, func() { show(true) })
			return
		}
		show(false)
	}
}

// directionModel is the model the direction preview speaks with: one
// that takes instructions with a speaker (Qwen3-TTS CustomVoice), else the
// Studio's.
func directionModel(studio catalog.Entry) catalog.Entry {
	for _, e := range catalog.Builtin().ForTask(catalog.TaskTTS) {
		if e.Has(catalog.CapVoiceStyle) && len(e.Voices) > 0 {
			return e
		}
	}
	return studio
}

func (a *app) previewDirection() {
	d := &a.dir
	if d.res == nil {
		return
	}
	e := directionModel(a.preset())
	speed := 1 + d.res.Rate
	t := apiRecipe(api.SpeakRequest{ID: core.NewID(timeNow()), Text: i18n.T("I can't believe we actually made it this far."), Model: e,
		Instructions: d.res.Instruct, Speed: speed, Normalize: true}, a.fillVoice, a.lib.svc.Voices.FilePath)
	if d.preview == nil {
		d.preview = &toolRun{}
	}
	a.toolPreview(d.preview, t)
}

// --- probe -------------------------------------------------------------------------

func (a *app) chooseProbeFile() {
	a.openFile(mygo.OpenDialogOptions{Title: i18n.T("A media file")}, a.runProbe)
}

func (a *app) runProbe(path string) {
	p := &a.prb
	p.file, p.busy, p.out, p.err = path, true, "", ""
	a.probeMedia(path, func(info proto.ProbeResult, err error) {
		if a.prb.file != path {
			return
		}
		a.prb.busy = false
		if err != nil {
			a.prb.err = engineErrorText(err)
			return
		}
		b, _ := json.MarshalIndent(info, "", "  ")
		a.prb.out = string(b)
	})
}

// rateLangDefault is the rate-fit language for the UI's.
func rateLangDefault() string {
	code := "en"
	if strings.HasPrefix(i18n.Locale(), "zh") {
		code = "zh"
	}
	return langName(code)
}
