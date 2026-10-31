package services

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

// A dub copied to another folder (a bundled sample opened, the data
// folder moved) keeps its render keys: editing one line renders that
// line again, not every line (0.5 F6b).
func TestDubCopyRendersOnlyTheEditedLine(t *testing.T) {
	e := newDubEnv(t, 4)
	p := e.create(t, nil)
	p = e.run(t, p.ID, RunRequest{Through: dub.StageMix})
	if len(p.Track("es").Lines) != 4 {
		t.Fatalf("lines %+v", p.Track("es").Lines)
	}

	// The copy: the project's folder under a new id, its document as is
	// (file names relative to the folder).
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var q dub.Project
	if err := json.Unmarshal(b, &q); err != nil {
		t.Fatal(err)
	}
	q.ID, q.CreatedAt = core.NewID(time.Now()), time.Time{}
	src, dst := e.dubs.Dir(p.ID), e.dubs.Dir(q.ID)
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	copied, err := e.dubs.Create(ctx, &q)
	if err != nil {
		t.Fatal(err)
	}

	// Nothing changed: nothing is rendered again.
	before := e.eng.Count(proto.MethodTTSSynthesize)
	same := e.run(t, copied.ID, RunRequest{Through: dub.StageMix})
	if n := e.eng.Count(proto.MethodTTSSynthesize) - before; n != 0 {
		t.Fatalf("the unchanged copy rendered %d lines again", n)
	}
	for id, r := range p.Track("es").Lines {
		if same.Track("es").Lines[id].Key != r.Key {
			t.Errorf("%s: key %s, was %s", id, same.Track("es").Lines[id].Key, r.Key)
		}
	}

	// One line edited: only it.
	edited := same.Segments[1].ID
	if _, err := e.dubs.Update(ctx, copied.ID, func(x *dub.Project) error {
		l := x.Segments[1].Lines["es"]
		l.Text, l.Edited = "Una frase nueva.", true
		x.Segments[1].Lines["es"] = l
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before = e.eng.Count(proto.MethodTTSSynthesize)
	after := e.run(t, copied.ID, RunRequest{Through: dub.StageMix})
	if n := e.eng.Count(proto.MethodTTSSynthesize) - before; n != 1 {
		t.Fatalf("editing one line rendered %d", n)
	}
	for id, r := range same.Track("es").Lines {
		if changed := after.Track("es").Lines[id].Key != r.Key; changed != (id == edited) {
			t.Errorf("%s: key changed %v", id, changed)
		}
	}
}
