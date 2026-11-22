package catalog

import (
	"slices"
	"testing"
)

// TestIrodori: Irodori-TTS v4.1 MF (thread #170), an optional Japanese
// model: MIT with the author's ethical terms stated, 48 kHz, Japanese
// only, offered but no workflow's default (TestIrodoriRecommended).
func TestIrodori(t *testing.T) {
	c := Builtin()
	e, ok := c.ByID("irodori-tts-v4.1-small-mf-8bit")
	if !ok || e.Hidden || e.Family != "irodori_tts" || e.SampleRate != 48000 || !e.Licence.Commercial || e.Licence.RequiresAcceptance {
		t.Fatalf("%+v", e)
	}
	if len(e.Revision) != 40 || e.Repo != "mlx-community/Irodori-TTS-v4.1-Small-MF-8bit" {
		t.Errorf("repo %s@%s", e.Repo, e.Revision)
	}
	for _, cp := range []Capability{CapClone, CapCloneAudioOnly, CapDesign, CapVoiceStyle, CapDuration, CapNonVerbal, CapSeed} {
		if !e.Has(cp) {
			t.Errorf("no %s", cp)
		}
	}
	// No instruct: stories' directions are English or Chinese, its caption Japanese.
	if e.Has(CapInstruct) || e.Has(CapLanguage) || e.Has(CapStream) || len(e.Voices) > 0 {
		t.Errorf("capabilities %v", e.Capabilities)
	}
	if len(e.Languages) != 1 || e.Languages[0].Code != "ja" || e.Knows("zh") || e.Knows("en") || !e.Knows("ja") {
		t.Errorf("languages %v", e.Languages)
	}
	var sum int64
	for _, f := range e.Files {
		sum += f.Size
		if f.Size > 1<<20 && len(f.SHA256) != 64 {
			t.Errorf("%s has no sha256", f.Path)
		}
	}
	if sum != e.Size {
		t.Errorf("size %d, files %d", e.Size, sum)
	}
	if !slices.Contains(e.NonVerbal, "[laughter]") || !slices.Contains(e.NonVerbal, "[sigh]") {
		t.Errorf("non-verbal %v", e.NonVerbal)
	}
}

// TestIrodoriRecommended (user 11-22): recommended first for Japanese
// content in the Studio, Stories and audiobooks; not in dubs or design,
// not for content that is not all Japanese; never a default.
func TestIrodoriRecommended(t *testing.T) {
	c := Builtin()
	const id = "irodori-tts-v4.1-small-mf-8bit"
	for _, ui := range []string{"zh-Hans", "en"} {
		for _, w := range []Workflow{WorkflowStudio, WorkflowStory, WorkflowAudiobook, WorkflowDub, WorkflowDesign} {
			for _, langs := range [][]string{{"ja"}, {"ja-JP"}, {"ja", "zh"}, {"zh"}, {"en"}, nil} {
				got := entryIDs(c.RecommendedFor(w, ui, langs))
				want := w != WorkflowDub && w != WorkflowDesign && len(langs) == 1 && BaseLanguage(langs[0]) == "ja"
				if has := slices.Contains(got, id); has != want || want && got[0] != id {
					t.Errorf("RecommendedFor(%s, %s, %v) = %v", w, ui, langs, got)
				}
				if len(got) > 3 {
					t.Errorf("RecommendedFor(%s, %s, %v): %d models", w, ui, langs, len(got))
				}
			}
		}
	}
	// The dub's list for Japanese stays the language list.
	if got := entryIDs(c.RecommendedFor(WorkflowDub, "zh-Hans", []string{"ja"})); !slices.Equal(got, []string{"qwen3-tts-0.6b-base-8bit", "qwen3-tts-1.7b-base-8bit"}) {
		t.Errorf("dub ja %v", got)
	}
	for _, l := range []string{"zh-Hans", "en", "ja"} {
		for _, w := range []Workflow{WorkflowStudio, WorkflowStory, WorkflowAudiobook, WorkflowDub, WorkflowDesign} {
			for _, hv := range []bool{false, true} {
				if e, _ := c.DefaultModel(w, l, hv); e.ID == id {
					t.Errorf("DefaultModel(%s, %s, %v) = %s", w, l, hv, e.ID)
				}
			}
		}
	}
}
