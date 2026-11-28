package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
)

// dialogueKeyChapter is a story chapter with whole dialogue: a portable
// reference (the kid, said on the story's model), a cloned voice's clip
// (mom) and a designed voice's reference (dad); the clips in
// testdata, at a fixed time (their keys hold the path, size and time).
func dialogueKeyChapter(model string, tags bool) ChapterParams {
	line := func(id, who, text, emotion string, in int) LineSpec {
		v := strings.ToLower(who)
		if who == "" {
			v = "narrator"
		}
		return LineSpec{ID: id, Character: who, Emotion: emotion, Intensity: in, Spans: []SpanSpec{{Voice: v, Character: who, Text: text}}}
	}
	prod := DefaultStoryProduction()
	prod.Dialogue, prod.DialogueModel, prod.DialogueTags = true, model, tags
	return ChapterParams{Render: "r", Index: 2, Model: "m/qwen", Lang: "zh", Seed: 7, Output: "/tmp/x.wav", Production: prod,
		Voices: map[string]core.Take{"kid": {Voice: "Vivian"}, "mom": {RefAudio: dialogueFixture("mom.wav"), RefText: "今天晚上我们早点吃饭。"},
			"dad": {Voice: "Uncle_Fu", CloneRef: dialogueFixture("dad-ref.wav"), CloneRefText: "今天放学的时候，我看见一只小鸟。"}, "narrator": {Voice: "Serena"}},
		Lines: []LineSpec{
			line("n", "", "那天晚上，一家人吃完饭坐在客厅里，窗外下着小雨。", EmotionSteady, 0),
			line("a", "Kid", "妈妈，我想领养一只小猫，可以吗？", EmotionHappy, 2), line("b", "Mom", "养猫要花很多时间照顾它。", EmotionWorried, 1),
			line("c", "Kid", "我能！我每天喂它。", EmotionExcited, 3), line("x", "", "她小声说。", EmotionSteady, 0),
			line("d", "Dad", "那我们先去救助站看看吧。", EmotionSad, 2),
			line("m", "", "第二天早上，太阳升起来了，孩子背着书包高高兴兴地去了学校，一路上都在想小猫。", EmotionSteady, 0),
			line("e", "Mom", "路上小心。", EmotionSteady, 0), line("f", "Kid", "知道啦！", EmotionHappy, 1),
		}}
}

// dialogueFixture is a clip of testdata/dialogue, dated 2026-10-18.
func dialogueFixture(name string) string {
	p := "testdata/dialogue/" + name
	at := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(p, at, at); err != nil {
		panic(err)
	}
	return p
}

// dialogueKeyDigest is everything of a chapter's dialogue plan that names
// a file in the cache: the chapter key, and per dialogue line its
// segment's key, file, seed, language, references (key, text, files) and
// the text the model is given.
func dialogueKeyDigest(t *testing.T, model string, tags bool) (string, string) {
	t.Helper()
	models := func(repo string) ModelInfo {
		switch repo {
		case "m/soulx":
			return ModelInfo{Traits: TraitsFor("soulx_podcast", []string{"dialogue"}), Revision: "rs"}
		case "m/moss":
			return ModelInfo{Traits: TraitsFor(FamilyMossTTSD, []string{"dialogue"}), Revision: "rm"}
		}
		return cvInfo(repo)
	}
	p := dialogueKeyChapter(model, tags)
	r := &renderer{cacheDir: filepath.Join(t.TempDir(), "cache"), models: models, logf: t.Logf,
		engine: (&fakeAnalysis{}).executor, aligner: "m/aligner", recognizer: "m/asr"}
	steps, key, _, err := chapterRenderer{r}.program(context.Background(), &p, r.model(p.Model), "")
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "chapter %s\n", key)
	for _, s := range steps {
		if s.dialogue == nil {
			continue
		}
		j, k := dialogueDigestOf(s.dialogue)
		fmt.Fprintf(&b, "line %d: %s %d\n", s.line, j, k)
	}
	h := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(h[:]), b.String()
}

// The dialogue cache keys of a story are byte for byte as main made them
// before the shared dialogue layer (229786f): its segments' files are
// found in the cache, its chapters reused.
func TestDialogueKeysAsBefore(t *testing.T) {
	for _, c := range []struct {
		model string
		tags  bool
		want  string
	}{
		{"m/soulx", false, "b0f400cf4b8a2db367ab5ef33db5ef5b0e2eaf80eb800f5da0eb84bdd7f7dd6a"},
		{"m/soulx", true, "8539a778163a5737f3ef385c0c23243350b917afbf84f36204d0f5bfdba428a4"},
		{"m/moss", true, "aff0fa1ba567533712ff1ef42455bef7e02177abb1b627bdfeb5e59b1e0bdbc3"},
	} {
		got, text := dialogueKeyDigest(t, c.model, c.tags)
		if got != c.want {
			t.Errorf("%s tags %v: digest %s, want %s (as before)\n%s", c.model, c.tags, got, c.want, text)
		}
	}
}
