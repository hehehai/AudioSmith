package services

// The references of the dub's dialogue model (thread #89, plan §2.4):
// what each voice of a stretch is cloned from.
//
//   - A speaker's own voice (Auto): 10–14 s of their lines in the source
//     (dub.DialogueRefLines: their clone reference's lines first, then
//     their longest), joined with short gaps, cleaned of noise when the
//     reference checker finds it noisy (as the references stage does), and
//     the source's transcript of them (a1: even across languages, P0
//     thread #86 found the dialogue model keeps to the target language).
//     A speaker without such lines keeps their clone reference.
//   - A saved cloned voice: its own clip and transcript; a designed voice:
//     its reference (thread #83).
//   - Any other voice (built-in, preset, the default): it says a reference
//     text (PortableLine) on the dub's model first.
//
// The runner tidies every one (−18 LUFS, pauses shortened) before the
// model hears it.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/refcheck"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/text"
)

// dialogueRef is the reference of seg's voice v in a stretch.
func (s *Dubs) dialogueRef(sc *stageCtx, seg dub.Segment, v dub.VoiceSpec, caps DubModelCaps) (dialogueRef, error) {
	p := sc.p
	hash := func(parts ...any) string {
		h := sha256.Sum256([]byte(fmt.Sprint(parts...)))
		return hex.EncodeToString(h[:8])
	}
	clip := func(audio, txt string) dialogueRef {
		// The clip by its path in the project and its content, not its
		// absolute path and time: a copied project keeps its keys.
		key := s.keyPath(p.ID, audio)
		if b, err := os.ReadFile(audio); err == nil {
			key = fmt.Sprintf("%s|%d|%s", key, len(b), hash(string(b)))
		}
		return dialogueRef{audio: audio, text: txt, key: key, tidy: sc.path(filepath.Join("refs", "dlg-"+hash(key)+".ref.wav"))}
	}
	switch {
	case v.Auto:
		return s.dialogueOwnRef(sc, seg.Speaker, clip)
	case v.RefAudio != "" && v.RefText != "":
		return clip(v.RefAudio, v.RefText), nil
	case v.CloneRef != "" && v.CloneRefText != "":
		return clip(v.CloneRef, v.CloneRefText), nil
	}
	// Said on the dub's model first.
	line := PortableLine(baseLang(sc.params.Lang))
	t := core.Take{Text: line, Model: dubFirst(v.Model, p.Settings.Model), Voice: v.Voice, VoicePrompt: v.VoicePrompt, Seed: v.Seed,
		Language: dubFirst(v.Language, langValue(caps, sc.params.Lang)), Render: &core.TakeRender{Bits: 32, SeedPinned: v.Seed != 0}}
	if caps.Instruct && v.Instruct != "" {
		if t.Voice != "" {
			t.Voice += ", " + v.Instruct
		} else {
			t.Voice = v.Instruct
		}
	}
	key := hash("made", t.Model, v.Voice, v.VoicePrompt, v.Instruct, v.Seed, t.Language, line)
	raw := sc.path(filepath.Join("refs", "dlg-made-"+key+".wav"))
	return dialogueRef{audio: sc.path(filepath.Join("refs", "dlg-made-"+key+".ref.wav")), text: line, key: "made:" + key,
		made: func(ctx context.Context) (string, error) {
			if fi, err := os.Stat(raw); err == nil && fi.Size() > 44 {
				return raw, nil
			}
			params, err := json.Marshal(RenderParams{Take: t, Lang: sc.params.Lang, Output: raw, NoWatermark: true, Workflow: "dub"})
			if err != nil {
				return "", err
			}
			sub := jobs.Job{ID: sc.job.ID + ".ref-" + key, Spec: jobs.Spec{Kind: KindRender, Lane: jobs.LaneGPU, Params: params, Owner: sc.job.Owner}}
			if _, err := s.cfg.Render.render.Run(ctx, sub, func(jobs.Progress) {}, nil); err != nil {
				return "", err
			}
			return raw, nil
		}}, nil
}

// dialogueOwnRef is a speaker's own reference: 10–14 s of their lines,
// joined (made when the stretch is), else their clone reference. It is
// kept by its lines and their text: a line moved (its timing edited)
// keeps it, so the stretch is not made again for it.
func (s *Dubs) dialogueOwnRef(sc *stageCtx, speaker string, clip func(audio, txt string) dialogueRef) (dialogueRef, error) {
	p := sc.p
	r, hasRef := p.References[speaker]
	var refp *dub.Reference
	if hasRef {
		refp = &r
	}
	lines := dub.DialogueRefLines(p.Segments, speaker, refp)
	if len(lines) == 0 {
		if !hasRef {
			return dialogueRef{}, stageErr(DubCodeFailed, "%s has no voice sample to clone", speaker)
		}
		return clip(sc.path(r.File), r.Text), nil
	}
	src := dubAudio
	if p.Media.Vocals != "" {
		src = dubVocals
	}
	var spans []dsp.Range
	parts := []any{"own", src}
	for _, i := range lines {
		g := p.Segments[i]
		spans = append(spans, dsp.Range{Start: g.Start, End: g.End})
		parts = append(parts, g.ID, g.Text) // not the times: a line moved keeps its reference
	}
	h := sha256.Sum256([]byte(fmt.Sprint(parts...)))
	key := hex.EncodeToString(h[:8])
	name := filepath.Join("refs", "dlg-"+dub.SafeName(speaker)+"-"+key)
	txt := dub.DialogueRefText(p.Segments, lines)
	m := s.cfg.Models()
	return dialogueRef{audio: sc.path(name + ".ref.wav"), text: txt, key: "own:" + key,
		made: func(ctx context.Context) (string, error) {
			raw, clean := sc.path(name+".wav"), sc.path(name+".clean.wav")
			if fi, err := os.Stat(clean); err == nil && fi.Size() > 44 {
				return clean, nil
			}
			if err := cutJoin(sc.path(src), raw, spans, dub.DialogueRefGap); err != nil {
				return "", err
			}
			b, _, err := wav.ReadFile(raw)
			if err != nil {
				return "", err
			}
			w, ch := text.Count(txt)
			if rep := refcheck.Measure(b, refcheck.Options{Units: max(w, ch), CJK: ch > w, CER: -1}); !rep.Noisy() || m.Enhance == "" {
				return raw, nil
			}
			// Noisy (music and effects, a room): cleaned, as the references stage does.
			ep := proto.EnhanceParams{Model: m.Enhance, Audio: raw, Output: proto.Output{Path: clean, Format: "wav", Bits: 16}}
			var er proto.EnhanceResult
			if err := sc.call(ctx, proto.MethodEnhanceRun, m.Enhance, &ep, &ep.ModelPath, &er, nil); err != nil {
				if ctx.Err() != nil {
					return "", ctx.Err()
				}
				return raw, nil
			}
			return clean, nil
		}}, nil
}

// cutJoin writes the spans of src (a WAV) to dst as mono, gap seconds of
// silence between them.
func cutJoin(src, dst string, spans []dsp.Range, gap float64) error {
	in, err := dub.OpenWAV(src, 0, 1)
	if err != nil {
		return err
	}
	defer in.Close()
	rate, _ := in.Format()
	var out []float32
	for k, sp := range spans {
		if k > 0 {
			out = append(out, make([]float32, dsp.FramesAt(gap, rate))...)
		}
		if err := in.SeekFrame(int64(sp.Start * float64(rate))); err != nil {
			return err
		}
		n := max(int((sp.End-sp.Start)*float64(rate)), 0)
		buf := make([]float32, n)
		got := 0
		for got < n {
			k, err := in.Read(buf[got:])
			got += k
			if err != nil || k == 0 {
				break
			}
		}
		b := dsp.Buffer{Rate: rate, Channels: 1, Samples: buf[:got]}
		dsp.FadeIn(b, dsp.FramesAt(0.01, rate), dsp.EqualPower)
		dsp.FadeOut(b, dsp.FramesAt(0.01, rate), dsp.EqualPower)
		out = append(out, b.Samples...)
	}
	tmp := dst + ".tmp"
	if err := wav.WriteFile(tmp, dsp.Buffer{Rate: rate, Channels: 1, Samples: out}, wav.PCM24, wav.Options{}); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
