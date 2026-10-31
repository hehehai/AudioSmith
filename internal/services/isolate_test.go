package services

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

// isoEngine answers an isolation's engine calls: it writes the stems.
type isoEngine struct {
	mu      sync.Mutex
	calls   []string
	enhance *jobs.Error // enhance.run fails with it
}

func (f *isoEngine) executor(method string) jobs.Executor {
	return jobs.ExecutorFunc(func(ctx context.Context, job jobs.Job, report func(jobs.Progress), _ func(json.RawMessage)) (json.RawMessage, error) {
		f.mu.Lock()
		f.calls = append(f.calls, method)
		f.mu.Unlock()
		write := func(path string) {
			wav.WriteFile(path, dsp.Buffer{Rate: 44100, Channels: 2, Samples: make([]float32, 44100)}, wav.PCM16, wav.Options{})
		}
		switch method {
		case proto.MethodMediaDecode:
			var p proto.DecodeParams
			json.Unmarshal(job.Params, &p)
			write(p.Output.Path)
			return json.Marshal(proto.DecodeResult{AudioOut: proto.AudioOut{Output: p.Output.Path, AudioSeconds: 3}})
		case proto.MethodSeparateRun:
			var p proto.SeparateParams
			json.Unmarshal(job.Params, &p)
			if p.ModelPath != "/snap/sep" {
				return nil, errors.New("no model path")
			}
			report(jobs.Progress{Stage: "separating", Fraction: 0.5})
			write(p.Vocals.Path)
			write(p.Background.Path)
			return json.Marshal(proto.SeparateResult{Vocals: p.Vocals.Path, Background: p.Background.Path, AudioSeconds: 3})
		case proto.MethodEnhanceRun:
			if f.enhance != nil {
				return nil, f.enhance
			}
			var p proto.EnhanceParams
			json.Unmarshal(job.Params, &p)
			write(p.Output.Path)
			return json.Marshal(proto.EnhanceResult{AudioOut: proto.AudioOut{Output: p.Output.Path}})
		}
		return nil, errors.New("unexpected " + method)
	})
}

func newIsolateTest(t *testing.T, missing ...string) (*Isolations, *isoEngine, *fakeScheduler, *[]IsolationChanged) {
	t.Helper()
	bus := core.NewBus()
	var evs []IsolationChanged
	var mu sync.Mutex
	core.On(bus, func(ev IsolationChanged) { mu.Lock(); evs = append(evs, ev); mu.Unlock() })
	eng := &isoEngine{}
	sched := &fakeScheduler{bus: bus, jobs: map[string]jobs.Job{}}
	svc := NewIsolations(IsolationsConfig{Dir: filepath.Join(t.TempDir(), "isolations"), Bus: bus, Scheduler: sched, Engine: eng.executor,
		Models: func() (string, string) { return "sep", "enh" },
		ModelPath: func(_ context.Context, repo string) (string, error) {
			if slices.Contains(missing, repo) {
				return "", core.ErrNotDownloaded
			}
			return "/snap/" + repo, nil
		}})
	return svc, eng, sched, &evs
}

func TestIsolateJob(t *testing.T) {
	svc, eng, sched, evs := newIsolateTest(t)
	in := filepath.Join(t.TempDir(), "Interview.mp4")
	os.WriteFile(in, []byte("video"), 0o644)
	iso, err := svc.Start(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if iso.Title != "Interview" || !iso.Video || iso.State != IsolateQueued || iso.Job == "" {
		t.Fatalf("started %+v", iso)
	}
	j, _ := sched.Get(ctx, iso.Job)
	if j.Kind != KindIsolate || j.Lane != jobs.LaneGPU || j.Owner != IsolateOwner+iso.ID {
		t.Fatalf("job %+v", j)
	}
	raw, err := svc.Executor().Run(ctx, j, func(jobs.Progress) {}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var done Isolation
	json.Unmarshal(raw, &done)
	if done.State != IsolateDone || done.Vocals != IsolateVocals || done.Background != IsolateBackground || done.Clean != IsolateClean || done.Duration != 3 {
		t.Fatalf("done %+v", done)
	}
	if !slices.Equal(eng.calls, []string{proto.MethodMediaDecode, proto.MethodSeparateRun, proto.MethodEnhanceRun}) {
		t.Errorf("calls %v", eng.calls)
	}
	for _, name := range []string{IsolateVocals, IsolateBackground, IsolateClean} {
		if _, err := os.Stat(svc.Path(iso.ID, name)); err != nil {
			t.Error(err)
		}
	}
	if _, err := os.Stat(svc.Path(iso.ID, IsolateAudio)); !os.IsNotExist(err) {
		t.Error("the decoded input is kept")
	}
	l, err := svc.List(ctx)
	if err != nil || len(l) != 1 || l[0].State != IsolateDone {
		t.Fatalf("list %+v %v", l, err)
	}
	if n := len(*evs); n < 3 {
		t.Errorf("%d events", n)
	}
	if err := svc.Delete(ctx, iso.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(svc.Dir(iso.ID)); !os.IsNotExist(err) {
		t.Error("folder kept")
	}
	if last := (*evs)[len(*evs)-1]; !last.Deleted {
		t.Errorf("last event %+v", last)
	}
	if l, _ := svc.List(ctx); len(l) != 0 {
		t.Errorf("list after delete %+v", l)
	}
}

// Without the denoiser the voices are kept as separated; without the
// separator nothing starts.
func TestIsolateMissingModels(t *testing.T) {
	svc, eng, sched, _ := newIsolateTest(t, "enh")
	in := filepath.Join(t.TempDir(), "song.wav")
	writeWAV(t, in, 48000, 0.5)
	if m := svc.Missing(ctx); !slices.Equal(m, []string{"enh"}) {
		t.Errorf("missing %v", m)
	}
	iso, err := svc.Start(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	j, _ := sched.Get(ctx, iso.Job)
	raw, err := svc.Executor().Run(ctx, j, func(jobs.Progress) {}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var done Isolation
	json.Unmarshal(raw, &done)
	if done.Clean != "" || !slices.Contains(done.Warnings, WarnNoDenoise) || done.Vocals == "" {
		t.Errorf("done %+v", done)
	}
	// A WAV is decoded in Go.
	if slices.Contains(eng.calls, proto.MethodMediaDecode) {
		t.Errorf("calls %v", eng.calls)
	}

	svc2, _, _, _ := newIsolateTest(t, "sep")
	if _, err := svc2.Start(ctx, in); !errors.Is(err, core.ErrNotDownloaded) {
		t.Errorf("no separator: %v", err)
	}
	if l, _ := svc2.List(ctx); len(l) != 0 {
		t.Errorf("made %+v", l)
	}
}

// A failure is saved with its reason.
func TestIsolateFails(t *testing.T) {
	svc, eng, sched, _ := newIsolateTest(t)
	eng.enhance = &jobs.Error{Code: proto.CodeInternal, Message: "boom"}
	in := filepath.Join(t.TempDir(), "a.wav")
	writeWAV(t, in, 44100, 0.5)
	iso, _ := svc.Start(ctx, in)
	j, _ := sched.Get(ctx, iso.Job)
	if _, err := svc.Executor().Run(ctx, j, func(jobs.Progress) {}, nil); err == nil {
		t.Fatal("no error")
	}
	got, _ := svc.Get(ctx, iso.ID)
	if got.State != IsolateFailed || got.Error == "" || got.ErrorCode != proto.CodeInternal {
		t.Errorf("%+v", got)
	}
}
