package services

// Rendering a longform chapter: each chunk from the cache or the engine,
// joined with the production's gaps (chapterRenderer.once, chapterRun),
// then brought to the book's loudness target with per-character levelling
// and written with its sidecar (longform_chapter_finish.go); split from
// longform_render.go.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

// once plans and renders the chapter with info's chunk limits.
func (cr chapterRenderer) once(ctx context.Context, job jobs.Job, p *ChapterParams, info ModelInfo, modelPath string, report func(jobs.Progress)) (json.RawMessage, error) {
	started := time.Now()
	dialoguePath, unavailable, err := cr.dialogueModel(ctx, job, p)
	if err != nil {
		return nil, err
	}
	steps, key, warnings, err := cr.program(ctx, p, info, modelPath)
	if err != nil {
		return nil, err
	}
	if unavailable != nil {
		warnings = append(warnings, *unavailable)
	}
	// A resumed render keeps a chapter made from the same program.
	if old, err := ReadChapter(p.Output); err == nil && old.Key == key {
		if fi, err := os.Stat(p.Output); err == nil && fi.Size() > 44 {
			old.Reused, old.Seconds = true, time.Since(started).Seconds()
			report(jobs.Progress{Stage: "cached", Fraction: 1, AudioSeconds: old.AudioSeconds})
			return json.Marshal(old)
		}
	}
	for _, d := range []string{cr.r.longformChunkDir(), cr.r.tmpDir(), filepath.Dir(p.Output)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, &jobs.Error{Code: CodeRenderFailed, Message: err.Error()}
		}
	}
	cx := cr.newRun(ctx, job, p, report, steps, key, warnings)
	cx.dialoguePath = dialoguePath
	defer cx.st.cleanup()
	if err := cx.st.silence(chapterHead); err != nil {
		return nil, &jobs.Error{Code: CodeRenderFailed, Message: err.Error()}
	}
	for _, s := range steps {
		if err := cx.run(s); err != nil {
			return nil, err
		}
	}
	return cx.finish(started)
}

// dialogueModel is the local snapshot of the production's dialogue model
// ("" without one). A book whose chapter has no dialogue passages, or
// whose dialogue model is not ready, reads its lines one by one; the
// latter with the notice it returns.
func (cr chapterRenderer) dialogueModel(ctx context.Context, job jobs.Job, p *ChapterParams) (string, *core.TakeNotice, error) {
	r := cr.r
	if p.book() && !hasBlocks(p.Lines) {
		p.Production.Dialogue = false // a book's chapter without dialogue passages
	}
	if !p.Production.Dialogue || p.Production.DialogueModel == "" || r.engine == nil {
		return "", nil, nil
	}
	dialoguePath, err := r.modelReady(ctx, p.Production.DialogueModel)
	if err == nil {
		return dialoguePath, nil, nil
	}
	if !p.book() {
		return "", nil, err
	}
	// A book's dialogue blocks are read line by line instead.
	r.logf("chapter %s: no dialogue model (%v): the dialogue blocks are read line by line", job.ID, err)
	p.Production.Dialogue = false
	return "", &core.TakeNotice{Code: NoticeDialogueUnavailable, Severity: "warning", Params: map[string]string{"model": p.Production.DialogueModel}}, nil
}

// chapterRun is one render of a chapter's steps: what they share.
type chapterRun struct {
	cr           chapterRenderer
	ctx          context.Context
	job          jobs.Job
	p            *ChapterParams
	st           *renderState
	res          ChapterResult
	warnings     []core.TakeNotice
	dialoguePath string
	i, n         int          // the chunk being made (1-based), of how many
	made         map[int]bool // lines the dialogue model made
}

func (cr chapterRenderer) newRun(ctx context.Context, job jobs.Job, p *ChapterParams, report func(jobs.Progress), steps []chapterStep, key string, warnings []core.TakeNotice) *chapterRun {
	gaps := gapsOf(p.Production)
	st := &renderState{r: cr.r, job: job, p: &RenderParams{Output: p.Output}, report: report, gaps: &gaps, keepEdge: p.Production.KeepEdges}
	n := 0
	for _, s := range steps {
		if s.chunk != nil {
			st.total += s.chunk.chars
			n++
		}
	}
	res := ChapterResult{Output: p.Output, Key: key, Chunks: n, Warnings: warnings}
	res.Lines = make([]LinePlace, len(p.Lines))
	for i, l := range p.Lines {
		res.Lines[i] = LinePlace{ID: l.ID, Start: -1, Spans: make([]SpanPlace, len(l.Spans))}
		for j, sp := range l.Spans {
			res.Lines[i].Spans[j] = SpanPlace{Character: sp.Character, Text: sp.Text, Start: -1}
		}
	}
	return &chapterRun{cr: cr, ctx: ctx, job: job, p: p, st: st, res: res, warnings: warnings, n: n, made: map[int]bool{}}
}

// place records that step s's span (and line) sounds from start to end.
func (cx *chapterRun) place(s chapterStep, start, end float64) {
	lp := &cx.res.Lines[s.line]
	sp := &lp.Spans[s.span]
	if sp.Start < 0 {
		sp.Start = start
	}
	sp.End = end
	if lp.Start < 0 {
		lp.Start = start
	}
	lp.End = end
}

// run makes one step: a pause, or a chunk from the cache, the engine, a
// whole-dialogue segment or a character's track.
func (cx *chapterRun) run(s chapterStep) error {
	ctx, st := cx.ctx, cx.st
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.chunk == nil {
		if err := st.silence(s.silence); err != nil {
			return &jobs.Error{Code: CodeRenderFailed, Message: err.Error()}
		}
		return nil
	}
	cx.i++
	c := *s.chunk
	if s.dialogue != nil {
		if done, err := cx.dialogueChunk(s, &c); done {
			return err
		}
	} else if s.track != nil {
		if done, err := cx.trackChunk(s, &c); done {
			return err
		}
	} else {
		_, hit, err := st.synthesize(ctx, cx.i, cx.n, c)
		if err != nil {
			return err
		}
		if hit {
			cx.res.Cached++
		}
	}
	buf, trimmed, lead, err := st.loadChunk(c.path)
	if err != nil {
		os.Remove(c.path) // unreadable: made again next time
		return &jobs.Error{Code: CodeRenderFailed, Message: fmt.Sprintf("chunk %d of %d: %v", cx.i, cx.n, err), Retryable: true}
	}
	if s.turn != nil {
		return cx.turnChunk(s, c, buf, trimmed, lead)
	}
	start, end, err := st.appendLoaded(buf, trimmed, s.brk)
	if err != nil {
		return &jobs.Error{Code: CodeRenderFailed, Message: fmt.Sprintf("chunk %d of %d: %v", cx.i, cx.n, err), Retryable: true}
	}
	cx.place(s, start, end)
	cx.checkDrops(s, c, start, end, trimmed.Seconds())
	return nil
}

// fallback makes steps instead of the one that failed, counting their
// chunks in.
func (cx *chapterRun) fallback(steps []chapterStep) error {
	for _, f := range steps {
		if f.chunk != nil {
			cx.n++
			cx.st.total += f.chunk.chars
		}
	}
	for _, f := range steps {
		if err := cx.run(f); err != nil {
			return err
		}
	}
	return nil
}

// dialogueChunk points c at its line of a whole-dialogue segment, made
// on first use; done when the step ended there (an error, or the line
// made by its fallback steps).
func (cx *chapterRun) dialogueChunk(s chapterStep, c *renderChunk) (done bool, err error) {
	ctx, res := cx.ctx, &cx.res
	seg := s.dialogue.seg
	if !seg.made {
		seg.made = true
		seg.err = cx.cr.dialogueRun(ctx, cx.st, cx.i, cx.n, seg, cx.p.Production.DialogueModel, cx.dialoguePath)
		var je *jobs.Error
		switch {
		case ctx.Err() != nil:
			return true, ctx.Err()
		case errors.As(seg.err, &je) && (je.Code == proto.CodeModelNotDownloaded || je.Code == proto.CodeLicenceRequired):
			return true, seg.err
		case seg.err != nil:
			cx.cr.r.logf("chapter %s: lines %v: no dialogue (%v); making them on the story's model", cx.job.ID, seg.lines, seg.err)
			res.DialogueFailed += len(seg.lines)
		default:
			res.Dialogues++
			res.DialogueLines += len(seg.lines) - len(seg.out.failed)
			res.DialogueFailed += len(seg.out.failed)
			res.DialogueRedone += seg.out.redone
		}
	}
	if _, failed := seg.out.failed[s.dialogue.k]; seg.err != nil || failed {
		return true, cx.fallback(s.dialogue.fallback)
	}
	c.path, c.seg.Text = seg.job.paths[s.dialogue.k], seg.job.texts[s.dialogue.k]
	cx.made[s.line] = true
	return false, nil
}

// trackChunk points c at its line of a character's track, cut on first
// use; done as dialogueChunk.
func (cx *chapterRun) trackChunk(s chapterStep, c *renderChunk) (done bool, err error) {
	ctx := cx.ctx
	seg := s.track.seg
	if !seg.made {
		seg.made = true
		seg.err = cx.cr.trackCut(ctx, cx.st, cx.i, cx.n, seg)
		if seg.err != nil && ctx.Err() == nil {
			cx.cr.r.logf("chapter %s: lines %v: no track (%v); making them one by one", cx.job.ID, seg.lines, seg.err)
			cx.res.TracksFailed++
		} else if seg.err == nil {
			cx.res.Tracks++
			cx.res.TrackLines += len(seg.lines)
		}
	}
	if seg.err != nil {
		if ctx.Err() != nil {
			return true, ctx.Err()
		}
		return true, cx.fallback(s.track.fallback)
	}
	c.path, c.seg.Text = seg.paths[s.track.k], seg.texts[s.track.k]
	return false, nil
}

// turnChunk splits a merged turn's chunk into its lines and places them;
// a turn that cannot be split is made line by line.
func (cx *chapterRun) turnChunk(s chapterStep, c renderChunk, buf, trimmed dsp.Buffer, lead int) error {
	st := cx.st
	cuts, ok := splitMerged(trimmed, s.turn.weights)
	if !ok {
		// Lines the turn cannot be split into are made one by one.
		cx.cr.r.logf("chapter %s: lines %v could not be split from their merged turn; making them one by one", cx.job.ID, s.turn.lines)
		cx.res.TurnsUnsplit++
		return cx.fallback(s.turn.fallback)
	}
	cx.res.Turns++
	if turns, ok := GapsOf(cx.p.Production.Pacing); ok && !st.keepEdge {
		trimmed, cuts = tightenTurn(trimmed, cuts, float64(turns.Same)/1000+turnPauseSlack)
	}
	start, end, err := st.appendLoaded(buf, trimmed, s.brk)
	if err != nil {
		return &jobs.Error{Code: CodeRenderFailed, Message: err.Error()}
	}
	off := 0
	if st.keepEdge {
		off = lead
	}
	at := func(frame int) float64 { return start + float64(frame+off)/float64(st.rate) }
	for k, li := range s.turn.lines {
		a, b := 0, trimmed.Frames()
		if k > 0 {
			a = cuts[k-1].end
		}
		if k < len(cuts) {
			b = cuts[k].start
		}
		ls, le := at(a), at(b)
		if k == 0 {
			ls = start
		}
		if k == len(s.turn.lines)-1 {
			le = end
		}
		cx.place(chapterStep{line: li}, ls, le)
	}
	cx.checkDrops(s, c, start, end, trimmed.Seconds())
	return nil
}

// checkDrops warns about a chunk much shorter than its text (render_finish.go).
func (cx *chapterRun) checkDrops(s chapterStep, c renderChunk, start, end, voiced float64) {
	for _, w := range dropCheck(cx.i, c, cx.p.Lines[s.line].Speed, start, end, voiced) {
		w.Params["line"] = strconv.Itoa(s.line + 1)
		cx.warnings = append(cx.warnings, w)
	}
}
