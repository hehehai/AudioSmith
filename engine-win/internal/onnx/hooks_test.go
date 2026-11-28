package onnx

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/hehehai/AudioSmith/engine-win/hw"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

type fakeSession struct {
	inputs  []string
	outputs []string
	runFn   func([]Tensor, []string) ([]Tensor, error)
}

func (f *fakeSession) InputNames() []string  { return f.inputs }
func (f *fakeSession) OutputNames() []string { return f.outputs }
func (f *fakeSession) Provider() string      { return "cpu" }
func (f *fakeSession) Close() error          { return nil }
func (f *fakeSession) Run(ins []Tensor, outs []string) ([]Tensor, error) {
	return f.runFn(ins, outs)
}

type fakeRuntime struct {
	sessions map[string]Session
}

func (f *fakeRuntime) Version() string              { return "1.24.4-fake" }
func (f *fakeRuntime) AvailableProviders() []string { return []string{"CPUExecutionProvider"} }
func (f *fakeRuntime) Close() error                 { return nil }
func (f *fakeRuntime) NewSession(modelPath string, _ SessionOptions) (Session, error) {
	if s, ok := f.sessions[filepath.Base(modelPath)]; ok {
		return s, nil
	}
	if s, ok := f.sessions[modelPath]; ok {
		return s, nil
	}
	return nil, os.ErrNotExist
}

func writeTestSineWAV(t *testing.T, path string, sr int, seconds float64, amp float32) {
	t.Helper()
	n := int(float64(sr) * seconds)
	samples := make([]float32, n)
	for i := range samples {
		samples[i] = amp * float32(math.Sin(2*math.Pi*440*float64(i)/float64(sr)))
	}
	w, err := wav.Create(path, wav.Format{Rate: sr, Channels: 1, Encoding: wav.PCM16}, wav.Options{})
	if err != nil {
		t.Fatalf("create wav: %v", err)
	}
	if err := w.Write(samples); err != nil {
		t.Fatalf("write wav: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close wav: %v", err)
	}
}

func TestDeepFilterNet_SingleWaveformAndStrength(t *testing.T) {
	dir := t.TempDir()
	inPath := filepath.Join(dir, "noisy.wav")
	outPath := filepath.Join(dir, "clean.wav")
	writeTestSineWAV(t, inPath, 16000, 0.25, 0.8) // 16 kHz -> resampled to 48 kHz

	// Fake waveform denoiser attenuates amplitude by 0.5x.
	sess := &fakeSession{
		inputs:  []string{"input"},
		outputs: []string{"output"},
		runFn: func(ins []Tensor, _ []string) ([]Tensor, error) {
			x := ins[0].Float32
			y := make([]float32, len(x))
			for i, v := range x {
				y[i] = v * 0.5
			}
			return []Tensor{Float32Tensor("output", ins[0].Shape, y)}, nil
		},
	}

	df := NewDeepFilterNet(sess)
	strength := 0.5 // mix 50% enhanced (0.5x) + 50% original (1.0x) => 0.75x
	res, err := df.Enhance(context.Background(), "job-df-1", proto.EnhanceParams{
		Model:    "deepfilternet-v3",
		Audio:    inPath,
		Output:   proto.Output{Path: outPath},
		Strength: &strength,
	}, nil)
	if err != nil {
		t.Fatalf("Enhance: %v", err)
	}
	if res.SampleRate != 48000 || res.Channels != 1 || res.Frames != 12000 {
		t.Fatalf("unexpected EnhanceResult: %+v", res)
	}
	samples, sr, err := readMonoFloat32WAV(outPath)
	if err != nil || sr != 48000 || len(samples) != 12000 {
		t.Fatalf("output wav = (len=%d sr=%d err=%v)", len(samples), sr, err)
	}
	var peak float32
	for _, s := range samples {
		if a := float32(math.Abs(float64(s))); a > peak {
			peak = a
		}
	}
	// Expected peak around 0.8 * 0.75 = 0.60
	if peak < 0.50 || peak > 0.68 {
		t.Fatalf("expected mixed peak ~0.60, got %.3f", peak)
	}
}

func TestDeepFilterNet_ThreeStageSplit(t *testing.T) {
	dir := t.TempDir()
	modelDir := filepath.Join(dir, "df3")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"enc.onnx", "erb_dec.onnx", "df_dec.onnx"} {
		if err := os.WriteFile(filepath.Join(modelDir, f), []byte("fake"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	enc := &fakeSession{
		inputs:  []string{"feat_erb", "feat_spec"},
		outputs: []string{"emb"},
		runFn: func(ins []Tensor, _ []string) ([]Tensor, error) {
			return []Tensor{Float32Tensor("emb", ins[0].Shape, ins[0].Float32)}, nil
		},
	}
	erb := &fakeSession{
		inputs:  []string{"emb"},
		outputs: []string{"m"},
		runFn: func(ins []Tensor, _ []string) ([]Tensor, error) {
			// Return gain = 0.8 for every ERB band and frame.
			mask := make([]float32, len(ins[0].Float32))
			for i := range mask {
				mask[i] = 0.8
			}
			return []Tensor{Float32Tensor("m", ins[0].Shape, mask)}, nil
		},
	}
	dfd := &fakeSession{
		inputs:  []string{"emb"},
		outputs: []string{"coefs"},
		runFn: func(ins []Tensor, _ []string) ([]Tensor, error) {
			return []Tensor{Float32Tensor("coefs", []int64{1}, []float32{0})}, nil
		},
	}

	rt := &fakeRuntime{sessions: map[string]Session{
		"enc.onnx":     enc,
		"erb_dec.onnx": erb,
		"df_dec.onnx":  dfd,
	}}
	df, sz, prov, err := OpenDeepFilterNet(rt, modelDir, SessionOptions{})
	if err != nil {
		t.Fatalf("OpenDeepFilterNet: %v", err)
	}
	defer df.Close()
	if sz == 0 || prov != "cpu" {
		t.Fatalf("unexpected open metadata: sz=%d prov=%q", sz, prov)
	}

	inPath := filepath.Join(dir, "in48.wav")
	outPath := filepath.Join(dir, "out48.wav")
	writeTestSineWAV(t, inPath, 48000, 0.04, 0.5) // 4 frames (1920 samples)

	res, err := df.Enhance(context.Background(), "job-df-split", proto.EnhanceParams{
		Model:  "deepfilternet-v3",
		Audio:  inPath,
		Output: proto.Output{Path: outPath},
	}, nil)
	if err != nil {
		t.Fatalf("split Enhance: %v", err)
	}
	if res.Frames != 1920 || res.SampleRate != 48000 {
		t.Fatalf("unexpected split EnhanceResult: %+v", res)
	}
}

func TestAudioSeal_EmbedDetectAndBackend(t *testing.T) {
	dir := t.TempDir()
	modelDir := filepath.Join(dir, "audioseal")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"generator.onnx", "detector.onnx"} {
		if err := os.WriteFile(filepath.Join(modelDir, f), []byte("fake"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	const testMessage = 0b1010_0101_1100_0011 // 42435
	var capturedBits []int64

	gen := &fakeSession{
		inputs:  []string{"audio", "message"},
		outputs: []string{"watermark"},
		runFn: func(ins []Tensor, _ []string) ([]Tensor, error) {
			capturedBits = append([]int64(nil), ins[1].Int64...)
			n := len(ins[0].Float32)
			wm := make([]float32, n)
			for i := range wm {
				wm[i] = 0.01 // subtle watermark delta
			}
			return []Tensor{Float32Tensor("watermark", ins[0].Shape, wm)}, nil
		},
	}
	det := &fakeSession{
		inputs:  []string{"audio"},
		outputs: []string{"result", "message"},
		runFn: func(ins []Tensor, _ []string) ([]Tensor, error) {
			n := len(ins[0].Float32)
			// 2-class logits [1, 2, N]: class 1 (watermarked) strongly positive.
			logits := make([]float32, 2*n)
			for i := 0; i < n; i++ {
				logits[i] = -2.0   // clean logit
				logits[n+i] = +3.0 // watermarked logit -> softmax ~0.993
			}
			bitProbs := make([]float32, 16)
			for i := 0; i < 16; i++ {
				if ((testMessage >> (15 - i)) & 1) == 1 {
					bitProbs[i] = 0.95
				} else {
					bitProbs[i] = 0.05
				}
			}
			return []Tensor{
				Float32Tensor("result", []int64{1, 2, int64(n)}, logits),
				Float32Tensor("message", []int64{1, 16}, bitProbs),
			}, nil
		},
	}

	rt := &fakeRuntime{sessions: map[string]Session{
		"generator.onnx": gen,
		"detector.onnx":  det,
	}}
	backend := NewAudioSealBackend(rt, hw.Info{NumCPU: 4})
	defer backend.Close()

	inPath := filepath.Join(dir, "voice.wav")
	wmPath := filepath.Join(dir, "watermarked.wav")
	writeTestSineWAV(t, inPath, 24000, 1.2, 0.5) // resampled to 16 kHz

	emb, err := backend.Embed(context.Background(), proto.WatermarkEmbedParams{
		Model:     "audioseal-16bits",
		ModelPath: modelDir,
		Input:     inPath,
		Output:    proto.Output{Path: wmPath},
		Message:   testMessage,
		Strength:  0.8,
	})
	if err != nil {
		t.Fatalf("AudioSeal Embed: %v", err)
	}
	if emb.SampleRate != 16000 || emb.Message != testMessage || emb.SNR <= 0 {
		t.Fatalf("unexpected Embed result: %+v", emb)
	}
	if len(capturedBits) != 16 || capturedBits[0] != 1 || capturedBits[1] != 0 || capturedBits[15] != 1 {
		t.Fatalf("unexpected message bits sent to generator: %v", capturedBits)
	}

	detRes, err := backend.Detect(context.Background(), proto.WatermarkDetectParams{
		Model:     "audioseal-16bits",
		ModelPath: modelDir,
		Input:     wmPath,
	})
	if err != nil {
		t.Fatalf("AudioSeal Detect: %v", err)
	}
	if !detRes.Detected || detRes.Probability < 0.99 {
		t.Fatalf("expected Detected=true prob~1, got %+v", detRes)
	}
	if detRes.Message != testMessage {
		t.Fatalf("decoded message = %#x, want %#x", detRes.Message, testMessage)
	}
	if detRes.Confidence < 0.85 || len(detRes.Frames) != 2 {
		t.Fatalf("unexpected confidence/frames: conf=%.2f frames=%d", detRes.Confidence, len(detRes.Frames))
	}
}
