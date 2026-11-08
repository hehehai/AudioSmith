package services

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/dsptest"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/text"
)

// fakeTrack is a one-track dialogue model (MOSS-TTSD): each generated
// turn a tone of its own pitch (by its index) as long as its text, 0.3 s
// apart, in one dialogue.wav with codec frames; stt.align times the
// track's text over the tones; stt.transcribe hears a clip by its pitch.
type fakeTrack struct {
	mu     sync.Mutex
	calls  []proto.DialogueParams
	heard  map[int]string // pitch (Hz) → what a clip of it says
	tracks map[string][]proto.Word
	garble func(call int, text string) bool // the recognizer mishears the turn
	skip   func(call int) bool              // the model skips the second turn
}

func trackPitch(i int) float64 { return 200 + 37*float64(i) }

func (f *fakeTrack) executor(method string) jobs.Executor {
	return jobs.ExecutorFunc(func(ctx context.Context, job jobs.Job, report func(jobs.Progress), _ func(json.RawMessage)) (json.RawMessage, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch method {
		case proto.MethodTTSDialogue:
			var p proto.DialogueParams
			json.Unmarshal(job.Params, &p)
			f.calls = append(f.calls, p)
			if p.Model != "m/moss" && p.Model != "m/vibevoice" {
				return nil, &jobs.Error{Code: proto.CodeBadRequest, Message: "model " + p.Model}
			}
			call := len(f.calls)
			var parts []dsp.Buffer
			var words []proto.Word
			at := 0.0
			res := proto.DialogueResult{Model: p.Model, SampleRate: 24000, Channels: 1}
			for i, t := range p.Turns {
				if len(t.Codes) > 0 {
					if len(t.Codes)%16 != 0 {
						return nil, &jobs.Error{Code: proto.CodeBadRequest, Message: "codes"}
					}
					continue
				}
				if i == 1 && f.skip != nil && f.skip(call) {
					continue
				}
				w, c := text.Count(t.Text)
				d := text.Estimate(w, c).Seconds()
				parts = append(parts, dsptest.Sine(24000, 1, trackPitch(i), -14, d, 0), dsptest.Silence(24000, 1, 0.3))
				units := []string{}
				for _, r := range t.Text {
					if alignUnits(string(r)) != "" {
						units = append(units, string(r))
					}
				}
				for k, u := range units {
					words = append(words, proto.Word{Text: u, Start: at + d*float64(k)/float64(len(units)), End: at + d*float64(k+1)/float64(len(units))})
				}
				at += d + 0.3
				f.heard[int(trackPitch(i))] = stripTags(t.Text)
				if f.garble != nil && f.garble(call, t.Text) {
					f.heard[int(trackPitch(i))] = "嗯嗯啊啊"
				}
				res.Turns = append(res.Turns, proto.DialogueTurnOut{Index: i, Speaker: t.Speaker, Text: t.Text})
			}
			buf := dsptest.Concat(parts...)
			os.MkdirAll(p.OutputDir, 0o755)
			out := filepath.Join(p.OutputDir, "dialogue.wav")
			wav.WriteFile(out, buf, wav.Float32, wav.Options{})
			f.tracks[out] = words
			frames := buf.Frames()/mossFrameSamples + 1
			codes := make([]int, frames*16)
			for i := range codes {
				codes[i] = (i*7 + call) % 1024
			}
			res.Mixed = &proto.DialogueMixed{Output: out, Frames: int64(buf.Frames()), AudioSeconds: buf.Seconds(), Codes: codes, Codebooks: 16}
			if p.Model == "m/vibevoice" { // VibeVoice: no codes
				res.Mixed.Codes, res.Mixed.Codebooks = nil, 0
			}
			return json.Marshal(res)
		case proto.MethodSTTAlign:
			var p proto.AlignParams
			json.Unmarshal(job.Params, &p)
			return json.Marshal(proto.AlignResult{Words: f.tracks[p.Audio]})
		case proto.MethodSTTTranscribe:
			var p proto.TranscribeParams
			json.Unmarshal(job.Params, &p)
			b, _, err := wav.ReadFile(p.Audio)
			if err != nil {
				return nil, err
			}
			return json.Marshal(proto.TranscribeResult{Text: f.heard[int(pitchOf(b))]})
		}
		return nil, &jobs.Error{Code: proto.CodeBadRequest, Message: method}
	})
}

// pitchOf is a tone's frequency by its zero crossings (the turn pitches
// are 37 Hz apart: rounded to the nearest).
func pitchOf(b dsp.Buffer) float64 {
	first, last, n := -1, -1, 0
	for i := 1; i < b.Frames(); i++ {
		if (b.Samples[i-1] < 0) != (b.Samples[i] < 0) && (b.Samples[i-1] != 0 || b.Samples[i] != 0) {
			if first < 0 {
				first = i
			}
			last = i
			n++
		}
	}
	if n < 2 {
		return 0
	}
	hz := float64(n-1) / 2 / (float64(last-first) / float64(b.Rate))
	k := math.Round((hz - 200) / 37)
	return 200 + 37*k
}

// MOSS-TTSD speaks the segment as one track, cut back into the lines; a
// line heard wrong is made again after the lines before it (their frames
// as context); a track the aligner cannot cut is made again whole.
func TestDialogueOneTrack(t *testing.T) {
	e := newLongformEnv(t, t.TempDir(), false)
	defer e.close()
	ft := &fakeTrack{heard: map[int]string{}, tracks: map[string][]proto.Word{}}
	r := e.svc.Longform.render
	r.engine, r.recognizer, r.aligner = ft.executor, "m/asr", "m/aligner"
	r.models = func(repo string) ModelInfo {
		if repo == "m/moss" {
			return ModelInfo{Traits: TraitsFor(FamilyMossTTSD, []string{"dialogue"}), Revision: "rm"}
		}
		return kokoroInfo(repo)
	}
	line := func(id, who, text string) LineSpec {
		return LineSpec{ID: id, Character: who, Emotion: EmotionSteady, Spans: []SpanSpec{{Voice: strings.ToLower(who), Character: who, Text: text}}}
	}
	prod := DefaultStoryProduction()
	prod.Dialogue, prod.DialogueModel, prod.DialogueTags = true, "m/moss", true
	spec := LongformSpec{Kind: core.ProjectStory, Title: "小猫", Model: "m/kokoro", Lang: "zh", Seed: 7,
		Voices: map[string]core.Take{"kid": {Voice: "zf_xiaobei"}, "mom": {Voice: "zf_xiaoni"}},
		Chapters: []ChapterSpec{{Lines: []LineSpec{
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
	// Line c is misheard the first time: made again after a and b.
	ft.garble = func(call int, text string) bool { return call == 1 && strings.HasPrefix(text, "我能") }
	res := run()
	if len(ft.calls) != 2 || res.Dialogues != 1 || res.DialogueLines != 4 || res.DialogueRedone != 1 || res.DialogueFailed != 0 {
		t.Fatalf("calls %d, result %+v", len(ft.calls), res)
	}
	first, redo := ft.calls[0], ft.calls[1]
	if len(first.Turns) != 4 || strings.Contains(first.Turns[0].Text, "<|") {
		t.Errorf("first call %+v", first.Turns)
	}
	if len(redo.Turns) != 3 || len(redo.Turns[0].Codes) == 0 || len(redo.Turns[0].Codes)%16 != 0 || len(redo.Turns[1].Codes) == 0 ||
		len(redo.Turns[2].Codes) != 0 || *redo.Seed == *first.Seed {
		t.Errorf("redo %d turns, codes %d %d", len(redo.Turns), len(redo.Turns[0].Codes), len(redo.Turns[1].Codes))
	}
	// Lines in order, each about its text's length.
	for i := 1; i < 4; i++ {
		if res.Lines[i].Start <= res.Lines[i-1].End {
			t.Errorf("lines %+v", res.Lines)
		}
	}
	// The model skips line b: the track does not cut, so it is made again whole.
	os.RemoveAll(filepath.Join(e.dir, "longform-chunks")) // the longform chunks (data folder, audit A7)
	ft.calls, ft.garble = nil, nil
	ft.skip = func(call int) bool { return call == 1 }
	res = run()
	if len(ft.calls) != 2 || res.DialogueLines != 4 || res.DialogueRedone != 4 || *ft.calls[1].Seed == *ft.calls[0].Seed {
		t.Errorf("track again: %d calls, %+v", len(ft.calls), res)
	}
	// Always skipped: the segment goes to the story's model.
	os.RemoveAll(filepath.Join(e.dir, "longform-chunks")) // the longform chunks (data folder, audit A7)
	e.synth.reset()
	ft.calls = nil
	ft.skip = func(int) bool { return true }
	res = run()
	said := "|" + strings.Join(e.synth.texts(), "|") + "|"
	if res.DialogueFailed != 4 || res.Dialogues != 0 || len(ft.calls) != 2 || !strings.Contains(said, "|养猫要花很多时间照顾它。|") {
		t.Errorf("failed track: %+v, %d calls, synth %q", res, len(ft.calls), e.synth.texts())
	}
}

// VibeVoice (thread #166) speaks the segment as one track too, but
// without codes: a line heard wrong cannot be made again after the lines
// before it, so it goes to the story's model; the others stay.
func TestDialogueOneTrackWithoutCodes(t *testing.T) {
	e := newLongformEnv(t, t.TempDir(), false)
	defer e.close()
	ft := &fakeTrack{heard: map[int]string{}, tracks: map[string][]proto.Word{}}
	r := e.svc.Longform.render
	r.engine, r.recognizer, r.aligner = ft.executor, "m/asr", "m/aligner"
	r.models = func(repo string) ModelInfo {
		if repo == "m/vibevoice" {
			return ModelInfo{Traits: TraitsFor(FamilyVibeVoice, []string{"dialogue"}), Revision: "rv"}
		}
		return kokoroInfo(repo)
	}
	line := func(id, who, text string) LineSpec {
		return LineSpec{ID: id, Character: who, Emotion: EmotionSteady, Spans: []SpanSpec{{Voice: strings.ToLower(who), Character: who, Text: text}}}
	}
	prod := DefaultStoryProduction()
	prod.Dialogue, prod.DialogueModel, prod.DialogueTags = true, "m/vibevoice", true
	spec := LongformSpec{Kind: core.ProjectStory, Title: "小猫", Model: "m/kokoro", Lang: "zh", Seed: 7,
		Voices: map[string]core.Take{"kid": {Voice: "zf_xiaobei"}, "mom": {Voice: "zf_xiaoni"}},
		Chapters: []ChapterSpec{{Lines: []LineSpec{
			line("a", "Kid", "妈妈，我想领养一只小猫，可以吗？"), line("b", "Mom", "养猫要花很多时间照顾它。"),
			line("c", "Kid", "我能！我每天喂它。"), line("d", "Mom", "那我们先去救助站看看吧。"),
		}}},
		Production: prod, Audition: true}
	ft.garble = func(call int, text string) bool { return strings.HasPrefix(text, "我能") }
	var marked []string // the chapter is watermarked as any model's (#166: VibeVoice too)
	r.watermark = func(_ context.Context, path string, _ int) (bool, error) {
		marked = append(marked, path)
		return true, nil
	}
	rr, err := e.svc.Longform.Start(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	_, body := e.wait(t, rr.ID)
	if st := body.Chapters[0].State; st != "done" {
		t.Fatalf("chapter %+v", body.Chapters[0])
	}
	res, err := ReadChapter(body.Chapters[0].File)
	if err != nil {
		t.Fatal(err)
	}
	if len(ft.calls) != 1 || res.Dialogues != 1 || res.DialogueLines != 3 || res.DialogueRedone != 0 || res.DialogueFailed != 1 {
		t.Fatalf("calls %d, result %+v", len(ft.calls), res)
	}
	// No SoulX tags in a one-track model's text.
	if strings.Contains(ft.calls[0].Turns[0].Text, "<|") {
		t.Errorf("turns %+v", ft.calls[0].Turns)
	}
	if len(marked) == 0 || !strings.HasSuffix(marked[len(marked)-1], ".wav") {
		t.Errorf("watermarked %q", marked)
	}
	if said := strings.Join(e.synth.texts(), "|"); !strings.Contains(said, "我能！我每天喂它。") {
		t.Errorf("story's model said %q", said)
	}
}

func TestMixedFrames(t *testing.T) {
	m := &proto.DialogueMixed{Codebooks: 2, Codes: []int{0, 0, 1, 1, 2, 2, 3, 3, 4, 4}} // 5 frames: a leading one, 4 under 4 × 1920 samples
	cuts := []int{0, 1920 + 5, 4 * 1920}
	if got := mixedFrames(m, cuts, 0); len(got) != 4 || got[2] != 1 {
		t.Errorf("line 0: %v", got)
	}
	if got := mixedFrames(m, cuts, 1); len(got) != 6 || got[0] != 2 {
		t.Errorf("line 1: %v", got)
	}
}
