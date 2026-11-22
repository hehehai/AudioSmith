package voicevox

// The tts.synthesize executor: one chunk of a render, spoken by the
// engine its voice names (/audio_query, the scales, /synthesis), written
// where the render wants it.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

// Job error codes of the executor, besides the protocol's.
const (
	// CodeOffline: the engine the voice names does not answer. Not
	// retryable: the user opens the app.
	CodeOffline = "voicevox_offline"
	// CodeUnknownVoice: the engine has no such character or style (the
	// model was removed, another engine answers at the address).
	CodeUnknownVoice = "voicevox_unknown_voice"
)

// Executor runs tts.synthesize jobs of the connector's model on the
// engines.
func (m *Manager) Executor() jobs.Executor {
	return jobs.ExecutorFunc(func(ctx context.Context, job jobs.Job, report func(jobs.Progress), _ func(json.RawMessage)) (json.RawMessage, error) {
		var p proto.SynthesizeParams
		if err := json.Unmarshal(job.Params, &p); err != nil {
			return nil, &jobs.Error{Code: proto.CodeBadRequest, Message: "params: " + err.Error()}
		}
		res, err := m.Synthesize(ctx, p)
		if err != nil {
			return nil, err
		}
		return json.Marshal(res)
	})
}

// Synthesize speaks p.Text with p.Voice into p.Output.Path.
func (m *Manager) Synthesize(ctx context.Context, p proto.SynthesizeParams) (proto.SynthesizeResult, error) {
	var res proto.SynthesizeResult
	v, ok := ParseVoice(p.Voice)
	if !ok {
		return res, &jobs.Error{Code: proto.CodeBadRequest, Message: fmt.Sprintf("voice %q: pick a character of VOICEVOX or AivisSpeech", p.Voice)}
	}
	if p.Output.Path == "" || !filepath.IsAbs(p.Output.Path) {
		return res, &jobs.Error{Code: proto.CodeBadRequest, Message: "output: an absolute path is required"}
	}
	if strings.TrimSpace(p.Text) == "" {
		return res, &jobs.Error{Code: proto.CodeBadRequest, Message: "text is empty"}
	}
	c := m.client(v.Kind)
	started := time.Now()
	q, err := c.AudioQuery(ctx, p.Text, v.Style)
	if err != nil {
		return res, m.jobError(ctx, v, err)
	}
	ApplyScales(q, p.Speed, p.Scales)
	if _, isAivis := q["tempoDynamicsScale"]; isAivis {
		// AivisSpeech reads the text itself for a natural intonation.
		q["kana"] = p.Text
	}
	data, err := c.Synthesis(ctx, q, v.Style)
	if err != nil {
		return res, m.jobError(ctx, v, err)
	}
	r, err := wav.NewReader(bytes.NewReader(data))
	if err != nil {
		return res, &jobs.Error{Code: proto.CodeInternal, Message: "the engine's audio: " + err.Error()}
	}
	f := r.Format()
	if err := writeFile(p.Output.Path, data); err != nil {
		return res, &jobs.Error{Code: proto.CodeInternal, Message: err.Error()}
	}
	res = proto.SynthesizeResult{Output: p.Output.Path, Model: p.Model, SampleRate: f.Rate, Channels: f.Channels, Frames: r.Frames(),
		Seconds: time.Since(started).Seconds()}
	if f.Rate > 0 {
		res.AudioSeconds = float64(r.Frames()) / float64(f.Rate)
	}
	if res.Seconds > 0 {
		res.RTF = res.AudioSeconds / res.Seconds
	}
	return res, nil
}

// ApplyScales sets an AudioQuery's speedScale from speed and the scales
// that are set (0 = the engine's own), clamped to the ranges the engines
// take.
func ApplyScales(q map[string]any, speed *float64, s *proto.Scales) {
	if speed != nil && *speed > 0 {
		q["speedScale"] = clamp(*speed, 0.5, 2)
	}
	if s == nil {
		return
	}
	if s.Pitch != 0 {
		q["pitchScale"] = clamp(s.Pitch, -0.15, 0.15)
	}
	if s.Intonation != 0 {
		q["intonationScale"] = clamp(s.Intonation, 0, 2)
	}
	if s.Volume != 0 {
		q["volumeScale"] = clamp(s.Volume, 0, 2)
	}
	if _, ok := q["tempoDynamicsScale"]; ok && s.TempoDynamics != 0 {
		q["tempoDynamicsScale"] = clamp(s.TempoDynamics, 0, 2)
	}
}

func clamp(v, lo, hi float64) float64 { return min(max(v, lo), hi) }

// jobError maps a request's failure to the job's error.
func (m *Manager) jobError(ctx context.Context, v Voice, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	brand := v.Kind.Brand()
	data, _ := json.Marshal(map[string]string{"engine": string(v.Kind), "url": m.URL(v.Kind)})
	if errors.Is(err, ErrOffline) {
		return &jobs.Error{Code: CodeOffline, Data: data,
			Message: fmt.Sprintf("%s is not running at %s: open the %s app and try again.", brand, m.URL(v.Kind), brand)}
	}
	var he *HTTPError
	if errors.As(err, &he) {
		switch {
		case he.Status == 404 || he.Status == 422 && strings.Contains(strings.ToLower(he.Detail), "speaker"):
			return &jobs.Error{Code: CodeUnknownVoice, Data: data,
				Message: fmt.Sprintf("%s has no such character or style (%s): pick another voice.", brand, he.Detail)}
		case he.Status >= 500:
			return &jobs.Error{Code: proto.CodeInternal, Data: data, Retryable: true, Message: brand + ": " + he.Error()}
		}
		return &jobs.Error{Code: proto.CodeBadRequest, Data: data, Message: brand + ": " + he.Error()}
	}
	return &jobs.Error{Code: proto.CodeInternal, Data: data, Message: brand + ": " + err.Error()}
}

// writeFile writes data to path through a temporary file beside it, so
// path never holds part of a file.
func writeFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".vv-*.wav")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}
