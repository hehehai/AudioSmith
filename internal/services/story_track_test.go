package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp/dsptest"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/text"
)

// timedWords times the characters (or words) of sentences said one after the
// other with a pause between them.
func timedWords(start, pause float64, sentences ...string) []proto.Word {
	var out []proto.Word
	at := start
	for _, s := range sentences {
		var units []string
		if cjkShare(s) >= 0.3 {
			for _, r := range s {
				if alignUnits(string(r)) != "" {
					units = append(units, string(r))
				}
			}
		} else {
			units = strings.Fields(s)
		}
		w, c := text.Count(s)
		d := text.Estimate(w, c).Seconds() / float64(len(units))
		for _, u := range units {
			out = append(out, proto.Word{Text: strings.Trim(u, ".,?!"), Start: at, End: at + d})
			at += d
		}
		at += pause
	}
	return out
}

// fakeAnalysis is the engine's stt.align (the fake synth's timing) and
// stt.transcribe (heard).
type fakeAnalysis struct {
	mu    sync.Mutex
	calls []string
	heard func(audio string) string
}

func (f *fakeAnalysis) executor(method string) jobs.Executor {
	return jobs.ExecutorFunc(func(ctx context.Context, job jobs.Job, _ func(jobs.Progress), _ func(json.RawMessage)) (json.RawMessage, error) {
		f.mu.Lock()
		f.calls = append(f.calls, method)
		f.mu.Unlock()
		switch method {
		case proto.MethodSTTAlign:
			var p proto.AlignParams
			json.Unmarshal(job.Params, &p)
			if p.Model != "m/aligner" || p.ModelPath != "/models/m/aligner" {
				return nil, &jobs.Error{Code: proto.CodeBadRequest, Message: "model " + p.Model}
			}
			return json.Marshal(proto.AlignResult{Words: timedWords(0.1, 0.35, fakeSentences(p.Text)...)})
		case proto.MethodSTTTranscribe:
			var p proto.TranscribeParams
			json.Unmarshal(job.Params, &p)
			return json.Marshal(proto.TranscribeResult{Text: f.heard(p.Audio)})
		}
		return nil, &jobs.Error{Code: proto.CodeBadRequest, Message: method}
	})
}

func (f *fakeAnalysis) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == method {
			n++
		}
	}
	return n
}

func TestTrackGroups(t *testing.T) {
	l := func(who, text, emotion string, intensity int, turn bool) LineSpec {
		return LineSpec{Character: who, Emotion: emotion, Intensity: intensity, Turn: turn, Spans: []SpanSpec{{Voice: who, Character: who, Text: text}}}
	}
	qwen := ModelInfo{Traits: TraitsFor("qwen3_tts", nil)}
	lines := []LineSpec{
		l("kid", "我想养猫。", "pleading", 1, false), l("mom", "你能照顾它吗？", "worried", 1, false), l("kid", "我能！", "excited", 1, false),
		l("", "妈妈笑了。", "steady", 0, false), l("mom", "好吧。", "tender", 1, false), l("kid", "太好了！", "excited", 3, true),
		l("kid", "谢谢妈妈！", "happy", 2, false), l("mom", "去吧。", "tender", 1, false), l("dad", "嗯。", "steady", 0, false),
		l("kid", "可是我好难过。", "sad", 2, false), l("kid", "真的好难过。", "hurt", 3, false),
	}
	// The kid's calm lines are one stretch; the turn (line 6) starts a bright one,
	// the sad lines another; mom's three; dad and the narrator have none.
	if got := fmt.Sprint(trackGroups(lines, qwen)); got != "[[0 2] [1 4 7] [5 6] [9 10]]" {
		t.Errorf("groups %s", got)
	}
	// Lines without emotions (stories directed before them) are never tracked.
	plain := []LineSpec{l("kid", "我想养猫。", "", 0, false), l("kid", "我能！", "", 0, false)}
	if g := trackGroups(plain, qwen); len(g) != 0 {
		t.Errorf("plain lines tracked: %v", g)
	}
	// The length limit (about 35 s).
	var long []LineSpec
	for range 12 {
		long = append(long, l("kid", strings.Repeat("长", 30)+"。", "steady", 0, false))
	}
	for _, g := range trackGroups(long, qwen) {
		if len(g)*30 > trackMaxCJK {
			t.Errorf("group of %d lines", len(g))
		}
	}
	// Kokoro's shorter chunks make shorter stretches.
	for _, g := range trackGroups(long, kokoroInfo("")) {
		if len(g)*30 > 80 {
			t.Errorf("kokoro group of %d lines", len(g))
		}
	}
	if e, i := trackFeeling([]LineSpec{{Baseline: EmotionTender, Emotion: EmotionSteady}, {Baseline: EmotionTender, Emotion: EmotionWorried, Intensity: 1}}); e != EmotionTender || i != 1 {
		t.Errorf("calm %s %d", e, i)
	}
	// The baseline's family is calm: an excited child's happy line goes on in the stretch, which says it a little stronger.
	bl := []LineSpec{{Baseline: EmotionExcited, Emotion: EmotionHappy, Intensity: 3}, {Baseline: EmotionExcited, Emotion: EmotionExcited, Intensity: 1}}
	if trackKey(bl[0]) != "" {
		t.Error("the baseline's family breaks a stretch")
	}
	if e, i := trackFeeling(bl); e != EmotionExcited || i != 2 {
		t.Errorf("baseline family %s %d", e, i)
	}
	if e, i := trackFeeling([]LineSpec{{Emotion: EmotionSad, Intensity: 2}, {Emotion: EmotionHurt, Intensity: 3}}); e != EmotionHurt || i != 3 {
		t.Errorf("strong %s %d", e, i)
	}
}

func TestTrackRanges(t *testing.T) {
	parts := []string{"我想养猫。", "我能！", "谢谢妈妈！"}
	w := timedWords(0.1, 0.35, parts...)
	rg, err := trackRanges(w, parts)
	if err != nil || len(rg) != 3 || rg[1][0] <= rg[0][1] {
		t.Fatalf("ranges %v %v", rg, err)
	}
	if _, err := trackRanges(w, []string{"我想养猫。", "我能！", "谢谢爸爸！"}); err != errCutWords {
		t.Errorf("words: %v", err)
	}
	// A pause inside a line ("……") is not speech: the line is not "too long".
	hes := []string{"不是……公司临时让我明天去外地出差。", "对不起。"}
	w = timedWords(0.1, 0.35, hes...)
	for i := 2; i < len(w)-3; i++ {
		w[i].Start, w[i].End = w[i].Start+1.5, w[i].End+1.5
	}
	for i := len(w) - 3; i < len(w); i++ {
		w[i].Start, w[i].End = w[i].Start+1.5, w[i].End+1.5
	}
	if _, err := trackRanges(w, hes); err != nil {
		t.Errorf("a hesitation fails the cut: %v", err)
	}
	// A line the model rushed through (a tenth of its share) fails.
	w = timedWords(0.1, 0.35, parts...)
	for i := 4; i < 6; i++ {
		w[i].End = w[i].Start + 0.01
	}
	if _, err := trackRanges(w, parts); !errors.Is(err, errCutLength) {
		t.Errorf("length: %v", err)
	}
	b := dsptest.Concat(dsptest.Sine(24000, 1, 220, -12, 1, 0), dsptest.Silence(24000, 1, 0.2), dsptest.Sine(24000, 1, 220, -12, 1, 0))
	if at := float64(quietSplit(b, 1.05, 1.15)) / 24000; at < 1.0 || at > 1.2 {
		t.Errorf("split at %.3f", at)
	}
}

// A character's lines are said in one go and cut back into the
// dialogue; a track the recognizer does not hear is made line by line;
// editing one line makes only its stretch again; a story without
// emotions is made line by line, as before.
func TestTrackRender(t *testing.T) {
	e := newLongformEnv(t, t.TempDir(), false)
	defer e.close()
	e.synth.pauses = true
	var heard []string
	fa := &fakeAnalysis{heard: func(string) string {
		if len(heard) == 0 {
			return ""
		}
		h := heard[0]
		heard = heard[1:]
		return h
	}}
	r := e.svc.Longform.render
	r.engine, r.aligner, r.recognizer = fa.executor, "m/aligner", "m/asr"
	line := func(id, who, text string) LineSpec {
		v := strings.ToLower(who)
		return LineSpec{ID: id, Character: who, Emotion: EmotionSteady, Spans: []SpanSpec{{Voice: v, Character: who, Text: text}}}
	}
	spec := LongformSpec{Kind: core.ProjectStory, Title: "Kitten", Model: "m/kokoro", Lang: "en", Seed: 7,
		Voices: map[string]core.Take{"kid": {Voice: "af_bella"}, "mom": {Voice: "af_heart"}},
		Chapters: []ChapterSpec{{Lines: []LineSpec{
			line("a", "Kid", "Can we get one today?"), line("b", "Mom", "Will you look after it every day?"),
			line("c", "Kid", "Yes, I promise."), line("d", "Mom", "All right"),
		}}},
		Production: DefaultStoryProduction(), Audition: true}
	run := func() ChapterResult {
		t.Helper()
		rr, err := e.svc.Longform.Start(ctx, spec)
		if err != nil {
			t.Fatal(err)
		}
		_, body := e.wait(t, rr.ID)
		if st := body.Chapters[0].State; st != "done" && st != "cached" {
			t.Fatalf("chapter %+v", body.Chapters[0])
		}
		res, err := ReadChapter(body.Chapters[0].File)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	heard = []string{"Can we get one today?", "Yes, I promise.", "Will you look after it every day?", "All right."}
	res := run()
	if got := strings.Join(e.synth.texts(), " | "); got != "Can we get one today? Yes, I promise. | Will you look after it every day? All right." {
		t.Fatalf("calls %q", got)
	}
	if res.Tracks != 2 || res.TrackLines != 4 || res.TracksFailed != 0 || fa.count(proto.MethodSTTTranscribe) != 4 {
		t.Fatalf("result %+v", res)
	}
	// Lines in dialogue order, each its own length; the sidecar keeps the text.
	for i := 1; i < 4; i++ {
		if res.Lines[i].Start <= res.Lines[i-1].End {
			t.Errorf("lines %+v", res.Lines)
		}
	}
	w, c := text.Count("Yes, I promise.")
	if d := res.Lines[2].End - res.Lines[2].Start; d > text.Estimate(w, c).Seconds()+0.15 {
		t.Errorf("line c lasts %.3f s", d)
	}
	if res.Lines[3].Spans[0].Text != "All right" {
		t.Errorf("text %q", res.Lines[3].Spans[0].Text)
	}

	// Editing one line makes only its stretch again.
	e.synth.reset()
	spec.Chapters[0].Lines[3].Spans[0].Text = "All right, then"
	heard = []string{"Will you look after it every day?", "All right, then."}
	res = run()
	if got := strings.Join(e.synth.texts(), " | "); got != "Will you look after it every day? All right, then." || res.Tracks != 2 {
		t.Errorf("after an edit: calls %q, %+v", got, res)
	}

	// The recognizer hears the kid's second line wrong: that track is made line by line.
	os.RemoveAll(filepath.Join(e.dir, "longform-chunks")) // the longform chunks (data folder, audit A7)
	e.synth.reset()
	heard = []string{"Can we get one today?", "Yes.", "Will you look after it every day?", "All right, then."}
	res = run()
	if res.Tracks != 1 || res.TracksFailed != 1 {
		t.Fatalf("failed track %+v", res)
	}
	if got := strings.Join(e.synth.texts(), " | "); got != "Can we get one today? Yes, I promise. | Can we get one today? | Will you look after it every day? All right, then. | Yes, I promise." {
		t.Errorf("calls %q", got)
	}
	// The failure is remembered: no new alignment.
	aligned := fa.count(proto.MethodSTTAlign)
	os.Remove(res.Output)
	if res = run(); fa.count(proto.MethodSTTAlign) != aligned || res.TracksFailed != 1 {
		t.Errorf("failure retried: %+v", res)
	}

	// NoTracks, or lines without emotions: line by line.
	for _, f := range []func(){
		func() { spec.Production.NoTracks = true },
		func() {
			spec.Production.NoTracks = false
			for i := range spec.Chapters[0].Lines {
				spec.Chapters[0].Lines[i].Emotion = ""
			}
		},
	} {
		f()
		os.RemoveAll(filepath.Join(e.dir, "longform-chunks")) // the longform chunks (data folder, audit A7)
		e.synth.reset()
		if res = run(); res.Tracks+res.TracksFailed != 0 || len(e.synth.texts()) != 4 {
			t.Errorf("line by line: %+v %q", res, e.synth.texts())
		}
	}
}
