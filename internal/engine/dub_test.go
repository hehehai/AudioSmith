package engine

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hehehai/AudioSmith/internal/engine/enginetest"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

// The M4 (dubbing) job kinds over the supervisor and the fake engine.
func TestM4Executors(t *testing.T) {
	sup, w := newTestSup(t, "", allRoles)
	w.until(t, "ready", ready)
	ctx := context.Background()
	dir := t.TempDir()
	run := func(kind string, params, out any) *jobs.Error {
		t.Helper()
		raw, _ := json.Marshal(params)
		res, err := KindExecutor(sup, kind).Run(ctx, jobs.Job{ID: "J" + kind, Spec: jobs.Spec{Params: raw}}, func(jobs.Progress) {}, nil)
		var je *jobs.Error
		if errors.As(err, &je) {
			return je
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(res, out); err != nil {
			t.Fatal(err)
		}
		return nil
	}
	wav := speechWAV(t, dir)

	var sr proto.SeparateResult
	v, b := filepath.Join(dir, "v.wav"), filepath.Join(dir, "b.wav")
	if e := run(proto.MethodSeparateRun, proto.SeparateParams{Model: "iky1e/demucs-mlx", Audio: wav, Vocals: proto.Output{Path: v}, Background: proto.Output{Path: b}}, &sr); e != nil {
		t.Fatal(e)
	}
	if a, err := enginetest.ReadWAV(b); err != nil || sr.SampleRate != 44100 || sr.Channels != 2 || sr.Vocals != v || int64(a.Frames()) != sr.Frames {
		t.Fatalf("separate %+v %v", sr, err)
	}
	var one proto.SeparateResult
	if e := run(proto.MethodSeparateRun, proto.SeparateParams{Model: "iky1e/demucs-mlx", Audio: wav, Vocals: proto.Output{Path: v}}, &one); e != nil || one.Background != "" {
		t.Fatalf("one stem: %+v %v", one, e)
	}

	var dr proto.DiarizeResult
	if e := run(proto.MethodDiarizeRun, proto.DiarizeParams{Model: "sortformer", Audio: wav}, &dr); e != nil || dr.Speakers != 2 || len(dr.Turns) != 2 || dr.Turns[1].Speaker != "1" {
		t.Fatalf("diarize %+v %v", dr, e)
	}
	if e := run(proto.MethodDiarizeRun, proto.DiarizeParams{Model: "sortformer", Audio: wav, Speakers: 6}, &dr); e == nil || e.Code != proto.CodeUnsupported || !strings.Contains(string(e.Data), `"max":4`) {
		t.Errorf("6 speakers: %+v", e)
	}

	mov := filepath.Join(dir, "clip.mov")
	os.WriteFile(mov, []byte("video"), 0o644)
	srt := filepath.Join(dir, "s.srt")
	os.WriteFile(srt, []byte("1\n00:00:00,000 --> 00:00:01,000\nHi\n"), 0o644)
	var mr proto.MuxResult
	out := filepath.Join(dir, "out.mp4")
	if e := run(proto.MethodMediaMux, proto.MuxParams{Video: mov, KeepAudio: true, Audio: []proto.MuxTrack{{Path: wav, Language: "es", Default: true}},
		Subtitles: []proto.MuxSubtitle{{Path: srt, Language: "es"}}, Output: proto.Output{Path: out}}, &mr); e != nil || mr.Tracks != 2 {
		t.Fatalf("mux %+v %v", mr, e)
	}
	if e := run(proto.MethodMediaMux, proto.MuxParams{Video: wav, Audio: []proto.MuxTrack{{Path: wav}}, Output: proto.Output{Path: out}}, &mr); e == nil || e.Code != proto.CodeUnsupportedMedia {
		t.Errorf("mux of audio: %+v", e)
	}
	if e := run(proto.MethodMediaMux, proto.MuxParams{Video: mov, Retime: []proto.Retime{{Start: 1, End: 2, Factor: 1.2}}, Output: proto.Output{Path: out}}, &mr); e == nil || e.Code != proto.CodeUnsupported {
		t.Errorf("retime: %+v", e)
	}

	var tr proto.ThumbnailResult
	poster := filepath.Join(dir, "poster.jpg")
	if e := run(proto.MethodMediaThumbnail, proto.ThumbnailParams{Input: mov, Times: []float64{1}, Outputs: []string{poster}, Width: 320}, &tr); e != nil || tr.Frames[0].Height != 180 {
		t.Fatalf("thumbnail %+v %v", tr, e)
	}
	if f, err := os.Open(poster); err != nil {
		t.Fatal(err)
	} else {
		defer f.Close()
		if cfg, _, err := image.DecodeConfig(f); err != nil || cfg.Width != 320 {
			t.Errorf("poster %+v %v", cfg, err)
		}
	}

	var tc proto.TranscodeResult
	if e := run(proto.MethodMediaTranscode, proto.TranscodeParams{Input: mov, Output: proto.Output{Path: filepath.Join(dir, "p.mp4")}}, &tc); e != nil || tc.Height != 720 || tc.Width != 1280 {
		t.Fatalf("transcode %+v %v", tc, e)
	}
	webm := filepath.Join(dir, "clip.webm")
	os.WriteFile(webm, []byte("x"), 0o644)
	if e := run(proto.MethodMediaTranscode, proto.TranscodeParams{Input: webm, Output: proto.Output{Path: filepath.Join(dir, "q.mp4")}}, &tc); e == nil || e.Code != proto.CodeUnsupportedMedia {
		t.Errorf("webm: %+v", e)
	}
}
