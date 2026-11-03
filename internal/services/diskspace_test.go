package services

// Free-space pre-checks (code review 1102 M4): long work is refused up
// front with disk_full (bytes needed and free) when the disk cannot hold
// it, and a write that finds the disk full says so instead of "internal".

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/store"
)

// fakeFree makes every disk have free bytes until the test ends.
func fakeFree(t *testing.T, free int64) {
	t.Helper()
	saved := freeSpace
	freeSpace = func(string) int64 { return free }
	t.Cleanup(func() { freeSpace = saved })
}

// wantDiskFull checks err is a pre-check's refusal needing more than free.
func wantDiskFull(t *testing.T, err error, free int64) *core.DiskFullError {
	t.Helper()
	var d *core.DiskFullError
	if !errors.As(err, &d) || !errors.Is(err, core.ErrDiskFull) {
		t.Fatalf("want a disk_full refusal, got %v", err)
	}
	if d.Free != free || d.Need <= free {
		t.Fatalf("need %d, free %d (faked %d)", d.Need, d.Free, free)
	}
	if je := jobs.DiskFull(err); je == nil || je.Code != core.CodeDiskFull || !strings.Contains(string(je.Data), `"need"`) {
		t.Fatalf("as a job error: %+v", je)
	}
	return d
}

func TestDiskCheckDubRun(t *testing.T) {
	e := newDubEnv(t, 2)
	// An hour of source: ingest, separation, speech and the mix of one
	// language are well over 2 GB.
	p := e.create(t, func(p *dub.Project) { p.Source.Duration = 3600 })
	fakeFree(t, 2e9)
	_, err := e.dubs.Run(ctx, p.ID, RunRequest{Through: dub.StageMix})
	d := wantDiskFull(t, err, 2e9)
	if d.Need < 3e9 || d.Need > 6e9 {
		t.Errorf("an hour's dub needs %s?", core.SizeText(d.Need))
	}
	if q, _ := e.dubs.Get(ctx, p.ID); q.Run != nil {
		t.Error("the refused run was recorded")
	}
	if active, _ := e.dubs.Active(ctx, p.ID); len(active) != 0 {
		t.Errorf("jobs submitted: %d", len(active))
	}
	// With room it runs.
	fakeFree(t, 100e9)
	p = e.run(t, p.ID, RunRequest{Through: dub.StageMix})
	if p.Tracks["es"].File == "" {
		t.Fatal("no track")
	}
	// An export is checked against the disk of its output.
	fakeFree(t, 1e9)
	_, err = e.dubs.Export(ctx, ExportRequest{Dub: p.ID, Format: dub.ExportWAV, Lang: "es", Output: filepath.Join(t.TempDir(), "x.wav")}, nil)
	wantDiskFull(t, err, 1e9)
	// As a job it fails with disk_full, not dub_failed.
	id, err := e.dubs.ExportJob(ctx, ExportRequest{Dub: p.ID, Format: dub.ExportWAV, Lang: "es", Output: filepath.Join(t.TempDir(), "y.wav")})
	if err != nil {
		t.Fatal(err)
	}
	if j := e.wait(t, id); j.Err == nil || j.Err.Code != core.CodeDiskFull {
		t.Fatalf("export job %+v", j.Err)
	}
}

// A stage whose write finds the disk full (the engine's message) is
// marked disk_full, not dub_failed or internal.
func TestDiskFullDubStageWrite(t *testing.T) {
	e := newDubEnv(t, 2)
	full := "The file “vocals.wav” couldn’t be saved because there isn’t enough space."
	inner := e.dubs.cfg.Engine
	e.dubs.cfg.Engine = func(m string) jobs.Executor {
		if m != dub.MethodSeparate {
			return inner(m)
		}
		return jobs.ExecutorFunc(func(context.Context, jobs.Job, func(jobs.Progress), func(json.RawMessage)) (json.RawMessage, error) {
			return nil, &jobs.Error{Code: proto.CodeInternal, Message: full}
		})
	}
	p := e.create(t, func(p *dub.Project) { p.Settings.KeepBackground = true })
	ids, err := e.dubs.Run(ctx, p.ID, RunRequest{Through: dub.StageReferences})
	if err != nil {
		t.Fatal(err)
	}
	j := e.wait(t, ids[dub.StageKey(dub.StageSeparate, "")])
	if j.Err == nil || j.Err.Code != core.CodeDiskFull || j.Err.Message != full {
		t.Fatalf("separate job %+v", j.Err)
	}
	for _, id := range ids {
		e.wait(t, id)
	}
	p, _ = e.dubs.Get(ctx, p.ID)
	if st := p.Stage(dub.StageSeparate, ""); st.Status != dub.StatusFailed || st.Code != core.CodeDiskFull {
		t.Errorf("stage %+v", st)
	}
	// Go's ENOSPC, the engine's other words; not other failures.
	je := jobs.DiskFull(fmt.Errorf("decode: %w", &os.PathError{Op: "write", Path: "a.wav", Err: syscall.ENOSPC}))
	if je == nil || je.Code != core.CodeDiskFull || je.Retryable {
		t.Fatalf("ENOSPC: %+v", je)
	}
	for _, msg := range []string{"未能存储文件，因为没有足够的空间。", "write /x: no space left on device"} {
		if je := jobs.DiskFull(&jobs.Error{Code: proto.CodeInternal, Message: msg}); je == nil || je.Code != core.CodeDiskFull || je.Message != msg {
			t.Errorf("%q: %+v", msg, je)
		}
	}
	if je := jobs.DiskFull(&jobs.Error{Code: proto.CodeInternal, Message: "model failed"}); je != nil {
		t.Errorf("not a full disk: %+v", je)
	}
}

func TestDiskCheckLongform(t *testing.T) {
	e := newLongformEnv(t, t.TempDir(), true)
	defer e.close()
	spec := storySpec()
	// A long book: 300 000 words (about 33 h spoken).
	words := strings.Repeat("word ", 1000)
	spec.Chapters = nil
	for range 300 {
		spec.Chapters = append(spec.Chapters, ChapterSpec{Lines: []LineSpec{{Spans: []SpanSpec{{Voice: "narrator", Text: words}}}}})
	}
	fakeFree(t, 20e9)
	_, err := e.svc.Longform.Start(ctx, spec)
	wantDiskFull(t, err, 20e9)
	if l, _ := e.svc.Longform.List(ctx, store.RenderQuery{}); len(l) != 0 {
		t.Errorf("the refused render was recorded: %d", len(l))
	}
	// The short story fits.
	r, err := e.svc.Longform.Start(ctx, storySpec())
	if err != nil {
		t.Fatal(err)
	}
	e.wait(t, r.ID)
}

func TestDiskCheckIsolation(t *testing.T) {
	svc, eng, sched, _ := newIsolateTest(t)
	// A WAV's length is read up front: 10 minutes at 44.1 kHz stereo is
	// about 420 MB of stems and decoded audio.
	in := filepath.Join(t.TempDir(), "talk.wav")
	writeSilence(t, in, 600)
	fakeFree(t, 1.2e9)
	_, err := svc.Start(ctx, in)
	wantDiskFull(t, err, 1.2e9)
	if l, _ := svc.List(ctx); len(l) != 0 {
		t.Errorf("the refused isolation was recorded: %+v", l)
	}
	// Another file's length is known once decoded: the job stops before
	// separating.
	video := filepath.Join(t.TempDir(), "talk.mp4")
	os.WriteFile(video, []byte("video"), 0o644)
	fakeFree(t, DiskKeepFree+1) // the start passes; 3 s of stems do not fit
	iso, err := svc.Start(ctx, video)
	if err != nil {
		t.Fatal(err)
	}
	j, _ := sched.Get(ctx, iso.Job)
	_, err = svc.Executor().Run(ctx, j, func(jobs.Progress) {}, nil)
	var je *jobs.Error
	if !errors.As(err, &je) || je.Code != core.CodeDiskFull {
		t.Fatalf("run: %v", err)
	}
	for _, c := range eng.calls {
		if c == proto.MethodSeparateRun {
			t.Error("separated anyway")
		}
	}
	got, _ := svc.Get(ctx, iso.ID)
	if got.State != IsolateFailed || got.ErrorCode != core.CodeDiskFull {
		t.Errorf("isolation %+v", got)
	}
}

func TestDiskCheckExportAudio(t *testing.T) {
	src := filepath.Join(t.TempDir(), "take.wav")
	writeSilence(t, src, 60)
	fakeFree(t, DiskKeepFree+1e6)
	_, err := ExportAudio(ctx, src, filepath.Join(t.TempDir(), "out.wav"), AudioExport{Format: FormatWAV, Bits: 16}, nil)
	wantDiskFull(t, err, DiskKeepFree+1e6)
	fakeFree(t, -1) // unknown: no check
	if _, err := ExportAudio(ctx, src, filepath.Join(t.TempDir(), "out.wav"), AudioExport{Format: FormatWAV, Bits: 16}, nil); err != nil {
		t.Fatal(err)
	}
}

// The estimates stay near what the work writes.
func TestDiskEstimates(t *testing.T) {
	hour := &dub.Project{Source: dub.Source{Duration: 3600}}
	nodes := []jobs.Node{{Spec: jobs.Spec{Kind: KindDubIngest}}, {Spec: jobs.Spec{Kind: KindDubSeparate}}, {Spec: jobs.Spec{Kind: KindDubTranscribe}}}
	if n := dubRunNeed(hour, nodes); n < 2e9 || n > 3e9 {
		t.Errorf("an hour's ingest and separation: %s", core.SizeText(n))
	}
	if n := dubRunNeed(&dub.Project{}, nodes); n != 0 {
		t.Errorf("unknown length: %d", n)
	}
	if n := isolateNeed(3600); n < 2e9 || n > 3e9 {
		t.Errorf("an hour's isolation: %s", core.SizeText(n))
	}
	// 9 000 words, an hour spoken: about 2 GB with stems.
	ch := []ChapterSpec{{Lines: []LineSpec{{Spans: []SpanSpec{{Text: strings.Repeat("word ", 9000)}}}}}}
	if n := bookNeed(ch, true); n < 1e9 || n > 4e9 {
		t.Errorf("an hour's book: %s", core.SizeText(n))
	}
	if s := bookSeconds([]ChapterSpec{{Lines: []LineSpec{{Spans: []SpanSpec{{Text: "你好世界"}}}}}}); s < 0.5 || s > 1.5 {
		t.Errorf("four CJK characters speak %.2f s", s)
	}
}

func writeSilence(t *testing.T, path string, seconds int) {
	t.Helper()
	b := dsp.Buffer{Rate: 44100, Channels: 2, Samples: make([]float32, 44100*2*seconds)}
	if err := wav.WriteFile(path, b, wav.PCM16, wav.Options{}); err != nil {
		t.Fatal(err)
	}
}
