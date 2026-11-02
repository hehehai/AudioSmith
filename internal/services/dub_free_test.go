package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/translate"
)

func wavEncoding(t *testing.T, path string) wav.Encoding {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rd, err := wav.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	return rd.Format().Encoding
}

// The user, 11-02 (code review 1102 L4): a dub exported a while ago
// frees its working files; edited again, it makes what it needs and
// exports again.
func TestDubFreeWorkThenEditAndExport(t *testing.T) {
	e := newDubEnv(t, 6)
	p := e.create(t, func(p *dub.Project) { p.Targets = []string{"es", "fr"} })
	p = e.run(t, p.ID, RunRequest{Through: dub.StageMix})
	out := t.TempDir()
	export := func(f string) error {
		_, err := e.dubs.Export(ctx, ExportRequest{Dub: p.ID, Format: f, Lang: "es", Output: filepath.Join(out, "x."+f)}, nil)
		return err
	}
	if err := export(dub.ExportWAV); err != nil {
		t.Fatal(err)
	}
	p, _ = e.dubs.Get(ctx, p.ID)
	if p.Exported.IsZero() {
		t.Fatal("the export was not recorded")
	}
	// Not yet: touched (opened) a moment ago.
	e.dubs.Touch(p.ID)
	if n, _ := e.dubs.FreeIdle(ctx, time.Hour, nil, nil); n != 0 {
		t.Fatal("freed a dub opened a moment ago")
	}
	// French is removed, and a line deleted.
	gone := p.Segments[5].ID
	p, _ = e.dubs.Update(ctx, p.ID, func(q *dub.Project) error {
		q.ChooseTargets([]string{"es"})
		q.Delete(gone)
		return nil
	})
	updated := p.UpdatedAt
	if n, freed := e.dubs.FreeIdle(ctx, 0, nil, func(string) bool { return false }); n != 1 || freed == 0 {
		t.Fatalf("freed %d dubs, %d bytes", n, freed)
	}
	p, _ = e.dubs.Get(ctx, p.ID)
	if p.Freed.IsZero() || !e.dubs.WorkGone(p) || !p.UpdatedAt.Equal(updated) {
		t.Fatalf("freed %v, gone %v, updated %v (was %v)", p.Freed, e.dubs.WorkGone(p), p.UpdatedAt, updated)
	}
	for _, name := range []string{dubAudio, dubSpeech, dubVocals, dubVocals16k, dubBackground, filepath.Join("renders", "fr"), filepath.Join("tracks", "fr.wav")} {
		if fileExists(e.dubs.Path(p.ID, name)) {
			t.Errorf("%s stayed", name)
		}
	}
	if _, ok := p.Tracks["fr"]; ok {
		t.Error("the removed language's track stayed")
	}
	if old, _ := filepath.Glob(e.dubs.Path(p.ID, filepath.Join("renders", "es", gone+"-*"))); len(old) > 0 {
		t.Errorf("the deleted line's render stayed: %v", old)
	}
	// What shows and plays the dub stays; the lines at 16 bits.
	for _, name := range []string{p.Media.Peaks, p.Track("es").File, p.Track("es").Speech} {
		if !fileExists(e.dubs.Path(p.ID, name)) {
			t.Errorf("%s went", name)
		}
	}
	for id, r := range p.Track("es").Lines {
		if id == gone {
			continue
		}
		if enc := wavEncoding(t, e.dubs.Path(p.ID, r.File)); enc != wav.PCM16 {
			t.Errorf("line %s: %v", id, enc)
		}
	}
	if e.dubs.MixedTrack(p, "es") == "" {
		t.Error("no mixed track to play")
	}
	// The mixed track exports as it is; the stems need a run.
	if err := export(dub.ExportWAV); err != nil {
		t.Errorf("wav of a freed dub: %v", err)
	}
	if err := export(dub.ExportStems); code(err) != DubCodeFreed {
		t.Errorf("stems of a freed dub: %v", err)
	}
	// Edit a line: the run decodes and separates again, renders that line
	// alone, mixes.
	id := p.Segments[1].ID
	e.dubs.Update(ctx, p.ID, func(q *dub.Project) error {
		q.SetText(id, "es", "¿Cómo estás hoy?")
		return nil
	})
	synth, sep := e.eng.Count(proto.MethodTTSSynthesize), e.eng.Count(dub.MethodSeparate)
	p = e.run(t, p.ID, RunRequest{Through: dub.StageMix})
	if n := e.eng.Count(proto.MethodTTSSynthesize) - synth; n != 1 {
		t.Errorf("rendered %d lines", n)
	}
	if e.eng.Count(dub.MethodSeparate) != sep+1 {
		t.Error("not separated again")
	}
	if !p.Freed.IsZero() || e.dubs.WorkGone(p) || p.Stage(dub.StageMix, "es").Status != dub.StatusDone {
		t.Fatalf("after the run: freed %v gone %v mix %+v", p.Freed, e.dubs.WorkGone(p), p.Stage(dub.StageMix, "es"))
	}
	for _, f := range []string{dub.ExportWAV, dub.ExportStems, dub.ExportSegments, dub.ExportSRT} {
		if err := export(f); err != nil {
			t.Errorf("%s after the run: %v", f, err)
		}
	}
}

// Rebuild decodes the audio alone; FreeWork waits for no job.
func TestDubRebuildAndBusy(t *testing.T) {
	e := newDubEnv(t, 3)
	p := e.create(t, nil)
	p = e.run(t, p.ID, RunRequest{Through: dub.StageMix})
	if _, err := e.dubs.FreeWork(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	ids, err := e.dubs.Rebuild(ctx, p.ID)
	if err != nil || len(ids) != 1 {
		t.Fatalf("rebuild %v %v", ids, err)
	}
	for _, j := range ids {
		e.wait(t, j)
	}
	p, _ = e.dubs.Get(ctx, p.ID)
	if !fileExists(e.dubs.Path(p.ID, dubAudio)) || p.Run != nil || p.Freed.IsZero() {
		t.Errorf("after rebuild: audio %v run %+v freed %v", fileExists(e.dubs.Path(p.ID, dubAudio)), p.Run, p.Freed)
	}
	// A source gone: nothing that cannot be made again is freed.
	os.Remove(e.src)
	if _, err := e.dubs.FreeWork(ctx, p.ID); err != nil || !fileExists(e.dubs.Path(p.ID, dubAudio)) {
		t.Errorf("freed without the source: %v", err)
	}
	// A job going on: busy.
	q := e.create(t, nil)
	block := make(chan struct{})
	defer close(block)
	e.dubs.cfg.Translator = func(ctx context.Context, _ dub.Settings) (translate.Translator, error) {
		select {
		case <-block:
		case <-ctx.Done():
		}
		return nil, ctx.Err()
	}
	if _, err := e.dubs.Run(ctx, q.ID, RunRequest{Through: dub.StageMix}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.dubs.FreeWork(ctx, q.ID); !errors.Is(err, core.ErrBusy) {
		t.Errorf("free during a run: %v", err)
	}
	e.dubs.Cancel(ctx, q.ID)
}
