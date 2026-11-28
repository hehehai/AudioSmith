package onnx

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

type mockSession struct {
	inputs   []string
	outputs  []string
	provider string
	runFn    func(inputs []Tensor, outputNames []string) ([]Tensor, error)
	closed   bool
}

func (m *mockSession) InputNames() []string  { return m.inputs }
func (m *mockSession) OutputNames() []string { return m.outputs }
func (m *mockSession) Provider() string      { return m.provider }
func (m *mockSession) Close() error          { m.closed = true; return nil }
func (m *mockSession) Run(inputs []Tensor, outputNames []string) ([]Tensor, error) {
	return m.runFn(inputs, outputNames)
}

func writeTestWAV(t *testing.T, path string, rate int, samples []float32) {
	t.Helper()
	w, err := wav.Create(path, wav.Format{Rate: rate, Channels: 1, Encoding: wav.PCM16}, wav.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Write(samples); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSileroVAD_V5AndResampling(t *testing.T) {
	dir := t.TempDir()
	wavPath := filepath.Join(dir, "speech48k.wav")

	// 1.5 seconds at 48 kHz: silence (0..0.3s), 440Hz tone (0.3..1.0s), silence (1.0..1.5s)
	const rate = 48000
	total := int(1.5 * rate)
	samples := make([]float32, total)
	for i := int(0.3 * rate); i < int(1.0*rate); i++ {
		samples[i] = 0.5 * float32(math.Sin(2*math.Pi*440*float64(i)/float64(rate)))
	}
	writeTestWAV(t, wavPath, rate, samples)

	sess := &mockSession{
		inputs:   []string{"input", "state", "sr"},
		outputs:  []string{"output", "stateN"},
		provider: "cpu",
		runFn: func(inputs []Tensor, outputNames []string) ([]Tensor, error) {
			if len(inputs) != 3 || inputs[0].Name != "input" || len(inputs[0].Float32) != sileroContextV5+sileroChunkSize {
				t.Fatalf("unexpected v5 inputs: %+v", inputs)
			}
			// Compute RMS of the 512-sample chunk to simulate speech probability.
			chunk := inputs[0].Float32[sileroContextV5:]
			var sum float64
			for _, s := range chunk {
				sum += float64(s * s)
			}
			rms := math.Sqrt(sum / float64(len(chunk)))
			prob := float32(0.05)
			if rms > 0.1 {
				prob = 0.92
			}
			return []Tensor{
				Float32Tensor("output", []int64{1, 1}, []float32{prob}),
				Float32Tensor("stateN", []int64{2, 1, 128}, make([]float32, 256)),
			}, nil
		},
	}

	vad := NewSileroVAD(sess)
	if !vad.isV5 {
		t.Fatalf("expected v5 detection")
	}
	res, err := vad.Detect(context.Background(), proto.VADParams{
		Model: "silero-vad",
		Audio: wavPath,
	})
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if res.AudioSeconds < 1.45 || res.AudioSeconds > 1.55 {
		t.Fatalf("unexpected AudioSeconds: %v", res.AudioSeconds)
	}
	if len(res.Regions) != 1 {
		t.Fatalf("expected 1 speech region, got %+v", res.Regions)
	}
	r := res.Regions[0]
	if r.Start > 0.35 || r.End < 0.95 || r.Prob < 0.8 {
		t.Fatalf("unexpected speech region: %+v", r)
	}
}

func TestSileroVAD_V4Layout(t *testing.T) {
	dir := t.TempDir()
	wavPath := filepath.Join(dir, "speech16k.wav")
	samples := make([]float32, 16000) // 1.0 s of tone
	for i := range samples {
		samples[i] = 0.4 * float32(math.Sin(2*math.Pi*300*float64(i)/16000))
	}
	writeTestWAV(t, wavPath, 16000, samples)

	sess := &mockSession{
		inputs:   []string{"input", "h", "c", "sr"},
		outputs:  []string{"output", "hn", "cn"},
		provider: "cpu",
		runFn: func(inputs []Tensor, _ []string) ([]Tensor, error) {
			if len(inputs) != 4 || inputs[1].Name != "h" || inputs[2].Name != "c" {
				t.Fatalf("unexpected v4 inputs: %+v", inputs)
			}
			return []Tensor{
				Float32Tensor("output", []int64{1, 1}, []float32{0.95}),
				Float32Tensor("hn", []int64{2, 1, 64}, make([]float32, 128)),
				Float32Tensor("cn", []int64{2, 1, 64}, make([]float32, 128)),
			}, nil
		},
	}

	vad := NewSileroVAD(sess)
	if vad.isV5 {
		t.Fatalf("expected v4 detection")
	}
	res, err := vad.Detect(context.Background(), proto.VADParams{Model: "silero-vad-v4", Audio: wavPath})
	if err != nil {
		t.Fatalf("Detect v4 failed: %v", err)
	}
	if len(res.Regions) != 1 || res.Regions[0].End < 0.9 {
		t.Fatalf("unexpected v4 regions: %+v", res.Regions)
	}
}

func TestKokoroG2PAndSynthesize(t *testing.T) {
	ipa := Phonemize("Hello Windows world! Testing audio 123.")
	if !strings.Contains(ipa, "həlˈO") || !strings.Contains(ipa, "wˈɪndOz") {
		t.Fatalf("unexpected IPA: %q", ipa)
	}
	ids := TokenizeKokoro(ipa, nil)
	if len(ids) < 10 || ids[0] != 0 || ids[len(ids)-1] != 0 {
		t.Fatalf("unexpected token IDs: %v", ids)
	}

	// Verify explicit IPA passthrough.
	if got := Phonemize("/həlˈO wˈɜld/"); got != "həlˈO wˈɜld" {
		t.Fatalf("IPA passthrough failed: %q", got)
	}

	// Build a synthetic voices.npz archive with one float32 .npy entry.
	dir := t.TempDir()
	npzPath := filepath.Join(dir, "voices-v1.0.bin")
	writeMockVoicesNPZ(t, npzPath, "af_heart.npy", 10)

	var seenStyle float32
	sess := &mockSession{
		inputs:   []string{"tokens", "style", "speed"},
		outputs:  []string{"audio"},
		provider: "directml",
		runFn: func(inputs []Tensor, _ []string) ([]Tensor, error) {
			if inputs[0].Name != "tokens" || inputs[1].Name != "style" || inputs[2].Name != "speed" {
				t.Fatalf("unexpected kokoro inputs: %+v", inputs)
			}
			seenStyle = inputs[1].Float32[0]
			// Return 0.25s of 24 kHz sine wave.
			pcm := make([]float32, 6000)
			for i := range pcm {
				pcm[i] = 0.25 * float32(math.Sin(2*math.Pi*220*float64(i)/24000))
			}
			return []Tensor{Float32Tensor("audio", []int64{1, int64(len(pcm))}, pcm)}, nil
		},
	}

	tts := NewKokoroTTS(sess, dir)
	outWAV := filepath.Join(dir, "out.wav")
	var chunks int
	n := testNotifier(func(method string, params any) error {
		if method == proto.MethodTTSChunk {
			chunks++
		}
		return nil
	})
	res, err := tts.Synthesize(context.Background(), "j-kokoro", proto.SynthesizeParams{
		Model:  "kokoro-82m",
		Text:   "Hello Windows world.",
		Voice:  "af_heart",
		Stream: true,
		Output: proto.Output{Path: outWAV, Format: "wav", Bits: 16},
	}, n)
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}
	if seenStyle == 0 {
		t.Fatalf("expected non-zero style vector loaded from voices-v1.0.bin")
	}
	if res.SampleRate != 24000 || res.Frames != 6000 || chunks == 0 {
		t.Fatalf("unexpected synthesize result: %+v chunks=%d", res, chunks)
	}
	if _, err := os.Stat(outWAV); err != nil {
		t.Fatalf("output WAV missing: %v", err)
	}
}

func TestDiscoverDLL_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	fakeDLL := filepath.Join(dir, "onnxruntime.dll")
	if err := os.WriteFile(fakeDLL, []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AUDIOSMITH_ONNXRUNTIME", fakeDLL)
	if got := DiscoverDLL(); got != fakeDLL {
		t.Fatalf("DiscoverDLL = %q, want %q", got, fakeDLL)
	}
}

func writeMockVoicesNPZ(t *testing.T, npzPath, entryName string, rows int) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fw, err := zw.Create(entryName)
	if err != nil {
		t.Fatal(err)
	}
	// Minimal NumPy v1.0 header
	hdr := "{'descr': '<f4', 'fortran_order': False, 'shape': (10, 1, 256), }          \n"
	_, _ = fw.Write([]byte{0x93, 'N', 'U', 'M', 'P', 'Y', 1, 0})
	_ = binary.Write(fw, binary.LittleEndian, uint16(len(hdr)))
	_, _ = fw.Write([]byte(hdr))
	for r := 0; r < rows; r++ {
		for c := 0; c < kokoroStyleDim; c++ {
			v := float32(r+1) * 0.01
			_ = binary.Write(fw, binary.LittleEndian, math.Float32bits(v))
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(npzPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

type testNotifier func(method string, params any) error

func (f testNotifier) Notify(method string, params any) error { return f(method, params) }
