package services

// Starting a longform render: Start (one full render at a time, beside
// auditions), the checks and summary of its spec, submitting the chapter
// and assemble jobs, Stop and Resume; split from longform.go.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/store"
	"github.com/hehehai/AudioSmith/internal/text"
)

// Start begins a render of spec. A full render is refused with
// core.ErrBusy while another runs.
func (s *Longform) Start(ctx context.Context, spec LongformSpec) (core.Render, error) {
	if s.sched == nil {
		return core.Render{}, fmt.Errorf("longform: no job scheduler: %w", core.ErrUnsupported)
	}
	if err := checkSpec(&spec); err != nil {
		return core.Render{}, err
	}
	now := s.now()
	id := core.NewID(now)
	dir := filepath.Join(s.dataDir, "renders", id)
	if spec.Audition {
		dir = filepath.Join(s.cacheDir, "auditions", id)
		s.pruneAuditions(ctx)
	}
	rec := core.Render{ID: id, ProjectID: spec.Project, Kind: spec.Kind, Title: spec.Title, State: core.RenderRunning,
		Audition: spec.Audition, CreatedAt: now, UpdatedAt: now}
	body := RenderBody{Spec: spec, Summary: summarize(&spec), Dir: dir, Runs: 1, Started: now}
	for _, ch := range spec.Chapters {
		body.Chapters = append(body.Chapters, RenderChapter{Title: ch.Title, Chars: chapterChars(ch), Lines: len(ch.Lines), State: ChapterPending})
	}
	if !spec.Audition {
		if title, busy := s.busy(ctx, ""); busy {
			return core.Render{}, fmt.Errorf("longform: %q is rendering: %w", title, core.ErrBusy)
		}
	}
	// Refused up front when the disk cannot hold the render (code
	// review 1102 M4).
	if err := checkFree("the render", filepath.Dir(dir), bookNeed(spec.Chapters, spec.Stems)); err != nil {
		return core.Render{}, err
	}
	s.mu.Lock()
	if !spec.Audition {
		for _, l := range s.live {
			if !l.rec.Audition && l.rec.State == core.RenderRunning {
				s.mu.Unlock()
				return core.Render{}, fmt.Errorf("longform: %q is rendering: %w", l.rec.Title, core.ErrBusy)
			}
		}
	}
	lr := &liveRender{rec: rec, body: body}
	s.live[id] = lr
	s.mu.Unlock()
	if err := os.MkdirAll(filepath.Join(dir, "chapters"), 0o755); err != nil {
		s.forget(id)
		return core.Render{}, err
	}
	if err := s.save(ctx, id); err != nil {
		s.forget(id)
		return core.Render{}, err
	}
	if err := s.submit(ctx, id); err != nil {
		s.mu.Lock()
		lr.rec.State, lr.body.Error = core.RenderFailed, &jobs.Error{Code: CodeRenderFailed, Message: err.Error()}
		s.mu.Unlock()
		s.save(context.WithoutCancel(ctx), id)
		return core.Render{}, err
	}
	out, _ := s.snapshot(id)
	return out, nil
}

// busy finds a full render other than except recorded as running: one
// the scheduler resumes after a restart is in the store before its first
// update brings it into memory.
func (s *Longform) busy(ctx context.Context, except string) (string, bool) {
	running, err := s.renders.List(ctx, store.RenderQuery{States: []core.RenderState{core.RenderRunning}})
	if err != nil {
		return "", false
	}
	for _, r := range running {
		if r.ID == except {
			continue
		}
		s.mu.Lock()
		lr := s.live[r.ID]
		state := r.State
		if lr != nil {
			state = lr.rec.State
		}
		s.mu.Unlock()
		if state == core.RenderRunning {
			return r.Title, true
		}
	}
	return "", false
}

// checkSpec validates a manifest.
func checkSpec(spec *LongformSpec) error {
	if spec.Model == "" {
		return fmt.Errorf("longform: no model: %w", core.ErrInvalid)
	}
	if spec.Kind != core.ProjectStory && spec.Kind != core.ProjectAudiobook {
		return fmt.Errorf("longform: kind %q: %w", spec.Kind, core.ErrInvalid)
	}
	if len(spec.Chapters) == 0 {
		return fmt.Errorf("longform: nothing to say: %w", core.ErrInvalid)
	}
	for ci, ch := range spec.Chapters {
		if len(ch.Lines) == 0 {
			return fmt.Errorf("longform: chapter %d has no lines: %w", ci+1, core.ErrInvalid)
		}
		for li, l := range ch.Lines {
			for _, sp := range l.Spans {
				if _, ok := spec.Voices[sp.Voice]; !ok {
					return fmt.Errorf("longform: chapter %d, line %d: voice %q: %w", ci+1, li+1, sp.Voice, core.ErrInvalid)
				}
			}
		}
	}
	if spec.Book.Loudness != LoudnessOff && loudnessTarget(spec.Book.Loudness) == nil {
		return fmt.Errorf("longform: loudness %q: %w", spec.Book.Loudness, core.ErrInvalid)
	}
	if spec.Title == "" {
		spec.Title = "Untitled"
	}
	return nil
}

func chapterChars(ch ChapterSpec) int {
	n := 0
	for _, l := range ch.Lines {
		for _, sp := range l.Spans {
			w, c := text.Count(sp.Text)
			n += w*6 + c // a spaced-script word is about six characters
		}
	}
	return max(n, 1)
}

// summarize is how a spec renders, for the library.
func summarize(spec *LongformSpec) RenderSummary {
	sum := RenderSummary{Model: spec.Model, Lang: spec.Lang, Format: spec.Format, Loudness: spec.Book.Loudness,
		Chapters: len(spec.Chapters), Prod: spec.Production, Stems: spec.Stems}
	used := map[string]bool{}
	for _, ch := range spec.Chapters {
		for _, l := range ch.Lines {
			sum.Lines++
			sp := l.Speed
			if sp <= 0 {
				sp = 1
			}
			if sum.MinSpeed == 0 || sp < sum.MinSpeed {
				sum.MinSpeed = sp
			}
			sum.MaxSpeed = max(sum.MaxSpeed, sp)
			for _, s := range l.Spans {
				w, c := text.Count(s.Text)
				sum.Words += w
				sum.Chars += c
				if !used[s.Voice] {
					used[s.Voice] = true
					v := spec.Voices[s.Voice]
					switch {
					case v.VoiceID != "":
						sum.Voices = append(sum.Voices, v.VoiceID)
					case v.Voice != "":
						sum.Voices = append(sum.Voices, SpeakerRef+v.Voice)
					}
				}
			}
		}
	}
	sum.Voices = slices.Compact(sum.Voices)
	return sum
}

// submit queues the render's jobs: its chapters, then the assembly.
func (s *Longform) submit(ctx context.Context, id string) error {
	s.mu.Lock()
	lr := s.live[id]
	if lr == nil {
		s.mu.Unlock()
		return fmt.Errorf("render %s: %w", id, core.ErrNotFound)
	}
	spec, dir, title, run := lr.body.Spec, lr.body.Dir, lr.rec.Title, lr.body.Runs
	s.mu.Unlock()
	priority := jobs.PriorityNormal
	loud := spec.Book.Loudness
	if spec.Audition {
		priority, loud = jobs.PriorityInteractive, LoudnessOff
	}
	var nodes []jobs.Node
	var keys []string
	for i, ch := range spec.Chapters {
		p := ChapterParams{Render: id, Run: run, Index: i, Title: ch.Title, Model: spec.Model, Lang: spec.Lang, Seed: spec.Seed,
			Voices: map[string]core.Take{}, Lines: ch.Lines, Production: spec.Production, Lexicon: spec.Lexicon,
			Loudness: loud, Output: chapterPath(dir, i), Scene: spec.Scene, Workflow: string(spec.Kind)}
		for _, l := range ch.Lines {
			for _, sp := range l.Spans {
				p.Voices[sp.Voice] = spec.Voices[sp.Voice]
			}
		}
		params, err := json.Marshal(p)
		if err != nil {
			return err
		}
		label := ch.Title
		if label == "" {
			label = strconv.Itoa(i + 1)
		}
		key := "c" + strconv.Itoa(i)
		keys = append(keys, key)
		jt := i18n.T("Chapter %s · %s", label, title)
		if spec.Audition {
			jt = i18n.T("Audition · %s", title)
		}
		nodes = append(nodes, jobs.Node{Key: key, Spec: jobs.Spec{Kind: KindChapter, Lane: jobs.LaneGPU, Priority: priority,
			Params: params, Title: jt, Owner: OwnerLongform + id, MaxAttempts: 2, Idempotent: true}})
	}
	if !spec.Audition {
		params, _ := json.Marshal(AssembleParams{Render: id, Run: run})
		nodes = append(nodes, jobs.Node{Key: "book", Spec: jobs.Spec{Kind: KindAssemble, Lane: jobs.LaneCPU, Priority: priority,
			Params: params, Title: i18n.T("Assemble · %s", title), Owner: OwnerLongform + id, After: keys, Idempotent: true}})
	}
	ids, err := s.sched.SubmitGraph(ctx, nodes)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if lr := s.live[id]; lr != nil {
		for i := range lr.body.Chapters {
			if j := ids["c"+strconv.Itoa(i)]; j != "" && lr.body.Chapters[i].Job == "" {
				lr.body.Chapters[i].Job = j
			}
		}
		if lr.body.AssembleJob == "" {
			lr.body.AssembleJob = ids["book"]
		}
	}
	s.mu.Unlock()
	return s.save(ctx, id)
}

// Stop cancels a render's jobs; the chapters made are kept, and Resume
// goes on from them.
func (s *Longform) Stop(ctx context.Context, id string) error {
	s.mu.Lock()
	lr := s.live[id]
	if lr == nil || lr.rec.State != core.RenderRunning {
		s.mu.Unlock()
		return nil
	}
	lr.body.Stopping = true
	var ids []string
	for _, c := range lr.body.Chapters {
		if c.Job != "" && (c.State == ChapterPending || c.State == ChapterRendering) {
			ids = append(ids, c.Job)
		}
	}
	if lr.body.AssembleJob != "" {
		ids = append(ids, lr.body.AssembleJob)
	}
	s.mu.Unlock()
	var errs []error
	for _, j := range ids {
		if err := s.sched.Cancel(ctx, j); err != nil && !errors.Is(err, jobs.ErrNotFound) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Resume renders again what a stopped or failed render lacks: its
// chapters made from the same program are kept, the rest (failed or not
// yet made) are rendered, then the book is assembled.
func (s *Longform) Resume(ctx context.Context, id string) (core.Render, error) {
	rec, body, err := s.load(ctx, id)
	if err != nil {
		return core.Render{}, err
	}
	if rec.State == core.RenderRunning {
		return rec, fmt.Errorf("longform: %q is rendering: %w", rec.Title, core.ErrBusy)
	}
	if !rec.Audition {
		if title, busy := s.busy(ctx, id); busy {
			return core.Render{}, fmt.Errorf("longform: %q is rendering: %w", title, core.ErrBusy)
		}
	}
	if err := checkFree("the render", filepath.Dir(body.Dir), bookNeed(chaptersLeft(body), body.Spec.Stems)); err != nil {
		return core.Render{}, err
	}
	now := s.now()
	s.mu.Lock()
	if !rec.Audition {
		for _, l := range s.live {
			if l.rec.ID != id && !l.rec.Audition && l.rec.State == core.RenderRunning {
				s.mu.Unlock()
				return core.Render{}, fmt.Errorf("longform: %q is rendering: %w", l.rec.Title, core.ErrBusy)
			}
		}
	}
	lr := s.live[id]
	if lr == nil {
		lr = &liveRender{rec: rec, body: body}
		s.live[id] = lr
	}
	lr.rec.State, lr.rec.UpdatedAt = core.RenderRunning, now
	b := &lr.body
	b.Runs++
	b.Stopping, b.Error, b.Assembling, b.Assembled, b.Finished = false, nil, false, 0, time.Time{}
	b.AssembleJob = ""
	for i := range b.Chapters {
		c := &b.Chapters[i]
		c.Job, c.Fraction, c.Error = "", 0, nil
		if c.State != ChapterDone && c.State != ChapterCached {
			c.State = ChapterPending
		}
	}
	s.mu.Unlock()
	if err := s.submit(ctx, id); err != nil {
		s.mu.Lock()
		lr.rec.State, lr.body.Error = core.RenderFailed, &jobs.Error{Code: CodeRenderFailed, Message: err.Error()}
		s.mu.Unlock()
		s.save(context.WithoutCancel(ctx), id)
		return core.Render{}, err
	}
	out, _ := s.snapshot(id)
	return out, nil
}

// chaptersLeft are the chapters of a render Resume renders again: those
// not done.
func chaptersLeft(b RenderBody) []ChapterSpec {
	var out []ChapterSpec
	for i, ch := range b.Spec.Chapters {
		if i < len(b.Chapters) && (b.Chapters[i].State == ChapterDone || b.Chapters[i].State == ChapterCached) {
			continue
		}
		out = append(out, ch)
	}
	return out
}
