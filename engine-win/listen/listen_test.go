package listen

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/engine-win/hw"
	"github.com/hehehai/AudioSmith/engine-win/server"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

type fakeBackend struct {
	devices []proto.CaptureDevice
	rate    int
	ch      int
}

func (f *fakeBackend) ListDevices(_ context.Context) ([]proto.CaptureDevice, error) {
	return f.devices, nil
}

func (f *fakeBackend) OpenStream(_ context.Context, deviceID string) (CaptureStream, error) {
	id := deviceID
	if id == "" && len(f.devices) > 0 {
		id = f.devices[0].ID
	}
	return &fakeStream{id: id, rate: f.rate, ch: f.ch}, nil
}

type fakeStream struct {
	id    string
	rate  int
	ch    int
	frame int
}

func (s *fakeStream) DeviceID() string { return s.id }
func (s *fakeStream) SampleRate() int  { return s.rate }
func (s *fakeStream) Channels() int    { return s.ch }
func (s *fakeStream) Close() error     { return nil }

func (s *fakeStream) Read(ctx context.Context) ([]float32, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(15 * time.Millisecond):
	}
	// 15 ms of 440 Hz sine wave.
	n := s.rate * 15 / 1000
	out := make([]float32, n*s.ch)
	for i := range n {
		v := float32(0.5 * math.Sin(2*math.Pi*440*float64(s.frame+i)/float64(s.rate)))
		for c := range s.ch {
			out[i*s.ch+c] = v
		}
	}
	s.frame += n
	return out, nil
}

type fakeAEC struct {
	called bool
}

func (a *fakeAEC) Process(samples []float32, _, _ int) []float32 {
	a.called = true
	return samples
}
func (a *fakeAEC) Close() error { return nil }

func TestListenRole_DevicesCaptureLevelAndStop(t *testing.T) {
	dir := t.TempDir()
	outWAV := filepath.Join(dir, "rec.wav")

	fb := &fakeBackend{
		rate: 48000,
		ch:   2,
		devices: []proto.CaptureDevice{
			{ID: "dev-1", Name: "USB Mic", Channels: 2, SampleRate: 48000, Default: true, Transport: "usb"},
		},
	}
	aec := &fakeAEC{}

	srv := server.New(server.Config{
		Role:     proto.RoleListen,
		Version:  "test",
		Hardware: hw.Probe(),
	})
	New(fb, func(rate, ch int) (AECProcessor, error) { return aec, nil }).Register(srv)

	pr, pw := io.Pipe()
	var outBuf bytes.Buffer
	var outMu sync.Mutex
	outW := &syncWriter{mu: &outMu, w: &outBuf}

	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(pr, outW)
	}()

	send := func(id, method string, params any) {
		m, err := proto.NewRequest(id, method, params)
		if err != nil {
			t.Fatal(err)
		}
		b, err := proto.Encode(m)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pw.Write(b); err != nil {
			t.Fatal(err)
		}
	}

	send("c1", proto.MethodHello, proto.HelloParams{Protocol: proto.Version, Role: proto.RoleListen})
	send("c2", proto.MethodCaptureDevices, nil)
	send("j1", proto.MethodCaptureStart, proto.CaptureParams{
		Device:     "dev-1",
		Channels:   1,
		SampleRate: 16000,
		Output:     proto.Output{Path: outWAV, Format: "wav", Bits: 16},
		AEC:        true,
	})
	time.Sleep(180 * time.Millisecond)
	send("c3", proto.MethodCaptureStop, proto.CaptureStopParams{Job: "j1"})
	send("c4", proto.MethodShutdown, nil)
	_ = pw.Close()

	if err := <-done; err != nil {
		t.Fatalf("Serve failed: %v", err)
	}

	var (
		levels  int
		devs    proto.DevicesResult
		capRes  proto.CaptureResult
		stopRes proto.CaptureStopResult
	)
	outMu.Lock()
	raw := outBuf.String()
	outMu.Unlock()
	for line := range strings.SplitSeq(strings.TrimSpace(raw), "\n") {
		m, err := proto.Decode([]byte(line))
		if err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		if m.Kind() == proto.KindNotification && m.Method == proto.MethodCaptureLevel {
			var lp proto.LevelParams
			if err := m.DecodeParams(&lp); err != nil {
				t.Fatalf("decode level: %v", err)
			}
			if lp.RMS <= -120 || lp.Peak <= -120 {
				t.Fatalf("expected non-silent meter levels, got %+v", lp)
			}
			levels++
		}
		if m.Kind() == proto.KindResponse {
			if m.Error != nil {
				t.Fatalf("error for %s: %v", m.ID, m.Error)
			}
			switch m.ID {
			case "c2":
				_ = m.DecodeResult(&devs)
			case "j1":
				_ = m.DecodeResult(&capRes)
			case "c3":
				_ = m.DecodeResult(&stopRes)
			}
		}
	}

	if len(devs.Devices) != 1 || devs.Devices[0].ID != "dev-1" {
		t.Fatalf("unexpected devices: %+v", devs)
	}
	if levels < 1 {
		t.Fatalf("expected at least 1 capture.level notification, got %d", levels)
	}
	if !stopRes.Stopped {
		t.Fatalf("expected capture.stop Stopped=true")
	}
	if capRes.Reason != proto.CaptureStopped || !capRes.AEC || !aec.called {
		t.Fatalf("unexpected capture result: %+v (aec.called=%v)", capRes, aec.called)
	}
	if capRes.SampleRate != 16000 || capRes.Channels != 1 || capRes.Frames == 0 {
		t.Fatalf("unexpected output audio: %+v", capRes)
	}

	wf, err := wav.Open(outWAV)
	if err != nil {
		t.Fatalf("open recorded wav: %v", err)
	}
	defer wf.Close()
	if wf.Format().Rate != 16000 || wf.Format().Channels != 1 {
		t.Fatalf("recorded wav format: %+v", wf.Format())
	}
	var _ json.RawMessage
}

type syncWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}
