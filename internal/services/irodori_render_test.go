package services

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/hehehai/AudioSmith/internal/core"
)

// irodoriInfo is Irodori-TTS as the app builds it from its catalogue
// entry (thread #170): 360 TextLength units (120 kana or kanji a call),
// its non-verbal tags, a duration, clones without instructions.
func irodoriInfo(string) ModelInfo {
	caps := []string{"clone", "cloneAudioOnly", "design", "voiceStyle", "duration", "nonVerbal", "seed"}
	return ModelInfo{Traits: TraitsFor("irodori_tts", caps).WithCatalog(360, []string{"[laughter]", "[sigh]", "[breath]"}), Revision: "r3"}
}

// The engine gets Irodori's tags (it turns them into emoji), Japanese in
// chunks of at most 120 characters, and the take's duration.
func TestRenderIrodori(t *testing.T) {
	ref := t.TempDir() + "/ref.wav"
	writeWAV(t, ref, 24000, 1)
	e := newRenderEnv(t, irodoriInfo)
	j, tk := e.generate(t, core.Take{Text: "そうなんだ [laughter] 本当に？ [question-en]", Model: "m/irodori", RefAudio: ref, Duration: 3})
	if j.Err != nil {
		t.Fatalf("%+v", j.Err)
	}
	got := e.synth.calls
	if len(got) != 1 || !strings.Contains(got[0].Text, "[laughter]") || strings.Contains(got[0].Text, "[question-en]") ||
		got[0].Duration == nil || *got[0].Duration != 3 || got[0].Instruct != "" {
		t.Fatalf("irodori got %+v", got)
	}
	if len(tk.Render.Warnings) != 1 || tk.Render.Warnings[0].Code != "plan.nonverbal_removed" {
		t.Errorf("warnings %+v", tk.Render.Warnings)
	}

	var long string // 12 different sentences, 252 characters
	for _, n := range strings.Split("一二三四五六七八九十百千", "") {
		long += "駅前の小さな喫茶店は、毎朝" + n + "時に開店する。"
	}
	l := newRenderEnv(t, irodoriInfo)
	l.generate(t, core.Take{Text: long, Model: "m/irodori", RefAudio: ref})
	calls := l.synth.calls
	if len(calls) < 3 {
		t.Fatalf("%d chunks", len(calls))
	}
	var joined string
	for _, c := range calls {
		if n := utf8.RuneCountInString(c.Text); n > 120 {
			t.Errorf("chunk of %d characters", n)
		}
		joined += c.Text
	}
	if strings.ReplaceAll(joined, " ", "") != long {
		t.Errorf("chunks lost text: %q", joined)
	}
}
