package main

// A voice from a clip (Quick clone, /voices/new/clone; its steps are
// clone_steps.go): upload or record a reference, keep
// its best part, transcribe it, clean its noise, then save it as a voice
// (name + portrait), use it once without saving, or replace the reference
// of a saved voice. The engine does the work (voice_engine.go); clone.go
// (this: the flow up to the selection), clone_save.go (transcribing, the
// rules about lengths, saving) and clone_mic.go (recording) hold the
// flow.

import (
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/audio"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp/refcheck"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/i18n"
)

// Reference lengths, in seconds.
const (
	refMin       = 1.0  // shorter is refused
	refIdealLow  = 5.0  // 5–15 s is what models clone best from
	refIdealHigh = 15.0 // longer clips start at their best 15 s
	refMaxBare   = 75.0 // longer needs a transcript
)

// cloneState is the reference flow's view state.
type cloneState struct {
	replaceFor string // voice whose reference this replaces; "" = a new voice
	step       int    // the guided page's step (clone_steps.go)
	savedID    string // the voice saved (the last step)
	gen        int    // bumps with each clip: results for older ones are dropped
	srcName    string // the file picked, or "Recording"
	work       string // the working WAV (mono) in referencesDir
	original   string // the clip before cleaning (Undo)
	seconds    float64
	peaks      []float32
	speech     []span
	auto       span    // the part the app chose; the user's own choice is kept
	selLo      float64 // the part kept, bound to the trimmer
	selHi      float64
	busy       string // what runs, translated; "" when idle
	err        string
	cleaned    bool
	cleanErr   string
	// The reference checker (clone_check.go): the last report and what it
	// was of; noisy: the clip as loaded was noisy (so it was denoised);
	// tidy: the part kept is tidied (edges, long pauses, loudness).
	check    *refcheck.Report
	checkKey string
	checking bool
	noisy    bool
	tidy     bool
	tipsOpen bool

	transcript string
	autoText   string // the machine transcript last put in transcript
	asr        string // "", "running", "failed", "noasr", "done"
	asrErr     string
	style      string
	language   string

	name        string
	portrait    []byte
	portraitBmp *ui.Bitmap
	saving      bool

	mic           micState
	devices       []proto.CaptureDevice
	devicesLoaded bool
	devicesErr    string
}

// sel is the part of the clip kept.
func (s *cloneState) sel() span { return span{s.selLo, s.selHi} }

// hasClip reports whether a clip is loaded.
func (s *cloneState) hasClip() bool { return s.work != "" }

// startClone opens the reference flow for a new voice, or to replace the
// reference of voice replaceFor.
func (a *app) startClone(replaceFor string) {
	a.resetClone(replaceFor)
	a.navigate("/clone")
	a.loadDevices()
}

// resetClone empties the clone form (for a new voice, or to replace the
// reference of voice replaceFor), keeping the microphone's choices: once
// a voice is saved, or the clip goes to the Studio, the next visit starts
// afresh.
func (a *app) resetClone(replaceFor string) {
	devices, loaded, mic := a.clone.devices, a.clone.devicesLoaded, a.clone.mic
	a.micCancel(&a.clone.mic)
	a.clone = cloneState{replaceFor: replaceFor, gen: a.clone.gen + 1, devices: devices, devicesLoaded: loaded,
		mic: micState{device: mic.device, channels: mic.channels}}
	if v := a.findVoice(replaceFor); v != nil {
		a.clone.name, a.clone.style, a.clone.language = v.Name, v.Style, v.Language
	}
}

// loadDevices lists the microphones once.
func (a *app) loadDevices() {
	if a.clone.devicesLoaded {
		return
	}
	a.clone.devicesLoaded = true
	a.captureDevices(func(ds []proto.CaptureDevice, err error) {
		if err != nil {
			a.clone.devicesErr = engineErrorText(err)
			return
		}
		a.clone.devices, a.clone.devicesErr = ds, ""
	})
}

// workPath is a new file in the references directory.
func (a *app) workPath(prefix string) string {
	return filepath.Join(a.referencesDir(), prefix+"-"+core.NewID(time.Now())+".wav")
}

// chooseCloneFile asks for an audio or video file.
func (a *app) chooseCloneFile() {
	exts := []string{"wav", "mp3", "m4a", "aac", "flac", "ogg", "opus", "aiff", "aif", "caf", "webm", "mp4", "mov", "m4v"}
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:  a.win,
			Title:   i18n.T("Reference audio"),
			Filters: []mygo.FileFilter{{Name: i18n.T("Audio or video"), Extensions: exts}},
		})
		if err != nil || len(paths) == 0 {
			return
		}
		a.post(func() { a.cloneLoad(paths[0], filepath.Base(paths[0]), false) })
	}()
}

// cloneDropped takes files dropped on the reference flow: the first
// audio or video file becomes the clip, an image the portrait.
func (a *app) cloneDropped(paths []string) {
	for _, p := range paths {
		if isPortraitPath(p) {
			a.clonePortrait(p)
			continue
		}
		a.cloneLoad(p, filepath.Base(p), false)
		return
	}
}

// cloneLoad makes src the clip: decoded to a mono WAV, its best part
// selected (by energy at once, then by the engine's VAD), then cleaned
// (recordings) and transcribed. A newer clip wins over a slow older one.
func (a *app) cloneLoad(src, name string, recorded bool) {
	s := &a.clone
	s.gen++
	gen := s.gen
	s.busy, s.err, s.srcName = i18n.T("Reading the clip…"), "", name
	s.work, s.original, s.peaks, s.speech, s.cleaned, s.cleanErr = "", "", nil, nil, false, ""
	s.check, s.checkKey, s.noisy, s.tidy = nil, "", false, true
	s.asr, s.asrErr = "", ""
	if s.transcript == s.autoText { // a new clip: a machine transcript of the old one goes
		s.transcript = ""
	}
	s.autoText = ""
	out := a.workPath("clip")
	a.decodeClip(src, out, func(secs float64, err error) {
		if s.gen != gen {
			os.Remove(out)
			return
		}
		if err != nil {
			s.busy, s.err = "", i18n.T("Could not read the clip: %s", engineErrorText(err))
			return
		}
		a.background(func() {
			peaks, _ := audio.Peaks(out, peakCount)
			speech := energySpeech(out)
			a.post(func() {
				if s.gen != gen {
					return
				}
				s.busy = ""
				s.work, s.seconds, s.speech, s.peaks = out, secs, speech, peaks
				s.auto = refWindow(speech, secs)
				s.selLo, s.selHi = s.auto.Start, s.auto.End
				a.cloneRefine(gen)
			})
		})
	})
}

// cloneRefine asks the engine's VAD where the speech is (keeping a part
// the user chose), then checks the clip: it is denoised when noisy, then
// transcribed (clone_check.go).
func (a *app) cloneRefine(gen int) {
	s := &a.clone
	next := func() { a.cloneFirstCheck(gen) }
	if s.seconds <= refIdealHigh {
		next() // all of it is kept: no need to look for the best part
		return
	}
	a.detectSpeech(s.work, func(speech []span, err error) {
		if s.gen != gen {
			return
		}
		if err == nil && len(speech) > 0 {
			s.speech = speech
			if s.sel() == s.auto {
				s.auto = refWindow(speech, s.seconds)
				s.selLo, s.selHi = s.auto.Start, s.auto.End
			}
		}
		next()
	})
}

// cloneClean removes the clip's noise (keeping the original for Undo);
// then, when asked, transcribes it.
func (a *app) cloneClean(thenTranscribe bool) {
	s := &a.clone
	if !s.hasClip() || s.cleaned || s.busy != "" {
		return
	}
	gen, in, out := s.gen, s.work, a.workPath("clean")
	s.busy, s.cleanErr = i18n.T("Cleaning the noise…"), ""
	a.enhanceClip(in, out, func(err error) {
		if s.gen != gen {
			os.Remove(out)
			return
		}
		if err != nil {
			s.busy, s.cleanErr = "", engineErrorText(err)
			if thenTranscribe {
				a.cloneTranscribe()
			}
			return
		}
		a.background(func() {
			peaks, _ := audio.Peaks(out, peakCount)
			a.post(func() {
				if s.gen != gen {
					return
				}
				s.busy = ""
				s.original, s.work, s.peaks, s.cleaned = s.work, out, peaks, true
				if thenTranscribe {
					a.cloneTranscribe()
				}
			})
		})
	})
}

// cloneUndoClean goes back to the clip as it was before cleaning.
func (a *app) cloneUndoClean() {
	s := &a.clone
	if !s.cleaned || s.original == "" {
		return
	}
	s.work, s.original, s.cleaned = s.original, "", false
	path, gen := s.work, s.gen
	a.background(func() {
		peaks, _ := audio.Peaks(path, peakCount)
		a.post(func() {
			if s.gen == gen {
				s.peaks = peaks
			}
		})
	})
}

// cloneSelection writes the kept part to a file of its own, tidied unless
// the user undid it, and calls done with it on the main thread (the
// whole clip when all of it is kept).
func (a *app) cloneSelection(done func(path string, seconds float64, err error)) {
	s := &a.clone
	work, sel, total, out := s.work, s.sel(), s.seconds, a.workPath("ref")
	var speech []span
	if s.tidy {
		speech = append([]span{}, s.speech...)
	}
	a.background(func() {
		secs, err := cutPart(work, out, sel, total)
		if err == nil && speech != nil {
			secs, err = tidyWAV(out, sel, speech)
		}
		a.post(func() { done(out, secs, err) })
	})
}

// cutPart writes the part sel of a WAV of total seconds to out (a copy
// when it is all of it).
func cutPart(work, out string, sel span, total float64) (float64, error) {
	if sel.Start <= 0.05 && sel.End >= total-0.05 {
		return total, copyWAV(work, out)
	}
	return cutWAV(work, out, sel)
}

// copyWAV copies a file.
func copyWAV(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
