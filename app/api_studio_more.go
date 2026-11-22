package main

// Voice discovery, take exports and workflows for agents (thread #35):
// the gallery's search and "Use voice" (gallery.go), the Export audio
// dialog's export (export_run.go), and running saved workflows
// (workflow.go) without editing their steps.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/gallery"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/workflow"
)

// --- voice discovery ---------------------------------------------------------------

// galleryVoiceJSON is a gallery voice as agents see it, in the UI's
// language.
func galleryVoiceJSON(a gallery.Archetype, loc string, saved map[string]string) api.GalleryVoiceJSON {
	attrs, _ := json.Marshal(a.Attrs.Canonical())
	sample, _ := a.PreviewLine()
	return api.GalleryVoiceJSON{ID: a.ID, Name: a.Name.In(loc), Description: a.Description.In(loc), Category: a.Category, Uses: a.Uses,
		Kind: a.Kind, Gender: a.Gender, Age: a.Age, Languages: a.Languages, Tags: a.Tags, Attributes: attrs, Persona: a.Persona,
		Model: a.Model, Speaker: a.Speaker, Sample: sample, Featured: a.Featured, Saved: saved[a.ID]}
}

// savedArchetypes maps gallery voice ids to the voices "Use voice" saved.
func savedArchetypes(voices []core.Voice) map[string]string {
	out := map[string]string{}
	for _, v := range voices {
		if id, ok := strings.CutPrefix(v.Origin, "gallery:"); ok {
			out[id] = v.ID
		}
	}
	return out
}

// SearchGallery searches Discover voices with the page's query
// (galleryFilterQuery) and returns the facets' values with their counts.
// An id finds one voice, an earlier gallery's too.
func (b *apiBackend) SearchGallery(ctx context.Context, q api.GalleryQuery) (api.GalleryPage, error) {
	voices, _ := b.voices(ctx)
	saved := savedArchetypes(voices)
	loc := i18n.Locale()
	g := gallery.Default()
	if id := strings.TrimSpace(q.ID); id != "" {
		a, ok := g.ByID(id)
		if !ok {
			return api.GalleryPage{Items: []api.GalleryVoiceJSON{}, Next: -1}, nil
		}
		return api.GalleryPage{Total: 1, Next: -1, Items: []api.GalleryVoiceJSON{galleryVoiceJSON(a, loc, saved)}}, nil
	}
	if c := q.Category; c != "" && gallery.CategoryID(c) == "" {
		var ids []string
		for _, x := range gallery.Categories() {
			ids = append(ids, x.ID)
		}
		return api.GalleryPage{}, errBadRequest("category", "category is one of %s.", strings.Join(ids, ", "))
	}
	gq := galleryFilterQuery(q.Query, q.Category, map[gallery.Facet]string{gallery.FacetGender: q.Gender, gallery.FacetAge: q.Age,
		gallery.FacetLanguage: q.Language})
	gq.Limit, gq.Offset = q.Limit, q.Offset
	page := g.Search(gq)
	out := api.GalleryPage{Total: page.Total, Offset: page.Offset, Next: page.Next, Items: []api.GalleryVoiceJSON{},
		Facets: map[string][]api.FacetValueJSON{}}
	for _, a := range page.Items {
		out.Items = append(out.Items, galleryVoiceJSON(a, loc, saved))
	}
	for f, vals := range page.Facets {
		for _, v := range vals {
			out.Facets[string(f)] = append(out.Facets[string(f)], api.FacetValueJSON{Value: v.Value, Label: v.Label.In(loc), Count: v.Count})
		}
	}
	return out, nil
}

// UseGalleryVoice saves a gallery voice as "Use voice" does (once).
func (b *apiBackend) UseGalleryVoice(ctx context.Context, id string) (api.GalleryUse, error) {
	arch, ok := gallery.Default().ByID(id)
	if !ok {
		return api.GalleryUse{}, errMissing("gallery voice", id)
	}
	voices, err := b.voices(ctx)
	if err != nil {
		return api.GalleryUse{}, err
	}
	// The model it is for: a built-in speaker's own, else the design
	// model (that made a curated voice's reference).
	mid := archetypeDesignModel(arch)
	if arch.Kind == gallery.KindBuiltin {
		mid = arch.Model
	}
	e, _ := catalog.Builtin().ByID(mid)
	use := api.GalleryUse{Model: e.ID}
	var v core.Voice
	if vid := savedArchetypes(voices)[arch.ID]; vid != "" {
		for _, x := range voices {
			if x.ID == vid {
				v = x
			}
		}
	} else {
		if v, err = saveArchetype(ctx, b.lib.svc.Voices, arch, i18n.Locale(), b.lib.store.Dir()); err != nil {
			return api.GalleryUse{}, err
		}
		use.Created = true
	}
	use.Voice = voiceJSONOf(v)
	for _, m := range catalog.Builtin().ForTask(catalog.TaskTTS) {
		if ok, _ := voiceUsable(&v, m); ok && !m.Hidden {
			use.Models = append(use.Models, m.ID)
		}
	}
	use.Downloaded, _ = b.onDisk(e)
	use.Note = "Speak with it: generate_speech with voice \"" + v.Name + "\" and a model from models. Use it in stories, audiobooks and dubs by its name."
	if v.RefAudio != "" {
		use.Note += " It has its reference already: models that clone (such as Qwen3-TTS 1.7B Base) speak it at once."
	}
	if !use.Downloaded && e.ID != "" {
		use.Note += " " + e.Label + " is not downloaded: ask the user to download it in AudioSmith → Models."
	}
	return use, nil
}

// voiceJSONOf is a saved voice's JSON (the API's VoiceJSON).
func voiceJSONOf(v core.Voice) api.VoiceJSON {
	return api.VoiceJSON{ID: v.ID, Object: "voice", Name: v.Name, Kind: string(v.Kind), Model: v.Model, Speaker: v.Speaker,
		Language: v.Language, Style: v.Style, Design: v.Design, Seed: v.Seed, RefText: v.RefText, RefSeconds: v.RefSeconds,
		Locked: v.Locked, Verified: v.Verified(), Tags: v.Tags, Origin: v.Origin, HasAudio: v.Sample != "" || v.RefAudio != "",
		CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
}

// --- take exports -------------------------------------------------------------------

// ExportTake exports a take with the Export audio dialog's form: the
// kind's first-use settings, changed by the request.
func (b *apiBackend) ExportTake(ctx context.Context, req api.TakeExport) (api.ExportJSON, error) {
	t, err := b.lib.svc.Takes.Get(ctx, req.ID)
	if err != nil {
		return api.ExportJSON{}, notFound(err, "take", req.ID)
	}
	src := b.lib.svc.Takes.AudioPath(t)
	info, err := services.AnalyzeAudio(src)
	if err != nil {
		return api.ExportJSON{}, errNotReady("The take's audio cannot be read: %v", err)
	}
	k := exportKindOf(&t)
	f := &exportForm{open: true, kind: k, take: t, src: src, info: &info,
		availOK: map[string]bool{}, availWhy: map[string]string{}, extras: map[string]*bool{}}
	ex := b.lib.encodeExecutor()
	exportAvailable(f, ex != nil && b.CanEncode(), b.tool())
	f.apply(k.defaults, k.defaults)
	f.name = strings.TrimSuffix(exportName(&t), ".wav")
	f.title, f.artist = exportTitle(t.Text), "AudioSmith"
	if req.Format != "" {
		id := strings.ToLower(req.Format)
		if !f.availOK[id] {
			return api.ExportJSON{}, apiErr(http.StatusBadRequest, api.CodeUnsupportedFormat, "format", "%s", englishWhy(id))
		}
		f.format = formatIndex(id)
	}
	ef := f.fmt_()
	if req.Bits != 0 {
		i := slices.Index(ef.bits, req.Bits)
		if i < 0 {
			return api.ExportJSON{}, errBadRequest("bits", "%s takes bits %v.", ef.label, ef.bits)
		}
		f.bits[f.format] = i
	}
	if req.Bitrate != 0 {
		i := slices.Index(ef.bitrates, req.Bitrate)
		if i < 0 {
			return api.ExportJSON{}, errBadRequest("bitrate_kbps", "%s takes bitrate_kbps %v.", ef.label, ef.bitrates)
		}
		f.bitrate[f.format] = i
	}
	if req.SampleRate != 0 {
		if !ef.rates {
			return api.ExportJSON{}, errBadRequest("sample_rate", "%s is always 48000 Hz.", ef.label)
		}
		opts := f.rateOptions(ef)
		want := req.SampleRate
		if want == f.srcRate() {
			want = 0
		}
		i := slices.Index(opts, want)
		if i < 0 {
			return api.ExportJSON{}, errBadRequest("sample_rate", "%s takes sample_rate 44100 or 48000 (or 0 for the take's %d).", ef.label, f.srcRate())
		}
		f.rate[f.format] = i
	}
	if req.Channels != 0 {
		f.channels = 0
		if req.Channels != f.srcChannels() {
			f.channels = 1
		}
	}
	switch req.Loudness {
	case "":
	case "off":
		f.loud = loudnessLabel("")
	default:
		f.loud = loudnessLabel(req.Loudness)
	}
	f.trim = req.Trim
	if s := strings.TrimSpace(req.Title); s != "" {
		f.title = s
	}
	if s := strings.TrimSpace(req.Artist); s != "" {
		f.artist = s
	}
	o := f.options()
	dst := filepath.Join(req.Dir, api.SafeName(req.Filename, strings.TrimSuffix(f.fileName(), "."+ef.ext), ef.ext))
	res, err := services.ExportAudio(ctx, src, dst, o, exportEncoderOf(ex, b.tool()))
	if err != nil {
		return api.ExportJSON{}, err
	}
	extras := map[string]bool{"script": req.Script, "subtitles": req.Subtitles}
	if err := writeExtras(t, dst, res, extras); err != nil {
		return api.ExportJSON{}, err
	}
	title := strings.Join(strings.Fields(t.Text), " ")
	if r := []rune(title); len(r) > 80 {
		title = string(r[:80]) + "…"
	}
	b.lib.svc.Library.RecordExport(ctx, core.Export{Kind: "take", SourceID: t.ID, Title: title, Path: dst, Format: ef.ext})
	out := api.ExportJSON{Files: []string{dst}, Format: ef.id, Bytes: res.Bytes, Seconds: round2(res.Seconds), Loudness: round1(res.Loudness)}
	base := strings.TrimSuffix(dst, filepath.Ext(dst))
	for _, x := range []string{".txt", ".srt"} {
		if (x == ".txt" && req.Script) || (x == ".srt" && req.Subtitles) {
			if _, err := os.Stat(base + x); err == nil {
				out.Files = append(out.Files, base+x)
			}
		}
	}
	return out, nil
}

// englishWhy says why a format cannot be written now.
func englishWhy(format string) string {
	switch format {
	case services.FormatM4A:
		return "M4A needs the speech engine's encoder, which is not running."
	case services.FormatWAV:
		return ""
	}
	return strings.ToUpper(format) + " needs ffmpeg: the user can set it up in AudioSmith → Settings → Audio tools."
}

// --- workflows ----------------------------------------------------------------------

// Workflows lists the saved workflows with their inputs.
func (b *apiBackend) Workflows(ctx context.Context) ([]api.WorkflowJSON, error) {
	if b.wf == nil {
		return nil, errNoModule("Workflows")
	}
	recs, err := b.wf.List(ctx)
	if err != nil {
		return nil, err
	}
	out := []api.WorkflowJSON{}
	for _, r := range recs {
		w, _, err := b.wf.Get(ctx, r.ID)
		if err != nil {
			continue
		}
		j := api.WorkflowJSON{ID: w.ID, Name: w.Name, Steps: services.WorkflowSummary(w), JobID: services.WorkflowOwner + w.ID, Updated: w.UpdatedAt}
		if in := w.InputNode(); in != nil {
			if in.Kind == workflow.KindStart {
				j.Input = "scripts"
				for _, s := range in.P.Scripts {
					t := s.Text
					if r := []rune(t); len(r) > 300 {
						t = string(r[:300]) + "…"
					}
					j.Scripts = append(j.Scripts, t)
				}
			} else {
				j.Input = "files"
				for _, c := range in.P.Clips {
					j.Files = append(j.Files, c.Path)
				}
			}
		}
		for _, n := range w.Nodes {
			if n.Kind == workflow.KindExport && n.P.Folder != "" {
				j.Exports = append(j.Exports, n.P.Folder)
			}
		}
		if r.State != "" {
			j.LastRun = &api.WorkflowRunOut{State: r.State, Items: r.Items, Done: r.Done}
		}
		for _, d := range b.wf.Validate(w).Diags {
			if d.Severity == workflow.Error {
				j.Problem = append(j.Problem, wfDiagText(w, d))
			}
		}
		out = append(out, j)
	}
	return out, nil
}

// wfDiagText names a workflow diagnostic (its code and step).
func wfDiagText(w *workflow.Workflow, d workflow.Diagnostic) string {
	s := d.Code
	if n := w.Node(d.Node); n != nil {
		s += " (" + n.StepName(string(n.Kind)) + ")"
	}
	return s
}

// RunWorkflow runs a saved workflow, with one-off inputs if given (the
// saved workflow, its steps and inputs, is not changed).
func (b *apiBackend) RunWorkflow(ctx context.Context, req api.WorkflowRun) (api.JobJSON, error) {
	if b.wf == nil {
		return api.JobJSON{}, errNoModule("Workflows")
	}
	w, _, err := b.wf.Get(ctx, req.ID)
	if err != nil {
		return api.JobJSON{}, notFound(err, "workflow", req.ID)
	}
	var inputs *services.WorkflowInputs
	if len(req.Scripts) > 0 || len(req.Files) > 0 {
		in := w.InputNode()
		switch {
		case in == nil:
			return api.JobJSON{}, errBadRequest("workflow_id", "The workflow has no input step.")
		case len(req.Scripts) > 0 && in.Kind != workflow.KindStart:
			return api.JobJSON{}, errBadRequest("scripts", "This workflow starts with audio files: give files.")
		case len(req.Files) > 0 && in.Kind != workflow.KindSource:
			return api.JobJSON{}, errBadRequest("files", "This workflow starts with scripts: give scripts.")
		case len(req.Scripts) > workflow.MaxScripts || len(req.Files) > workflow.MaxClips:
			return api.JobJSON{}, errBadRequest("scripts", "At most %d inputs.", workflow.MaxScripts)
		}
		// One-off inputs of this run: the saved workflow keeps its own.
		inputs = &services.WorkflowInputs{}
		for i, s := range req.Scripts {
			if len([]rune(s)) > workflow.MaxScriptChars {
				return api.JobJSON{}, errBadRequest("scripts", "Script %d is longer than %d characters.", i+1, workflow.MaxScriptChars)
			}
			inputs.Scripts = append(inputs.Scripts, workflow.Script{Text: s})
		}
		for _, p := range req.Files {
			fi, err := os.Stat(p)
			if err != nil {
				return api.JobJSON{}, errBadRequest("files", "Cannot read %s: %v", p, err)
			}
			if fi.Size() > workflow.MaxClipBytes {
				return api.JobJSON{}, errBadRequest("files", "%s is larger than 64 MB.", filepath.Base(p))
			}
			inputs.Clips = append(inputs.Clips, workflow.Clip{Name: filepath.Base(p), Path: p, Size: fi.Size(), Mod: fi.ModTime()})
		}
	}
	// A run with nothing to do (no input changed) ends at once: its
	// job is done, with the results it kept.
	if _, err := b.wf.Run(ctx, req.ID, services.RunOptions{Again: req.Again, Priority: jobs.PriorityNormal, Inputs: inputs}); err != nil {
		var inv *services.WorkflowInvalidError
		switch {
		case errors.As(err, &inv):
			var probs []string
			for _, d := range inv.Report.Diags {
				if d.Severity == workflow.Error {
					probs = append(probs, wfDiagText(w, d))
				}
			}
			return api.JobJSON{}, &api.Error{Status: http.StatusBadRequest, Code: services.WorkflowCodeInvalid, Param: "workflow_id",
				Message: "The workflow has errors: fix them in AudioSmith → Workflows (" + strings.Join(probs, "; ") + ").", Data: map[string]any{"problems": probs}}
		case errors.Is(err, core.ErrBusy):
			return api.JobJSON{}, errBusy("The workflow is running: follow get_job %s, or cancel it.", services.WorkflowOwner+req.ID)
		}
		return api.JobJSON{}, err
	}
	return b.workflowTask(ctx, req.ID)
}

// workflowTask is a workflow's job: "workflow:<id>", its last run.
func (b *apiBackend) workflowTask(ctx context.Context, id string) (api.JobJSON, error) {
	if b.wf == nil {
		return api.JobJSON{}, errNoModule("Workflows")
	}
	w, run, err := b.wf.Get(ctx, id)
	if err != nil {
		return api.JobJSON{}, notFound(err, "workflow", id)
	}
	if run == nil {
		return api.JobJSON{}, errNotReady("The workflow has not run yet: run_workflow.")
	}
	parts, cur := b.activeJobs(ctx, services.WorkflowOwner+id)
	state := jobs.StateRunning
	var jerr *jobs.Error
	switch run.State {
	case workflow.RunDone:
		state = jobs.StateSucceeded
	case workflow.RunFailed:
		state = jobs.StateFailed
		jerr = &jobs.Error{Code: firstOf(run.Code, "workflow.failed"), Message: firstOf(run.Err, "Some items failed.")}
	case workflow.RunCancelled:
		state, jerr = jobs.StateCancelled, &jobs.Error{Code: jobs.CodeCancelled, Message: "The run was cancelled: run_workflow goes on where it stopped."}
	}
	if state == jobs.StateRunning && len(parts) == 0 {
		state = jobs.StateQueued
	}
	done, failed, total := run.Counts()
	progress := 1.0
	if state == jobs.StateRunning || state == jobs.StateQueued {
		progress = float64(done+failed) / float64(max(total, 1))
	}
	stage := ""
	if cur != nil {
		stage = cur.Title
	}
	type stepOut struct {
		Step   string `json:"step"`
		Status string `json:"status"`
		File   string `json:"file,omitempty"`
		Text   string `json:"text,omitempty"`
		Error  string `json:"error,omitempty"`
	}
	type itemOut struct {
		Name  string    `json:"name"`
		State string    `json:"state"`
		Steps []stepOut `json:"steps"`
	}
	var items []itemOut
	var files []string
	for _, it := range run.Items {
		io := itemOut{Name: it.Name, State: string(it.State())}
		for _, n := range w.Nodes {
			st := it.Steps[n.ID]
			if st == nil || n.Kind.IsInput() {
				continue
			}
			so := stepOut{Step: n.StepName(string(n.Kind)), Status: string(st.Status)}
			if st.Err != "" {
				so.Error = firstOf(st.Code, "failed") + ": " + st.Err
			}
			if st.Out != nil {
				so.Text = st.Out.Text
				if st.Out.File != "" {
					so.File = b.wf.Path(id, st.Out.File)
				}
			}
			io.Steps = append(io.Steps, so)
		}
		items = append(items, io)
	}
	for _, o := range services.Outputs(w, run) {
		if o.Value.File != "" {
			files = append(files, b.wf.Path(id, o.Value.File))
		}
	}
	res := map[string]any{"workflow_id": id, "run_id": run.ID, "state": run.State, "items": items, "done": done, "failed": failed, "total": total}
	return api.GroupJob(services.WorkflowOwner+id, "workflow.run", w.Name, state, stage, progress, "", jerr, files, res, parts), nil
}
