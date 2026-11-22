package services

// A render's chunks and its running state: planning the chunks
// (renderer.chunks), then for each one the cache or the engine, edges,
// joins and the streamed WAV (renderState); split from render.go.

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/edit"
	"github.com/hehehai/AudioSmith/internal/dsp/loudness"
	"github.com/hehehai/AudioSmith/internal/dsp/resample"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/text"
	"github.com/hehehai/AudioSmith/internal/text/plan"
)

// chunks makes the engine calls of the plan's speech, with their cache
// keys and their files in dir (the chunk cache); extra adds to every key
// (longform's "vary repeated lines").
func (r *renderer) chunks(p *RenderParams, pl *plan.Plan, info ModelInfo, modelPath string, seed *int64, dir string, extra map[string]string) ([]renderChunk, error) {
	t := p.Take
	info.FillDefaults(&t, p.Workflow)
	speech := pl.Speech()
	totalChars := 0
	for _, i := range speech {
		totalChars += text.RuneLen(pl.Segments[i].Text)
	}
	// A target duration is shared by the chunks by their length, after
	// the pauses.
	speechSeconds := 0.0
	if t.Duration > 0 {
		speechSeconds = max(t.Duration-float64(pl.PauseMillis())/1000, 0.5*float64(len(speech)))
	}
	ref := ""
	if t.RefAudio != "" {
		fi, err := os.Stat(t.RefAudio)
		if err != nil {
			return nil, fmt.Errorf("reference clip: %w", err)
		}
		ref = t.RefAudio + "|" + strconv.FormatInt(fi.Size(), 10) + "|" + strconv.FormatInt(fi.ModTime().UnixNano(), 10)
	}
	out := make([]renderChunk, 0, len(speech))
	for _, i := range speech {
		seg := pl.Segments[i]
		chars := text.RuneLen(seg.Text)
		sp := proto.SynthesizeParams{
			Model: t.Model, ModelPath: modelPath, Text: seg.Text, Voice: t.Voice, Language: t.Language,
			MaxTokens: t.MaxTokens, RefAudio: t.RefAudio, RefText: t.RefText, Steps: t.Steps, Seed: seed,
			VoicePrompt: t.VoicePrompt,
		}
		scales := ""
		if sc := t.Scales; sc != nil && (sc.Pitch != 0 || sc.Intonation != 0 || sc.Volume != 0 || sc.TempoDynamics != 0) {
			sp.Scales = &proto.Scales{Pitch: sc.Pitch, Intonation: sc.Intonation, Volume: sc.Volume, TempoDynamics: sc.TempoDynamics}
			scales = fmt.Sprintf("%g/%g/%g/%g", sc.Pitch, sc.Intonation, sc.Volume, sc.TempoDynamics)
		}
		if t.Guidance != 0 {
			sp.Guidance = &t.Guidance
		}
		if t.Exaggeration != 0 {
			sp.Exaggeration = &t.Exaggeration
		}
		speed := t.Speed
		if seg.Speed != 1 && seg.Speed > 0 {
			if speed == 0 {
				speed = 1
			}
			speed *= seg.Speed
		}
		if speed != 0 {
			sp.Speed = &speed
		}
		if t.Temperature != 0 {
			sp.Temperature = &t.Temperature
		}
		if t.TopP != 0 {
			sp.TopP = &t.TopP
		}
		if speechSeconds > 0 && totalChars > 0 {
			d := math.Round(speechSeconds*float64(chars)/float64(totalChars)*100) / 100
			sp.Duration = &d
		}
		f := func(v *float64) string {
			if v == nil {
				return ""
			}
			return strconv.FormatFloat(*v, 'g', -1, 64)
		}
		keyParams := map[string]string{
			"model": t.Model, "revision": info.Revision, "voice": t.Voice, "language": t.Language,
			"speed": f(sp.Speed), "temperature": f(sp.Temperature), "topP": f(sp.TopP),
			"maxTokens": strconv.Itoa(t.MaxTokens), "steps": strconv.Itoa(t.Steps), "duration": f(sp.Duration),
			"ref": ref, "refText": t.RefText, "bits": "32",
			"guidance": f(sp.Guidance), "exaggeration": f(sp.Exaggeration), "voicePrompt": t.VoicePrompt,
		}
		if scales != "" {
			keyParams["scales"] = scales // only when set: other models' keys stay as they were
		}
		if seed != nil && !info.Deterministic {
			keyParams["seed"] = strconv.FormatInt(*seed, 10)
		}
		for k, v := range extra {
			keyParams[k] = v
		}
		key := text.Key(seg.Text, keyParams)
		path := filepath.Join(dir, key+".wav")
		sp.Output = proto.Output{Path: path, Format: "wav", Bits: 32}
		out = append(out, renderChunk{seg: seg, params: sp, key: key, path: path, chars: chars})
	}
	return out, nil
}

// renderState is one render in progress: the joined audio streams to a
// float WAV in the temporary directory, metered for mastering.
type renderState struct {
	r        *renderer
	job      jobs.Job
	p        *RenderParams
	report   func(jobs.Progress)
	chunkOut func(json.RawMessage)

	total, done int // chars of all chunks, of chunks finished

	rate, channels int
	joiner         *edit.Joiner
	tmp            *wav.FileWriter
	tmpPath        string
	meter          *loudness.Meter // integrated loudness of the joined audio
	frames         int64
	pending        []byte // pcm16 not yet streamed
	seq            int
	files          []string
	leading        float64 // seconds of [pause] before the first speech

	// Longform renders (longform_render.go): the gaps between sentences,
	// lines and paragraphs (nil: edit.DefaultGaps), and whether the
	// engine's edge silence is kept.
	gaps     *edit.GapPolicy
	keepEdge bool
}

func (st *renderState) seconds() float64 {
	if st.rate == 0 {
		return 0
	}
	return float64(st.frames) / float64(st.rate)
}

func (st *renderState) cleanup() {
	if st.tmp != nil {
		st.tmp.Abort()
	}
	for _, f := range st.files {
		os.Remove(f)
	}
}

// synthesize makes chunk i (1-based) of n, or finds it in the cache.
func (st *renderState) synthesize(ctx context.Context, i, n int, c renderChunk) (proto.SynthesizeResult, bool, error) {
	var res proto.SynthesizeResult
	if fi, err := os.Stat(c.path); err == nil && fi.Size() > 44 {
		now := time.Now()
		os.Chtimes(c.path, now, now) // most recently used
		st.done += c.chars
		return res, true, nil
	}
	synth := st.r.synth
	var ex jobs.Executor
	if synth != nil {
		ex = synth()
	}
	if ex == nil {
		return res, false, &jobs.Error{Code: CodeEngineUnavailable, Message: "The engine is not connected.", Retryable: true}
	}
	params, err := json.Marshal(c.params)
	if err != nil {
		return res, false, err
	}
	sub := jobs.Job{ID: st.job.ID + "." + strconv.Itoa(i), Spec: jobs.Spec{Kind: proto.MethodTTSSynthesize, Lane: jobs.LaneGPU, Params: params, Owner: st.job.Owner}}
	base := st.seconds()
	report := func(pr jobs.Progress) {
		frac := 0.0
		if pr.Fraction > 0 {
			frac = pr.Fraction
		}
		stage := "generating"
		if pr.Stage == "loading" {
			stage = "loading"
		}
		fraction := -1.0
		if st.total > 0 && (pr.Fraction >= 0 || n > 1) {
			fraction = (float64(st.done) + frac*float64(c.chars)) / float64(st.total)
		}
		st.report(jobs.Progress{Stage: stage, Fraction: min(fraction, 1), AudioSeconds: base + pr.AudioSeconds,
			Message: fmt.Sprintf("chunk %d/%d", i, n)})
	}
	report(jobs.Progress{Stage: "generating", Fraction: -1})
	raw, err := ex.Run(ctx, sub, report, nil)
	if err != nil {
		if ctx.Err() != nil {
			return res, false, ctx.Err()
		}
		var je *jobs.Error
		if errors.As(err, &je) {
			// The engine's data, plus which chunk and how long it is.
			fields := map[string]any{}
			json.Unmarshal(je.Data, &fields)
			fields["chunk"], fields["chunks"], fields["chars"] = i, n, c.chars
			data, _ := json.Marshal(fields)
			msg := je.Message
			if n > 1 {
				msg = fmt.Sprintf("Chunk %d of %d: %s", i, n, je.Message)
			}
			return res, false, &jobs.Error{Code: je.Code, Message: msg, Data: data, Retryable: je.Retryable}
		}
		return res, false, err
	}
	json.Unmarshal(raw, &res)
	st.done += c.chars
	return res, false, nil
}

// joinFor is the join before a segment, with the default gaps.
func joinFor(b plan.Break) edit.Join { return joinWith(edit.DefaultGaps, b) }

// joinWith is the join before a segment with gaps g.
func joinWith(g edit.GapPolicy, b plan.Break) edit.Join {
	switch b {
	case plan.BreakChunk:
		return g.Join(edit.Chunk)
	case plan.BreakSentence:
		return g.Join(edit.Sentence)
	case plan.BreakLine:
		return g.Join(edit.Line)
	case plan.BreakParagraph:
		return g.Join(edit.Paragraph)
	}
	return edit.Join{}
}

// open starts the joined stream at the first chunk's format.
func (st *renderState) open(rate, channels int) error {
	st.rate, st.channels = rate, channels
	st.tmpPath = filepath.Join(st.r.tmpDir(), st.job.ID+"-joined.wav")
	w, err := wav.Create(st.tmpPath, wav.Format{Rate: rate, Channels: channels, Encoding: wav.Float32}, wav.Options{Large: true})
	if err != nil {
		return err
	}
	st.tmp = w
	st.meter = loudness.NewMeter(rate, channels, loudness.Options{SkipTruePeak: true})
	st.joiner = edit.NewJoiner(rate, channels, edit.DefaultCrossfade, st.sink)
	return nil
}

// sink receives finished joined audio.
func (st *renderState) sink(s []float32) error {
	if err := st.tmp.Write(s); err != nil {
		return err
	}
	st.meter.Write(s)
	st.frames += int64(len(s) / st.channels)
	if st.p.Stream && st.chunkOut != nil {
		for _, v := range s {
			v = min(max(v, -1), 1)
			st.pending = binary.LittleEndian.AppendUint16(st.pending, uint16(int16(v*32767)))
		}
	}
	return nil
}

// flushStream sends the audio joined so far to the preview, in pieces of
// at most about 5 s.
func (st *renderState) flushStream(final bool) {
	if !st.p.Stream || st.chunkOut == nil {
		return
	}
	piece := max(st.rate*st.channels*2*5, 2)
	for len(st.pending) > 0 || final {
		n := min(len(st.pending), piece)
		c := proto.ChunkParams{Job: st.job.ID, Seq: st.seq, SampleRate: st.rate, Channels: st.channels,
			Encoding: "pcm16le", Data: st.pending[:n], Final: final && n == len(st.pending)}
		raw, err := json.Marshal(c)
		if err == nil {
			st.chunkOut(raw)
		}
		st.seq++
		st.pending = st.pending[n:]
		if c.Final {
			break
		}
	}
	st.pending = st.pending[:0:0]
}

// silence appends a [pause].
func (st *renderState) silence(seconds float64) error {
	if st.joiner == nil {
		// A pause before any speech: the format is not known yet; keep it.
		st.leading += seconds
		return nil
	}
	_, err := st.joiner.Silence(seconds)
	st.flushStream(false)
	return err
}

// appendChunk reads a chunk's audio, trims its edges and joins it. It
// returns where the chunk landed (seconds) and how much of it is sound.
func (st *renderState) appendChunk(path string, b plan.Break) (start, end, voiced float64, err error) {
	buf, trimmed, _, err := st.loadChunk(path)
	if err != nil {
		return 0, 0, 0, err
	}
	start, end, err = st.appendLoaded(buf, trimmed, b)
	return start, end, trimmed.Seconds(), err
}

// loadChunk reads a chunk's audio in the joined stream's format (which
// the first chunk sets) and trims its edges: lead is how many frames
// went from its start.
func (st *renderState) loadChunk(path string) (buf, trimmed dsp.Buffer, lead int, err error) {
	buf, _, err = wav.ReadFile(path)
	if err != nil {
		return buf, buf, 0, err
	}
	if buf.Frames() == 0 || buf.Rate <= 0 {
		return buf, buf, 0, fmt.Errorf("%s: no audio", filepath.Base(path))
	}
	if st.joiner == nil {
		if err := st.open(buf.Rate, buf.Channels); err != nil {
			return buf, buf, 0, err
		}
		if st.leading > 0 {
			if _, err := st.joiner.Silence(st.leading); err != nil {
				return buf, buf, 0, err
			}
		}
	}
	if buf.Rate != st.rate || buf.Channels != st.channels {
		buf = resample.Convert(buf, st.rate, st.channels, resample.Good)
	}
	lead, _ = edit.TrimRange(buf, edit.TrimOptions{})
	return buf, edit.Trim(buf, edit.TrimOptions{}), lead, nil
}

// appendLoaded joins a loaded chunk (as trimmed, or whole when the edges
// are kept) and returns where it landed (seconds).
func (st *renderState) appendLoaded(buf, trimmed dsp.Buffer, b plan.Break) (start, end float64, err error) {
	if st.keepEdge {
		trimmed = buf
	}
	join := joinFor(b)
	if st.gaps != nil {
		join = joinWith(*st.gaps, b)
	}
	if st.joiner.Frames() == 0 {
		join = edit.Join{}
	}
	span, err := st.joiner.Append(trimmed.Samples, join)
	if err != nil {
		return 0, 0, err
	}
	st.flushStream(false)
	rg := span.Range(st.rate)
	return rg.Start, rg.End, nil
}
