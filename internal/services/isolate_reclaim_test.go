package services

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

// newIsolateSched is an isolation service on a real scheduler, whose
// separation waits for release, then writes its stems (as the engine
// does after a cancel, at the end of its window).
func newIsolateSched(t *testing.T) (*Isolations, chan struct{}, chan struct{}) {
	t.Helper()
	bus := core.NewBus()
	eng := &isoEngine{}
	inSep, release := make(chan struct{}, 1), make(chan struct{})
	sched := jobs.New(jobs.Options{Store: jobs.NewMemStore(), Publish: func(u jobs.Updated) { bus.Publish(u) },
		RetryDelay: func(int) time.Duration { return time.Millisecond }})
	svc := NewIsolations(IsolationsConfig{Dir: filepath.Join(t.TempDir(), "isolations"), Bus: bus, Scheduler: sched,
		Models:    func() (string, string) { return "sep", "enh" },
		ModelPath: func(_ context.Context, repo string) (string, error) { return "/snap/" + repo, nil },
		Engine: func(method string) jobs.Executor {
			if method != proto.MethodSeparateRun {
				return eng.executor(method)
			}
			return jobs.ExecutorFunc(func(ctx context.Context, job jobs.Job, _ func(jobs.Progress), _ func(json.RawMessage)) (json.RawMessage, error) {
				var p proto.SeparateParams
				json.Unmarshal(job.Params, &p)
				inSep <- struct{}{}
				<-release
				for _, path := range []string{p.Vocals.Path, p.Background.Path} {
					os.MkdirAll(filepath.Dir(path), 0o755)
					wav.WriteFile(path, dsp.Buffer{Rate: 44100, Channels: 2, Samples: make([]float32, 44100)}, wav.PCM16, wav.Options{})
				}
				return nil, context.Canceled
			})
		}})
	if err := sched.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sched.Close() })
	return svc, inSep, release
}

// Code review 1102 M5 (PoC TestCR1102IsolationDeleteDuringRun): Delete
// removed the folder at once; the engine's late write made it again, a
// folder List never shows.
func TestIsolationDeleteWaitsForTheJob(t *testing.T) {
	svc, inSep, release := newIsolateSched(t)
	in := filepath.Join(t.TempDir(), "Interview.wav")
	wav.WriteFile(in, dsp.Buffer{Rate: 44100, Channels: 2, Samples: make([]float32, 8820)}, wav.PCM16, wav.Options{})
	iso, err := svc.Start(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	<-inSep
	deleted := make(chan error, 1)
	go func() { deleted <- svc.Delete(ctx, iso.ID) }()
	select {
	case err := <-deleted:
		t.Fatalf("Delete returned while the job ran: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-deleted; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(svc.Dir(iso.ID)); !os.IsNotExist(err) {
		ents, _ := os.ReadDir(svc.Dir(iso.ID))
		t.Fatalf("a stray folder with %d files", len(ents))
	}
}

// A job that does not end in time makes Delete busy; the folder stays.
func TestIsolationDeleteBusy(t *testing.T) {
	svc, inSep, release := newIsolateSched(t)
	defer close(release)
	svc.deleteWait = 100 * time.Millisecond
	in := filepath.Join(t.TempDir(), "Interview.wav")
	wav.WriteFile(in, dsp.Buffer{Rate: 44100, Channels: 2, Samples: make([]float32, 8820)}, wav.PCM16, wav.Options{})
	iso, _ := svc.Start(ctx, in)
	<-inSep
	if err := svc.Delete(ctx, iso.ID); !errors.Is(err, core.ErrBusy) {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.Get(ctx, iso.ID); err != nil {
		t.Errorf("the isolation went: %v", err)
	}
}

// Code review 1102 M5 (PoC TestCR1102IsolationFailKeepsDecoded): a
// failed isolation kept its decoded input.
func TestIsolationFailDropsDecoded(t *testing.T) {
	svc, eng, sched, _ := newIsolateTest(t)
	eng.enhance = &jobs.Error{Code: "oom", Message: "out of memory"}
	in := filepath.Join(t.TempDir(), "Interview.wav")
	os.WriteFile(in, []byte("x"), 0o644)
	iso, _ := svc.Start(ctx, in)
	j, _ := sched.Get(ctx, iso.Job)
	if _, err := svc.Executor().Run(ctx, j, func(jobs.Progress) {}, nil); err == nil {
		t.Fatal("the isolation did not fail")
	}
	if _, err := os.Stat(svc.Path(iso.ID, IsolateAudio)); !os.IsNotExist(err) {
		t.Errorf("audio.wav stayed: %v", err)
	}
	if got, _ := svc.Get(ctx, iso.ID); got.State != IsolateFailed {
		t.Errorf("state %s", got.State)
	}
	// Sweep: a folder without its isolation.json goes.
	stray := filepath.Join(svc.cfg.Dir, "01STRAY")
	os.MkdirAll(stray, 0o755)
	os.WriteFile(filepath.Join(stray, IsolateVocals), []byte("x"), 0o644)
	past := time.Now().Add(-2 * time.Hour)
	os.Chtimes(stray, past, past)
	if n := svc.Sweep(ctx); n != 1 {
		t.Errorf("swept %d", n)
	}
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Error("the stray folder stayed")
	}
}
