package media

import (
	"bytes"
	"encoding/json"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hehehai/AudioSmith/engine-win/hw"
	"github.com/hehehai/AudioSmith/engine-win/server"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

func writeTestSineWAV(t *testing.T, path string, rate, ch int, seconds float64) {
	t.Helper()
	w, err := wav.Create(path, wav.Format{Rate: rate, Channels: ch, Encoding: wav.PCM16}, wav.Options{})
	if err != nil {
		t.Fatal(err)
	}
	frames := int(float64(rate) * seconds)
	samples := make([]float32, frames*ch)
	for i := range frames {
		v := float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/float64(rate)))
		for c := range ch {
			samples[i*ch+c] = v
		}
	}
	if err := w.Write(samples); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMediaRole_NativeWAVProbeAndDecodeRoundtrip(t *testing.T) {
	dir := t.TempDir()
	inWAV := filepath.Join(dir, "in.wav")
	outWAV := filepath.Join(dir, "out_16k_mono.wav")
	writeTestSineWAV(t, inWAV, 48000, 2, 0.5)

	srv := server.New(server.Config{
		Role:     proto.RoleMedia,
		Version:  "test",
		Hardware: hw.Probe(),
	})
	New(hw.Probe(), "", nil).Register(srv)

	reqs := []proto.Message{
		mustReq(t, "c1", proto.MethodHello, proto.HelloParams{Protocol: proto.Version, Role: proto.RoleMedia}),
		mustReq(t, "c2", proto.MethodMediaProbe, proto.ProbeParams{Input: inWAV}),
		mustReq(t, "j1", proto.MethodMediaDecode, proto.DecodeParams{
			Input:      inWAV,
			Output:     proto.Output{Path: outWAV, Format: "wav", Bits: 16},
			SampleRate: 16000,
			Channels:   1,
			Start:      0.1,
			Duration:   0.25,
		}),
		mustReq(t, "c3", proto.MethodShutdown, nil),
	}
	var in bytes.Buffer
	for _, r := range reqs {
		b, err := proto.Encode(r)
		if err != nil {
			t.Fatal(err)
		}
		in.Write(b)
	}

	var out bytes.Buffer
	if err := srv.Serve(&in, &out); err != nil {
		t.Fatalf("Serve failed: %v", err)
	}

	resByID := decodeResponses(t, out.String())
	var probe proto.ProbeResult
	c2 := resByID["c2"]
	if err := c2.DecodeResult(&probe); err != nil {
		t.Fatalf("decode probe: %v", err)
	}
	if !probe.HasAudio || probe.SampleRate != 48000 || probe.Channels != 2 {
		t.Fatalf("unexpected probe: %+v", probe)
	}

	var dec proto.DecodeResult
	j1 := resByID["j1"]
	if err := j1.DecodeResult(&dec); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if dec.SampleRate != 16000 || dec.Channels != 1 {
		t.Fatalf("unexpected decode output format: %+v", dec)
	}
	if math.Abs(dec.AudioSeconds-0.25) > 0.02 {
		t.Fatalf("AudioSeconds = %.3f, want ~0.25", dec.AudioSeconds)
	}

	// Verify the written file is a valid WAV.
	wf, err := wav.Open(outWAV)
	if err != nil {
		t.Fatalf("open output wav: %v", err)
	}
	defer wf.Close()
	if wf.Format().Rate != 16000 || wf.Format().Channels != 1 {
		t.Fatalf("output wav format = %+v", wf.Format())
	}
}

func TestSelectH264Encoder_iGPUFirstAndFallback(t *testing.T) {
	intelHW := hw.Info{
		GPUTier:          hw.TierDirectML,
		PreferredAdapter: &hw.Adapter{Name: "Intel Iris Xe", Vendor: "Intel"},
	}
	enc := SelectH264Encoder(func(name string) bool {
		return name == "h264_qsv" || name == "h264_mf" || name == "libx264"
	}, intelHW)
	if enc.Name != "h264_qsv" || !enc.Hardware {
		t.Fatalf("Intel iGPU got %+v, want h264_qsv", enc)
	}

	amdHW := hw.Info{
		GPUTier:          hw.TierDirectML,
		PreferredAdapter: &hw.Adapter{Name: "AMD Radeon", Vendor: "AMD"},
	}
	enc = SelectH264Encoder(func(name string) bool {
		return name == "h264_amf" || name == "h264_mf" || name == "libx264"
	}, amdHW)
	if enc.Name != "h264_amf" || !enc.Hardware {
		t.Fatalf("AMD iGPU got %+v, want h264_amf", enc)
	}

	// Everyday PC / ARM64 with only Media Foundation HW encoder.
	armHW := hw.Info{GPUTier: hw.TierCPU}
	enc = SelectH264Encoder(func(name string) bool {
		return name == "h264_mf" || name == "libx264"
	}, armHW)
	if enc.Name != "h264_mf" || !enc.Hardware {
		t.Fatalf("ARM64/MF got %+v, want h264_mf", enc)
	}

	// Software fallback when no hardware encoders exist.
	enc = SelectH264Encoder(func(name string) bool {
		return name == "libx264"
	}, armHW)
	if enc.Name != "libx264" || enc.Hardware {
		t.Fatalf("software fallback got %+v, want libx264", enc)
	}

	// Ultimate LGPL-safe fallback when even libx264 is absent.
	enc = SelectH264Encoder(func(string) bool { return false }, armHW)
	if enc.Name != "mpeg4" || enc.Hardware {
		t.Fatalf("ultimate fallback got %+v, want mpeg4", enc)
	}
}

func mustReq(t *testing.T, id, method string, params any) proto.Message {
	t.Helper()
	m, err := proto.NewRequest(id, method, params)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func decodeResponses(t *testing.T, raw string) map[string]proto.Message {
	t.Helper()
	res := map[string]proto.Message{}
	for line := range strings.SplitSeq(strings.TrimSpace(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		m, err := proto.Decode([]byte(line))
		if err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		if m.Kind() == proto.KindResponse {
			if m.Error != nil {
				t.Fatalf("unexpected error for %s: %v", m.ID, m.Error)
			}
			res[m.ID] = m
		}
	}
	var _ json.RawMessage
	return res
}
