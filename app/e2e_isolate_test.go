package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/engine/enginetest"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// TestIsolateEngine runs the Voice isolator (0.5 F2) on the real engine:
// a clip of two macOS voices over music (about 20 s) is decoded,
// separated (HTDemucs) and its voices denoised (DeepFilterNet); the
// voices must be much closer to the speech than the mix is. It uses the
// models already in the shared cache and downloads none (it skips when
// they are missing). AUDIOSMITH_F2_DATA (an app's data folder) keeps the
// isolation in its isolations/ and seeds it for a look in the app: a few
// dubs and transcripts, Chinese, dictation and update checks off, the
// Local API on port 3951.
//
//	AUDIOSMITH_E2E=1 go test -run TestIsolateEngine -v .
func TestIsolateEngine(t *testing.T) {
	if os.Getenv("AUDIOSMITH_E2E") == "" {
		t.Skip("set AUDIOSMITH_E2E=1 to run the real engine")
	}
	bin := engine.Find("../resources")
	if bin == "" {
		t.Fatal("no engine: run scripts/build-engine.sh")
	}
	ctx := context.Background()
	client := hub.New(hub.Options{CacheDir: hub.CacheDir()})
	snap := map[string]string{}
	for _, e := range []catalog.Entry{isoSeparator(), isoDenoiser()} {
		l, err := client.Status(ctx, e)
		if err != nil || l.State != hub.Downloaded {
			t.Skipf("%s is not in the model cache (no download here): %v", e.Repo, err)
		}
		snap[e.Repo] = l.Snapshot
	}

	// The clip: two voices taking turns over music, as a WAV.
	dir := t.TempDir()
	const rate = 44100
	var speech []float64
	for i, l := range []struct{ voice, text string }{
		{"Samantha", "Welcome back to the show. Today we talk about night trains and the hills of Scotland."},
		{"Daniel", "Thanks for having me. I stayed up all night watching the hills in the moonlight."},
		{"Samantha", "That sounds wonderful. Let us listen to the next song."},
	} {
		aiff, wav := filepath.Join(dir, fmt.Sprintf("l%d.aiff", i)), filepath.Join(dir, fmt.Sprintf("l%d.wav", i))
		for _, c := range [][]string{{"say", "-v", l.voice, "-o", aiff, l.text}, {"afconvert", "-f", "WAVE", "-d", "LEI16@44100", "-c", "1", aiff, wav}} {
			if out, err := exec.Command(c[0], c[1:]...).CombinedOutput(); err != nil {
				t.Fatalf("%v: %v\n%s", c, err, out)
			}
		}
		a, err := enginetest.ReadWAV(wav)
		if err != nil {
			t.Fatal(err)
		}
		speech = append(speech, a.Samples...)
		speech = append(speech, make([]float64, rate/2)...)
	}
	music := synthMusic(len(speech), rate)
	mix := make([]float64, 2*len(speech))
	for i, v := range speech {
		mix[2*i], mix[2*i+1] = 0.9*v+music[2*i], 0.9*v+music[2*i+1]
	}
	src := filepath.Join(dir, "Night train (interview with music).wav")
	if err := enginetest.WriteWAV(src, enginetest.Audio{Rate: rate, Channels: 2, Samples: mix}, 16); err != nil {
		t.Fatal(err)
	}
	data := os.Getenv("AUDIOSMITH_F2_DATA")
	if data != "" {
		keep := filepath.Join(data, "Night train (interview with music).wav")
		os.MkdirAll(data, 0o755)
		if err := copyFile(src, keep); err != nil {
			t.Fatal(err)
		}
		src = keep
	}

	// The service over the real engine; the job is run here.
	sup := engine.NewSupervisor(engine.Config{Bin: bin, Bus: core.NewBus(), OnDemand: []proto.Role{proto.RoleMedia}, LogDir: dir, Offline: true,
		CacheDir: hub.CacheDir(), StatsEvery: -1, Logf: t.Logf})
	sup.Start(ctx)
	defer sup.Stop()
	isoDir := filepath.Join(dir, "isolations")
	if data != "" {
		isoDir = filepath.Join(data, "isolations")
	}
	fe := newFakeEngine() // its scheduler records the job; it runs here
	bus := core.NewBus()
	svc := services.NewIsolations(services.IsolationsConfig{Dir: isoDir, Bus: bus, Scheduler: fe.host.sched,
		Engine: func(method string) jobs.Executor { return engine.KindExecutor(sup, method) },
		Models: func() (string, string) { return isoSeparator().Repo, isoDenoiser().Repo },
		ModelPath: func(_ context.Context, repo string) (string, error) {
			if s := snap[repo]; s != "" {
				return s, nil
			}
			return "", fmt.Errorf("%s: %w", repo, core.ErrNotDownloaded)
		}})
	iso, err := svc.Start(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	job := fe.jobs[iso.Job]
	start := time.Now()
	raw, err := svc.Executor().Run(ctx, job, func(p jobs.Progress) {}, nil)
	took := time.Since(start)
	if err != nil {
		var je *jobs.Error
		errors.As(err, &je)
		t.Fatalf("isolate: %v %+v", err, je)
	}
	var done services.Isolation
	json.Unmarshal(raw, &done)
	t.Logf("isolated %.1f s in %.1f s (%.1f× real time): %+v", done.Duration, took.Seconds(), done.Duration/took.Seconds(), done)
	if done.State != services.IsolateDone || done.Clean == "" {
		t.Fatalf("%+v", done)
	}
	v, err := enginetest.ReadWAV(svc.Path(iso.ID, done.Vocals))
	if err != nil {
		t.Fatal(err)
	}
	mixMono := make([]float64, len(speech))
	for i := range mixMono {
		mixMono[i] = mix[2*i]
	}
	base, vs := siSDR(speech, mixMono), siSDR(speech, mono(v)[:min(len(speech), len(mono(v)))])
	t.Logf("voices vs speech %.1f dB (the mix: %.1f dB)", vs, base)
	if vs < base+6 {
		t.Errorf("separation too weak: %.1f dB vs %.1f", vs, base)
	}
	c, err := enginetest.ReadWAV(svc.Path(iso.ID, done.Clean))
	if err != nil || c.Rate == 0 || len(c.Samples) == 0 {
		t.Errorf("denoised: %v %d", err, len(c.Samples))
	}
	if data != "" {
		seedF2(t, data, src)
	}
}

// seedF2 seeds an app's data folder for a look at 0.5 F2's pages.
func seedF2(t *testing.T, data, src string) {
	ctx := context.Background()
	lib, err := openLibrary(data, filepath.Join(data, "cache"), libraryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer lib.close()
	off := false
	lib.settings.Update(ctx, func(s *settings.Settings) {
		s.General.Language, s.Setup.Done, s.Dictation.Enabled = "zh-Hans", true, false
		s.API.Enabled, s.API.Port, s.Updates.AutoCheck = true, 3951, &off
	})
	dubs := services.NewDubs(services.DubsConfig{Store: lib.store.Dubs(), Bus: lib.bus, Documents: lib.store.Documents()})
	for i, name := range []string{"夜航列车访谈", "产品发布会彩排", "播客第 12 期"} {
		p := newDubProject(dub.Source{Kind: dub.SourceFile, Path: src, Name: name + ".wav", Duration: 21}, lib.settings.Get().Dubbing, dubDefaultModel)
		p.Title = name
		if i == 1 {
			p.ChooseTargets([]string{"en"})
		}
		if _, err := dubs.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	for i, tr := range []core.Transcript{
		{ID: core.NewID(now.Add(-2 * time.Hour)), Title: "周会录音.m4a", Source: core.SourceFile, Mode: "accurate", Language: "zh", Duration: 1260,
			Segments: []core.TranscriptSegment{{Start: 0, End: 4, Text: "今天先过一下发布计划，然后看看配音的进度。"}}},
		{ID: core.NewID(now.Add(-time.Hour)), Title: "Customer interview.mp3", Source: core.SourceFile, Mode: "fast", Language: "en", Duration: 754,
			Segments: []core.TranscriptSegment{{Start: 0, End: 3, Text: "We mostly use it to dub our product videos."}}},
	} {
		tr.CreatedAt = now.Add(-time.Duration(2-i) * time.Hour)
		tr.UpdatedAt = tr.CreatedAt
		if err := lib.store.Transcripts().Put(ctx, tr); err != nil {
			t.Fatal(err)
		}
	}
}
