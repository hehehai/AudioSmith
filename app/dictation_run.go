package main

// A dictation's run: start (and preload), stop, transcribe, deliver the
// text into the app in front or the clipboard, fail, end and cancel;
// split from dictation.go.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dictation"
	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/llm"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/translate"
)

// dictStart records a new dictation for target.
func (a *app) dictStart(target dictation.Target, hold bool) {
	a.dictGen++
	gen := a.dictGen
	out := filepath.Join(a.lib.store.Dir(), "dictation", core.NewID(time.Now())+".wav")
	s := &dictSession{gen: gen, target: target, phase: dictStarting, hold: hold, pressed: time.Now(), out: out, level: -120}
	a.dict = s
	// Its models stay loaded until it ends, however long the user speaks
	// (#109): the recogniser, and the engine's language model when it
	// cleans up; not the gpu worker's other models (code review 1102 M1).
	asr := a.holdModelsOf(proto.RoleListen, a.dictModel().Repo)
	s.release = asr
	if a.prefs.Dictation.Cleanup && a.localLLMFirst(llm.SkillDictationCleanup) {
		cleanup := a.holdModelsOf(proto.RoleGPU, a.localLLMEntry().Repo)
		s.release = func() { asr(); cleanup() }
	}
	if a.dictSys.Microphone() == dictation.PermissionDenied || a.dictSys.Microphone() == dictation.PermissionRestricted {
		a.dictFail(s, i18n.T("AudioSmith may not use the microphone. Allow it in System Settings → Privacy & Security → Microphone."))
		a.dictShow()
		return
	}
	a.dictShow()
	a.dictCue(dictStartEvent)
	a.dictPreload()
	if a.prefs.Dictation.Cleanup {
		a.warmLocalLLM(llm.SkillDictationCleanup) // clean-up runs within 4 s: load the model while the user speaks
	}
	os.MkdirAll(filepath.Dir(out), 0o755)
	p := proto.CaptureParams{Device: a.prefs.Dictation.Device, Channels: 1, SampleRate: services.TranscriptRate,
		Output: proto.Output{Path: out, Format: "wav", Bits: 16}, MaxSeconds: dictMaxSeconds, AEC: a.echoOn()}
	a.startRecording(p, func(l proto.LevelParams) {
		if a.dict == s {
			s.level = l.RMS
			a.noteAutoEcho(l, &s.aecAuto)
		}
	}, func(rec *engine.Recording, err error) {
		if a.dict != s || s.phase != dictStarting {
			if rec != nil {
				a.background(rec.Cancel)
			}
			return
		}
		if err != nil {
			a.dictFail(s, engineErrorText(err))
			return
		}
		s.rec, s.phase, s.started = rec, dictListening, time.Now()
		a.dictRefresh()
		if s.released {
			a.dictStop()
		}
	})
}

// dictPreload loads the dictation model while the user speaks, so the
// transcription does not wait for it (a models.load job on the listen
// worker, skipped when the model is resident there or not downloaded).
// After a long pause the idle limit has unloaded it (#109): the press
// starts the load, and the transcription waits for it behind the load in
// the listen lane; the recording is kept meanwhile.
func (a *app) dictPreload() {
	e := a.dictModel()
	if e.Repo == "" || a.listenHas(e.Repo) || a.engState != engineReady {
		return
	}
	if ready, known := a.modelOnDisk(e); known && !ready {
		return
	}
	params, _ := json.Marshal(proto.LoadParams{Model: e.Repo, Task: "stt"})
	sched := a.lib.sched
	a.async(func() {
		sched.Submit(context.Background(), jobs.Spec{Kind: proto.KindFor(proto.RoleListen, proto.MethodModelsLoad), Lane: jobs.LaneListen, Priority: jobs.PriorityInteractive,
			Params: params, Title: i18n.T("Load model"), Owner: dictLoadOwner})
	})
}

// listenHas reports whether the listen worker has model resident, as its
// last engine.stats said.
func (a *app) listenHas(model string) bool {
	for _, w := range a.workers {
		if w.Role == proto.RoleListen && w.State == engine.WorkerReady && w.Stats != nil {
			for _, m := range w.Stats.Models {
				if m.Model == model {
					return true
				}
			}
		}
	}
	return false
}

// dictLoadPending reports whether dictation's model load (dictPreload)
// is queued or runs.
func (a *app) dictLoadPending() bool {
	for _, j := range a.jobList {
		if j.Owner == dictLoadOwner && (j.State == jobs.StateQueued || j.State == jobs.StateWaiting || j.State == jobs.StateRunning) {
			return true
		}
	}
	return false
}

// dictLoadOwner owns dictation's model loads.
const dictLoadOwner = "dictation:load"

func containsString(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// dictStop ends the recording and transcribes it.
func (a *app) dictStop() {
	s := a.dict
	if s == nil {
		return
	}
	if s.phase == dictStarting {
		s.released = true
		return
	}
	if s.phase != dictListening || s.rec == nil {
		return
	}
	s.phase, s.stage = dictTranscribing, i18n.T("Transcribing…")
	s.loading = a.dictLoadPending()
	a.dictCue(dictStopEvent)
	a.dictRefresh()
	a.dictAfter(dictLoadingAfter, func() {
		if a.dict == s && s.phase == dictTranscribing {
			s.slow = true
			a.dictRefresh()
		}
	})
	rec := s.rec
	a.stopRecording(rec, func(res proto.CaptureResult, err error) {
		if a.dict != s || s.phase != dictTranscribing {
			return
		}
		s.rec = nil
		path := firstOf(res.Output, s.out)
		switch {
		case err != nil:
			a.dictFail(s, engineErrorText(err))
		case res.AudioSeconds < minSpeech:
			os.Remove(path)
			a.dictEnd(s, dictNoSpeech, i18n.T("Nothing was heard. Check the microphone in Settings → Dictation."))
		default:
			s.out = path
			a.dictTranscribe(s, path)
		}
	})
}

// dictTranscribe queues the transcription of a dictation's recording.
func (a *app) dictTranscribe(s *dictSession, path string) {
	d := a.prefs.Dictation
	e := a.dictModel()
	s.trID = core.NewID(time.Now())
	req := services.TranscribeRequest{ID: s.trID, Input: path, Source: core.SourceDictation, RemoveInput: true,
		Mode: settings.TranscribeFast, Model: e.Repo, Language: d.Language, Prompt: d.Vocabulary, App: s.target.Name,
		Role: proto.RoleListen} // the listen worker's own model: not behind renders
	svc := a.trSvc
	a.async(func() {
		_, id, err := svc.Transcribe(context.Background(), req)
		a.post(func() {
			if a.dict != s {
				return
			}
			if err != nil {
				os.Remove(path)
				if errors.Is(err, core.ErrNotDownloaded) {
					s.need = &e
					a.dictFail(s, i18n.T("%s is not downloaded yet.", modelRef(e)))
					return
				}
				var lic *core.LicenceRequiredError
				if errors.As(err, &lic) {
					a.dictFail(s, i18n.T("The model's licence has not been accepted. Review it in Settings → Models & network."))
					return
				}
				a.dictFail(s, err.Error())
				return
			}
			if s.jobID == "" {
				s.jobID = id
			}
		})
	})
}

// dictJobUpdated follows a dictation's transcription; it reports whether
// the job was dictation's.
func (a *app) dictJobUpdated(u jobs.Job) bool {
	s := a.dict
	if s == nil || s.trID == "" || u.Owner != services.TranscriptOwner+s.trID {
		return false
	}
	if s.phase != dictTranscribing {
		return true
	}
	s.jobID = u.ID
	switch u.State {
	case jobs.StateWaiting, jobs.StateQueued:
		// Behind dictation's own model load: the model is loading.
		s.loading = a.dictLoadPending()
		s.stage = i18n.T("Waiting for the engine…")
		if s.loading {
			s.stage = i18n.T("Transcribing…")
		}
	case jobs.StateRunning:
		s.stage = i18n.T("Transcribing…")
		s.loading = u.Progress.Stage == "loading"
	case jobs.StateSucceeded:
		var res services.TranscribeResult
		json.Unmarshal(u.Result, &res)
		a.dictDeliver(s, res.Transcript)
	case jobs.StateCancelled:
		os.Remove(s.out)
		a.dictEnd(s, dictIdle, "")
	default:
		os.Remove(s.out) // the recording, if the job did not take it
		if u.Err != nil && u.Err.Code == proto.CodeModelNotDownloaded {
			e := a.dictModel()
			s.need = &e
		}
		a.dictFail(s, jobErrorText(u.Err))
	}
	a.dictRefresh()
	return true
}

// dictDeliver polishes (and cleans) the text, then types it.
func (a *app) dictDeliver(s *dictSession, t core.Transcript) {
	raw := strings.TrimSpace(t.Text())
	if raw == "" {
		svc := a.trSvc
		a.async(func() { svc.Delete(context.Background(), t.ID) })
		a.dictEnd(s, dictNoSpeech, i18n.T("Nothing was heard. Check the microphone in Settings → Dictation."))
		return
	}
	d := a.prefs.Dictation
	s.phase = dictCleaning
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	cleaner := &translate.Cleaner{Router: a.llmRouter}
	_, who := a.llmAvailable()
	req := translate.CleanupRequest{Text: raw, Lang: t.Language, Vocabulary: vocabulary(d.Vocabulary), NoLLM: !d.Cleanup}
	cold := d.Cleanup && a.localLLMCold(llm.SkillDictationCleanup)
	if cold {
		// The model still loads (local_llm_warm.go): a longer budget once.
		req.Budget, s.warming = coldCleanupBudget, true
	}
	if d.Cleanup && a.localLLMFirst(llm.SkillDictationCleanup) && a.gpuLaneBusy() {
		// A render holds the gpu lane: the model would only get its turn
		// after it, so the polished text is typed at once (thread #53).
		req.Busy = true
	}
	// The paste waits at most the budget, from now: the clean-up's own
	// budget does not count a wait for the gpu lane (llm.WithBudget).
	wall := req.Budget
	if wall <= 0 {
		wall = translate.DefaultCleanupBudget
	}
	in := dictation.Inserter{Sys: a.dictSys, Clip: a.dictClip, CopyOnly: d.Output == settings.OutputCopy, Sleep: dictSleep,
		Pasting: a.familyPasting} // family_coord.go
	target := s.target
	svc := a.trSvc
	a.async(func() {
		cctx, ccancel := context.WithTimeout(ctx, wall)
		res := cleaner.Clean(cctx, req)
		ccancel()
		if cold && res.Fallback == translate.FallbackTimeout {
			a.post(func() { a.warmLocalLLM(llm.SkillDictationCleanup) }) // for the next one
		}
		text := strings.TrimSpace(res.Text)
		if text == "" {
			text = raw
		}
		a.post(func() {
			if a.dict == s && s.phase == dictCleaning {
				s.phase = dictInserting
				a.dictRefresh()
			}
		})
		if ctx.Err() != nil {
			return
		}
		out := in.Insert(ctx, target, text)
		by := "polish"
		if res.LLM {
			by = who
		}
		svc.Update(context.Background(), t.ID, func(t *core.Transcript) {
			if text != t.Text() {
				t.Clean, t.CleanBy = text, by
			}
			t.Inserted = out.Outcome == dictation.Inserted
		})
		a.post(func() {
			if a.dict != s {
				return
			}
			s.text = text
			if out.Outcome == dictation.Inserted {
				a.dictEnd(s, dictInserted, "")
				return
			}
			a.dictEnd(s, dictCopied, copiedReason(out))
		})
	})
}

// gpuLaneBusy reports whether a job other than the language model's
// warm-up runs on the gpu lane (a render, a dub stage): a request of the
// engine's language model would wait for it to end. Main thread.
func (a *app) gpuLaneBusy() bool {
	for _, j := range a.jobList {
		if j.Lane == jobs.LaneGPU && j.State == jobs.StateRunning && j.Owner != llmWarmOwner {
			return true
		}
	}
	return false
}

// copiedReason says why a dictation was copied rather than typed.
func copiedReason(r dictation.Result) string {
	switch r.Reason {
	case dictation.ReasonCopyOnly:
		return ""
	case dictation.ReasonNoAccess:
		return i18n.T("To type into apps, allow AudioSmith in Privacy & Security → Accessibility.")
	case dictation.ReasonNoTarget:
		return i18n.T("No app to type into: paste it where you want it.")
	case dictation.ReasonNotActivated:
		return i18n.T("The app did not come back to the front: paste it there.")
	case dictation.ReasonPasteFailed:
		return i18n.T("Typing into the app failed: paste it there.")
	case dictation.ReasonClipboardError:
		return i18n.T("The clipboard could not be written.")
	}
	return ""
}

// dictFail ends a dictation with an error.
func (a *app) dictFail(s *dictSession, msg string) {
	if s.rec != nil {
		rec := s.rec
		s.rec = nil
		a.background(rec.Cancel)
	}
	a.dictCue(dictErrorEvent)
	a.dictEnd(s, dictError, msg)
}

// dictEnd puts a dictation in an end state; the pill hides by itself
// (errors stay until closed or the next dictation).
func (a *app) dictEnd(s *dictSession, phase dictPhase, note string) {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if s.release != nil {
		s.release() // its models may be unloaded again after the idle limit
		s.release = nil
	}
	s.phase, s.note = phase, note
	a.dictEscape(false)
	a.updateTray()
	a.dictRefresh()
	var wait time.Duration
	switch phase {
	case dictIdle:
		a.dictHide()
		return
	case dictInserted:
		wait = hideInserted
	case dictCopied:
		wait = hideCopied
	case dictNoSpeech:
		wait = hideNoSpeech
	default:
		return
	}
	gen := s.gen
	a.dictAfter(wait, func() {
		if a.dict == s && s.gen == gen && !s.phase.live() {
			a.dictHide()
		}
	})
}

// dictCancel drops the dictation: the recording, its transcription, its
// insertion if it has not started typing.
func (a *app) dictCancel() {
	s := a.dict
	if s == nil {
		return
	}
	if s.rec != nil {
		rec := s.rec
		s.rec = nil
		a.background(rec.Cancel)
	}
	if s.jobID != "" && s.phase == dictTranscribing {
		a.cancelJob(s.jobID)
	}
	if s.phase == dictStarting || s.phase == dictListening {
		os.Remove(s.out)
	}
	a.dictEnd(s, dictIdle, "")
}
