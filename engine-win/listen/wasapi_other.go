//go:build !windows

package listen

import (
	"context"

	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

type stubBackend struct{}

func defaultBackend() CaptureBackend { return &stubBackend{} }

func (s *stubBackend) ListDevices(_ context.Context) ([]proto.CaptureDevice, error) {
	return []proto.CaptureDevice{}, nil
}

func (s *stubBackend) OpenStream(_ context.Context, _ string) (CaptureStream, error) {
	return nil, proto.Errorf(proto.CodeDeviceUnavailable, "WASAPI capture is only available on Windows")
}
