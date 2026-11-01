package main

// TestLLMStructured runs every feature that asks the local LLM for JSON
// constrained to a schema, each on a small set of inputs, on the real
// engine, and records each llm.generate call and each feature's outcome:
// subtitle translation (Fast into Chinese, Japanese and Spanish,
// Cinematic with its review, Autofit with its rewrites), glossary
// extraction, the direction coach, the emotion director, dictation
// clean-up. It is the before/after check of changes
// to constrained decoding (the engine's LLMGuided.swift): run it with each
// engine and compare the summaries.
//
//	AUDIOSMITH_E2E=1 AUDIOSMITH_LLMS_OUT=/tmp/llms/new go test -run TestLLMStructured -v -timeout 1h .
//
// AUDIOSMITH_ENGINE picks the engine binary; AUDIOSMITH_LLMS_ENV passes
// engine variables (comma-separated, e.g. AUDIOSMITH_LLM_PRIME=0);
// AUDIOSMITH_LLMS_ONLY runs some groups (translate, glossary, direction,
// emotion, cleanup). The model is catalog.DefaultLLM (or
// AUDIOSMITH_LLM), already downloaded in the family's model library or
// the Hugging Face cache: nothing is fetched. Output:
// calls.jsonl (each call: group, finish, tokens, whether the reply holds a
// value its schema accepts), features.jsonl and summary.md.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	"github.com/hehehai/actnowkit/models"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/llm"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/translate"
)

// llmsCall is one llm.generate of TestLLMStructured.
type llmsCall struct {
	Group      string  `json:"group"`
	Case       string  `json:"case"`
	Repair     bool    `json:"repair,omitempty"` // a repair turn of Client.JSON
	MaxTokens  int     `json:"maxTokens"`
	Finish     string  `json:"finish"`
	Tokens     int     `json:"tokens"`
	Seconds    float64 `json:"seconds"`
	Valid      bool    `json:"valid"` // the reply holds a value the schema accepts
	Problem    string  `json:"problem,omitempty"`
	Digits     bool    `json:"digits,omitempty"`  // a run of 8+ digits (the old loop's closing zone)
	Special    bool    `json:"special,omitempty"` // a chat control token as text
	Err        string  `json:"err,omitempty"`
	Text       string  `json:"text"`
	SchemaName string  `json:"schema,omitempty"`
}

// llmsFeature is one feature run's outcome.
type llmsFeature struct {
	Group   string  `json:"group"`
	Case    string  `json:"case"`
	OK      bool    `json:"ok"`
	Detail  string  `json:"detail"`
	Seconds float64 `json:"seconds"`
	Calls   int     `json:"calls"`
	Repairs int     `json:"repairs"`
}

var (
	llmsDigits  = regexp.MustCompile(`[0-9]{8,}`)
	llmsSpecial = regexp.MustCompile(`<\|[a-z_]+\|>|<turn\|>|<eos>|<end_of_turn>`)
)

// llmsStories are the director's inputs: a Chinese family scene, an
// English one, and a longer Chinese one (a long answer).
var llmsStories = []struct {
	name  string
	zh    bool
	cast  []string
	lines [][2]string // speaker ("" narrator), text
}{
	{"zh-family", true, []string{"妈妈", "小雨"}, [][2]string{
		{"", "傍晚，雨下得很大，小雨抱着书包冲进家门。"},
		{"妈妈", "怎么淋成这样？伞呢？"},
		{"小雨", "借给同桌了，她家离车站好远。"},
		{"妈妈", "你自己感冒了怎么办？快去换衣服。"},
		{"小雨", "妈，我今天数学考了九十八分！"},
		{"妈妈", "真的？让我看看。"},
		{"", "妈妈接过试卷，嘴角慢慢扬了起来。"},
		{"妈妈", "这道题你以前总做错，这次全对了。"},
		{"小雨", "是爸爸上周教我的方法。"},
		{"", "提到爸爸，屋里忽然安静了下来。"},
		{"妈妈", "他……要是知道，一定很高兴。"},
		{"小雨", "妈，你别难过。爸爸下个月就回来了。"},
		{"妈妈", "嗯，下个月就回来了。"},
		{"", "窗外的雨渐渐小了，厨房里飘出了汤的香味。"},
		{"妈妈", "去洗手，今晚做了你最爱喝的玉米汤。"},
		{"小雨", "太好了！我要喝两碗！"},
	}},
	{"en-office", false, []string{"Maya", "Tom"}, [][2]string{
		{"", "The office was empty except for the hum of the printer."},
		{"Maya", "You're still here? It's almost midnight."},
		{"Tom", "The client moved the deadline. Again."},
		{"Maya", "To when?"},
		{"Tom", "Tomorrow morning. Nine o'clock."},
		{"Maya", "That's insane. Did you tell Rachel?"},
		{"Tom", "She said it's not her problem."},
		{"", "Maya dropped her bag on the desk and pulled up a chair."},
		{"Maya", "Fine. Show me what's left."},
		{"Tom", "You don't have to do this."},
		{"Maya", "I know. Now move over."},
		{"Tom", "Thank you. Really."},
	}},
	{"zh-long", true, []string{"老陈", "阿杰", "林医生"}, [][2]string{
		{"", "凌晨三点，医院走廊的灯一闪一闪。"},
		{"阿杰", "爸，你坐一会儿吧，站了两个小时了。"},
		{"老陈", "我不累。你妈还在里面，我坐不住。"},
		{"阿杰", "医生说手术要四个小时，还早呢。"},
		{"老陈", "四个小时……她怕疼，从小就怕。"},
		{"", "阿杰没说话，把热水递到父亲手里。"},
		{"老陈", "你小时候发烧，她背着你走了两站路来这家医院。"},
		{"阿杰", "我记得。那天也下着雨。"},
		{"老陈", "她一路上都在哼歌，怕你睡着了醒不过来。"},
		{"阿杰", "爸，别说了。"},
		{"老陈", "我要说。我这辈子欠她太多了。"},
		{"", "走廊尽头传来脚步声，林医生摘下口罩走了过来。"},
		{"阿杰", "林医生！我妈怎么样了？"},
		{"林医生", "别紧张，手术还在进行，我出来跟你们说一下情况。"},
		{"老陈", "是不是出什么事了？"},
		{"林医生", "肿瘤比片子上看到的大一些，但位置还好，能切干净。"},
		{"老陈", "能切干净……能切干净就好。"},
		{"林医生", "还需要两个小时左右，你们先休息一下。"},
		{"阿杰", "谢谢您，林医生。"},
		{"", "医生走后，老陈靠着墙慢慢滑坐到地上。"},
		{"阿杰", "爸！你怎么了？"},
		{"老陈", "没事，腿软了。听到能切干净，腿就软了。"},
		{"阿杰", "你吓死我了。"},
		{"老陈", "等你妈出来，我要给她做一桌子菜。"},
		{"阿杰", "你会做什么菜？你连面都煮不熟。"},
		{"老陈", "我学！我现在就学！"},
		{"", "父子俩对视了一眼，都笑了，笑着笑着眼睛就红了。"},
		{"阿杰", "妈要是听见，肯定说你又吹牛。"},
		{"老陈", "让她说，她说什么都行，只要她能说。"},
		{"", "天边泛起了一点白，走廊的灯终于不闪了。"},
	}},
}

var llmsDirections = []string{
	"Whisper it, like you're telling a secret, a little scared.",
	"Angry but holding it back, slow and cold.",
	"Excited, fast, almost shouting with joy!",
	"Tired and sad, as if after a long day of bad news.",
	"Playful and teasing, a bit sarcastic.",
	"温柔一点，像哄孩子睡觉那样，慢慢说。",
	"非常生气，大声质问对方。",
	"紧张，语速快，声音有点发抖。",
	"带着哭腔，很委屈地说。",
	"平静、专业，像新闻播报。",
}

var llmsDictations = []struct{ lang, text string }{
	{"zh", "嗯那个就是说我们明天下午三点开会然后呃讨论一下那个新版本的发布计划大家记得带电脑"},
	{"zh", "我觉得这个方案吧其实还可以就是成本有点高我们是不是可以再再想想别的办法"},
	{"en", "um so basically I think we should uh move the launch to next week because the the API isn't ready yet"},
	{"en", "hey can you send me the uh the slides from yesterday's meeting I need them for the client call at four"},
	{"zh", "今天用 Kubernetes 部署的时候那个 pod 一直重启我查了一下是内存不够然后我把 limit 调到了两个G就好了"},
	{"en", "remind me to call doctor Patel on Monday about the uh test results and also to book the flight to Osaka"},
	{"zh", "这个周末我们去爬山吧如果天气好的话就去香山不好的话就在家看电影你觉得怎么样"},
	{"en", "okay so the quarterly numbers look good revenue is up twelve percent but uh costs are up too so we need to talk about that"},
}

var llmsGlossaryLines = []string{
	"Welcome back to Deep Stack, I'm Elena Park.",
	"Today we're talking about Project Halcyon, our new edge runtime.",
	"Halcyon runs WebAssembly modules right next to the user.",
	"My guest is Marcus Webb, the lead engineer at Northwind Labs.",
	"Marcus, how did Halcyon start?",
	"We kept hitting cold starts on Kubernetes, sometimes two seconds.",
	"So we built a scheduler we call Tidepool.",
	"Tidepool keeps a warm pool of isolates for every tenant.",
	"And the Northwind team open-sourced Tidepool last month.",
	"Right, under the Apache license.",
	"What about state? WebAssembly is stateless by default.",
	"We pair it with Driftwood, our replicated key-value store.",
	"Driftwood replicates across regions in under fifty milliseconds.",
	"Elena, you tried Halcyon yourself, didn't you?",
	"I did. I ported our podcast transcoder to it in a weekend.",
}

func TestLLMStructured(t *testing.T) {
	if os.Getenv("AUDIOSMITH_E2E") == "" {
		t.Skip("set AUDIOSMITH_E2E=1 to run the local LLM's structured features")
	}
	out := os.Getenv("AUDIOSMITH_LLMS_OUT")
	if out == "" {
		out = filepath.Join(t.TempDir(), "llms")
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := engine.Find("../resources")
	if bin == "" {
		t.Fatal("no engine: run scripts/build-engine.sh or set AUDIOSMITH_ENGINE")
	}
	id := os.Getenv("AUDIOSMITH_LLM")
	if id == "" {
		id = catalog.DefaultLLM
	}
	e, ok := catalog.Builtin().ByID(id)
	if !ok {
		t.Fatalf("no entry %s", id)
	}
	ctx := context.Background()
	// The family's model library (hub.CacheDir) when it has the model,
	// else the Hugging Face cache, which the app imports from at its start
	// and a test does not (it would change the user's folders).
	cacheDir := hub.CacheDir()
	l, err := hub.New(hub.Options{CacheDir: cacheDir}).Status(ctx, e)
	if err != nil || l.State != hub.Downloaded {
		if env, _ := models.EnvHub(); env != "" && env != cacheDir {
			if l2, err2 := hub.New(hub.Options{CacheDir: env}).Status(ctx, e); err2 == nil && l2.State == hub.Downloaded {
				cacheDir, l, err = env, l2, nil
			}
		}
	}
	if err != nil || l.State != hub.Downloaded {
		t.Skipf("%s is not downloaded in %s or the Hugging Face cache (%v): this test fetches nothing", e.ID, cacheDir, err)
	}
	t.Logf("models from %s", cacheDir)
	snap := l.Snapshot
	var env []string
	if v := os.Getenv("AUDIOSMITH_LLMS_ENV"); v != "" {
		env = strings.Split(v, ",")
	}
	sup := engine.NewSupervisor(engine.Config{Bin: offlineEngine(t, bin), Bus: core.NewBus(), LogDir: t.TempDir(), Offline: true,
		CacheDir: cacheDir, StatsEvery: -1, Env: env, OnDemand: []proto.Role{proto.RoleMedia, proto.RoleListen}, Logf: t.Logf,
		Log: func(e core.LogEntry) {
			if e.Level != core.SeverityDebug {
				t.Logf("engine %s: %s", e.Level, e.Message)
			}
		}})
	sup.Start(ctx)
	t.Cleanup(sup.Stop)
	ready := time.Now().Add(2 * time.Minute)
	for !slices.ContainsFunc(sup.Status(), func(st engine.WorkerStatus) bool {
		return st.Role == proto.RoleGPU && st.State == engine.WorkerReady
	}) {
		if time.Now().After(ready) {
			t.Fatalf("engine not ready: %+v", sup.Status())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := sup.Call(ctx, proto.RoleGPU, core.NewID(time.Now()), proto.MethodModelsLoad, proto.LoadParams{Model: e.Repo, Path: snap, Task: "llm"}, nil); err != nil {
		t.Fatalf("load: %v", err)
	}

	var (
		mu       sync.Mutex
		group    string
		caseName string
		calls    []llmsCall
	)
	reg := llm.NewRegistry(nil, nil)
	reg.Register("local", &llm.EngineBackend{DefaultModel: e.Repo, Call: func(ctx context.Context, method string, params any, notify func(string, json.RawMessage)) (json.RawMessage, error) {
		p := params.(llm.GenerateParams)
		p.ModelPath = snap
		raw, err := sup.Call(ctx, proto.RoleGPU, core.NewID(time.Now()), method, p, func(m proto.Message) {
			if notify != nil {
				notify(m.Method, m.Params)
			}
		})
		c := llmsCall{MaxTokens: p.MaxTokens}
		mu.Lock()
		c.Group, c.Case = group, caseName
		mu.Unlock()
		if n := len(p.Messages); n > 0 && strings.HasPrefix(p.Messages[n-1].Content, "Your previous reply could not be used") {
			c.Repair = true
		}
		if err != nil {
			c.Err = err.Error()
		} else {
			var r proto.LLMGenerateResult
			json.Unmarshal(raw, &r)
			c.Finish, c.Tokens, c.Seconds, c.Text = r.FinishReason, r.CompletionTokens, r.Seconds, r.Text
			c.Digits = llmsDigits.MatchString(r.Text)
			c.Special = llmsSpecial.MatchString(r.Text)
			v := llm.ExtractJSON(r.Text)
			var s llm.Schema
			switch {
			case v == nil:
				c.Problem = "no JSON"
			case len(p.Schema) > 0 && json.Unmarshal(p.Schema, &s) == nil:
				c.SchemaName = s.Name
				if errs := s.Validate(v); len(errs) > 0 {
					c.Problem = errs[0].String()
				} else {
					c.Valid = true
				}
			default:
				c.Valid = true
			}
		}
		mu.Lock()
		calls = append(calls, c)
		mu.Unlock()
		return raw, err
	}})
	router := &llm.Router{Registry: reg}
	router.SetRouting(llm.Routing{Default: []llm.Route{{Provider: "local"}}})

	var features []llmsFeature
	only := os.Getenv("AUDIOSMITH_LLMS_ONLY")
	run := func(g, name string, fn func() (bool, string)) {
		if only != "" && !slices.Contains(strings.Split(only, ","), g) {
			return
		}
		mu.Lock()
		group, caseName = g, name
		before := len(calls)
		mu.Unlock()
		began := time.Now()
		ok, detail := fn()
		f := llmsFeature{Group: g, Case: name, OK: ok, Detail: detail, Seconds: time.Since(began).Seconds()}
		mu.Lock()
		for _, c := range calls[before:] {
			f.Calls++
			if c.Repair {
				f.Repairs++
			}
		}
		mu.Unlock()
		features = append(features, f)
		t.Logf("%s %s: ok %v, %s (%.1f s, %d calls, %d repairs)", g, name, ok, detail, f.Seconds, f.Calls, f.Repairs)
	}

	han := func(s string) bool {
		return strings.IndexFunc(s, func(r rune) bool { return unicode.Is(unicode.Han, r) }) >= 0
	}
	kana := func(s string) bool {
		return strings.IndexFunc(s, func(r rune) bool { return unicode.In(r, unicode.Hiragana, unicode.Katakana) }) >= 0
	}

	// Subtitle translation.
	var segs []translate.Segment
	for i, s := range llmSubtitles {
		segs = append(segs, translate.Segment{ID: fmt.Sprint(i + 1), Text: s})
	}
	for _, tc := range []struct {
		name    string
		target  string
		quality translate.Quality
		check   func(string) bool
		slots   bool
	}{
		{"fast-zh", "zh-Hans", translate.Fast, han, false},
		{"fast-ja", "ja", translate.Fast, func(s string) bool { return han(s) || kana(s) }, false},
		{"fast-es", "es", translate.Fast, func(s string) bool { return !han(s) }, false},
		{"cinematic-zh", "zh-Hans", translate.Cinematic, han, false},
		{"autofit-zh", "zh-Hans", translate.Autofit, han, true},
	} {
		run("translate", tc.name, func() (bool, string) {
			req := translate.Request{Source: "en", Target: tc.target, Segments: slices.Clone(segs), Quality: tc.quality}
			var tr translate.Translator = &translate.LLM{Router: router}
			if tc.slots {
				// Tight slots (0.6 × the English line's own length), so
				// lines overflow and are rewritten.
				for i := range req.Segments {
					req.Segments[i].Slot = 0.6 * float64(len([]rune(req.Segments[i].Text))) / 15
				}
				tr = translate.WithFit(tr, &translate.Fitter{Router: router})
			}
			res, err := tr.Translate(ctx, req)
			if err != nil {
				return false, err.Error()
			}
			bad, rewritten, over := 0, 0, 0
			for i, s := range res.Segments {
				if s.Err != nil || !tc.check(s.Text) || s.Text == llmSubtitles[i] {
					bad++
				}
				if s.Draft != "" {
					rewritten++
				}
				if s.Fit != nil && s.Fit.Over > 0 {
					over++
				}
			}
			d := fmt.Sprintf("%d lines, %d failed, %d bad; 1 %q, 11 %q, 20 %q", len(res.Segments), res.Failed, bad,
				res.Segments[0].Text, res.Segments[10].Text, res.Segments[19].Text)
			if tc.slots || tc.quality == translate.Cinematic {
				d += fmt.Sprintf("; %d rewritten/adapted, %d still over", rewritten, over)
			}
			return bad <= 1, d
		})
	}

	// Glossary extraction.
	run("glossary", "en-zh", func() (bool, string) {
		var gs []translate.Segment
		for i, s := range llmsGlossaryLines {
			gs = append(gs, translate.Segment{ID: fmt.Sprint(i + 1), Text: s})
		}
		cands, _, err := (&translate.Extractor{Router: router}).Extract(ctx, "en", "zh-Hans", gs, nil, "")
		if err != nil {
			return false, err.Error()
		}
		var terms []string
		found := 0
		for _, c := range cands {
			terms = append(terms, c.Source+"→"+c.Target)
		}
		for _, want := range []string{"Halcyon", "Tidepool", "Driftwood", "Northwind"} {
			if slices.ContainsFunc(cands, func(c translate.Candidate) bool { return strings.Contains(c.Source, want) }) {
				found++
			}
		}
		return found >= 3, fmt.Sprintf("%d terms, %d/4 key names: %s", len(cands), found, strings.Join(terms, ", "))
	})

	// The direction coach.
	for i, note := range llmsDirections {
		run("direction", fmt.Sprint(i+1), func() (bool, string) {
			d := (&translate.Director{Router: router}).Parse(ctx, note)
			ok := d.Source == "llm" && len(d.Emotions) > 0 && d.Instruct != ""
			return ok, fmt.Sprintf("%s %s %v rate %.2f pitch %.1f energy %.2f %q", d.Source, d.Fallback, d.Emotions, d.Rate, d.Pitch, d.Energy, d.Instruct)
		})
	}

	// The emotion director (v2).
	for _, st := range llmsStories {
		script := translate.StoryScript{Title: st.name}
		for _, n := range st.cast {
			script.Cast = append(script.Cast, translate.StoryRole{Name: n})
		}
		for _, l := range st.lines {
			script.Lines = append(script.Lines, translate.StoryScriptLine{Speaker: l[0], Text: l[1]})
		}
		run("emotion", st.name, func() (bool, string) {
			vocab := emotionVocab(st.zh)
			em, err := (&translate.StoryDirector{Router: router}).DirectEmotions(ctx, script, vocab)
			if err != nil {
				return false, err.Error()
			}
			known, turns, nonSteady := 0, 0, 0
			for _, l := range em.Lines {
				if slices.Contains(services.Emotions, strings.ToLower(l.Emotion)) {
					known++
				}
				if l.Turn {
					turns++
				}
				if l.Intensity > 0 {
					nonSteady++
				}
			}
			return len(em.Lines) == len(script.Lines) && known == len(em.Lines),
				fmt.Sprintf("%d/%d lines, %d known emotions, %d with intensity > 0, %d turns, %d baselines, scene %q",
					len(em.Lines), len(script.Lines), known, nonSteady, turns, len(em.Baselines), em.Scene)
		})
	}

	// Dictation clean-up (its own 4 s budget, the model warm).
	for i, d := range llmsDictations {
		run("cleanup", fmt.Sprintf("%d-%s", i+1, d.lang), func() (bool, string) {
			r := (&translate.Cleaner{Router: router}).Clean(ctx, translate.CleanupRequest{Text: d.text, Lang: d.lang, Vocabulary: []string{"Kubernetes", "Patel", "Osaka"}})
			return r.LLM, fmt.Sprintf("llm %v fallback %q %s %.1f s: %q", r.LLM, r.Fallback, r.Detail, r.Elapsed.Seconds(), r.Text)
		})
	}

	// Records and a summary.
	write := func(name string, rows any) {
		var b strings.Builder
		switch v := rows.(type) {
		case []llmsCall:
			for _, r := range v {
				j, _ := json.Marshal(r)
				b.Write(append(j, '\n'))
			}
		case []llmsFeature:
			for _, r := range v {
				j, _ := json.Marshal(r)
				b.Write(append(j, '\n'))
			}
		}
		if err := os.WriteFile(filepath.Join(out, name), []byte(b.String()), 0o644); err != nil {
			t.Error(err)
		}
	}
	write("calls.jsonl", calls)
	write("features.jsonl", features)
	var sum strings.Builder
	fmt.Fprintf(&sum, "# Structured LLM features %s\n\nEngine %s, env %v, model %s\n\n", time.Now().Format("2006-01-02 15:04"), bin, env, e.Repo)
	sum.WriteString("| group | features ok | calls | first replies valid | repairs | length | digit runs | control tokens | errors | mean s/call |\n|---|---|---|---|---|---|---|---|---|---|\n")
	var groups []string
	for _, f := range features {
		if !slices.Contains(groups, f.Group) {
			groups = append(groups, f.Group)
		}
	}
	for _, g := range groups {
		fok, fn := 0, 0
		for _, f := range features {
			if f.Group == g {
				fn++
				if f.OK {
					fok++
				}
			}
		}
		n, first, firstValid, repairs, length, digits, special, errs := 0, 0, 0, 0, 0, 0, 0, 0
		secs := 0.0
		for _, c := range calls {
			if c.Group != g {
				continue
			}
			n++
			secs += c.Seconds
			if c.Repair {
				repairs++
			} else {
				first++
				if c.Valid {
					firstValid++
				}
			}
			if c.Finish == "length" {
				length++
			}
			if c.Digits {
				digits++
			}
			if c.Special {
				special++
			}
			if c.Err != "" {
				errs++
			}
		}
		mean := 0.0
		if n > 0 {
			mean = secs / float64(n)
		}
		fmt.Fprintf(&sum, "| %s | %d/%d | %d | %d/%d | %d | %d | %d | %d | %d | %.1f |\n", g, fok, fn, n, firstValid, first, repairs, length, digits, special, errs, mean)
	}
	sum.WriteString("\n## Features\n\n")
	for _, f := range features {
		fmt.Fprintf(&sum, "- %s %s: %v — %s (%.1f s, %d calls, %d repairs)\n", f.Group, f.Case, f.OK, f.Detail, f.Seconds, f.Calls, f.Repairs)
	}
	if err := os.WriteFile(filepath.Join(out, "summary.md"), []byte(sum.String()), 0o644); err != nil {
		t.Error(err)
	}
	t.Log("\n" + sum.String())
}
