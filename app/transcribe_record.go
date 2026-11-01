package main

// Transcribe's playback of a transcript's audio and its recorder (record,
// then transcribe); split from transcribe.go.

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/services"
)

// trPlayID is the player's id for a transcript's audio.
func trPlayID(id string) string { return "transcript:" + id }

// playTranscriptAt plays the transcript shown from second s.
func (a *app) playTranscriptAt(s float64) {
	t := a.trDetail
	if t == nil || t.Audio == "" || t.Duration <= 0 {
		return
	}
	id := trPlayID(t.ID)
	if a.pl.Current() != id {
		if err := a.pl.Toggle(id, a.trSvc.AudioPath(*t)); err != nil {
			a.toastWarn(i18n.T("Cannot play: %v", err))
			return
		}
	} else if !a.pl.Playing(id) {
		a.pl.Toggle(id, a.trSvc.AudioPath(*t))
	}
	a.pl.Seek(id, float32(min(max(s/t.Duration, 0), 1)))
}

// toggleTranscriptPlayback plays or pauses the transcript shown.
func (a *app) toggleTranscriptPlayback() {
	t := a.trDetail
	if t == nil || t.Audio == "" {
		return
	}
	if err := a.pl.Toggle(trPlayID(t.ID), a.trSvc.AudioPath(*t)); err != nil {
		a.toastWarn(i18n.T("Cannot play: %v", err))
	}
}

// stopTranscriptPlayback stops the transcript's audio, if it plays.
func (a *app) stopTranscriptPlayback() {
	if strings.HasPrefix(a.pl.Current(), "transcript:") {
		a.pl.Stop()
	}
}

// trPosition is where the transcript shown is playing, in seconds; -1
// when it is not loaded in the player.
func (a *app) trPosition() float64 {
	t := a.trDetail
	if t == nil || a.pl.Current() != trPlayID(t.ID) {
		return -1
	}
	return float64(a.pl.Progress(trPlayID(t.ID))) * t.Duration
}

// trRecMaxSeconds bounds a recording (4 hours).
const trRecMaxSeconds = 4 * 3600

// trRecStart records for a transcript, mono at 16 kHz, into the
// transcript directory.
func (a *app) trRecStart() {
	m := &a.trMic
	if m.phase != "" || a.trJob != nil {
		return
	}
	a.loadDevices()
	out := filepath.Join(a.lib.store.Transcripts().AudioDir(), "rec-"+core.NewID(time.Now())+".wav")
	a.micCapture(m, proto.CaptureParams{Device: a.prefs.Transcribe.Device, Channels: 1, SampleRate: services.TranscriptRate,
		Output: proto.Output{Path: out, Format: "wav", Bits: 16}, MaxSeconds: trRecMaxSeconds})
}

// trRecStop ends the recording and transcribes it.
func (a *app) trRecStop() {
	m := &a.trMic
	if m.phase != "recording" || m.rec == nil {
		return
	}
	m.phase = "stopping"
	rec, out := m.rec, m.out
	a.stopRecording(rec, func(res proto.CaptureResult, err error) {
		if m.out != out {
			return
		}
		m.rec, m.phase, m.level = nil, "", -120
		path := firstOf(res.Output, out)
		switch {
		case err != nil:
			m.err = engineErrorText(err)
		case res.AudioSeconds < 0.5:
			os.Remove(path)
			m.err = i18n.T("Recording too short.")
		default:
			if res.Reason == proto.CaptureDeviceLost {
				a.toast(i18n.T("The microphone went away: what was recorded is kept."))
			}
			a.transcribeRecording(path)
		}
	})
}

// trRecEnded notices a recording that ended by itself (the device went
// away, the time limit) and finishes it as Stop does.
func (a *app) trRecEnded() {
	m := &a.trMic
	if m.phase != "recording" || m.rec == nil {
		return
	}
	select {
	case <-m.rec.Done():
		a.trRecStop()
	default:
	}
}
