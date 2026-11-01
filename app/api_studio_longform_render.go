package main

// Rendering and exporting Stories and Audiobook projects for agents
// (thread #35): a render is a group job of the longform service, owned by
// "longform:<render>"; an export saves a finished render as Save does
// (longform_view_outputs.go).

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/ffmpeg"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
)

// RenderLongform compiles a project and starts its render (or of some
// chapters: a book of those only).
func (b *apiBackend) RenderLongform(ctx context.Context, req api.LongformRender) (api.JobJSON, error) {
	p, d, err := b.loadLongform(ctx, req.ID)
	if err != nil {
		return api.JobJSON{}, err
	}
	voices, err := b.voices(ctx)
	if err != nil {
		return api.JobJSON{}, err
	}
	before := maps.Clone(d.VoiceCast)
	given := b.castBookUncast(p, &d, voices) // api_studio_longform_dialogue.go
	// record writes what this render adds to the project — the voices
	// given, its id — into the project as it is now (#101 M7: an edit
	// saved meanwhile stays).
	record := func(render string) error {
		_, _, err := b.lib.svc.Library.UpdateDoc(ctx, p.ID, func(_ *core.Project, cur *services.LongformDoc) error {
			for name, v := range d.VoiceCast {
				if _, had := before[name]; had {
					continue
				}
				if _, set := cur.VoiceCast[name]; !set {
					if cur.VoiceCast == nil {
						cur.VoiceCast = map[string]string{}
					}
					cur.VoiceCast[name] = v
				}
			}
			if render != "" {
				cur.Render = render
			}
			return nil
		})
		return err
	}
	spec, probs := b.compileFor(&d, voices)
	var blocking []api.ProblemJSON
	for _, pr := range probs {
		if pr.Blocking {
			blocking = append(blocking, api.ProblemJSON{Code: pr.Code, Message: problemMessage(pr), Blocking: true})
		}
	}
	if len(blocking) > 0 {
		return api.JobJSON{}, &api.Error{Status: http.StatusBadRequest, Code: "not_renderable", Param: "project_id",
			Message: "The project cannot render yet: " + blocking[0].Message, Data: map[string]any{"problems": blocking}}
	}
	if err := b.modelReady(ctx, entryFor(d.Model)); err != nil {
		return api.JobJSON{}, err
	}
	if p.Kind == core.ProjectAudiobook && specHasBlocks(&spec) {
		e, why := b.bookDialogueModel(&d)
		spec.Production.Dialogue = why == ""
		if why == "" {
			spec.Production.DialogueModel = e.Repo
		}
	}
	if blk := req.DialogueBlock; blk != nil {
		if p.Kind != core.ProjectAudiobook || len(req.Chapters) > 0 {
			return api.JobJSON{}, errBadRequest("dialogue_block", "dialogue_block previews a block of an audiobook, without chapters.")
		}
		ch, ok := blockSpec(&spec, blockWhat(services.DialogueBlockInfo{Chapter: blk.Chapter - 1, Index: blk.Index}))
		if !ok {
			return api.JobJSON{}, errBadRequest("dialogue_block", "Chapter %d has no dialogue block %d (get_longform lists them).", blk.Chapter, blk.Index)
		}
		spec.Chapters, spec.Audition = []services.ChapterSpec{ch}, true
	}
	if len(req.Chapters) > 0 {
		var chs []services.ChapterSpec
		for _, n := range req.Chapters {
			if n < 1 || n > len(spec.Chapters) {
				return api.JobJSON{}, errBadRequest("chapters", "Chapter %d does not exist: the project has %d.", n, len(spec.Chapters))
			}
			chs = append(chs, spec.Chapters[n-1])
		}
		spec.Chapters = chs
	}
	spec.Project = p.ID
	if spec.Title == "" {
		spec.Title = p.Title
	}
	r, err := b.lib.svc.Longform.Start(ctx, spec)
	if err != nil {
		if errors.Is(err, core.ErrBusy) {
			return api.JobJSON{}, errBusy("Another book or story is rendering: wait for it (list it with get_job) or cancel it first.")
		}
		return api.JobJSON{}, err
	}
	if spec.Audition {
		if len(given) > 0 {
			if err := record(""); err != nil {
				return api.JobJSON{}, err
			}
		}
		j, err := b.renderTask(ctx, r.ID)
		return withGiven(j, given), err
	}
	// The project remembers its last render (and the voices given), as the page does.
	if err := record(r.ID); err != nil {
		return api.JobJSON{}, err
	}
	j, err := b.renderTask(ctx, r.ID)
	return withGiven(j, given), err
}

// renderTask is a render's job: "longform:<render id>".
func (b *apiBackend) renderTask(ctx context.Context, id string) (api.JobJSON, error) {
	r, body, err := b.lib.svc.Longform.Get(ctx, id)
	if err != nil {
		return api.JobJSON{}, notFound(err, "render", id)
	}
	state := jobs.StateRunning
	var jerr *jobs.Error
	switch r.State {
	case core.RenderDone:
		state = jobs.StateSucceeded
	case core.RenderFailed:
		state, jerr = jobs.StateFailed, body.Error
		if jerr == nil {
			for i, c := range body.Chapters {
				if c.Error != nil {
					e := *c.Error
					e.Message = services.Untitled(i+1) + ": " + e.Message
					jerr = &e
					break
				}
			}
		}
		if jerr == nil {
			jerr = &jobs.Error{Code: services.CodeRenderFailed, Message: "Some chapters did not render."}
		}
	case core.RenderStopped:
		state, jerr = jobs.StateCancelled, &jobs.Error{Code: jobs.CodeCancelled, Message: "The render was stopped; the chapters made are kept."}
	}
	n := len(body.Chapters)
	done, frac, stage := 0.0, 0.0, ""
	for i, c := range body.Chapters {
		switch c.State {
		case services.ChapterDone, services.ChapterCached:
			done++
		case services.ChapterRendering:
			frac += max(c.Fraction, 0)
			stage = "chapter " + itoa(i+1) + "/" + itoa(n)
			if c.Title != "" {
				stage += ": " + c.Title
			}
		}
	}
	progress := -1.0
	if n > 0 {
		progress = (done + frac) / float64(n) * 0.95
	}
	if body.Assembling {
		stage, progress = "assembling the book", 0.95+0.05*max(body.Assembled, 0)
	}
	if state == jobs.StateSucceeded {
		progress, stage = 1, ""
	}
	msg := ""
	if eta, ok := services.ETA(&body, time.Now()); ok && state == jobs.StateRunning {
		msg = "about " + eta.Round(time.Second).String() + " left"
	}
	var parts []string
	for _, c := range body.Chapters {
		if c.Job != "" {
			parts = append(parts, c.Job)
		}
	}
	if body.AssembleJob != "" {
		parts = append(parts, body.AssembleJob)
	}
	rj := renderJSON(r, &body, 0)
	var files []string
	if rj.M4B != "" {
		files = append(files, rj.M4B)
	}
	if rj.Book != "" {
		files = append(files, rj.Book)
	}
	for _, c := range rj.Chapter {
		if c.File != "" && state == jobs.StateSucceeded {
			files = append(files, c.File)
		}
	}
	res := map[string]any{"render": rj, "project_id": r.ProjectID}
	return api.GroupJob(services.OwnerLongform+id, "longform.render", r.Title, state, stage, progress, msg, jerr, files, res, parts), nil
}

// ExportLongform saves a finished render's files (Save in the page).
func (b *apiBackend) ExportLongform(ctx context.Context, req api.LongformExport) (api.ExportJSON, error) {
	rid := strings.TrimPrefix(strings.TrimSpace(req.Render), services.OwnerLongform)
	if rid == "" {
		_, d, err := b.loadLongform(ctx, req.ID)
		if err != nil {
			return api.ExportJSON{}, err
		}
		if d.Render == "" {
			return api.ExportJSON{}, errNotReady("The project has not been rendered: render_longform first.")
		}
		rid = d.Render
	}
	rec, body, err := b.lib.svc.Longform.Get(ctx, rid)
	if err != nil {
		return api.ExportJSON{}, notFound(err, "render", rid)
	}
	if req.ID != "" && rec.ProjectID != req.ID {
		return api.ExportJSON{}, errBadRequest("render_id", "The render %s is not of the project %s.", rid, req.ID)
	}
	out := body.Output
	if rec.State != core.RenderDone || out == nil {
		return api.ExportJSON{}, errNotReady("The render is %s: wait for its job (get_job longform:%s) to succeed, then export.", rec.State, rid)
	}
	lib := b.lib.svc.Library
	title := services.SafeFileName(rec.Title, kindName(rec.Kind))
	format := firstOf(strings.ToLower(req.Format), services.FormatWAV)
	res := api.ExportJSON{Format: format}
	file := func(ext string) string { return filepath.Join(req.Dir, api.SafeName(req.Filename, title, ext)) }
	folder := func(suffix string) (string, error) {
		dir := filepath.Join(req.Dir, api.SafeName(req.Filename, title+suffix, ""))
		return dir, os.MkdirAll(dir, 0o755)
	}
	switch format {
	case services.FormatM4B:
		if out.Encoded == "" {
			why := ""
			if out.EncodeError != "" {
				why = " (" + out.EncodeError + ")"
			}
			return api.ExportJSON{}, apiErr(http.StatusBadRequest, api.CodeUnsupportedFormat, "format",
				"This render has no M4B%s: set format m4b with update_longform and render again, or export wav.", why)
		}
		dst := file("m4b")
		if err := writeRenderFile(ctx, lib, rec, "book", out.Encoded, dst, "m4b", 0); err != nil {
			return api.ExportJSON{}, err
		}
		res.Files = []string{dst}
	case services.FormatWAV:
		dst := file("wav")
		if err := writeRenderFile(ctx, lib, rec, "book", out.Book, dst, "wav", req.Bits); err != nil {
			return api.ExportJSON{}, err
		}
		res.Files = []string{dst}
	case ffmpeg.MP3, ffmpeg.Opus:
		tool := b.tool()
		if !tool.CanEncode(format) {
			return api.ExportJSON{}, apiErr(http.StatusBadRequest, api.CodeUnsupportedFormat, "format",
				"%s needs ffmpeg: the user can set it up in AudioSmith → Settings → Audio tools; without it export wav or m4b (or m4a), or ask the user before converting the WAV yourself.", strings.ToUpper(format))
		}
		o := bookEncodeOptions(rec, &body.Spec, out, format)
		o.Output = file(formatExt(format))
		if err := tool.EncodeAudio(ctx, o, nil); err != nil {
			return api.ExportJSON{}, err
		}
		lib.RecordExport(ctx, core.Export{Kind: "book", SourceID: rec.ID, ProjectID: rec.ProjectID, Title: rec.Title, Path: o.Output, Format: formatExt(format)})
		res.Files = []string{o.Output}
	case "chapters":
		dir, err := folder(" chapters")
		if err != nil {
			return api.ExportJSON{}, err
		}
		if res.Files, err = writeRenderChapters(ctx, lib, rec, slices.Clone(out.Chapters), dir); err != nil {
			return api.ExportJSON{}, err
		}
	case "cues":
		dst := file("txt")
		if err := writeRenderFile(ctx, lib, rec, "cues", out.Cues, dst, "txt", 0); err != nil {
			return api.ExportJSON{}, err
		}
		res.Files = []string{dst}
	case "stems":
		if len(out.Stems) == 0 {
			return api.ExportJSON{}, errNotReady("This render has no stems: set stems: true with update_longform and render again.")
		}
		dir, err := folder(" stems")
		if err != nil {
			return api.ExportJSON{}, err
		}
		for _, s := range out.Stems {
			name := firstOf(s.Character, "Narrator")
			dst := filepath.Join(dir, api.SafeName(name, "stem", "wav"))
			if err := writeRenderFile(ctx, lib, rec, "stem", s.File, dst, "wav", req.Bits); err != nil {
				return api.ExportJSON{}, err
			}
			res.Files = append(res.Files, dst)
		}
	}
	for _, f := range res.Files {
		if fi, err := os.Stat(f); err == nil {
			res.Bytes += fi.Size()
		}
	}
	res.Seconds = round1(out.Seconds)
	return res, nil
}
