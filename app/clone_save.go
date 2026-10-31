package main

// A voice from a clip, the second half: transcribing the reference, the
// portrait, the rules about lengths (problems, advice, the best window),
// saving, using it once, and the prompt; split from clone.go.

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
)

// cloneTranscribe transcribes the kept part. The transcript never
// replaces what the user typed: only an empty one, or the machine's own.
func (a *app) cloneTranscribe() {
	s := &a.clone
	if !s.hasClip() || s.asr == "running" {
		return
	}
	s.asr, s.asrErr = "running", ""
	gen, lang := s.gen, s.language
	a.cloneSelection(func(path string, _ float64, err error) {
		if err != nil {
			s.asr, s.asrErr = "failed", err.Error()
			return
		}
		a.transcribeClip(path, lang, func(text string, err error) {
			os.Remove(path)
			if s.gen != gen {
				return
			}
			switch {
			case errors.Is(err, errNoASR):
				s.asr = "noasr"
			case err != nil:
				s.asr, s.asrErr = "failed", engineErrorText(err)
			default:
				s.asr = "done"
				if s.transcript == s.autoText {
					s.transcript = text
				}
				s.autoText = text
			}
		})
	})
}

// clonePortrait makes an image the new voice's portrait.
func (a *app) clonePortrait(path string) {
	a.background(func() {
		png, err := portraitPNG(path)
		var bmp *ui.Bitmap
		if err == nil {
			bmp, _ = ui.DecodeBitmap(png)
		}
		a.post(func() {
			if err != nil {
				a.toastWarn(portraitErrorText(err))
				return
			}
			a.clone.portrait, a.clone.portraitBmp = png, bmp
		})
	})
}

// portraitErrorText explains why an image can't be a portrait.
func portraitErrorText(err error) string {
	switch {
	case errors.Is(err, errPortraitLarge):
		return i18n.T("Choose an image under 5 MB.")
	case errors.Is(err, errPortraitType):
		return i18n.T("Choose a PNG, JPEG or WebP image.")
	}
	return i18n.T("Could not use the image: %v", err)
}

// choosePortrait asks for an image and hands it to use.
func (a *app) choosePortrait(use func(path string)) {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:  a.win,
			Title:   i18n.T("Portrait"),
			Filters: []mygo.FileFilter{{Name: i18n.T("Images"), Extensions: []string{"png", "jpg", "jpeg", "webp"}}},
		})
		if err != nil || len(paths) == 0 {
			return
		}
		a.post(func() { use(paths[0]) })
	}()
}

// cloneProblem says why the clip can't be used yet ("" when it can).
func (a *app) cloneProblem() string {
	s := &a.clone
	switch {
	case !s.hasClip():
		return i18n.T("Add a clip first.")
	case s.busy != "":
		return s.busy
	case s.sel().Len() < refMin:
		return i18n.T("The part kept is too short: keep at least 1 second.")
	case s.sel().Len() > refMaxBare && strings.TrimSpace(s.transcript) == "":
		return i18n.T("Clips over 75 seconds need a transcript. Keep a shorter part, or type what it says.")
	}
	return ""
}

// refAdvice is the note on the kept part's length and its tone: "ok",
// "warning" or "danger".
func refAdvice(seconds float64, transcript bool) (string, string) {
	switch {
	case seconds < refMin:
		return i18n.T("Too short: keep at least 1 second; 5–15 seconds of clean speech works best."), "danger"
	case seconds < refIdealLow:
		return i18n.T("Short: 5–15 seconds of clean speech works best."), "warning"
	case seconds <= refIdealHigh+0.05:
		return i18n.T("Good length: %.1f seconds.", seconds), "ok"
	case seconds <= refMaxBare:
		return i18n.T("Longer than 15 seconds: most models clone best from 5–15 seconds. Keep the best part, or all of it."), "warning"
	case !transcript:
		return i18n.T("Over 75 seconds: keep a shorter part, or add the transcript."), "danger"
	}
	return i18n.T("Over 75 seconds: long references are slow, and a shorter part usually sounds as good."), "warning"
}

// bestWindow picks the stretch of at most maxLen seconds with the most
// speech in it. Windows start just before a stretch of speech and, when a
// word would be cut at their end, end before that word; they are compared
// after that. Clips no longer than maxLen are kept whole.
func bestWindow(speech []span, total, maxLen float64) span {
	if total <= maxLen {
		return span{0, total}
	}
	window := func(start float64) span {
		w := span{start, min(start+maxLen, total)}
		if w.Len() < maxLen {
			w.Start = max(0, w.End-maxLen)
		}
		for _, s := range speech {
			if s.Start < w.End && s.End > w.End && s.Start-w.Start >= refIdealLow {
				w.End = s.Start
				break
			}
		}
		return w
	}
	best := window(0)
	bestSpeech := speechIn(speech, best)
	for _, s := range speech {
		w := window(max(0, s.Start-0.1))
		if v := speechIn(speech, w); v > bestSpeech+1e-9 {
			best, bestSpeech = w, v
		}
	}
	return best
}

// speechIn is how much speech lies inside w.
func speechIn(speech []span, w span) float64 {
	var n float64
	for _, s := range speech {
		n += max(0, min(s.End, w.End)-max(s.Start, w.Start))
	}
	return n
}

// cloneSave saves the clip: as a new voice, or as the new reference of
// the voice it replaces. A voice prompt for the Studio's model is cached
// afterwards when the engine can make one.
func (a *app) cloneSave() {
	s := &a.clone
	if a.cloneProblem() != "" || s.saving {
		return
	}
	name := strings.TrimSpace(s.name)
	if name == "" {
		a.toast(i18n.T("Give the voice a name."))
		return
	}
	s.saving = true
	v := core.Voice{Name: name, Kind: core.VoiceCloned, RefText: strings.TrimSpace(s.transcript), Style: s.style, Language: s.language}
	if old := a.findVoice(s.replaceFor); old != nil {
		v = *old
		v.Name, v.RefText, v.Style, v.Language = name, strings.TrimSpace(s.transcript), s.style, s.language
	}
	portrait := s.portrait
	v.RefCheck = a.cloneQuality()
	gen := s.gen
	a.cloneOriginal(func(original string) {
		a.cloneSelection(func(path string, secs float64, err error) {
			if err != nil {
				s.saving = false
				os.Remove(original)
				a.toastWarn(i18n.T("Could not save the voice profile: %v", err))
				return
			}
			v.RefSeconds = secs
			a.cloneSaveFiles(v, core.VoiceFiles{Reference: path, Original: original, Portrait: portrait}, gen)
		})
	})
}

// cloneSaveFiles saves the voice with its files (removing the temporary
// ones), then opens it.
func (a *app) cloneSaveFiles(v core.Voice, files core.VoiceFiles, gen int) {
	s := &a.clone
	svc := a.lib.svc.Voices
	a.background(func() {
		saved, err := svc.SaveFiles(context.Background(), v, files)
		os.Remove(files.Reference)
		if files.Original != "" {
			os.Remove(files.Original)
		}
		a.post(func() {
			if s.gen == gen {
				s.saving = false
			}
			if err != nil {
				a.toastWarn(i18n.T("Could not save the voice profile: %v", err))
				return
			}
			replaced := s.replaceFor != ""
			a.voiceChanged(core.VoiceChanged{Voice: saved})
			a.encodePrompt(saved, a.preset())
			a.resetClone("") // the next visit to Clone starts with an empty form
			if replaced {
				a.toast(i18n.T("Reference replaced"))
				a.openVoiceEditor(saved.ID)
				return
			}
			// The last step: the hand-offs (clone_steps.go).
			a.clone.step, a.clone.savedID = cloneDone, saved.ID
		})
	})
}

// cloneUseOnce hands the clip to the Studio without saving a voice.
func (a *app) cloneUseOnce() {
	s := &a.clone
	if a.cloneProblem() != "" {
		return
	}
	text, style, name := strings.TrimSpace(s.transcript), s.style, s.srcName
	a.cloneSelection(func(path string, _ float64, err error) {
		if err != nil {
			a.toastWarn(i18n.T("Could not use the clip: %v", err))
			return
		}
		a.studioClip = &clipRef{Path: path, Text: text, Name: name, Style: style}
		a.studioVoice = ""
		a.resetClone("") // the clip went to the Studio: the next visit starts afresh
		if p := a.preset(); !p.Has(catalog.CapClone) {
			if ms := cloneModels(); len(ms) > 0 {
				a.selectModel(ms[0])
			}
		}
		a.navigate("/studio")
	})
}

// encodePrompt caches the voice's prompt for the Studio's model, when
// the model can reuse one (tts.encodeVoice): its takes then skip
// encoding the reference.
func (a *app) encodePrompt(v core.Voice, e catalog.Entry) {
	if !e.Has(catalog.CapVoicePrompt) || !e.Has(catalog.CapClone) || v.RefAudio == "" {
		return
	}
	svc := a.lib.svc.Voices
	ref, out := a.voicePath(&v, v.RefAudio), a.workPath("prompt")
	a.encodeVoice(e, ref, v.RefText, v.Language, out, func(err error) {
		if err != nil {
			os.Remove(out)
			return // the engine encodes the reference with each take instead
		}
		a.background(func() {
			ctx := context.Background()
			if cur, err := svc.Get(ctx, v.ID); err == nil {
				svc.SaveFiles(ctx, cur, core.VoiceFiles{Prompt: out, PromptModel: e.Repo})
			}
			os.Remove(out)
		})
	})
}
