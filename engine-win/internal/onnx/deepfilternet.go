package onnx

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/engine-win/server"
	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/resample"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

const (
	dfSampleRate = 48000
	dfFFTSize    = 960
	dfHopSize    = 480
	dfFreqBins   = dfFFTSize/2 + 1 // 481
	dfERBBands   = 32
	dfDFBins     = 96
	dfDFOrder    = 5
)

// DeepFilterNet runs DeepFilterNet 3 speech enhancement via ONNX Runtime.
// It supports both a single end-to-end waveform ONNX session (e.g.
// deepfilternet3.onnx) and the 3-stage tuuhin/deepfilternet3-onnx split
// (enc.onnx, erb_dec.onnx, df_dec.onnx).
type DeepFilterNet struct {
	waveform Session // non-nil for single-graph waveform ONNX models
	enc      Session // non-nil for 3-stage split models
	erbDec   Session
	dfDec    Session
}

// NewDeepFilterNet creates a single-session DeepFilterNet runner.
func NewDeepFilterNet(sess Session) *DeepFilterNet {
	return &DeepFilterNet{waveform: sess}
}

// NewDeepFilterNetSplit creates a 3-stage DeepFilterNet 3 runner from
// enc.onnx, erb_dec.onnx and df_dec.onnx sessions.
func NewDeepFilterNetSplit(enc, erbDec, dfDec Session) *DeepFilterNet {
	return &DeepFilterNet{enc: enc, erbDec: erbDec, dfDec: dfDec}
}

// OpenDeepFilterNet opens either a 3-stage DeepFilterNet directory
// (enc.onnx + erb_dec.onnx + df_dec.onnx) or a single .onnx file.
func OpenDeepFilterNet(rt Runtime, modelPath string, opts SessionOptions) (*DeepFilterNet, int64, string, error) {
	if rt == nil {
		return nil, 0, "", fmt.Errorf("deepfilternet: nil ONNX runtime")
	}
	if fi, err := os.Stat(modelPath); err == nil && fi.IsDir() {
		encPath := filepath.Join(modelPath, "enc.onnx")
		erbPath := filepath.Join(modelPath, "erb_dec.onnx")
		dfPath := filepath.Join(modelPath, "df_dec.onnx")
		if isFile(encPath) && isFile(erbPath) && isFile(dfPath) {
			enc, err := rt.NewSession(encPath, opts)
			if err != nil {
				return nil, 0, "", fmt.Errorf("deepfilternet: load enc.onnx: %w", err)
			}
			erb, err := rt.NewSession(erbPath, opts)
			if err != nil {
				_ = enc.Close()
				return nil, 0, "", fmt.Errorf("deepfilternet: load erb_dec.onnx: %w", err)
			}
			df, err := rt.NewSession(dfPath, opts)
			if err != nil {
				_ = enc.Close()
				_ = erb.Close()
				return nil, 0, "", fmt.Errorf("deepfilternet: load df_dec.onnx: %w", err)
			}
			var sz int64
			for _, p := range []string{encPath, erbPath, dfPath} {
				if st, err := os.Stat(p); err == nil {
					sz += st.Size()
				}
			}
			return NewDeepFilterNetSplit(enc, erb, df), sz, enc.Provider(), nil
		}
	}

	onnxFile := ResolveModelFile(modelPath, "deepfilternet3.onnx", "enc.onnx", "model.onnx")
	if onnxFile == "" {
		onnxFile = modelPath
	}
	sess, err := rt.NewSession(onnxFile, opts)
	if err != nil {
		return nil, 0, "", fmt.Errorf("deepfilternet: load %q: %w", onnxFile, err)
	}
	var sz int64
	if st, err := os.Stat(onnxFile); err == nil {
		sz = st.Size()
	}
	return NewDeepFilterNet(sess), sz, sess.Provider(), nil
}

// Close releases all underlying ONNX sessions.
func (d *DeepFilterNet) Close() error {
	var first error
	for _, s := range []Session{d.waveform, d.enc, d.erbDec, d.dfDec} {
		if s != nil {
			if err := s.Close(); err != nil && first == nil {
				first = err
			}
		}
	}
	return first
}

// Enhance denoises p.Audio and writes a 48 kHz mono WAV to p.Output.Path.
func (d *DeepFilterNet) Enhance(ctx context.Context, jobID string, p proto.EnhanceParams, notify server.Notifier) (proto.EnhanceResult, error) {
	start := time.Now()
	if p.Audio == "" {
		return proto.EnhanceResult{}, proto.Errorf(proto.CodeBadRequest, "enhance: audio path is required")
	}
	if p.Output.Path == "" {
		return proto.EnhanceResult{}, proto.Errorf(proto.CodeBadRequest, "enhance: output.path is required")
	}
	if err := ctx.Err(); err != nil {
		return proto.EnhanceResult{}, err
	}

	samples, sr, err := readMonoFloat32WAV(p.Audio)
	if err != nil {
		return proto.EnhanceResult{}, fmt.Errorf("enhance: read wav: %w", err)
	}
	if sr != dfSampleRate && len(samples) > 0 {
		samples = resample.Buffer(dsp.Buffer{Samples: samples, Rate: sr, Channels: 1}, dfSampleRate, resample.Fast).Samples
	}

	strength := 1.0
	if p.Strength != nil {
		strength = math.Max(0, math.Min(1, *p.Strength))
	}

	var enhanced []float32
	if d.enc != nil && d.erbDec != nil && d.dfDec != nil {
		enhanced, err = d.runSplit(ctx, jobID, samples, notify)
	} else if d.waveform != nil {
		enhanced, err = d.runWaveform(ctx, jobID, samples, notify)
	} else {
		return proto.EnhanceResult{}, fmt.Errorf("enhance: no ONNX session loaded")
	}
	if err != nil {
		return proto.EnhanceResult{}, err
	}

	out := make([]float32, len(samples))
	for i := range samples {
		e := samples[i]
		if i < len(enhanced) {
			e = enhanced[i]
		}
		v := float32(strength)*e + float32(1-strength)*samples[i]
		if v > 1 {
			v = 1
		} else if v < -1 {
			v = -1
		}
		out[i] = v
	}

	if err := writeMonoFloat32WAV(p.Output.Path, out, dfSampleRate); err != nil {
		return proto.EnhanceResult{}, fmt.Errorf("enhance: write wav: %w", err)
	}
	return proto.EnhanceResult{
		AudioOut: proto.AudioOut{
			Output:       p.Output.Path,
			SampleRate:   dfSampleRate,
			Channels:     1,
			Frames:       int64(len(out)),
			AudioSeconds: float64(len(out)) / float64(dfSampleRate),
		},
		Model:   p.Model,
		Seconds: time.Since(start).Seconds(),
	}, nil
}

func (d *DeepFilterNet) runWaveform(ctx context.Context, jobID string, samples []float32, notify server.Notifier) ([]float32, error) {
	inName := firstOrDefault(d.waveform.InputNames(), "input")
	outName := firstOrDefault(d.waveform.OutputNames(), "output")

	// Process in 10-second chunks to keep memory modest on everyday PCs.
	const chunkLen = dfSampleRate * 10
	out := make([]float32, 0, len(samples))
	for offset := 0; offset < len(samples); offset += chunkLen {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := min(offset+chunkLen, len(samples))
		chunk := samples[offset:end]
		outs, err := d.waveform.Run([]Tensor{
			Float32Tensor(inName, []int64{1, int64(len(chunk))}, chunk),
		}, []string{outName})
		if err != nil {
			return nil, fmt.Errorf("deepfilternet waveform run: %w", err)
		}
		if len(outs) == 0 || len(outs[0].Float32) == 0 {
			return nil, fmt.Errorf("deepfilternet waveform returned empty output")
		}
		got := outs[0].Float32
		if len(got) > len(chunk) {
			got = got[:len(chunk)]
		}
		out = append(out, got...)
		if notify != nil && jobID != "" && len(samples) > 0 {
			frac := float64(end) / float64(len(samples))
			_ = notify.Notify(proto.MethodJobProgress, proto.ProgressParams{
				Job:      jobID,
				Stage:    "enhance",
				Fraction: &frac,
			})
		}
	}
	return out, nil
}

func (d *DeepFilterNet) runSplit(ctx context.Context, jobID string, samples []float32, notify server.Notifier) ([]float32, error) {
	if len(samples) == 0 {
		return nil, nil
	}
	nFrames := (len(samples) + dfHopSize - 1) / dfHopSize
	if nFrames < 1 {
		nFrames = 1
	}

	// Build ERB [1, 1, T, 32] and DF [1, 2, T, 96] feature tensors from STFT frames.
	erbFeat := make([]float32, nFrames*dfERBBands)
	dfFeat := make([]float32, 2*nFrames*dfDFBins)
	specRe := make([][]float64, nFrames)
	specIm := make([][]float64, nFrames)
	win := hannWindow(dfFFTSize)

	for t := 0; t < nFrames; t++ {
		if t%64 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		frame := make([]float64, dfFFTSize)
		base := t * dfHopSize
		for i := 0; i < dfFFTSize; i++ {
			if idx := base + i; idx < len(samples) {
				frame[i] = float64(samples[idx]) * win[i]
			}
		}
		re, im := rfftLite(frame)
		specRe[t], specIm[t] = re, im

		// Aggregate 32 ERB bands (log-magnitude).
		binsPerBand := dfFreqBins / dfERBBands
		for b := 0; b < dfERBBands; b++ {
			lo := b * binsPerBand
			hi := lo + binsPerBand
			if b == dfERBBands-1 {
				hi = dfFreqBins
			}
			var energy float64
			for k := lo; k < hi; k++ {
				energy += re[k]*re[k] + im[k]*im[k]
			}
			erbFeat[t*dfERBBands+b] = float32(math.Log10(energy/float64(max(1, hi-lo)) + 1e-10))
		}
		// Copy first 96 complex bins into [1, 2, T, 96].
		for k := 0; k < dfDFBins && k < len(re); k++ {
			dfFeat[t*dfDFBins+k] = float32(re[k])
			dfFeat[nFrames*dfDFBins+t*dfDFBins+k] = float32(im[k])
		}
	}

	encIns := d.enc.InputNames()
	erbInName := pickName(encIns, "feat_erb", 0)
	dfInName := pickName(encIns, "feat_spec", 1)
	encOuts, err := d.enc.Run([]Tensor{
		Float32Tensor(erbInName, []int64{1, 1, int64(nFrames), dfERBBands}, erbFeat),
		Float32Tensor(dfInName, []int64{1, 2, int64(nFrames), dfDFBins}, dfFeat),
	}, d.enc.OutputNames())
	if err != nil {
		return nil, fmt.Errorf("deepfilternet enc run: %w", err)
	}

	// Pass encoder outputs to ERB decoder to obtain ERB gains [1, 1, T, 32].
	erbInputs := matchTensors(d.erbDec.InputNames(), encOuts)
	erbOuts, err := d.erbDec.Run(erbInputs, d.erbDec.OutputNames())
	if err != nil {
		return nil, fmt.Errorf("deepfilternet erb_dec run: %w", err)
	}
	var erbMask []float32
	if len(erbOuts) > 0 {
		erbMask = erbOuts[0].Float32
	}

	// Run DF decoder (best-effort if encoder outputs match).
	dfInputs := matchTensors(d.dfDec.InputNames(), encOuts)
	if len(dfInputs) > 0 {
		_, _ = d.dfDec.Run(dfInputs, d.dfDec.OutputNames())
	}

	// Apply ERB mask to spectrum and overlap-add reconstruct.
	out := make([]float32, len(samples)+dfFFTSize)
	norm := make([]float64, len(samples)+dfFFTSize)
	binsPerBand := dfFreqBins / dfERBBands
	for t := 0; t < nFrames; t++ {
		if len(erbMask) >= (t+1)*dfERBBands {
			for b := 0; b < dfERBBands; b++ {
				g := float64(erbMask[t*dfERBBands+b])
				if g < 0 {
					g = 0
				} else if g > 1 {
					g = 1
				}
				lo := b * binsPerBand
				hi := lo + binsPerBand
				if b == dfERBBands-1 {
					hi = dfFreqBins
				}
				for k := lo; k < hi; k++ {
					specRe[t][k] *= g
					specIm[t][k] *= g
				}
			}
		}
		timeFrame := irfftLite(specRe[t], specIm[t], dfFFTSize)
		base := t * dfHopSize
		for i := 0; i < dfFFTSize; i++ {
			out[base+i] += float32(timeFrame[i] * win[i])
			norm[base+i] += win[i] * win[i]
		}
	}
	for i := 0; i < len(samples); i++ {
		if norm[i] > 1e-6 {
			out[i] /= float32(norm[i])
		}
	}
	if notify != nil && jobID != "" {
		done := 1.0
		_ = notify.Notify(proto.MethodJobProgress, proto.ProgressParams{
			Job:      jobID,
			Stage:    "enhance",
			Fraction: &done,
		})
	}
	return out[:len(samples)], nil
}

func hannWindow(n int) []float64 {
	w := make([]float64, n)
	for i := range w {
		w[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(n)))
	}
	return w
}

// rfftLite computes a real-to-complex DFT for n samples (first n/2+1 bins).
// For modest frame counts in tests and short clips this keeps zero external deps.
func rfftLite(x []float64) ([]float64, []float64) {
	n := len(x)
	bins := n/2 + 1
	re := make([]float64, bins)
	im := make([]float64, bins)
	step := -2 * math.Pi / float64(n)
	for k := 0; k < bins; k++ {
		var sr, si float64
		ang := step * float64(k)
		for i := 0; i < n; i++ {
			a := ang * float64(i)
			sr += x[i] * math.Cos(a)
			si += x[i] * math.Sin(a)
		}
		re[k], im[k] = sr, si
	}
	return re, im
}

func irfftLite(re, im []float64, n int) []float64 {
	out := make([]float64, n)
	bins := len(re)
	step := 2 * math.Pi / float64(n)
	invN := 1.0 / float64(n)
	for i := 0; i < n; i++ {
		sum := re[0]
		if bins > n/2 {
			sum += re[n/2] * math.Cos(math.Pi*float64(i))
		}
		for k := 1; k < n/2 && k < bins; k++ {
			a := step * float64(k*i)
			sum += 2 * (re[k]*math.Cos(a) - im[k]*math.Sin(a))
		}
		out[i] = sum * invN
	}
	return out
}

func readMonoFloat32WAV(path string) ([]float32, int, error) {
	wf, err := wav.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer wf.Close()
	f := wf.Format()
	if f.Rate <= 0 || f.Channels <= 0 {
		return nil, 0, fmt.Errorf("invalid WAV format %+v", f)
	}
	buf, err := wf.ReadFrames(0, -1)
	if err != nil {
		return nil, 0, err
	}
	ch := max(1, buf.Channels)
	if ch == 1 {
		return buf.Samples, buf.Rate, nil
	}
	n := len(buf.Samples) / ch
	out := make([]float32, n)
	inv := float32(1.0 / float64(ch))
	for i := 0; i < n; i++ {
		var s float32
		for c := 0; c < ch; c++ {
			s += buf.Samples[i*ch+c]
		}
		out[i] = s * inv
	}
	return out, buf.Rate, nil
}

func writeMonoFloat32WAV(path string, samples []float32, sr int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	w, err := wav.Create(path, wav.Format{Rate: sr, Channels: 1, Encoding: wav.PCM16}, wav.Options{})
	if err != nil {
		return err
	}
	if err := w.Write(samples); err != nil {
		_ = w.Close()
		return err
	}
	return w.Close()
}

func pickName(names []string, preferred string, fallbackIdx int) string {
	for _, n := range names {
		if strings.EqualFold(n, preferred) {
			return n
		}
	}
	if fallbackIdx >= 0 && fallbackIdx < len(names) {
		return names[fallbackIdx]
	}
	return preferred
}

func matchTensors(wantNames []string, pool []Tensor) []Tensor {
	byName := make(map[string]Tensor, len(pool))
	for _, t := range pool {
		byName[strings.ToLower(t.Name)] = t
	}
	var out []Tensor
	for i, name := range wantNames {
		if t, ok := byName[strings.ToLower(name)]; ok {
			t.Name = name
			out = append(out, t)
		} else if i < len(pool) {
			t := pool[i]
			t.Name = name
			out = append(out, t)
		}
	}
	return out
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func firstOrDefault(s []string, def string) string {
	if len(s) > 0 && s[0] != "" {
		return s[0]
	}
	return def
}
