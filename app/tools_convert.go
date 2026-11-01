package main

// The A/B and Convert tools: two voices side by side, and a recording
// re-spoken in a saved voice (with "Match the length"); split from
// tools.go.

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp/stretch"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
)

// --- A/B ---------------------------------------------------------------------

func (a *app) runAB() {
	text := strings.TrimSpace(a.ab.text)
	if text == "" {
		return
	}
	for i, label := range []string{a.ab.a, a.ab.b} {
		ch, ok := a.toolChoice(label)
		if !ok {
			continue
		}
		if a.ab.runs[i] == nil {
			a.ab.runs[i] = &toolRun{}
		}
		a.toolPreview(a.ab.runs[i], a.toolRecipe(ch, text))
	}
}

// --- convert -------------------------------------------------------------------

func (a *app) chooseConvertFile() {
	a.openFile(mygo.OpenDialogOptions{Title: i18n.T("A recording to convert"),
		Filters: fileTypes(i18n.T("Audio or video"), transcribeExts...)}, a.setConvertFile)
}

// setConvertFile takes a file and measures it.
func (a *app) setConvertFile(path string) {
	cv := &a.cv
	cv.file, cv.name, cv.duration, cv.heard, cv.take, cv.err, cv.note = path, filepath.Base(path), 0, "", nil, "", ""
	a.probeMedia(path, func(info proto.ProbeResult, err error) {
		if a.cv.file == path && err == nil {
			a.cv.duration = info.Duration
		}
	})
}

// runConvert transcribes the file, then speaks the text in the voice.
func (a *app) runConvert() {
	cv := &a.cv
	ch, ok := a.toolChoice(cv.voice)
	if cv.file == "" || !ok || a.trSvc == nil {
		return
	}
	stt := sttEntry(a.prefs.Transcribe.FastModel, "")
	req := services.TranscribeRequest{Input: cv.file, Source: core.SourceFile, Title: i18n.T("Convert: %s", cv.name), Mode: "fast", Model: stt.Repo}
	cv.stage, cv.err, cv.note, cv.heard, cv.take = i18n.T("Listening…"), "", "", "", nil
	svc, file := a.trSvc, cv.file
	a.async(func() {
		_, jobID, err := svc.Transcribe(context.Background(), req)
		a.post(func() {
			if a.cv.file != file {
				return
			}
			if err != nil {
				a.cv.stage, a.cv.err = "", a.toolRefusal(err)
				return
			}
			a.toolAwait(jobID, func(j jobs.Job) {
				if a.cv.file != file {
					return
				}
				var res services.TranscribeResult
				if j.State != jobs.StateSucceeded || json.Unmarshal(j.Result, &res) != nil {
					a.cv.stage, a.cv.err = "", toolJobErr(j)
					return
				}
				a.cv.heard = res.Transcript.Text()
				if a.cv.duration == 0 {
					a.cv.duration = res.Transcript.Duration
				}
				a.convertSpeak(ch)
			})
		})
	})
}

// convertSpeak generates what was heard in the chosen voice.
func (a *app) convertSpeak(ch toolChoice) {
	cv := &a.cv
	if strings.TrimSpace(cv.heard) == "" {
		cv.stage, cv.err = "", i18n.T("No speech was heard in the recording.")
		return
	}
	t := a.toolRecipe(ch, cv.heard)
	stretchTo := 0.0
	if cv.match && cv.duration > 0 {
		if ch.model.Has(catalog.CapDuration) {
			t.Duration = cv.duration
		} else {
			stretchTo = cv.duration // after the take, by time-stretching it
		}
	}
	cv.stage = i18n.T("Speaking…")
	svc := a.lib.svc.Takes
	file := cv.file
	a.async(func() {
		jobID, err := svc.Generate(context.Background(), core.TakeRequest{Recipe: t})
		a.post(func() {
			if a.cv.file != file {
				return
			}
			if err != nil {
				a.cv.stage, a.cv.err = "", a.toolRefusal(err)
				return
			}
			a.toolAwait(jobID, func(j jobs.Job) {
				if a.cv.file != file {
					return
				}
				a.cv.stage = ""
				if j.State != jobs.StateSucceeded {
					a.cv.err = toolJobErr(j)
					return
				}
				var res services.RenderResult
				json.Unmarshal(j.Result, &res)
				done := t
				done.File = t.ID + ".wav"
				done.AudioSeconds, done.SampleRate, done.JobID, done.GenSeconds = res.AudioSeconds, res.SampleRate, j.ID, res.Seconds
				if res.Seed != 0 {
					done.Seed = res.Seed
				}
				a.cv.take = &done
				if stretchTo > 0 && res.Output != "" {
					a.convertStretch(done, res.Output, stretchTo)
				}
			})
		})
	})
}

// convertMaxRatio bounds how far Convert stretches a take to match a
// recording (beyond it speech smears; see dsp/stretch).
const convertMaxRatio = 2.0

// convertStretch makes a copy of take as long as the recording, by
// time-stretching it without changing its pitch, and adds it to the
// History (the natural take stays there too).
func (a *app) convertStretch(take core.Take, path string, target float64) {
	file := a.cv.file
	a.cv.stage = i18n.T("Matching the length…")
	svc := a.lib.svc.Takes
	a.async(func() {
		matched, note, err := stretchTake(context.Background(), svc, take, path, target)
		a.post(func() {
			if a.cv.file != file {
				return
			}
			a.cv.stage = ""
			if err != nil {
				a.cv.err = i18n.T("Could not match the length: %v", err)
				return
			}
			a.cv.note = note
			a.cv.take = &matched
		})
	})
}

// stretchTake writes take's audio stretched to target seconds as a new
// take. note says when the ratio had to be capped.
func stretchTake(ctx context.Context, svc core.TakeService, take core.Take, path string, target float64) (core.Take, string, error) {
	buf, _, err := wav.ReadFile(path)
	if err != nil {
		return core.Take{}, "", err
	}
	if buf.Frames() == 0 || buf.Rate <= 0 {
		return core.Take{}, "", errors.New("the take has no audio")
	}
	have := float64(buf.Frames()) / float64(buf.Rate)
	ratio, note := target/have, ""
	if r := min(max(ratio, 1/convertMaxRatio), convertMaxRatio); r != ratio {
		ratio = r
		note = i18n.T("The take is %.1f s and the recording %.1f s: stretched as far as speech allows (%.1f s).", have, target, have*ratio)
	}
	out := stretch.ToLength(buf, int(math.Round(have*ratio*float64(buf.Rate))), stretch.Options{})
	m := take
	m.ID = core.NewID(timeNow())
	m.File = m.ID + ".wav"
	m.CreatedAt, m.Starred, m.JobID = time.Time{}, false, ""
	m.Duration = out.Seconds()
	m.AudioSeconds, m.SampleRate = out.Seconds(), out.Rate
	if err := wav.WriteFile(filepath.Join(svc.AudioDir(), m.File), out, wav.PCM16, wav.Options{}); err != nil {
		return core.Take{}, "", err
	}
	saved, err := svc.Add(ctx, m)
	return saved, note, err
}
