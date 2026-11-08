package services

import (
	"testing"

	"github.com/hehehai/AudioSmith/internal/dub"
)

// TestLineTakeCloneInstruct: a cloned line's direction reaches models
// whose clones take instructions (VoxCPM2, thread #165) and no others.
func TestLineTakeCloneInstruct(t *testing.T) {
	p := &dub.Project{ID: "p1", Settings: dub.Settings{Model: "m/vox"}}
	seg := dub.Segment{ID: "s1", Speaker: "S1", Text: "hello", Direction: "whispered"}
	v := dub.VoiceSpec{RefAudio: "/ref.wav", RefText: "ref"}
	if got := LineTake(p, seg, "es", v, DubModelCaps{Clone: true, Instruct: true, CloneInstruct: true}); got.Voice != "whispered" || got.RefAudio != "/ref.wav" {
		t.Errorf("clone instruct: %+v", got)
	}
	if got := LineTake(p, seg, "es", v, DubModelCaps{Clone: true}); got.Voice != "" {
		t.Errorf("plain clone: %+v", got)
	}
	if got := LineTake(p, seg, "es", v, DubModelCaps{Clone: true, Instruct: true}); got.Voice != "" {
		t.Errorf("instruct model's clone: %+v", got)
	}
}
