package services

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/store"
)

// Projects implements core.ProjectService: CRUD over the store.
type Projects struct {
	store store.Projects
	bus   *core.Bus
	now   func() time.Time
	mu    sync.Mutex
	// dropped deletes what a deleted project leaves: its unfinished
	// renders (Longform.DropProject); nil: nothing.
	dropped func(ctx context.Context, id string)
}

var _ core.ProjectService = (*Projects)(nil)

var projectKinds = []core.ProjectKind{core.ProjectStory, core.ProjectAudiobook, core.ProjectDub, core.ProjectTranscript, core.ProjectWorkflow}

func (s *Projects) List(ctx context.Context, kind core.ProjectKind, search string) ([]core.Project, error) {
	return s.store.List(ctx, kind, search)
}

func (s *Projects) Get(ctx context.Context, id string) (core.Project, error) {
	return s.store.Get(ctx, id)
}

// Save creates (empty ID) or updates a project, keeping its creation time.
func (s *Projects) Save(ctx context.Context, p core.Project) (core.Project, error) {
	var oldTitle string
	if p.ID != "" {
		if old, err := s.store.Get(ctx, p.ID); err == nil {
			oldTitle = old.Title
		}
	}
	saved, err := s.save(ctx, p)
	logSave(ctx, string(p.Kind), p.ID == "", oldTitle, saved.ID, saved.Title, err)
	return saved, err
}

func (s *Projects) save(ctx context.Context, p core.Project) (core.Project, error) {
	p.Title = strings.TrimSpace(p.Title)
	if p.Title == "" {
		return p, fmt.Errorf("project: no title: %w", core.ErrInvalid)
	}
	if !slices.Contains(projectKinds, p.Kind) {
		return p, fmt.Errorf("project: kind %q: %w", p.Kind, core.ErrInvalid)
	}
	s.mu.Lock()
	now := s.now()
	if p.ID == "" {
		p.ID, p.CreatedAt = core.NewID(now), now
	} else {
		old, err := s.store.Get(ctx, p.ID)
		if err != nil {
			s.mu.Unlock()
			return p, err
		}
		p.CreatedAt = old.CreatedAt
	}
	p.UpdatedAt = now
	if len(p.Body) == 0 {
		p.Body = []byte("{}")
	}
	err := s.store.Put(ctx, p)
	s.mu.Unlock()
	if err != nil {
		return p, err
	}
	s.bus.Publish(core.ProjectChanged{Project: p})
	return p, nil
}

func (s *Projects) Delete(ctx context.Context, id string) error {
	if s.dropped != nil {
		// First: the renders lose their project when it goes (ON DELETE
		// SET NULL), and could not be found by it after.
		if _, err := s.store.Get(ctx, id); err == nil {
			s.dropped(ctx, id)
		}
	}
	s.mu.Lock()
	p, err := s.store.Get(ctx, id)
	if err == nil {
		err = s.store.Delete(ctx, id)
	}
	s.mu.Unlock()
	logDelete(ctx, firstNonEmpty(string(p.Kind), "project"), id, p.Title, err)
	if err != nil {
		return err
	}
	s.bus.Publish(core.ProjectChanged{Project: p, Deleted: true})
	return nil
}
