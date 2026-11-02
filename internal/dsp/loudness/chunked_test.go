package loudness

import (
	"math"
	"runtime"
	"testing"

	"github.com/hehehai/AudioSmith/internal/dsp"
)

// TestMeasureChunked: Measure feeds the meter in chunks (code review
// 11-02) and gets what one whole write gets, allocating a small part of
// the buffer instead of several times it.
func TestMeasureChunked(t *testing.T) {
	b := dsp.Buffer{Rate: 48000, Channels: 2, Samples: make([]float32, 2*48000*60)}
	for i := range b.Samples {
		b.Samples[i] = float32(0.3 * math.Sin(float64(i/2)*0.07) * (1 + 0.5*math.Sin(float64(i)*1e-5)))
	}
	whole := NewMeter(b.Rate, b.Channels, Options{})
	whole.Write(b.Samples)
	want := whole.Stats()
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	got := Measure(b)
	runtime.ReadMemStats(&after)
	if got != want {
		t.Errorf("chunked %+v, whole %+v", got, want)
	}
	if alloc, size := after.TotalAlloc-before.TotalAlloc, uint64(len(b.Samples)*4); alloc > size/2 {
		t.Errorf("allocated %d MB for %d MB of samples", alloc>>20, size>>20)
	}
}
