package main

// Dubbing's texts: errors, stages, warnings, phases, language names and
// the hints of the step panels; split from dub.go.

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/lang"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/translate"
)

// dubErrorText explains a failed stage.
func dubErrorText(e *jobs.Error) string {
	if e == nil {
		return ""
	}
	switch e.Code {
	case services.DubCodeNoTranslator:
		return i18n.T("Translating needs a translation engine: a local model (Ollama, LM Studio) or a provider in Settings → Writing help.")
	case services.DubCodeNoSpeech:
		return i18n.T("No speech was found in the source.")
	case services.DubCodeNoModel:
		return i18n.T("Choose a voice model for the dub.")
	case services.DubCodeNoLines:
		return i18n.T("Nothing to say in this language: translate it first.")
	case services.DubCodeNeedsEngine:
		return i18n.T("The engine cannot do this yet. It comes with a later engine update.")
	case services.DubCodeNeedsFFmpeg:
		return i18n.T("This needs ffmpeg: set it up in Settings → Audio tools.") // M7a
	case services.DubCodeNotMixed:
		return i18n.T("That track is not generated yet.")
	case "dub.no_audio":
		return i18n.T("The file has no sound to dub.")
	case "dub.no_video":
		return i18n.T("The source has no video: export WAV or M4A.")
	case dub.CodeNoYtDlp, dub.CodeURLScheme, dub.CodeURLPrivate, dub.CodeURLPlaylist, dub.CodeURLLive, dub.CodeURLManifest, dub.CodeURLInvalid, dub.CodeYtDlp, dub.CodeCookies:
		var d struct{ Detail string }
		json.Unmarshal(e.Data, &d)
		return dubURLErrorText(&dub.URLError{Code: e.Code, Detail: d.Detail})
	case translate.CodeAuth, translate.CodeNoKey:
		return i18n.T("The translation service refused the key. Check it in Settings → Writing help.")
	case translate.CodeQuota:
		return i18n.T("The translation service's quota is used up. Try again later.")
	case translate.CodeNetwork:
		return i18n.T("The translation service could not be reached.")
	}
	return jobErrorText(e)
}

// dubURLErrorText explains a refused URL.
func dubURLErrorText(err error) string {
	var ue *dub.URLError
	if !errors.As(err, &ue) {
		return err.Error()
	}
	switch ue.Code {
	case dub.CodeURLScheme:
		return i18n.T("Only http and https links can be imported.")
	case dub.CodeURLPrivate:
		return i18n.T("Links to this Mac or the local network cannot be imported.")
	case dub.CodeURLPlaylist:
		return i18n.T("That is a playlist: paste the link of one video.")
	case dub.CodeURLLive:
		return i18n.T("Live streams and premieres cannot be imported.")
	case dub.CodeURLManifest:
		return i18n.T("Stream manifests (HLS, DASH) cannot be imported: paste the video page's link.")
	case dub.CodeNoYtDlp:
		return i18n.T("Importing from a URL is off. Turn it on in Settings → Dubbing (it downloads yt-dlp, about 35 MB).")
	case dub.CodeCookies:
		return i18n.T("The cookie export must be 1 MB or smaller.")
	case dub.CodeChecksum:
		return i18n.T("The download of yt-dlp did not match its checksum, so it was not installed.")
	case dub.CodeYtDlp:
		if strings.Contains(ue.Detail, "Requested format is not available") {
			return i18n.T("The site offers this video only as separate video and audio streams, which need ffmpeg to join: set ffmpeg up in Settings → Audio tools, then try again.")
		}
		if ue.Detail != "" {
			return i18n.T("The download failed: %s", ue.Detail)
		}
		return i18n.T("The download failed.")
	}
	return i18n.T("That is not a link to a video.")
}

// dubStageName names a stage.
func dubStageName(s dub.Stage) string {
	switch s {
	case dub.StageFetch:
		return i18n.T("Downloading the video…")
	case dub.StageIngest:
		return i18n.T("Reading the source…")
	case dub.StageSeparate:
		return i18n.T("Separating voices and music…")
	case dub.StageTranscribe:
		return i18n.T("Transcribing…")
	case dub.StageReferences:
		return i18n.T("Picking each speaker's voice…")
	case dub.StageTranslate:
		return i18n.T("Translating…")
	case dub.StageSynthesize:
		return i18n.T("Generating speech…")
	case dub.StageMix:
		return i18n.T("Mixing the track…")
	case dub.StageQC:
		return i18n.T("Checking the dub…")
	}
	return i18n.T("Working…")
}

// dubStageOfKind is the stage a job kind runs.
func dubStageOfKind(kind string) dub.Stage {
	return dub.Stage(strings.TrimPrefix(kind, "dub."))
}

// dubWarningText explains a stage's warning code.
func dubWarningText(code string) string {
	switch code {
	case services.DubWarnNoSeparation:
		return i18n.T("Voices and music could not be separated (the engine cannot yet): the original audio plays quietly under the dub.")
	case services.NoticeWatermarkFailed: // thread #14
		return i18n.T("The invisible watermark could not be added to the dubbed track.")
	case services.DubWarnNoDiarization:
		return i18n.T("Speakers could not be told apart (the engine cannot yet): every line is one speaker's. Set speakers by hand.")
	case services.DubWarnTooManySpk:
		return i18n.T("The speaker model tells at most 4 speakers apart: set the others by hand.")
	case services.DubWarnSpeakersModel:
		return i18n.T("Speakers were guessed without %s, which tells them apart far more reliably: download it and transcribe again.", modelRef(dubSpeakersEntry()))
	case services.DubWarnNoPoster:
		return i18n.T("No preview frame: the engine cannot make one yet.")
	case services.DubWarnNoProxy:
		return i18n.T("This video's format cannot play in the preview; the audio still dubs.")
	case services.DubWarnNoEnhance:
		return i18n.T("The voice references were not cleaned of noise.")
	case services.DubWarnCaptions:
		return i18n.T("The video's own captions were used as the transcript.")
	case services.DubWarnNoReference:
		return i18n.T("A speaker says too little to clone: their lines use the default voice unless cast.")
	case services.DubWarnVoice:
		return i18n.T("A cast voice could not be used with this model: those lines use the default voice.")
	case services.DubWarnTranslateFailed:
		return i18n.T("Some lines could not be translated: retry them, or write them.")
	case services.DubWarnLinesFailed:
		return i18n.T("Some lines could not be generated: they stay silent.")
	case services.WarnNoWordTimes:
		return i18n.T("Word timings are missing: karaoke subtitles spread the words evenly.")
	case services.DubWarnDialogueUnavailable:
		return i18n.T("The multi-speaker dialogue model is not downloaded (or this Mac has too little memory for it): every line was made line by line.")
	case services.DubWarnDialogueFallback:
		return i18n.T("Some conversation lines were made line by line: the dialogue model did not say them clearly.")
	case services.DubWarnLinesFlagged:
		return i18n.T("Some lines were not heard clearly, or not in their language, even made again: listen to the lines marked Listen.")
	}
	return code
}

// dubPhaseText names a phase.
func dubPhaseText(ph dub.Phase) string {
	switch ph {
	case dub.PhaseNew:
		return i18n.T("No source")
	case dub.PhaseReady:
		return i18n.T("Ready")
	case dub.PhasePreparing:
		return i18n.T("Preparing")
	case dub.PhaseTranscribed:
		return i18n.T("Transcribed")
	case dub.PhaseTranslating:
		return i18n.T("Translating")
	case dub.PhaseTranslated:
		return i18n.T("Translated")
	case dub.PhaseGenerating:
		return i18n.T("Generating")
	case dub.PhaseDone:
		return i18n.T("Done")
	case dub.PhaseFailed:
		return i18n.T("Failed")
	}
	return string(ph)
}

// dubAutoTargetHint says what the default target does.
func dubAutoTargetHint() string {
	return i18n.T("By default dubs are made in Chinese; a video in Chinese is dubbed into English.")
}

// dubRetargetNote says that p's default target was switched ("" if not).
func dubRetargetNote(p *dub.Project) string {
	if p == nil || !p.TargetsAuto || p.TargetSwitched == "" {
		return ""
	}
	return i18n.T("The video is in %s, so it is dubbed into %s.", dubLangPlain(p.SourceLanguage()), dubLangPlain(p.Primary()))
}

// dubLangPlain is a language's name in the UI's language, without a flag.
func dubLangPlain(code string) string {
	if l, ok := lang.Lookup(code); ok {
		return l.NameIn(i18n.Locale())
	}
	return code
}

// dubLangName names a language for lists: flag and name.
func dubLangName(code string) string {
	if code == "" {
		return i18n.T("Auto-detect")
	}
	if l, ok := lang.Lookup(code); ok {
		name := l.NameIn(i18n.Locale())
		if base, region, ok := strings.Cut(code, "-"); ok && len(region) == 2 {
			if bl, ok := lang.Lookup(base); ok {
				name = bl.NameIn(i18n.Locale()) + " (" + region + ")"
			}
		}
		return strings.TrimSpace(l.Flag() + " " + name)
	}
	return code
}

// dubSpeakersEntry is the model that transcribes and tells speakers
// apart (thread #48).
func dubSpeakersEntry() catalog.Entry {
	e, _ := catalog.Builtin().SpeakersModel()
	return e
}

// dubSpeakersHint recommends the speakers model while it is not
// downloaded, for a source with more than one speaker: a Download button,
// and, once the source is transcribed without it, Transcribe again.
// Nothing downloads without the click.
func (a *app) dubSpeakersHint(c *ui.Context, key string) {
	p := a.dub
	e := dubSpeakersEntry()
	if p == nil || e.ID == "" || p.Speakers == 1 || a.dubDismissed["speakers-model"] {
		return
	}
	if ready, known := a.modelOnDisk(e); ready || !known {
		return
	}
	m := a.model(e)
	text := i18n.T("Lines get the right speaker far more often with %s (%s): it transcribes and tells speakers apart in one pass.", modelRef(e), catalog.HumanSize(e.Size))
	notice(c, key, noticeInfo, text, "", func() {
		label := i18n.T("Download")
		if m.downloading {
			label = i18n.T("Downloading…")
		}
		if secondaryIconButton(c.Key(key+"-download"), icons.DownloadMinimalistic, label).Label(label).Disabled(m.downloading).Clicked() {
			a.download(e)
		}
	}, func() { a.dubDismissed["speakers-model"] = true })
}

// dubRetranscribeHint offers to transcribe again once the speakers model
// is downloaded, when the transcript was made without it.
func (a *app) dubRetranscribeHint(c *ui.Context, key string) {
	p := a.dub
	e := dubSpeakersEntry()
	if p == nil || e.ID == "" || len(p.Segments) == 0 || a.dubBusy() || a.dubDismissed["speakers-again"] ||
		!slices.Contains(p.Stage(dub.StageTranscribe, "").Warnings, services.DubWarnSpeakersModel) {
		return
	}
	if ready, _ := a.modelOnDisk(e); !ready {
		return
	}
	notice(c, key, noticeInfo, i18n.T("%s is ready: transcribe again to tell the speakers apart with it.", modelRef(e)), "", func() {
		if secondaryButton(c.Key(key+"-again"), i18n.T("Transcribe again")).Clicked() {
			a.dubConfirm, a.dubConfirmOpen = "transcribe", true
		}
	}, func() { a.dubDismissed["speakers-again"] = true })
}
