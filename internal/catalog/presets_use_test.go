package catalog

import (
	"slices"
	"testing"
)

func entryIDs(es []Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.ID)
	}
	return out
}

// TestRecommendByUse (thread #167): VoxCPM2 is recommended, before
// OmniVoice (non-commercial), for languages Qwen3-TTS doesn't speak, for
// directed clones in Stories and for Chinese dialect design; the
// languages presets list, and every default, stay as they were.
func TestRecommendByUse(t *testing.T) {
	c := Builtin()
	const vox, omni = "voxcpm2-8bit", "omnivoice-bf16"
	for _, w := range []struct {
		w    Workflow
		ui   string
		lang []string
		uses []Use
		want []string
	}{
		// Languages outside Qwen3-TTS's ten.
		{WorkflowStudio, "en", []string{"vi"}, nil, []string{vox, omni}},
		{WorkflowStudio, "zh-Hans", []string{"th"}, nil, []string{vox, omni}},
		{WorkflowStudio, "en", []string{"hi"}, nil, []string{"kokoro-82m-bf16", vox, omni}}, // Kokoro speaks Hindi but doesn't clone
		{WorkflowDub, "zh-Hans", []string{"ar"}, nil, []string{vox, omni}},
		{WorkflowDub, "en", []string{"ja", "vi"}, nil, []string{vox, omni}},
		{WorkflowDesign, "zh-Hans", []string{"id"}, nil, []string{vox, omni}},
		{WorkflowStory, "en", []string{"nl"}, nil, []string{vox, omni}},
		{WorkflowAudiobook, "zh-Hans", []string{"sw"}, nil, []string{vox, omni}},
		{WorkflowDub, "en", []string{"zu"}, nil, []string{omni}}, // VoxCPM2 doesn't speak Zulu
		// Languages the lists already serve: unchanged.
		{WorkflowStudio, "zh-Hans", []string{"ja"}, nil, []string{"qwen3-tts-0.6b-customvoice-8bit", "qwen3-tts-1.7b-base-8bit"}},
		{WorkflowStudio, "en", []string{"en"}, nil, []string{"kokoro-82m-bf16", "qwen3-tts-1.7b-base-8bit", "chatterbox-turbo-8bit"}},
		{WorkflowDub, "zh-Hans", []string{"en", "", "auto"}, nil, []string{"qwen3-tts-0.6b-base-8bit", "qwen3-tts-1.7b-base-8bit"}},
		// Uses.
		{WorkflowStory, "zh-Hans", nil, []Use{UseDirectedClone}, []string{vox, "qwen3-tts-1.7b-base-8bit", "qwen3-tts-0.6b-customvoice-8bit"}},
		{WorkflowDesign, "zh-Hans", []string{"zh"}, []Use{UseDialect}, []string{vox, "qwen3-tts-1.7b-voicedesign-4bit"}},
		{WorkflowStudio, "zh-Hans", nil, []Use{UseDialect}, []string{"qwen3-tts-0.6b-customvoice-8bit", "qwen3-tts-1.7b-base-8bit"}}, // no dialect use there
		{WorkflowDesign, "en", []string{"vi"}, []Use{UseDialect}, []string{vox, omni}},
	} {
		if got := entryIDs(c.RecommendedFor(w.w, w.ui, w.lang, w.uses...)); !slices.Equal(got, w.want) {
			t.Errorf("RecommendedFor(%s, %s, %v, %v) = %v, want %v", w.w, w.ui, w.lang, w.uses, got, w.want)
		}
	}
	// Recommended for an unlisted language covers it the same way.
	if got := entryIDs(c.Recommended(WorkflowStudio, "vi")); !slices.Equal(got, []string{vox, omni}) {
		t.Errorf("Recommended(studio, vi) = %v", got)
	}
	// VoxCPM2 is never a default, in any language.
	for _, l := range []string{"zh-Hans", "en", "vi", "th", "ar", "hi"} {
		for _, w := range []Workflow{WorkflowStudio, WorkflowStory, WorkflowAudiobook, WorkflowDub, WorkflowDesign} {
			for _, hv := range []bool{false, true} {
				if e, _ := c.DefaultModel(w, l, hv); e.ID == vox || e.ID == omni {
					t.Errorf("DefaultModel(%s, %s, %v) = %s", w, l, hv, e.ID)
				}
			}
		}
	}
}
