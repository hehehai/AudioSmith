package enginetest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

// tts.dialogue in the fake engine: each generated turn is a tone (0.15 s a
// character, the speaker's own pitch) in <outputDir>/turn-NNN.wav, with its
// text in turn-NNN.wav.txt so the fake's stt.transcribe reads it back. A
// turn whose text contains FakeGarbleMark is written as gibberish when the
// job's seed is even (the app's per-turn check fails it; a redo with
// another seed passes). A model with "one-track" or "moss" in its name
// (MOSS-TTSD: up to 5 speakers) writes the whole dialogue to dialogue.wav
// instead and returns the turns' texts and codec frames (16 codes each).
// One with "vibevoice" in its name (VibeVoice 1.5B, thread #166) does too,
// with up to 4 speakers who need no transcript, and without codes: turns
// with codes (a continuation) are refused.

// FakeGarbleMark makes the fake dialogue garble a turn on even seeds.
const FakeGarbleMark = "〔含糊〕"

func (f *fakeEngine) dialogue(m proto.Message) {
	var p proto.DialogueParams
	if err := m.DecodeParams(&p); err != nil || p.Model == "" {
		f.fail(m.ID, proto.CodeBadRequest, "Missing model")
		return
	}
	lower := strings.ToLower(p.Model)
	vibe := strings.Contains(lower, "vibevoice")
	oneTrack := strings.Contains(lower, "one-track") || strings.Contains(lower, "moss") || vibe
	if !strings.Contains(lower, "soulx") && !strings.Contains(lower, "dialogue") && !oneTrack {
		f.failData(m.ID, proto.CodeUnsupported, map[string]string{"capability": "dialogue"}, "%s cannot speak dialogues", p.Model)
		return
	}
	maxSpeakers := 4
	if oneTrack && !vibe {
		maxSpeakers = 5
	}
	if len(p.Speakers) > maxSpeakers {
		f.failData(m.ID, proto.CodeUnsupported, map[string]string{"capability": "speakers"}, "%s takes at most %d speakers", p.Model, maxSpeakers)
		return
	}
	pitch := map[string]float64{}
	for i, s := range p.Speakers {
		if s.RefText == "" && !vibe {
			f.fail(m.ID, proto.CodeBadRequest, "Speaker %s needs the reference's transcript (refText)", s.ID)
			return
		}
		if !f.inputOK(m.ID, s.RefAudio) {
			return
		}
		pitch[s.ID] = 180 + 60*float64(i)
	}
	for i, t := range p.Turns {
		if _, ok := pitch[t.Speaker]; !ok {
			f.fail(m.ID, proto.CodeBadRequest, "Turn %d names an unknown speaker %s", i, t.Speaker)
			return
		}
		if vibe && len(t.Codes) > 0 {
			f.failData(m.ID, proto.CodeUnsupported, map[string]string{"capability": "continuation"}, "%s cannot go on from earlier turns", p.Model)
			return
		}
	}
	if !f.ensure(m.ID, p.Model) || !f.work(m.ID, p.Model, "generating", nil) {
		return
	}
	if err := os.MkdirAll(p.OutputDir, 0o755); err != nil {
		f.fail(m.ID, proto.CodeInternal, "%v", err)
		return
	}
	seed := int64(0)
	if p.Seed != nil {
		seed = *p.Seed
	}
	res := proto.DialogueResult{Model: p.Model, SampleRate: 24000, Channels: 1, Turns: []proto.DialogueTurnOut{}, Seed: p.Seed, Seconds: 0.05}
	if oneTrack {
		all := Audio{Rate: 24000, Channels: 1}
		for i, t := range p.Turns {
			if len(t.Codes) > 0 {
				continue
			}
			a := Tone(24000, 1, 0.15*float64(len([]rune(t.Text))), pitch[t.Speaker], 0.25)
			all.Samples = append(all.Samples, a.Samples...)
			res.Turns = append(res.Turns, proto.DialogueTurnOut{Index: i, Speaker: t.Speaker, Text: t.Text})
		}
		out := filepath.Join(p.OutputDir, "dialogue.wav")
		if err := WriteWAV(out, all, p.Bits); err != nil {
			f.fail(m.ID, proto.CodeInternal, "%v", err)
			return
		}
		codes := make([]int, (all.Frames()/1920+1)*16)
		for k := range codes {
			codes[k] = (k*37 + int(seed)) % 1024
		}
		res.Mixed = &proto.DialogueMixed{Output: out, Frames: int64(all.Frames()), AudioSeconds: all.Seconds(), Codes: codes, Codebooks: 16}
		if vibe {
			res.Mixed.Codes, res.Mixed.Codebooks = nil, 0
		}
		res.AudioSeconds = all.Seconds()
		res.RTF = res.AudioSeconds / res.Seconds
		f.touch(p.Model)
		f.result(m.ID, res)
		return
	}
	for i, t := range p.Turns {
		if len(t.Codes) > 0 {
			continue
		}
		n := len([]rune(t.Text))
		a := Tone(24000, 1, 0.15*float64(n), pitch[t.Speaker], 0.25)
		out := filepath.Join(p.OutputDir, fmt.Sprintf("turn-%03d.wav", i))
		if err := WriteWAV(out, a, p.Bits); err != nil {
			f.fail(m.ID, proto.CodeInternal, "Could not write %s: %v", out, err)
			return
		}
		said := strings.ReplaceAll(t.Text, FakeGarbleMark, "")
		if strings.Contains(t.Text, FakeGarbleMark) && seed%2 == 0 {
			said = "嗯嗯啊啊哦哦呀呀"
		}
		os.WriteFile(out+".txt", []byte(said), 0o644)
		codes := make([]int, 0, n*4)
		for k := range n * 4 {
			codes = append(codes, (k*37+i*101+int(seed))%6561)
		}
		f.notify(proto.MethodTTSTurn, proto.TurnParams{Job: m.ID, Index: i, Speaker: t.Speaker, Output: out,
			Frames: int64(a.Frames()), AudioSeconds: a.Seconds(), SampleRate: 24000})
		res.Turns = append(res.Turns, proto.DialogueTurnOut{Index: i, Speaker: t.Speaker, Output: out, Frames: int64(a.Frames()),
			AudioSeconds: a.Seconds(), Codes: codes, Seconds: 0.01})
		res.AudioSeconds += a.Seconds()
	}
	res.RTF = res.AudioSeconds / res.Seconds
	f.touch(p.Model)
	f.result(m.ID, res)
}
