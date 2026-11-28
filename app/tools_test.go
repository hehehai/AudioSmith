package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/translate"
)

// lastKind is the last job of a kind the app submitted.
func lastKind(t *testing.T, eng *fakeEngine, kind string) sentReq {
	t.Helper()
	for i := len(eng.sent) - 1; i >= 0; i-- {
		if eng.sent[i].Spec.Kind == kind {
			return eng.sent[i]
		}
	}
	t.Fatalf("no %s job (sent %d)", kind, len(eng.sent))
	return sentReq{}
}

func TestToolsAB(t *testing.T) {
	a, eng, tt := newTestApp(t)
	a.navigate("/tools")
	tt.Frame()
	if a.ab.a == "" || a.ab.b == "" || a.ab.a == a.ab.b {
		t.Fatalf("voices %q %q", a.ab.a, a.ab.b)
	}
	if err := tt.Click("Compare"); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range eng.sent {
		if r.Spec.Kind == services.KindRender && strings.HasPrefix(r.Spec.Owner, "preview:") {
			ids = append(ids, r.ID)
		}
	}
	if len(ids) != 2 || !a.ab.runs[0].busy {
		t.Fatalf("previews %v", ids)
	}
	eng.succeed(ids[0], services.RenderResult{Output: a.ab.runs[0].output})
	eng.fail(ids[1], "oom", "out of memory")
	tt.Frame()
	if a.ab.runs[0].busy || a.ab.runs[0].err != "" || a.ab.runs[1].err == "" || a.ab.runs[1].output != "" {
		t.Fatalf("runs %+v %+v", a.ab.runs[0], a.ab.runs[1])
	}
	r := lastKind(t, eng, services.KindRender)
	if r.Voice == "" {
		t.Fatalf("no speaker: %+v", r.SynthesizeParams)
	}
}

func TestToolsConvert(t *testing.T) {
	simulateDarwin(t)
	a, eng, tt := newTestApp(t)
	a.navigate("/tools")
	a.toolTab = 1
	tt.Frame()
	src := filepath.Join(t.TempDir(), "memo.wav")
	writeTestWAV(t, src, 24000, 3)
	a.setConvertFile(src)
	tt.Frame()
	if a.cv.duration != 3 || !tt.HasText("memo.wav") {
		t.Fatalf("file %+v", a.cv)
	}
	a.cv.match = true
	a.runConvert()
	tr := lastKind(t, eng, services.KindTranscribe)
	tx := core.Transcript{ID: "x", Duration: 3, Segments: []core.TranscriptSegment{{Start: 0, End: 3, Text: "Meet me at noon."}}}
	eng.succeed(tr.ID, services.TranscribeResult{Transcript: tx})
	tt.Frame()
	if a.cv.heard != "Meet me at noon." || !tt.HasText("Meet me at noon.") {
		t.Fatalf("heard %q", a.cv.heard)
	}
	gen := lastKind(t, eng, services.KindRender)
	if !strings.HasPrefix(gen.Spec.Owner, "take:") || gen.Text != "Meet me at noon." {
		t.Fatalf("generate %+v", gen)
	}
	if gen.Duration != nil {
		t.Fatal("Kokoro got a target duration")
	}
	// Kokoro cannot aim at a length: its take is stretched to 3 s.
	out := filepath.Join(t.TempDir(), "natural.wav")
	writeTestWAV(t, out, 24000, 2.5)
	eng.succeed(gen.ID, services.RenderResult{Output: out, AudioSeconds: 2.5, SampleRate: 24000})
	tt.Frame()
	if a.cv.take == nil || a.cv.stage != "" || a.cv.err != "" {
		t.Fatalf("take %+v", a.cv)
	}
	if d := a.cv.take.AudioSeconds; d < 2.99 || d > 3.01 {
		t.Fatalf("matched take is %.3f s", d)
	}
	if _, err := a.lib.svc.Takes.Get(context.Background(), a.cv.take.ID); err != nil {
		t.Fatalf("the matched take is not in the History: %v", err)
	}
}

func TestToolsRateFitDirectionProbe(t *testing.T) {
	simulateDarwin(t)
	a, eng, tt := newTestApp(t)
	a.navigate("/tools")
	a.toolTab = 2
	tt.Frame()
	a.rf.text = "This sentence is much too long to say in a single second."
	a.rf.slot = 1
	a.runRateFit()
	tt.Frame()
	if a.rf.res == nil || a.rf.res.Fit.Verdict != translate.FitOverflow || !tt.HasText("Too long") {
		t.Fatalf("fit %+v %q", a.rf.res, a.rf.err)
	}

	a.toolTab = 3
	a.dir.note = "more urgent, a little scared"
	a.runDirection()
	tt.Frame()
	if a.dir.res == nil || a.dir.res.Instruct == "" || len(a.dir.res.Emotions) == 0 {
		t.Fatalf("direction %+v", a.dir.res)
	}
	a.previewDirection()
	r := lastKind(t, eng, services.KindRender)
	e := directionModel(a.preset())
	if r.Model != e.Repo || (e.Has(catalog.CapVoiceStyle) && !strings.Contains(r.Voice, ",")) {
		t.Fatalf("preview %+v", r.SynthesizeParams)
	}

	a.toolTab = 4
	path := filepath.Join(t.TempDir(), "clip.wav")
	writeTestWAV(t, path, 24000, 1)
	a.runProbe(path)
	tt.Frame()
	if a.prb.out == "" || !strings.Contains(a.prb.out, `"sampleRate"`) || !tt.HasText(`"hasAudio": true`) {
		t.Fatalf("probe %q %q", a.prb.out, a.prb.err)
	}
}
