package services

// A longform render while its jobs run: the live record (load, snapshot,
// save, publish), job updates applied to the render and its chapters,
// settling the render, and the ETA; split from longform.go.

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

// load returns a render, from memory when it is followed.
func (s *Longform) load(ctx context.Context, id string) (core.Render, RenderBody, error) {
	s.mu.Lock()
	if lr := s.live[id]; lr != nil {
		rec, body := lr.rec, lr.body
		body.Chapters = slices.Clone(body.Chapters)
		s.mu.Unlock()
		return rec, body, nil
	}
	s.mu.Unlock()
	rec, err := s.renders.Get(ctx, id)
	if err != nil {
		return rec, RenderBody{}, err
	}
	body, err := DecodeRender(rec)
	return rec, body, err
}

func (s *Longform) forget(id string) {
	s.mu.Lock()
	delete(s.live, id)
	s.mu.Unlock()
}

// snapshot is a followed render with its published body.
func (s *Longform) snapshot(id string) (core.Render, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lr := s.live[id]
	if lr == nil {
		return core.Render{}, false
	}
	return lr.published(), true
}

// published is the render with its body without the manifest's chapters
// and voices. Caller holds mu.
func (lr *liveRender) published() core.Render {
	b := lr.body
	b.Spec = LongformSpec{Project: b.Spec.Project, Kind: b.Spec.Kind, Title: b.Spec.Title, Model: b.Spec.Model,
		Lang: b.Spec.Lang, Format: b.Spec.Format, Stems: b.Spec.Stems, Book: b.Spec.Book, Production: b.Spec.Production,
		Audition: b.Spec.Audition}
	b.Chapters = slices.Clone(b.Chapters)
	rec := lr.rec
	rec.Body, _ = json.Marshal(b)
	return rec
}

// save writes a followed render's record.
func (s *Longform) save(ctx context.Context, id string) error {
	s.mu.Lock()
	lr := s.live[id]
	if lr == nil {
		s.mu.Unlock()
		return nil
	}
	rec := lr.rec
	rec.UpdatedAt = s.now()
	lr.rec.UpdatedAt = rec.UpdatedAt
	body, err := json.Marshal(lr.body)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	rec.Body = body
	if err := s.renders.Put(ctx, rec); err != nil {
		s.logf("render %s: %v", id, err)
		return err
	}
	return nil
}

func (s *Longform) publish(id string) {
	if r, ok := s.snapshot(id); ok {
		s.bus.Publish(core.RenderChanged{Render: r})
	}
}

// onJob is the jobs.Updated handler: updates of longform jobs are applied
// in order, off the publisher's goroutine.
func (s *Longform) onJob(u jobs.Updated) {
	if u.Chunk != nil || !strings.HasPrefix(u.Job.Owner, OwnerLongform) {
		return
	}
	s.qmu.Lock()
	if s.closed {
		s.qmu.Unlock()
		return
	}
	s.queue = append(s.queue, u.Job)
	if s.draining {
		s.qmu.Unlock()
		return
	}
	s.draining = true
	s.drained.Add(1)
	s.qmu.Unlock()
	s.async(s.drain)
}

func (s *Longform) drain() {
	defer s.drained.Done()
	for {
		s.qmu.Lock()
		if len(s.queue) == 0 || s.closed {
			s.draining = false
			s.qmu.Unlock()
			return
		}
		j := s.queue[0]
		s.queue = s.queue[1:]
		s.qmu.Unlock()
		s.apply(j)
	}
}

// apply folds one job update into its render.
func (s *Longform) apply(j jobs.Job) {
	id := strings.TrimPrefix(j.Owner, OwnerLongform)
	ctx := context.Background()
	s.mu.Lock()
	lr := s.live[id]
	s.mu.Unlock()
	if lr == nil {
		rec, err := s.renders.Get(ctx, id)
		if err != nil {
			return // a render deleted meanwhile
		}
		body, err := DecodeRender(rec)
		if err != nil {
			s.logf("render %s: %v", id, err)
			return
		}
		s.mu.Lock()
		if lr = s.live[id]; lr == nil {
			lr = &liveRender{rec: rec, body: body}
			s.live[id] = lr
		}
		s.mu.Unlock()
	}

	s.mu.Lock()
	b := &lr.body
	if run := runOf(j); run != 0 && run != b.Runs {
		// A late update of an earlier run (Resume came before it): taken
		// for the new run's, it would claim a chapter or the assembly
		// and hide the new job's updates.
		s.mu.Unlock()
		return
	}
	before := lr.rec.State
	durable := false
	revive := func() {
		if lr.rec.State != core.RenderRunning && !j.State.Terminal() && j.State != jobs.StateInterrupted {
			// The scheduler runs a job of a render recorded as ended (re-run
			// after a restart): it renders again.
			lr.rec.State, b.Finished, b.Error = core.RenderRunning, time.Time{}, nil
		}
	}
	switch j.Kind {
	case KindChapter:
		i := s.chapterOf(b, j)
		if i < 0 {
			s.mu.Unlock()
			return
		}
		revive()
		durable = applyChapter(&b.Chapters[i], j, s.now())
	case KindAssemble:
		if b.AssembleJob == "" {
			b.AssembleJob = j.ID
		}
		if b.AssembleJob != j.ID {
			s.mu.Unlock()
			return
		}
		revive()
		switch j.State {
		case jobs.StateRunning:
			durable = !b.Assembling
			b.Assembling = true
			if j.Progress.Fraction >= 0 {
				b.Assembled = j.Progress.Fraction
			}
		case jobs.StateSucceeded:
			var res AssembleResult
			if err := json.Unmarshal(j.Result, &res); err == nil {
				b.Output = &res
				lr.rec.AudioSeconds = res.Seconds
			}
			b.Assembling, b.Assembled, durable = false, 1, true
		case jobs.StateFailed, jobs.StateCancelled:
			b.Assembling, durable = false, true
			if j.Err != nil && j.Err.Code != jobs.CodeDependency && j.Err.Code != jobs.CodeCancelled {
				b.Error = j.Err
			}
		}
	default:
		s.mu.Unlock()
		return
	}
	s.settle(lr)
	if lr.rec.State != before {
		durable = true
	}
	done := lr.rec.State == core.RenderDone && before != core.RenderDone
	rec := lr.rec
	s.mu.Unlock()
	if durable {
		s.save(ctx, id)
	}
	s.publish(id)
	if done {
		s.dropSuperseded(ctx, rec) // longform_sweep.go
	}
}

// runOf is the run a longform job was submitted for (0: unknown, a job
// of an older version).
func runOf(j jobs.Job) int {
	var p struct {
		Run int `json:"run"`
	}
	json.Unmarshal(j.Params, &p)
	return p.Run
}

// chapterOf finds the chapter a job renders: by its id, else by its
// params (an update that came before Submit returned). -1 for a job of
// an earlier run. Caller holds mu.
func (s *Longform) chapterOf(b *RenderBody, j jobs.Job) int {
	for i, c := range b.Chapters {
		if c.Job == j.ID {
			return i
		}
	}
	var p struct {
		Index int `json:"index"`
	}
	if err := json.Unmarshal(j.Params, &p); err != nil || p.Index < 0 || p.Index >= len(b.Chapters) {
		return -1
	}
	if c := &b.Chapters[p.Index]; c.Job == "" {
		c.Job = j.ID
		return p.Index
	}
	return -1
}

// applyChapter folds a chapter job's update; it reports whether the
// chapter's state changed (worth saving).
func applyChapter(c *RenderChapter, j jobs.Job, now time.Time) bool {
	old := c.State
	switch j.State {
	case jobs.StateWaiting, jobs.StateQueued:
		c.State = ChapterPending
	case jobs.StateRunning:
		c.State = ChapterRendering
		if c.Started.IsZero() || old != ChapterRendering {
			c.Started = j.Started
			if c.Started.IsZero() {
				c.Started = now
			}
		}
		if j.Progress.Fraction >= 0 {
			c.Fraction = j.Progress.Fraction
		}
		c.AudioSeconds = j.Progress.AudioSeconds
	case jobs.StateSucceeded:
		var r ChapterResult
		json.Unmarshal(j.Result, &r)
		c.State, c.Fraction, c.Error = ChapterDone, 1, nil
		if r.Reused || (r.Chunks > 0 && r.Cached == r.Chunks) {
			c.State = ChapterCached
		}
		c.File, c.AudioSeconds, c.Seconds, c.Chunks, c.Cached = r.Output, r.AudioSeconds, r.Seconds, r.Chunks, r.Cached
		c.Finished = j.Finished
		if c.Finished.IsZero() {
			c.Finished = now
		}
	case jobs.StateFailed:
		c.State, c.Error = ChapterFailed, j.Err
		c.Finished = now
	case jobs.StateCancelled:
		c.State = ChapterStopped
		c.Finished = now
	case jobs.StateInterrupted:
		c.State = ChapterStopped
		if j.Idempotent && j.Err != nil && j.Err.Code == jobs.CodeShutdown {
			c.State = ChapterPending // the app quits: it runs again at the next start
		}
	}
	return c.State != old
}

// settle decides a render's state from its jobs. Caller holds mu.
func (s *Longform) settle(lr *liveRender) {
	b := &lr.body
	if lr.rec.State != core.RenderRunning {
		return
	}
	for _, c := range b.Chapters {
		if c.State == ChapterPending || c.State == ChapterRendering {
			return
		}
	}
	allMade := b.Done() == len(b.Chapters)
	switch {
	case lr.rec.Audition && allMade:
		lr.rec.State = core.RenderDone
	case lr.rec.Audition:
		lr.rec.State = core.RenderFailed
	case b.Output != nil && allMade:
		lr.rec.State = core.RenderDone
	case b.Assembling || (allMade && b.Error == nil && !b.Stopping && b.AssembleJob != ""):
		return // the book is being assembled (or about to be)
	case b.Stopping:
		lr.rec.State = core.RenderStopped
	default:
		lr.rec.State = core.RenderFailed
	}
	if lr.rec.State != core.RenderRunning {
		b.Finished = s.now()
		b.Stopping = false
		if lr.rec.State == core.RenderDone && lr.rec.Audition {
			for _, c := range b.Chapters {
				lr.rec.AudioSeconds += c.AudioSeconds
			}
		}
	}
}

// ETA estimates the seconds a render still needs from the characters done
// so far and the time they took ("" when too early to say). Pure.
func ETA(b *RenderBody, now time.Time) (time.Duration, bool) {
	var doneChars, leftChars float64
	var spent time.Duration
	for _, c := range b.Chapters {
		switch c.State {
		case ChapterDone:
			doneChars += float64(c.Chars)
			spent += c.Finished.Sub(c.Started)
		case ChapterRendering:
			doneChars += float64(c.Chars) * c.Fraction
			leftChars += float64(c.Chars) * (1 - c.Fraction)
			if !c.Started.IsZero() {
				spent += now.Sub(c.Started)
			}
		case ChapterPending, ChapterFailed, ChapterStopped:
			leftChars += float64(c.Chars)
		}
	}
	if doneChars < 1 || spent <= 0 {
		return 0, false
	}
	per := float64(spent) / doneChars
	return time.Duration(per * leftChars), true
}
