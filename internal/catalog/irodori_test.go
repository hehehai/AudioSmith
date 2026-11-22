package catalog

import (
	"slices"
	"testing"
)

// TestIrodori: Irodori-TTS v4.1 MF (thread #170), an optional Japanese
// model: MIT with the author's ethical terms stated, 48 kHz, Japanese
// only, offered but no workflow's default or recommendation.
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
	for _, w := range []Workflow{WorkflowStudio, WorkflowStory, WorkflowAudiobook, WorkflowDub, WorkflowDesign} {
		for _, ui := range []string{"zh-Hans", "en"} {
			for _, r := range c.RecommendedFor(w, ui, []string{"ja"}) {
				if r.ID == e.ID {
					t.Errorf("recommended for %s (%s)", w, ui)
				}
			}
		}
	}
}
