package services

// The dub's transcribe and references stages: the transcript (the
// downloaded captions, or speech to text with word timings, then
// diarization for the speakers) and each speaker's clone reference;
// split from dub_stages.go.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/refcheck"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/text"
	"github.com/hehehai/AudioSmith/internal/translate"
)

// speechFile is the 16 kHz audio speech models listen to: the separated
// voices when there are.
func speechFile(p *dub.Project) string {
	if p.Media.Vocals != "" {
		return dubVocals16k
	}
	return dubSpeech
}

// transcribe makes the transcript: the downloaded captions, or speech to
// text with word timings; then diarization gives the lines speakers.
func (s *Dubs) transcribe(ctx context.Context, sc *stageCtx) error {
	p := sc.p
	m := s.cfg.Models()
	speech := sc.path(speechFile(p))
	q := p.Clone()
	q.Segments, q.Names, q.NextSeg = nil, nil, 1
	detected := ""
	labelled := false // the speakers model labelled the lines
	if p.Source.Captions != "" && !sc.params.Force {
		data, err := os.ReadFile(p.Source.Captions)
		if err == nil {
			q.ImportSubtitles(string(data), p.Source.Duration)
			sc.warn(DubWarnCaptions)
		}
	}
	if len(q.Segments) == 0 {
		if m.STT == "" {
			return stageErr(proto.CodeModelNotDownloaded, "No speech-to-text model.")
		}
		stt, wordTimes := m.STTFor(p.SourceLang)
		var tr proto.TranscribeResult
		call := func(model string) func(k, n int, tp *proto.TranscribeParams, tr *proto.TranscribeResult) error {
			return func(k, n int, tp *proto.TranscribeParams, tr *proto.TranscribeResult) error {
				return sc.call(ctx, proto.MethodSTTTranscribe, model, tp, &tp.ModelPath, tr, func(pg jobs.Progress) {
					pg.Stage = "transcribing"
					pg.Fraction = windowFraction(pg.Fraction, k, n)
					sc.report(pg)
				})
			}
		}
		// More than one speaker: the speakers model transcribes and tells
		// them apart (thread #48); Sortformer below only when it is not
		// downloaded.
		if spk := m.SpeakersFor(p.SourceLang); spk != "" && p.Speakers != 1 {
			tp := proto.TranscribeParams{Model: spk, Audio: speech, Language: p.SourceLang, Prompt: glossaryPrompt(p.Glossary)}
			res, err := TranscribeSpeakers(ctx, call(spk), tp, p.Source.Duration)
			switch {
			case ctx.Err() != nil:
				return ctx.Err()
			case missing(err):
				sc.warn(DubWarnSpeakersModel)
			case err != nil:
				return err
			default:
				tr, stt, wordTimes, labelled = res, spk, false, true
			}
		}
		if !labelled {
			tp := proto.TranscribeParams{Model: stt, Audio: speech, Language: p.SourceLang, Words: wordTimes}
			if err := call(stt)(0, 1, &tp, &tr); err != nil {
				return err
			}
		}
		if tr.AudioSeconds <= 0 {
			tr.AudioSeconds = p.Source.Duration
		}
		CleanTranscription(stt, &tr)
		detected = tr.Language
		words := segmentWords(tr.Segments)
		if len(words) == 0 && m.Align != "" && strings.TrimSpace(tr.Text) != "" {
			ap := proto.AlignParams{Model: m.Align, Audio: speech, Text: strings.TrimSpace(tr.Text), Language: dubFirst(tr.Language, p.SourceLang)}
			aw, err := alignWords(ctx, func(ap *proto.AlignParams, out *proto.AlignResult) error {
				return sc.call(ctx, proto.MethodSTTAlign, m.Align, ap, &ap.ModelPath, out, func(pg jobs.Progress) {
					pg.Stage = "aligning"
					sc.report(pg)
				})
			}, ap, tr.Segments, tr.AudioSeconds)
			switch {
			case ctx.Err() != nil:
				return ctx.Err()
			case err != nil:
				sc.warn(WarnNoWordTimes)
			default:
				words = aw
			}
		}
		for _, ts := range BuildSegments(tr.Segments, words, p.Source.Duration) {
			if strings.TrimSpace(ts.Text) == "" {
				continue
			}
			seg := q.NewSegment(ts.Start, ts.End, ts.Speaker, strings.TrimSpace(ts.Text))
			for _, w := range ts.Words {
				seg.Words = append(seg.Words, dub.Word{Text: w.Text, Start: w.Start, End: w.End})
			}
			q.Segments = append(q.Segments, seg)
		}
	}
	if len(q.Segments) == 0 {
		return stageErr(DubCodeNoSpeech, "No speech was found.")
	}
	// Who speaks.
	var turns []dub.Turn
	if labelled {
		// S1, S2… in order of appearance already; a line the model gave
		// no label is the speaker's before it.
		last := "S1"
		for i := range q.Segments {
			if q.Segments[i].Speaker == "" {
				q.Segments[i].Speaker = last
			}
			last = q.Segments[i].Speaker
		}
	} else if p.Speakers != 1 && !slices.ContainsFunc(q.Segments, func(s dub.Segment) bool { return s.Speaker != "" && q.Names != nil }) {
		if m.Diarize == "" {
			sc.warn(DubWarnNoDiarization)
		} else {
			dp := dub.DiarizeParams{Model: m.Diarize, Audio: speech, Speakers: p.Speakers}
			var dr dub.DiarizeResult
			err := sc.call(ctx, dub.MethodDiarize, m.Diarize, &dp, &dp.ModelPath, &dr, func(pg jobs.Progress) {
				pg.Stage = "diarizing"
				sc.report(pg)
			})
			switch {
			case ctx.Err() != nil:
				return ctx.Err()
			case code(err) == proto.CodeUnsupported:
				sc.warn(DubWarnTooManySpk)
			case missing(err):
				sc.warn(DubWarnNoDiarization)
			case err != nil:
				return err
			default:
				turns = dr.Turns
			}
		}
	}
	if q.Names == nil && !labelled {
		q.AssignSpeakers(turns)
	}
	return sc.save(ctx, func(p *dub.Project) {
		p.Segments, p.NextSeg, p.Names = q.Segments, q.NextSeg, q.Names
		if detected != "" {
			p.Detected = detected
		}
		p.AutoRetarget() // a defaulted target is not the spoken language
		// A new transcript: translations, renders and references go.
		p.Tracks, p.References, p.RefTried = nil, nil, nil
		for k := range p.Stages {
			if !strings.HasPrefix(k, string(dub.StageIngest)) && !strings.HasPrefix(k, string(dub.StageSeparate)) &&
				!strings.HasPrefix(k, string(dub.StageFetch)) && !strings.HasPrefix(k, string(dub.StageTranscribe)) {
				delete(p.Stages, k)
			}
		}
	})
}

// glossaryPrompt is the vocabulary a speakers model expects: the
// glossary's source terms the user entered (not the ones extracted from
// an earlier transcript), at most 40.
func glossaryPrompt(g translate.Glossary) string {
	var terms []string
	for _, t := range g {
		if src := strings.TrimSpace(t.Source); src != "" && !t.Auto && !slices.Contains(terms, src) && len(terms) < 40 {
			terms = append(terms, src)
		}
	}
	return strings.Join(terms, ", ")
}

// references picks each speaker's clone reference, checks it, denoises it
// when it is noisy and tidies it.
func (s *Dubs) references(ctx context.Context, sc *stageCtx) error {
	p := sc.p
	m := s.cfg.Models()
	scoring, err := wav.Open(sc.path(speechFile(p))) // read a candidate window at a time
	if err != nil {
		return err
	}
	defer scoring.Close()
	src := dubAudio
	if p.Media.Vocals != "" {
		src = dubVocals
	}
	cands, err := dub.PickReferences(p.Segments, scoring)
	if err != nil {
		return err
	}
	refs := map[string]dub.Reference{}
	labels := p.SpeakerLabels()
	if err := os.MkdirAll(sc.path("refs"), 0o755); err != nil {
		return err
	}
	for i, sp := range labels {
		c, ok := cands[sp]
		if !ok {
			sc.warn(DubWarnNoReference)
			continue
		}
		sc.progress("references", float64(i)/float64(len(labels)), sp)
		name := filepath.Join("refs", dub.SafeName(sp)+".wav")
		if err := cutClip(sc.path(src), sc.path(name), c.Start, c.End); err != nil {
			return err
		}
		ref := dub.Reference{File: name, Start: c.Start, End: c.End, Text: c.Text, Score: c.Score}
		// Checked (thread #83): denoised only when noisy (the source's music
		// and effects, a noisy room), then tidied like every reference.
		b, _, err := wav.ReadFile(sc.path(name))
		if err != nil {
			return err
		}
		w, ch := text.Count(c.Text)
		rep := refcheck.Measure(b, refcheck.Options{Units: max(w, ch), CJK: ch > w, CER: -1})
		ref.Grade = string(rep.Grade(refcheck.UseClone))
		for _, h := range rep.Hints(refcheck.UseClone) {
			ref.Hints = append(ref.Hints, h.Code)
		}
		if rep.Noisy() && m.Enhance != "" {
			clean := filepath.Join("refs", dub.SafeName(sp)+".clean.wav")
			ep := proto.EnhanceParams{Model: m.Enhance, Audio: sc.path(name), Output: proto.Output{Path: sc.path(clean), Format: "wav", Bits: 16}}
			var er proto.EnhanceResult
			err := sc.call(ctx, proto.MethodEnhanceRun, m.Enhance, &ep, &ep.ModelPath, &er, func(pg jobs.Progress) {
				pg.Stage = "enhancing"
				sc.report(pg)
			})
			switch {
			case ctx.Err() != nil:
				return ctx.Err()
			case err == nil:
				ref.File, ref.Enhanced = clean, true
			case missing(err):
				sc.warn(DubWarnNoEnhance)
			default:
				return err
			}
		}
		tidy := filepath.Join("refs", dub.SafeName(sp)+".ref.wav")
		if err := tidyReference(sc.path(ref.File), sc.path(tidy)); err == nil {
			ref.File, ref.Tidied = tidy, true
		}
		refs[sp] = ref
	}
	return sc.save(ctx, func(p *dub.Project) {
		p.References, p.RefTried = refs, labels
	})
}

// cutClip writes start–end of src (a WAV) to dst as mono.
func cutClip(src, dst string, start, end float64) error {
	in, err := dub.OpenWAV(src, 0, 1)
	if err != nil {
		return err
	}
	defer in.Close()
	rate, _ := in.Format()
	if err := in.SeekFrame(int64(start * float64(rate))); err != nil {
		return err
	}
	n := max(int((end-start)*float64(rate)), 0)
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
	tmp := dst + ".tmp"
	if err := wav.WriteFile(tmp, b, wav.PCM24, wav.Options{}); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
