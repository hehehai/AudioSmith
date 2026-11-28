package services

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dialogue"
	"github.com/hehehai/AudioSmith/internal/dsp/dsptest"
	"github.com/hehehai/AudioSmith/internal/dsp/loudness"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/text"
)

// fakeDialogue is the engine's tts.dialogue (each generated turn a tone as
// long as its text) and stt.transcribe (what garble says is heard).
type fakeDialogue struct {
	mu     sync.Mutex
	calls  []proto.DialogueParams
	said   map[string]string // file → its text
	garble func(call int, text string) bool
	fail   bool
	asr    int
}

func (f *fakeDialogue) executor(method string) jobs.Executor {
	return jobs.ExecutorFunc(func(ctx context.Context, job jobs.Job, report func(jobs.Progress), _ func(json.RawMessage)) (json.RawMessage, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch method {
		case proto.MethodTTSDialogue:
			var p proto.DialogueParams
			json.Unmarshal(job.Params, &p)
			f.calls = append(f.calls, p)
			if f.fail {
				return nil, &jobs.Error{Code: proto.CodeInternal, Message: "Metal GPU timeout"}
			}
			if p.Model != "m/soulx" || p.ModelPath != "/models/m/soulx" {
				return nil, &jobs.Error{Code: proto.CodeBadRequest, Message: "model " + p.Model + " " + p.ModelPath}
			}
			for _, s := range p.Speakers {
				if _, err := os.Stat(s.RefAudio); err != nil || s.RefText == "" {
					return nil, &jobs.Error{Code: proto.CodeFileNotFound, Message: s.RefAudio}
				}
			}
			report(jobs.Progress{Stage: "generating", Fraction: 0.5})
			os.MkdirAll(p.OutputDir, 0o755)
			res := proto.DialogueResult{Model: p.Model, SampleRate: 24000, Channels: 1, Turns: []proto.DialogueTurnOut{}}
			for i, t := range p.Turns {
				if len(t.Codes) > 0 {
					continue
				}
				w, c := text.Count(t.Text)
				out := filepath.Join(p.OutputDir, fmt.Sprintf("turn-%03d.wav", i))
				buf := dsptest.Concat(dsptest.Silence(24000, 1, 0.05), dsptest.Sine(24000, 1, 200, -14, text.Estimate(w, c).Seconds(), 0))
				wav.WriteFile(out, buf, wav.Float32, wav.Options{})
				f.said[out] = stripTags(t.Text)
				if f.garble != nil && f.garble(len(f.calls), t.Text) {
					f.said[out] = "嗯嗯啊啊"
				}
				res.Turns = append(res.Turns, proto.DialogueTurnOut{Index: i, Speaker: t.Speaker, Output: out, Codes: []int{i, len(f.calls)}})
			}
			return json.Marshal(res)
		case proto.MethodSTTTranscribe:
			var p proto.TranscribeParams
			json.Unmarshal(job.Params, &p)
			f.asr++
			return json.Marshal(proto.TranscribeResult{Text: f.said[p.Audio]})
		}
		return nil, &jobs.Error{Code: proto.CodeBadRequest, Message: method}
	})
}

func TestDialogueGroups(t *testing.T) {
	l := func(who, voice, text string) LineSpec {
		return LineSpec{Character: who, Spans: []SpanSpec{{Voice: voice, Character: who, Text: text}}}
	}
	lines := []LineSpec{
		l("", "n", "那天晚上，一家人吃完饭坐在客厅里，窗外下着小雨，屋里很安静。"),                                 // long narration: not in a segment
		l("kid", "kid", "妈妈，我想养猫。"), l("", "n", "她小声说。"), l("mom", "mom", "你能照顾它吗？"), // a short aside keeps the segment
		l("kid", "kid", "我能！"),
		l("", "n", "第二天早上，太阳升起来了，孩子背着书包高高兴兴地去了学校，一路上都在想小猫。"), // ends it
		l("a", "a", "一。"), l("b", "b", "二。"), l("c", "c", "三。"), l("d", "d", "四。"), l("e", "e", "五。"), // a fifth voice starts another
		l("kid", "kid", "[pause 500ms] 嗯。"), // pause markup: line by line
	}
	if got := fmt.Sprint(dialogueGroups(lines, dialogue.LimitsFor("soulx_podcast"))); got != "[[1 3 4] [6 7 8 9] [10]]" {
		t.Errorf("groups %s", got)
	}
	var many []LineSpec
	for i := range 20 {
		many = append(many, l("kid", "kid", fmt.Sprintf("第%d句。", i)))
	}
	if g := dialogueGroups(many, dialogue.LimitsFor("soulx_podcast")); len(g) != 2 || len(g[0]) != 16 {
		t.Errorf("turn limit: %v", g)
	}
	if dialogueTag(LineSpec{Emotion: EmotionHappy, Intensity: 2}) != "<|laughter|>" || dialogueTag(LineSpec{Emotion: EmotionSad, Intensity: 1}) != "" {
		t.Error("tags")
	}
}

// The characters' lines are spoken by the dialogue model in one job, with
// portable references made on the story's model; narration stays there.
// A line the recognizer does not hear is made again with the earlier lines
// as context; one that fails twice, and a whole segment the model fails,
// are made on the story's model. Everything is cached.
func TestDialogueRender(t *testing.T) {
	e := newLongformEnv(t, t.TempDir(), false)
	defer e.close()
	fd := &fakeDialogue{said: map[string]string{}}
	r := e.svc.Longform.render
	r.engine, r.recognizer = fd.executor, "m/asr"
	clone := filepath.Join(e.dir, "mom.wav")
	wav.WriteFile(clone, dsptest.Sine(24000, 1, 300, -14, 6, 0), wav.Float32, wav.Options{})
	line := func(id, who, text string) LineSpec {
		v := strings.ToLower(who)
		if who == "" {
			v = "narrator"
		}
		return LineSpec{ID: id, Character: who, Emotion: EmotionSteady, Spans: []SpanSpec{{Voice: v, Character: who, Text: text}}}
	}
	prod := DefaultStoryProduction()
	prod.Dialogue, prod.DialogueModel = true, "m/soulx"
	spec := LongformSpec{Kind: core.ProjectStory, Title: "小猫", Model: "m/kokoro", Lang: "zh", Seed: 7,
		Voices: map[string]core.Take{"kid": {Voice: "zf_xiaobei"}, "mom": {RefAudio: clone, RefText: "今天晚上我们早点吃饭。"}, "narrator": {Voice: "zm_yunxi"}},
		Chapters: []ChapterSpec{{Lines: []LineSpec{
			line("n", "", "那天晚上，一家人吃完饭坐在客厅里，窗外下着小雨。"),
			line("a", "Kid", "妈妈，我想领养一只小猫，可以吗？"), line("b", "Mom", "养猫要花很多时间照顾它。"),
			line("c", "Kid", "我能！我每天喂它。"), line("d", "Mom", "那我们先去救助站看看吧。"),
		}}},
		Production: prod, Audition: true}
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
	// Line c is heard wrong the first time: made again with a and b as context.
	fd.garble = func(call int, text string) bool { return call == 1 && strings.HasPrefix(text, "我能") }
	res := run()
	if len(fd.calls) != 2 || res.Dialogues != 1 || res.DialogueLines != 4 || res.DialogueRedone != 1 || res.DialogueFailed != 0 {
		t.Fatalf("calls %d, result %+v", len(fd.calls), res)
	}
	first, redo := fd.calls[0], fd.calls[1]
	if len(first.Speakers) != 2 || !strings.Contains(filepath.ToSlash(first.Speakers[1].RefAudio), "/longform-chunks/ref-") || first.Speakers[0].RefText != PortableLine("zh") || len(first.Turns) != 4 {
		t.Errorf("first call %+v", first)
	}
	// The clone's clip is tidied: evened out to −18 LUFS like the others.
	for _, sp := range first.Speakers {
		if b, _, err := wav.ReadFile(sp.RefAudio); err != nil || math.Abs(loudness.Integrated(b)-(-18)) > 0.3 {
			t.Errorf("reference %s: %v", sp.RefAudio, err)
		}
	}
	if len(redo.Turns) != 3 || len(redo.Turns[0].Codes) == 0 || len(redo.Turns[1].Codes) == 0 || len(redo.Turns[2].Codes) != 0 || *redo.Seed == *first.Seed {
		t.Errorf("redo %+v", redo)
	}
	// The story's model said the kid's reference and the narration only.
	if got := strings.Join(e.synth.texts(), " | "); got != "那天晚上，一家人吃完饭坐在客厅里，窗外下着小雨。 | "+PortableLine("zh") {
		t.Errorf("synth %q", got)
	}
	for i := 1; i < 5; i++ {
		if res.Lines[i].Start <= res.Lines[i-1].End {
			t.Errorf("lines %+v", res.Lines)
		}
	}
	// Cached: nothing new.
	os.Remove(res.Output)
	e.synth.reset()
	if res = run(); len(fd.calls) != 2 || len(e.synth.texts()) != 0 {
		t.Errorf("not cached: %d calls, synth %q", len(fd.calls), e.synth.texts())
	}

	// Line d is never heard: made on the story's model.
	os.RemoveAll(filepath.Join(e.dir, "longform-chunks")) // the longform chunks (data folder, audit A7)
	e.synth.reset()
	fd.calls = nil
	fd.garble = func(_ int, text string) bool { return strings.HasPrefix(text, "那我们") }
	res = run()
	if res.DialogueFailed != 1 || res.DialogueLines != 3 || res.DialogueRedone != 1 || !strings.Contains(strings.Join(e.synth.texts(), "|"), "那我们先去救助站看看吧。") {
		t.Errorf("failed line: %+v, synth %q", res, e.synth.texts())
	}

	// The dialogue model fails: the segment's lines are made on the story's model.
	os.RemoveAll(filepath.Join(e.dir, "longform-chunks")) // the longform chunks (data folder, audit A7)
	e.synth.reset()
	fd.calls, fd.garble, fd.fail = nil, nil, true
	res = run()
	if res.DialogueFailed != 4 || res.Dialogues != 0 || len(e.synth.texts()) != 6 {
		t.Errorf("failed segment: %+v, synth %q", res, e.synth.texts())
	}

	// A designed voice spoken by a model that designs it brings its own
	// reference (Take.CloneRef): nothing to make on the story's model.
	os.RemoveAll(filepath.Join(e.dir, "longform-chunks")) // the longform chunks (data folder, audit A7)
	e.synth.reset()
	fd.calls, fd.fail = nil, false
	kidRef := filepath.Join(e.dir, "kid-reference.wav")
	wav.WriteFile(kidRef, dsptest.Sine(24000, 1, 400, -10, 11, 0), wav.Float32, wav.Options{})
	spec.Voices["kid"] = core.Take{Voice: "zf_xiaobei", CloneRef: kidRef, CloneRefText: "今天放学的时候，我看见一只小鸟。"}
	res = run()
	if len(fd.calls) != 1 || fd.calls[0].Speakers[0].RefText != "今天放学的时候，我看见一只小鸟。" || slices.Contains(e.synth.texts(), PortableLine("zh")) {
		t.Errorf("designed reference: %+v, synth %q", fd.calls, e.synth.texts())
	}

	// Off: as before.
	os.RemoveAll(filepath.Join(e.dir, "longform-chunks")) // the longform chunks (data folder, audit A7)
	e.synth.reset()
	fd.calls, fd.fail = nil, false
	spec.Production.Dialogue = false
	if res = run(); len(fd.calls) != 0 || res.Dialogues+res.DialogueFailed != 0 {
		t.Errorf("dialogue off: %+v", res)
	}
}
