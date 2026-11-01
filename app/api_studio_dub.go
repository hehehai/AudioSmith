package main

// Dubbing and the batch queue for agents (thread #35): projects made as
// the Dub page makes them (Settings → Dubbing's choices), runs and edits
// through the dub service, exports as the export panel queues them. This
// file creates, reads and runs dubs; edits and exports are
// api_studio_dub_edit.go, the batch queue api_studio_batch.go.

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/lang"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/translate"
)

// dubStage is the stage a run goes through.
func dubStage(through string) dub.Stage {
	switch through {
	case "transcript":
		return dub.StageReferences
	case "translation":
		return dub.StageTranslate
	case "verify":
		return dub.StageQC
	}
	return dub.StageMix
}

// dubVoiceRef resolves a dub's voice: a saved voice (id or name) → its
// id; "speaker:", "design:", "gallery:" and "preset:" references pass.
func dubVoiceRef(voices []core.Voice, name, param string) (string, error) {
	n := strings.TrimSpace(name)
	if n == "" || isDefault(n) {
		return "", nil
	}
	for _, p := range []string{dub.SpeakerPrefix, dub.DesignPrefix, dub.GalleryPrefix, dub.PresetPrefix} {
		if strings.HasPrefix(n, p) {
			return n, nil
		}
	}
	v, err := savedVoice(voices, n)
	if err != nil {
		return "", err
	}
	if v == nil {
		return "", apiErr(http.StatusBadRequest, api.CodeVoiceNotFound, param, "No saved voice is called %s: list_voices lists them.", n)
	}
	return v.ID, nil
}

// checkLangs checks BCP-47 codes.
func checkLangs(param string, codes []string) error {
	for _, c := range codes {
		if _, ok := lang.Lookup(c); !ok {
			return errBadRequest(param, "%q is not a language code (BCP-47: en, es, zh, ja…).", c)
		}
	}
	return nil
}

// dubReady checks what a run through stage needs: the speech model
// downloaded and its licence accepted, and a translator (Settings →
// Writing help) unless every line is translated.
func (b *apiBackend) dubReady(ctx context.Context, p *dub.Project, through dub.Stage) error {
	if through == dub.StageMix {
		e := entryFor(p.Settings.Model)
		if err := b.modelReady(ctx, e); err != nil {
			return err
		}
	}
	if through == dub.StageTranslate || through == dub.StageMix {
		missing := len(p.Segments) == 0
		for _, l := range p.Languages() {
			if len(p.Untranslated(l)) > 0 {
				missing = true
			}
		}
		if missing {
			if _, err := dubMakeTranslator(b.router, p.Settings); err != nil {
				var te *translate.Error
				if errors.As(err, &te) {
					return apiErr(http.StatusBadRequest, services.DubCodeNoTranslator, "",
						"Translating needs a translation engine: ask the user to set up a local model (Ollama, LM Studio) or a provider in AudioSmith → Settings → Writing help.")
				}
				return err
			}
		}
	}
	return nil
}

// CreateDub makes a dub of a file, as dropping it on the Dub page does,
// and starts it (through: transcript, translation, dub; none: not yet).
func (b *apiBackend) CreateDub(ctx context.Context, req api.DubCreate) (out api.DubJSON, err error) {
	if b.dubs == nil {
		return api.DubJSON{}, errNoModule("Dubbing")
	}
	src, err := b.dubSource(ctx, req)
	if err != nil {
		return api.DubJSON{}, err
	}
	p := newDubProject(src, b.lib.settings.Get().Dubbing, dubDefaultModelBy(b.onDisk))
	if t := strings.TrimSpace(req.Title); t != "" {
		p.Title = t
	}
	if sl := strings.TrimSpace(req.SourceLanguage); sl != "" {
		if err := checkLangs("source_language", []string{sl}); err != nil {
			return api.DubJSON{}, err
		}
		p.SourceLang = sl
	}
	if len(req.Targets) > 0 {
		if err := checkLangs("target_languages", req.Targets); err != nil {
			return api.DubJSON{}, err
		}
		p.ChooseTargets(req.Targets)
	}
	if req.Model != "" {
		e, warn, err := ttsEntry(req.Model, "", true) // a dub clones its speakers
		if err != nil {
			return api.DubJSON{}, err
		}
		p.Settings.Model = e.Repo
		defer func() {
			if err == nil {
				out.Warning = warn
			}
		}()
	}
	if req.Voice != "" {
		voices, err := b.voices(ctx)
		if err != nil {
			return api.DubJSON{}, err
		}
		ref, err := dubVoiceRef(voices, req.Voice, "voice")
		if err != nil {
			return api.DubJSON{}, err
		}
		if ref != "" {
			p.Cast = map[string]string{dub.CastAll: ref}
		}
	}
	p.Speakers = req.Speakers
	if err := applyDubDialogue(&p.Settings, req.Dialogue, req.DialogueModel); err != nil {
		return api.DubJSON{}, err
	}
	if req.KeepBackground != nil {
		p.Settings.KeepBackground = *req.KeepBackground
	}
	through := req.Through
	if through != "none" {
		if err := b.dubReady(ctx, p, dubStage(through)); err != nil {
			return api.DubJSON{}, err
		}
	}
	created, err := b.dubs.Create(ctx, p)
	if err != nil {
		return api.DubJSON{}, err
	}
	if src.Kind == dub.SourceURL {
		go b.dubs.Probe(context.WithoutCancel(ctx), created.ID) // the link's details, as the page does
	}
	if through != "none" {
		if _, err := b.dubs.Run(ctx, created.ID, services.RunRequest{Through: dubStage(through), Priority: jobs.PriorityNormal}); err != nil {
			return api.DubJSON{}, err
		}
	}
	return b.Dub(ctx, api.DubQuery{ID: created.ID})
}

// dubSource is a new dub's source: a media file, or a link (yt-dlp, when
// Settings → Dubbing allows links), as the Dub page's composer takes them.
func (b *apiBackend) dubSource(ctx context.Context, req api.DubCreate) (dub.Source, error) {
	if req.URL != "" {
		if _, err := dub.CheckURL(ctx, req.URL, nil); err != nil {
			code := api.CodeInvalid
			var ue *dub.URLError
			if errors.As(err, &ue) {
				code = ue.Code
			}
			return dub.Source{}, apiErr(http.StatusBadRequest, code, "url", "%s", dubURLErrorText(err))
		}
		if d := b.lib.settings.Get().Dubbing; !d.URLImport || d.YtDlp == "" {
			return dub.Source{}, apiErr(http.StatusConflict, dub.CodeNoYtDlp, "url",
				"Importing from a link is off: ask the user to turn it on in AudioSmith → Settings → Dubbing (it downloads yt-dlp), or download the video and give its path.")
		}
		return dub.Source{Kind: dub.SourceURL, URL: req.URL, Name: req.URL}, nil
	}
	if !dub.IsMedia(req.Path, dub.MediaExts) {
		return dub.Source{}, apiErr(http.StatusBadRequest, api.CodeUnsupportedFormat, "path", "%s is not a video or audio file AudioSmith dubs (%s).", filepath.Base(req.Path), strings.Join(dub.MediaExts, ", "))
	}
	src := dub.Source{Kind: dub.SourceFile, Path: req.Path, Name: filepath.Base(req.Path)}
	if fi, err := os.Stat(req.Path); err == nil {
		src.Size = fi.Size()
	}
	return src, nil
}

// Dub reads a dub: its stages, speakers, tracks and lines in a language.
func (b *apiBackend) Dub(ctx context.Context, q api.DubQuery) (api.DubJSON, error) {
	if b.dubs == nil {
		return api.DubJSON{}, errNoModule("Dubbing")
	}
	p, err := b.dubs.Get(ctx, q.ID)
	if err != nil {
		return api.DubJSON{}, notFound(err, "dub", q.ID)
	}
	active, _ := b.dubs.Active(ctx, p.ID)
	out := api.DubJSON{ID: p.ID, Title: p.Title, Source: firstOf(p.Source.Path, p.Source.URL), Duration: round1(p.Source.Duration),
		SourceLang: p.SourceLang, Detected: p.Detected, Targets: p.Targets, Phase: string(p.Phase()), Translator: p.Settings.Translator,
		Batch: p.Batch != "", Running: len(active) > 0, JobID: services.DubOwner + p.ID, Updated: p.UpdatedAt}
	if e, ok := catalog.Builtin().ByRepo(p.Settings.Model); ok {
		out.Model = e.ID
	} else {
		out.Model = p.Settings.Model
	}
	voices, _ := b.voices(ctx)
	label := func(ref string) string {
		if n := voiceLabel(voices, ref); n != "" {
			return n
		}
		return ref
	}
	for _, sp := range p.SpeakerLabels() {
		ref := p.Cast[sp]
		if ref == "" {
			ref = p.Cast[dub.CastAll]
		}
		out.Speakers = append(out.Speakers, api.DubSpeakerJSON{Label: sp, Name: p.Names[sp], Voice: label(ref)})
	}
	for k, st := range p.Stages {
		if out.Stages == nil {
			out.Stages = map[string]api.DubStageJSON{}
		}
		out.Stages[k] = api.DubStageJSON{Status: string(st.Status), Code: st.Code, Message: st.Message, Warnings: st.Warnings}
	}
	for l, t := range p.Tracks {
		if out.Tracks == nil {
			out.Tracks = map[string]api.DubTrackJSON{}
		}
		tj := api.DubTrackJSON{File: b.dubs.Path(p.ID, t.File), Speech: b.dubs.Path(p.ID, t.Speech), Seconds: round1(t.Seconds)}
		if t.QC != nil {
			tj.Flagged = t.QC.Flagged
		}
		out.Tracks[l] = tj
	}
	l := q.Language
	if l == "" && len(p.Targets) > 0 {
		l = p.Targets[0]
	}
	if q.Segments != nil && !*q.Segments {
		b.dubDialogueJSON(&out, p, l)
		return out, nil
	}
	out.Language = l
	t := p.Track(l)
	for _, s := range p.Segments {
		sj := api.DubSegmentJSON{ID: s.ID, Start: round2(s.Start), End: round2(s.End), Speaker: s.Speaker, Text: s.Text,
			Voice: label(s.Voice), NoDialogue: s.NoDialogue}
		if l != "" {
			sj.Translation = p.TextFor(s, l)
			if ln := s.Lines[dub.LangFor(s, l)]; ln.Err != "" {
				sj.Error = ln.Err
			}
			if r, ok := t.Lines[s.ID]; ok {
				sj.Rendered = r.File != "" && r.Err == ""
				if r.Err != "" {
					sj.Error = r.Err
				}
			}
		}
		out.Segments = append(out.Segments, sj)
	}
	b.dubDialogueJSON(&out, p, l)
	return out, nil
}

// RunDub runs a dub's pipeline as the page's buttons do.
func (b *apiBackend) RunDub(ctx context.Context, req api.DubRun) (api.JobJSON, error) {
	if b.dubs == nil {
		return api.JobJSON{}, errNoModule("Dubbing")
	}
	p, err := b.dubs.Get(ctx, req.ID)
	if err != nil {
		return api.JobJSON{}, notFound(err, "dub", req.ID)
	}
	if len(req.Languages) > 0 {
		for _, l := range req.Languages {
			if !slices.Contains(p.Languages(), l) {
				return api.JobJSON{}, errBadRequest("languages", "The dub is not into %s: its languages are %s.", l, strings.Join(p.Languages(), ", "))
			}
		}
	}
	through := dubStage(req.Through)
	if req.Through == "verify" && !slices.ContainsFunc(p.Languages(), func(l string) bool { return p.Tracks[l].File != "" }) {
		return api.JobJSON{}, errNotReady("There is no dubbed track to verify yet: run the dub first.")
	}
	if err := b.dubReady(ctx, p, through); err != nil {
		return api.JobJSON{}, err
	}
	rr := services.RunRequest{Through: through, Langs: req.Languages, Only: req.Segments, Priority: jobs.PriorityNormal}
	if req.Retranslate {
		rr.Force = append(rr.Force, dub.StageTranslate)
	}
	if req.Retranscribe {
		rr.Force = append(rr.Force, dub.StageTranscribe)
	}
	if _, err := b.dubs.Run(ctx, p.ID, rr); err != nil {
		if errors.Is(err, core.ErrBusy) {
			return api.JobJSON{}, errBusy("The dub is running: follow get_job %s, or cancel it.", services.DubOwner+p.ID)
		}
		return api.JobJSON{}, err
	}
	return b.dubTask(ctx, p.ID)
}

// dubTask is a dub's job: "dub:<id>", running while it has jobs.
func (b *apiBackend) dubTask(ctx context.Context, id string) (api.JobJSON, error) {
	if b.dubs == nil {
		return api.JobJSON{}, errNoModule("Dubbing")
	}
	p, err := b.dubs.Get(ctx, id)
	if err != nil {
		return api.JobJSON{}, notFound(err, "dub", id)
	}
	parts, cur := b.activeJobs(ctx, services.DubOwner+id)
	state := jobs.StateSucceeded
	stage, msg, progress := "", "", 1.0
	var jerr *jobs.Error
	if cur != nil {
		state, stage, progress = jobs.StateQueued, cur.Title, -1
		if cur.State == jobs.StateRunning {
			state = jobs.StateRunning
			progress = cur.Progress.Fraction
			msg = cur.Progress.Message
		}
	} else {
		for _, k := range p.Failed() {
			st := p.Stages[k]
			code := firstOf(st.Code, services.DubCodeFailed)
			if code == jobs.CodeCancelled {
				state, jerr = jobs.StateCancelled, &jobs.Error{Code: code, Message: "The run was cancelled: run_dub goes on where it stopped."}
				continue
			}
			state, jerr = jobs.StateFailed, &jobs.Error{Code: code, Message: k + ": " + firstOf(st.Message, code)}
			break
		}
		for _, st := range p.Stages {
			if st.Code == jobs.CodeCancelled && jerr == nil {
				state, jerr = jobs.StateCancelled, &jobs.Error{Code: st.Code, Message: "The run was cancelled: run_dub goes on where it stopped."}
			}
		}
	}
	var files []string
	tracks := map[string]string{}
	for _, l := range p.Languages() {
		if f := p.Tracks[l].File; f != "" {
			files = append(files, b.dubs.Path(p.ID, f))
			tracks[l] = b.dubs.Path(p.ID, f)
		}
	}
	res := map[string]any{"dub_id": p.ID, "phase": p.Phase(), "tracks": tracks, "segments": len(p.Segments)}
	return api.GroupJob(services.DubOwner+id, "dub.run", p.Title, state, stage, progress, msg, jerr, files, res, parts), nil
}
