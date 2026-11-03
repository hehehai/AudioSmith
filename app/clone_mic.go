package main

// Recording a reference from the microphone; split from clone.go.

import (
	"os"
	"path/filepath"
	"time"

	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/i18n"
)

// micState is a microphone recording: the reference flow's and the
// consent statement's.
type micState struct {
	rec      *engine.Recording
	out      string
	phase    string // "", "starting", "recording", "stopping"
	started  time.Time
	level    float64 // input RMS, dBFS
	err      string
	device   string // capture device id; "" = the system's default
	channels int    // 0 auto, 1 mono, 2 stereo
	aecAuto  string // why the engine turned echo cancellation on in this recording (mic_aec.go)
	onStop   func(path string)
}

// micStart starts recording into a new file; done gets it when stopped.
func (a *app) micStart(m *micState, done func(path string)) {
	if m.phase != "" {
		return
	}
	m.onStop = done
	a.micCapture(m, proto.CaptureParams{Device: m.device, Channels: m.channels,
		Output: proto.Output{Path: a.workPath("rec"), Format: "wav"}, MaxSeconds: 600, AEC: a.echoOn()})
}

// micCapture starts the capture p into p.Output.Path for m: m shows it
// starting, then recording with its input level (Clone, Transcribe).
func (a *app) micCapture(m *micState, p proto.CaptureParams) {
	out := p.Output.Path
	m.phase, m.err, m.level, m.out, m.aecAuto = "starting", "", -120, out, ""
	os.MkdirAll(filepath.Dir(out), 0o755)
	a.startRecording(p, func(l proto.LevelParams) {
		if m.out == out {
			m.level = l.RMS
			a.noteAutoEcho(l, &m.aecAuto)
		}
	}, func(rec *engine.Recording, err error) {
		if m.out != out || m.phase != "starting" {
			if rec != nil {
				a.background(rec.Cancel)
			}
			return
		}
		if err != nil {
			m.phase, m.err = "", engineErrorText(err)
			return
		}
		m.rec, m.phase, m.started = rec, "recording", time.Now()
	})
}

// micStop ends the recording and hands the file on.
func (a *app) micStop(m *micState) {
	if m.phase != "recording" || m.rec == nil {
		return
	}
	m.phase = "stopping"
	rec, out, done := m.rec, m.out, m.onStop
	a.stopRecording(rec, func(res proto.CaptureResult, err error) {
		if m.out != out {
			return
		}
		m.rec, m.phase, m.level = nil, "", -120
		switch {
		case err != nil:
			m.err = engineErrorText(err)
		case res.AudioSeconds < 0.5:
			m.err = i18n.T("Recording too short.")
		default:
			a.noteRecordingEnd(res)
			if done != nil {
				done(firstOf(res.Output, out))
			}
		}
	})
}

// micEnded notices a recording that ended by itself (the device went
// away, the time limit) and finishes it as Stop does.
func (a *app) micEnded(m *micState) {
	if m.phase != "recording" || m.rec == nil {
		return
	}
	select {
	case <-m.rec.Done():
		a.micStop(m)
	default:
	}
}

// micCancel drops a recording in progress.
func (a *app) micCancel(m *micState) {
	rec := m.rec
	m.rec, m.phase, m.out = nil, "", ""
	if rec != nil {
		a.background(rec.Cancel)
	}
}
