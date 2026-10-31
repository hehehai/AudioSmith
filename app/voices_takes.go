package main

// The takes the voice pages generate (previews of a design or a clone,
// with their job, sample and attachment to a voice) and playing voices;
// split from voices.go.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/licence"
	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
)

// --- takes the voice pages generate -------------------------------------

// voiceJob is a take a voice page asked for: a "Try this voice" phrase, a
// design preview, or a voice's sample. It is followed until the take is
// saved (TakeCreated) or its job fails.
type voiceJob struct {
	takeID   string
	purpose  string // "try", "design", "sample"
	voiceID  string // the voice it is for; "" for an unsaved design
	model    string
	jobID    string
	fraction float64
	status   string
	err      string
	// needDownload is the model to offer for download after the error.
	needDownload *catalog.Entry
	take         *core.Take // the result
	started      time.Time
}

// running reports whether the job is still going.
func (j *voiceJob) running() bool { return j != nil && j.take == nil && j.err == "" }

// voiceJob returns the newest job of purpose for voice voiceID.
func (a *app) voiceJob(purpose, voiceID string) *voiceJob {
	var out *voiceJob
	for _, j := range a.voiceJobs {
		if j.purpose == purpose && j.voiceID == voiceID && (out == nil || j.started.After(out.started)) {
			out = j
		}
	}
	return out
}

// voiceGenerate queues a take for a voice page through TakeService, as
// the Studio does, and returns its id: retry runs again after the licence
// dialog accepts ("" then).
func (a *app) voiceGenerate(purpose, voiceID string, t core.Take, retry func()) string {
	e := entryFor(t.Model)
	if licence.Required(e) != nil && !licence.Covers(a.accepted, e) {
		a.askLicence(e, retry)
		return ""
	}
	for id, j := range a.voiceJobs { // one at a time per purpose and voice
		if j.purpose == purpose && j.voiceID == voiceID && !j.running() {
			delete(a.voiceJobs, id)
		}
	}
	now := time.Now()
	t.ID = core.NewID(now)
	t.Origin = core.TakeVoice // a preview or a sample (History, the Export dialog)
	j := &voiceJob{takeID: t.ID, purpose: purpose, voiceID: voiceID, model: t.Model, fraction: -1,
		status: i18n.T("Starting…"), started: now}
	a.voiceJobs[t.ID] = j
	takes := a.lib.svc.Takes
	a.async(func() {
		id, err := takes.Generate(context.Background(), core.TakeRequest{Recipe: t})
		a.post(func() {
			if err == nil {
				if j.jobID == "" {
					j.jobID = id
				}
				return
			}
			var lic *core.LicenceRequiredError
			switch {
			case errors.As(err, &lic):
				delete(a.voiceJobs, j.takeID)
				a.askLicence(e, retry)
			case errors.Is(err, core.ErrNotDownloaded):
				j.err, j.needDownload = i18n.T("%s is not downloaded yet.", modelRef(e)), &e
			default:
				j.err = err.Error()
			}
			a.voiceJobFailed(j)
		})
	})
	return t.ID
}

// voiceJobFailed tells a reference being made that one of its takes
// failed.
func (a *app) voiceJobFailed(j *voiceJob) {
	if j.purpose == "reference" && j.err != "" {
		a.refTakeFailed(j.voiceID, j.takeID, j.err)
	}
}

// voiceJobUpdated follows the jobs of the voice pages' takes.
func (a *app) voiceJobUpdated(u jobs.Job) {
	j := a.voiceJobs[strings.TrimPrefix(u.Owner, takeOwner)]
	if j == nil || j.take != nil {
		return
	}
	j.jobID = u.ID
	switch u.State {
	case jobs.StateWaiting, jobs.StateQueued:
		j.status = i18n.T("Waiting for the engine…")
	case jobs.StateRunning:
		if u.Progress.Stage != "" {
			j.status = stageText(u.Progress.Stage)
		}
		j.fraction = u.Progress.Fraction
	case jobs.StateCancelled:
		j.err = i18n.T("Cancelled")
	case jobs.StateFailed, jobs.StateInterrupted:
		j.err = jobErrorText(u.Err)
		if u.Err != nil && u.Err.Code == "model_not_downloaded" {
			e := entryFor(j.model)
			j.needDownload = &e
		}
	}
	a.voiceJobFailed(j)
}

// voiceTakeCreated finishes a voice page's take: a sample is attached to
// its voice; "try" and design previews play.
func (a *app) voiceTakeCreated(t core.Take) {
	a.mine.usesFor = "" // My voices' “used” counts read again
	j := a.voiceJobs[t.ID]
	if j == nil {
		return
	}
	j.take = &t
	switch j.purpose {
	case "sample":
		a.attachSample(j.voiceID, t)
	case "reference":
		a.refTakeCreated(j.voiceID, t) // voice_reference.go
	case "candidate": // Design's candidates (design_flow.go): the first plays
		if j.voiceID == "cand-0" && !a.pl.Playing(a.pl.Current()) {
			a.play(&t)
		}
	default:
		a.play(&t)
	}
}

// attachSample saves a take's audio as a voice's sample, and plays it.
func (a *app) attachSample(voiceID string, t core.Take) {
	v := a.findVoice(voiceID)
	if v == nil {
		return
	}
	nv := *v
	nv.SampleText = t.Text
	if nv.CreatedWith == "" {
		nv.CreatedWith = t.Model
	}
	path := a.takeStore.AudioPath(t)
	svc := a.lib.svc.Voices
	a.background(func() {
		saved, err := svc.SaveFiles(context.Background(), nv, core.VoiceFiles{Sample: path})
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not save the voice's sample: %v", err))
				return
			}
			a.playVoice(&saved)
		})
	})
}

// --- playing voices -----------------------------------------------------

// voiceAudio is what plays for a voice: its sample, else its reference.
func (a *app) voiceAudio(v *core.Voice) string {
	if v.Sample != "" {
		return a.voicePath(v, v.Sample)
	}
	return a.voicePath(v, v.RefAudio)
}

// playVoice plays (or stops) a voice's audio; a designed voice without a
// sample renders one first.
func (a *app) playVoice(v *core.Voice) {
	path := a.voiceAudio(v)
	if path == "" {
		a.renderSample(v)
		return
	}
	if err := a.pl.Toggle("voice:"+v.ID, path); err != nil {
		a.toastWarn(i18n.T("Could not play: %v", err))
	}
}

// voicePlaying reports whether a voice's audio plays now.
func (a *app) voicePlaying(v *core.Voice) bool { return a.pl.Playing("voice:" + v.ID) }

// sampleLine is the fixed line a voice's sample says, in its language, so
// the same recipe always renders the same sample.
func sampleLine(lang string) string {
	if strings.HasPrefix(lang, "zh") {
		return "你好，这就是我的声音。很高兴认识你，希望你喜欢。"
	}
	return "Hello, this is how I sound. It's nice to meet you, and I hope you like my voice."
}

// renderSample generates a designed voice's sample with a model that can
// design; the take becomes the voice's sample when it is saved.
func (a *app) renderSample(v *core.Voice) {
	if v.Kind != core.VoiceDesigned {
		a.toast(i18n.T("This voice has no audio."))
		return
	}
	e, ok := a.designModelFor(v.CreatedWith)
	if !ok {
		a.toast(i18n.T("No model that can design voices is available."))
		return
	}
	r := voiceRecipe(v)
	lang := r.Attrs.Language()
	if lang == "" {
		lang = v.Language
	}
	low, err := vd.Lower(designTarget(e), r, vd.Options{Lang: designTextLang(lang)})
	if err != nil {
		a.toastWarn(err.Error())
		return
	}
	id := v.ID
	t := core.Take{Text: sampleLine(lang), Model: e.Repo, Voice: low.Voice, Seed: r.Seed, Language: modelLanguage(e, lang), VoiceID: id}
	a.voiceGenerate("sample", id, t, func() {
		if v := a.findVoice(id); v != nil {
			a.renderSample(v)
		}
	})
}

// voiceRecipe is a designed voice's recipe.
func voiceRecipe(v *core.Voice) vd.Recipe {
	r := vd.Recipe{Description: v.Design, Seed: v.Seed}
	if len(v.DesignAttrs) > 0 {
		json.Unmarshal(v.DesignAttrs, &r.Attrs)
	}
	return r
}

// modelLanguage is the value of a model's language parameter for a
// BCP-47 code ("" when the model takes none or doesn't speak it).
func modelLanguage(e catalog.Entry, code string) string {
	if code == "" || !e.Has(catalog.CapLanguage) {
		return ""
	}
	for _, l := range e.Languages {
		if l.Code == code || strings.HasPrefix(l.Code, code+"-") {
			if l.Value != "" {
				return l.Value
			}
			return l.Code
		}
	}
	if len(e.Languages) == 0 {
		return code
	}
	return ""
}
