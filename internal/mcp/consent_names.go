package mcp

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/core"
)

// deletePrompt is the question delete_item asks the user: the item's own
// name and what it is, from AudioSmith, not the agent's id alone, so a
// wrong id shows (code review 11-02, N7).
func (s *Server) deletePrompt(ctx context.Context, kind, id string) string {
	var what string
	if name := s.itemName(ctx, kind, id); name != "" {
		what = fmt.Sprintf("%s %s (id %s)", kind, name, id)
	} else {
		what = fmt.Sprintf("%s %s", kind, id)
	}
	return "AudioSmith: delete the " + what + "? This cannot be undone."
}

// itemName names an item for deletePrompt (quoted, with its type), or ""
// when it cannot be found.
func (s *Server) itemName(ctx context.Context, kind, id string) string {
	q := func(name string) string { return "“" + clip(core.CleanName(name), 60) + "”" }
	switch kind {
	case api.DeleteTake:
		if t, err := s.o.Backend.Take(ctx, id); err == nil {
			return fmt.Sprintf("%s (%.0f s)", q(t.Text), t.AudioSeconds)
		}
	case api.DeleteVoice:
		if v, err := s.o.Backend.Voice(ctx, id); err == nil {
			return fmt.Sprintf("%s (%s)", q(v.Name), v.Kind)
		}
	case api.DeleteTranscript:
		if t, err := s.o.Backend.Transcript(ctx, id); err == nil {
			return q(firstNonEmpty(t.Title, t.Preferred()))
		}
	}
	if s.o.API == nil {
		return ""
	}
	switch kind {
	case api.DeleteProject:
		if p, err := s.o.API.Longform(ctx, id); err == nil {
			return fmt.Sprintf("%s (%s)", q(p.Title), p.Kind)
		}
	case api.DeleteDub:
		if d, err := s.o.API.Dub(ctx, api.DubQuery{ID: id}); err == nil {
			return q(d.Title)
		}
	case api.DeleteIsolation:
		if x, err := s.o.API.Isolation(ctx, id); err == nil {
			return q(x.Title)
		}
	}
	return ""
}

// clip shortens s to n runes, with an ellipsis.
func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}
