package main

// What the engine link says: the text of a worker's failure, of a job's
// stage and of its error codes, and engineErrorText for any error of an
// engine call or job.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
)

// failureText says, translated, why a worker failed ("" when it did not).
func failureText(st engine.WorkerStatus) string {
	if st.State != engine.WorkerFailed {
		return ""
	}
	var msg string
	switch st.Reason {
	case engine.FailMissing:
		msg = i18n.T("audiosmith-engine was not found: build it with scripts/build-engine.sh, or set AUDIOSMITH_ENGINE.")
	case engine.FailProtocol:
		msg = i18n.T("Engine version mismatch: rebuild audiosmith-engine.") + " (" + st.Err + ")"
	case engine.FailHandshake:
		msg = i18n.T("The engine did not answer in time.")
	case engine.FailStuck:
		msg = i18n.T("A cancelled job did not stop, so the engine was stopped.")
	case engine.FailStart:
		msg = i18n.T("The engine could not be started: %s", st.Err)
	default:
		msg = i18n.T("The engine stopped") + ": " + st.Err
	}
	if !st.RestartAt.IsZero() {
		msg += " " + i18n.T("Restarting…")
	}
	return msg
}

// stageText is a job stage as the Studio shows it.
func stageText(stage string) string {
	switch stage {
	case "loading":
		return i18n.T("Loading model…")
	case "generating":
		return i18n.T("Generating…")
	case "writing":
		return i18n.T("Saving…")
	case "transcribing":
		return i18n.T("Transcribing…")
	case "aligning":
		return i18n.T("Aligning words…")
	case "detecting":
		return i18n.T("Finding speech…")
	case "enhancing":
		return i18n.T("Removing noise…")
	case "decoding":
		return i18n.T("Reading audio…")
	case "encoding":
		return i18n.T("Preparing the voice…")
	case "recording":
		return i18n.T("Recording…")
	case "moving": // family_shared.go: Move models
		return i18n.T("Moving models…")
	}
	return i18n.T("Working…")
}

// jobErrorText explains a failed job, translated where the code is known.
func jobErrorText(e *jobs.Error) string {
	if e == nil {
		return i18n.T("The job failed")
	}
	if d := services.DiskFullJob(e); d != nil {
		return diskFullText(d)
	}
	switch e.Code {
	case jobs.CodeInterrupted, jobs.CodeShutdown:
		return i18n.T("The engine stopped while it worked. Try again.")
	case engine.CodeEngineUnavailable:
		return i18n.T("The engine is not running. Restart it on the Engine page.")
	case proto.CodeModelNotDownloaded:
		return i18n.T("The model is not downloaded. Download it in Settings → Models & network.")
	case proto.CodeLicenceRequired:
		return i18n.T("The model's licence has not been accepted. Review it in Settings → Models & network.")
	case proto.CodeOOM:
		return i18n.T("Out of memory. Unload other models, or choose a smaller one.")
	case proto.CodeInputTooLong:
		return i18n.T("The text is too long for this model in one piece. Split it into shorter parts.")
	case proto.CodeFileNotFound:
		var d struct{ Path string }
		if json.Unmarshal(e.Data, &d) == nil && d.Path != "" {
			return i18n.T("File not found: %s", d.Path)
		}
		return i18n.T("A file the job needs was not found.")
	case proto.CodeUnsupportedMedia:
		return i18n.T("This file's format cannot be read. Convert it to WAV, M4A or MP3 and try again.")
	case proto.CodePermissionDenied:
		return i18n.T("AudioSmith may not use the microphone. Allow it in System Settings → Privacy & Security → Microphone.")
	case proto.CodeDeviceUnavailable:
		return i18n.T("The microphone is not available. Check that it is connected, or choose another input.")
	case core.CodeNeedsReference:
		var d struct{ Model string }
		json.Unmarshal(e.Data, &d)
		if d.Model == "" {
			return needsReferenceText(modelInMessage(e.Message))
		}
		return needsReferenceText(modelRef(entryFor(d.Model)))
	case proto.CodeBadRequest:
		// The engine's own refusal of a clone-only model without a
		// reference, in older or newer words.
		if m := strings.ToLower(e.Message); strings.Contains(m, "only clones voices") || strings.Contains(m, "requires refaudio") {
			return needsReferenceText(modelInMessage(e.Message))
		}
	}
	return readableEngineMessage(e.Message)
}

// needsReferenceText explains that model only clones voices, wherever a
// take was refused for want of a reference clip.
func needsReferenceText(model string) string {
	if model == "" {
		return i18n.T("This model only clones voices: it needs a reference clip or a saved cloned voice.")
	}
	return i18n.T("%s only clones voices: it needs a reference clip or a saved cloned voice.", model)
}

// modelInMessage is the model an engine message starts with
// ("Some-Model-100M only clones…"), as the UI names it in a sentence
// (modelRef), or "".
func modelInMessage(msg string) string {
	for _, e := range catalog.Builtin().Entries {
		if strings.HasPrefix(msg, catalog.ShortRepo(e.Repo)+" ") || strings.HasPrefix(msg, e.Label+" ") {
			return modelRef(e)
		}
	}
	return ""
}

// readableEngineMessage turns Cocoa's generic sentence for a Swift error
// without a description ("The operation couldn’t be completed.
// (MLXAudioTTS.MossTTSNanoError error 0.)", what engines before 0.3.1
// sent) into one that at least names the error; other messages pass.
func readableEngineMessage(msg string) string {
	// Its English and Chinese forms (the engine runs in the Mac's language).
	if !strings.HasPrefix(msg, "The operation couldn") && !strings.HasPrefix(msg, "未能完成操作") {
		return msg
	}
	msg = strings.NewReplacer("（", "(", "）", ")", "错误", " error ", "。", ".").Replace(msg)
	i, j := strings.LastIndex(msg, "("), strings.LastIndex(msg, ")")
	if i < 0 || j < i {
		return i18n.T("The engine failed without saying why.")
	}
	name := strings.Join(strings.Fields(strings.TrimSuffix(strings.TrimSpace(msg[i+1:j]), ".")), " ")
	return i18n.T("The engine failed: %s.", name)
}

// engineErrorText explains an error of an engine call or job (a
// *jobs.Error from the executors and engine.Probe/Devices/StartCapture),
// translated where the code is known.
func engineErrorText(err error) string {
	if d := services.DiskFullJob(err); d != nil {
		return diskFullText(d)
	}
	var je *jobs.Error
	if errors.As(err, &je) {
		return jobErrorText(je)
	}
	var nr *core.NeedsReferenceError
	if errors.As(err, &nr) {
		if m := modelInMessage(nr.Model + " "); m != "" {
			return needsReferenceText(m) // the label, as the UI names it
		}
		return needsReferenceText(nr.Model)
	}
	if errors.Is(err, context.Canceled) {
		return i18n.T("Cancelled")
	}
	return err.Error()
}
