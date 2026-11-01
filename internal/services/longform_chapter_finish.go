package services

// The end of a chapter's render (chapterRun.finish): levelling, the
// loudness target, the watermark and the sidecar; split from
// longform_chapter.go.

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/hehehai/AudioSmith/internal/dsp/loudness"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

// finish writes the chapter and its sidecar and returns the result.
func (cx *chapterRun) finish(started time.Time) (json.RawMessage, error) {
	ctx, st, p, r, res := cx.ctx, cx.st, cx.p, cx.cr.r, &cx.res
	if st.joiner == nil {
		return nil, &jobs.Error{Code: CodeNothingToSay, Message: "The chapter has nothing to say."}
	}
	if _, err := st.joiner.Silence(chapterTail); err != nil {
		return nil, &jobs.Error{Code: CodeRenderFailed, Message: err.Error()}
	}
	st.report(jobs.Progress{Stage: "writing", Fraction: 1, AudioSeconds: st.seconds()})
	out, rep, err := st.finishChapter(ctx, loudnessTarget(p.Loudness), cx.levelGains())
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &jobs.Error{Code: CodeRenderFailed, Message: err.Error()}
	}
	if _, notice := r.watermarkOutput(ctx, p.Output, 16); notice != nil { // watermark.go
		cx.warnings = append(cx.warnings, *notice)
	}
	res.SampleRate, res.Channels, res.Frames = out.rate, out.channels, out.frames
	res.AudioSeconds = out.seconds()
	res.Report = rep
	res.Warnings = cx.warnings
	res.Seconds = time.Since(started).Seconds()
	for i := range res.Lines {
		if res.Lines[i].Start < 0 {
			res.Lines[i].Start = 0
		}
		for j := range res.Lines[i].Spans {
			if res.Lines[i].Spans[j].Start < 0 {
				res.Lines[i].Spans[j].Start = 0
			}
		}
	}
	data, err := json.Marshal(res)
	if err != nil {
		return nil, err
	}
	if err := writeFileAtomic(Sidecar(p.Output), data); err != nil {
		return nil, &jobs.Error{Code: CodeRenderFailed, Message: err.Error()}
	}
	return data, nil
}

// levelGains is how finishChapter levels the lines: per character when
// paced, a book's dialogue lines to its narration; it returns nil gains
// when the chapter is not levelled.
func (cx *chapterRun) levelGains() func() []lineGain {
	ctx, st, p, res := cx.ctx, cx.st, cx.p, &cx.res
	_, paced := GapsOf(p.Production.Pacing)
	if !(paced || p.book() && len(cx.made) > 0) || p.Production.NoLevel {
		return func() []lineGain { return nil }
	}
	return func() []lineGain {
		lufs, err := st.lineLoudness(ctx, res.Lines)
		if err != nil {
			cx.cr.r.logf("chapter %s: levelling: %v", cx.job.ID, err)
			return nil
		}
		if !paced { // a book's dialogue lines to its narration (book_dialogue_render.go)
			g := bookLevelGains(res.Lines, p.Lines, cx.made, lufs)
			res.Levelled = len(g)
			return g
		}
		who := make([]string, len(p.Lines))
		for i, l := range p.Lines {
			who[i] = l.Character
			if len(l.Spans) > 0 {
				who[i] += "\x00" + l.Spans[0].Voice
			}
		}
		g := levelGains(res.Lines, who, lufs)
		res.Levelled = len(g)
		return g
	}
}

// finishChapter closes the joined audio and writes the chapter: lines
// levelled by the gains level returns (nil: none), then gain and a
// true-peak limiter to target (nil: as joined), 16 bits with dither,
// measured for the report.
func (st *renderState) finishChapter(ctx context.Context, target *loudness.Target, level func() []lineGain) (finalAudio, LoudnessReport, error) {
	if err := st.joiner.Close(); err != nil {
		return finalAudio{}, LoudnessReport{}, err
	}
	integrated := st.meter.Integrated()
	if err := st.tmp.Close(); err != nil {
		return finalAudio{}, LoudnessReport{}, err
	}
	st.tmp = nil
	st.files = append(st.files, st.tmpPath)
	if level != nil {
		if gains := level(); len(gains) > 0 {
			lv, err := st.applyLevel(ctx, gains)
			if err != nil {
				return finalAudio{}, LoudnessReport{}, err
			}
			integrated = lv
		}
	}
	var stage *loudness.Stream
	gain := 0.0
	if target != nil && integrated > loudness.Floor {
		gain = target.Integrated - integrated
		stage = loudness.NewStream(st.rate, st.channels, gain, target.TruePeak, true)
	}
	out, err := wav.Create(st.p.Output, wav.Format{Rate: st.rate, Channels: st.channels, Encoding: wav.PCM16}, wav.Options{Dither: true})
	if err != nil {
		return finalAudio{}, LoudnessReport{}, err
	}
	v := newVerifier(st.rate, st.channels)
	write := func(s []float32) error {
		v.Write(s)
		return out.Write(s)
	}
	var buf []float32
	err = eachBlock(ctx, st.tmpPath, func(s []float32) error {
		if stage == nil {
			return write(s)
		}
		buf = stage.Process(buf[:0], s)
		return write(buf)
	})
	if err == nil && stage != nil {
		err = write(stage.Flush(buf[:0]))
	}
	if err != nil {
		out.Abort()
		return finalAudio{}, LoudnessReport{}, err
	}
	frames := out.Frames()
	if err := out.Close(); err != nil {
		return finalAudio{}, LoudnessReport{}, err
	}
	rep := v.Report(target)
	if stage != nil {
		rep.GainDB = round2(gain)
		rep.LimitDB = round2(stage.MaxReductionDB())
	}
	fa := finalAudio{rate: st.rate, channels: st.channels, frames: frames}
	if fi, err := os.Stat(st.p.Output); err == nil {
		fa.bytes = fi.Size()
	}
	return fa, rep, nil
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// lineLoudness measures each placed line of the joined audio (LUFS;
// −Inf for lines too short to measure).
func (st *renderState) lineLoudness(ctx context.Context, places []LinePlace) ([]float64, error) {
	meters := make([]*loudness.Meter, len(places))
	for i, pl := range places {
		if pl.End-pl.Start >= levelMinS {
			meters[i] = loudness.NewMeter(st.rate, st.channels, loudness.Options{SkipTruePeak: true})
		}
	}
	var at int64
	err := eachBlock(ctx, st.tmpPath, func(s []float32) error {
		n := int64(len(s) / st.channels)
		t0, t1 := float64(at)/float64(st.rate), float64(at+n)/float64(st.rate)
		for i, pl := range places {
			if meters[i] == nil || pl.End <= t0 || pl.Start >= t1 {
				continue
			}
			a := max(int64(math.Round(pl.Start*float64(st.rate)))-at, 0)
			b := min(int64(math.Round(pl.End*float64(st.rate)))-at, n)
			if b > a {
				meters[i].Write(s[a*int64(st.channels) : b*int64(st.channels)])
			}
		}
		at += n
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]float64, len(places))
	for i, m := range meters {
		out[i] = math.Inf(-1)
		if m != nil {
			out[i] = m.Integrated()
		}
	}
	return out, nil
}

// applyLevel rewrites the joined audio with the levelling gains and
// returns its new integrated loudness.
func (st *renderState) applyLevel(ctx context.Context, gains []lineGain) (float64, error) {
	path := filepath.Join(st.r.tmpDir(), st.job.ID+"-levelled.wav")
	st.files = append(st.files, path)
	w, err := wav.Create(path, wav.Format{Rate: st.rate, Channels: st.channels, Encoding: wav.Float32}, wav.Options{Large: true})
	if err != nil {
		return 0, err
	}
	m := loudness.NewMeter(st.rate, st.channels, loudness.Options{SkipTruePeak: true})
	var at int64
	err = eachBlock(ctx, st.tmpPath, func(s []float32) error {
		for f := 0; f < len(s)/st.channels; f++ {
			g := float32(gainAt(gains, float64(at+int64(f))/float64(st.rate)))
			if g != 1 {
				for c := range st.channels {
					s[f*st.channels+c] *= g
				}
			}
		}
		at += int64(len(s) / st.channels)
		m.Write(s)
		return w.Write(s)
	})
	if err != nil {
		w.Abort()
		return 0, err
	}
	if err := w.Close(); err != nil {
		return 0, err
	}
	st.tmpPath = path
	return m.Integrated(), nil
}
