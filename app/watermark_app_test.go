package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/watermark"
)

// fakeWatermark records the watermark calls and answers them.
type fakeWatermark struct {
	embeds  []proto.WatermarkEmbedParams
	detects []proto.WatermarkDetectParams
}

func (f *fakeWatermark) executor(method string) jobs.Executor {
	return jobs.ExecutorFunc(func(_ context.Context, j jobs.Job, _ func(jobs.Progress), _ func(json.RawMessage)) (json.RawMessage, error) {
		if method == proto.MethodWatermarkEmbed {
			var p proto.WatermarkEmbedParams
			json.Unmarshal(j.Params, &p)
			f.embeds = append(f.embeds, p)
			return json.Marshal(proto.WatermarkEmbedResult{Output: p.Output.Path, Message: p.Message, SNR: 25})
		}
		var p proto.WatermarkDetectParams
		json.Unmarshal(j.Params, &p)
		f.detects = append(f.detects, p)
		return json.Marshal(proto.WatermarkDetectResult{Probability: 1, Detected: true, Message: watermark.Message(7), Bits: make([]float64, 16), Confidence: 0.9})
	})
}

func TestWatermarkWiring(t *testing.T) {
	a, _, tt := newTestApp(t)
	fw := &fakeWatermark{}
	a.wmEngine = fw.executor
	ctx := context.Background()
	e, _ := catalog.Builtin().ByID(catalog.DefaultWatermark)
	take := filepath.Join(t.TempDir(), "take.wav")
	writeTestWAV(t, take, 24000, 1)

	// Not downloaded: the take stays unmarked, and the app says so once.
	cache := modelCache(a.lib.settings.Get())
	os.RemoveAll(hub.RepoDir(cache, e.Repo))
	a.lib.resetHub()
	for range 2 {
		if ok, err := a.lib.watermarkRender(ctx, take, 16); ok || err != nil {
			t.Fatalf("without the model: %v %v", ok, err)
		}
	}
	n := 0
	for _, x := range a.notices {
		if x.Title == "Generated audio is not watermarked" {
			n++
		}
	}
	if n != 1 || len(fw.embeds) != 0 {
		t.Fatalf("notices %d, embeds %d", n, len(fw.embeds))
	}

	// Downloaded (and converted): marked with this install's message.
	snap := filepath.Join(hub.RepoDir(cache, e.Repo), "snapshots", e.Revision)
	os.MkdirAll(snap, 0o755)
	for _, f := range e.Files {
		fh, _ := os.Create(filepath.Join(snap, f.Path))
		fh.Truncate(f.Size)
		fh.Close()
	}
	conv := watermark.ConvertedDir(cache, e.Repo, e.Revision)
	os.MkdirAll(conv, 0o755)
	for _, out := range watermark.Files {
		os.WriteFile(filepath.Join(conv, out), []byte("converted"), 0o644)
	}
	a.lib.resetHub()
	ok, err := a.lib.watermarkRender(ctx, take, 24)
	id := a.lib.settings.Get().Privacy.WatermarkID
	if !ok || err != nil || len(fw.embeds) != 1 || id < 1 || id > 255 {
		t.Fatalf("marked %v %v, embeds %+v, id %d", ok, err, fw.embeds, id)
	}
	p := fw.embeds[0]
	if p.Input != take || p.Output.Path != take || p.Output.Bits != 24 || p.Message != watermark.Message(uint8(id)) || p.ModelPath != conv {
		t.Fatalf("params %+v", p)
	}

	// Off in Settings → Privacy.
	a.navigate("/settings/privacy")
	tt.Frame()
	if !tt.HasText("Add an invisible watermark to generated audio") || !a.wmOn {
		t.Fatalf("privacy page %q", tt.Texts())
	}
	a.updateSettings(func(s *settings.Settings) { s.Privacy.NoWatermark = true })
	if ok, _ := a.lib.watermarkRender(ctx, take, 24); ok || len(fw.embeds) != 1 {
		t.Fatal("marked while off")
	}

	// Tools → Watermark.
	a.navigate("/tools")
	a.toolTab = 5
	a.checkWatermark(take)
	tt.Frame()
	if !tt.HasText("Watermarked: 100% of the audio carries the mark.") || !tt.HasText("The mark is in AudioSmith's format (install number 7): the audio likely came from an AudioSmith. A mark can be imitated or removed, so this is not proof.") {
		t.Fatalf("tool %q", tt.Texts())
	}
}
