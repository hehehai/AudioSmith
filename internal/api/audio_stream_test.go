package api

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/resample"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
)

// TestFilePCMStreams: filePCM reads a chunk at a time (thread #150) and
// gives what the whole-buffer conversion gave.
func TestFilePCMStreams(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.wav")
	b := dsp.Buffer{Rate: 48000, Channels: 2, Samples: make([]float32, 2*70000)}
	for i := range b.Samples {
		b.Samples[i] = float32((i*7919)%2001-1000) / 1500
	}
	if err := wav.WriteFile(path, b, wav.Float32, wav.Options{}); err != nil {
		t.Fatal(err)
	}
	for _, rate := range []int{PCMRate, 0} {
		got, err := filePCM(path, rate)
		if err != nil {
			t.Fatal(err)
		}
		mono := dsp.Buffer{Rate: b.Rate, Channels: 1, Samples: make([]float32, b.Frames())}
		for f := range mono.Samples {
			mono.Samples[f] = (b.Samples[2*f] + b.Samples[2*f+1]) / 2
		}
		if rate > 0 {
			mono = resample.Buffer(mono, rate, resample.Good)
		}
		if want := floatsToPCM16(mono.Samples); !slices.Equal(got, want) {
			t.Errorf("rate %d: %d bytes, want %d", rate, len(got), len(want))
		}
	}
}
