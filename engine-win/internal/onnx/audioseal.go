package onnx

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/hehehai/AudioSmith/engine-win/hw"
	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/resample"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

const (
	audioSealSampleRate = 16000
	audioSealChunkSize  = audioSealSampleRate * 10 // 10 s chunks
	audioSealNumBits    = 16
)

// AudioSeal embeds and detects 16-bit neural watermarks via ONNX Runtime
// (generator.onnx + detector.onnx) at 16 kHz on everyday CPU/iGPU hardware.
type AudioSeal struct {
	generator Session
	detector  Session
}

// NewAudioSeal creates an AudioSeal runner from pre-opened generator and/or
// detector ONNX sessions.
func NewAudioSeal(generator, detector Session) *AudioSeal {
	return &AudioSeal{generator: generator, detector: detector}
}

// Close closes any open generator/detector sessions.
func (a *AudioSeal) Close() error {
	var first error
	if a.generator != nil {
		if err := a.generator.Close(); err != nil {
			first = err
		}
	}
	if a.detector != nil {
		if err := a.detector.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Embed applies a 16-bit AudioSeal watermark to p.Input and writes p.Output.Path.
func (a *AudioSeal) Embed(ctx context.Context, p proto.WatermarkEmbedParams) (proto.WatermarkEmbedResult, error) {
	start := time.Now()
	if a.generator == nil {
		return proto.WatermarkEmbedResult{}, fmt.Errorf("audioseal: generator ONNX session not loaded")
	}
	if p.Input == "" || p.Output.Path == "" {
		return proto.WatermarkEmbedResult{}, proto.Errorf(proto.CodeBadRequest, "watermark.embed: input and output.path are required")
	}
	if err := ctx.Err(); err != nil {
		return proto.WatermarkEmbedResult{}, err
	}

	samples, sr, err := readMonoFloat32WAV(p.Input)
	if err != nil {
		return proto.WatermarkEmbedResult{}, fmt.Errorf("audioseal embed: read wav: %w", err)
	}
	if sr != audioSealSampleRate && len(samples) > 0 {
		samples = resample.Buffer(dsp.Buffer{Samples: samples, Rate: sr, Channels: 1}, audioSealSampleRate, resample.Fast).Samples
	}

	strength := p.Strength
	if strength <= 0 {
		strength = 1.0
	}

	bits := make([]int64, audioSealNumBits)
	for i := 0; i < audioSealNumBits; i++ {
		bits[i] = int64((p.Message >> (15 - i)) & 1)
	}

	inNames := a.generator.InputNames()
	audioIn := pickName(inNames, "audio", 0)
	msgIn := pickName(inNames, "message", 1)
	outName := firstOrDefault(a.generator.OutputNames(), "watermark")

	out := make([]float32, len(samples))
	var sigPow, wmPow float64
	for offset := 0; offset < len(samples); offset += audioSealChunkSize {
		if err := ctx.Err(); err != nil {
			return proto.WatermarkEmbedResult{}, err
		}
		end := min(offset+audioSealChunkSize, len(samples))
		chunk := samples[offset:end]

		inputs := []Tensor{
			Float32Tensor(audioIn, []int64{1, 1, int64(len(chunk))}, chunk),
		}
		if len(inNames) >= 2 || msgIn != "" {
			inputs = append(inputs, Int64Tensor(msgIn, []int64{1, audioSealNumBits}, bits))
		}
		outs, err := a.generator.Run(inputs, []string{outName})
		if err != nil {
			return proto.WatermarkEmbedResult{}, fmt.Errorf("audioseal generator run: %w", err)
		}
		if len(outs) == 0 || len(outs[0].Float32) == 0 {
			return proto.WatermarkEmbedResult{}, fmt.Errorf("audioseal generator returned empty output")
		}
		wm := outs[0].Float32
		for i := range chunk {
			var delta float32
			if i < len(wm) {
				delta = wm[i] * float32(strength)
			}
			sigPow += float64(chunk[i]) * float64(chunk[i])
			wmPow += float64(delta) * float64(delta)
			v := chunk[i] + delta
			if v > 1 {
				v = 1
			} else if v < -1 {
				v = -1
			}
			out[offset+i] = v
		}
	}

	if err := writeMonoFloat32WAV(p.Output.Path, out, audioSealSampleRate); err != nil {
		return proto.WatermarkEmbedResult{}, fmt.Errorf("audioseal embed: write wav: %w", err)
	}

	snr := 0.0
	if wmPow > 0 && sigPow > 0 {
		snr = 10 * math.Log10(sigPow/wmPow)
	}
	return proto.WatermarkEmbedResult{
		Output:       p.Output.Path,
		SampleRate:   audioSealSampleRate,
		Channels:     1,
		Frames:       int64(len(out)),
		AudioSeconds: float64(len(out)) / float64(audioSealSampleRate),
		Model:        p.Model,
		Message:      p.Message & 0xffff,
		SNR:          snr,
		Seconds:      time.Since(start).Seconds(),
	}, nil
}

// Detect checks p.Input for an AudioSeal watermark and decodes its 16-bit payload.
func (a *AudioSeal) Detect(ctx context.Context, p proto.WatermarkDetectParams) (proto.WatermarkDetectResult, error) {
	start := time.Now()
	if a.detector == nil {
		return proto.WatermarkDetectResult{}, fmt.Errorf("audioseal: detector ONNX session not loaded")
	}
	if p.Input == "" {
		return proto.WatermarkDetectResult{}, proto.Errorf(proto.CodeBadRequest, "watermark.detect: input is required")
	}
	if err := ctx.Err(); err != nil {
		return proto.WatermarkDetectResult{}, err
	}

	samples, sr, err := readMonoFloat32WAV(p.Input)
	if err != nil {
		return proto.WatermarkDetectResult{}, fmt.Errorf("audioseal detect: read wav: %w", err)
	}
	if sr != audioSealSampleRate && len(samples) > 0 {
		samples = resample.Buffer(dsp.Buffer{Samples: samples, Rate: sr, Channels: 1}, audioSealSampleRate, resample.Fast).Samples
	}

	threshold := p.Threshold
	if threshold <= 0 {
		threshold = 0.5
	}

	audioIn := firstOrDefault(a.detector.InputNames(), "audio")
	outNames := a.detector.OutputNames()
	if len(outNames) == 0 {
		outNames = []string{"result", "message"}
	}

	sampleProbs := make([]float64, 0, len(samples))
	var bitSums [audioSealNumBits]float64
	var bitChunks int

	for offset := 0; offset < len(samples); offset += audioSealChunkSize {
		if err := ctx.Err(); err != nil {
			return proto.WatermarkDetectResult{}, err
		}
		end := min(offset+audioSealChunkSize, len(samples))
		chunk := samples[offset:end]

		outs, err := a.detector.Run([]Tensor{
			Float32Tensor(audioIn, []int64{1, 1, int64(len(chunk))}, chunk),
		}, outNames)
		if err != nil {
			return proto.WatermarkDetectResult{}, fmt.Errorf("audioseal detector run: %w", err)
		}
		if len(outs) == 0 {
			return proto.WatermarkDetectResult{}, fmt.Errorf("audioseal detector returned empty output")
		}

		// Parse per-sample watermark probabilities from [1, 2, N] logits or [1, 1, N] probs.
		probs := parseDetectionProbs(outs[0], len(chunk))
		sampleProbs = append(sampleProbs, probs...)

		// Parse 16-bit message probabilities from second output tensor if present.
		if len(outs) >= 2 && len(outs[1].Float32) >= audioSealNumBits {
			for i := 0; i < audioSealNumBits; i++ {
				bitSums[i] += clampProb(float64(outs[1].Float32[i]))
			}
			bitChunks++
		}
	}

	var hits int
	for _, pr := range sampleProbs {
		if pr > threshold {
			hits++
		}
	}
	prob := 0.0
	if len(sampleProbs) > 0 {
		prob = float64(hits) / float64(len(sampleProbs))
	}

	bits := make([]float64, audioSealNumBits)
	msg := 0
	var confSum float64
	for i := 0; i < audioSealNumBits; i++ {
		if bitChunks > 0 {
			bits[i] = bitSums[i] / float64(bitChunks)
		}
		if bits[i] >= 0.5 {
			msg |= 1 << (15 - i)
		}
		confSum += math.Abs(2*bits[i] - 1)
	}

	// Build 1-second frames.
	var frames []proto.WatermarkFrame
	for sec := 0; sec*audioSealSampleRate < len(sampleProbs); sec++ {
		lo := sec * audioSealSampleRate
		hi := min(lo+audioSealSampleRate, len(sampleProbs))
		var h int
		for _, pr := range sampleProbs[lo:hi] {
			if pr > threshold {
				h++
			}
		}
		frames = append(frames, proto.WatermarkFrame{
			Start:       float64(lo) / float64(audioSealSampleRate),
			End:         float64(hi) / float64(audioSealSampleRate),
			Probability: float64(h) / float64(max(1, hi-lo)),
		})
	}

	return proto.WatermarkDetectResult{
		Probability:  prob,
		Detected:     prob >= 0.5,
		Message:      msg,
		Bits:         bits,
		Confidence:   confSum / float64(audioSealNumBits),
		Frames:       frames,
		AudioSeconds: float64(len(samples)) / float64(audioSealSampleRate),
		Model:        p.Model,
		Seconds:      time.Since(start).Seconds(),
	}, nil
}

func parseDetectionProbs(t Tensor, n int) []float64 {
	data := t.Float32
	out := make([]float64, n)
	if len(data) == 0 {
		return out
	}
	// [1, 2, N] two-class logits (class 0 = clean, class 1 = watermarked).
	if len(t.Shape) == 3 && t.Shape[1] == 2 && len(data) >= 2*n {
		for i := 0; i < n; i++ {
			a, b := float64(data[i]), float64(data[n+i])
			m := math.Max(a, b)
			ea, eb := math.Exp(a-m), math.Exp(b-m)
			out[i] = eb / (ea + eb)
		}
		return out
	}
	// [1, 1, N] or [1, N] direct probabilities.
	for i := 0; i < n && i < len(data); i++ {
		out[i] = clampProb(float64(data[i]))
	}
	return out
}

func clampProb(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// AudioSealBackend implements media.WatermarkBackend lazily opening ONNX Runtime
// and caching AudioSeal generator/detector sessions by model path.
type AudioSealBackend struct {
	hw hw.Info

	mu      sync.Mutex
	rt      Runtime
	runners map[string]*AudioSeal
}

// NewAudioSealBackend creates a WatermarkBackend backed by ONNX Runtime.
// rt may be nil to lazily open onnxruntime.dll via DiscoverDLL on first call.
func NewAudioSealBackend(rt Runtime, hwInfo hw.Info) *AudioSealBackend {
	return &AudioSealBackend{
		hw:      hwInfo,
		rt:      rt,
		runners: map[string]*AudioSeal{},
	}
}

func (b *AudioSealBackend) ensureRuntime() (Runtime, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.rt != nil {
		return b.rt, nil
	}
	rt, err := Open("")
	if err != nil {
		return nil, proto.Errorf(proto.CodeUnsupported, "audioseal: %v", err)
	}
	b.rt = rt
	return rt, nil
}

func (b *AudioSealBackend) getRunner(modelPath string, needGen, needDet bool) (*AudioSeal, error) {
	rt, err := b.ensureRuntime()
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	r := b.runners[modelPath]
	if r == nil {
		r = &AudioSeal{}
		b.runners[modelPath] = r
	}
	opts := SessionOptions{PreferDirectML: false, IntraOpThreads: max(1, min(4, b.hw.NumCPU)), InterOpThreads: 1}
	if needGen && r.generator == nil {
		p := ResolveModelFile(modelPath, "generator.onnx", "audioseal_generator.onnx", "generator_base.onnx")
		if p == "" {
			return nil, proto.Errorf(proto.CodeFileNotFound, "audioseal: generator.onnx not found in %q", modelPath)
		}
		sess, err := rt.NewSession(p, opts)
		if err != nil {
			return nil, fmt.Errorf("audioseal: open generator: %w", err)
		}
		r.generator = sess
	}
	if needDet && r.detector == nil {
		p := ResolveModelFile(modelPath, "detector.onnx", "audioseal_detector.onnx", "detector_base.onnx")
		if p == "" {
			return nil, proto.Errorf(proto.CodeFileNotFound, "audioseal: detector.onnx not found in %q", modelPath)
		}
		sess, err := rt.NewSession(p, opts)
		if err != nil {
			return nil, fmt.Errorf("audioseal: open detector: %w", err)
		}
		r.detector = sess
	}
	return r, nil
}

// Embed satisfies media.WatermarkBackend.
func (b *AudioSealBackend) Embed(ctx context.Context, p proto.WatermarkEmbedParams) (proto.WatermarkEmbedResult, error) {
	r, err := b.getRunner(p.ModelPath, true, false)
	if err != nil {
		return proto.WatermarkEmbedResult{}, err
	}
	return r.Embed(ctx, p)
}

// Detect satisfies media.WatermarkBackend.
func (b *AudioSealBackend) Detect(ctx context.Context, p proto.WatermarkDetectParams) (proto.WatermarkDetectResult, error) {
	r, err := b.getRunner(p.ModelPath, false, true)
	if err != nil {
		return proto.WatermarkDetectResult{}, err
	}
	return r.Detect(ctx, p)
}

// Close closes all cached AudioSeal sessions.
func (b *AudioSealBackend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for k, r := range b.runners {
		_ = r.Close()
		delete(b.runners, k)
	}
	return nil
}
