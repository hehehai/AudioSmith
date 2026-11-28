package onnx

import (
	"context"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/resample"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

const (
	sileroSampleRate = 16000
	sileroChunkSize  = 512 // 32 ms at 16 kHz
	sileroContextV5  = 64  // 4 ms context prepended in Silero VAD v5
)

// SileroVAD runs Silero VAD (v5 or v4) over an ONNX Runtime Session.
type SileroVAD struct {
	session Session
	isV5    bool
}

// NewSileroVAD wraps an open Silero VAD ONNX Session, auto-detecting v5
// (inputs: input, state, sr) vs v4 (inputs: input, h, c, sr).
func NewSileroVAD(sess Session) *SileroVAD {
	inNames := sess.InputNames()
	isV5 := slices.Contains(inNames, "state")
	return &SileroVAD{
		session: sess,
		isV5:    isV5,
	}
}

// Detect runs Silero VAD on the audio WAV file specified in p and returns
// merged speech regions in time order.
func (v *SileroVAD) Detect(ctx context.Context, p proto.VADParams) (proto.VADResult, error) {
	if p.Audio == "" {
		return proto.VADResult{}, proto.Errorf(proto.CodeBadRequest, "vad.detect: audio path is required")
	}

	samples, err := readMono16kWAV(p.Audio)
	if err != nil {
		return proto.VADResult{}, err
	}
	audioSecs := float64(len(samples)) / float64(sileroSampleRate)

	threshold := 0.5
	if p.Threshold != nil && *p.Threshold > 0 && *p.Threshold < 1 {
		threshold = *p.Threshold
	}
	minSpeech := 0.25
	if p.MinSpeech != nil && *p.MinSpeech >= 0 {
		minSpeech = *p.MinSpeech
	}
	minSilence := 0.10
	if p.MinSilence != nil && *p.MinSilence >= 0 {
		minSilence = *p.MinSilence
	}
	pad := 0.03
	if p.Pad != nil && *p.Pad >= 0 {
		pad = *p.Pad
	}

	start := time.Now()
	probs, err := v.inferProbabilities(ctx, samples)
	if err != nil {
		return proto.VADResult{}, err
	}

	regions := segmentProbabilities(probs, audioSecs, threshold, minSpeech, minSilence, pad)
	return proto.VADResult{
		Model:        p.Model,
		Regions:      regions,
		AudioSeconds: audioSecs,
		Seconds:      time.Since(start).Seconds(),
	}, nil
}

func (v *SileroVAD) inferProbabilities(ctx context.Context, samples []float32) ([]float64, error) {
	if len(samples) == 0 {
		return nil, nil
	}
	numChunks := (len(samples) + sileroChunkSize - 1) / sileroChunkSize
	probs := make([]float64, 0, numChunks)

	if v.isV5 {
		state := make([]float32, 2*1*128)
		prevCtx := make([]float32, sileroContextV5)
		sr := []int64{sileroSampleRate}
		inBuf := make([]float32, sileroContextV5+sileroChunkSize)

		for i := 0; i < numChunks; i++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			copy(inBuf[:sileroContextV5], prevCtx)
			clear(inBuf[sileroContextV5:])
			off := i * sileroChunkSize
			end := min(off+sileroChunkSize, len(samples))
			copy(inBuf[sileroContextV5:], samples[off:end])
			copy(prevCtx, inBuf[len(inBuf)-sileroContextV5:])

			outs, err := v.session.Run([]Tensor{
				Float32Tensor("input", []int64{1, int64(len(inBuf))}, inBuf),
				Float32Tensor("state", []int64{2, 1, 128}, state),
				Int64Tensor("sr", nil, sr),
			}, []string{"output", "stateN"})
			if err != nil {
				return nil, fmt.Errorf("silero vad v5 chunk %d: %w", i, err)
			}
			if len(outs) < 2 || len(outs[0].Float32) == 0 {
				return nil, fmt.Errorf("silero vad v5 chunk %d: empty output", i)
			}
			probs = append(probs, float64(outs[0].Float32[0]))
			if len(outs[1].Float32) == len(state) {
				copy(state, outs[1].Float32)
			}
		}
		return probs, nil
	}

	// Silero VAD v4: input [1, 512], h [2, 1, 64], c [2, 1, 64], sr [1]
	h := make([]float32, 2*1*64)
	c := make([]float32, 2*1*64)
	sr := []int64{sileroSampleRate}
	inBuf := make([]float32, sileroChunkSize)

	for i := 0; i < numChunks; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		clear(inBuf)
		off := i * sileroChunkSize
		end := min(off+sileroChunkSize, len(samples))
		copy(inBuf, samples[off:end])

		outs, err := v.session.Run([]Tensor{
			Float32Tensor("input", []int64{1, sileroChunkSize}, inBuf),
			Float32Tensor("h", []int64{2, 1, 64}, h),
			Float32Tensor("c", []int64{2, 1, 64}, c),
			Int64Tensor("sr", []int64{1}, sr),
		}, []string{"output", "hn", "cn"})
		if err != nil {
			return nil, fmt.Errorf("silero vad v4 chunk %d: %w", i, err)
		}
		if len(outs) < 3 || len(outs[0].Float32) == 0 {
			return nil, fmt.Errorf("silero vad v4 chunk %d: empty output", i)
		}
		probs = append(probs, float64(outs[0].Float32[0]))
		if len(outs[1].Float32) == len(h) {
			copy(h, outs[1].Float32)
		}
		if len(outs[2].Float32) == len(c) {
			copy(c, outs[2].Float32)
		}
	}
	return probs, nil
}

// segmentProbabilities converts per-chunk (32 ms) probabilities into merged,
// padded proto.Region spans using Silero's hysteresis algorithm.
func segmentProbabilities(probs []float64, audioSecs, threshold, minSpeech, minSilence, pad float64) []proto.Region {
	if len(probs) == 0 {
		return []proto.Region{}
	}
	chunkSec := float64(sileroChunkSize) / float64(sileroSampleRate)
	negThreshold := math.Max(threshold-0.15, 0.01)

	type rawSpan struct {
		startIdx int
		endIdx   int // exclusive
	}
	var spans []rawSpan

	triggered := false
	curStart := 0
	tempEnd := -1

	for i, p := range probs {
		if p >= threshold && tempEnd >= 0 {
			tempEnd = -1
		}
		if p >= threshold && !triggered {
			triggered = true
			curStart = i
			continue
		}
		if triggered {
			if p < negThreshold {
				if tempEnd < 0 {
					tempEnd = i
				}
				silDur := float64(i-tempEnd+1) * chunkSec
				if silDur >= minSilence {
					endIdx := tempEnd
					if endIdx <= curStart {
						endIdx = i
					}
					if float64(endIdx-curStart)*chunkSec >= minSpeech {
						spans = append(spans, rawSpan{startIdx: curStart, endIdx: endIdx})
					}
					triggered = false
					tempEnd = -1
				}
			}
		}
	}
	if triggered {
		endIdx := len(probs)
		if tempEnd >= 0 {
			endIdx = tempEnd
		}
		if float64(endIdx-curStart)*chunkSec >= minSpeech {
			spans = append(spans, rawSpan{startIdx: curStart, endIdx: endIdx})
		}
	}

	if len(spans) == 0 {
		return []proto.Region{}
	}

	out := make([]proto.Region, 0, len(spans))
	for _, sp := range spans {
		s := math.Max(0, float64(sp.startIdx)*chunkSec-pad)
		e := math.Min(audioSecs, float64(sp.endIdx)*chunkSec+pad)
		meanP := 0.0
		if sp.endIdx > sp.startIdx {
			var sum float64
			for idx := sp.startIdx; idx < sp.endIdx; idx++ {
				sum += probs[idx]
			}
			meanP = sum / float64(sp.endIdx-sp.startIdx)
		}
		if len(out) > 0 && s <= out[len(out)-1].End {
			prev := &out[len(out)-1]
			if e > prev.End {
				prev.End = e
			}
			prev.Prob = (prev.Prob + meanP) / 2
			continue
		}
		out = append(out, proto.Region{
			Start: round3(s),
			End:   round3(e),
			Prob:  round3(meanP),
		})
	}
	return out
}

func round3(v float64) float64 {
	return math.Round(v*1000) / 1000
}

// readMono16kWAV reads a WAV file, mixes multi-channel audio down to mono, and
// resamples to 16 kHz if needed.
func readMono16kWAV(path string) ([]float32, error) {
	wf, err := wav.Open(path)
	if err != nil {
		return nil, proto.Errorf(proto.CodeUnsupportedMedia, "vad.detect: open WAV %q: %v", path, err)
	}
	defer wf.Close()

	f := wf.Format()
	if f.Rate <= 0 || f.Channels <= 0 {
		return nil, proto.Errorf(proto.CodeUnsupportedMedia, "vad.detect: invalid WAV format %+v", f)
	}

	buf, err := wf.ReadFrames(0, -1)
	if err != nil {
		return nil, proto.Errorf(proto.CodeUnsupportedMedia, "vad.detect: read WAV %q: %v", path, err)
	}
	if buf.Channels > 1 {
		frames := len(buf.Samples) / buf.Channels
		mono := make([]float32, frames)
		inv := float32(1.0 / float64(buf.Channels))
		for i := 0; i < frames; i++ {
			var sum float32
			for ch := 0; ch < buf.Channels; ch++ {
				sum += buf.Samples[i*buf.Channels+ch]
			}
			mono[i] = sum * inv
		}
		buf = dsp.Buffer{Samples: mono, Rate: buf.Rate, Channels: 1}
	}
	if buf.Rate != sileroSampleRate && len(buf.Samples) > 0 {
		buf = resample.Buffer(buf, sileroSampleRate, resample.Fast)
	}
	return buf.Samples, nil
}
