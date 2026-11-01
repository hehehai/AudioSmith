package engine

// Engine requests that do not go through the job queue: quick control
// requests (media.probe, capture.devices) and microphone recordings, which
// are interactive (the user starts and stops them, a meter follows them)
// and must not wait behind queued jobs or be re-run after a restart.
//
// Errors are *jobs.Error with the protocol's codes, as a job's would be
// (CodeEngineUnavailable when the worker is not running, …), or the
// context's error.

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

// Probe describes a media file (media.probe on the media worker).
func Probe(ctx context.Context, sup Supervisor, path string) (proto.ProbeResult, error) {
	var res proto.ProbeResult
	return res, callDirect(ctx, sup, proto.MethodMediaProbe, proto.ProbeParams{Input: path}, &res)
}

// Devices lists the audio inputs, the system default first
// (capture.devices on the listen worker).
func Devices(ctx context.Context, sup Supervisor) ([]proto.CaptureDevice, error) {
	var res proto.DevicesResult
	err := callDirect(ctx, sup, proto.MethodCaptureDevices, proto.Empty{}, &res)
	return res.Devices, err
}

func callDirect(ctx context.Context, sup Supervisor, method string, params, result any) error {
	raw, err := sup.Call(ctx, proto.RoleOf(method), "", method, params, nil)
	if err != nil {
		return jobError(ctx, err)
	}
	return json.Unmarshal(raw, result)
}

// Recording is a microphone recording running on the listen worker
// (capture.start). Stop ends it and keeps the file; Cancel discards it.
type Recording struct {
	ID string // the capture.start request id

	sup     Supervisor
	cancel  context.CancelFunc
	started chan struct{}
	done    chan struct{}
	once    sync.Once
	res     proto.CaptureResult
	err     error
}

// StartCapture starts recording p and returns once the engine reports the
// device running (after macOS asked for microphone access, the first
// time), or with its error: CodePermissionDenied, CodeDeviceUnavailable…
// level receives the meter (capture.level, about 20 Hz) on the engine's
// reader goroutine: it must not block (post to the UI). ctx bounds the
// start only; the recording runs until Stop, Cancel, p.MaxSeconds, or the
// device going away (Done, Result). id "" picks a new ULID.
func StartCapture(ctx context.Context, sup Supervisor, id string, p proto.CaptureParams, level func(proto.LevelParams)) (*Recording, error) {
	if id == "" {
		id = core.NewID(time.Now())
	}
	rctx, cancel := context.WithCancel(context.Background())
	r := &Recording{ID: id, sup: sup, cancel: cancel, started: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer cancel()
		raw, err := sup.Call(rctx, proto.RoleListen, id, proto.MethodCaptureStart, p, func(m proto.Message) {
			switch m.Method {
			case proto.MethodJobProgress:
				var pp proto.ProgressParams
				if m.DecodeParams(&pp) == nil && pp.Stage == "recording" {
					r.once.Do(func() { close(r.started) })
				}
			case proto.MethodCaptureLevel:
				var lp proto.LevelParams
				if level != nil && m.DecodeParams(&lp) == nil {
					level(lp)
				}
			}
		})
		if err == nil {
			err = json.Unmarshal(raw, &r.res)
		} else {
			err = jobError(rctx, err)
		}
		r.err = err
		close(r.done)
	}()
	select {
	case <-r.started:
		return r, nil
	case <-r.done:
		if r.err != nil {
			return nil, r.err
		}
		return r, nil // it ended at once (maxSeconds): the result is there
	case <-ctx.Done():
		r.Cancel()
		return nil, ctx.Err()
	}
}

// Stop ends the recording and returns it (capture.stop, then the job's
// result). It is safe after the recording ended by itself.
func (r *Recording) Stop(ctx context.Context) (proto.CaptureResult, error) {
	select {
	case <-r.done:
	default:
		// An error other than ctx's (the worker died) is the job's too:
		// its call reports it.
		if _, err := r.sup.Call(ctx, proto.RoleListen, "", proto.MethodCaptureStop, proto.CaptureStopParams{Job: r.ID}, nil); err != nil && ctx.Err() != nil {
			return proto.CaptureResult{}, ctx.Err()
		}
	}
	select {
	case <-r.done:
		return r.res, r.err
	case <-ctx.Done():
		return proto.CaptureResult{}, ctx.Err()
	}
}

// Cancel discards the recording (job.cancel) and waits for it to end.
func (r *Recording) Cancel() {
	r.cancel()
	<-r.done
}

// Done is closed when the recording has ended (Result is then set).
func (r *Recording) Done() <-chan struct{} { return r.done }

// Result is the recording's outcome once Done is closed: the file, or the
// error (context.Canceled after Cancel).
func (r *Recording) Result() (proto.CaptureResult, error) {
	<-r.done
	return r.res, r.err
}
