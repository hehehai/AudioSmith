package main

// "Report this problem" beside the failures AudioSmith shows, and the
// recent actions the error reports carry (actnowkit's Capture and Step).
// The codes (E-<area>-<number>) are AudioSmith's own and stable across
// versions: the site groups the reports by them. Where is a place in the
// app, never the user's content (no text, file, service or app name).

import (
	"errors"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/actnowkit/feedback"
	fbui "github.com/hehehai/actnowkit/feedbackui"

	"github.com/hehehai/AudioSmith/internal/i18n"
)

// AudioSmith's error codes and kinds of the failures with a report link.
const (
	codeSpeech     = "E-TTS-0001" // a take of Text to speech failed (Text to speech, under Generate)
	codeEngine     = "E-ENG-0001" // an engine worker stopped or did not start (Engine)
	codeModel      = "E-MDL-0001" // a model's download failed (Models → its card)
	codeTranscribe = "E-ASR-0001" // a transcription failed (Speech to text)
	codeAudiobook  = "E-LFM-0001" // an audiobook's or a story's render failed
	codeDub        = "E-DUB-0001" // a dub's run failed (Dub)

	kindSpeech        = "speech_failed"
	kindModelDownload = "model_download_failed"
	kindTranscribe    = "transcription_failed"
	kindRender        = "render_failed"
	kindDub           = "dub_failed"
)

// failure is a failure shown: key names it among the others (an entry's
// or a session's id: a new key or a new message is a new report), the
// rest is its context.
type failure struct {
	key, title, code, kind, where, message string
	// atError: the report is captured when the failure first shows, with
	// the log lines of that moment (a single failure whose lines a flood
	// of others would soon push out: the helper's or the engine's
	// crash); otherwise when the link is clicked (lists, many at once).
	atError bool
}

// captureReport is f's report, now (off the main thread); nil when it fails.
func captureReport(m *feedback.Manager, f failure) *feedback.Report {
	var err error
	if f.message != "" {
		err = errors.New(f.message)
	}
	rep, cerr := m.Capture(feedback.ErrorContext{Title: f.title, Code: f.code, Kind: f.kind, Where: f.where, Err: err})
	if rep == nil || cerr != nil && rep.ID == "" {
		return nil
	}
	return rep
}

// fbReport is the report of a failure captured when it shows (atError):
// captured off the main thread the first time the failure shows (Capture reads the log and writes the
// report: the build pass does no I/O), nil until it is ready and without
// the feedback.
func (a *app) fbReport(f failure) *feedback.Report {
	m := a.fbMgr
	if m == nil {
		return nil
	}
	k := f.key + "\x00" + f.code + "\x00" + f.kind + "\x00" + f.message
	a.fbRepMu.Lock()
	rep, seen := a.fbReports[k]
	if !seen {
		if a.fbReports == nil || len(a.fbReports) > 200 {
			a.fbReports = map[string]*feedback.Report{}
		}
		a.fbReports[k] = nil // being captured
	}
	a.fbRepMu.Unlock()
	if seen {
		return rep
	}
	a.async(func() {
		rep := captureReport(m, f)
		if rep == nil {
			return // no link for this failure
		}
		a.post(func() {
			a.fbRepMu.Lock()
			defer a.fbRepMu.Unlock()
			if _, ok := a.fbReports[k]; ok && a.fbMgr == m {
				a.fbReports[k] = rep
			}
		})
	})
	a.fbRepMu.Lock()
	defer a.fbRepMu.Unlock()
	return a.fbReports[k] // set already when async ran at once (tests)
}

// reportLink draws "Report this problem" for f: a click opens the
// feedback window with its report, captured then (or when the failure
// first showed, f.atError). Nothing without the feedback.
func (a *app) reportLink(c *ui.Context, f failure) {
	m := a.fbMgr
	if a.fbWin == nil || m == nil {
		return
	}
	key := c.Key("report:" + f.code + ":" + f.key)
	if !f.atError {
		fbui.ReportLinkFunc(key, a.fbWin, func() *feedback.Report { return captureReport(m, f) })
		return
	}
	rep := a.fbReport(f)
	if rep == nil {
		return
	}
	fbui.ReportLink(key, a.fbWin, rep)
}

// step records a user's action (its name only: "try_transcribe") for the
// reports (main thread).
func (a *app) step(name string) {
	if m := a.fbMgr; m != nil {
		m.Step(name)
	}
}

// feedbackStepName shows a step of an error report in the app's language
// ("": the window's own wording).
func feedbackStepName(s string) string {
	switch s {
	case "generate":
		return i18n.T("Generate speech")
	case "download_model":
		return i18n.T("Download a model")
	case "restart_engine":
		return i18n.T("Restart engine")
	case "transcribe":
		return i18n.T("Transcribe a recording")
	case "render_audiobook":
		return i18n.T("Render an audiobook")
	case "render_story":
		return i18n.T("Render a story")
	case "resume_render":
		return i18n.T("Resume a render")
	case "run_dub":
		return i18n.T("Run a dub")
	}
	return ""
}
