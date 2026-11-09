package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/llm"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/translate"
)

const customVoice06 = "mlx-community/Qwen3-TTS-12Hz-0.6B-CustomVoice-8bit"

// kittenDoc is the user's two-person story on 0.6B CustomVoice.
func kittenDoc(d *services.LongformDoc) {
	d.Title, d.Model, d.Language = "我想领养一只小猫", customVoice06, "zh"
	d.Voice = "speaker:Vivian"
	d.Cast = []services.CastMember{{ID: "kid", Name: "孩子", Voice: "speaker:Vivian"}, {ID: "mom", Name: "妈妈", Voice: "speaker:Serena"}}
	d.Lines = []services.StoryLine{
		{ID: "l1", Character: "kid", Text: "我想领养一只小猫。"},
		{ID: "l2", Character: "kid", Text: "妈妈，我想领养一只小猫，可以吗？"},
		{ID: "l3", Character: "mom", Text: "领养是好事，可是养猫要花很多时间照顾它。你能一直照顾它吗？"},
		{ID: "l4", Character: "kid", Text: "我能！我每天喂它，给它换水，铲猫砂。"},
	}
}

const kittenEmotions = `{"scene":"傍晚的家里，孩子想领养小猫，妈妈犹豫","cast":[{"name":"孩子","baseline":"excited"},{"name":"妈妈","baseline":"tender"}],
"lines":[{"n":1,"emotion":"steady","intensity":1,"turn":false,"note":"小声试探"},{"n":2,"emotion":"pleading","intensity":2,"turn":false,"note":"撒娇央求"},
{"n":3,"emotion":"worried","intensity":1,"turn":false,"note":"犹豫但温和"},{"n":4,"emotion":"excited","intensity":3,"turn":false,"note":"急切地保证"}]}`

// The catalogue says what the engine reports: Qwen3-TTS CustomVoice and
// VoiceDesign take instructions, and VoxCPM2 (thread #165, its clones
// too); the stories' and dubs' logic read it through one predicate.
func TestInstructCapability(t *testing.T) {
	for _, e := range catalog.Builtin().ForTask(catalog.TaskTTS) {
		repo := strings.ToLower(e.Repo)
		want := e.Family == "qwen3_tts" && (strings.Contains(repo, "customvoice") || strings.Contains(repo, "voicedesign")) || e.Family == "voxcpm2"
		if e.Has(catalog.CapInstruct) != want {
			t.Errorf("%s: instruct %v in the catalogue, the engine says %v", e.ID, e.Has(catalog.CapInstruct), want)
		}
		if got := services.TakesInstruct(capNames(e)); got != want {
			t.Errorf("%s: TakesInstruct %v", e.ID, got)
		}
		if dubCaps(e.Repo).Instruct != want || modelInfo(e.Repo).Instruct != want {
			t.Errorf("%s: dub %v, story %v", e.ID, dubCaps(e.Repo).Instruct, modelInfo(e.Repo).Instruct)
		}
		level := storyContinuity(e.Repo)
		switch {
		case want && level != services.ContinuityInstruct,
			!want && level != services.ContinuityLines:
			t.Errorf("%s: continuity %s", e.ID, level)
		}
	}
}

func TestStoryPerformanceUI(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.SetSize(1100, 900)
	a.navigate("/stories")
	p := a.lfStories
	if p.doc.Production.Pacing != services.PacingDialogue {
		t.Fatalf("a new story's pacing %q", p.doc.Production.Pacing)
	}
	kittenDoc(&p.doc)
	p.open["scene"] = true
	tt.Frame()
	a.setAdvancedOpen("story", true)
	tt.SetSize(1100, 1600)
	tt.Frame()
	for _, s := range []string{"Performance", "Directed delivery", "Pacing", "Dialogue (tight)", "Scene", "AI director", "Merge a character's lines", "Even out each voice"} {
		if !tt.HasText(s) {
			t.Errorf("no %q: %q", s, tt.Texts())
		}
	}
	// Whole dialogue on a Mac that fits both dialogue models: a menu picks one (thread #82).
	if tt.HasText("Dialogue model") {
		t.Error("a dialogue model menu with whole dialogue off")
	}
	a.sys = &sysInfo{Memory: 32 << 30}
	p.doc.Production.Dialogue, p.doc.Production.DialogueModel = true, "actnowai/MOSS-TTSD-v1.0-MLX-8bit"
	tt.Frame()
	if !tt.HasText("Dialogue model") || !tt.HasText("Dialogue · One take") {
		t.Errorf("no dialogue model menu: %q", tt.Texts())
	}
	a.sys = &sysInfo{Memory: 16 << 30}
	tt.Frame()
	// 16 GB: SoulX-Podcast and VibeVoice (thread #166), not MOSS-TTSD.
	if !tt.HasText("Dialogue model") || tt.HasText("Dialogue · One take") || !tt.HasText("Dialogue · Long-form") {
		t.Errorf("16 GB dialogue menu: %q", tt.Texts())
	}
	p.doc.Production.Dialogue, p.doc.Production.DialogueModel = false, ""
	tt.Frame()

	// No language model: the director says what to set up.
	a.llmReg.Register(localProvider, nil)
	a.toasts = nil
	a.directStory(p, false)
	if len(a.toasts) != 1 || a.toasts[0] != noDirectorText() || p.directing {
		t.Errorf("no model: %q", a.toasts)
	}

	// With one, it plans the emotions.
	f := &fakeLLM{answer: func(req llm.Request) (string, error) {
		if !strings.Contains(req.Messages[len(req.Messages)-1].Content, "孩子 (voice: 明亮的年轻女声)") {
			t.Errorf("the director does not hear the voices: %s", req.Messages[len(req.Messages)-1].Content)
		}
		return kittenEmotions, nil
	}}
	useFakeLLM(a, f)
	tt.SetSize(1100, 1400)
	tt.Frame()
	if err := tt.Click("AI director"); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		tt.Frame()
	}
	d := &p.doc
	if d.Scene == "" || d.Lines[3].Emotion != services.EmotionExcited || d.Lines[3].Intensity != 3 || d.Cast[0].Baseline != services.EmotionExcited {
		t.Fatalf("not directed: %q %v %d %+v", a.toasts, p.directing, f.calls, d.Lines)
	}
	tt.SetSize(1100, 900)
	// Directing again keeps what the user set; the rest follows it.
	a.clearDirection(p)
	d.Lines[3].Emotion, d.Lines[3].Intensity, d.Lines[3].EmotionByUser = services.EmotionHurt, 1, true
	a.directStory(p, true)
	for range 5 {
		tt.Frame()
	}
	if p.directing || d.Scene != "傍晚的家里，孩子想领养小猫，妈妈犹豫" || d.Lines[1].Emotion != services.EmotionPleading || d.Lines[1].Intensity != 2 ||
		d.Lines[1].Note != "撒娇央求" || d.Lines[3].Emotion != services.EmotionHurt || d.Lines[3].Intensity != 1 {
		t.Fatalf("directed: %+v", *d)
	}
	if !tt.HasText("Pleading · 2") || !tt.HasText("Hurt · 1") || !tt.HasText("4 lines directed") {
		t.Errorf("chips: %q", tt.Texts())
	}
	// The spoken text is untouched; the spec carries the emotions, a steady
	// line in its character's baseline.
	a.compileLf(p)
	l, l0 := p.spec.Chapters[0].Lines[1], p.spec.Chapters[0].Lines[0]
	if l.Spans[0].Text != "妈妈，我想领养一只小猫，可以吗？" || l.Emotion != services.EmotionPleading || l.Intensity != 2 ||
		l0.Emotion != services.EmotionExcited || l0.Intensity != 1 {
		t.Errorf("spec lines %+v %+v", l0, l)
	}
	a.clearDirection(p)
	if storyDirected(d) {
		t.Error("cleared")
	}

	// A line's options edit its emotion; the user's edits are marked.
	p.sel = 1
	tt.Frame()
	if !tt.HasText("Emotion: —") || !tt.HasText("Turn") {
		t.Errorf("line options: %q", tt.Texts())
	}
	d.Lines[1].Emotion = services.EmotionPleading
	tt.Frame()
	if !tt.HasText("Own words") {
		t.Errorf("own words: %q", tt.Texts())
	}

	// A story of the first director keeps its direction row.
	d.Lines[1].Emotion, d.Lines[1].Direction = "", "撒娇央求"
	tt.Frame()
	if !tt.HasText("Direction") || !tt.HasText("Intensity: —") {
		t.Errorf("v1 line options: %q", tt.Texts())
	}

	// A model that takes no instructions: line by line, with the advice,
	// which recommends VoxCPM2 for cloned voices (thread #167) and picks
	// it only when followed.
	d.Model = kokoro
	tt.Frame()
	if !tt.HasText("Line by line") || !tt.HasText("To hear the directions, switch to “Expressive clone · 48 kHz” (recommended: it keeps cloned voices), or to “Standard voices” or “Voice design”.") {
		t.Errorf("kokoro: %q", tt.Texts())
	}
	if d.Model != kokoro {
		t.Fatalf("the advice switched the model: %s", d.Model)
	}
	if err := tt.Click("Use Expressive clone · 48 kHz"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if vox, _ := catalog.Builtin().ByID("voxcpm2-8bit"); d.Model != vox.Repo || p.auto || !tt.HasText("Directed delivery") {
		t.Errorf("use VoxCPM2: model %s auto %v", d.Model, p.auto)
	}
	d.Model = kokoro

	// Production (Advanced): gaps follow the pacing; the fixed gap brings its slider back.
	p.open["scene"] = false
	tt.Frame()
	if !tt.HasText("Gaps between lines follow the pacing: Dialogue (tight) (Performance).") || tt.HasText("Gap between lines 400 ms") {
		t.Errorf("production: %q", tt.Texts())
	}
	d.Production.Pacing = services.PacingFixed
	tt.Frame()
	if !tt.HasText("Gap between lines 400 ms") || tt.HasText("Merge a character's lines") {
		t.Errorf("fixed: %q", tt.Texts())
	}
}

// Auto-cast moves stage directions out of the spoken text.
func TestAutoCastDirections(t *testing.T) {
	var d services.LongformDoc
	n, _ := autoCastText(&d, "妈妈（温柔地）：宝贝，过来。\n孩子：（小声）我可以养它吗？\nMOM (softly): Come here.", nil)
	if n != 3 {
		t.Fatalf("%d lines", n)
	}
	got := fmt.Sprintf("%q/%q %q/%q %q/%q", d.Lines[0].Text, d.Lines[0].Direction, d.Lines[1].Text, d.Lines[1].Direction, d.Lines[2].Text, d.Lines[2].Direction)
	if got != `"宝贝，过来。"/"温柔地" "我可以养它吗？"/"小声" "Come here."/"softly"` {
		t.Errorf("lines %s", got)
	}
}

func TestStoryPerformanceAPI(t *testing.T) {
	e := newStudioEnv(t)
	var p api.LongformJSON
	e.must(t, "create_story", map[string]any{"text": "Kid: I want a kitten.\nKid: Can I?\nMom: Will you look after it?", "model": "qwen3-tts-0.6b-customvoice-8bit",
		"cast": map[string]string{"Kid": "Vivian", "Mom": "Serena"}}, &p)
	if p.Pacing != services.PacingDialogue || p.Continuity != services.ContinuityInstruct || len(p.Lines) != 3 {
		t.Fatalf("story %+v", p)
	}
	id := p.Lines[1].ID
	scene := "A rainy evening"
	e.must(t, "update_longform", map[string]any{"project_id": p.ID, "scene": scene, "personas": map[string]string{"kid": "seven, lively"},
		"directions": map[string]string{id: "pleading"}, "intensities": map[string]int{id: 3}, "pacing": "relaxed", "merge_turns": false}, &p)
	persona := func(name string) string {
		for _, c := range p.Cast {
			if c.Name == name {
				return c.Persona
			}
		}
		return "?"
	}
	if p.Scene != scene || persona("Kid") != "seven, lively" || p.Lines[1].Direction != "pleading" || p.Lines[1].Intensity != 3 || p.Pacing != "relaxed" {
		t.Fatalf("updated %+v", p)
	}
	_, d, err := e.b.loadLongform(t.Context(), p.ID)
	if err != nil || !d.Production.NoMerge || d.Production.NoLevel {
		t.Fatalf("doc %+v %v", d.Production, err)
	}
	e.fails(t, "update_longform", map[string]any{"project_id": p.ID, "pacing": "fast"}, api.CodeInvalid)
	e.fails(t, "update_longform", map[string]any{"project_id": p.ID, "directions": map[string]string{"nope": "x"}}, api.CodeInvalid)
	e.fails(t, "update_longform", map[string]any{"project_id": p.ID, "intensities": map[string]int{id: 4}}, api.CodeInvalid)
	e.fails(t, "update_longform", map[string]any{"project_id": p.ID, "personas": map[string]string{"Ghost": "x"}}, api.CodeInvalid)
	e.fails(t, "update_longform", map[string]any{"project_id": p.ID, "emotions": map[string]string{id: "melancholic-ish"}}, api.CodeInvalid)
	// Emotions per line: the user's, kept by the director.
	id3 := p.Lines[2].ID
	e.must(t, "update_longform", map[string]any{"project_id": p.ID, "emotions": map[string]string{id3: "委屈"}, "intensities": map[string]int{id3: 0}}, &p)
	if p.Lines[2].Emotion != services.EmotionHurt || p.Lines[2].Intensity != 0 {
		t.Fatalf("emotion %+v", p.Lines[2])
	}
	e.onMain(func() { e.a.llmReg.Register(localProvider, nil) })
	e.fails(t, "update_longform", map[string]any{"project_id": p.ID, "direct": "all"}, api.CodeLLMUnavailable)

	// The AI director, through the writing-help model: what the API set is kept.
	e.onMain(func() {
		useFakeLLM(e.a, &fakeLLM{answer: func(llm.Request) (string, error) {
			return `{"scene":"Kitchen, after school","cast":[{"name":"Kid","baseline":"excited"},{"name":"Mom","baseline":"tender"}],
		"lines":[{"n":1,"emotion":"excited","intensity":2,"turn":false,"note":"hopeful"},{"n":2,"emotion":"pleading","intensity":3,"turn":false,"note":"begging"},{"n":3,"emotion":"worried","intensity":1,"turn":false,"note":"gentle doubt"}]}`, nil
		}})
	})
	e.must(t, "update_longform", map[string]any{"project_id": p.ID, "direct": "fill"}, &p)
	if p.Scene != scene || persona("Kid") != "seven, lively" || p.Lines[1].Direction != "pleading" || p.Lines[1].Emotion != "" ||
		p.Lines[0].Emotion != "excited" || p.Lines[0].Intensity != 2 || p.Lines[2].Emotion != services.EmotionHurt {
		t.Fatalf("filled %+v", p)
	}
	e.must(t, "update_longform", map[string]any{"project_id": p.ID, "direct": "all"}, &p)
	if p.Scene != "Kitchen, after school" || p.Lines[1].Direction != "pleading" || p.Lines[2].Emotion != services.EmotionHurt || p.Lines[2].Intensity != 0 {
		t.Fatalf("replaced %+v", p)
	}
	e.onMain(func() {
		useFakeLLM(e.a, &fakeLLM{answer: func(llm.Request) (string, error) { return "", &llm.Error{Code: llm.CodeTimeout} }})
	})
	e.fails(t, "update_longform", map[string]any{"project_id": p.ID, "direct": "all"}, api.CodeDirectorFailed)
	e.fails(t, "update_longform", map[string]any{"project_id": p.ID, "direct": "maybe"}, api.CodeInvalid)

	// Whole dialogue (thread #80): on and off; the dialogue model is set.
	e.must(t, "update_longform", map[string]any{"project_id": p.ID, "dialogue": true}, &p)
	if _, d, err := e.b.loadLongform(t.Context(), p.ID); err != nil || !p.Dialogue || !d.Production.Dialogue || d.Production.DialogueModel != "actnowai/SoulX-Podcast-1.7B-MLX-8bit" {
		t.Fatalf("dialogue %v %+v %v", p.Dialogue, d.Production, err)
	}
	// MOSS-TTSD (thread #82) by name, on a Mac with 32 GB; the choice is kept when it is turned on again.
	savedSys := readSysInfo
	t.Cleanup(func() { readSysInfo = savedSys })
	readSysInfo = func() sysInfo { return sysInfo{Memory: 32 << 30} }
	e.must(t, "update_longform", map[string]any{"project_id": p.ID, "dialogue_model": "moss"}, &p)
	if _, d, _ := e.b.loadLongform(t.Context(), p.ID); p.DialogueModel != "moss" || d.Production.DialogueModel != "actnowai/MOSS-TTSD-v1.0-MLX-8bit" {
		t.Fatalf("dialogue model %q %+v", p.DialogueModel, d.Production)
	}
	e.fails(t, "update_longform", map[string]any{"project_id": p.ID, "dialogue_model": "fish"}, api.CodeInvalid)
	var off api.LongformJSON
	e.must(t, "update_longform", map[string]any{"project_id": p.ID, "dialogue": false}, &off)
	if off.Dialogue || off.DialogueModel != "" {
		t.Error("dialogue still on")
	}
	e.must(t, "update_longform", map[string]any{"project_id": p.ID, "dialogue": true}, &p)
	if p.DialogueModel != "moss" {
		t.Errorf("on again: %q", p.DialogueModel)
	}

	// Audiobooks have no performance, but the dialogue model for their blocks.
	var b api.LongformJSON
	e.must(t, "create_audiobook", map[string]any{"text": "# One\n\nHello."}, &b)
	e.fails(t, "update_longform", map[string]any{"project_id": b.ID, "scene": "x"}, api.CodeInvalid)
	e.must(t, "update_longform", map[string]any{"project_id": b.ID, "dialogue": true}, &b)
	if !b.Dialogue {
		t.Error("an audiobook's dialogue")
	}
	_ = core.ProjectStory
	_ = translate.CodeNoLLM
}

// The dialogue models a Mac may use (thread #82): MOSS-TTSD needs 24 GB.
func TestDialogueChoice(t *testing.T) {
	d := services.NewLongformDoc(core.ProjectStory, "")
	d.Language = "zh"
	name := func(es []catalog.Entry) string {
		var out []string
		for _, e := range es {
			out = append(out, dialogueModelName(e))
		}
		return strings.Join(out, ",")
	}
	if e, ok, why := dialogueChoice(16, &d); why != "" || name(ok) != "soulx,vibevoice" || dialogueModelName(e) != "soulx" {
		t.Errorf("16 GB: %s %s %q", e.ID, name(ok), why)
	}
	d.Production.DialogueModel = "actnowai/MOSS-TTSD-v1.0-MLX-8bit"
	if e, ok, why := dialogueChoice(24, &d); why != "" || name(ok) != "soulx,moss,vibevoice" || dialogueModelName(e) != "moss" {
		t.Errorf("24 GB: %s %s %q", e.ID, name(ok), why)
	}
	if e, _, _ := dialogueChoice(16, &d); dialogueModelName(e) != "soulx" {
		t.Errorf("16 GB with moss chosen: %s", e.ID)
	}
	if _, ok, why := dialogueChoice(8, &d); len(ok) != 0 || why == "" {
		t.Errorf("8 GB: %v %q", name(ok), why)
	}
	if e, ok := dialogueByName("MOSS"); !ok || e.Family != services.FamilyMossTTSD {
		t.Errorf("by name: %+v", e)
	}
	// VibeVoice (thread #166): optional, 16 GB, never the default.
	d.Production.DialogueModel = "microsoft/VibeVoice-1.5B"
	if e, _, why := dialogueChoice(16, &d); why != "" || dialogueModelName(e) != "vibevoice" {
		t.Errorf("16 GB with vibevoice chosen: %s %q", e.ID, why)
	}
	if e, ok := dialogueByName("vibevoice"); !ok || e.Family != services.FamilyVibeVoice {
		t.Errorf("by name: %+v", e)
	}
}
