package main

// The toolkit's voice management (thread #135): rename and edit,
// duplicate, .asvoice export and import, preview clips and the reference
// check, as My voices' menu, the voice editor, the pickers' ▶ and Quick
// clone's checker do (voices_view.go, voice_editor.go, voice_persona.go,
// voice_preview.go, reference_check.go), over the voice service.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp/refcheck"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/gallery"
	"github.com/hehehai/AudioSmith/internal/gallery/persona"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
)

// savedVoiceOf finds a saved voice by id or name (404 when none).
func (b *apiBackend) savedVoiceOf(ctx context.Context, name string) (core.Voice, error) {
	voices, err := b.voices(ctx)
	if err != nil {
		return core.Voice{}, err
	}
	v, err := savedVoice(voices, name)
	if err != nil {
		return core.Voice{}, err
	}
	if v == nil {
		return core.Voice{}, apiErr(http.StatusNotFound, api.CodeVoiceNotFound, "voice_id", "No saved voice is called %s: list_voices lists them.", name)
	}
	return *v, nil
}

// UpdateVoice changes a saved voice's details, as the voice editor does.
func (b *apiBackend) UpdateVoice(ctx context.Context, req api.VoiceUpdate) (core.Voice, error) {
	v, err := b.savedVoiceOf(ctx, req.ID)
	if err != nil {
		return core.Voice{}, err
	}
	if n := core.CleanName(req.Name); n != "" {
		v.Name = n
	}
	if s := strings.TrimSpace(req.Style); s != "" {
		v.Style = s
	}
	if l := strings.TrimSpace(req.Language); l != "" {
		v.Language = l
	}
	if t := strings.TrimSpace(req.RefText); t != "" {
		if v.Kind != core.VoiceCloned {
			return core.Voice{}, errBadRequest("ref_text", "%s is not a cloned voice: only cloned voices have a reference text.", v.Name)
		}
		v.RefText = t
	}
	if req.Tags != nil {
		v.Tags = nil
		for _, tag := range *req.Tags {
			if tag = strings.TrimSpace(tag); tag != "" && !slices.Contains(v.Tags, tag) {
				v.Tags = append(v.Tags, tag)
			}
		}
	}
	return b.lib.svc.Voices.Save(ctx, v)
}

// voiceCopy is a copy of v as Duplicate makes it (voices_view.go): its
// reference, sample and portrait, not its lock, ownership or prompts.
func voiceCopy(v core.Voice, name string, path func(string) string) (core.Voice, core.VoiceFiles, string) {
	cp := v
	cp.ID, cp.Name, cp.Origin = "", name, ""
	cp.Locked, cp.LockTake, cp.Consent, cp.Prompts = false, "", core.VoiceConsent{}, nil
	cp.RefAudio, cp.Sample, cp.Portrait, cp.RefOriginal = "", "", "", ""
	var files core.VoiceFiles
	if v.RefAudio != "" {
		files.Reference = path(v.RefAudio)
	}
	if v.RefOriginal != "" {
		files.Original = path(v.RefOriginal)
	}
	if v.Sample != "" {
		files.Sample = path(v.Sample)
	}
	portrait := ""
	if v.Portrait != "" {
		portrait = path(v.Portrait)
	}
	return cp, files, portrait
}

// DuplicateVoice saves a copy of a voice.
func (b *apiBackend) DuplicateVoice(ctx context.Context, req api.VoiceDuplicate) (core.Voice, error) {
	v, err := b.savedVoiceOf(ctx, req.ID)
	if err != nil {
		return core.Voice{}, err
	}
	name := core.CleanName(req.Name)
	if name == "" {
		name = core.CleanName(i18n.T("%s (copy)", v.Name)) // as Voices → Mine names it
	}
	cp, files, portrait := voiceCopy(v, name, func(n string) string { return b.lib.svc.Voices.FilePath(v, n) })
	if portrait != "" {
		files.Portrait, _ = os.ReadFile(portrait)
	}
	return b.lib.svc.Voices.SaveFiles(ctx, cp, files)
}

// ExportVoice writes a voice as an .asvoice file.
func (b *apiBackend) ExportVoice(ctx context.Context, req api.VoiceExportRequest) (api.ExportJSON, error) {
	v, err := b.savedVoiceOf(ctx, req.ID)
	if err != nil {
		return api.ExportJSON{}, err
	}
	name := strings.TrimSuffix(firstOf(strings.TrimSpace(req.Filename), exportVoiceName(v.Name)), persona.Ext)
	dst := filepath.Join(req.Dir, api.SafeName(name, "voice", strings.TrimPrefix(persona.Ext, ".")))
	err = b.lib.svc.Voices.Export(ctx, v.ID, dst, core.VoiceExport{IncludeReference: req.IncludeReference, Licence: strings.TrimSpace(req.Licence),
		App: "AudioSmith", AppVersion: b.version})
	if errors.Is(err, services.ErrNoPreview) {
		return api.ExportJSON{}, apiErr(http.StatusConflict, "not_ready", "voice_id",
			"%s has no audio to build a preview from: generate a take with it first (generate_speech).", v.Name)
	}
	if err != nil {
		return api.ExportJSON{}, err
	}
	if lib := b.lib.svc.Library; lib != nil { // Projects → Exports, as the app records it
		lib.RecordExport(ctx, core.Export{Kind: "voice", SourceID: v.ID, Title: v.Name, Path: dst, Format: "asvoice"})
	}
	out := api.ExportJSON{Files: []string{dst}, Format: "asvoice"}
	if fi, err := os.Stat(dst); err == nil {
		out.Bytes = fi.Size()
	}
	return out, nil
}

// ImportVoice reads a voice file into My voices; its reference is
// checked and cleaned afterwards, as an import in the app's is.
func (b *apiBackend) ImportVoice(ctx context.Context, path string) (api.VoiceImported, error) {
	res, err := b.lib.svc.Voices.Import(ctx, path)
	if err != nil {
		return api.VoiceImported{}, errBadRequest("path", "%s", importErrorText(filepath.Base(path), err))
	}
	if b.cleanRef != nil {
		b.cleanRef(res.Voice)
	}
	out := api.VoiceImported{Voice: api.VoiceOf(res.Voice), Warnings: res.Warnings}
	return out, nil
}

// VoicePreview is a voice's preview clip.
func (b *apiBackend) VoicePreview(ctx context.Context, q api.VoicePreviewQuery) (api.VoicePreview, error) {
	switch {
	case strings.TrimSpace(q.VoiceID) != "":
		v, err := b.savedVoiceOf(ctx, q.VoiceID)
		if err != nil {
			return api.VoicePreview{}, err
		}
		file := firstOf(v.Sample, v.RefAudio)
		if file == "" {
			if v.Kind == core.VoiceBuiltin && v.Speaker != "" {
				return speakerClip(entryFor(v.Model), v.Speaker)
			}
			return api.VoicePreview{}, apiErr(http.StatusConflict, "not_ready", "voice_id",
				"%s has no audio yet: generate_speech with it makes a take to hear.", v.Name)
		}
		data, err := clipWAV(b.lib.svc.Voices.FilePath(v, file), previewLimit)
		if err != nil {
			return api.VoicePreview{}, err
		}
		text := ""
		if file == v.RefAudio {
			text = v.RefText
		}
		return api.VoicePreview{Data: data, MIME: "audio/wav", Ext: "wav", Name: exportVoiceName(v.Name) + ".wav", Text: text}, nil
	case strings.TrimSpace(q.Gallery) != "":
		arch, ok := gallery.Default().ByID(strings.TrimSpace(q.Gallery))
		if !ok {
			return api.VoicePreview{}, apiErr(http.StatusNotFound, api.CodeNotFound, "gallery_id", "There is no Discover voice %q: search_voices lists them.", q.Gallery)
		}
		if arch.Kind == gallery.KindBuiltin {
			return speakerClip(entryFor(arch.Model), arch.Speaker)
		}
		data := galleryClip(arch.ID, "preview")
		if data == nil {
			return api.VoicePreview{}, apiErr(http.StatusNotFound, api.CodeNotFound, "gallery_id",
				"%s has no bundled preview: design_voice_candidates with its description makes one.", arch.ID)
		}
		text, _ := arch.PreviewLine()
		return api.VoicePreview{Data: data, MIME: "audio/ogg", Ext: "ogg", Name: exportVoiceName(arch.ID) + ".ogg", Text: text, Model: galleryDesignModel}, nil
	}
	e, ok := catalog.Builtin().ByID(strings.TrimSpace(q.Model))
	if !ok {
		return api.VoicePreview{}, apiErr(http.StatusNotFound, api.CodeModelNotFound, "model", "There is no model %q: list_models lists them.", q.Model)
	}
	return speakerClip(e, strings.TrimSpace(q.Speaker))
}

// speakerClip is a built-in speaker's bundled preview.
func speakerClip(e catalog.Entry, speaker string) (api.VoicePreview, error) {
	for _, sp := range e.AllVoiceIDs() {
		if strings.EqualFold(sp, speaker) {
			if data := builtinPreview(e, sp); data != nil {
				return api.VoicePreview{Data: data, MIME: "audio/ogg", Ext: "ogg", Name: e.ID + "-" + sp + ".ogg", Model: e.ID}, nil
			}
			return api.VoicePreview{}, apiErr(http.StatusNotFound, api.CodeNotFound, "speaker",
				"%s's speaker %s has no bundled preview: generate_speech with it makes one.", e.ID, sp)
		}
	}
	return api.VoicePreview{}, apiErr(http.StatusBadRequest, api.CodeVoiceNotFound, "speaker", "%s has no built-in speaker %q (list_models → voices).", e.ID, speaker)
}

// clipWAV is the start of a WAV (at most limit), as WAV bytes.
func clipWAV(path string, limit time.Duration) ([]byte, error) {
	// Only the start is read (thread #150).
	buf, _, err := wav.ReadSeconds(path, 0, limit.Seconds())
	if err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp("", "audiosmith-preview-*.wav")
	if err != nil {
		return nil, err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	if err := wav.WriteFile(tmp.Name(), buf, wav.PCM16, wav.Options{}); err != nil {
		return nil, err
	}
	return os.ReadFile(tmp.Name())
}

// refGradeName is a grade as agents get it.
func refGradeName(g refcheck.Grade) string {
	switch g {
	case refcheck.Good:
		return "good"
	case refcheck.OK:
		return "fine"
	case refcheck.Poor:
		return "needs_work"
	}
	return "unknown"
}

// CheckReference grades a recording as a clone reference (Quick clone's
// checker, without its automatic cleaning: clone_voice cleans the
// reference it saves).
func (b *apiBackend) CheckReference(ctx context.Context, req api.RefCheckRequest) (api.RefCheckJSON, error) {
	work := filepath.Join(b.tmp, "refcheck-"+core.NewID(time.Now())+".wav")
	if err := os.MkdirAll(b.tmp, 0o755); err != nil {
		return api.RefCheckJSON{}, err
	}
	defer os.Remove(work)
	if _, err := decodeWAV(req.Path, work); err != nil || !isWAVPath(req.Path) {
		if err := b.engineRun(ctx, proto.MethodMediaDecode, proto.DecodeParams{Input: req.Path, Output: proto.Output{Path: work, Format: "wav"},
			SampleRate: refRate, Channels: 1}); err != nil {
			return api.RefCheckJSON{}, err
		}
	}
	text := strings.TrimSpace(req.Text)
	r, err := measureRef(work, span{}, nil, text, "")
	if err != nil {
		return api.RefCheckJSON{}, err
	}
	out := api.RefCheckJSON{Grade: refGradeName(r.Grade(refcheck.UseClone)), Seconds: round2(r.Seconds), SpeechSeconds: round2(r.Speech), Noisy: r.Noisy()}
	for _, h := range r.Hints(refcheck.UseClone) {
		out.Hints = append(out.Hints, api.HintJSON{Code: h.Code, Text: refHintText(h.Code, false)})
	}
	switch {
	case out.Grade == "needs_work":
		out.Note = "Fix the hints (or record again) before clone_voice; 5–15 s of one person speaking clearly works best."
	case r.Noisy():
		out.Note = "clone_voice removes the background noise of the reference it saves by itself."
	}
	return out, nil
}

// engineRun runs an engine method as a queue job and waits for its end.
func (b *apiBackend) engineRun(ctx context.Context, kind string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	done := make(chan jobs.Job, 1)
	id, err := b.lib.sched.Submit(ctx, jobs.Spec{Kind: kind, Lane: jobs.Lane(proto.RoleOf(kind)), Priority: jobs.PriorityInteractive,
		Params: raw, Title: workTitle(kind), Owner: "api:" + core.NewID(time.Now())})
	if err != nil {
		return err
	}
	b.whenDone(id, func(j jobs.Job) { done <- j })
	select {
	case j := <-done:
		if j.State == jobs.StateSucceeded {
			return nil
		}
		if j.Err != nil {
			return j.Err
		}
		return errors.New("the engine job " + string(j.State))
	case <-ctx.Done():
		b.lib.sched.Cancel(context.Background(), id)
		return ctx.Err()
	}
}
