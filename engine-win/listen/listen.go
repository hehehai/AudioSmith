// Package listen implements the protocol-v2 "listen" role for Windows:
// audio input enumeration (capture.devices), recording (capture.start /
// capture.stop), 20 Hz input level metering (capture.level), WAV output via
// internal/dsp/wav, and a clean AECProcessor hook for Phase 1.5 WebRTC APM.
package listen

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hehehai/AudioSmith/engine-win/server"
	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/resample"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

// CaptureStream is an active audio input stream producing interleaved float32
// PCM frames at its native SampleRate and Channels.
type CaptureStream interface {
	DeviceID() string
	SampleRate() int
	Channels() int
	// Read blocks until audio frames are available, ctx is done, or an error
	// occurs (io.EOF or device lost).
	Read(ctx context.Context) ([]float32, error)
	Close() error
}

// CaptureBackend abstracts OS audio capture (WASAPI on Windows; fake in tests).
type CaptureBackend interface {
	ListDevices(ctx context.Context) ([]proto.CaptureDevice, error)
	OpenStream(ctx context.Context, deviceID string) (CaptureStream, error)
}

// AECProcessor is the Phase 1.5 hook for WebRTC APM (AEC3 + loopback
// reference). Returning nil from NewAECProcessor falls back cleanly to raw
// capture with proto.AECFallbackFailed.
type AECProcessor interface {
	Process(samples []float32, sampleRate, channels int) []float32
	Close() error
}

// AECFactory constructs an AECProcessor for a capture session.
type AECFactory func(sampleRate, channels int) (AECProcessor, error)

type activeCapture struct {
	stopCh chan struct{}
	doneCh chan struct{}
	once   sync.Once
}

func (a *activeCapture) requestStop() {
	a.once.Do(func() { close(a.stopCh) })
}

// Service implements the listen role RPC handlers.
type Service struct {
	backend    CaptureBackend
	aecFactory AECFactory

	mu      sync.Mutex
	actives map[string]*activeCapture
}

// New creates a listen Service. If backend is nil, the platform default
// backend (WASAPI on Windows) is used.
func New(backend CaptureBackend, aecFactory AECFactory) *Service {
	if backend == nil {
		backend = defaultBackend()
	}
	return &Service{
		backend:    backend,
		aecFactory: aecFactory,
		actives:    map[string]*activeCapture{},
	}
}

// Register attaches all listen role RPC handlers to s.
func (l *Service) Register(s *server.Server) {
	s.Handle(proto.MethodCaptureDevices, l.handleDevices)
	s.Handle(proto.MethodCaptureStart, l.handleStart)
	s.Handle(proto.MethodCaptureStop, l.handleStop)
}

func (l *Service) handleDevices(ctx context.Context, _ string, _ json.RawMessage, _ server.Notifier) (any, error) {
	devs, err := l.backend.ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	if devs == nil {
		devs = []proto.CaptureDevice{}
	}
	return proto.DevicesResult{Devices: devs}, nil
}

func (l *Service) handleStop(_ context.Context, _ string, raw json.RawMessage, _ server.Notifier) (any, error) {
	var p proto.CaptureStopParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, proto.Errorf(proto.CodeBadRequest, "invalid capture.stop params: %v", err)
	}
	l.mu.Lock()
	ac, ok := l.actives[p.Job]
	l.mu.Unlock()
	if !ok {
		return proto.CaptureStopResult{Stopped: false}, nil
	}
	ac.requestStop()
	// Wait briefly for capture.start to finalize and send its result first.
	select {
	case <-ac.doneCh:
	case <-time.After(5 * time.Second):
	}
	return proto.CaptureStopResult{Stopped: true}, nil
}

func (l *Service) handleStart(ctx context.Context, jobID string, raw json.RawMessage, n server.Notifier) (any, error) {
	var p proto.CaptureParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, proto.Errorf(proto.CodeBadRequest, "invalid capture.start params: %v", err)
	}
	if p.Output.Path == "" {
		return nil, proto.Errorf(proto.CodeBadRequest, "output.path is required")
	}
	outCh := p.Channels
	if outCh <= 0 {
		outCh = 1
	}
	if outCh > 2 {
		return nil, proto.Errorf(proto.CodeBadRequest, "channels must be 1 or 2, got %d", outCh)
	}

	stream, err := l.backend.OpenStream(ctx, p.Device)
	if err != nil {
		var perr *proto.Error
		if errors.As(err, &perr) {
			return nil, perr
		}
		data, _ := json.Marshal(map[string]string{"device": p.Device})
		return nil, &proto.Error{Code: proto.CodeDeviceUnavailable, Message: err.Error(), Data: data}
	}
	defer stream.Close()

	inRate := stream.SampleRate()
	inCh := stream.Channels()
	outRate := p.SampleRate
	if outRate <= 0 {
		outRate = inRate
	}

	// Optional Phase 1.5 AEC hook.
	var aecProc AECProcessor
	var aecOn bool
	var aecFallback string
	if p.AEC {
		if l.aecFactory != nil {
			if proc, err := l.aecFactory(inRate, outCh); err == nil && proc != nil {
				aecProc = proc
				aecOn = true
				defer aecProc.Close()
			} else {
				aecFallback = proto.AECFallbackFailed
			}
		} else {
			aecFallback = proto.AECFallbackFailed
		}
	}

	if err := os.MkdirAll(filepath.Dir(p.Output.Path), 0o755); err != nil {
		return nil, err
	}
	enc := wavEncoding(p.Output.Bits)
	writer, err := wav.Create(p.Output.Path, wav.Format{Rate: outRate, Channels: outCh, Encoding: enc}, wav.Options{})
	if err != nil {
		return nil, err
	}

	ac := &activeCapture{stopCh: make(chan struct{}), doneCh: make(chan struct{})}
	l.mu.Lock()
	l.actives[jobID] = ac
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		delete(l.actives, jobID)
		l.mu.Unlock()
		close(ac.doneCh)
	}()

	// Context for reading from the stream: stops on either capture.stop or job.cancel.
	readCtx, cancelRead := context.WithCancel(ctx)
	defer cancelRead()
	go func() {
		select {
		case <-ac.stopCh:
			cancelRead()
		case <-readCtx.Done():
		}
	}()

	var (
		totalOutFrames int64
		overallPeakLin float64
		totalClipped   int
		winSumSq       float64
		winSamples     int
		winPeakLin     float64
		winClipped     bool
		lastMeter      = time.Now()
		reason         = proto.CaptureStopped
	)

	const meterInterval = 50 * time.Millisecond // ~20 Hz

	for {
		samples, readErr := stream.Read(readCtx)
		if len(samples) > 0 {
			mixed := selectAndMix(samples, inCh, p.InputChannels, outCh)
			if aecProc != nil {
				mixed = aecProc.Process(mixed, inRate, outCh)
			}
			// Meter on captured signal.
			for _, s := range mixed {
				a := math.Abs(float64(s))
				winSumSq += a * a
				winSamples++
				if a > winPeakLin {
					winPeakLin = a
				}
				if a > overallPeakLin {
					overallPeakLin = a
				}
				if a >= 1.0 {
					winClipped = true
					totalClipped++
				}
			}

			outSamples := mixed
			if outRate != inRate {
				buf := resample.Buffer(dsp.Buffer{Rate: inRate, Channels: outCh, Samples: mixed}, outRate, resample.Good)
				outSamples = buf.Samples
			}

			// Enforce MaxSeconds by trimming final chunk if needed.
			if p.MaxSeconds > 0 {
				maxFrames := int64(math.Round(p.MaxSeconds * float64(outRate)))
				chunkFrames := int64(len(outSamples) / outCh)
				if totalOutFrames+chunkFrames >= maxFrames {
					keep := max(maxFrames-totalOutFrames, 0)
					outSamples = outSamples[:keep*int64(outCh)]
					if len(outSamples) > 0 {
						if err := writer.Write(outSamples); err != nil {
							writer.Abort()
							return nil, err
						}
						totalOutFrames += keep
					}
					reason = proto.CaptureMaxSeconds
					break
				}
			}

			if len(outSamples) > 0 {
				if err := writer.Write(outSamples); err != nil {
					writer.Abort()
					return nil, err
				}
				totalOutFrames += int64(len(outSamples) / outCh)
			}

			if n != nil && time.Since(lastMeter) >= meterInterval && winSamples > 0 {
				rms := math.Sqrt(winSumSq / float64(winSamples))
				_ = n.Notify(proto.MethodCaptureLevel, proto.LevelParams{
					Job:         jobID,
					Seconds:     float64(totalOutFrames) / float64(outRate),
					RMS:         linToDBFS(rms),
					Peak:        linToDBFS(winPeakLin),
					Clipped:     winClipped,
					AECFallback: aecFallback,
				})
				winSumSq, winSamples, winPeakLin, winClipped = 0, 0, 0, false
				lastMeter = time.Now()
			}
		}

		if readErr != nil {
			// Distinguish job.cancel vs capture.stop vs device lost.
			if ctx.Err() != nil {
				writer.Abort()
				return nil, proto.Errorf(proto.CodeCancelled, "capture %s cancelled", jobID)
			}
			select {
			case <-ac.stopCh:
				reason = proto.CaptureStopped
			default:
				reason = proto.CaptureDeviceLost
			}
			break
		}
	}

	if err := writer.Close(); err != nil {
		return nil, err
	}

	return proto.CaptureResult{
		AudioOut: proto.AudioOut{
			Output:       p.Output.Path,
			SampleRate:   outRate,
			Channels:     outCh,
			Frames:       totalOutFrames,
			AudioSeconds: float64(totalOutFrames) / float64(outRate),
		},
		Device:      stream.DeviceID(),
		Peak:        linToDBFS(overallPeakLin),
		Clipped:     totalClipped,
		Reason:      reason,
		AEC:         aecOn,
		AECFallback: aecFallback,
	}, nil
}

func wavEncoding(bits int) wav.Encoding {
	switch bits {
	case 24:
		return wav.PCM24
	case 32:
		return wav.Float32
	default:
		return wav.PCM16
	}
}

func linToDBFS(v float64) float64 {
	if v <= 1e-6 {
		return -120.0
	}
	db := 20.0 * math.Log10(v)
	if db < -120.0 {
		return -120.0
	}
	return db
}

func selectAndMix(in []float32, inCh int, inputChannels []int, outCh int) []float32 {
	if inCh <= 0 {
		return nil
	}
	frames := len(in) / inCh
	if len(inputChannels) == 0 && inCh == outCh {
		out := make([]float32, len(in))
		copy(out, in)
		return out
	}
	// Resolve 1-based input channel indices.
	var sel []int
	for _, ch := range inputChannels {
		if ch >= 1 && ch <= inCh {
			sel = append(sel, ch-1)
		}
	}
	if len(sel) == 0 {
		if outCh == 2 && inCh >= 2 {
			sel = []int{0, 1}
		} else {
			sel = []int{0}
		}
	}

	out := make([]float32, frames*outCh)
	if outCh == 1 {
		scale := float32(1.0 / float64(len(sel)))
		for i := range frames {
			base := i * inCh
			var sum float32
			for _, c := range sel {
				sum += in[base+c]
			}
			out[i] = sum * scale
		}
		return out
	}
	for i := range frames {
		base := i * inCh
		out[i*2] = in[base+sel[0]]
		if len(sel) > 1 {
			out[i*2+1] = in[base+sel[1]]
		} else {
			out[i*2+1] = in[base+sel[0]]
		}
	}
	return out
}
