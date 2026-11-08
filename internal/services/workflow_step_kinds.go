package services

// The kinds of workflow steps, one method each: source, recipe, render,
// speak, speech to text, transcribe, translate, convert, normalize and
// export (with write, which saves a value in the export's format); split
// from workflow_steps.go.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/loudness"
	"github.com/hehehai/AudioSmith/internal/dsp/stretch"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/text"
	"github.com/hehehai/AudioSmith/internal/translate"
	"github.com/hehehai/AudioSmith/internal/workflow"
)

// source reads a clip into the run's folder as a WAV.
func (st *wfStep) source(ctx context.Context, ri *workflow.RunItem, in *workflow.Value) (wfResult, error) {
	if in.File == "" {
		return wfResult{}, wfErr(WorkflowCodeNoInput, "no clip")
	}
	name := st.out(ri.Key, "wav")
	if err := st.decode(ctx, in.File, st.path(name), 0, 0); err != nil {
		return wfResult{}, err
	}
	v, err := st.audioValue(name)
	return wfResult{out: v}, err
}

// recipe is the take a Speak or Convert step renders for text.
func (st *wfStep) recipe(ctx context.Context, txt, lang string) (core.Take, error) {
	p := st.n.P
	caps := st.s.cfg.Caps(p.Model)
	t := core.Take{Text: txt, Model: p.Model, Render: &core.TakeRender{Bits: 32}}
	if lang != "" {
		t.Language = langValue(caps, lang)
	}
	if p.Voice != "" && st.s.cfg.Voice != nil {
		v, err := st.s.cfg.Voice(ctx, p.Voice, p.Model, lang)
		if err != nil {
			return t, wfErr(WorkflowCodeVoice, "%v", err)
		}
		t.Voice, t.RefAudio, t.RefText, t.VoicePrompt, t.Seed = v.Voice, v.RefAudio, v.RefText, v.VoicePrompt, v.Seed
		if v.Language != "" {
			t.Language = v.Language
		}
		if v.Instruct != "" && caps.Instruct && (t.RefAudio == "" || caps.CloneInstruct) {
			t.Voice = strings.Trim(t.Voice+", "+v.Instruct, ", ")
		}
		if v.Seed != 0 {
			t.Render.SeedPinned = true
		}
	}
	if caps.Speed && p.Speed > 0 && p.Speed != 1 {
		t.Speed = p.Speed
	}
	return t, nil
}

// render speaks a take into name through the render pipeline.
func (st *wfStep) render(ctx context.Context, t core.Take, lang, name string) error {
	if st.s.cfg.Render == nil || st.s.cfg.Render.render == nil {
		return wfErr(CodeEngineUnavailable, "The render pipeline is not connected.")
	}
	out := st.path(name)
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	t.ID = core.NewID(st.s.cfg.Now())
	params, err := json.Marshal(RenderParams{Take: t, Lang: lang, Output: out})
	if err != nil {
		return err
	}
	sub := jobs.Job{ID: st.job.ID + "." + filepath.Base(filepath.Dir(name)), Spec: jobs.Spec{Kind: KindRender, Lane: jobs.LaneGPU, Params: params, Owner: st.job.Owner}}
	_, err = st.s.cfg.Render.render.Run(ctx, sub, func(pg jobs.Progress) {}, nil)
	return err
}

// speak renders the item's text.
func (st *wfStep) speak(ctx context.Context, ri *workflow.RunItem, in *workflow.Value) (wfResult, error) {
	txt, err := st.readText(in)
	if err != nil {
		return wfResult{}, err
	}
	if strings.TrimSpace(txt) == "" {
		return wfResult{}, wfErr(CodeNothingToSay, "There is nothing to say.")
	}
	lang := dubFirst(st.n.P.Language, in.Lang)
	t, err := st.recipe(ctx, txt, lang)
	if err != nil {
		return wfResult{}, err
	}
	name := st.out(ri.Key, "wav")
	if err := st.render(ctx, t, lang, name); err != nil {
		return wfResult{}, err
	}
	v, err := st.audioValue(name)
	if err != nil {
		return wfResult{}, err
	}
	v.Lang = lang
	return wfResult{out: v}, nil
}

// stt transcribes an audio value: a 16 kHz copy, speech to text, word
// times (the aligner when the model has none).
func (st *wfStep) stt(ctx context.Context, item string, in *workflow.Value, language string) (core.Transcript, []string, error) {
	m := st.s.cfg.STT()
	model, wordTimes := m.For(language)
	if model == "" {
		return core.Transcript{}, nil, wfErr(WorkflowCodeNoSTT, "No speech-to-text model.")
	}
	speech := st.path("out/" + item + "/" + st.n.ID + ".16k.wav")
	defer os.Remove(speech)
	if err := st.decode(ctx, st.path(in.File), speech, TranscriptRate, 1); err != nil {
		return core.Transcript{}, nil, err
	}
	report := func(pg jobs.Progress) {}
	tp := proto.TranscribeParams{Model: model, Audio: speech, Language: language, Words: wordTimes}
	var tr proto.TranscribeResult
	if err := st.call(ctx, proto.MethodSTTTranscribe, model, &tp, &tp.ModelPath, &tr, report); err != nil {
		return core.Transcript{}, nil, err
	}
	if tr.AudioSeconds <= 0 {
		tr.AudioSeconds = in.Seconds
	}
	CleanTranscription(model, &tr)
	var warnings []string
	words := segmentWords(tr.Segments)
	if len(words) == 0 && m.Aligner != "" && strings.TrimSpace(tr.Text) != "" {
		ap := proto.AlignParams{Model: m.Aligner, Audio: speech, Text: strings.TrimSpace(tr.Text), Language: dubFirst(tr.Language, language)}
		aw, err := alignWords(ctx, func(ap *proto.AlignParams, out *proto.AlignResult) error {
			return st.call(ctx, proto.MethodSTTAlign, m.Aligner, ap, &ap.ModelPath, out, report)
		}, ap, tr.Segments, tr.AudioSeconds)
		switch {
		case ctx.Err() != nil:
			return core.Transcript{}, nil, ctx.Err()
		case err != nil:
			warnings = append(warnings, WarnNoWordTimes)
		default:
			words = aw
		}
	}
	lang := tr.Language
	if lang == "" || lang == "auto" {
		lang = language
	}
	secs := in.Seconds
	if tr.AudioSeconds > 0 {
		secs = tr.AudioSeconds
	}
	now := st.s.cfg.Now()
	t := core.Transcript{ID: item, CreatedAt: now, UpdatedAt: now, Source: core.SourceFile, Model: model, Language: lang,
		Duration: secs, Raw: strings.TrimSpace(tr.Text), Seconds: tr.Seconds}
	t.Segments = BuildSegments(tr.Segments, words, secs)
	if strings.TrimSpace(t.Preferred()) == "" {
		return t, warnings, wfErr(WorkflowCodeNoSpeech, "No speech was found.")
	}
	return t, warnings, nil
}

// transcribe makes a transcript of the item's audio.
func (st *wfStep) transcribe(ctx context.Context, ri *workflow.RunItem, in *workflow.Value) (wfResult, error) {
	t, warnings, err := st.stt(ctx, ri.Key, in, st.n.P.From)
	if err != nil {
		return wfResult{}, err
	}
	t.Title = ri.Name
	v, err := st.transcriptValue(ri.Key, t)
	return wfResult{out: v, warnings: warnings}, err
}

// translate translates a text (by paragraphs) or a transcript (by
// segments, keeping their times).
func (st *wfStep) translate(ctx context.Context, ri *workflow.RunItem, in *workflow.Value) (wfResult, error) {
	p := st.n.P
	if st.s.cfg.Translator == nil {
		return wfResult{}, wfErr(translate.CodeNoLLM, "No translator is set up.")
	}
	tr, err := st.s.cfg.Translator(ctx, p.Translator)
	if err != nil {
		var te *translate.Error
		if errors.As(err, &te) {
			return wfResult{}, wfErr(te.Code, "%v", err)
		}
		return wfResult{}, wfErr(translate.CodeConfig, "%v", err)
	}
	src := dubFirst(p.From, in.Lang)
	ask := func(segs []translate.Segment) ([]string, error) {
		res, err := tr.Translate(ctx, translate.Request{Source: src, Target: p.To, Segments: segs, Quality: translate.Fast})
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			var te *translate.Error
			if errors.As(err, &te) {
				return nil, wfErr(te.Code, "%v", err)
			}
			return nil, wfErr(translate.CodeNetwork, "%v", err)
		}
		byID := map[string]string{}
		for _, t := range res.Segments {
			if t.Err != nil {
				return nil, wfErr(translate.CodeLineFailed, "%v", t.Err)
			}
			byID[t.ID] = t.Text
		}
		out := make([]string, len(segs))
		for i, s := range segs {
			t, ok := byID[s.ID]
			if !ok {
				return nil, wfErr(translate.CodeMissing, "a line came back missing")
			}
			out[i] = t
		}
		return out, nil
	}
	if in.Type == workflow.TypeTranscript {
		t, err := st.readTranscript(in)
		if err != nil {
			return wfResult{}, err
		}
		var segs []translate.Segment
		var idx []int
		for i, sg := range t.Segments {
			if strings.TrimSpace(sg.Text) != "" {
				segs = append(segs, translate.Segment{ID: fmt.Sprint(i), Text: sg.Text, Speaker: sg.Speaker, Slot: sg.End - sg.Start})
				idx = append(idx, i)
			}
		}
		if len(segs) == 0 {
			return wfResult{}, wfErr(WorkflowCodeEmpty, "Nothing to translate.")
		}
		got, err := ask(segs)
		if err != nil {
			return wfResult{}, err
		}
		for k, i := range idx {
			t.Segments[i].Text, t.Segments[i].Words, t.Segments[i].Edited = got[k], nil, true
		}
		t.Language, t.Clean, t.CleanBy = p.To, "", ""
		var parts []string
		for _, sg := range t.Segments {
			if s := strings.TrimSpace(sg.Text); s != "" {
				parts = append(parts, s)
			}
		}
		t.Raw = strings.Join(parts, " ")
		v, err := st.transcriptValue(ri.Key, t)
		return wfResult{out: v}, err
	}
	txt, err := st.readText(in)
	if err != nil {
		return wfResult{}, err
	}
	var segs []translate.Segment
	for i, para := range text.Paras(txt) {
		if s := strings.TrimSpace(txt[para.Start:para.End]); s != "" {
			segs = append(segs, translate.Segment{ID: fmt.Sprint(i), Text: s})
		}
	}
	if len(segs) == 0 {
		return wfResult{}, wfErr(WorkflowCodeEmpty, "Nothing to translate.")
	}
	got, err := ask(segs)
	if err != nil {
		return wfResult{}, err
	}
	v, err := st.textValue(ri.Key, strings.Join(got, "\n\n"), p.To)
	return wfResult{out: v}, err
}

// convert says the item's words again in the step's voice.
func (st *wfStep) convert(ctx context.Context, ri *workflow.RunItem, in *workflow.Value) (wfResult, error) {
	p := st.n.P
	t, warnings, err := st.stt(ctx, ri.Key, in, p.Language)
	if err != nil {
		return wfResult{}, err
	}
	lang := dubFirst(p.Language, t.Language)
	take, err := st.recipe(ctx, t.Preferred(), lang)
	if err != nil {
		return wfResult{}, err
	}
	caps := st.s.cfg.Caps(p.Model)
	if p.MatchDuration && caps.Duration && in.Seconds > 0.3 {
		take.Duration = math.Round(in.Seconds*100) / 100
	}
	name := st.out(ri.Key, "wav")
	if err := st.render(ctx, take, lang, name); err != nil {
		return wfResult{}, err
	}
	if p.MatchDuration && take.Duration == 0 && in.Seconds > 0.3 {
		// The model has no duration control: time-stretch to the source's
		// length (within the stretcher's limits).
		b, _, err := wav.ReadFile(st.path(name))
		if err != nil {
			return wfResult{}, wfErr(WorkflowCodeFile, "%v", err)
		}
		if b.Seconds() > 0 {
			b = stretch.Stretch(b, in.Seconds/b.Seconds(), stretch.Options{})
			if err := writeWAVFloat(st.path(name), b); err != nil {
				return wfResult{}, err
			}
		}
	}
	v, err := st.audioValue(name)
	if err != nil {
		return wfResult{}, err
	}
	v.Lang = lang
	return wfResult{out: v, warnings: warnings}, nil
}

// Warnings of steps.
const (
	WorkflowWarnSilent   = "workflow.silent"    // normalize: the audio is silent, kept as it is
	WorkflowWarnNoFolder = "workflow.no_folder" // export: no folder, nothing written
)

// normalize brings the audio to the step's loudness or peak.
func (st *wfStep) normalize(ctx context.Context, ri *workflow.RunItem, in *workflow.Value) (wfResult, error) {
	b, _, err := wav.ReadFile(st.path(in.File))
	if err != nil {
		return wfResult{}, wfErr(WorkflowCodeFile, "%v", err)
	}
	if err := ctx.Err(); err != nil {
		return wfResult{}, err
	}
	p := st.n.P
	var warnings []string
	peak := float64(dsp.Peak(b.Samples))
	switch {
	case peak <= 1e-6:
		warnings = append(warnings, WorkflowWarnSilent)
	case p.Level == workflow.LevelPeak:
		dsp.Gain(b.Samples, float32(dsp.Amp(p.Target-dsp.DB(peak))))
	default:
		b, _ = loudness.Normalize(b, loudness.Target{ID: "workflow", Integrated: p.Target, MinIntegrated: p.Target - 1,
			MaxIntegrated: p.Target + 1, TruePeak: -1}, loudness.NormalizeOptions{})
	}
	name := st.out(ri.Key, "wav")
	if err := os.MkdirAll(filepath.Dir(st.path(name)), 0o755); err != nil {
		return wfResult{}, err
	}
	if err := writeWAVFloat(st.path(name), b); err != nil {
		return wfResult{}, err
	}
	v, err := st.audioValue(name)
	if err != nil {
		return wfResult{}, err
	}
	v.Lang = in.Lang
	return wfResult{out: v, warnings: warnings}, nil
}

// export writes the item's value into the step's folder.
func (st *wfStep) export(ctx context.Context, ri *workflow.RunItem, index int, in *workflow.Value) (wfResult, error) {
	p := st.n.P
	if strings.TrimSpace(p.Folder) == "" {
		out := *in
		return wfResult{out: &out, warnings: []string{WorkflowWarnNoFolder}}, nil
	}
	format := workflow.ExportFormat(p.Format, in.Type)
	name := workflow.FileName(p.Template, workflow.NameVars{Workflow: st.w.Name, N: index + 1, Item: ri.Name,
		Step: st.n.StepName(st.n.Kind.Label()), Date: st.s.cfg.Now(), Lang: in.Lang})
	dst := filepath.Join(p.Folder, name+"."+format)
	if err := st.s.write(ctx, st.w.ID, in, format, dst, st.call); err != nil {
		return wfResult{}, err
	}
	out := *in
	out.File = dst
	return wfResult{out: &out}, nil
}

// write saves a value as format at dst (absolute), atomically.
func (s *Workflows) write(ctx context.Context, id string, v *workflow.Value, format, dst string,
	call func(ctx context.Context, method, model string, params any, modelPath *string, out any, report func(jobs.Progress)) error) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return wfErr(WorkflowCodeFile, "%v", err)
	}
	src := s.Path(id, v.File)
	switch v.Type {
	case workflow.TypeAudio:
		switch format {
		case workflow.FormatM4A:
			if call == nil {
				return wfErr(WorkflowCodeEncode, "M4A needs the engine.")
			}
			tmp := dst + ".tmp.m4a"
			ep := proto.EncodeParams{Input: src, Output: proto.Output{Path: tmp, Format: "m4a"}}
			var er json.RawMessage
			if err := call(ctx, proto.MethodMediaEncode, "", &ep, nil, &er, nil); err != nil {
				os.Remove(tmp)
				if c := code(err); c == proto.CodeMethodNotFound || c == proto.CodeUnsupported {
					return wfErr(WorkflowCodeEncode, "This engine can't write M4A: %v", err)
				}
				return err
			}
			return os.Rename(tmp, dst)
		default:
			return ConvertWAV(ctx, src, dst, 24)
		}
	case workflow.TypeTranscript:
		data, err := os.ReadFile(src)
		if err != nil {
			return wfErr(WorkflowCodeFile, "%v", err)
		}
		var t core.Transcript
		if err := json.Unmarshal(data, &t); err != nil {
			return wfErr(WorkflowCodeFile, "%v", err)
		}
		out, err := Export(t, format)
		if err != nil {
			return err
		}
		return wfWriteFile(dst, out)
	default:
		data, err := os.ReadFile(src)
		if err != nil {
			return wfErr(WorkflowCodeFile, "%v", err)
		}
		if !bytes.HasSuffix(data, []byte("\n")) {
			data = append(data, '\n')
		}
		return wfWriteFile(dst, data)
	}
}
