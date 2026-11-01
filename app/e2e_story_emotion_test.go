package main

// TestStoryEmotionExp renders stories in the variants of the emotion
// experiment (plan/story-emotion-context-plan.md, thread #78) on the real
// engine, for listening and measuring: A (the emotion director v2: short
// emotion instructions, line by line), AE (A with
// emotion-paced gaps) and AEF (AE with character tracks: the defaults). It
// writes, per story, the directors' answers (step "direct"), then per
// model and variant (step "render") the chapter, its sidecar, each line's
// clip and a manifest (lines, instructions, timings, the recognizer's
// text and error rate, lead-in counts, render time).
//
//	AUDIOSMITH_EMO_OUT=/tmp/exp AUDIOSMITH_EMO_STORIES=a.json,b.json AUDIOSMITH_EMO_STEP=direct \
//	  go test -run TestStoryEmotionExp -v -timeout 2h .
//
// A story file: {"name", "title", "cast": [{"name", "voice"}], "lines": [{"speaker", "text"}]}
// (voice: a CustomVoice speaker). AUDIOSMITH_EMO_MODELS picks models
// (cv06, vd17), AUDIOSMITH_EMO_VARIANTS variants (default A,AE,AEF).
// Thread #80 adds the letters D (whole dialogue on the catalogue's
// dialogue model), T (its emotion tags) and 0 (no director: the story as
// written), e.g. AED, AEDT, 0D; thread #82 the letter M (whole dialogue on
// MOSS-TTSD), e.g. AEM.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/llm"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/translate"
)

type expStory struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	// Narrator is the CustomVoice speaker of lines whose speaker is not cast.
	Narrator string `json:"narrator,omitempty"`
	Cast     []struct {
		Name  string `json:"name"`
		Voice string `json:"voice"`
	} `json:"cast"`
	Lines []struct {
		Speaker string `json:"speaker"`
		Text    string `json:"text"`
	} `json:"lines"`
}

// expDesigns are the 1.7B VoiceDesign voices of the CustomVoice speakers.
var expDesigns = map[string]string{
	"Vivian":   "一个七八岁的小女孩，声音清脆稚嫩，音调偏高，活泼可爱。",
	"Serena":   "一位三十多岁的年轻妈妈，声音温柔柔和，语速平稳，略带疲惫。",
	"Uncle_Fu": "一位四十岁左右的爸爸，声音低沉温和，语速平稳。",
	"Aiden":    "A man in his late thirties, a warm, friendly American father's voice, relaxed pace.",
}

// expLine is one line of a rendered variant in its manifest.
type expLine struct {
	N         int     `json:"n"`
	Who       string  `json:"who"`
	Text      string  `json:"text"`
	Emotion   string  `json:"emotion,omitempty"`
	Intensity int     `json:"intensity"`
	Turn      bool    `json:"turn,omitempty"`
	Direction string  `json:"direction,omitempty"`
	Instruct  string  `json:"instruct"`
	Start     float64 `json:"start"`
	End       float64 `json:"end"`
	Gap       float64 `json:"gapBefore"`
	File      string  `json:"file"`
	Heard     string  `json:"heard"`
	CER       float64 `json:"cer"`
}

type expManifest struct {
	Story, Model, Variant string
	Seconds               float64 // render wall time
	AudioSeconds          float64
	Cached                int
	Chunks                int
	Turns, TurnsUnsplit   int
	Tracks, TrackLines    int
	TracksFailed          int
	Dialogues             int
	DialogueLines         int
	DialogueRedone        int
	DialogueFailed        int
	Lines                 []expLine
}

func TestStoryEmotionExp(t *testing.T) {
	out := os.Getenv("AUDIOSMITH_EMO_OUT")
	if out == "" {
		t.Skip("set AUDIOSMITH_EMO_OUT, AUDIOSMITH_EMO_STORIES and AUDIOSMITH_EMO_STEP (direct, render)")
	}
	var stories []expStory
	for _, f := range envList("AUDIOSMITH_EMO_STORIES", "") {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var s expStory
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		stories = append(stories, s)
	}
	bin := engine.Find("../resources")
	if bin == "" {
		t.Fatal("no engine: set AUDIOSMITH_ENGINE")
	}
	lib := openTestLibrary(t)
	h := &engineHost{bin: bin, logDir: t.TempDir()}
	a := newApp(h, lib)
	a.autoplay = false
	events := make(chan func(), 1<<16)
	a.enginePost = func(fn func()) { events <- fn }
	a.postQueue = a.enginePost
	h.sup = engine.NewSupervisor(engine.Config{Bin: bin, Bus: a.bus, LogDir: h.logDir, Offline: true, CacheDir: modelCache(a.prefs),
		OnDemand: []proto.Role{proto.RoleListen, proto.RoleMedia},
		Log: func(e core.LogEntry) {
			if e.Level != core.SeverityInfo && e.Level != core.SeverityDebug {
				t.Logf("%s %s: %s", e.Level, e.Source, e.Message)
			}
		}, Logf: t.Logf})
	a.connectEngine(h)
	defer h.Stop()
	waitFor := func(what string, timeout time.Duration, done func() bool) {
		t.Helper()
		deadline := time.After(timeout)
		for !done() {
			if a.engState == engineFailed {
				t.Fatalf("engine failed: %s\n%s", a.engError, a.engTail)
			}
			select {
			case fn := <-events:
				fn()
			case <-time.After(200 * time.Millisecond):
			case <-deadline:
				t.Fatalf("timed out waiting for %s", what)
			}
		}
	}
	a.startEngine()
	waitFor("ready", time.Minute, func() bool { return a.engState == engineReady })
	ctx := context.Background()

	for _, s := range stories {
		dir := filepath.Join(out, s.Name)
		os.MkdirAll(dir, 0o755)
		doc := expDoc(s)
		switch os.Getenv("AUDIOSMITH_EMO_STEP") {
		case "direct":
			expDirect(t, ctx, a, h, &doc, dir)
		case "render":
			expRender(t, ctx, a, h, doc, dir, waitFor)
		default:
			t.Fatal("AUDIOSMITH_EMO_STEP: direct or render")
		}
	}
}

// expDoc is a story's document, undirected.
func expDoc(s expStory) services.LongformDoc {
	doc := services.NewLongformDoc(core.ProjectStory, "")
	doc.Title, doc.Seed = s.Title, 20261017
	if s.Narrator != "" {
		doc.Voice = services.SpeakerRef + s.Narrator
	}
	ids := map[string]string{}
	for i, c := range s.Cast {
		id := fmt.Sprintf("c%d", i+1)
		ids[c.Name] = id
		doc.Cast = append(doc.Cast, services.CastMember{ID: id, Name: c.Name, Voice: services.SpeakerRef + c.Voice})
	}
	for i, l := range s.Lines {
		doc.Lines = append(doc.Lines, services.StoryLine{ID: fmt.Sprintf("l%02d", i+1), Text: l.Text, Character: ids[l.Speaker]})
	}
	doc.Language = "en"
	if storyZh(&doc) {
		doc.Language = "zh"
	}
	return doc
}

// expDirect runs the emotion director on the local language model and
// saves its answer, timed.
func expDirect(t *testing.T, ctx context.Context, a *app, h *engineHost, doc *services.LongformDoc, dir string) {
	e, _ := catalog.Builtin().ByID(catalog.DefaultLLM)
	snap, err := a.lib.modelSnapshot(ctx, e.Repo)
	if err != nil {
		t.Fatalf("%s: %v", e.Repo, err)
	}
	reg := llm.NewRegistry(nil, nil)
	reg.Register("local", &llm.EngineBackend{ID: "local", DefaultModel: e.Repo, Call: func(ctx context.Context, method string, params any, notify func(string, json.RawMessage)) (json.RawMessage, error) {
		p := params.(llm.GenerateParams)
		p.ModelPath = snap
		return h.sup.Call(ctx, proto.RoleGPU, core.NewID(time.Now()), method, p, func(m proto.Message) {
			if notify != nil {
				notify(m.Method, m.Params)
			}
		})
	}})
	router := &llm.Router{Registry: reg}
	router.SetRouting(llm.Routing{Default: []llm.Route{{Provider: "local"}}})
	director := &translate.StoryDirector{Router: router}
	save := func(name string, v any, took time.Duration) {
		b, _ := json.MarshalIndent(map[string]any{"seconds": math.Round(took.Seconds()*10) / 10, "answer": v}, "", "  ")
		os.WriteFile(filepath.Join(dir, name), b, 0o644)
		t.Logf("%s (%.1f s):\n%s", name, took.Seconds(), b)
	}
	started := time.Now()
	v2, err := director.DirectEmotions(ctx, storyScript(doc, nil), emotionVocab(storyZh(doc)))
	if err != nil {
		t.Fatalf("director v2: %v", err)
	}
	save("director-v2.json", v2, time.Since(started))
}

// expDirected is doc directed as variant v by the emotion director, with
// its production.
func expDirected(t *testing.T, doc services.LongformDoc, dir, v string) services.LongformDoc {
	read := func(name string, into any) {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		var w struct {
			Answer json.RawMessage `json:"answer"`
		}
		if err != nil || json.Unmarshal(raw, &w) != nil || json.Unmarshal(w.Answer, into) != nil {
			t.Fatalf("%s/%s: %v (run the direct step first)", dir, name, err)
		}
	}
	d := doc
	d.Lines, d.Cast = slices.Clone(doc.Lines), slices.Clone(doc.Cast)
	d.Production = services.DefaultStoryProduction()
	flags, seed, _ := strings.Cut(v, "#")
	if !strings.Contains(flags, "0") {
		var em translate.StoryEmotions
		read("director-v2.json", &em)
		applyEmotions(&d, em, false)
	}
	// "A" then the parts on top: E (emotion gaps), F (character tracks),
	// D (whole dialogue), M (on MOSS-TTSD), T (its emotion tags); "#n" adds n to the seed.
	if strings.Contains(flags, "D") || strings.Contains(flags, "M") {
		e, _ := dialogueEntry()
		if strings.Contains(flags, "M") {
			e, _ = dialogueByName("moss")
		}
		d.Production.Dialogue, d.Production.DialogueModel = true, e.Repo
		d.Production.DialogueTags = strings.Contains(flags, "T")
	}
	d.Production.NoEmotionGaps = !strings.Contains(flags, "E")
	d.Production.NoTracks = !strings.Contains(flags, "F")
	if seed != "" {
		var n int64
		fmt.Sscanf(seed, "%d", &n)
		d.Seed += n
	}
	return d
}

func expRender(t *testing.T, ctx context.Context, a *app, h *engineHost, doc services.LongformDoc, dir string,
	waitFor func(string, time.Duration, func() bool)) {
	for _, m := range envList("AUDIOSMITH_EMO_MODELS", "cv06") {
		var repo string
		var resolve services.VoiceResolver
		switch m {
		case "cv06":
			e, _ := catalog.Builtin().ByID("qwen3-tts-0.6b-customvoice-8bit")
			repo = e.Repo
			resolve = func(ref string) (string, core.Take, bool) {
				id, ok := strings.CutPrefix(ref, services.SpeakerRef)
				return ref, core.Take{Voice: id}, ok
			}
		case "vd17":
			e, _ := catalog.Builtin().ByID("qwen3-tts-1.7b-voicedesign-4bit")
			repo = e.Repo
			resolve = func(ref string) (string, core.Take, bool) {
				id, _ := strings.CutPrefix(ref, services.SpeakerRef)
				d, ok := expDesigns[id]
				return "design:" + id, core.Take{Voice: d, Seed: int64(len(id)) * 7919}, ok
			}
		case "kokoro":
			// Kokoro (English stories): Kokoro voices for the speakers.
			e, _ := catalog.Builtin().ByID("kokoro-82m-bf16")
			repo = e.Repo
			kv := map[string]string{"Vivian": "af_heart", "Serena": "af_bella", "Aiden": "am_adam", "Uncle_Fu": "am_michael"}
			resolve = func(ref string) (string, core.Take, bool) {
				id, _ := strings.CutPrefix(ref, services.SpeakerRef)
				v, ok := kv[id]
				return "kokoro:" + v, core.Take{Voice: v}, ok
			}
		default:
			t.Fatalf("model %s", m)
		}
		for _, v := range envList("AUDIOSMITH_EMO_VARIANTS", "A,AE,AEF") {
			d := expDirected(t, doc, dir, v)
			d.Model = repo
			spec, probs := services.Compile(&d, resolve)
			for _, p := range probs {
				if p.Blocking {
					t.Fatalf("%s-%s: %+v", m, v, p)
				}
			}
			spec.Audition = true
			started := time.Now()
			r, err := a.lib.svc.Longform.Start(ctx, spec)
			if err != nil {
				t.Fatal(err)
			}
			var body services.RenderBody
			waitFor(m+"-"+v, time.Hour, func() bool {
				rr, b, err := a.lib.svc.Longform.Get(ctx, r.ID)
				body = b
				return err == nil && rr.State != core.RenderRunning
			})
			if len(body.Chapters) != 1 || body.Chapters[0].File == "" {
				t.Fatalf("%s-%s: %+v %+v", m, v, body.Chapters, body.Error)
			}
			took := time.Since(started).Seconds()
			name := m + "-" + strings.ReplaceAll(v, "#", "_s")
			vdir := filepath.Join(dir, name)
			os.MkdirAll(vdir, 0o755)
			wavPath := filepath.Join(dir, name+".wav")
			if err := services.CopyFile(ctx, body.Chapters[0].File, wavPath); err != nil {
				t.Fatal(err)
			}
			res, err := services.ReadChapter(body.Chapters[0].File)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := json.MarshalIndent(res, "", " ")
			os.WriteFile(wavPath+".json", b, 0o644)
			man := expManifest{Story: filepath.Base(dir), Model: m, Variant: v, Seconds: math.Round(took*10) / 10, AudioSeconds: res.AudioSeconds,
				Cached: res.Cached, Chunks: res.Chunks, Turns: res.Turns, TurnsUnsplit: res.TurnsUnsplit, Tracks: res.Tracks, TrackLines: res.TrackLines, TracksFailed: res.TracksFailed,
				Dialogues: res.Dialogues, DialogueLines: res.DialogueLines, DialogueRedone: res.DialogueRedone, DialogueFailed: res.DialogueFailed}
			man.Lines = expLines(t, ctx, h.sup, a.lib, wavPath, vdir, res, spec)
			b, _ = json.MarshalIndent(man, "", " ")
			os.WriteFile(filepath.Join(dir, name+".manifest.json"), b, 0o644)
			cer := 0.0
			for _, l := range man.Lines {
				cer += l.CER
			}
			t.Logf("%s/%s: %.1f s audio in %.0f s; turns %d (unsplit %d); tracks %d of %d lines (failed %d); dialogue %d segments, %d lines (redone %d, failed %d); mean CER %.3f",
				filepath.Base(dir), name, res.AudioSeconds, took, res.Turns, res.TurnsUnsplit, res.Tracks, res.TrackLines, res.TracksFailed,
				res.Dialogues, res.DialogueLines, res.DialogueRedone, res.DialogueFailed, cer/float64(len(man.Lines)))
		}
	}
}

// expLines cuts each line's clip from the chapter, transcribes it with
// Qwen3-ASR and lists it with its direction and instruction.
func expLines(t *testing.T, ctx context.Context, sup engine.Supervisor, lib *library, path, dir string, res services.ChapterResult, spec services.LongformSpec) []expLine {
	e, _ := catalog.Builtin().ByID(catalog.DefaultSTT)
	snap, err := lib.modelSnapshot(ctx, e.Repo)
	if err != nil {
		t.Fatalf("ASR: %v", err)
	}
	buf, _, err := wav.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := spec.Chapters[0].Lines
	var out []expLine
	for i, lp := range res.Lines {
		l := lines[i]
		seg := buf.Slice(dsp.FramesAt(lp.Start, buf.Rate), min(dsp.FramesAt(lp.End, buf.Rate), buf.Frames()))
		f := filepath.Join(dir, fmt.Sprintf("line-%02d.wav", i+1))
		if err := wav.WriteFile(f, seg, wav.PCM16, wav.Options{}); err != nil {
			t.Fatal(err)
		}
		lang := spec.Lang
		raw, err := sup.Call(ctx, proto.RoleGPU, core.NewID(time.Now()), proto.MethodSTTTranscribe,
			proto.TranscribeParams{Model: e.Repo, ModelPath: snap, Audio: f, Language: lang}, nil)
		var tr proto.TranscribeResult
		if err != nil {
			t.Logf("ASR line %d: %v", i+1, err)
		} else {
			json.Unmarshal(raw, &tr)
		}
		text := l.Spans[0].Text
		gap := 0.0
		if i > 0 {
			gap = math.Round((lp.Start-res.Lines[i-1].End)*1000) / 1000
		}
		fold := strings.NewReplacer("她", "他", "它", "他")
		out = append(out, expLine{N: i + 1, Who: l.Character, Text: text, Emotion: l.Emotion, Intensity: l.Intensity, Turn: l.Turn, Direction: l.Direction,
			Instruct: services.LineInstruct(lines, i, spec.Scene), Start: lp.Start, End: lp.End, Gap: gap, File: filepath.Base(f),
			Heard: tr.Text, CER: dub.Drift(fold.Replace(text), fold.Replace(tr.Text))})
	}
	return out
}
