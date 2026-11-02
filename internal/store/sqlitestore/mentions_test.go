package sqlitestore

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
)

func TestMentioned(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dir := "/data/Audio <Smith>/references"
	draft, _ := json.Marshal(map[string]string{"refAudio": dir + "/ref-1.wav"})
	if err := s.Documents().Put(ctx, "studio.draft", draft); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	body, _ := json.Marshal(map[string]any{"voices": map[string]any{"a": map[string]string{"refAudio": dir + "/clip-2.wav"}}})
	if err := s.Projects().Put(ctx, core.Project{ID: "p1", Kind: core.ProjectStory, Title: "T", Body: body, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Mentioned(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !got["ref-1.wav"] || !got["clip-2.wav"] || len(got) != 2 {
		t.Errorf("mentioned %v", got)
	}
}
